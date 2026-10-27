package providerexec

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/providerproto"
)

func TestReadOnlyGroupObservationBoundAndErrors(t *testing.T) {
	for _, fault := range []error{nil, syscall.EPERM, syscall.EINTR} {
		calls := 0
		start := time.Now()
		if pollGroupAbsence(func() error { calls++; return fault }, 30*time.Millisecond) {
			t.Fatal("presence/error promoted to absence", fault)
		}
		if calls == 0 || time.Since(start) > time.Second {
			t.Fatal("observation bound", calls, time.Since(start))
		}
	}
	calls := 0
	if !pollGroupAbsence(func() error {
		calls++
		if calls == 1 {
			return nil
		}
		return syscall.ESRCH
	}, 100*time.Millisecond) || calls != 2 {
		t.Fatal("positive late absence not observed", calls)
	}
	for _, budget := range []time.Duration{0, -1, 2*time.Second + 1} {
		if pollGroupAbsence(func() error { t.Fatal("invalid bound probed group"); return nil }, budget) {
			t.Fatal("invalid bound accepted")
		}
	}
	if observeGroupAbsence(0) || observeGroupAbsence(-1) {
		t.Fatal("invalid group selector accepted")
	}
}

// A genuinely live group unrelated to the worker cannot be mistaken for its
// disappearance. The probe sends signal zero only; this test alone owns and
// kills its still-unreaped control process after proving it remains alive.
func TestReadOnlyGroupObservationRefusesLiveForeignControl(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "_fixture-group-anchor")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	readyPipe, ok := pipe.(*os.File)
	if !ok {
		t.Fatal("owned native readiness pipe is not a deadline-capable file")
	}
	if err = readyPipe.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal("owned readiness deadline unavailable", err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	line, err := bufio.NewReader(pipe).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatal("native control startup", line, err)
	}
	pid := cmd.Process.Pid
	if pollGroupAbsence(func() error { return syscall.Kill(-pid, 0) }, 30*time.Millisecond) {
		t.Fatal("unrelated live group claimed absent")
	}
	if err = cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("observation modified live control", err)
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait() // The expected signal exit is separately consumed exactly once.
	waited = true
	if !observeGroupAbsence(pid) {
		t.Fatal("reaped exact control group still visible")
	}
}

func TestActualGroupObservationPendingRetainsAdmissionAndUnknown(t *testing.T) {
	f := newFixture(t)
	gate := f.gate()
	f.owner.beforeGroupAbsence = gate
	p := f.prepared(t, "group-pending", "registration", providerproto.Validate, "")
	h := f.begin(t, p)
	f.grant(t, h)
	until := time.Now().Add(10 * time.Second)
	for !h.Snapshot().HelperWaitObserved && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	o := h.Snapshot()
	if !o.HelperWaitObserved || o.ScopedNativeClosed || o.GroupAbsentObserved {
		t.Fatal("unobserved group forged closure", o)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	_, err := h.WaitObservation(ctx)
	cancel()
	if err == nil {
		t.Fatal("caller timeout forged terminal native observation")
	}
	terminal, old, err := f.journal.terminal(p.attempt)
	if err != nil {
		t.Fatal(err)
	}
	milestone(&terminal, 5, "unknown")
	if err = f.journal.publish(p.attempt, terminal, old); err != nil {
		t.Fatal(err)
	}
	terminal, _, err = f.journal.terminal(p.attempt)
	if err != nil || terminal.Transitions[5].Phase != "unknown" {
		t.Fatal("uncertainty not durably retained", err)
	}
	var remaining int
	if err = f.journal.db.QueryRow(`SELECT remaining FROM worker_attempts WHERE id=?`, p.attempt).Scan(&remaining); err != nil || remaining <= 0 {
		t.Fatal("finalization reserve lost", remaining, err)
	}
	status, err := f.m.Status()
	if err != nil || status.Reserved.CPU != 1 || status.Reserved.Memory != 128<<20 || status.Reserved.Disk < 64<<20 {
		t.Fatal("pending group released ordinary admission", status.Reserved, err)
	}
	f.owner.mu.Lock()
	retained := f.owner.handles[p.attempt] == h
	f.owner.mu.Unlock()
	if !retained {
		t.Fatal("group observation lost owned handle")
	}
	close(gate)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	o, err = h.WaitObservation(ctx)
	if err != nil || !o.HelperWaitObserved || !o.GroupAbsentObserved || !o.ScopedNativeClosed {
		t.Fatal("actual late group observation unavailable", o, err)
	}
	f.settle(t, h)
}
