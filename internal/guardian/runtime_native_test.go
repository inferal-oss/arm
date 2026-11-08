package guardian

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/usage"
)

// TestNativeLadder runs the act-mode ladder against a real busy child in
// its own process group, under synthetic critical CPU pressure.
func TestNativeLadder(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "(while :; do :; done) & (while :; do :; done) & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	child, err := usage.Capture(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Mode: model.GuardianAct, Role: model.GuardianUnmanned, Interval: time.Second, TerminateGrace: time.Second,
		Ladder: []model.GuardianStepRule{rule(model.GuardianNotify, model.PressureHigh, 0), rule(model.GuardianDeprioritize, model.PressureHigh, 0), rule(model.GuardianFreeze, model.PressureHigh, 0), rule(model.GuardianTerminate, model.PressureHigh, 0)}, Cooldown: time.Millisecond}
	var notes []string
	r, err := New(cfg, t.TempDir(), Hooks{
		Pressure: func() (model.MonitorPressure, time.Time, bool, Host) {
			return pressure(model.PressureCritical), time.Now(), false, Host{CPUs: 4}
		},
		Notify: func(body string) error { notes = append(notes, body); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	r.desktop = nil
	// Only the child's group is visible, so other host processes are safe.
	r.collect = func(ctx context.Context) Inventory {
		inv := Collect(ctx)
		keep := inv.Processes[:0]
		for _, p := range inv.Processes {
			if p.Identity.PGID == child.PGID {
				keep = append(keep, p)
			}
		}
		inv.Processes = keep
		return inv
	}
	ctx := context.Background()
	step := func(want string) model.GuardianAction {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(1100 * time.Millisecond)
			r.tick(ctx)
			for _, a := range r.Status().Actions {
				if a.Step == want && a.Outcome != model.GuardianSkipped {
					return a
				}
			}
		}
		t.Fatalf("no %s action; actions %+v", want, r.Status().Actions)
		return model.GuardianAction{}
	}
	step(model.GuardianNotify)
	if os.Geteuid() == 0 {
		// Under root every process is a system process: nothing is targeted.
		time.Sleep(1100 * time.Millisecond)
		r.tick(ctx)
		for _, a := range r.Status().Actions {
			if len(a.Targets) > 0 {
				t.Fatalf("root guardian targeted %+v", a)
			}
		}
		return
	}
	dep := step(model.GuardianDeprioritize)
	if dep.Outcome != model.GuardianApplied || !dep.Reversible {
		t.Fatalf("deprioritize %+v", dep)
	}
	frz := step(model.GuardianFreeze)
	if frz.Outcome != model.GuardianApplied {
		t.Fatalf("freeze %+v", frz)
	}
	if state := processState(t, cmd.Process.Pid); !strings.HasPrefix(state, "T") {
		t.Fatalf("not stopped: %q", state)
	}
	// Operator undo resumes it.
	if _, err := r.Undo(frz.ID); err != nil {
		t.Fatal(err)
	}
	if state := processState(t, cmd.Process.Pid); strings.HasPrefix(state, "T") {
		t.Fatalf("still stopped after undo: %q", state)
	}
	if _, err := r.Undo(frz.ID); err == nil {
		t.Fatal("second undo accepted")
	}
	// The operator undo spares this group for the rest of the incident.
	for i := 0; i < 3; i++ {
		time.Sleep(1100 * time.Millisecond)
		r.tick(ctx)
	}
	if state := processState(t, cmd.Process.Pid); strings.HasPrefix(state, "T") {
		t.Fatalf("refrozen after operator undo: %q", state)
	}
	// A new incident can still terminate it.
	r.mu.Lock()
	// Freezing removes CPU use, so under CPU pressure a frozen group is no
	// longer the culprit; this incident goes straight to termination.
	second := r.cfg
	second.Ladder = []model.GuardianStepRule{rule(model.GuardianNotify, model.PressureHigh, 0), rule(model.GuardianTerminate, model.PressureHigh, 0)}
	r.engine = NewEngine(second)
	r.mu.Unlock()
	term := step(model.GuardianTerminate)
	if term.Outcome != model.GuardianApplied {
		t.Fatalf("terminate %+v", term)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("child survived termination")
	}
	if len(notes) == 0 {
		t.Fatal("no operator notification")
	}
	// The identity is gone: a stale action can no longer signal anything.
	if err := Signal(child, syscall.SIGCONT); err == nil {
		t.Fatal("signalled an exited identity")
	}
}

func processState(t *testing.T, pid int) string {
	t.Helper()
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestCollectSeesSelf(t *testing.T) {
	inv := Collect(context.Background())
	for _, p := range inv.Processes {
		if p.Identity.PID == os.Getpid() {
			if p.CPUSeconds == nil || p.Memory == nil || p.Executable == "" {
				t.Fatalf("self row incomplete: %+v", p)
			}
			wd, _ := os.Getwd()
			if got := workingDirectory(p.Identity.PID); got != wd {
				t.Fatalf("cwd %q want %q", got, wd)
			}
			return
		}
	}
	t.Fatalf("self not in inventory (%d processes, %s)", len(inv.Processes), inv.Detail)
}

// TestNativeFreezeSkipsStoppedAndCapsTargets checks that a process the
// user already stopped is neither frozen nor resumed by the guardian.
func TestNativeFreezeSkipsStopped(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	cmd.Process.Signal(syscall.SIGSTOP)
	time.Sleep(200 * time.Millisecond)
	id, _ := usage.Capture(cmd.Process.Pid)
	s, err := usage.Inspect(cmd.Process.Pid)
	if err != nil || !s.Stopped {
		t.Fatalf("stopped state not observed: %+v %v", s, err)
	}
	r, _ := New(Config{Mode: model.GuardianAct}, t.TempDir(), Hooks{})
	g := &Candidate{Group: &Group{ID: "g", Name: "sleep", Members: []Usage{{Process: Process{Identity: id, Stopped: true}}}}}
	r.execute(Decision{Step: model.GuardianFreeze, Resource: "cpu", Group: g, Targets: []Process{{Identity: id, Stopped: true}}, Execute: true}, time.Now())
	a := r.journal.Recent(1)[0]
	if a.Outcome != model.GuardianSkipped || len(a.Targets) != 0 {
		t.Fatalf("froze a user-stopped process: %+v", a)
	}
	// Crash recovery must not resume it either.
	if un := r.journal.Unreverted(); len(un) != 0 && len(un[0].Targets) != 0 {
		t.Fatalf("intent recorded a user-stopped process: %+v", un)
	}
	if state := processState(t, cmd.Process.Pid); !strings.HasPrefix(state, "T") {
		t.Fatalf("user-stopped process resumed: %q", state)
	}
}

func TestExecuteCapsTargets(t *testing.T) {
	r, _ := New(Config{Mode: model.GuardianObserve}, t.TempDir(), Hooks{})
	var targets []Process
	for i := 0; i < maxTargets+10; i++ {
		targets = append(targets, Process{Identity: model.ProcessIdentity{PID: 1 << 30, Start: strconv.Itoa(i)}})
	}
	r.execute(Decision{Step: model.GuardianFreeze, Resource: "cpu", Group: &Candidate{Group: &Group{ID: "g"}}, Targets: targets}, time.Now())
	if a := r.journal.Recent(1)[0]; len(a.Targets) != maxTargets {
		t.Fatalf("recorded %d targets", len(a.Targets))
	}
}
