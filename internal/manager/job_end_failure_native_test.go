package manager

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/childowner"
	"github.com/inferal-oss/arm/internal/model"
)

var endingFailureUnknownFixtures struct {
	sync.Mutex
	commands []*exec.Cmd
}

// This unrelated original group must survive every control request to the
// admitted job, even when its PID is deliberately substituted in the durable
// row. Its normal, positively joined exit is stronger than kill(pid, 0), which
// would also succeed for a killed but unreaped sentinel.
func endingFailureForeignGroup(t *testing.T) (int, func()) {
	t.Helper()
	if err := childowner.Available(); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", `IFS= read -r arm_foreign_token; [ "$arm_foreign_token" = go ]`)
	cmd.Stdin = reader
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		t.Fatal(err)
	}
	owner, err := childowner.New(cmd)
	if err != nil {
		// Closing the original input lets the private read fail. A missing
		// native binding grants no numeric-PID signal or fallback Wait.
		reader.Close()
		writer.Close()
		endingFailureUnknownFixtures.Lock()
		endingFailureUnknownFixtures.commands = append(endingFailureUnknownFixtures.commands, cmd)
		endingFailureUnknownFixtures.Unlock()
		t.Fatal(err)
	}
	reader.Close()
	joined := false
	t.Cleanup(func() {
		if !joined {
			_ = owner.Signal(syscall.SIGKILL)
			_ = writer.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result := owner.Wait(ctx)
			if !result.Reaped {
				t.Error("foreign original fixture did not join", result.CleanupError)
			}
		}
	})
	return cmd.Process.Pid, func() {
		t.Helper()
		if _, err := writer.Write([]byte("go\n")); err != nil {
			t.Fatal("foreign original group lost its own live input", err)
		}
		_ = writer.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		result := owner.Wait(ctx)
		joined = result.Reaped
		if !result.Reaped || result.WaitError != nil || result.CleanupError != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0 {
			t.Fatal("original job cancellation controlled the unrelated sentinel", result, cmd.ProcessState)
		}
	}
}

func TestSSHEndingJournalFailureCancelsOriginalGroup(t *testing.T) {
	for _, mode := range []string{"end", "close"} {
		t.Run(mode, func(t *testing.T) {
			m, err := Open(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
			if err != nil {
				t.Fatal(err)
			}
			var closeOnce sync.Once
			var closeErr error
			closeManager := func() error { closeOnce.Do(func() { closeErr = m.Close() }); return closeErr }
			t.Cleanup(func() { _ = closeManager() })
			// Keep the existing .5 CPU/64-byte SSH fixture claim. These are real
			// fresh native readings, not a CPU observer or admission override.
			awaitNativeFixtureHeadroom(t, m, model.Resources{CPU: .5, Memory: 64})
			foreignPID, finishForeign := endingFailureForeignGroup(t)
			f, actual, inputs, release := sshSettlementFixtureWithManager(t, m, false)
			defer release()
			var runtime *sshRuntime
			defer func() {
				// Failure cleanup retains the same original Manager-owned waiter;
				// it never calls cmd.Wait or signals a PID decoded from the row.
				if runtime != nil && runtime.leaseHold != nil && runtime.leaseHold.job != nil {
					_ = runtime.leaseHold.job.signalGroup(syscall.SIGKILL)
					runtime.cancel()
				}
				f.cancel()
				_ = f.client.Close()
			}()
			session, err := f.client.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			input, writer := io.Pipe()
			defer input.Close()
			defer writer.Close()
			session.Stdin = input
			fifo, ready := filepath.Join(f.q.CWD, "owned-block"), filepath.Join(f.q.CWD, "native-ready")
			if err := syscall.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			// No descendant or requested-work timer can terminate this group.
			// The shell ignores SSH stdin and TERM, and blocks on its own RDWR
			// FIFO. Only the original KILL escalation can produce the exit below.
			command := `trap '' TERM; exec 9<> ` + sshTestQuote(fifo) + `; printf ready > ` + sshTestQuote(ready) + `; IFS= read -r arm_never_sent <&9`
			if err := session.Start(command); err != nil {
				t.Fatal(err)
			}
			runtime = awaitSSHRuntime(t, actual)
			held := awaitSSHInput(t, inputs)
			eventually(t, func() bool { data, err := os.ReadFile(ready); return err == nil && string(data) == "ready" })
			j := runtime.leaseHold.job
			if j == nil || j.childOwner == nil || !j.childOwner.Active() || j.cmd.Process.Pid == foreignPID {
				t.Fatal("real original control precondition absent")
			}
			id := runtime.leaseHold.original.ID
			m.mu.Lock()
			prior, err := m.get(id)
			if err == nil && mode == "end" {
				// The exact current job capability, not this corrupted numeric
				// descriptor, remains the only target for cancellation.
				prior.PID = foreignPID
				err = m.put(prior, "test.foreign_pid_successor")
			}
			var priorRaw string
			if err == nil {
				err = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, id).Scan(&priorRaw)
			}
			if err == nil {
				_, err = m.db.Exec(`CREATE TRIGGER reject_original_ending BEFORE UPDATE OF data ON leases WHEN json_extract(NEW.data,'$.state')='ending' BEGIN SELECT RAISE(ABORT,'owned original ending journal refusal'); END`)
			}
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			type observedClose struct {
				err            error
				originalJoined bool
			}
			closed := make(chan observedClose, 1)
			closeAndObserve := func() {
				result := observedClose{err: closeManager()}
				select {
				case <-j.done:
					result.originalJoined = true
				default:
				}
				closed <- result
			}
			if mode == "end" {
				if _, err = m.End(id, "cancelled"); err == nil || !strings.Contains(err.Error(), "owned original ending journal refusal") {
					t.Fatal("original ending refusal was hidden", err)
				}
			} else {
				go closeAndObserve()
			}
			awaitTransportSignal(t, runtime.ctx.Done(), "ending refusal did not cancel original SSH IO")
			awaitTransportSignal(t, j.nativeDone, "ending refusal did not reach original KILL escalation and sole native join")
			if j.cmd.ProcessState == nil {
				t.Fatal("original native join remained unknown")
			}
			status, ok := j.cmd.ProcessState.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL || j.childOwner.Active() {
				t.Fatal("stdin-ignoring original child lacked its positive retired KILL join", j.cmd.ProcessState)
			}
			awaitTransportSignal(t, held.entered, "original SSH input did not reach actual EOF")
			awaitTransportSignal(t, runtime.leaseHold.ready, "native settlement did not reach its durable fence")
			m.mu.Lock()
			var current string
			err = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, id).Scan(&current)
			m.mu.Unlock()
			if err != nil || current != priorRaw {
				t.Fatal("failed ending transaction overwrote the prior charged row", err)
			}
			stored, err := m.Inspect(id)
			if err != nil || !consuming(stored.State) || stored.Ended != nil {
				t.Fatal("unjoined input lost its durable obligation", stored, err)
			}
			budget, err := m.Status()
			if err != nil || budget.Reserved.CPU != .5 || budget.Reserved.Memory != 64 {
				t.Fatal("ending refusal freed original resources before input join", budget.Reserved, err)
			}
			select {
			case <-j.done:
				t.Fatal("native exit falsely joined the held original SSH input")
			default:
			}
			if mode == "end" {
				go closeAndObserve()
			}
			// Manager.Close must have started before opening the original input
			// gate. Capture join state at its actual return, not after release.
			eventually(t, func() bool {
				m.mu.Lock()
				defer m.mu.Unlock()
				return m.closing
			})
			select {
			case result := <-closed:
				t.Fatal("Close returned before the real original input joined", result.err)
			default:
			}
			release()
			select {
			case result := <-closed:
				if result.err == nil || !result.originalJoined {
					t.Fatal("Close hid retained uncertainty or returned before its actual original join", result.err, result.originalJoined)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("Close waited on an uncancelled original child")
			}
			select {
			case <-j.done:
			default:
				t.Fatal("Close returned without original transport settlement join")
			}
			if runtime.leaseHold.err == nil {
				t.Fatal("failed ending/staging lost its sticky retention error")
			}
			finishForeign()
		})
	}
}
