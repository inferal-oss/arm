package manager

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/model"
)

// This opt-in gate uses the actual production sampler/admission. It must fail
// rather than manufacture capacity when the native scope is unavailable.
func TestOriginalWaiterNativeAdmissionAndExpiry(t *testing.T) {
	if os.Getenv("ARM_WAITER_NATIVE") != "1" {
		t.Skip("requires explicit real native CPU/process gate")
	}
	home := filepath.Join(t.TempDir(), "arm")
	m, err := Open(home, model.Resources{CPU: .25, Memory: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	}()
	m.Start()
	cwd := t.TempDir()
	marker := filepath.Join(cwd, "ran")
	q := model.Request{Key: "native-original-expiry", Reason: "native original-child expiry fixture", Resources: model.Resources{CPU: .01, Memory: 1 << 20}, TTL: time.Second}
	run := RunRequest{Request: q, Command: []string{"/bin/sh", "-c", `printf ready > "$1"; exec sleep 10`, "sh", marker}, CWD: cwd}
	var lease model.Lease
	limit := time.Now().Add(5 * time.Second)
	for {
		lease, err = m.Acquire(q, &run)
		if err == nil {
			break
		}
		if errorCode(err) != "insufficient_capacity" || time.Now().After(limit) {
			s, _ := m.Status()
			t.Fatalf("native admission unavailable: %v; CPU=%+v", err, s.CPU)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lease.Process == nil || lease.PID <= 0 {
		t.Fatal("missing captured original native identity", lease)
	}
	eventually(t, func() bool { _, err := os.Stat(marker); return err == nil })
	eventually(t, func() bool { l, e := m.Inspect(lease.ID); return e == nil && l.State == "terminal" })
	final, err := m.Inspect(lease.ID)
	if err != nil || final.Outcome != "expired" || final.ExitCode == nil {
		t.Fatal(final, err)
	}
	s, err := m.Status()
	if err != nil || s.Reserved.CPU != 0 || s.Reserved.Memory != 0 {
		t.Fatal(s.Reserved, err)
	}
	t.Logf("native admitted %.2f CPU; original pid %d namespace %s; outcome %s; original process/IO joined before released charge", q.Resources.CPU, lease.PID, lease.Process.Namespace, final.Outcome)
}
