package manager

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/cargoprofile"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
)

// Only this test binary uses an init trampoline; production's fixed hidden
// entrypoint is in CLI.Main before global home preparation.
func init() {
	if os.Getenv("ARM_MANAGER_CARGO_HELPER") != "1" || len(os.Args) != 2 || os.Args[1] != "_cargo-helper" {
		return
	}
	image, e := mesh.CaptureRuntimeExecutable()
	if e != nil {
		os.Exit(125)
	}
	proof, e := image.Proof()
	if e != nil {
		os.Exit(125)
	}
	in, out := os.NewFile(3, "cargo-parent-in"), os.NewFile(4, "cargo-parent-out")
	syscall.CloseOnExec(3)
	syscall.CloseOnExec(4)
	var wire io.Writer = out
	if kind := os.Getenv("ARM_CARGO_TEST_DROP"); kind != "" {
		wire = &cargoDroppingFrames{out: out, drop: kind}
	}
	os.Exit(cargoprofile.RunHelper(context.Background(), in, wire, cargoprofile.HelperRuntime{SHA256: proof.SHA256, Size: proof.Size, Version: proof.Version, Protocol: cargoprofile.HelperProtocol}, os.Stdout, os.Stderr))
}
func configureCargoTestHelper(t *testing.T, m *Manager) {
	t.Helper()
	t.Setenv("ARM_MANAGER_CARGO_HELPER", "1")
	image, e := mesh.CaptureRuntimeExecutable()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { image.Close() })
	e = m.ConfigureCargoHelper(func() (string, cargoprofile.HelperRuntime, error) {
		path, e := image.VerifiedPath()
		if e != nil {
			return "", cargoprofile.HelperRuntime{}, e
		}
		proof, e := image.Proof()
		return path, cargoprofile.HelperRuntime{SHA256: proof.SHA256, Size: proof.Size, Version: proof.Version, Protocol: cargoprofile.HelperProtocol}, e
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestCargoPreparationIsChargedAndNeverHoldsGlobalMutex(t *testing.T) {
	m := newManager(t)
	store, e := m.Acquire(storageRequest("cargo-parent"), nil)
	if e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var called atomic.Int32
	if e = m.ConfigureCargoHelper(func() (string, cargoprofile.HelperRuntime, error) {
		called.Add(1)
		close(entered)
		<-release
		return "", cargoprofile.HelperRuntime{}, errors.New("private fixture image unavailable")
	}); e != nil {
		t.Fatal(e)
	}
	r := request("cargo-preparing")
	r.Profile = "rust-build"
	r.StorageLease = store.ID
	run := RunRequest{Request: r, Command: []string{"cargo", "build", "--offline"}, CWD: t.TempDir()}
	lease, e := m.Acquire(r, &run)
	if e != nil {
		close(release)
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Cargo worker did not start")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		status, e := m.Status()
		if e != nil || status.Reserved.CPU != 1 {
			t.Errorf("preparation was not charged: %+v %v", status, e)
		}
		again, e := m.Acquire(r, &run)
		if e != nil || again.ID != lease.ID {
			t.Errorf("exact retry started another request: %+v %v", again, e)
		}
		if _, e = m.StorageHandoff(store.ID); e == nil {
			t.Error("live preparing consumer did not protect storage")
		}
		if _, e = m.End(lease.ID, "cancelled"); e != nil {
			t.Error(e)
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("slow helper image IO held Manager.mu")
	}
	state, e := m.Inspect(lease.ID)
	if e != nil || state.State != "ending" {
		close(release)
		t.Fatal("cancel returned capacity before worker quiesced", state, e)
	}
	close(release)
	eventually(t, func() bool { l, _ := m.Inspect(lease.ID); return l.State == "terminal" })
	state, e = m.Inspect(lease.ID)
	if e != nil || state.Outcome != "cancelled" || called.Load() != 1 {
		t.Fatal(state, called.Load(), e)
	}
	again, e := m.Acquire(r, &run)
	if e != nil || again.ID != lease.ID || called.Load() != 1 {
		t.Fatal("terminal exact key caused execution retry", again, called.Load(), e)
	}
}
func TestCargoRecoveryNeverFreesUnknownPIDZeroSpawnIntent(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	capacity := model.Resources{CPU: 1, Memory: 1024, Disk: 2048}
	m, e := Open(home, capacity)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if m != nil {
			m.Close()
		}
	}()
	store, e := m.Acquire(storageRequest("recover-storage"), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.ConfigureCargoHelper(func() (string, cargoprofile.HelperRuntime, error) {
		return "", cargoprofile.HelperRuntime{}, errors.New("fixture")
	}); e != nil {
		t.Fatal(e)
	}
	r := request("recover-cargo")
	r.Profile = "rust-build"
	r.StorageLease = store.ID
	run := RunRequest{Request: r, Command: []string{"cargo", "check", "--offline"}, CWD: t.TempDir()}
	awaitNativeFixtureHeadroom(t, m, r.Resources)
	l, e := m.Acquire(r, &run)
	if e != nil {
		t.Fatal(e)
	}
	eventually(t, func() bool { x, _ := m.Inspect(l.ID); return x.State == "terminal" })
	m.mu.Lock()
	l, e = m.get(l.ID)
	if e != nil {
		m.mu.Unlock()
		t.Fatal(e)
	}
	rec, e := m.getCargo(l.ID)
	if e != nil {
		m.mu.Unlock()
		t.Fatal(e)
	}
	// Restore the exact durable pre-bind crash window using the real admission
	// producer's original record; PID0 alone must not prove no process existed.
	rec.Phase = "spawn_intent"
	rec.Worker = "lost-worker"
	rec.NoSpawn = false
	rec.NoPreflight = false
	rec.GroupAbsent = false
	rec.FinalAt = nil
	rec.Error = ""
	rec.Revision++
	l.State = "provisioning"
	l.Outcome = ""
	l.Ended = nil
	l.Failure = ""
	l.Revision++
	e = m.putCargo(l, rec, "fixture.crash_window")
	m.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	m = nil
	m, e = Open(home, capacity)
	if e != nil {
		t.Fatal(e)
	}
	status, e := m.Status()
	if e != nil || status.Reserved.CPU != 1 {
		t.Fatal("unknown pre-bind spawn returned capacity", status, e)
	}
	l, e = m.Inspect(l.ID)
	if e != nil || l.State != "needs_attention" || l.PID != 0 {
		t.Fatal(l, e)
	}
	if _, e = m.Reconcile(l.ID); e == nil {
		t.Fatal("PID0 unknown spawn was reconciled as absent")
	}
	if _, e = m.End(l.ID, "released"); e == nil {
		t.Fatal("unknown Cargo spawn was released")
	}
	if _, e = m.StorageHandoff(store.ID); e == nil {
		t.Fatal("uncertain Cargo consumer no longer protects parent storage")
	}
}

type cargoDroppingFrames struct {
	out     io.Writer
	drop    string
	pending []byte
}

func (w *cargoDroppingFrames) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	for len(w.pending) >= 4 {
		n := int(binary.BigEndian.Uint32(w.pending[:4]))
		if n > cargoprofile.MaxHelperFrame {
			return 0, io.ErrShortWrite
		}
		if len(w.pending) < n+4 {
			break
		}
		frame := w.pending[:n+4]
		var packet cargoprofile.HelperPacket
		if e := json.Unmarshal(frame[4:], &packet); e != nil {
			return 0, e
		}
		if packet.Kind != w.drop {
			if _, e := w.out.Write(frame); e != nil {
				return 0, e
			}
		}
		w.pending = w.pending[n+4:]
	}
	return len(p), nil
}
func cargoWait(t *testing.T, condition func() bool) {
	t.Helper()
	end := time.Now().Add(40 * time.Second)
	for time.Now().Before(end) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Cargo phase did not settle")
}
func realCargoTestProject(t *testing.T, m *Manager, wait bool) (model.Request, RunRequest, string, string) {
	t.Helper()
	cargo, e := exec.LookPath("cargo")
	if bin := os.Getenv("ARM_TEST_TOOLCHAIN_BIN"); bin != "" {
		cargo, e = filepath.Join(bin, "cargo"), nil
	}
	if e != nil {
		t.Skip("real installed Cargo required")
	}
	configureCargoTestHelper(t, m)
	private, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("CARGO_HOME", filepath.Join(private, "cargo-home"))
	t.Setenv("CARGO_NET_OFFLINE", "true")
	if bin := os.Getenv("ARM_TEST_TOOLCHAIN_BIN"); bin != "" {
		t.Setenv("RUSTC", filepath.Join(bin, "rustc"))
		t.Setenv("RUSTDOC", filepath.Join(bin, "rustdoc"))
	}
	if path := os.Getenv("ARM_TEST_TOOL_LIBRARY_PATH"); path != "" {
		t.Setenv("LD_LIBRARY_PATH", path)
	}
	m.capacity.Disk = 64 << 20
	storage := storageRequest("cargo-fault-storage")
	storage.Resources.Disk = 32 << 20
	storage.TTL = 3 * time.Minute
	store, e := m.Acquire(storage, nil)
	if e != nil {
		t.Fatal(e)
	}
	project := filepath.Join(private, "project")
	if e = os.MkdirAll(filepath.Join(project, "src"), 0700); e != nil {
		t.Fatal(e)
	}
	for name, text := range map[string]string{"Cargo.toml": "[package]\nname='arm_cargo_fault'\nversion='0.1.0'\nedition='2021'\n", "src/main.rs": "fn main(){}\n"} {
		if e = os.WriteFile(filepath.Join(project, name), []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
	}
	ready, release := filepath.Join(private, "ready"), filepath.Join(private, "release")
	if wait {
		script := "fn main(){std::fs::write(" + strconv.Quote(ready) + ",b\"ready\").unwrap();while !std::path::Path::new(" + strconv.Quote(release) + ").exists(){std::thread::sleep(std::time::Duration::from_millis(20));}}\n"
		if e = os.WriteFile(filepath.Join(project, "build.rs"), []byte(script), 0600); e != nil {
			t.Fatal(e)
		}
	}
	r := request("cargo-fault-command")
	r.Profile = "rust-build"
	r.StorageLease = store.ID
	r.TTL = time.Minute
	return r, RunRequest{Request: r, Command: []string{cargo, "build", "--offline"}, CWD: project}, ready, release
}
func TestCargoLostStartedReplyKeepsUnknownChargedAndExactKeyNeverReruns(t *testing.T) {
	m := newManager(t)
	m.Start()
	t.Setenv("ARM_CARGO_TEST_DROP", "started")
	r, run, _, _ := realCargoTestProject(t, m, false)
	l, e := m.Acquire(r, &run)
	if e != nil {
		t.Fatal(e)
	}
	cargoWait(t, func() bool { x, _ := m.Inspect(l.ID); return x.State == "needs_attention" })
	before, e := m.Inspect(l.ID)
	if e != nil {
		t.Fatal(e)
	}
	if before.Cargo.AuthorizedAt == nil || before.Cargo.StartedAt != nil || before.Outcome != "cargo_outcome_unknown" {
		t.Fatal("lost started receipt invented known execution result", before)
	}
	status, e := m.Status()
	if e != nil || status.Reserved.CPU != 1 {
		t.Fatal("lost post-grant reply returned capacity", status, e)
	}
	again, e := m.Acquire(r, &run)
	if e != nil || again.ID != l.ID || again.Revision != before.Revision || again.PID != before.PID {
		t.Fatal("unknown exact retry restarted execution", again, e)
	}
	if _, e = m.StorageHandoff(r.StorageLease); e == nil {
		t.Fatal("unknown reply stopped protecting managed storage")
	}
	if _, e = m.Reconcile(l.ID); e != nil {
		t.Fatal("verified absent group could not be explicitly reconciled", e)
	}
}
func TestCargoCancelAfterCompileGrantBeforeStartedReceiptWaitsForGroup(t *testing.T) {
	m := newManager(t)
	m.Start()
	t.Setenv("ARM_CARGO_TEST_DROP", "started")
	r, run, ready, _ := realCargoTestProject(t, m, true)
	l, e := m.Acquire(r, &run)
	if e != nil {
		t.Fatal(e)
	}
	cargoWait(t, func() bool { _, e := os.Stat(ready); return e == nil })
	before, e := m.Inspect(l.ID)
	if e != nil || before.Cargo.AuthorizedAt == nil || before.Cargo.StartedAt != nil {
		t.Fatal("fixture did not reach exact lost-start boundary", before, e)
	}
	ending, e := m.End(l.ID, "cancelled")
	if e != nil || ending.State != "ending" {
		t.Fatal(ending, e)
	}
	cargoWait(t, func() bool {
		current, err := m.Inspect(l.ID)
		if err != nil {
			t.Fatal("original Cargo lease inspection failed", err)
		}
		if current.State == "needs_attention" {
			m.mu.Lock()
			rec, recordErr := m.getCargo(l.ID)
			m.mu.Unlock()
			t.Fatalf("original Cargo cleanup became uncertain: failure=%q cargo_error=%q record_error=%v pid=%d process=%+v", current.Failure, rec.Error, recordErr, current.PID, current.Process)
		}
		return current.State == "terminal"
	})
	done, e := m.Inspect(l.ID)
	if e != nil || done.Outcome != "cancelled" {
		t.Fatal(done, e)
	}
	if !errors.Is(syscall.Kill(-before.PID, 0), syscall.ESRCH) {
		t.Fatal("returned capacity before group absence")
	}
}
func TestCargoFinalWriteFailureRetainsCapacityAndStorageUntilReconciled(t *testing.T) {
	m := newManager(t)
	m.Start()
	r, run, ready, release := realCargoTestProject(t, m, true)
	l, e := m.Acquire(r, &run)
	if e != nil {
		t.Fatal(e)
	}
	cargoWait(t, func() bool { _, e := os.Stat(ready); return e == nil })
	m.mu.Lock()
	_, e = m.db.Exec(`CREATE TRIGGER cargo_finish_fault BEFORE UPDATE ON cargo_workloads WHEN json_extract(NEW.data,'$.phase')='finished' BEGIN SELECT RAISE(ABORT,'final Cargo write fixture'); END`)
	m.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(release, []byte("release"), 0600); e != nil {
		t.Fatal(e)
	}
	cargoWait(t, func() bool { x, _ := m.Inspect(l.ID); return x.State == "needs_attention" })
	done, e := m.Inspect(l.ID)
	if e != nil || done.Outcome != "cargo_persistence_failed" {
		t.Fatal(done, e)
	}
	status, e := m.Status()
	if e != nil || status.Reserved.CPU != 1 {
		t.Fatal("failed final persistence returned capacity", status, e)
	}
	if _, e = m.StorageHandoff(r.StorageLease); e == nil {
		t.Fatal("failed final persistence lost storage protection")
	}
	m.mu.Lock()
	_, e = m.db.Exec(`DROP TRIGGER cargo_finish_fault`)
	m.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Reconcile(l.ID); e != nil {
		t.Fatal(e)
	}
}
func TestCargoFinishedReceiptCannotContradictRecordedGrantOrStart(t *testing.T) {
	now := time.Now().UTC()
	started := &cargoprofile.HelperStarted{At: now}
	for _, test := range []struct {
		record cargoRecord
		finish cargoprofile.HelperFinished
	}{
		{cargoRecord{Started: started, AuthorizedAt: &now}, cargoprofile.HelperFinished{Code: 1, CommandStarted: false}},
		{cargoRecord{}, cargoprofile.HelperFinished{Code: 1, CommandStarted: true}},
		{cargoRecord{AuthorizedAt: &now}, cargoprofile.HelperFinished{Code: 0, CommandStarted: true}},
	} {
		if e := cargoFinishedConsistent(test.record, test.finish); e == nil {
			t.Fatal("contradictory final receipt accepted")
		}
	}
	if e := cargoFinishedConsistent(cargoRecord{}, cargoprofile.HelperFinished{Code: 125, CommandStarted: false}); e != nil {
		t.Fatal("known preflight failure rejected", e)
	}
}

func TestCargoQueuedDeadlineNeverStartsHelperOrCreatesArtifactRoots(t *testing.T) {
	m := newManager(t)
	store, e := m.Acquire(storageRequest("cargo-deadline-parent"), nil)
	if e != nil {
		t.Fatal(e)
	}
	blocker, e := m.Acquire(request("cargo-deadline-blocker"), nil)
	if e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	if e = m.ConfigureCargoHelper(func() (string, cargoprofile.HelperRuntime, error) {
		calls.Add(1)
		return "", cargoprofile.HelperRuntime{}, errors.New("must remain queued")
	}); e != nil {
		t.Fatal(e)
	}
	r := request("cargo-queued-deadline")
	r.Profile = "rust-build"
	r.StorageLease = store.ID
	r.Queue = true
	deadline := time.Now().UTC().Add(150 * time.Millisecond)
	r.StartDeadline = &deadline
	run := RunRequest{Request: r, Command: []string{"cargo", "check", "--offline"}, CWD: t.TempDir()}
	l, e := m.Acquire(r, &run)
	if e != nil || l.State != "queued" {
		t.Fatal(l, e)
	}
	m.Start()
	cargoWait(t, func() bool { x, _ := m.Inspect(l.ID); return x.State == "terminal" })
	l, e = m.Inspect(l.ID)
	if e != nil || l.Outcome != "rejected" || l.PID != 0 || calls.Load() != 0 || l.QueueInfo == nil || l.QueueInfo.Rejection == nil || l.QueueInfo.Rejection.Code != "start_deadline_exceeded" {
		t.Fatal(l, calls.Load(), e)
	}
	items, e := os.ReadDir(store.ManagedDir)
	if e != nil || len(items) != 0 {
		t.Fatal("queued deadline created output roots", items, e)
	}
	if _, e = m.End(blocker.ID, "released"); e != nil {
		t.Fatal(e)
	}
	again, e := m.Acquire(r, &run)
	if e != nil || again.ID != l.ID || calls.Load() != 0 {
		t.Fatal("deadline retry reran helper", again, e)
	}
}

func TestCargoExpiryAfterAuthorizationBeforeStartedReceiptWaitsForVerifiedGroupAbsence(t *testing.T) {
	m := newManager(t)
	t.Setenv("ARM_CARGO_TEST_DROP", "started")
	r, run, ready, _ := realCargoTestProject(t, m, true)
	r.TTL = 5 * time.Second
	run.Request = r
	m.Start()
	l, e := m.Acquire(r, &run)
	if e != nil {
		t.Fatal(e)
	}
	cargoWait(t, func() bool { _, e := os.Stat(ready); return e == nil })
	live, e := m.Inspect(l.ID)
	if e != nil || live.Cargo == nil || live.Cargo.AuthorizedAt == nil || live.Cargo.StartedAt != nil {
		t.Fatal("actual build not started before expiry", live, e)
	}
	cargoWait(t, func() bool {
		current, err := m.Inspect(l.ID)
		if err != nil {
			t.Fatal("original Cargo lease inspection failed", err)
		}
		if current.State == "needs_attention" {
			m.mu.Lock()
			rec, recordErr := m.getCargo(l.ID)
			m.mu.Unlock()
			t.Fatalf("original Cargo cleanup became uncertain: failure=%q cargo_error=%q record_error=%v pid=%d process=%+v", current.Failure, rec.Error, recordErr, current.PID, current.Process)
		}
		return current.State == "terminal"
	})
	l, e = m.Inspect(l.ID)
	if e != nil || l.Outcome != "expired" {
		t.Fatal(l, e)
	}
	if e = syscall.Kill(-live.Process.PGID, 0); e != syscall.ESRCH {
		t.Fatal("expiry returned live process capacity", e)
	}
	status, e := m.Status()
	if e != nil || status.Reserved.CPU != 0 {
		t.Fatal(status, e)
	}
}

func TestCargoEndingRefusesReplacedOriginalWorkerBinding(t *testing.T) {
	for _, field := range []string{"host", "owner", "request"} {
		t.Run(field, func(t *testing.T) {
			m := newManager(t)
			store, err := m.Acquire(storageRequest("bound-parent"), nil)
			if err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			if err = m.ConfigureCargoHelper(func() (string, cargoprofile.HelperRuntime, error) {
				close(entered)
				<-release
				return "", cargoprofile.HelperRuntime{}, errors.New("owned source fixture completed")
			}); err != nil {
				t.Fatal(err)
			}
			request := request("bound-worker")
			request.Profile = "rust-build"
			request.StorageLease = store.ID
			lease, err := m.Acquire(request, &RunRequest{Request: request, Command: []string{"cargo", "build", "--offline"}, CWD: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("original source did not start")
			}
			m.mu.Lock()
			original, err := m.get(lease.ID)
			if err != nil {
				m.mu.Unlock()
				t.Fatal(err)
			}
			changed := original
			switch field {
			case "host":
				changed.Host = "changed-host"
			case "owner":
				changed.Owner = "changed-owner"
			case "request":
				changed.Request.Reason = "changed intent"
			}
			err = m.put(changed, "test.cargo_binding_changed")
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.End(lease.ID, "cancelled"); errorCode(err) != "needs_attention" {
				t.Fatal("replaced row controlled original worker", err)
			}
			status, err := m.Status()
			if err != nil || status.Reserved.CPU != 1 {
				t.Fatal("replaced row freed worker charge", status.Reserved, err)
			}
			m.mu.Lock()
			err = m.put(original, "test.cargo_binding_restored")
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			unblock()
			eventually(t, func() bool { current, err := m.Inspect(lease.ID); return err == nil && current.State == "terminal" })
		})
	}
}

func TestCargoEndingJournalFailureCancelsOriginalWorker(t *testing.T) {
	for _, mode := range []string{"end", "close"} {
		t.Run(mode, func(t *testing.T) {
			m, err := Open(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
			if err != nil {
				t.Fatal(err)
			}
			cpuReading(m, 64, 64, 0, 64, 0)
			var closeOnce, releaseOnce, cancelOnce sync.Once
			var closeErr error
			closeManager := func() error { closeOnce.Do(func() { closeErr = m.Close() }); return closeErr }
			release := make(chan struct{})
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(func() { unblock(); _ = closeManager() })
			store, err := m.Acquire(storageRequest("cargo-journal-parent"), nil)
			if err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{})
			if err = m.ConfigureCargoHelper(func() (string, cargoprofile.HelperRuntime, error) {
				close(entered)
				<-release
				return "", cargoprofile.HelperRuntime{}, errors.New("owned cancellable image preparation returned")
			}); err != nil {
				t.Fatal(err)
			}
			q := request("cargo-journal-cancel")
			q.Profile = "rust-build"
			q.StorageLease = store.ID
			lease, err := m.Acquire(q, &RunRequest{Request: q, Command: []string{"cargo", "build", "--offline"}, CWD: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("original Cargo image callback did not enter")
			}
			cancelled := make(chan struct{})
			m.mu.Lock()
			worker := m.cargoWorkers[lease.ID]
			if worker == nil {
				m.mu.Unlock()
				t.Fatal("original admitted Cargo worker absent")
			}
			prior, err := m.get(lease.ID)
			if err != nil {
				m.mu.Unlock()
				t.Fatal(err)
			}
			originalCancel := worker.cancel
			worker.cancel = func() {
				originalCancel()
				cancelOnce.Do(func() { close(cancelled) })
				if mode == "close" {
					unblock()
				}
			}
			_, err = m.db.Exec(`CREATE TRIGGER reject_cargo_ending_journal BEFORE INSERT ON events WHEN NEW.type='cargo.ending' BEGIN SELECT RAISE(ABORT,'owned ending journal refusal'); END`)
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "end" {
				if _, err = m.End(lease.ID, "cancelled"); err == nil {
					t.Fatal("Cargo ending journal refusal was hidden")
				}
				select {
				case <-cancelled:
				case <-time.After(3 * time.Second):
					t.Fatal("failed ending write did not cancel its original worker")
				}
				stored, err := m.Inspect(lease.ID)
				if err != nil || stored.State != prior.State || stored.Revision != prior.Revision || stored.Outcome != prior.Outcome || !consuming(stored.State) || stored.Ended != nil {
					t.Fatalf("failed ending write changed prior durable charge: state=%s prior_state=%s revision=%d prior_revision=%d outcome=%s prior_outcome=%s ended=%v error=%v", stored.State, prior.State, stored.Revision, prior.Revision, stored.Outcome, prior.Outcome, stored.Ended, err)
				}
				status, err := m.Status()
				if err != nil || status.Reserved.CPU != 1 {
					t.Fatal("failed ending write freed CPU before original callback joined", status.Reserved, err)
				}
				select {
				case <-worker.done:
					t.Fatal("worker joined before original blocked callback returned")
				default:
				}
				unblock()
				select {
				case <-worker.done:
				case <-time.After(3 * time.Second):
					t.Fatal("cancelled original callback did not join")
				}
				stored, err = m.Inspect(lease.ID)
				if err != nil || stored.State != "terminal" {
					t.Fatal("joined cancelled no-spawn worker did not settle", stored, err)
				}
			} else {
				closed := make(chan error, 1)
				go func() { closed <- closeManager() }()
				select {
				case <-cancelled:
				case <-time.After(3 * time.Second):
					t.Fatal("Close could not cancel original worker after journal refusal")
				}
				select {
				case err = <-closed:
					if err == nil {
						t.Fatal("Close hid Cargo ending journal failure")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("Close waited on uncancelled Cargo callback")
				}
				select {
				case <-worker.done:
				default:
					t.Fatal("Close returned before original Cargo callback joined")
				}
			}
		})
	}
}
