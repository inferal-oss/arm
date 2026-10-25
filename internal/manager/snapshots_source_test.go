package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/snapshotlease"
	"github.com/yrashk/arm/internal/storage"
)

func snapshotSourceRow(t *testing.T, m *Manager, rowID int64) string {
	t.Helper()
	var raw string
	if e := m.db.QueryRow(`SELECT json_array(typeof(id),quote(id),typeof(request_key),quote(request_key),typeof(data),hex(data),typeof(run),quote(run)) FROM leases WHERE rowid=?`, rowID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestSnapshotReservationSourceSQLBinding(t *testing.T) {
	foreign := strings.Repeat("b", 32)
	cases := []struct {
		name, update string
		args         []any
	}{
		{"body-id", `UPDATE leases SET data=json_set(data,'$.id',?) WHERE rowid=?`, []any{foreign}},
		{"artifact-name", `UPDATE leases SET data=json_set(data,'$.artifact.name',?) WHERE rowid=?`, []any{foreign}},
		{"artifact-path", `UPDATE leases SET data=json_set(data,'$.artifact.path',?) WHERE rowid=?`, []any{"/foreign/managed/allocation"}},
		{"managed-directory", `UPDATE leases SET data=json_set(data,'$.managed_dir',?) WHERE rowid=?`, []any{"/foreign/managed/allocation"}},
		{"request-key", `UPDATE leases SET request_key=? WHERE rowid=?`, []any{"another-request"}},
		{"request-key-null", `UPDATE leases SET request_key=NULL WHERE rowid=?`, nil},
		{"request-key-blob", `UPDATE leases SET request_key=CAST(request_key AS BLOB) WHERE rowid=?`, nil},
		{"run-present", `UPDATE leases SET run='{}' WHERE rowid=?`, nil},
		{"id-sql-type", `UPDATE leases SET id=CAST(id AS BLOB) WHERE rowid=?`, nil},
		{"body-sql-type", `UPDATE leases SET data=CAST(data AS BLOB) WHERE rowid=?`, nil},
		{"body-byte-bound", `UPDATE leases SET data=? WHERE rowid=?`, []any{strings.Repeat("x", (1<<20)+1)}},
	}
	for _, cut := range []string{"before-control", "inside-source-transaction"} {
		t.Run(cut, func(t *testing.T) {
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					f := newSnapshotFixture(t)
					q := snapshotRequest(f, "bad-source")
					var e error
					var rowID int64
					if e = f.m.db.QueryRow(`SELECT rowid FROM leases WHERE id=?`, f.source.ID).Scan(&rowID); e != nil {
						t.Fatal(e)
					}
					args := append(append([]any(nil), test.args...), rowID)
					if _, e = f.m.db.Exec(test.update, args...); e != nil {
						t.Fatal(e)
					}
					before := snapshotSourceRow(t, f.m, rowID)
					leases := snapshotCount(t, f.m, `SELECT count(*) FROM leases`)
					events := snapshotCount(t, f.m, `SELECT count(*) FROM events`)
					preflights := 0
					f.m.snapshots.preflight = func(ctx context.Context, p *storage.Provider, a model.Artifact) (storage.SnapshotReservationObservation, error) {
						preflights++
						return p.SnapshotReservationPreflight(ctx, a)
					}
					if cut == "before-control" {
						_, e = f.m.reserveSnapshot(t.Context(), q)
					} else {
						// Call the actual fixed source reader in a real SQL
						// transaction, independently of the earlier manager
						// guards. Production admission uses this same read
						// before its first optional DDL or promise write.
						tx, beginErr := f.m.db.BeginTx(t.Context(), nil)
						if beginErr != nil {
							t.Fatal(beginErr)
						}
						_, e = snapshotlease.ReadSourceLease(t.Context(), tx, q.Source, f.m.host, f.m.owner, f.home)
						if rollbackErr := tx.Rollback(); rollbackErr != nil {
							t.Fatal(rollbackErr)
						}
					}
					if e == nil {
						t.Fatal("malformed physical source admitted", cut, test.name)
					}
					if preflights != 0 || len(f.m.snapshots.controls) != 0 || len(f.m.snapshots.state.Copies) != 0 {
						t.Fatal("malformed source registered/retargeted control or copy", preflights, f.m.snapshots.controls)
					}
					if snapshotCount(t, f.m, `SELECT count(*) FROM leases`) != leases || snapshotCount(t, f.m, `SELECT count(*) FROM events`) != events || snapshotCount(t, f.m, `SELECT count(*) FROM sqlite_schema WHERE name GLOB 'snapshot_*'`) != 0 {
						t.Fatal("source refusal committed promises/events/optional schema")
					}
					if snapshotSourceRow(t, f.m, rowID) != before || f.m.snapshots.failure != nil || f.m.snapshots.commitPending {
						t.Fatal("source refusal repaired data or fabricated commit uncertainty")
					}
				})
			}
		})
	}
}

func TestSnapshotReservationUnkeyedSourceUsesSQLNull(t *testing.T) {
	f := newSnapshotFixture(t)
	q := f.source.Request
	q.Key = ""
	source, e := f.m.Acquire(q, nil)
	if e != nil {
		t.Fatal(e)
	}
	request := snapshotRequest(f, "unkeyed-source")
	request.Source, request.SourceRevision = source.ID, source.Revision
	r, e := f.m.reserveSnapshot(t.Context(), request)
	if e != nil || r.Source != source.ID {
		t.Fatal("valid original SQL NULL source key refused", r, e)
	}
}

func TestSnapshotReservationRetainedSourceSQLBinding(t *testing.T) {
	for _, test := range []struct{ name, query string }{
		{"request-key", `UPDATE leases SET request_key='foreign-key-after-reservation' WHERE id=?`},
		{"run-present", `UPDATE leases SET run='{}' WHERE id=?`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSnapshotFixture(t)
			q := snapshotRequest(f, "retained-source")
			r, e := f.m.reserveSnapshot(t.Context(), q)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.m.db.Exec(test.query, f.source.ID); e != nil {
				t.Fatal(e)
			}
			if _, e = f.m.lookupSnapshotRequest(t.Context(), q.Key); e == nil {
				t.Fatal("exact-key recovery accepted a replaced source SQL binding")
			}
			for _, id := range []string{r.WorkLease, r.DestinationLease} {
				if !consuming(mustSnapshotLease(t, f.m, id).State) {
					t.Fatal("invalid retained source released a promise")
				}
			}
			e = f.m.Close()
			f.closed = true
			if e == nil {
				t.Fatal("source evidence failure was lost at original Manager close")
			}
			before := snapshotReadonlyCopy(t, f.home, r.ID)
			if m, e := Open(f.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 256 << 20}); e == nil {
				closeErr := m.Close()
				t.Fatal("startup accepted/repaired retained source SQL binding", closeErr)
			}
			if snapshotReadonlyCopy(t, f.home, r.ID) != before {
				t.Fatal("failed recovery changed the retained copy record")
			}
			if _, e = os.Lstat(filepath.Join(f.home, "storage", r.DestinationLease)); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("failed source recovery allocated destination", e)
			}
		})
	}
}

func TestSnapshotReservationConcurrentSourceKeyConflict(t *testing.T) {
	f := newSnapshotFixture(t)
	secondRequest := f.source.Request
	secondRequest.Key = "second-independent-source"
	second, e := f.m.Acquire(secondRequest, nil)
	if e != nil {
		t.Fatal(e)
	}
	q1 := snapshotRequest(f, "same-key-two-sources")
	q2 := q1
	q2.Source, q2.SourceRevision = second.ID, second.Revision
	entered := make(chan string, 2)
	firstRelease, secondRelease := make(chan struct{}), make(chan struct{})
	var firstOnce, secondOnce sync.Once
	releaseFirst := func() { firstOnce.Do(func() { close(firstRelease) }) }
	releaseSecond := func() { secondOnce.Do(func() { close(secondRelease) }) }
	var workers sync.WaitGroup
	defer func() { releaseFirst(); releaseSecond(); workers.Wait() }()
	f.m.snapshots.preflight = func(ctx context.Context, p *storage.Provider, a model.Artifact) (storage.SnapshotReservationObservation, error) {
		// Both helpers really open/validate/close their original descriptors.
		// Hold their actual closed receipts before Manager receives them; this
		// is not a claim that native descriptors remain open while paused.
		out, err := p.SnapshotReservationPreflight(ctx, a)
		if err != nil {
			return out, err
		}
		entered <- a.Name
		if a.Name == f.source.ID {
			<-firstRelease
		} else {
			<-secondRelease
		}
		return out, nil
	}
	type result struct {
		record snapshotlease.Record
		err    error
	}
	firstDone, secondDone := make(chan result, 1), make(chan result, 1)
	start := func(q snapshotlease.Request, done chan<- result) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			r, e := f.m.reserveSnapshot(t.Context(), q)
			done <- result{r, e}
		}()
	}
	waitEntered := func(want string) {
		t.Helper()
		select {
		case got := <-entered:
			if got != want {
				t.Fatal("wrong original source receipt held", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("original real preflight did not reach held receipt", want)
		}
	}
	start(q1, firstDone)
	waitEntered(f.source.ID)
	start(q2, secondDone)
	waitEntered(second.ID)
	f.m.mu.Lock()
	controls := f.m.snapshotStopControls()
	f.m.mu.Unlock()
	if len(controls) != 2 || controls[0].Source == controls[1].Source {
		t.Fatal("both actual different-source controls were not admitted", controls)
	}
	releaseFirst()
	first := <-firstDone
	if first.err != nil || first.record.Source != f.source.ID {
		t.Fatal(first)
	}
	leases, events := snapshotCount(t, f.m, `SELECT count(*) FROM leases`), snapshotCount(t, f.m, `SELECT count(*) FROM events`)
	if leases != 4 { // Two original sources plus one atomic work/destination pair.
		t.Fatal("first admission was not exactly one pair", leases)
	}
	releaseSecond()
	rejected := <-secondDone
	var typed *model.Error
	if !errors.As(rejected.err, &typed) || typed.Code != "idempotency_conflict" || rejected.record != (snapshotlease.Record{}) {
		t.Fatal("second source did not get known immutable-key conflict", rejected)
	}
	if snapshotCount(t, f.m, `SELECT count(*) FROM leases`) != leases || snapshotCount(t, f.m, `SELECT count(*) FROM events`) != events {
		t.Fatal("known conflict appended promises/events")
	}
	if len(f.m.snapshots.state.Copies) != 1 || f.m.snapshots.state.Copies[first.record.ID] != first.record || len(f.m.snapshots.controls) != 0 || f.m.snapshotPolicyError() != nil {
		t.Fatal("known conflict replaced receipt, retained control or latched boundary")
	}
	if pin, e := f.m.shareStorageBusy(second.ID); e != nil || pin != "" {
		t.Fatal("conflicting source gained a durable pin", pin, e)
	}
	if _, e = os.Lstat(filepath.Join(f.home, "storage", first.record.DestinationLease)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("no-work pair allocated a destination", e)
	}
}
