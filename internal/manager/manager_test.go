package manager

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func newManager(t *testing.T) *Manager {
	t.Helper()
	m, e := Open(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	if e != nil {
		t.Fatal(e)
	}
	cpuReading(m, 64, 64, 0, 64, 0)
	t.Cleanup(func() {
		if e := m.Close(); e != nil {
			t.Error(e)
		}
	})
	return m
}
func request(key string) model.Request {
	return model.Request{Host: "localhost", Reason: "compile integration fixture", Resources: model.Resources{CPU: 1}, TTL: time.Minute, Key: key}
}
func eventually(t *testing.T, check func() bool) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

func TestConcurrentAdmissionAndAttribution(t *testing.T) {
	m := newManager(t)
	var granted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			if _, e := m.Acquire(request(""), nil); e == nil {
				granted.Add(1)
			}
		})
	}
	wg.Wait()
	if granted.Load() != 1 {
		t.Fatalf("admitted %d requests to one core", granted.Load())
	}
	s, e := m.Status()
	if e != nil {
		t.Fatal(e)
	}
	if s.Reserved.CPU != 1 || len(s.Resources) != 3 || s.Resources[0].Available != "0" {
		t.Fatalf("wrong resource accounting: %+v", s)
	}
	u := s.Resources[0].Users
	if len(u) != 1 || u[0].Reason != "compile integration fixture" || u[0].Owner == "" {
		t.Fatalf("missing who/why attribution: %+v", u)
	}
	if s.Resources[0].Observed != nil {
		t.Fatal("unmeasured consumption must be unknown")
	}
}

func TestIdempotencyAndAtomicClaims(t *testing.T) {
	m := newManager(t)
	r := request("one")
	r.Resources.Memory = 1024
	first, e := m.Acquire(r, nil)
	if e != nil {
		t.Fatal(e)
	}
	same, e := m.Acquire(r, nil)
	if e != nil || same.ID != first.ID {
		t.Fatalf("duplicate request: %+v %v", same, e)
	}
	r.Reason = "different"
	if _, e = m.Acquire(r, nil); e == nil {
		t.Fatal("reused key accepted for different request")
	}
	bad := request("two")
	bad.Resources.CPU = 0.5
	bad.Resources.Memory = 1
	if _, e = m.Acquire(bad, nil); e == nil {
		t.Fatal("partially admitted a request")
	}
	s, _ := m.Status()
	if len(s.Leases) != 1 || s.Reserved.Memory != 1024 {
		t.Fatalf("partial reservation leaked: %+v", s)
	}
	if _, e = m.End(first.ID, "released"); e != nil {
		t.Fatal(e)
	}
	if _, e = m.End(first.ID, "released"); e != nil {
		t.Fatal("release is not idempotent:", e)
	}
}

func TestExpiryQueueAndRenewal(t *testing.T) {
	m := newManager(t)
	m.Start()
	r := request("short")
	r.TTL = 80 * time.Millisecond
	first, e := m.Acquire(r, nil)
	if e != nil {
		t.Fatal(e)
	}
	r = request("queued")
	r.Queue = true
	second, e := m.Acquire(r, nil)
	if e != nil || second.State != "queued" {
		t.Fatalf("not queued: %+v %v", second, e)
	}
	if _, e = m.Renew(first.ID, 2*time.Minute); e == nil {
		t.Fatal("renewal ignored queued work")
	}
	eventually(t, func() bool { l, _ := m.Inspect(second.ID); return l.State == "active" })
	l, _ := m.Inspect(first.ID)
	if l.Outcome != "expired" {
		t.Fatalf("not expired: %+v", l)
	}
	if _, e = m.Renew(first.ID, time.Minute); e == nil {
		t.Fatal("revived expired lease")
	}
	events, e := m.Events(0)
	if e != nil || len(events) < 4 {
		t.Fatalf("missing audit events: %v %v", events, e)
	}
	if _, e = m.End(second.ID, "released"); e != nil {
		t.Fatal(e)
	}
}

func TestStableHostAndRawLeaseRecovery(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	budget := model.Resources{CPU: 1}
	m, e := Open(home, budget)
	if e != nil {
		t.Fatal(e)
	}
	l, e := m.Acquire(request("persist"), nil)
	if e != nil {
		t.Fatal(e)
	}
	m.Close()
	m, e = Open(home, budget)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	s, e := m.Status()
	if e != nil || s.Host != l.Host || s.Reserved.CPU != 1 {
		t.Fatalf("recovery lost host or allocation: %+v %v", s, e)
	}
}

func TestSupervisedExpiryAndDescendants(t *testing.T) {
	m := newManager(t)
	m.Start()
	r := request("run")
	r.TTL = 80 * time.Millisecond
	cwd := t.TempDir()
	run := RunRequest{Request: r, Command: []string{"/bin/sh", "-c", "sleep 10 & wait"}, CWD: cwd}
	l, e := m.Acquire(r, &run)
	if e != nil {
		t.Fatal(e)
	}
	eventually(t, func() bool { l, _ := m.Inspect(l.ID); return l.State == "terminal" })
	l, _ = m.Inspect(l.ID)
	if l.Outcome != "expired" || l.ExitCode == nil {
		t.Fatalf("wrong supervised outcome: %+v", l)
	}
	s, _ := m.Status()
	if s.Reserved.CPU != 0 {
		t.Fatal("terminated job still charged")
	}
	marker := filepath.Join(cwd, "escaped-marker")
	r = request("child")
	run = RunRequest{Request: r, Command: []string{"/bin/sh", "-c", "(sleep 0.4; echo unsafe > \"$1\") & exit 0", "sh", marker}, CWD: cwd}
	l, e = m.Acquire(r, &run)
	if e != nil {
		t.Fatal(e)
	}
	eventually(t, func() bool { l, _ := m.Inspect(l.ID); return l.State == "terminal" })
	time.Sleep(500 * time.Millisecond)
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("descendant survived parent completion")
	}
}

func TestUnknownRecoveredJobStaysCharged(t *testing.T) {
	// Use a group created inside the current PID namespace. An inherited
	// outside group can appear as PGID 0 under an unprivileged unshare fixture.
	child := exec.Command("/bin/sleep", "30")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	home := filepath.Join(t.TempDir(), "arm")
	m, e := Open(home, model.Resources{CPU: 1})
	if e != nil {
		t.Fatal(e)
	}
	pool := makePool(t, m, "recovered job permits", "global", 1)
	req := request("unknown")
	req.Pools = []model.PoolClaim{{Pool: pool.ID, Quantity: 1}}
	l, e := m.Acquire(req, nil)
	if e != nil {
		t.Fatal(e)
	}
	l.PID = child.Process.Pid
	data, _ := json.Marshal(l)
	if _, e = m.db.Exec(`UPDATE leases SET run='{}',data=? WHERE id=?`, string(data), l.ID); e != nil {
		t.Fatal(e)
	}
	m.Close()
	m, e = Open(home, model.Resources{CPU: 1})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	s, _ := m.Status()
	if s.Reserved.CPU != 1 || s.Leases[0].State != "needs_attention" {
		t.Fatal("unverified recovered PID released capacity")
	}
	poolView, err := m.PoolInspect(pool.ID)
	if err != nil || poolView.Reserved != 1 {
		t.Fatal("recovered supervised job released custom permits", err)
	}
	if _, e = m.End(l.ID, "released"); e == nil {
		t.Fatal("released unverified allocation")
	}
	if err := child.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("recovery or release signalled the unverified group", err)
	}
}

func TestCargoBudgetCannotBeOverridden(t *testing.T) {
	m := newManager(t)
	r := request("cargo")
	r.Profile = "rust-build"
	run := RunRequest{Request: r, Command: []string{"cargo", "build", "-j8"}, CWD: t.TempDir()}
	if _, e := m.Acquire(r, &run); e == nil {
		t.Fatal("accepted jobs override")
	}
	s, _ := m.Status()
	if len(s.Leases) != 0 {
		t.Fatal("invalid profile created a lease")
	}
}

func TestCancellationCannotRaceIntoLaterAdmission(t *testing.T) {
	m := newManager(t)
	if e := m.CancelRequest("not-yet-admitted"); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Acquire(request("not-yet-admitted"), nil); e == nil {
		t.Fatal("cancelled request was admitted after cancellation won the race")
	}
	l, e := m.Acquire(request("existing"), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.CancelRequest("existing"); e != nil {
		t.Fatal(e)
	}
	l, _ = m.Inspect(l.ID)
	if l.State != "terminal" || l.Outcome != "cancelled" {
		t.Fatal("existing request was not cancelled")
	}
	s, _ := m.Status()
	if s.Reserved.CPU != 0 {
		t.Fatal("cancelled request retained its reservation")
	}
}
