package manager

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
)

func TestNodeStopWaitCancelsQueuedStorageWithoutAllocation(t *testing.T) {
	m, actor, external := stopManagerFixture(t)
	// A fixed ceiling makes the second actual storage acquisition queue without
	// depending on how much disk happens to be free on the test machine.
	m.capacity.Disk = 1024
	storage, err := m.Acquire(storageRequest("stop-active-storage"), nil)
	if err != nil {
		t.Fatal(err)
	}
	active, err := m.Acquire(request("stop-active-cpu"), nil)
	if err != nil {
		t.Fatal(err)
	}
	queuedRequest := storageRequest("stop-queued-storage")
	queuedRequest.Queue = true
	queued, err := m.Acquire(queuedRequest, nil)
	if err != nil || queued.State != "queued" || queued.Artifact != nil {
		t.Fatalf("expected an unallocated storage request: %+v %v", queued, err)
	}
	q := stopApplication(t, m, actor, external, model.ShutdownRequest{Wait: true})
	if _, err = m.BeginNodeStop(q, actor, true, external); err != nil {
		t.Fatal(err)
	}
	queued, err = m.Inspect(queued.ID)
	if err != nil || queued.State != "terminal" || queued.Outcome != "cancelled" || queued.Artifact != nil || queued.ManagedDir != "" {
		t.Fatalf("Stop provisioned or retained the queued storage request: %+v %v", queued, err)
	}
	if _, err = os.Stat(m.storage.Path(queued.ID)); !os.IsNotExist(err) {
		t.Fatal("queued storage cancellation created a native allocation", err)
	}
	report, err := m.AdvanceShutdown()
	if err != nil || report.Phase != "draining" || report.ForceApplied || len(report.Remaining) != 2 {
		t.Fatalf("wait released existing work: %+v %v", report, err)
	}
	if _, err = m.Renew(storage.ID, 3*time.Minute); err == nil {
		t.Fatal("source renewal prolonged the accepted Stop")
	}
	if _, err = m.Acquire(request("stop-late-work"), nil); err == nil {
		t.Fatal("new work entered after durable Stop")
	}
	if _, err = m.End(active.ID, "released"); err != nil {
		t.Fatal(err)
	}
	// Handoff is an explicit ordinary settlement, not proof of reclaimed blocks.
	if _, err = m.StorageHandoff(storage.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(storage.ManagedDir); err != nil {
		t.Fatal("handoff deleted the independent allocation", err)
	}
	report, err = m.AdvanceShutdown()
	if err != nil || report.Phase != "ready" || report.ForceApplied || len(report.Remaining) != 0 {
		t.Fatalf("ordinary settlements did not finish the original wait: %+v %v", report, err)
	}
	record, present, err := m.NodeStopDrainReady(report)
	if err != nil || !present || record.Phase != "drain-ready" || record.Plan.Key != q.Key {
		t.Fatalf("readiness lost the exact Stop: %+v %v %v", record, present, err)
	}
}

func TestNodeStopForceAndTimeoutReleaseOwnedRunButRetainReplacedStorage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy model.ShutdownRequest
	}{
		{"wait-timeout", model.ShutdownRequest{Wait: true, Timeout: time.Second}},
		{"immediate-force", model.ShutdownRequest{Force: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkNodeStopOwnedRunAndReplacedStorage(t, tc.policy)
		})
	}
}

func checkNodeStopOwnedRunAndReplacedStorage(t *testing.T, policy model.ShutdownRequest) {
	t.Helper()
	m, actor, external := stopManagerFixture(t)
	storage, err := m.Acquire(storageRequest("stop-replaced-storage"), nil)
	if err != nil {
		t.Fatal(err)
	}
	// This independent native process is never registered as an ARM job. Its
	// creator owns cleanup and its single Wait even when the test fails.
	foreignRead, foreignInput, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var foreignOutput, foreignWrite *os.File
	foreign := exec.Command("/bin/cat")
	started := false
	closePipe := func(name string, file **os.File) {
		if *file != nil {
			if err := (*file).Close(); err != nil {
				t.Errorf("close unrelated cat %s: %v", name, err)
			}
			*file = nil
		}
	}
	t.Cleanup(func() {
		closePipe("input", &foreignInput) // EOF lets the original cat exit normally.
		closePipe("child input", &foreignRead)
		closePipe("child output", &foreignWrite)
		if started {
			waited := make(chan error, 1)
			go func() { waited <- foreign.Wait() }() // Exactly one original Wait.
			select {
			case err := <-waited:
				if err != nil {
					t.Errorf("unrelated cat exit after EOF: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Error("unrelated cat did not exit after EOF; killing the creator-owned child")
				if err := foreign.Process.Kill(); err != nil {
					t.Errorf("kill original unrelated cat: %v", err)
				}
				select {
				case err := <-waited:
					if err != nil {
						t.Errorf("unrelated cat forced cleanup exit: %v", err)
					}
				case <-time.After(2 * time.Second):
					t.Error("original unrelated cat Wait remains unresolved after kill")
				}
			}
		}
		closePipe("output", &foreignOutput)
	})
	foreignOutput, foreignWrite, err = os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	foreign.Stdin, foreign.Stdout = foreignRead, foreignWrite
	if err = foreign.Start(); err != nil {
		t.Fatal(err)
	}
	started = true
	closePipe("child input", &foreignRead)
	closePipe("child output", &foreignWrite)
	r := request("stop-owned-run")
	r.StorageLease = storage.ID
	run := RunRequest{Request: r, CWD: t.TempDir(), Command: []string{"/bin/sleep", "30"}}
	child, err := m.Acquire(r, &run)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	owned := m.jobs[child.ID]
	m.mu.Unlock()
	if owned == nil {
		t.Fatal("supervised execution has no original runtime owner")
	}
	q := stopApplication(t, m, actor, external, policy)
	if _, err = m.BeginNodeStop(q, actor, true, external); err != nil {
		t.Fatal(err)
	}
	if policy.Wait {
		report, err := m.AdvanceShutdown()
		if err != nil || report.ForceApplied {
			t.Fatalf("wait forced before its original deadline: %+v %v", report, err)
		}
	}
	// Replace the actual directory after admission. The force path must use the
	// original provider identity, never adopt or delete the replacement path.
	original := storage.ManagedDir + "-retained-original"
	if err = os.Rename(storage.ManagedDir, original); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(storage.ManagedDir, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(storage.ManagedDir, "unrelated")
	if err = os.WriteFile(marker, []byte("must survive force"), 0600); err != nil {
		t.Fatal(err)
	}
	// Exercise the actual monotonic wait deadline rather than overwriting it in
	// the ledger. AdvanceShutdown is the same continuation used by the backend.
	deadline := time.Now().Add(8 * time.Second)
	var report model.ShutdownReport
	for time.Now().Before(deadline) {
		report, err = m.AdvanceShutdown()
		if err != nil {
			t.Fatal(err)
		}
		if report.Phase == "blocked" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if report.Phase != "blocked" || !report.ForceApplied || len(report.Remaining) != 1 || report.Remaining[0].ID != storage.ID {
		t.Fatalf("force lost the unresolved storage obligation: %+v", report)
	}
	ended, err := m.Inspect(child.ID)
	if err != nil || ended.State != "terminal" || ended.Outcome != "released" {
		t.Fatalf("original supervised child did not settle: %+v %v", ended, err)
	}
	select {
	case <-owned.done:
	case <-time.After(time.Second):
		t.Fatal("terminal lease did not join its original process owner")
	}
	retained, err := m.Inspect(storage.ID)
	if err != nil || retained.State != "needs_attention" || retained.Artifact == nil || retained.Artifact.Identity == nil || *retained.Artifact.Identity != *storage.Artifact.Identity {
		t.Fatalf("replacement became new allocation authority: %+v %v", retained, err)
	}
	status, err := m.Status()
	if err != nil || status.Reserved.CPU != 0 || status.Reserved.Disk != storage.Request.Resources.Disk {
		t.Fatalf("unknown disk was declared free: %+v %v", status.Reserved, err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "must survive force" {
		t.Fatal("force changed unrelated replacement contents", string(data), err)
	}
	if _, err = os.Stat(original); err != nil {
		t.Fatal("force deleted the moved original allocation", err)
	}
	if err = foreignInput.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = foreignOutput.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	const ping = "unrelated process still usable\n"
	if n, err := io.WriteString(foreignInput, ping); err != nil || n != len(ping) {
		t.Fatalf("unrelated process stopped accepting input after Stop: %d %v", n, err)
	}
	reply := make([]byte, len(ping))
	if _, err = io.ReadFull(foreignOutput, reply); err != nil || string(reply) != ping {
		t.Fatal("unrelated process did not answer after Stop", string(reply), err)
	}
	if _, _, err = m.NodeStopDrainReady(report); err == nil {
		t.Fatal("blocked cleanup was certified drain-ready")
	}
	receipt, err := m.LookupNodeStop(q.Key, actor)
	if err != nil || receipt.Phase != "accepted" {
		t.Fatalf("blocked native cleanup changed the original Stop receipt: %+v %v", receipt, err)
	}
}

func TestNodeStopForceRefusesForeignLedgerObligationsWithoutMutation(t *testing.T) {
	for _, field := range []string{"host", "owner"} {
		t.Run(field, func(t *testing.T) {
			m, actor, external := stopManagerFixture(t)
			lease, err := m.Acquire(request("foreign-"+field), nil)
			if err != nil {
				t.Fatal(err)
			}
			// A retained foreign row is recovery evidence, not local signal or
			// provider authority. Only the fixture changes its provenance.
			if field == "host" {
				lease.Host = "foreign-host"
			} else {
				lease.Owner = "foreign-owner"
			}
			if err = m.put(lease, "fixture.foreign-obligation"); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(lease)
			q := stopApplication(t, m, actor, external, model.ShutdownRequest{Force: true})
			if _, err = m.BeginNodeStop(q, actor, true, external); err == nil {
				t.Fatal("force adopted a foreign obligation")
			}
			after, err := m.get(lease.ID)
			encoded, _ := json.Marshal(after)
			if err != nil || !bytes.Equal(before, encoded) || m.draining {
				t.Fatalf("refused Stop changed foreign work or admission: %+v %v", after, err)
			}
			if _, present, err := m.NodeStopDesiredState(); err != nil || present {
				t.Fatal("refused Stop committed a desired-state fence", present, err)
			}
		})
	}
}
