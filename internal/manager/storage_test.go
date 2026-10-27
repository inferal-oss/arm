package manager

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func storageRequest(key string) model.Request {
	return model.Request{Kind: "storage", Reason: "retained build outputs", Resources: model.Resources{Disk: 1024}, TTL: 2 * time.Minute, Key: key}
}

func storageEventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("storage operation did not reach the expected state")
}
func TestStorageLifetimeIsIndependentAndHandoffPreservesArtifacts(t *testing.T) {
	m := newManager(t)
	m.Start()
	s, err := m.Acquire(storageRequest("retained"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.State != "active" || s.Artifact == nil || s.Artifact.Identity == nil || s.ManagedDir == "" {
		t.Fatalf("storage not provisioned: %+v", s)
	}
	r := request("writer")
	r.StorageLease = s.ID
	run := RunRequest{Request: r, CWD: t.TempDir(), Command: []string{"/bin/sh", "-c", `printf output > "$ARM_STORAGE_DIR/result"`}}
	l, err := m.Acquire(r, &run)
	if err != nil {
		t.Fatal(err)
	}
	storageEventually(t, func() bool { l, _ := m.Inspect(l.ID); return l.State == "terminal" })
	s, _ = m.Inspect(s.ID)
	if s.State != "active" {
		t.Fatal("execution exit ended storage lifetime")
	}
	data, err := os.ReadFile(filepath.Join(s.ManagedDir, "result"))
	if err != nil || string(data) != "output" {
		t.Fatal("retained output missing")
	}
	status, _ := m.Status()
	if status.Reserved.CPU != 0 || status.Reserved.Disk != 1024 {
		t.Fatalf("wrong independent accounting: %+v", status.Reserved)
	}
	preview, err := m.StoragePreview(context.Background(), s.ID)
	if err != nil || !preview.CanClean || preview.Reclaimable != nil {
		t.Fatalf("preview failed: %+v %v", preview, err)
	}
	if _, err := os.Stat(filepath.Join(s.ManagedDir, "result")); err != nil {
		t.Fatal("dry-run changed data")
	}
	if _, err := m.StorageHandoff(s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.ManagedDir, "result")); err != nil {
		t.Fatal("handoff removed retained output")
	}
	status, _ = m.Status()
	if status.Reserved.Disk != 0 {
		t.Fatal("handoff did not end ARM's obligation")
	}
	if _, err := m.StorageCleanup(s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.ManagedDir, "result")); err != nil {
		t.Fatal("terminal handed-off data was cleaned")
	}
}

func TestStorageRevokesOwnedConsumersBeforeCleanupAndKeepsUnknownCharged(t *testing.T) {
	m := newManager(t)
	m.Start()
	s, err := m.Acquire(storageRequest("shared"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.ManagedDir, "payload"), []byte("blocks are not proven reclaimed"), 0600); err != nil {
		t.Fatal(err)
	}
	var consumers []model.Lease
	for _, key := range []string{"a", "b"} {
		r := request(key)
		r.Resources.CPU = .5
		r.StorageLease = s.ID
		run := RunRequest{Request: r, CWD: t.TempDir(), Command: []string{"/bin/sleep", "10"}}
		l, err := m.Acquire(r, &run)
		if err != nil {
			t.Fatal(err)
		}
		consumers = append(consumers, l)
	}
	if _, err := m.StorageHandoff(s.ID); err == nil {
		t.Fatal("handed off storage with active consumers")
	}
	preview, err := m.StoragePreview(context.Background(), s.ID)
	if err != nil || preview.CanClean || len(preview.Consumers) != 2 {
		t.Fatalf("missing shared consumers: %+v %v", preview, err)
	}
	if _, err := m.End(s.ID, "released"); err != nil {
		t.Fatal(err)
	}
	storageEventually(t, func() bool { l, _ := m.Inspect(s.ID); return l.State == "needs_attention" })
	s, _ = m.Inspect(s.ID)
	if s.Artifact.Phase != "removed" || s.Artifact.LastError == "" {
		t.Fatalf("cleanup did not record uncertainty: %+v", s)
	}
	for _, c := range consumers {
		l, _ := m.Inspect(c.ID)
		if l.State != "terminal" || l.Outcome != "revoked" {
			t.Fatalf("consumer survived: %+v", l)
		}
	}
	status, _ := m.Status()
	if status.Reserved.Disk != 1024 || status.Reserved.CPU != 0 {
		t.Fatalf("unknown reclamation became free: %+v", status.Reserved)
	}
	storageEventually(t, func() bool { _, err := m.StorageHandoff(s.ID); return err == nil })
}

func TestStorageRecoveryAndUnverifiedIdentityStayCharged(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	budget := model.Resources{CPU: 1, Disk: 2048}
	m, err := Open(home, budget)
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Acquire(storageRequest("recover"), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err = Open(home, budget)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.Start()
	recovered, err := m.Inspect(s.ID)
	if err != nil || recovered.ManagedDir != s.ManagedDir || recovered.Artifact.Identity == nil || *recovered.Artifact.Identity != *s.Artifact.Identity {
		t.Fatal("storage identity lost on restart")
	}
	// Simulate a crash after mkdir but before identity was committed.
	recovered.Artifact.Identity = nil
	m.mu.Lock()
	err = m.put(recovered, "fixture.unverified")
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.End(s.ID, "expired"); err != nil {
		t.Fatal(err)
	}
	storageEventually(t, func() bool { l, _ := m.Inspect(s.ID); return l.State == "needs_attention" })
	if _, err := os.Stat(s.ManagedDir); err != nil {
		t.Fatal("unverified directory was deleted")
	}
	status, _ := m.Status()
	if status.Reserved.Disk != 1024 {
		t.Fatal("unverified storage freed its budget")
	}
	retry, err := m.StorageCleanup(s.ID)
	if err != nil || retry.Outcome != "expired" {
		t.Fatalf("cleanup retry rewrote the original termination reason: %+v %v", retry, err)
	}
}

func TestStorageDeadlinesAndQueuedCancellation(t *testing.T) {
	m := newManager(t)
	m.Start()
	s, err := m.Acquire(storageRequest("deadline"), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := request("too-long")
	r.StorageLease = s.ID
	r.TTL = 3 * time.Minute
	if _, err := m.Acquire(r, &RunRequest{Request: r, CWD: t.TempDir(), Command: []string{"/bin/true"}}); err == nil {
		t.Fatal("execution outlives storage")
	}
	r = storageRequest("too-large")
	r.Resources.Disk = 2048
	r.Queue = true
	queued, err := m.Acquire(r, nil)
	if err != nil || queued.State != "queued" {
		t.Fatalf("expected queued storage: %+v %v", queued, err)
	}
	if _, err := m.End(queued.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	queued, _ = m.Inspect(queued.ID)
	if queued.State != "terminal" || queued.Artifact != nil {
		t.Fatal("queued cancellation provisioned a directory")
	}
}

func TestEmptyStorageCleanupAndInterruptedQuarantineRecovery(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	budget := model.Resources{Disk: 2048}
	m, err := Open(home, budget)
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Acquire(storageRequest("empty-recovery"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.storage.Quarantine(*s.Artifact); err != nil {
		t.Fatal(err)
	}
	// Simulate the persisted intent preceding an interrupted quarantine: the
	// record can still say owned; identity, not the old path, reconciles it.
	s.State = "ending"
	s.Outcome = "expired"
	zero := int64(0)
	s.Artifact.AllocatedBefore = &zero
	m.mu.Lock()
	err = m.put(s, "fixture.cleanup_interrupted")
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err = Open(home, budget)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.Start()
	storageEventually(t, func() bool { l, _ := m.Inspect(s.ID); return l.State == "terminal" || l.State == "needs_attention" })
	l, _ := m.Inspect(s.ID)
	if l.State != "terminal" || !l.Artifact.NamespaceRemoved || l.Outcome != "expired" {
		t.Fatalf("interrupted cleanup not reconciled: %+v artifact: %+v", l, l.Artifact)
	}
	status, _ := m.Status()
	if status.Reserved.Disk != 0 {
		t.Fatal("empty cleanup did not release its reservation")
	}
}

func TestCargoBuildUsesAttachedManagedStorage(t *testing.T) {
	cargo, err := exec.LookPath("cargo")
	if bin := os.Getenv("ARM_TEST_TOOLCHAIN_BIN"); bin != "" {
		cargo, err = filepath.Join(bin, "cargo"), nil
		t.Setenv("RUSTC", filepath.Join(bin, "rustc"))
		t.Setenv("RUSTDOC", filepath.Join(bin, "rustdoc"))
	}
	if lib := os.Getenv("ARM_TEST_TOOL_LIBRARY_PATH"); lib != "" {
		t.Setenv("LD_LIBRARY_PATH", lib)
	}
	if err != nil {
		t.Skip("Cargo is not installed")
	}
	m := newManager(t)
	configureCargoTestHelper(t, m)
	cacheParent, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("CARGO_HOME", filepath.Join(cacheParent, "cargo-home"))
	m.capacity.Disk = 64 << 20
	m.Start()
	r := storageRequest("cargo-storage")
	r.Resources.Disk = 32 << 20
	s, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "Cargo.toml"), []byte("[package]\nname = \"arm_storage_fixture\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "src", "main.rs"), []byte("fn main() { println!(\"arm\"); }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r = request("cargo-managed")
	r.Profile = "rust-build"
	r.StorageLease = s.ID
	r.TTL = 30 * time.Second
	run := RunRequest{Request: r, CWD: project, Command: []string{cargo, "build", "--offline"}}
	l, err := m.Acquire(r, &run)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		l, err = m.Inspect(l.ID)
		if err != nil {
			t.Fatal(err)
		}
		if l.State == "terminal" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if l.State != "terminal" || l.ExitCode == nil || *l.ExitCode != 0 {
		output, _ := m.Output(l.ID, 0)
		t.Fatalf("real Cargo build failed: %+v\n%s", l, output)
	}
	if _, err := os.Stat(filepath.Join(l.Cargo.Target, "debug", "arm_storage_fixture")); err != nil {
		t.Fatal("Cargo output not retained in storage lease:", err)
	}
	if _, err := os.Stat(filepath.Join(project, "target")); !os.IsNotExist(err) {
		t.Fatal("Cargo used the unowned project target directory")
	}
	bad := run
	bad.Key = "cargo-override"
	bad.Command = append(append([]string{}, run.Command...), "--target-dir", filepath.Join(project, "unowned"))
	if _, err := m.Acquire(bad.Request, &bad); err == nil {
		t.Fatal("explicit target override bypassed managed storage")
	}
}

func TestLaunchFailureIsTerminalAndDoesNotKeepAStorageConsumer(t *testing.T) {
	m := newManager(t)
	s, err := m.Acquire(storageRequest("launch-failure"), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := request("bad-cwd")
	r.StorageLease = s.ID
	run := RunRequest{Request: r, CWD: filepath.Join(t.TempDir(), "missing"), Command: []string{"/bin/true"}}
	l, err := m.Acquire(r, &run)
	if err != nil {
		t.Fatal(err)
	}
	if l.State != "terminal" || l.Outcome != "launch_failed" || l.Failure == "" {
		t.Fatalf("launch failure did not resolve allocation: %+v", l)
	}
	if _, err := m.StorageHandoff(s.ID); err != nil {
		t.Fatal("failed launch kept storage consumer alive:", err)
	}
}

func TestStorageExpiryReclaimsAnEmptyDirectory(t *testing.T) {
	m := newManager(t)
	m.Start()
	r := storageRequest("actual-expiry")
	r.TTL = 200 * time.Millisecond
	s, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	storageEventually(t, func() bool { l, _ := m.Inspect(s.ID); return l.State == "terminal" || l.State == "needs_attention" })
	l, _ := m.Inspect(s.ID)
	if l.State != "terminal" || l.Outcome != "expired" || !l.Artifact.NamespaceRemoved {
		t.Fatalf("expiry did not reclaim empty storage: %+v artifact: %+v", l, l.Artifact)
	}
	status, _ := m.Status()
	if status.Reserved.Disk != 0 {
		t.Fatal("empty expired storage still reserved")
	}
}
