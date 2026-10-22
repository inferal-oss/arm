package manager

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharelease"
)

func shareLoseNextCommitAcknowledgment(m *Manager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shares.commitAckFailure = errors.New("private fixture lost durable commit acknowledgment")
}
func shareAssertUnavailable(t *testing.T, m *Manager, key string) {
	t.Helper()
	if _, _, err := m.LookupSharePreparation(key); errorCode(err) != "share_evidence_unavailable" {
		t.Fatal("stale cache reported an exact-key outcome", err)
	}
	if _, err := m.StorageHandoff(mustShareStorage(t, m)); errorCode(err) != "share_evidence_unavailable" {
		t.Fatal("uncertain commit permitted storage handoff", err)
	}
}
func mustShareStorage(t *testing.T, m *Manager) string {
	t.Helper()
	list, err := m.StorageList()
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	return list[0].ID
}
func TestShareAdmissionLostCommitAcknowledgmentNeverReportsFalseAbsence(t *testing.T) {
	f := newShareFixture(t)
	var native atomic.Int32
	f.m.shares.beforeNativeBuild = func(_ context.Context) { native.Add(1) }
	q := shareRequest(f.storage, "admission-ack-lost")
	shareLoseNextCommitAcknowledgment(f.m)
	if _, err := f.m.PrepareShare(t.Context(), q); errorCode(err) != "share_outcome_unknown" {
		t.Fatal(err)
	}
	if native.Load() != 0 {
		t.Fatal("uncertain admission started native work")
	}
	snapshot, err := sharelease.Load(t.Context(), f.m.db, f.m.host, f.m.owner)
	if err != nil || len(snapshot.Publications) != 1 {
		t.Fatal("fault must follow actual durable commit", err)
	}
	var p sharelease.Publication
	for _, v := range snapshot.Publications {
		p = v
	}
	l, err := f.m.Inspect(p.Lease)
	if err != nil || l.State != "active" {
		t.Fatal("durable ordinary obligation missing", l, err)
	}
	if len(f.m.shares.state.Publications) != 0 {
		t.Fatal("fixture did not exercise stale empty cache")
	}
	shareAssertUnavailable(t, f.m, q.Key)
	if _, err = f.m.PrepareShare(t.Context(), q); errorCode(err) != "share_evidence_unavailable" {
		t.Fatal("automatic/repeated dispatch accepted", err)
	}
	if _, err = f.m.End(p.Lease, "released"); errorCode(err) != "share_evidence_unavailable" {
		t.Fatal("unverified outcome released credits", err)
	}
	safe, err := f.m.Acquire(model.Request{Resources: model.Resources{CPU: .25}, TTL: time.Minute, Reason: "independent work while share acknowledgment is unknown", Key: "ack-safe"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.m.End(safe.ID, "released")
	f.close()
	reopened, err := Open(f.m.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	exact, found, err := reopened.LookupSharePreparation(q.Key)
	if err != nil || !found || exact.ID != p.ID || exact.State != "needs_attention" || len(reopened.shares.workers) != 0 {
		t.Fatal("restart did not restore exact unknown receipt without replay", exact, found, err)
	}
}

func TestSharePhaseLostCommitAcknowledgmentNeverReportsOldPhase(t *testing.T) {
	f := newShareFixture(t)
	entered, cancelled, release := blockShareNativeIO(t, f.m)
	p, err := f.m.PrepareShare(t.Context(), shareRequest(f.storage, "phase-ack-lost"))
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	shareLoseNextCommitAcknowledgment(f.m)
	// The exact ending transaction commits before its acknowledgment is lost.
	if _, err = f.m.End(p.Lease, "released"); errorCode(err) != "share_outcome_unknown" {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("uncertain commit did not cancel live native worker")
	}
	actual, err := sharelease.Load(t.Context(), f.m.db, f.m.host, f.m.owner)
	if err != nil || actual.Publications[p.ID].State != "ending" {
		t.Fatal("ending receipt not durable", err)
	}
	shareAssertUnavailable(t, f.m, p.Request.Key)
	status, err := f.m.Status()
	if err != nil || status.Reserved.CPU != 1 || status.Reserved.Memory != p.Request.Resources.Memory {
		t.Fatal("unknown phase freed native claims", status.Reserved, err)
	}
	release()
	shareWait(t, func() bool { f.m.mu.Lock(); defer f.m.mu.Unlock(); return len(f.m.shares.workers) == 0 })
	shareAssertUnavailable(t, f.m, p.Request.Key)
	l, _ := f.m.Inspect(p.Lease)
	if l.State != "ending" {
		t.Fatal("lost receipt was silently replayed or settled", l)
	}
}

func TestShareTerminalLostCommitAcknowledgmentRequiresExactStartupRead(t *testing.T) {
	f := newShareFixture(t)
	p, err := f.m.PrepareShare(t.Context(), shareRequest(f.storage, "terminal-ack-lost"))
	if err != nil {
		t.Fatal(err)
	}
	shareWait(t, func() bool { current, e := f.m.InspectShare(p.ID); return e == nil && current.State == "prepared" })
	shareLoseNextCommitAcknowledgment(f.m)
	if _, err = f.m.End(p.Lease, "released"); errorCode(err) != "share_outcome_unknown" {
		t.Fatal(err)
	}
	actual, err := sharelease.Load(t.Context(), f.m.db, f.m.host, f.m.owner)
	if err != nil || actual.Publications[p.ID].State != "cancelled" {
		t.Fatal("terminal transaction not durable", err)
	}
	shareAssertUnavailable(t, f.m, p.Request.Key)
	f.close()
	reopened, err := Open(f.m.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	exact, found, err := reopened.LookupSharePreparation(p.Request.Key)
	if err != nil || !found || exact.ID != p.ID || exact.State != "cancelled" || exact.Retired == nil {
		t.Fatal("startup did not restore exact durable terminal outcome", exact, found, err)
	}
	var count int
	if err = reopened.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='share.cancelled' AND entity=?`, p.Lease).Scan(&count); err != nil || count != 1 {
		t.Fatal("terminal mutation was replayed", count, err)
	}
}
