package manager

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/snapshotlease"
)

func snapshotSequenceRows(t *testing.T, home string) string {
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
	rows, e := db.QueryContext(t.Context(), `SELECT rowid,typeof(name),hex(name),typeof(seq),hex(seq) FROM sqlite_sequence ORDER BY rowid LIMIT 65`)
	if e != nil {
		t.Fatal(e)
	}
	type sequence struct {
		RowID                           int64
		NameType, NameHex, Type, SeqHex string
	}
	var values []sequence
	for rows.Next() {
		var v sequence
		if e = rows.Scan(&v.RowID, &v.NameType, &v.NameHex, &v.Type, &v.SeqHex); e != nil {
			break
		}
		values = append(values, v)
	}
	rowsErr, closeErr := rows.Err(), rows.Close()
	if e != nil || rowsErr != nil || closeErr != nil || len(values) > 64 {
		t.Fatal("bounded actual sequence rows", e, rowsErr, closeErr, len(values))
	}
	raw, e := json.Marshal(values)
	if e != nil {
		t.Fatal(e)
	}
	return string(raw)
}

func corruptSnapshotSequence(t *testing.T, home, query string) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(home, "state.db")}
	db, e := sql.Open("sqlite", u.String()+"?mode=rw")
	if e != nil {
		t.Fatal(e)
	}
	closed := false
	defer func() {
		if !closed {
			closed = true
			if closeErr := db.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}
	}()
	result, execErr := db.ExecContext(t.Context(), query)
	var count int64
	var countErr error
	if execErr == nil {
		count, countErr = result.RowsAffected()
	}
	closeErr := db.Close()
	closed = true // Even failed/unknown Close is never retried.
	if execErr != nil || countErr != nil || count != 1 || closeErr != nil {
		t.Fatal("intended actual sequence commit/original writer close failed", count, execErr, countErr, closeErr)
	}
}

func TestSnapshotReservationJournalSequenceProofGuards(t *testing.T) {
	for _, test := range []struct {
		name, query string
		absent      bool
	}{
		{"lowered", `UPDATE sqlite_sequence SET seq=0 WHERE name='snapshot_copy_journal'`, false},
		{"missing", `DELETE FROM sqlite_sequence WHERE name='snapshot_copy_journal'`, false},
		{"duplicate", `INSERT INTO sqlite_sequence(name,seq) SELECT name,seq FROM sqlite_sequence WHERE name='snapshot_copy_journal'`, false},
		{"blob-name", `UPDATE sqlite_sequence SET name=CAST(name AS BLOB) WHERE name='snapshot_copy_journal'`, false},
		{"blob-sequence", `UPDATE sqlite_sequence SET seq=CAST(seq AS BLOB) WHERE name='snapshot_copy_journal'`, false},
		{"text-sequence", `UPDATE sqlite_sequence SET seq='not-an-integer' WHERE name='snapshot_copy_journal'`, false},
		{"forged-absent-namespace", `INSERT INTO sqlite_sequence(name,seq) VALUES('snapshot_copy_journal',1)`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSnapshotFixture(t)
			var r snapshotlease.Record
			if !test.absent {
				var e error
				r, e = f.m.reserveSnapshot(t.Context(), snapshotRequest(f, "sequence-proof"))
				if e != nil {
					t.Fatal(e)
				}
				if e = f.m.cancelSnapshotReservation(t.Context(), r.Key, 1, "actual no-work decision before sequence mutation"); e != nil {
					t.Fatal(e)
				}
				if snapshotCount(t, f.m, `SELECT seq FROM sqlite_sequence WHERE name='snapshot_copy_journal'`) != 2 {
					t.Fatal("actual positive journal counter missing")
				}
			} else if snapshotCount(t, f.m, `SELECT count(*) FROM sqlite_schema WHERE name GLOB 'snapshot_*'`) != 0 {
				t.Fatal("absent-namespace control already has snapshot tables")
			}
			if e := os.WriteFile(filepath.Join(f.home, "daemon.lock"), nil, 0600); e != nil {
				t.Fatal(e)
			}
			closeErr := f.m.Close()
			f.closed = true
			if closeErr != nil {
				t.Fatal(closeErr)
			}
			// The original Manager is positively closed. A separate owned
			// private SQLite writer commits only the intended corruption and
			// closes before the independent readonly proof snapshot opens.
			corruptSnapshotSequence(t, f.home, test.query)
			before := snapshotSequenceRows(t, f.home)
			var copyBefore, promisesBefore string
			if !test.absent {
				copyBefore = snapshotReadonlyCopy(t, f.home, r.ID)
				promisesBefore = snapshotPhysicalPromises(t, f.home, r.WorkLease, r.DestinationLease)
			}
			s, e := database.Offline(t.Context(), f.home)
			if e != nil {
				t.Fatal(e)
			}
			proof, proofErr := database.ProveDatabaseSettled(t.Context(), s, f.m.owner)
			closeErr = s.Close()
			if closeErr != nil {
				t.Fatal(closeErr)
			}
			if proofErr == nil || proof.Complete || proof.Settled {
				t.Fatal("malformed/unsupported sequence became complete proof", proof, proofErr)
			}
			if snapshotSequenceRows(t, f.home) != before {
				t.Fatal("readonly refusal repaired sequence evidence")
			}
			if !test.absent && (snapshotReadonlyCopy(t, f.home, r.ID) != copyBefore || snapshotPhysicalPromises(t, f.home, r.WorkLease, r.DestinationLease) != promisesBefore) {
				t.Fatal("sequence refusal changed copy/promises")
			}
		})
	}
}
