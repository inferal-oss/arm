package manager

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"getarm.org/internal/model"
)

func errorCode(err error) string {
	var api *model.Error
	if errors.As(err, &api) {
		return api.Code
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestLeaseActionChecksRevisionUnderMutationLock(t *testing.T) {
	m := newManager(t)
	l, err := m.Acquire(request("reviewed-lease"), nil)
	if err != nil {
		t.Fatal(err)
	}
	var winners, conflicts atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, err := m.LeaseAction(model.LeaseAction{ID: l.ID, Revision: l.Revision, Action: "renew", TTL: 2 * time.Minute})
			if err == nil {
				winners.Add(1)
			} else if errorCode(err) == "revision_conflict" {
				conflicts.Add(1)
			} else {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 || conflicts.Load() != 19 {
		t.Fatalf("winners=%d conflicts=%d", winners.Load(), conflicts.Load())
	}
	if _, err = m.LeaseAction(model.LeaseAction{ID: l.ID, Revision: l.Revision, Action: "release"}); errorCode(err) != "revision_conflict" {
		t.Fatalf("stale release: %v", err)
	}
	current, _ := m.Inspect(l.ID)
	if current.State != "active" || current.Revision != l.Revision+1 {
		t.Fatalf("stale action changed lease: %+v", current)
	}
	for _, bad := range []model.LeaseAction{{ID: l.ID, Action: "release"}, {ID: l.ID, Revision: current.Revision, Action: "release", TTL: time.Second}, {ID: l.ID, Revision: current.Revision, Action: "unknown"}} {
		if _, err = m.LeaseAction(bad); errorCode(err) != "invalid_request" {
			t.Fatalf("invalid action: %v", err)
		}
	}
	ended, err := m.LeaseAction(model.LeaseAction{ID: l.ID, Revision: current.Revision, Action: "release"})
	if err != nil || ended.State != "terminal" || ended.Outcome != "released" {
		t.Fatalf("guarded release: %+v %v", ended, err)
	}
}

func TestLeaseActionStorageGuardsPreserveFilesAndOwnership(t *testing.T) {
	m := newManager(t)
	l, err := m.Acquire(model.Request{Reason: "guarded storage fixture", Kind: "storage", Resources: model.Resources{Disk: 1024}, TTL: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"storage_cleanup", "storage_handoff", "release", "cancel"} {
		if _, err = m.LeaseAction(model.LeaseAction{ID: l.ID, Revision: l.Revision + 1, Action: action}); errorCode(err) != "revision_conflict" {
			t.Fatalf("stale %s: %v", action, err)
		}
	}
	current, _ := m.Inspect(l.ID)
	if current.State != "active" || current.Artifact.Phase != "owned" {
		t.Fatalf("stale action changed ownership: %+v", current)
	}
	handed, err := m.LeaseAction(model.LeaseAction{ID: l.ID, Revision: l.Revision, Action: "storage_handoff"})
	if err != nil || handed.Outcome != "handed_off" || handed.Artifact.Phase != "handed_off" {
		t.Fatalf("handoff: %+v %v", handed, err)
	}
}

func TestLeaseActionCancellationUsesExistingSupervision(t *testing.T) {
	m := newManager(t)
	r := request("guarded-supervised")
	l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sleep", "20"}, CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	current, err := m.Renew(l.ID, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.LeaseAction(model.LeaseAction{ID: l.ID, Revision: l.Revision, Action: "cancel"}); errorCode(err) != "revision_conflict" {
		t.Fatal("stale cancellation accepted", err)
	}
	if ended, err := m.LeaseAction(model.LeaseAction{ID: l.ID, Revision: current.Revision, Action: "cancel"}); err != nil || ended.State != "ending" {
		t.Fatalf("cancel: %+v %v", ended, err)
	}
	eventually(t, func() bool { ended, _ := m.Inspect(l.ID); return ended.State == "terminal" })
}
