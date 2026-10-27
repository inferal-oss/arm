package manager

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
	"golang.org/x/crypto/ssh"
)

const checkpointNativeRefusal = "native fixture checkpoint refusal"

type checkpointNativeCommand struct {
	fifo, ready, terms string
	writer             *os.File
}

// TERM is observed but cannot terminate this shell. Its private RDWR FIFO has
// no unrelated writer and does not depend on SSH stdin, EOF or a wall timer.
func checkpointNativePayload(t *testing.T, dir string) (*checkpointNativeCommand, string) {
	t.Helper()
	c := &checkpointNativeCommand{fifo: filepath.Join(dir, "owned-input"), ready: filepath.Join(dir, "executions"), terms: filepath.Join(dir, "term-receipts")}
	if err := syscall.Mkfifo(c.fifo, 0600); err != nil {
		t.Fatal(err)
	}
	trap := "printf 'term\\n' >> " + sshTestQuote(c.terms)
	command := "trap " + sshTestQuote(trap) + " TERM; exec 9<> " + sshTestQuote(c.fifo) + "; printf 'ready\\n' >> " + sshTestQuote(c.ready) + `; while :; do IFS= read -r arm_clock_token <&9 || continue; [ "$arm_clock_token" = finish ] && exit 0; done`
	return c, command
}

func (c *checkpointNativeCommand) awaitReady(t *testing.T) {
	t.Helper()
	eventually(t, func() bool { data, err := os.ReadFile(c.ready); return err == nil && string(data) == "ready\n" })
	var err error
	c.writer, err = os.OpenFile(c.fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0600)
	if err != nil {
		t.Fatal("original private FIFO writer unavailable", err)
	}
}

func (c *checkpointNativeCommand) finish(t *testing.T) {
	t.Helper()
	if _, err := c.writer.Write([]byte("finish\n")); err != nil {
		t.Fatal("an allegedly unsignalled original no longer accepts its own input", err)
	}
}

type checkpointNativePair struct {
	m             *Manager
	clock         *controlledLeaseClock
	generic, ssh  model.Lease
	genericJob    *job
	runtime       *sshRuntime
	genericCmd    *checkpointNativeCommand
	sshCmd        *checkpointNativeCommand
	fixture       *sshManagerFixture
	held          *heldSSHInputCompletion
	release       func()
	session       *ssh.Session
	input         *io.PipeReader
	inputWriter   *io.PipeWriter
	original      map[string]string
	restore       map[string]string
	checkpoint    string
	foreignPID    int
	finishForeign func()
	cleanupOnce   sync.Once
}

// Preserve original handles if failure cleanup itself becomes uncertain; a
// numeric PID or a second Wait is never substituted for their missing join.
var checkpointNativeQuarantine struct {
	sync.Mutex
	pairs []*checkpointNativePair
}

func newCheckpointNativePair(t *testing.T) *checkpointNativePair {
	t.Helper()
	c := &controlledLeaseClock{sample: leaseclock.Sample{Boot: "native-checkpoint-boot", Tick: int64(time.Hour), Wall: time.Now().UTC()}}
	m, err := openWithLeaseClock(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 2, Memory: 16 << 20, Disk: 16 << 20}, c.read)
	if err != nil {
		t.Fatal(err)
	}
	x := &checkpointNativePair{m: m, clock: c, original: map[string]string{}, restore: map[string]string{}}
	t.Cleanup(func() { x.cleanup(t) })
	x.foreignPID, x.finishForeign = endingFailureForeignGroup(t)
	var command string
	x.genericCmd, command = checkpointNativePayload(t, t.TempDir())
	q := model.Request{Key: "checkpoint-native-generic", Reason: "original native checkpoint failure fixture", Resources: model.Resources{CPU: .01, Memory: 64}, TTL: time.Minute}
	awaitNativeFixtureHeadroom(t, m, q.Resources)
	x.generic, err = m.Acquire(q, &RunRequest{Request: q, Command: []string{"/bin/sh", "-c", command}, CWD: filepath.Dir(x.genericCmd.fifo)})
	m.mu.Lock()
	x.genericJob = m.jobs[x.generic.ID]
	m.mu.Unlock()
	if err != nil || x.genericJob == nil {
		t.Fatal("native generic admission failed", err)
	}
	x.genericCmd.awaitReady(t)
	awaitNativeFixtureHeadroom(t, m, model.Resources{CPU: .5, Memory: 64})
	var actual <-chan *sshRuntime
	var inputs <-chan *heldSSHInputCompletion
	x.fixture, actual, inputs, x.release = sshSettlementFixtureWithManager(t, m, false)
	// Run this before the fixture's own server cleanup, including on the causal
	// negative path. The earlier registration still covers partial setup.
	t.Cleanup(func() { x.cleanup(t) })
	x.session, err = x.fixture.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	x.input, x.inputWriter = io.Pipe()
	x.session.Stdin = x.input
	x.sshCmd, command = checkpointNativePayload(t, x.fixture.q.CWD)
	if err := x.session.Start(command); err != nil {
		t.Fatal("native SSH admission failed", err)
	}
	x.runtime = awaitSSHRuntime(t, actual)
	x.held = awaitSSHInput(t, inputs)
	x.sshCmd.awaitReady(t)
	m.mu.Lock()
	x.generic, err = m.get(x.generic.ID)
	if err == nil {
		x.ssh, err = m.get(x.runtime.leaseHold.original.ID)
	}
	if err == nil {
		err = m.db.QueryRow(`SELECT value FROM metadata WHERE key='lease_clock'`).Scan(&x.checkpoint)
	}
	for _, l := range []model.Lease{x.generic, x.ssh} {
		if err == nil {
			var raw string
			err = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&raw)
			x.original[l.ID] = raw
		}
	}
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range x.jobs() {
		if j == nil || j.childOwner == nil || !j.childOwner.Active() || j.identity == nil || j.cmd.Process.Pid == x.foreignPID {
			t.Fatal("original live native capability was not captured")
		}
	}
	return x
}

func (x *checkpointNativePair) jobs() []*job {
	jobs := []*job{x.genericJob}
	if x.runtime != nil && x.runtime.leaseHold != nil {
		jobs = append(jobs, x.runtime.leaseHold.job)
	}
	return jobs
}

func (x *checkpointNativePair) cleanup(t *testing.T) {
	x.cleanupOnce.Do(func() {
		x.m.mu.Lock()
		_, _ = x.m.db.Exec(`DROP TRIGGER IF EXISTS refuse_native_clock_checkpoint`)
		_, _ = x.m.db.Exec(`DROP TRIGGER IF EXISTS refuse_native_clock_settlement`)
		for id, raw := range x.restore {
			_, _ = x.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, raw, id)
		}
		x.m.mu.Unlock()
		// Failure-only owner cleanup cannot satisfy any acceptance witness:
		// every test asserts cancellation (or its absence) before returning.
		for _, j := range x.jobs() {
			if j != nil && j.childOwner != nil && j.childOwner.Active() {
				_ = j.signalGroup(syscall.SIGKILL)
			}
		}
		if x.runtime != nil {
			x.runtime.cancel()
		}
		if x.release != nil {
			x.release()
		}
		if x.inputWriter != nil {
			_ = x.inputWriter.Close()
			_ = x.input.Close()
		}
		if x.session != nil {
			_ = x.session.Close()
		}
		if x.fixture != nil {
			x.fixture.cancel()
			_ = x.fixture.client.Close()
		}
		joined := true
		for _, j := range x.jobs() {
			if j == nil {
				continue
			}
			select {
			case <-j.done:
				if !checkpointNativeCleanupReaped(j) {
					joined = false
					t.Error("closed fixture completion lacked a positive original native reap")
				}
			case <-time.After(4 * time.Second):
				joined = false
				t.Error("original native fixture cleanup remained unjoined")
			}
		}
		for _, c := range []*checkpointNativeCommand{x.genericCmd, x.sshCmd} {
			if c != nil && c.writer != nil {
				_ = c.writer.Close()
			}
		}
		if !joined {
			checkpointNativeQuarantine.Lock()
			checkpointNativeQuarantine.pairs = append(checkpointNativeQuarantine.pairs, x)
			checkpointNativeQuarantine.Unlock()
			return // no unbounded Close or replacement native Wait
		}
		_ = x.m.Close() // a retained settlement error is expected in fault cases
	})
}

// Completion channels also close on unknown native supervision. Require the
// native channel and atomic reap publication before reading ProcessState, then
// observe absence of this captured original group. No numeric PID grants control.
func checkpointNativeCleanupReaped(j *job) bool {
	select {
	case <-j.nativeDone:
	default:
		return false
	}
	if !j.cpuExited.Load() || j.cmd == nil || j.cmd.Process == nil || j.childOwner == nil {
		return false
	}
	return j.cmd.ProcessState != nil && !j.childOwner.Active() && errors.Is(syscall.Kill(-j.cmd.Process.Pid, 0), syscall.ESRCH)
}

func (x *checkpointNativePair) refuse(t *testing.T, settlement bool) {
	t.Helper()
	x.m.mu.Lock()
	defer x.m.mu.Unlock()
	_, err := x.m.db.Exec(`CREATE TRIGGER refuse_native_clock_checkpoint BEFORE UPDATE ON metadata WHEN NEW.key='lease_clock' BEGIN SELECT RAISE(ABORT,'native fixture checkpoint refusal'); END`)
	if err == nil && settlement {
		_, err = x.m.db.Exec(`CREATE TRIGGER refuse_native_clock_settlement BEFORE UPDATE OF data ON leases BEGIN SELECT RAISE(ABORT,'native fixture settlement refusal'); END`)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func (x *checkpointNativePair) reconcile() error {
	x.m.mu.Lock()
	defer x.m.mu.Unlock()
	return x.m.reconcileLeaseExpiries()
}

func (x *checkpointNativePair) expire(t *testing.T) {
	t.Helper()
	x.m.mu.Lock()
	x.clock.sample.Tick = max(x.generic.Clock.End, x.ssh.Clock.End) + 1
	x.m.mu.Unlock()
	x.assertWallAndContextLive(t)
}

func (x *checkpointNativePair) assertWallAndContextLive(t *testing.T) {
	t.Helper()
	until, ok := x.runtime.ctx.Deadline()
	if !ok || time.Until(until) < 15*time.Second || !time.Now().Before(*x.generic.Expires) || !time.Now().Before(*x.ssh.Expires) || x.runtime.ctx.Err() != nil || t.Context().Err() != nil {
		t.Fatal("fixture wall or Go context expired before the controlled elapsed cut")
	}
}

func (x *checkpointNativePair) assertRows(t *testing.T) {
	t.Helper()
	x.m.mu.Lock()
	defer x.m.mu.Unlock()
	for id, want := range x.original {
		var got string
		if err := x.m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, id).Scan(&got); err != nil || got != want {
			t.Fatal("failed checkpoint changed the prior raw charged row", err)
		}
	}
	var checkpoint string
	if err := x.m.db.QueryRow(`SELECT value FROM metadata WHERE key='lease_clock'`).Scan(&checkpoint); err != nil || checkpoint != x.checkpoint {
		t.Fatal("refused checkpoint advanced durable elapsed authority", err)
	}
}

func checkpointNativeJoined(t *testing.T, j *job, killed bool) {
	t.Helper()
	awaitTransportSignal(t, j.nativeDone, "original native cancellation did not reach its sole native Wait")
	if j.cmd.ProcessState == nil || j.childOwner.Active() || !j.cpuExited.Load() {
		t.Fatal("original native terminal observation or retirement is missing")
	}
	status, ok := j.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || (killed && (!status.Signaled() || status.Signal() != syscall.SIGKILL)) || (!killed && (!status.Exited() || status.ExitStatus() != 0)) {
		t.Fatal("original native join had the wrong cause", j.cmd.ProcessState)
	}
	// Observation only, from the captured original command after retirement.
	// No signal/reap authority is reconstructed from the durable row.
	eventually(t, func() bool { return errors.Is(syscall.Kill(-j.cmd.Process.Pid, 0), syscall.ESRCH) })
}

func (x *checkpointNativePair) joinTransport(t *testing.T) {
	t.Helper()
	x.release()
	awaitTransportSignal(t, x.fixture.serverDone, "original Serve connection did not join")
	for _, done := range []<-chan struct{}{x.runtime.inputDone, x.runtime.outputDone, x.runtime.closeDone, x.runtime.transportJoined, x.runtime.leaseHold.done, x.runtime.leaseHold.job.done} {
		awaitTransportSignal(t, done, "original SSH copy/callback/transport settlement did not join")
	}
}

func (x *checkpointNativePair) noSignals(t *testing.T) {
	t.Helper()
	x.assertWallAndContextLive(t)
	// Cover the production escalation interval too. A hidden KILL request
	// cannot pass merely because this shell records but ignores TERM.
	until := time.NewTimer(2300 * time.Millisecond)
	defer until.Stop()
	select {
	case <-x.runtime.ctx.Done():
		t.Fatal("unproven elapsed deadline cancelled the original SSH runtime")
	case <-x.genericJob.nativeDone:
		t.Fatal("unproven elapsed deadline terminated the original generic group")
	case <-x.runtime.leaseHold.job.nativeDone:
		t.Fatal("unproven elapsed deadline terminated the original SSH group")
	case <-until.C:
	}
	for _, c := range []*checkpointNativeCommand{x.genericCmd, x.sshCmd} {
		if data, err := os.ReadFile(c.terms); !errors.Is(err, os.ErrNotExist) || len(data) != 0 {
			t.Fatal("unproven elapsed deadline signalled an original native group", err)
		}
	}
	for _, j := range x.jobs() {
		if !j.childOwner.Active() {
			t.Fatal("unproven elapsed deadline retired an original live owner")
		}
	}
}

func (x *checkpointNativePair) finishUnsignalled(t *testing.T) {
	t.Helper()
	x.genericCmd.finish(t)
	x.sshCmd.finish(t)
	checkpointNativeJoined(t, x.genericJob, false)
	checkpointNativeJoined(t, x.runtime.leaseHold.job, false)
	x.joinTransport(t)
	x.finishForeign()
}

func TestLeaseClockCheckpointFailureCancelsOriginalNativeJobs(t *testing.T) {
	for _, driver := range []string{"reconcile", "tick"} {
		for _, settlement := range []bool{false, true} {
			name := "checkpoint-only"
			if settlement {
				name = "checkpoint-and-settlement"
			}
			t.Run(driver+"/"+name, func(t *testing.T) {
				x := newCheckpointNativePair(t)
				x.refuse(t, settlement)
				x.expire(t)
				if driver == "tick" {
					x.m.tick()
				}
				if err := x.reconcile(); err == nil || !strings.Contains(err.Error(), checkpointNativeRefusal) {
					t.Fatal("checkpoint refusal was not preserved", err)
				}
				x.assertRows(t) // before the two-second native escalation/Wait
				awaitTransportSignal(t, x.runtime.ctx.Done(), "checkpoint refusal did not cancel original SSH IO")
				for i := 0; i < 3; i++ {
					x.m.tick()
					time.Sleep(100 * time.Millisecond)
				}
				for _, j := range x.jobs() {
					checkpointNativeJoined(t, j, true)
				}
				awaitTransportSignal(t, x.genericJob.done, "original generic waiter did not join its final journal attempt")
				awaitTransportSignal(t, x.held.entered, "original SSH input did not reach actual EOF")
				awaitTransportSignal(t, x.runtime.leaseHold.ready, "native SSH result did not reach settlement fence")
				for _, c := range []*checkpointNativeCommand{x.genericCmd, x.sshCmd} {
					if data, err := os.ReadFile(c.terms); err != nil || string(data) != "term\n" {
						t.Fatal("repeated refused ticks did not request exactly one original TERM", string(data), err)
					}
				}
				if settlement {
					x.assertRows(t)
				}
				stored, err := x.m.Inspect(x.ssh.ID)
				budget, budgetErr := x.m.Status()
				if err != nil || !consuming(stored.State) || stored.Ended != nil || budgetErr != nil || budget.Reserved.CPU < .5 || budget.Reserved.Memory < 64 {
					t.Fatal("held original IO lost its charged obligation", err, budgetErr, budget.Reserved)
				}
				select {
				case <-x.runtime.leaseHold.job.done:
					t.Fatal("native exit falsely joined the held original input")
				default:
				}
				x.joinTransport(t)
				if settlement {
					x.assertRows(t)
				} else {
					final, err := x.m.Inspect(x.ssh.ID)
					if err != nil || final.State != "terminal" || final.Ended == nil {
						t.Fatal("positive original native and IO joins did not settle", err)
					}
				}
				x.m.mu.Lock()
				_, err = x.m.db.Exec(`DROP TRIGGER refuse_native_clock_checkpoint`)
				if err == nil && settlement {
					_, err = x.m.db.Exec(`DROP TRIGGER refuse_native_clock_settlement`)
				}
				x.m.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				if err := x.reconcile(); err != nil {
					t.Fatal("ordinary post-refusal reconciliation failed", err)
				}
				for _, original := range []model.Lease{x.generic, x.ssh} {
					if _, err := x.m.Renew(original.ID, 2*time.Minute); err == nil {
						t.Fatal("expired original deadline was rebased after trigger removal")
					}
					current, err := x.m.Inspect(original.ID)
					if err != nil || !reflect.DeepEqual(current.Clock, original.Clock) || current.Expires == nil || !current.Expires.Equal(*original.Expires) {
						t.Fatal("trigger removal replayed or rebased an original lease", err)
					}
				}
				for _, c := range []*checkpointNativeCommand{x.genericCmd, x.sshCmd} {
					if data, err := os.ReadFile(c.ready); err != nil || string(data) != "ready\n" {
						t.Fatal("post-refusal reconciliation replayed requested work", err)
					}
				}
				x.finishForeign()
			})
		}
	}
}

func TestLeaseClockCheckpointFailureLeavesUnprovenNativeOwnersLive(t *testing.T) {
	for _, kind := range []string{"below-deadline", "read-error", "regressed", "other-boot", "missing-deadline", "list-decode-error", "post-refusal-read-error", "post-refusal-regressed", "post-refusal-other-boot"} {
		t.Run(kind, func(t *testing.T) {
			x := newCheckpointNativePair(t)
			func() {
				x.m.mu.Lock()
				defer x.m.mu.Unlock()
				x.clock.sample.Tick = max(x.generic.Clock.End, x.ssh.Clock.End) + 1
				switch kind {
				case "below-deadline":
					x.clock.sample.Tick = min(x.generic.Clock.End, x.ssh.Clock.End) - 1
				case "read-error":
					x.clock.err = errors.New("native clock read refused")
				case "regressed":
					x.clock.sample.Tick = x.m.leaseClockLast.Tick - 1
				case "other-boot":
					x.clock.sample.Boot = "different-boot"
				case "missing-deadline", "list-decode-error":
					for id, raw := range x.original {
						x.restore[id] = raw
						changed := "{"
						if kind == "missing-deadline" {
							var l model.Lease
							if err := json.Unmarshal([]byte(raw), &l); err != nil {
								t.Fatal(err)
							}
							l.Clock = nil
							data, err := json.Marshal(l)
							if err != nil {
								t.Fatal(err)
							}
							changed = string(data)
						}
						if _, err := x.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, changed, id); err != nil {
							t.Fatal(err)
						}
					}
				default:
					calls := 0
					valid := x.clock.sample
					x.m.leaseClockRead = func() (leaseclock.Sample, error) {
						calls++
						if calls == 1 {
							return valid, nil
						}
						s := valid
						switch kind {
						case "post-refusal-read-error":
							return leaseclock.Sample{}, errors.New("post-refusal native clock read refused")
						case "post-refusal-regressed":
							s.Tick--
						case "post-refusal-other-boot":
							s.Boot = "post-refusal-other-boot"
						}
						return s, nil
					}
				}
			}()
			x.refuse(t, true)
			if err := x.reconcile(); err == nil {
				t.Fatal("unproven/refused elapsed reconciliation was accepted")
			}
			x.noSignals(t)
			x.finishUnsignalled(t)
		})
	}
}

func TestLeaseClockCheckpointFailureChangedBindingDoesNotStarveOriginalSibling(t *testing.T) {
	for _, kind := range []string{"pid", "process", "owner", "request", "id"} {
		t.Run(kind, func(t *testing.T) {
			x := newCheckpointNativePair(t)
			var err error
			func() {
				x.m.mu.Lock()
				defer x.m.mu.Unlock()
				var changed model.Lease
				if err := json.Unmarshal([]byte(x.original[x.generic.ID]), &changed); err != nil {
					t.Fatal(err)
				}
				x.restore[x.generic.ID] = x.original[x.generic.ID]
				switch kind {
				case "pid":
					changed.PID = x.foreignPID
				case "process":
					copy := *changed.Process
					copy.Start += "-changed"
					changed.Process = &copy
				case "owner":
					changed.Owner += "-changed"
				case "request":
					changed.Request.Reason += " changed"
				case "id":
					changed.ID = "unregistered-physical-alias"
				}
				data, marshalErr := json.Marshal(changed)
				err = marshalErr
				if err == nil {
					_, err = x.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(data), x.generic.ID)
				}
			}()
			if err != nil {
				t.Fatal(err)
			}
			x.refuse(t, true)
			x.expire(t)
			if err := x.reconcile(); err == nil || !strings.Contains(err.Error(), checkpointNativeRefusal) {
				t.Fatal("original checkpoint failure was lost", err)
			}
			awaitTransportSignal(t, x.runtime.ctx.Done(), "invalid first binding starved the independent expired original SSH owner")
			checkpointNativeJoined(t, x.runtime.leaseHold.job, true)
			if !x.genericJob.childOwner.Active() {
				t.Fatal("changed physical/native binding controlled its prior original")
			}
			if _, err := os.Stat(x.genericCmd.terms); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("changed binding signalled the original generic group", err)
			}
			x.joinTransport(t)
			x.genericCmd.finish(t)
			checkpointNativeJoined(t, x.genericJob, false)
			x.finishForeign()
		})
	}
}

func TestLeaseClockCheckpointFailureRejectsPhysicalIDAlias(t *testing.T) {
	x := newCheckpointNativePair(t)
	awaitNativeFixtureHeadroom(t, x.m, model.Resources{CPU: .5, Memory: 128})
	renewed, err := x.m.Renew(x.ssh.ID, 2*time.Minute)
	if err != nil {
		t.Fatal("legitimate later original SSH deadline was not granted", err)
	}
	x.m.mu.Lock()
	// All native/process/request/owner fields name the other registered job,
	// but only this wrong physical row carries the forged expired deadline.
	alias := renewed
	alias.Clock = x.generic.Clock
	alias.Expires = x.generic.Expires
	data, err := json.Marshal(alias)
	if err == nil {
		x.restore[x.generic.ID] = x.original[x.generic.ID]
		_, err = x.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(data), x.generic.ID)
	}
	x.clock.sample.Tick = x.generic.Clock.End + 1
	x.m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	x.refuse(t, true)
	if err := x.reconcile(); err == nil || !strings.Contains(err.Error(), checkpointNativeRefusal) {
		t.Fatal("physical alias lost original checkpoint refusal", err)
	}
	x.noSignals(t)
	x.finishUnsignalled(t)
}

// FAIL retains preceding changes in the failing statement. The explicit raw
// readbacks below establish that this fixture reached the post-refusal cut.
// https://www.sqlite.org/lang_conflict.html
func TestLeaseClockCheckpointFailureFreshRosterDecodeRefusesAllNativeSignals(t *testing.T) {
	x := newCheckpointNativePair(t)
	func() {
		x.m.mu.Lock()
		defer x.m.mu.Unlock()
		rows, err := x.m.list()
		if err != nil || len(rows) != 2 || rows[0].ID != x.generic.ID || rows[1].ID != x.ssh.ID {
			t.Fatal("pre-refusal complete roster was not valid and ordered", err)
		}
		x.restore[x.ssh.ID] = x.original[x.ssh.ID]
		// This trigger changes only the later physical row. There is no lease
		// settlement-denial trigger: it would suppress the intended SQL fault.
		id := strings.ReplaceAll(x.ssh.ID, "'", "''")
		_, err = x.m.db.Exec(`CREATE TRIGGER refuse_native_clock_checkpoint BEFORE UPDATE ON metadata WHEN NEW.key='lease_clock' BEGIN UPDATE leases SET data='{' WHERE id='` + id + `'; SELECT RAISE(FAIL,'native fixture checkpoint refusal'); END`)
		if err != nil {
			t.Fatal(err)
		}
	}()
	x.expire(t)
	if err := x.reconcile(); err == nil || !strings.Contains(err.Error(), checkpointNativeRefusal) {
		t.Fatal("fresh-list refusal lost the original checkpoint error", err)
	}
	func() {
		x.m.mu.Lock()
		defer x.m.mu.Unlock()
		for id, want := range map[string]string{x.generic.ID: x.original[x.generic.ID], x.ssh.ID: "{"} {
			var raw string
			if err := x.m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, id).Scan(&raw); err != nil || raw != want {
				t.Fatal("post-refusal list-decode fault was not established", err)
			}
		}
		var checkpoint string
		if err := x.m.db.QueryRow(`SELECT value FROM metadata WHERE key='lease_clock'`).Scan(&checkpoint); err != nil || checkpoint != x.checkpoint {
			t.Fatal("post-refusal list fault advanced its denied checkpoint", err)
		}
	}()
	// In particular, the valid first expired owner must not be acted upon
	// before the entire fresh keyed roster has been decoded successfully.
	x.noSignals(t)
	x.finishUnsignalled(t)
}
