package manager

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/model"
)

func TestForwardListenerCrashRecoveryAfterExclusiveProcessFence(t *testing.T) {
	if os.Getenv("ARM_FORWARD_CRASH_HELPER") == "1" {
		home := os.Getenv("ARM_FORWARD_CRASH_HOME")
		lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
		m, err := Open(home, model.Resources{CPU: 1, Memory: 16 << 20})
		if err != nil {
			t.Fatal(err)
		}
		cpuReading(m, 64, 64, 0, 64, 0)
		binding := model.ForwardSourceBinding{Workspace: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Service: "web", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: strings.Repeat("c", 64)}
		h, err := m.AcquireForwardListener(context.Background(), strings.Repeat("d", 32), "original crashed listener", binding, time.Minute, model.Resources{CPU: .0001, Memory: 1 << 20}, make(chan struct{}))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = os.Stdout.WriteString(h.LeaseID() + "\n")
		_ = os.Stdout.Sync()
		for {
			time.Sleep(time.Hour)
		}
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestForwardListenerCrashRecoveryAfterExclusiveProcessFence$")
	cmd.Env = append(os.Environ(), "ARM_FORWARD_CRASH_HELPER=1", "ARM_FORWARD_CRASH_HOME="+home)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	id, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("crashed helper did not admit listener: %v %s", err, stderr.String())
	}
	id = strings.TrimSpace(id)
	if len(id) != 32 {
		t.Fatalf("unexpected helper lease ID %q: %s", id, stderr.String())
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("original process lock still held: %v", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	m, err := Open(home, model.Resources{CPU: 1, Memory: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.Inspect(id)
	if err != nil || l.State != "needs_attention" || l.Outcome != "forward_listener_supervision_lost" {
		t.Fatalf("crashed owner incorrectly settled: %+v %v", l, err)
	}
	status, err := m.Status()
	if err != nil || status.Reserved.CPU < .0001 || status.Reserved.Memory < 1<<20 {
		t.Fatalf("lost crash charge: %+v %v", status.Reserved, err)
	}
	// A legacy TCP-only unresolved-port record remains eligible for the
	// original port probe once the exclusive process fence has been acquired.
	m.mu.Lock()
	l.Outcome = "legacy_port_cleanup_unresolved"
	l.Failure = "Port reclamation unresolved: prior probe unavailable"
	l.Revision++
	err = m.put(l, "forward.legacy_port_probe_fixture")
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	l, err = m.RecoverForwardListener(id, "exclusive daemon lock after original process exit")
	if err != nil || l.State != "terminal" || l.Outcome != "reconciled" || l.RecoveryReason == "" {
		t.Fatalf("exact source recovery failed: %+v %v", l, err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	source, err := database.OfflineHeld(t.Context(), home, lock)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	proof, err := database.ProveDatabaseSettled(t.Context(), source, l.Owner)
	if err != nil || !proof.Complete || !proof.Settled {
		t.Fatal("recovered source reset proof", proof, err)
	}
}
