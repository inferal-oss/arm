package manager

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/inferal-oss/arm/internal/model"
)

func gradleEvidenceFixture(t *testing.T) (*Manager, model.Lease, gradleRecord, string) {
	t.Helper()
	m := newManager(t)
	m.Start()
	store, err := m.Acquire(storageRequest("gradle-evidence-store"), nil)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.Acquire(request("gradle-evidence-lease"), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := gradleRecord{Lease: lease.ID, Storage: store.ID, Name: "workload-" + lease.ID, Nonce: "private-nonce", Phase: "ready", ExpectedPID: 123, ServiceHeap: 1000, ServiceMetaspace: 500,
		Binding: model.WorkloadBinding{Role: "consumer", Home: "/owned/home", JavaHome: "/installed/jdk", WorkerLimit: 2, ServiceStartMillis: 42, GradleVersion: "9.8.1"}, Children: map[string]gradleDirIdentity{}}
	dir, identity, err := m.openGradleDirectory(store.ID, r.Name, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	r.Identity = &identity
	child, childID, err := gradleChildDirectory(dir, "receipts", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	child.Close()
	r.Children["receipts"] = childID
	if err := m.saveGradleRecord(&lease, r, "workload.fixture"); err != nil {
		t.Fatal(err)
	}
	return m, lease, r, filepath.Join(store.ManagedDir, r.Name, "receipts")
}

func TestGradleCompletionRequiresExactPrivateServiceEvidence(t *testing.T) {
	for _, change := range []string{"valid", "nonce", "lease", "pid", "start", "jdk", "home", "heap", "metaspace", "workers", "version", "missing", "mismatch"} {
		t.Run(change, func(t *testing.T) {
			m, _, r, path := gradleEvidenceFixture(t)
			e := gradleEvidence{Nonce: r.Nonce, Lease: r.Lease, PID: 123, StartMillis: 42, JavaHome: r.Binding.JavaHome, GradleHome: r.Binding.Home, Version: "9.8.1", Workers: 2, MaxHeap: 900, MetaspaceMax: 400}
			write := func(phase string, value gradleEvidence) {
				value.Phase = phase
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, phase+".json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("started", e)
			switch change {
			case "nonce":
				e.Nonce = "other"
			case "lease":
				e.Lease = "other"
			case "pid":
				e.PID++
			case "start":
				e.StartMillis++
			case "jdk":
				e.JavaHome += "/other"
			case "home":
				e.GradleHome += "/other"
			case "heap":
				e.MaxHeap = 1001
			case "metaspace":
				e.MetaspaceMax = -1
			case "workers":
				e.Workers++
			case "version":
				e.Version = "other"
			case "mismatch":
				write("mismatch", e)
			}
			if change != "missing" {
				write("finished", e)
			}
			_, err := m.gradleFinishedEvidence(r)
			if (err == nil) != (change == "valid") {
				t.Fatalf("completion %s: %v", change, err)
			}
		})
	}
}

func TestGradleRecordAndLeaseProjectionCommitTogether(t *testing.T) {
	m, lease, r, _ := gradleEvidenceFixture(t)
	before := lease.Revision
	if _, err := m.db.Exec(`CREATE TRIGGER fail_gradle_record BEFORE UPDATE ON gradle_workloads BEGIN SELECT RAISE(ABORT,'fixture persistence failure'); END`); err != nil {
		t.Fatal(err)
	}
	r.Binding.State = "must-not-appear"
	if err := m.saveGradleRecord(&lease, r, "workload.fixture_failure"); err == nil {
		t.Fatal("failed persistence accepted")
	}
	stored, err := m.get(lease.ID)
	if err != nil || stored.Revision != before || stored.Workload.State == "must-not-appear" {
		t.Fatalf("partial lease projection: %+v %v", stored, err)
	}
	record, err := m.gradleRecord(lease.ID)
	if err != nil || record.Binding.State == "must-not-appear" {
		t.Fatal("partial private record", record, err)
	}
}

func TestGradleProfileRefusesRemoteAndUnsupervisedServiceAttachments(t *testing.T) {
	m := newManager(t)
	r := request("no-unsupervised-service")
	r.Profile, r.StorageLease = "gradle-service", "storage"
	r.Resources.Memory = 1 << 30
	if err := m.validateGradleRequest(r, nil); errorCode(err) != "profile_conflict" {
		t.Fatal(err)
	}
	r.Profile, r.ServiceLease = "", "service"
	if err := m.validateGradleRequest(r, &RunRequest{}); errorCode(err) != "profile_conflict" {
		t.Fatal(err)
	}
	r.Profile = "gradle-build"
	if _, err := m.AcquirePeer("peer", r, &RunRequest{}, model.Resources{CPU: 10, Memory: 2 << 30}, r.TTL); errorCode(err) != "unsupported_remote_claim" {
		t.Fatal("remote service accepted", err)
	}
}

func TestGradleLateProbeFailureCannotResurrectSettledOperation(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		t.Run(map[bool]string{true: "terminal-lease", false: "finished-record"}[terminal], func(t *testing.T) {
			m, l, r, _ := gradleEvidenceFixture(t)
			if terminal {
				l.State = "terminal"
			} else {
				r.Phase = "finished"
			}
			r.Binding.State = "settled"
			if err := m.saveGradleRecord(&l, r, "workload.fixture_settled"); err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			m.finishGradleProbe(l.ID, nil, errors.New("late cancellation"), nil)
			m.mu.Unlock()
			actual, err := m.get(l.ID)
			if err != nil || actual.Revision != l.Revision || actual.Workload.State != "settled" {
				t.Fatal("settled lease changed", actual, err)
			}
			record, err := m.gradleRecord(l.ID)
			if err != nil || record.Uncertain || record.Binding.State != "settled" {
				t.Fatal("late probe resurrected uncertainty", record, err)
			}
		})
	}
}
