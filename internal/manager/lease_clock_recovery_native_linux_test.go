//go:build linux

package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"getarm.org/internal/childowner"
	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
	"getarm.org/internal/usage"
)

// These tests use the actual Linux lease clock, with no clock, CPU observer or
// system-clock override. Close/reopen of bare allocations is a clean backend
// restart; the separately seeded process descriptors below are adversarial
// persisted data, not a claim to have crashed or recovered a managed child.
func recoveryNativeOpen(t *testing.T, home string) (*Manager, func()) {
	t.Helper()
	m, err := Open(home, model.Resources{CPU: 1, Memory: 2})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	closeManager := func() {
		t.Helper()
		if !closed {
			closed = true
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(closeManager)
	return m, closeManager
}

func recoveryNativeRequest(key string, ttl time.Duration) model.Request {
	return model.Request{Key: key, Reason: "native Linux recovery fixture", Resources: model.Resources{Memory: 2}, TTL: ttl}
}

func recoveryNativeGrant(t *testing.T, m *Manager, q model.Request) model.Lease {
	t.Helper()
	l, err := m.Acquire(q, nil)
	if err != nil || l.State != "active" || l.Clock == nil || l.Expires == nil {
		t.Fatal("native fixture was not admitted", l, err)
	}
	return l
}

func recoveryNativeSample(t *testing.T) leaseclock.Sample {
	t.Helper()
	s, err := leaseclock.Read()
	if err != nil || !s.Valid() {
		t.Fatal("native lease clock unavailable", s, err)
	}
	return s
}

// Poll the native deadline itself. The Go timer is only a bound on the test;
// elapsed expiry never follows from a sleep, wall timestamp or context timeout.
func recoveryNativeAwaitExpiry(t *testing.T, l model.Lease) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	last := l.Clock.Anchor
	for {
		s := recoveryNativeSample(t)
		if s.Boot != l.Clock.Boot || s.Tick < last {
			t.Fatal("native clock changed scope or regressed during fixture", s)
		}
		last = s.Tick
		remaining, err := l.Clock.Remaining(s, l.Expires)
		if err != nil {
			t.Fatal(err)
		}
		if remaining == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("native deadline did not expire within fixture bound", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func recoveryNativeUnchanged(t *testing.T, want, got model.Lease) {
	t.Helper()
	if !reflect.DeepEqual(got.Request, want.Request) || !reflect.DeepEqual(got.Clock, want.Clock) || !reflect.DeepEqual(got.Expires, want.Expires) {
		t.Fatal("recovery changed original request or elapsed/audit deadline", want, got)
	}
}

func recoveryNativeCharge(t *testing.T, m *Manager, want model.Resources) {
	t.Helper()
	s, err := m.Status()
	if err != nil || s.Reserved != want {
		t.Fatal("recovery reservation differs", s.Reserved, want, err)
	}
}

func recoveryNativeError(t *testing.T, err error, code string) {
	t.Helper()
	var e *model.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatal("unexpected recovery refusal", code, err)
	}
}

func TestLeaseClockNativeDeadlineRecoveryPreservesElapsedAuthority(t *testing.T) {
	for _, kind := range []string{"live", "expired"} {
		t.Run(kind, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "arm")
			m, closeManager := recoveryNativeOpen(t, home)
			ttl := time.Minute
			if kind == "expired" {
				ttl = time.Second
			}
			l := recoveryNativeGrant(t, m, recoveryNativeRequest("persisted", ttl))
			before, err := l.Clock.Remaining(recoveryNativeSample(t), l.Expires)
			if err != nil || before <= 0 {
				t.Fatal("deadline was not live before clean Close", before, err)
			}
			closeManager()
			if kind == "expired" {
				recoveryNativeAwaitExpiry(t, l)
			}
			// A second clean reopen proves that recovery does not restart the TTL
			// or silently replace an already terminal native deadline.
			for generation := 0; generation < 2; generation++ {
				m, closeManager = recoveryNativeOpen(t, home)
				got := clockGet(t, m, l.ID)
				recoveryNativeUnchanged(t, l, got)
				remaining, err := got.Clock.Remaining(recoveryNativeSample(t), got.Expires)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "live" {
					if got.State != "active" || got.Ended != nil || remaining <= 0 || remaining >= before {
						t.Fatal("clean restart refreshed or lost live elapsed authority", got, before, remaining)
					}
					before = remaining
					recoveryNativeCharge(t, m, l.Request.Resources)
				} else {
					if got.State != "terminal" || got.Outcome != "expired" || got.Ended == nil || remaining != 0 {
						t.Fatal("native expiry did not survive backend restart", got, remaining)
					}
					_, err = m.Renew(l.ID, time.Minute)
					recoveryNativeError(t, err, "lease_inactive")
					recoveryNativeUnchanged(t, l, clockGet(t, m, l.ID))
					recoveryNativeCharge(t, m, model.Resources{})
				}
				closeManager()
			}
		})
	}
}

// Mutate only a closed, private fixture database. Each row remains bound to its
// real physical key. No saved clock/PID is substituted into a running Manager.
func recoveryNativeStoredFixture(t *testing.T, home string, l model.Lease, checkpoint *string, run *RunRequest) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal("fixture did not update exactly its original row", n, err)
	}
	if checkpoint != nil {
		if _, err = db.Exec(`UPDATE metadata SET value=? WHERE key='lease_clock'`, *checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	if run != nil {
		raw, err = json.Marshal(run)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`UPDATE leases SET run=? WHERE id=?`, string(raw), l.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLeaseClockNativeRecoveryUncertainPersistedClockRetainsCharges(t *testing.T) {
	for _, kind := range []string{"other-boot-row", "legacy-row", "regressed-checkpoint", "malformed-checkpoint"} {
		t.Run(kind, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "arm")
			m, closeManager := recoveryNativeOpen(t, home)
			l := recoveryNativeGrant(t, m, recoveryNativeRequest("uncertain", time.Minute))
			closeManager()
			var checkpoint *string
			globalUncertain := false
			switch kind {
			case "other-boot-row":
				l.Clock.Boot += "-persisted-other-boot" // persisted-data control, no physical reboot
			case "legacy-row":
				l.Clock = nil
			case "regressed-checkpoint":
				s := recoveryNativeSample(t)
				s.Tick += int64(time.Hour) // future stored checkpoint, actual clock unchanged
				b, err := json.Marshal(s)
				if err != nil {
					t.Fatal(err)
				}
				raw := string(b)
				checkpoint = &raw
				globalUncertain = true
			case "malformed-checkpoint":
				raw := "{}"
				checkpoint = &raw
				globalUncertain = true
			}
			recoveryNativeStoredFixture(t, home, l, checkpoint, nil)
			for generation := 0; generation < 2; generation++ {
				m, closeManager = recoveryNativeOpen(t, home)
				m.tick()
				got := clockGet(t, m, l.ID)
				if got.State != "needs_attention" || got.RecoveryReason != "lease_clock_uncertain" || got.Ended != nil {
					t.Fatal("uncertain persisted deadline settled or regained authority", got)
				}
				recoveryNativeUnchanged(t, l, got)
				_, err := m.Renew(l.ID, 2*time.Minute)
				if globalUncertain {
					recoveryNativeError(t, err, "lease_clock_unavailable")
				} else {
					recoveryNativeError(t, err, "lease_inactive")
				}
				q := recoveryNativeRequest("new-authority", time.Minute)
				q.Resources.Memory = 1
				_, err = m.Acquire(q, nil)
				if globalUncertain {
					recoveryNativeError(t, err, "lease_clock_unavailable")
				} else {
					// Row-only uncertainty keeps its charge; it does not establish
					// a global native-clock read failure or waive capacity limits.
					recoveryNativeError(t, err, "insufficient_capacity")
				}
				_, err = m.End(l.ID, "cancelled")
				recoveryNativeError(t, err, "needs_attention")
				recoveryNativeUnchanged(t, l, clockGet(t, m, l.ID))
				recoveryNativeCharge(t, m, l.Request.Resources)
				if (m.leaseClockError != nil) != globalUncertain {
					t.Fatal("row uncertainty and global clock failure were conflated", m.leaseClockError)
				}
				if checkpoint != nil {
					var raw string
					if err := m.db.QueryRow(`SELECT value FROM metadata WHERE key='lease_clock'`).Scan(&raw); err != nil || raw != *checkpoint {
						t.Fatal("uncertain checkpoint was silently rebased", err)
					}
				}
				closeManager()
			}
		})
	}
}

// Retain uncertain original handles if a failed fixture cannot prove its join.
// No numeric-PID cleanup or replacement waiter is introduced on that path.
var recoveryNativeUnjoined struct {
	sync.Mutex
	owners   []*childowner.Owner
	commands []*exec.Cmd
}

func recoveryNativeRetainUnjoined(owner *childowner.Owner, cmd *exec.Cmd) {
	recoveryNativeUnjoined.Lock()
	defer recoveryNativeUnjoined.Unlock()
	recoveryNativeUnjoined.owners = append(recoveryNativeUnjoined.owners, owner)
	recoveryNativeUnjoined.commands = append(recoveryNativeUnjoined.commands, cmd)
}

func TestLeaseClockNativeRecoveredPIDIsNotCancellationAuthority(t *testing.T) {
	for _, kind := range []string{"generic", "ssh"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			// This child belongs solely to the test's retained original Owner.
			// Its TERM receipt plus normal FIFO exit detects signals that cannot
			// be inferred merely from continued PID existence.
			payload, command := checkpointNativePayload(t, dir)
			cmd := exec.Command("/bin/sh", "-c", command)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := childowner.Available(); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			owner, err := childowner.New(cmd)
			if err != nil {
				// Preserve the original command rather than signal/reap a numeric
				// PID without the required native binding.
				endingFailureUnknownFixtures.Lock()
				endingFailureUnknownFixtures.commands = append(endingFailureUnknownFixtures.commands, cmd)
				endingFailureUnknownFixtures.Unlock()
				t.Fatal(err)
			}
			waited := false
			t.Cleanup(func() {
				if !waited {
					_ = owner.Signal(syscall.SIGKILL)
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					result := owner.Wait(ctx)
					cancel()
					if !result.Reaped || result.CleanupError != nil {
						recoveryNativeRetainUnjoined(owner, cmd)
						t.Error("private sentinel cleanup did not positively join", result)
					}
				}
				if payload.writer != nil {
					_ = payload.writer.Close()
				}
			})
			payload.awaitReady(t)
			identity, err := usage.Capture(cmd.Process.Pid)
			if err != nil || identity.PID != cmd.Process.Pid || identity.PGID != cmd.Process.Pid || !owner.Active() {
				t.Fatal("original sentinel identity unavailable", identity, err)
			}
			home := filepath.Join(dir, "arm")
			m, closeManager := recoveryNativeOpen(t, home)
			q := recoveryNativeRequest("saved-descriptor", time.Second)
			q.Resources.CPU = .01
			awaitNativeFixtureHeadroom(t, m, q.Resources)
			l := recoveryNativeGrant(t, m, q)
			closeManager() // no Manager-owned child exists, so this is not a crash
			l.PID, l.Process = identity.PID, &identity
			marker := filepath.Join(dir, "forbidden-reexecution")
			run := &RunRequest{Request: l.Request, Command: []string{"/bin/sh", "-c", "printf replay >> " + sshTestQuote(marker)}}
			if kind == "ssh" {
				run.SSH = &model.SSHRunDescriptor{}
			}
			recoveryNativeStoredFixture(t, home, l, nil, run)
			recoveryNativeAwaitExpiry(t, l)
			for generation := 0; generation < 2; generation++ {
				m, closeManager = recoveryNativeOpen(t, home)
				m.tick()
				got := clockGet(t, m, l.ID)
				outcome := "supervision_lost"
				if kind == "ssh" {
					outcome = "ssh_supervision_lost"
				}
				if got.State != "needs_attention" || got.Outcome != outcome || got.Ended != nil || got.PID != identity.PID || !reflect.DeepEqual(got.Process, &identity) {
					t.Fatal("saved descriptor settled or changed original obligation", got)
				}
				recoveryNativeUnchanged(t, l, got)
				m.mu.Lock()
				adopted := m.jobs[l.ID] != nil || m.sshTransports[l.ID] != nil
				m.mu.Unlock()
				if adopted {
					t.Fatal("saved descriptor reconstructed process-local authority")
				}
				_, err = m.Renew(l.ID, time.Minute)
				recoveryNativeError(t, err, "lease_inactive")
				_, err = m.End(l.ID, "cancelled")
				recoveryNativeError(t, err, "needs_attention")
				_, err = m.Reconcile(l.ID)
				recoveryNativeError(t, err, "needs_attention")
				recoveryNativeCharge(t, m, q.Resources)
				recoveryNativeUnchanged(t, l, clockGet(t, m, l.ID))
				closeManager()
			}
			// Cover the existing 2s original-owner escalation window after the
			// last refused End/Close. This wait grants no saved-PID authority.
			select {
			case <-t.Context().Done():
				t.Fatal(t.Context().Err())
			case <-time.After(2300 * time.Millisecond):
			}
			if data, err := os.ReadFile(payload.terms); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("saved process data received TERM or receipt became unreadable", string(data), err)
			}
			if data, err := os.ReadFile(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("recovery replayed saved command or marker became unreadable", string(data), err)
			}
			current, err := usage.Capture(identity.PID)
			if err != nil || current != identity || !owner.Active() {
				t.Fatal("recovery disturbed unrelated original sentinel", current, err)
			}
			payload.finish(t)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			result := owner.Wait(ctx) // sole Wait on the original Owner
			waited = true
			cancel()
			if !result.Reaped {
				recoveryNativeRetainUnjoined(owner, cmd)
			}
			if !result.Reaped || result.WaitError != nil || result.CleanupError != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0 || owner.Active() {
				t.Fatal("unrelated original sentinel did not exit normally and join", result, cmd.ProcessState)
			}
			if data, err := os.ReadFile(payload.ready); err != nil || string(data) != "ready\n" {
				t.Fatal("sentinel execution count changed", string(data), err)
			}
			if data, err := os.ReadFile(payload.terms); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("saved descriptor caused a late TERM receipt", string(data), err)
			}
			if data, err := os.ReadFile(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("saved command replayed after original join", string(data), err)
			}
			if !errors.Is(syscall.Kill(identity.PID, 0), syscall.ESRCH) {
				t.Fatal("original sentinel PID remained after positive join")
			}
			if !errors.Is(syscall.Kill(-identity.PGID, 0), syscall.ESRCH) {
				t.Fatal("original sentinel process group remained after positive join")
			}
		})
	}
}
