package sharelease

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func fixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	name := filepath.Join(t.TempDir(), "state.db")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	db, err := sql.Open("sqlite", name)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return db
}
func writePublication(t *testing.T, db *sql.DB, p Publication) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name='share_publications'`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists == 0 {
		if err = Initialize(context.Background(), tx); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := Canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO share_publications VALUES(?,?)`, p.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	h, err := hash(p)
	if err != nil {
		t.Fatal(err)
	}
	j := Journal{Schema: 1, Authority: p.Authority, Owner: p.Owner, At: p.Created, Actor: "uid:" + p.Owner, Reason: p.Reason, Entity: "publication", ID: p.ID, Revision: 1, Transition: "reserved", AfterSHA256: h}
	event, err := EncodeJournal(j)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO share_journal(data) VALUES(?)`, string(event)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func TestUnusedShareReadDoesNotInitializeAnything(t *testing.T) {
	db := fixtureDB(t)
	p, _, _ := evidenceFixture()
	before := []string{}
	rows, err := db.Query(`SELECT name FROM sqlite_schema ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		before = append(before, n)
	}
	rows.Close()
	s, err := Load(context.Background(), db, p.Authority, p.Owner)
	if err != nil || s.Enabled {
		t.Fatal(s, err)
	}
	var after int
	if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema`).Scan(&after); err != nil || after != len(before) {
		t.Fatal("read initialized optional evidence", after, err)
	}
}
func TestOptionalGroupAtomicRollbackAndCompleteRead(t *testing.T) {
	db := fixtureDB(t)
	p, _, _ := evidenceFixture()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = Initialize(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	s, err := Load(context.Background(), db, p.Authority, p.Owner)
	if err != nil || s.Enabled {
		t.Fatal("failed atomic admission leaked namespace", s, err)
	}
	writePublication(t, db, p)
	s, err = Load(context.Background(), db, p.Authority, p.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Enabled || len(s.Publications) != 1 || s.Capacity.ReservedSlots != PublicationFinalSlots || s.ManifestRaw != 8<<20 || s.ManifestStored <= s.ManifestRaw || len(s.Journal) != 1 {
		t.Fatalf("incomplete admitted snapshot %+v", s)
	}
}
func TestShareSnapshotRefusesChangedSchemaMissingMarkerAndHistory(t *testing.T) {
	for name, sqlText := range map[string]string{
		"missing-marker":           `DELETE FROM metadata WHERE key='share_schema'`,
		"unknown-marker":           `UPDATE metadata SET value='2' WHERE key='share_schema'`,
		"missing-table":            `DROP TABLE share_key_fences`,
		"unknown-table":            `CREATE TABLE share_future(value TEXT)`,
		"hidden-trigger":           `CREATE TRIGGER share_rewrite AFTER INSERT ON share_publications BEGIN DELETE FROM share_consumers; END`,
		"missing-journal":          `DELETE FROM share_journal`,
		"missing-current":          `DELETE FROM share_publications`,
		"missing-all-used-history": `DELETE FROM share_publications; DELETE FROM share_journal`,
		"bad-row-key":              `UPDATE share_publications SET id='different'`,
		"huge-row-key":             `UPDATE share_publications SET id=hex(zeroblob(1000000))`,
		"huge-text":                `UPDATE share_publications SET data=char(0)||hex(zeroblob(1000000))`,
	} {
		t.Run(name, func(t *testing.T) {
			db := fixtureDB(t)
			p, _, _ := evidenceFixture()
			writePublication(t, db, p)
			if _, err := db.Exec(sqlText); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(context.Background(), db, p.Authority, p.Owner); err == nil {
				t.Fatal("accepted incomplete/corrupt share evidence")
			}
		})
	}
}
func TestShareCurrentRecordMustMatchExactJournalBytes(t *testing.T) {
	db := fixtureDB(t)
	p, _, _ := evidenceFixture()
	writePublication(t, db, p)
	p.Reason = "rewritten justification"
	raw, _ := Canonical(p)
	if _, err := db.Exec(`UPDATE share_publications SET data=? WHERE id=?`, string(raw), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), db, p.Authority, p.Owner); err == nil {
		t.Fatal("changed record accepted without matching immutable journal")
	}
}
func TestUnpreparedPublicationCannotAcquireManifestParts(t *testing.T) {
	db := fixtureDB(t)
	p, _, _ := evidenceFixture()
	writePublication(t, db, p)
	if _, err := db.Exec(`INSERT INTO share_manifest_parts VALUES(?,?,0,'eA==')`, p.ID, p.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), db, p.Authority, p.Owner); err == nil {
		t.Fatal("unprepared publication retained guessed/unpublished manifest")
	}
}
func TestCurrentAuthorityAndNativeOwnerMustMatch(t *testing.T) {
	db := fixtureDB(t)
	p, _, _ := evidenceFixture()
	writePublication(t, db, p)
	for _, binding := range [][2]string{{strings.Repeat("1", 32), p.Owner}, {p.Authority, "502"}} {
		if _, err := Load(context.Background(), db, binding[0], binding[1]); err == nil {
			t.Fatal("accepted wrong current authority/owner")
		}
	}
}
func TestShareSnapshotCancellationDoesNotCreateState(t *testing.T) {
	db := fixtureDB(t)
	p, _, _ := evidenceFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Load(ctx, db, p.Authority, p.Owner); err == nil {
		t.Fatal("ignored cancelled read")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name GLOB 'share_*'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("cancelled read wrote namespace", n, err)
	}
}
