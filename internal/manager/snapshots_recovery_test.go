package manager

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/snapshotlease"
)

func TestSnapshotReservationReopenNeverProvisions(t *testing.T) {
	f := newSnapshotFixture(t)
	q := snapshotRequest(f, "reopen")
	r, e := f.m.reserveSnapshot(t.Context(), q)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.m.Close(); e != nil {
		t.Fatal(e)
	}
	f.closed = true
	m, e := Open(f.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 256 << 20})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := m.Close(); e != nil {
			t.Error(e)
		}
	}()
	got, found, e := m.lookupSnapshotReservation(t.Context(), q.Key)
	if e != nil || !found || got != r {
		t.Fatal(got, found, e)
	}
	if _, e = os.Lstat(filepath.Join(f.home, "storage", r.DestinationLease)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("restart allocated destination", e)
	}
	if _, e = m.provisionStorage(mustSnapshotLease(t, m, r.DestinationLease)); e == nil {
		t.Fatal("restart ordinary provision bypassed exact S binding")
	}
	if e = m.cancelSnapshotReservation(t.Context(), r.Key, 1, "reviewed no-work cancellation after restart"); e != nil {
		t.Fatal(e)
	}
	if snapshotStored(t, m).Copies[r.ID].Phase != "cancelled-no-work" {
		t.Fatal("restarted cancellation did not settle")
	}
}
func mustSnapshotLease(t *testing.T, m *Manager, id string) model.Lease {
	t.Helper()
	l, e := m.get(id)
	if e != nil {
		t.Fatal(e)
	}
	return l
}

func TestSnapshotReservationStartupCorruptionAndOrphanWork(t *testing.T) {
	cases := []struct {
		name  string
		query string
		args  func(snapshotlease.Record) []any
	}{
		{"partial-schema", `DROP TABLE snapshot_copy_parts`, nil},
		{"missing-work", `DELETE FROM leases WHERE id=?`, func(r snapshotlease.Record) []any { return []any{r.WorkLease} }},
		{"wrong-part", `UPDATE snapshot_copy_parts SET data=CAST('forged request' AS BLOB)`, nil},
		{"wrong-part-sql-type", `UPDATE snapshot_copy_parts SET data=CAST(data AS TEXT)`, nil},
		{"wrong-key-sql-type", `UPDATE snapshot_copies SET request_key=CAST(request_key AS BLOB)`, nil},
		{"aliased-marker", `UPDATE metadata SET key='Snapshot_Copy_Schema' WHERE key='snapshot_copy_schema'`, nil},
		{"missing-journal", `DELETE FROM snapshot_copy_journal`, nil},
		{"unknown-object", `CREATE TRIGGER snapshot_extra AFTER INSERT ON snapshot_copies BEGIN SELECT 1; END`, nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f := newSnapshotFixture(t)
			r, e := f.m.reserveSnapshot(t.Context(), snapshotRequest(f, test.name))
			if e != nil {
				t.Fatal(e)
			}
			var args []any
			if test.args != nil {
				args = test.args(r)
			}
			if _, e = f.m.db.Exec(test.query, args...); e != nil {
				t.Fatal(e)
			}
			if e = f.m.Close(); e != nil {
				t.Fatal(e)
			}
			f.closed = true
			before := snapshotReadonlyCopy(t, f.home, r.ID)
			if m, e := Open(f.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 256 << 20}); e == nil {
				m.Close()
				t.Fatal("startup repaired/accepted corrupt S evidence")
			}
			if _, e = os.Lstat(filepath.Join(f.home, "storage", r.DestinationLease)); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("corrupt recovery allocated destination", e)
			}
			after := snapshotReadonlyCopy(t, f.home, r.ID)
			if before != after {
				t.Fatal("failed optional validation rewrote corrupt durable copy record")
			}
		})
	}
	t.Run("orphan-kind-with-absent-optional-group", func(t *testing.T) {
		f := newSnapshotFixture(t)
		if _, e := f.m.db.Exec(`UPDATE leases SET data=json_set(data,'$.request.kind','share-snapshot-work') WHERE id=?`, f.source.ID); e != nil {
			t.Fatal(e)
		}
		if e := f.m.Close(); e != nil {
			t.Fatal(e)
		}
		f.closed = true
		if m, e := Open(f.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 256 << 20}); e == nil {
			m.Close()
			t.Fatal("orphan internal work entered generic recovery")
		}
	})
}

func TestSnapshotReservationDatabaseProofRejectsAlteredEvidence(t *testing.T) {
	for _, test := range []struct{ name, query string }{{"request-part", `UPDATE snapshot_copy_parts SET data=CAST('altered canonical request' AS BLOB)`}, {"missing-work", `DELETE FROM leases WHERE json_extract(data,'$.request.kind')='share-snapshot-work'`}} {
		t.Run(test.name, func(t *testing.T) {
			f := newSnapshotFixture(t)
			r, e := f.m.reserveSnapshot(t.Context(), snapshotRequest(f, "bad-proof"))
			if e != nil {
				t.Fatal(e)
			}
			if e = f.m.cancelSnapshotReservation(t.Context(), r.Key, 1, "actual no-work cancel before proof mutation"); e != nil {
				t.Fatal(e)
			}
			if _, e = f.m.db.Exec(test.query); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(f.home, "daemon.lock"), nil, 0600); e != nil {
				t.Fatal(e)
			}
			if e = f.m.Close(); e != nil {
				t.Fatal(e)
			}
			f.closed = true
			s, e := database.Offline(t.Context(), f.home)
			if e != nil {
				t.Fatal(e)
			}
			p, e := database.ProveDatabaseSettled(t.Context(), s, f.m.owner)
			closeErr := s.Close()
			if closeErr != nil {
				t.Fatal(closeErr)
			}
			if e == nil || p.Complete || p.Settled {
				t.Fatal("altered snapshot evidence became complete settled proof", p, e)
			}
		})
	}
}

func TestSnapshotReservationCancelledDatabaseProof(t *testing.T) {
	f := newSnapshotFixture(t)
	r, e := f.m.reserveSnapshot(t.Context(), snapshotRequest(f, "proof"))
	if e != nil {
		t.Fatal(e)
	}
	if e = f.m.cancelSnapshotReservation(t.Context(), r.Key, 1, "known no native attempt"); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(f.home, "daemon.lock"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	if e = f.m.Close(); e != nil {
		t.Fatal(e)
	}
	f.closed = true
	s, e := database.Offline(t.Context(), f.home)
	if e != nil {
		t.Fatal(e)
	}
	p, e := database.ProveDatabaseSettled(t.Context(), s, f.m.owner)
	closeErr := s.Close()
	if e != nil || closeErr != nil || !p.Complete {
		t.Fatal(p, e, closeErr)
	}
	// The unrelated original source is still active. This is not a whole-home
	// settled verdict and grants no source/destination namespace deletion.
	if p.Settled {
		t.Fatal("active original source was declared settled")
	}
	for _, b := range p.Blockers {
		if b.Kind == "snapshot" || b.Entity == r.WorkLease || b.Entity == r.DestinationLease {
			t.Fatal("complete cancelled-no-work receipt was not consumed", b)
		}
	}
	if p.Tables["snapshot_copies"] != 1 || p.Tables["snapshot_copy_parts"] < 1 || p.Tables["snapshot_copy_journal"] != 2 {
		t.Fatal("proof omitted S evidence", p.Tables)
	}
}

func TestSnapshotReservationPrecancelSurvivesReopen(t *testing.T) {
	f := newSnapshotFixture(t)
	q := snapshotRequest(f, "cancel-restart")
	if e := f.m.cancelSnapshotReservation(t.Context(), q.Key, 0, "stop before reserve"); e != nil {
		t.Fatal(e)
	}
	if e := f.m.Close(); e != nil {
		t.Fatal(e)
	}
	f.closed = true
	m, e := Open(f.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 256 << 20})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if _, e = m.reserveSnapshot(t.Context(), q); e == nil {
		t.Fatal("durable pre-cancel disappeared on restart")
	}
	if snapshotCount(t, m, `SELECT count(*) FROM sqlite_schema WHERE name GLOB 'snapshot_*'`) != 0 {
		t.Fatal("recovery initialized cancelled optional group")
	}
}

func TestSnapshotReservationAggregateCapacityRefusal(t *testing.T) {
	for _, dimension := range []string{"cpu", "memory", "disk"} {
		t.Run(dimension, func(t *testing.T) {
			f := newSnapshotFixture(t)
			q := snapshotRequest(f, dimension)
			switch dimension {
			case "cpu":
				q.Resources.CPU = 5
			case "memory":
				q.Resources.Memory = 1 << 30
			case "disk":
				q.ContentCeiling = 1 << 30
			}
			before := snapshotCount(t, f.m, `SELECT count(*) FROM leases`)
			if _, e := f.m.reserveSnapshot(t.Context(), q); e == nil {
				t.Fatal("over-capacity bundle accepted")
			}
			if snapshotCount(t, f.m, `SELECT count(*) FROM leases`) != before {
				t.Fatal("one side committed before capacity refusal")
			}
		})
	}
}

func TestSnapshotReservationMalformedPhaseNeverBecomesWork(t *testing.T) {
	f := newSnapshotFixture(t)
	r, e := f.m.reserveSnapshot(t.Context(), snapshotRequest(f, "phase"))
	if e != nil {
		t.Fatal(e)
	}
	for _, phase := range []string{"planning", "prepared", "published"} {
		raw := string(f.m.snapshots.state.Raw[r.ID])
		raw = strings.Replace(raw, `"phase":"reserved-no-work"`, `"phase":"`+phase+`"`, 1)
		v, e := snapshotlease.Decode[snapshotlease.Record]([]byte(raw), snapshotlease.MaxRecordBytes)
		if e == nil && v.Validate() == nil {
			t.Fatal("unfinished copy/publication admitted by no-work codec")
		}
	}
	if _, e = f.m.db.Exec(`UPDATE snapshot_copies SET data=json_set(data,'$.allocation_attempted',json('true'))`); e != nil {
		t.Fatal(e)
	}
	if e = f.m.cancelSnapshotReservation(t.Context(), r.Key, 1, "cannot erase attempted work"); e == nil {
		t.Fatal("corrupt attempt flag released promises")
	}
	f.wantCloseError = true
	for _, id := range []string{r.WorkLease, r.DestinationLease} {
		if !consuming(mustSnapshotLease(t, f.m, id).State) {
			t.Fatal("corrupt record freed charge")
		}
	}
}

func snapshotReadonlyCopy(t *testing.T, home, id string) string {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(home, "state.db")}
	db, e := sql.Open("sqlite", u.String()+"?mode=ro")
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := db.Close(); e != nil {
			t.Error(e)
		}
	}()
	var raw string
	if e = db.QueryRow(`SELECT data FROM snapshot_copies WHERE id=?`, id).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	return raw
}
