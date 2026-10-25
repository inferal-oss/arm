package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
	"github.com/yrashk/arm/internal/snapshotlease"
	"github.com/yrashk/arm/internal/storage"
)

type snapshotFixture struct {
	m              *Manager
	source         model.Lease
	home           string
	closed         bool
	wantCloseError bool
}

func newSnapshotFixture(t *testing.T) *snapshotFixture {
	t.Helper()
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(base, "authority")
	m, e := Open(home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 256 << 20})
	if e != nil {
		t.Fatal(e)
	}
	f := &snapshotFixture{m: m, home: home}
	t.Cleanup(func() {
		if !f.closed {
			e := f.m.Close()
			if f.wantCloseError {
				if e == nil {
					t.Error("unknown boundary close falsely succeeded")
				}
			} else if e != nil {
				t.Error(e)
			}
			f.closed = true
		}
	})
	f.source, e = m.Acquire(model.Request{Kind: "storage", Key: "snapshot-source", Reason: "actual private source", TTL: 5 * time.Minute, Resources: model.Resources{Disk: 1 << 20}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(f.source.ManagedDir, "payload"), []byte("selected immutable fixture bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	return f
}
func snapshotRequest(f *snapshotFixture, key string) snapshotlease.Request {
	return snapshotlease.Request{Schema: 1, Key: key, Source: f.source.ID, SourceRevision: f.source.Revision, Selections: []model.ExportSelection{{Source: "payload", Path: "payload"}}, Policy: snapshotlease.IdlePolicy, Reason: "reserve exact private no-work snapshot", WorkTTL: time.Minute, Retention: 3 * time.Minute, ContentCeiling: 1 << 20, Resources: model.Resources{CPU: 1, Memory: 128 << 20}}
}
func snapshotCount(t *testing.T, m *Manager, query string, args ...any) int {
	t.Helper()
	var n int
	if e := m.db.QueryRow(query, args...).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func snapshotStored(t *testing.T, m *Manager) snapshotlease.Snapshot {
	t.Helper()
	s, e := snapshotlease.Load(t.Context(), m.db, m.host, m.owner, m.home)
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func TestSnapshotReservationAtomicNoPlanning(t *testing.T) {
	f := newSnapshotFixture(t)
	q := snapshotRequest(f, "atomic")
	if e := os.Chmod(filepath.Join(f.source.ManagedDir, "payload"), 0000); e != nil {
		t.Fatal(e)
	}
	providerBefore := snapshotDirectoryNames(t, filepath.Join(f.home, "storage"))
	r, e := f.m.reserveSnapshot(t.Context(), q)
	if e != nil {
		t.Fatal(e)
	}
	if r.Phase != "reserved-no-work" || r.PlanningAttempted || r.AllocationAttempted {
		t.Fatalf("unexpected work: %+v", r)
	}
	if _, e = os.Lstat(filepath.Join(f.home, "storage", r.DestinationLease)); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("destination allocated: %v", e)
	}
	if after := snapshotDirectoryNames(t, filepath.Join(f.home, "storage")); !reflect.DeepEqual(providerBefore, after) {
		t.Fatal("reservation changed complete provider namespace", providerBefore, after)
	}
	state := snapshotStored(t, f.m)
	if len(state.Copies) != 1 || len(state.Journal) != 1 || state.ReservedSlots != 1 {
		t.Fatal(state)
	}
	for _, id := range []string{r.WorkLease, r.DestinationLease} {
		l, e := f.m.get(id)
		if e != nil || !consuming(l.State) || l.Budget == nil {
			t.Fatal(l, e)
		}
		if snapshotCount(t, f.m, `SELECT count(*) FROM lease_budget_bindings WHERE lease=?`, id) != 1 {
			t.Fatal("binding absent")
		}
	}
	if busy, e := f.m.shareStorageBusy(f.source.ID); e != nil || busy != r.ID {
		t.Fatal("source pin absent", busy, e)
	}
	before := snapshotCount(t, f.m, `SELECT count(*) FROM events`)
	got, e := f.m.reserveSnapshot(t.Context(), q)
	if e != nil || got != r {
		t.Fatal("same-key replay", got, e)
	}
	if snapshotCount(t, f.m, `SELECT count(*) FROM events`) != before {
		t.Fatal("lookup wrote events")
	}
	q.Selections[0].Path = "other"
	if _, e = f.m.reserveSnapshot(t.Context(), q); e == nil {
		t.Fatal("changed immutable selection accepted")
	}
	if _, e = f.m.Acquire(model.Request{Kind: snapshotlease.WorkKind, Resources: q.Resources, TTL: time.Minute, Reason: "wire cannot construct private admission"}, nil); e == nil {
		t.Fatal("untyped work admitted")
	}
}

func snapshotDirectoryNames(t *testing.T, path string) []string {
	t.Helper()
	entries, e := os.ReadDir(path)
	if e != nil {
		t.Fatal(e)
	}
	out := []string{}
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	return out
}

func TestSnapshotReservationLastWriteRollback(t *testing.T) {
	f := newSnapshotFixture(t)
	if _, e := f.m.db.Exec(`CREATE TEMP TRIGGER reject_snapshot BEFORE INSERT ON events WHEN NEW.type='snapshot.reserved_no_work' BEGIN SELECT RAISE(ABORT,'after both promises and copy intent'); END`); e != nil {
		t.Fatal(e)
	}
	before := snapshotCount(t, f.m, `SELECT count(*) FROM leases`)
	if _, e := f.m.reserveSnapshot(t.Context(), snapshotRequest(f, "rollback")); e == nil {
		t.Fatal("injected SQL failure accepted")
	}
	if snapshotCount(t, f.m, `SELECT count(*) FROM leases`) != before || snapshotCount(t, f.m, `SELECT count(*) FROM sqlite_schema WHERE name GLOB 'snapshot_*'`) != 0 {
		t.Fatal("partial leases/schema committed")
	}
	if busy, e := f.m.shareStorageBusy(f.source.ID); e != nil || busy != "" {
		t.Fatal("rolled-back pin leaked", busy, e)
	}
}

func TestSnapshotReservationRealCommitAckLossLookupOnly(t *testing.T) {
	f := newSnapshotFixture(t)
	q := snapshotRequest(f, "lost-ack")
	f.m.snapshots.commitAckFailure = errors.New("reported loss AFTER actual Commit")
	if _, e := f.m.reserveSnapshot(t.Context(), q); e == nil {
		t.Fatal("lost acknowledgment accepted")
	}
	f.wantCloseError = true
	r, found, e := f.m.lookupSnapshotReservation(t.Context(), q.Key)
	if e != nil || !found || r.Phase != "reserved-no-work" {
		t.Fatal(r, found, e)
	}
	before := snapshotCount(t, f.m, `SELECT count(*) FROM leases`)
	if _, e = f.m.reserveSnapshot(t.Context(), q); e == nil {
		t.Fatal("latched boundary retried admission")
	}
	if e = f.m.cancelSnapshotReservation(t.Context(), q.Key, 1, "unsafe stale cache"); e == nil {
		t.Fatal("mutation cleared uncertainty")
	}
	if snapshotCount(t, f.m, `SELECT count(*) FROM leases`) != before {
		t.Fatal("unknown created replacement promise")
	}
	if _, e = os.Lstat(filepath.Join(f.home, "storage", r.DestinationLease)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("unknown admitted allocation", e)
	}
}

func TestSnapshotReservationPreCancelDurableAndUnknown(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "known", true: "lost-ack"}[lost], func(t *testing.T) {
			f := newSnapshotFixture(t)
			q := snapshotRequest(f, "early-cancel")
			if lost {
				f.m.snapshots.commitAckFailure = errors.New("reported cancel loss AFTER actual Commit")
				f.wantCloseError = true
			}
			e := f.m.cancelSnapshotReservation(t.Context(), q.Key, 0, "cancel before admission")
			if (e != nil) != lost {
				t.Fatal(e)
			}
			if snapshotCount(t, f.m, `SELECT count(*) FROM cancelled_requests WHERE key=?`, snapshotlease.LeaseKey(f.m.host, q.Key, "request")) != 1 {
				t.Fatal("fence not durable")
			}
			lookup, readErr := f.m.lookupSnapshotRequest(t.Context(), q.Key)
			if readErr != nil || lookup.Present || !lookup.RequestFenced {
				t.Fatal("exact read did not recover durable pre-cancel", lookup, readErr)
			}
			if snapshotCount(t, f.m, `SELECT count(*) FROM sqlite_schema WHERE name GLOB 'snapshot_*'`) != 0 {
				t.Fatal("pre-cancel initialized optional group")
			}
			if _, e = f.m.reserveSnapshot(t.Context(), q); e == nil {
				t.Fatal("cancelled key admitted")
			}
		})
	}
}

func TestSnapshotReservationPreCancelRollbackHasNoPhantomFence(t *testing.T) {
	f := newSnapshotFixture(t)
	q := snapshotRequest(f, "cancel-rollback")
	if _, e := f.m.db.Exec(`CREATE TEMP TRIGGER reject_snapshot_cancel BEFORE INSERT ON events WHEN NEW.type='snapshot.request_cancelled' BEGIN SELECT RAISE(ABORT,'failure after fence insert before commit'); END`); e != nil {
		t.Fatal(e)
	}
	if e := f.m.cancelSnapshotReservation(t.Context(), q.Key, 0, "rolled back cancellation"); e == nil {
		t.Fatal("injected cancellation failure accepted")
	}
	if snapshotCount(t, f.m, `SELECT count(*) FROM cancelled_requests WHERE key=?`, snapshotlease.LeaseKey(f.m.host, q.Key, "request")) != 0 {
		t.Fatal("uncommitted fence survived rollback")
	}
	if f.m.snapshots.failure != nil {
		t.Fatal("known precommit rollback mislabeled unknown", f.m.snapshots.failure)
	}
	if _, e := f.m.reserveSnapshot(t.Context(), q); e != nil {
		t.Fatal("phantom memory fence blocked actual admission", e)
	}
}

func TestSnapshotReservationOrdinaryEndUsesAtomicNoWorkCancel(t *testing.T) {
	f := newSnapshotFixture(t)
	r, e := f.m.reserveSnapshot(t.Context(), snapshotRequest(f, "ordinary-end"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.m.End(r.WorkLease, "released"); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{r.WorkLease, r.DestinationLease} {
		l := mustSnapshotLease(t, f.m, id)
		if l.State != "terminal" || l.Outcome != "cancelled" {
			t.Fatal("ordinary end split reserved bundle", l)
		}
	}
	if mustSnapshotLease(t, f.m, f.source.ID).State != "active" {
		t.Fatal("snapshot cancellation ended independent original source")
	}
	if snapshotStored(t, f.m).Copies[r.ID].Phase != "cancelled-no-work" {
		t.Fatal("ordinary end skipped exact copy decision")
	}
}

func TestSnapshotReservationCancelRealCommitAckLoss(t *testing.T) {
	f := newSnapshotFixture(t)
	r, e := f.m.reserveSnapshot(t.Context(), snapshotRequest(f, "cancel-ack"))
	if e != nil {
		t.Fatal(e)
	}
	f.m.snapshots.commitAckFailure = errors.New("reported cancel acknowledgment loss AFTER real Commit")
	if e = f.m.cancelSnapshotReservation(t.Context(), r.Key, 1, "actual known-no-work cancel"); e == nil {
		t.Fatal("lost cancel acknowledgment accepted")
	}
	f.wantCloseError = true
	lookup, e := f.m.lookupSnapshotRequest(t.Context(), r.Key)
	if e != nil || !lookup.Present || lookup.Record.Phase != "cancelled-no-work" {
		t.Fatal("fresh exact read did not recover actual cancellation", lookup, e)
	}
	for _, id := range []string{r.WorkLease, r.DestinationLease} {
		l := mustSnapshotLease(t, f.m, id)
		if l.State != "terminal" || l.Outcome != "cancelled" {
			t.Fatal("actual committed cancellation split promises", l)
		}
	}
	if e = f.m.cancelSnapshotReservation(t.Context(), r.Key, 1, "actual known-no-work cancel"); e == nil {
		t.Fatal("uncertain original boundary retried mutation")
	}
	if f.m.snapshots.state.Copies[r.ID].Phase != "reserved-no-work" {
		t.Fatal("lost acknowledgement optimistically replaced original cache")
	}
}

func TestSnapshotReservationKnownCancelAtomicGuards(t *testing.T) {
	f := newSnapshotFixture(t)
	r, e := f.m.reserveSnapshot(t.Context(), snapshotRequest(f, "end"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.m.storageForRun(model.Request{StorageLease: f.source.ID, TTL: time.Second}); e == nil {
		t.Fatal("writer crossed source pin")
	}
	if _, e = f.m.Renew(r.WorkLease, time.Minute); e == nil {
		t.Fatal("work renewal changed immutable scope")
	}
	destination, _ := f.m.get(r.DestinationLease)
	if _, e = f.m.provisionStorage(destination); e == nil {
		t.Fatal("ordinary provisioning created S destination")
	}
	if e = f.m.cancelSnapshotReservation(t.Context(), r.Key, 1, "explicit never-started cancellation"); e != nil {
		t.Fatal(e)
	}
	state := snapshotStored(t, f.m)
	r = state.Copies[r.ID]
	if r.Phase != "cancelled-no-work" || state.ReservedSlots != 0 || len(state.Journal) != 2 {
		t.Fatal(state)
	}
	for _, id := range []string{r.WorkLease, r.DestinationLease} {
		l, _ := f.m.get(id)
		if l.State != "terminal" || l.Outcome != "cancelled" {
			t.Fatal(l)
		}
	}
	if busy, e := f.m.shareStorageBusy(f.source.ID); e != nil || busy != "" {
		t.Fatal("source pin not released after exact cancel", busy, e)
	}
	if _, e = f.m.reserveSnapshot(t.Context(), snapshotRequest(f, "end")); e != nil {
		t.Fatal("cancelled receipt lookup failed", e)
	}
}

func TestSnapshotReservationPendingControlAndOriginalCloseUnknown(t *testing.T) {
	f := newSnapshotFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	f.m.snapshots.preflight = func(ctx context.Context, p *storage.Provider, a model.Artifact) (storage.SnapshotReservationObservation, error) {
		close(entered)
		<-release
		out, e := p.SnapshotReservationPreflight(ctx, a)
		out.Native.Unknown = true
		out.Native.Closed = false
		return out, errors.Join(e, errors.New("explicit reported unknown AFTER real preflight closes"))
	}
	done := make(chan error, 1)
	go func() { _, e := f.m.reserveSnapshot(t.Context(), snapshotRequest(f, "control")); done <- e }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("control not registered")
	}
	f.m.mu.Lock()
	impact, _, e := f.m.nodeStopCensus(nodestop.Impact{}, nodestop.LeaseReadScope{})
	f.m.mu.Unlock()
	if e != nil || len(impact.Blockers) == 0 {
		t.Fatal("pending original control omitted", impact, e)
	}
	close(release)
	if e = <-done; e == nil {
		t.Fatal("reported unknown close admitted")
	}
	f.wantCloseError = true
	f.m.mu.Lock()
	controls := f.m.snapshotStopControls()
	f.m.mu.Unlock()
	if len(controls) != 1 || !controls[0].Returned || !controls[0].Unknown {
		t.Fatal(controls)
	}
	if snapshotCount(t, f.m, `SELECT count(*) FROM sqlite_schema WHERE name GLOB 'snapshot_*'`) != 0 {
		t.Fatal("unknown preflight created promises")
	}
}

func TestSnapshotReservationRequestClosedAndDetached(t *testing.T) {
	f := newSnapshotFixture(t)
	q := snapshotRequest(f, "codec")
	for _, mutate := range []func(*snapshotlease.Request){func(q *snapshotlease.Request) { q.Policy = "verified-copy-v1" }, func(q *snapshotlease.Request) { q.ContentCeiling = -1 }, func(q *snapshotlease.Request) { q.Resources.Memory = 1 }, func(q *snapshotlease.Request) {
		q.Selections = []model.ExportSelection{{Source: "../foreign", Path: "payload"}}
	}} {
		bad := q
		mutate(&bad)
		if _, e := f.m.reserveSnapshot(t.Context(), bad); e == nil {
			t.Fatal("closed request accepted", bad)
		}
	}
	raw, _ := snapshotlease.Canonical(q, snapshotlease.MaxRequestBytes)
	for _, bad := range [][]byte{append(raw, byte(' ')), []byte(strings.Replace(string(raw), `"schema":1`, `"schema":1,"schema":1`, 1)), []byte(strings.Replace(string(raw), `"policy":"idle-native-v1"`, `"Policy":"idle-native-v1"`, 1))} {
		if _, e := snapshotlease.Decode[snapshotlease.Request](bad, snapshotlease.MaxRequestBytes); e == nil {
			t.Fatal("noncanonical request accepted")
		}
	}
	r, e := f.m.reserveSnapshot(t.Context(), q)
	if e != nil {
		t.Fatal(e)
	}
	q.Selections[0].Source = "changed"
	state := snapshotStored(t, f.m)
	if reflect.DeepEqual(state.Requests[r.ID].Selections, q.Selections) {
		t.Fatal("caller alias changed immutable request")
	}
	r.AllocationAttempted = true
	altered, e := snapshotlease.Canonical(r, snapshotlease.MaxRecordBytes)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := snapshotlease.Decode[snapshotlease.Record](altered, snapshotlease.MaxRecordBytes)
	if e != nil {
		t.Fatal("canonical semantic-negative did not reach record validator", e)
	}
	if decoded.Validate() == nil {
		t.Fatal("attempted allocation accepted by no-work record validator")
	}
}
