//go:build linux && (amd64 || arm64)

package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
	"getarm.org/internal/observe"
	"getarm.org/internal/syncfs"
	"getarm.org/internal/syncreconcile"
)

// This opt-in test runs only inside the fresh loopback XFS guest fixture. It
// proves native quota observation and an actual allocation refusal for that
// disposable filesystem. The production enrollment/writer audit stays closed.
func TestNativeChargedScopeGuest(t *testing.T) {
	projectPath := os.Getenv("ARM_SYNC_XFS_GUEST_PROJECT")
	if projectPath == "" {
		t.Skip("requires disposable XFS guest fixture")
	}
	if os.Geteuid() != 0 || os.Getenv("ARM_SYNC_XFS_DISPOSABLE_GUEST") != "yes" {
		t.Fatal("disposable root guest witness required")
	}
	project, err := syncfs.OpenRoot(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	defer project.Close()
	selected, err := syncfs.OpenRoot(filepath.Join(projectPath, "selected"))
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Close()
	home, err := syncfs.OpenRoot(filepath.Join(projectPath, "home"))
	if err != nil {
		t.Fatal(err)
	}
	defer home.Close()
	quota, err := syncfs.ProbeNativeQuota(t.Context(), selected, home)
	if err != nil {
		t.Fatal(err)
	}
	const q int64 = 64 << 20
	if quota.Provider() != "xfs-project-quota" || quota.HardBytes() != uint64(q) || quota.UsedBytes() >= quota.HardBytes() {
		t.Fatalf("expected fresh enforced Q=64MiB, got %+v", quota)
	}
	projectID, err := project.NativeID()
	if err != nil {
		t.Fatal(err)
	}
	selectedID, err := selected.NativeID()
	if err != nil {
		t.Fatal(err)
	}
	homeID, err := home.NativeID()
	if err != nil {
		t.Fatal(err)
	}
	m, err := openWithSyncSchema(home.Path(), model.Resources{CPU: 2, Memory: 4 << 30, Disk: 512 << 20}, leaseclock.Read, nil, "3")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	// The home is within the quota project, so statfs(home) reports project
	// remainder, which is necessarily less than the full Q charged at admission.
	// This test-only collector reads the real backing XFS mount outside project
	// 42. Production needs a separately enrolled, identity-checked equivalent.
	backingPath := filepath.Dir(projectPath)
	m.observationMu.Lock()
	m.diskCollector = func(ctx context.Context, _ string) model.DiskAvailability {
		return observe.DiskAvailability(ctx, backingPath)
	}
	m.observationMu.Unlock()
	// This fixture derives digests from fresh guest identities. Production
	// enrollment must persist and independently verify its ownership record.
	enrollment := sha256.Sum256([]byte(fmt.Sprintf("guest-enrollment:%s:%s:%s:%s:%d", quota.Mount(), projectID, selectedID, homeID, q)))
	ownership := sha256.Sum256([]byte(fmt.Sprintf("guest-owner:%s:%s:%d", quota.Scope(), projectID, q)))
	m.syncDedicated = &syncDedicatedScope{
		gate: newSyncScopeGate(), project: project, selected: selected, home: home,
		scopeID: quota.Scope(), mountID: quota.Mount(), projectRootID: projectID,
		selectedRootID: selectedID, homeRootID: homeID, projectID: 42, ownerEpoch: 1,
		enrollmentDigest: hex.EncodeToString(enrollment[:]), ownershipDigest: hex.EncodeToString(ownership[:]),
		chargedHard: q, initialUsed: int64(quota.UsedBytes()),
	}
	// Cloud-init can still be using the virtual CPUs when runcmd starts. Wait
	// for genuine native headroom rather than injecting a test reading.
	deadline := time.Now().Add(8 * time.Second)
	for {
		m.sampleCPU()
		m.sampleMemory()
		m.sampleDisk()
		m.mu.Lock()
		cpu := m.cpuBudget(0)
		memory := m.memoryBudget(0)
		disk := m.diskBudget(0)
		m.mu.Unlock()
		if cpu.Known && cpu.Available >= 1 && memory.Available >= 768<<20 && disk.Known && disk.Available >= q {
			break
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "ARM_SYNC_HEADROOM cpu_known=%t cpu_available=%.3f cpu_quality=%s cpu_detail=%q cpu_interval=%s memory_available=%d disk_known=%t disk_available=%d\n",
				cpu.Known, cpu.Available, cpu.Observation.Quality, cpu.Observation.Detail, cpu.Observation.Interval,
				memory.Available, disk.Known, disk.Available)
			t.Fatal("native guest admission headroom unavailable")
		}
		time.Sleep(300 * time.Millisecond)
	}
	key := "guest-original-scope-12345"
	// Native headroom can change between the earlier observation and the
	// atomic admission check. A refusal must leave no lease or charged row;
	// retry only that transient refusal with new real samples.
	var p *syncPreparation
	admissionDeadline := time.Now().Add(30 * time.Second)
	for {
		p, err = m.beginSyncPreparation(context.Background(), key, syncreconcile.Local, 2*time.Minute, nil)
		if err == nil {
			break
		}
		var problem *model.Error
		if !errors.As(err, &problem) || problem.Code != "insufficient_capacity" {
			t.Fatal(err)
		}
		var leases, scopes int
		if e := m.db.QueryRow(`SELECT COUNT(*) FROM leases`).Scan(&leases); e != nil {
			t.Fatal(e)
		}
		if e := m.db.QueryRow(`SELECT COUNT(*) FROM sync_charged_scopes`).Scan(&scopes); e != nil {
			t.Fatal(e)
		}
		if leases != 0 || scopes != 0 {
			t.Fatalf("refused admission left original state: leases=%d scopes=%d", leases, scopes)
		}
		if time.Now().After(admissionDeadline) {
			t.Fatalf("fresh native headroom never admitted charged Q: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	proof, err := p.verifyChargedOriginal(t.Context(), key, syncreconcile.Local)
	if err != nil {
		p.scratchUncertain()
		p.finish()
		t.Fatal(err)
	}
	// The cached proof is only an observation; a fresh original hold is still
	// required to read its binding, and the hard limit must equal charged Q.
	claim, err := syncfs.NewScopeClaim(syncfs.ScopeClaimFields{
		Key: key, Side: "local", PreparationLease: p.owner.id, Provider: quota.Provider(),
		ScopeID: quota.Scope(), MountID: quota.Mount(), ProjectRootID: projectID,
		SelectedRootID: selectedID, ParentRootID: homeID,
		OwnershipDigest: hex.EncodeToString(ownership[:]), ProjectID: 42,
		OwnerEpoch: 1, ChargedHardBytes: q,
	})
	if err != nil {
		p.scratchUncertain()
		p.finish()
		t.Fatal(err)
	}
	binding, err := proof.BindingFor(t.Context(), claim)
	if err != nil || binding.HardBytes() != uint64(q) {
		p.scratchUncertain()
		p.finish()
		t.Fatalf("native Q differs from original charge: %+v %v", binding, err)
	}
	var fs unix.Statfs_t
	if err := unix.Statfs(backingPath, &fs); err != nil || fs.Bavail*uint64(fs.Bsize) <= 128<<20 {
		p.scratchUncertain()
		p.finish()
		t.Fatalf("backing filesystem lacks free-space control: %+v %v", fs, err)
	}
	effect, err := p.beginChargedEffect(t.Context(), proof)
	if err != nil {
		p.scratchUncertain()
		p.finish()
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(selected.Path(), "quota-refusal"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		effect.close()
		p.scratchUncertain()
		p.finish()
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	var refusal error
	for n := 0; n < 96; n++ {
		if _, refusal = file.Write(chunk); refusal != nil {
			break
		}
		if refusal = file.Sync(); refusal != nil {
			break
		}
	}
	if closeErr := file.Close(); refusal == nil {
		refusal = closeErr
	}
	effect.close()
	p.scratchUncertain()
	if err := p.finish(); err == nil {
		t.Fatal("quota failure settled the original charge")
	}
	status, err := m.InspectSyncKey(t.Context(), key, syncreconcile.Local)
	if err != nil || status.Lease.ID != p.owner.id || status.Lease.State != "needs_attention" {
		t.Fatalf("quota refusal lost original charge: %+v %v", status, err)
	}
	afterQuota, quotaErr := syncfs.ProbeNativeQuota(t.Context(), selected, home)
	var afterFS unix.Statfs_t
	spaceErr := unix.Statfs(backingPath, &afterFS)
	if quotaErr != nil || afterQuota.HardBytes() != uint64(q) || afterQuota.UsedBytes() < uint64(q-(2<<20)) ||
		spaceErr != nil || afterFS.Bavail*uint64(afterFS.Bsize) <= 128<<20 {
		t.Fatalf("quota refusal control missing: quota=%+v quota_err=%v backing=%+v backing_err=%v", afterQuota, quotaErr, afterFS, spaceErr)
	}
	if !errors.Is(refusal, unix.EDQUOT) && !errors.Is(refusal, unix.ENOSPC) {
		t.Fatalf("expected XFS project allocation refusal with backing space free, got %v", refusal)
	}
	t.Logf("ARM_SYNC_NATIVE_QUOTA_REFUSAL hard=%d used_before=%d used_after=%d backing_free=%d refused=%v original_retained=%s",
		q, quota.UsedBytes(), afterQuota.UsedBytes(), afterFS.Bavail*uint64(afterFS.Bsize), refusal, status.Lease.ID)
}
