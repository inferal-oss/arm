package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func fixture(t *testing.T) (string, *sql.DB) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "state.db"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", "CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)", "INSERT INTO metadata VALUES('host','0123456789abcdef0123456789abcdef')", "CREATE TABLE leases(id TEXT PRIMARY KEY,data TEXT NOT NULL)", `INSERT INTO leases VALUES('one','{"state":"terminal"}')`} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return home, db
}
func tree(t *testing.T, home string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(home, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		result[entry.Name()] = info.Mode().String() + string(data)
	}
	return result
}
func TestStoppedReadsPreserveSourceAndIncludeCommittedWAL(t *testing.T) {
	home, _ := fixture(t)
	before := tree(t, home)
	source, err := Offline(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	stats, err := ReadStats(context.Background(), source)
	if err != nil || stats.Tables["leases"] != 1 || stats.Leases["terminal"] != 1 || stats.Host == "" {
		t.Fatal(stats, err)
	}
	for _, full := range []bool{false, true} {
		check, err := CheckDatabase(context.Background(), source, full)
		if err != nil || !check.OK || !check.Complete {
			t.Fatal(check, err)
		}
	}
	if after := tree(t, home); !reflect.DeepEqual(before, after) {
		t.Fatal("readonly inspection modified original files/permissions")
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}
	source.temp = ""
	source.Directory = nil
	source.lock = nil
	source.DB = nil
}
func TestPrivateHomeAllowsReadBitsWithoutPermissionRepair(t *testing.T) {
	home, _ := fixture(t)
	for _, name := range databaseNames {
		if err := os.Chmod(filepath.Join(home, name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	before := tree(t, home)
	source, err := Offline(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err = ReadStats(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, home)) {
		t.Fatal("read bits repaired implicitly")
	}
	if err = os.Chmod(filepath.Join(home, "state.db"), 0664); err != nil {
		t.Fatal(err)
	}
	if next, err := Offline(context.Background(), home); err == nil {
		next.Close()
		t.Fatal("group-writable source accepted")
	}
	if err = os.Chmod(home, 0755); err != nil {
		t.Fatal(err)
	}
	if next, err := OpenDirectory(home); err == nil {
		next.Close()
		t.Fatal("public home accepted")
	}
}
func TestSourceGuardsLockLinksCompanionsAndStaleCopy(t *testing.T) {
	home, _ := fixture(t)
	lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if s, err := Offline(context.Background(), home); err == nil {
		s.Close()
		t.Fatal("active authority accepted")
	}
	unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	source, err := Offline(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	f, err := os.OpenFile(filepath.Join(home, "state.db-wal"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("changed"))
	f.Close()
	if err = source.Validate(context.Background(), true); err == nil {
		t.Fatal("changed WAL accepted")
	}
}
func TestLiveReadAndConsistentStandalonePrivateBackup(t *testing.T) {
	home, writer := fixture(t)
	source, err := Live(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err = ReadStats(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(home, "backup.db")
	report, err := BackupDatabase(context.Background(), source, output)
	if err != nil || report.Size <= 0 || len(report.SHA256) != 64 {
		t.Fatal(report, err)
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	backup, err := openRO(output)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var n int
	if err = backup.QueryRow("SELECT COUNT(*) FROM leases").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err = writer.Exec(`INSERT INTO leases VALUES('two','{"state":"queued"}')`); err != nil {
		t.Fatal("source no longer writable", err)
	}
	if _, err = BackupDatabase(context.Background(), source, output); err == nil {
		t.Fatal("backup overwrote existing destination")
	}
	if bytes, err := json.Marshal(report); err != nil || strings.Contains(string(bytes), "token") {
		t.Fatal("backup report leaked credentials", err)
	}
}
func TestBackupCancellationAndUnsafeDestinationsLeaveSource(t *testing.T) {
	home, _ := fixture(t)
	source, err := Live(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	before := tree(t, home)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = BackupDatabase(ctx, source, filepath.Join(home, "cancelled.db")); err == nil {
		t.Fatal("cancelled backup succeeded")
	}
	if _, err = os.Stat(filepath.Join(home, "cancelled.db")); !os.IsNotExist(err) {
		t.Fatal("cancelled destination published")
	}
	if !reflect.DeepEqual(before, tree(t, home)) {
		t.Fatal("cancelled backup changed source or leaked temp")
	}
	sentinel := filepath.Join(home, "sentinel")
	os.WriteFile(sentinel, []byte("keep"), 0600)
	os.Symlink(sentinel, filepath.Join(home, "alias"))
	if _, err = BackupDatabase(context.Background(), source, filepath.Join(home, "alias")); err == nil {
		t.Fatal("symlink destination accepted")
	}
	body, _ := os.ReadFile(sentinel)
	if string(body) != "keep" {
		t.Fatal("sentinel changed")
	}
}

func TestBoundedLeaseStatesForeignKeysAndCancelledValidation(t *testing.T) {
	home, writer := fixture(t)
	for _, query := range []string{`INSERT INTO leases VALUES('odd','{"state":"` + strings.Repeat("x", 10000) + `"}')`, `INSERT INTO leases VALUES('bad','malformed')`, "CREATE TABLE policy_outbox(state TEXT)", "INSERT INTO policy_outbox VALUES('queued'),('attempting'),('unknown'),('done')",
		"CREATE TABLE messages(data TEXT)", `INSERT INTO messages VALUES('{"deliveries":{"agent":{"state":"pending"}}}'),('{"deliveries":{"agent":{"state":"unknown"}}}'),('malformed'),('{"deliveries":{"agent":{"state":"responded"}}}')`,
		"CREATE TABLE parent(id INTEGER PRIMARY KEY)", "CREATE TABLE child(parent INTEGER REFERENCES parent(id))", "INSERT INTO child VALUES(42)"} {
		if _, err := writer.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	source, err := Live(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	stats, err := ReadStats(context.Background(), source)
	if err != nil || len(stats.Leases) != 2 || stats.Leases["unknown"] != 2 || stats.Obligations["leases_awaiting_settlement"] != 2 || stats.Obligations["policy_outbox_unsettled"] != 3 || stats.Obligations["deliveries_pending_or_unknown"] != 3 {
		t.Fatal(stats, err)
	}
	check, err := CheckDatabase(context.Background(), source, true)
	if err != nil || check.OK || !check.Complete || check.ForeignKeys != "declared constraints checked" || len(check.Findings) != 2 {
		t.Fatal(check, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = source.Validate(ctx, true); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled rehash ignored context", err)
	}
}
func TestMissingLiveIndexIsNotCreatedAndTempReplacementPreserved(t *testing.T) {
	home, writer := fixture(t)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	before := tree(t, home)
	if live, err := Live(context.Background(), home); err == nil {
		live.Close()
		t.Fatal("live read created absent WAL index")
	}
	if !reflect.DeepEqual(before, tree(t, home)) {
		t.Fatal("live refusal changed files")
	}
	source, err := Offline(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	original := source.temp
	renamed := original + "-preserved"
	if err = os.Rename(original, renamed); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(renamed)
	if err = os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(original)
	sentinel := filepath.Join(original, "state.db")
	os.WriteFile(sentinel, []byte("operator data"), 0600)
	if err = source.Close(); err == nil {
		t.Fatal("replaced temporary directory accepted")
	}
	data, _ := os.ReadFile(sentinel)
	if string(data) != "operator data" {
		t.Fatal("substituted directory erased")
	}
}

func TestBackupMidStepAndPublicationCancellationAreBounded(t *testing.T) {
	home, writer := fixture(t)
	if _, err := writer.Exec("CREATE TABLE padding(data BLOB); INSERT INTO padding VALUES(zeroblob(4194304))"); err != nil {
		t.Fatal(err)
	}
	source, err := Live(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for _, phase := range []string{"step", "publication"} {
		ctx, cancel := context.WithCancel(context.Background())
		seen := false
		target := filepath.Join(home, "cancel-"+phase+".db")
		_, err = backupDatabase(ctx, source, target, func(at string) {
			if at == phase && !seen {
				seen = true
				cancel()
			}
		})
		cancel()
		if !seen || !errors.Is(err, context.Canceled) {
			t.Fatal(phase, seen, err)
		}
		if _, err = os.Stat(target); !os.IsNotExist(err) {
			t.Fatal("cancelled output published", phase, err)
		}
		matches, _ := filepath.Glob(filepath.Join(home, ".arm-database-backup-*"))
		if len(matches) != 0 {
			t.Fatal("verified private staging leaked", matches)
		}
	}
}
func TestBackupConcurrentCommitRemainsWholeTransaction(t *testing.T) {
	home, writer := fixture(t)
	for _, query := range []string{"CREATE TABLE left_side(value INTEGER)", "CREATE TABLE right_side(value INTEGER)", "INSERT INTO left_side VALUES(0)", "INSERT INTO right_side VALUES(0)", "CREATE TABLE padding(data BLOB)", "INSERT INTO padding VALUES(zeroblob(4194304))"} {
		if _, err := writer.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	source, err := Live(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	committed := false
	target := filepath.Join(home, "concurrent.db")
	_, err = backupDatabase(context.Background(), source, target, func(phase string) {
		if phase != "step" || committed {
			return
		}
		committed = true
		tx, e := writer.Begin()
		if e == nil {
			_, e = tx.Exec("UPDATE left_side SET value=1; UPDATE right_side SET value=1")
		}
		if e == nil {
			e = tx.Commit()
		} else if tx != nil {
			tx.Rollback()
		}
		if e != nil {
			t.Fatal(e)
		}
	})
	if err != nil || !committed {
		t.Fatal(committed, err)
	}
	db, err := openRO(target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var left, right int
	if err = db.QueryRow("SELECT (SELECT value FROM left_side),(SELECT value FROM right_side)").Scan(&left, &right); err != nil || left != right {
		t.Fatal("backup split a transaction", left, right, err)
	}
}
func TestBackupUnknownCompanionRetainsPrivateBundle(t *testing.T) {
	home, _ := fixture(t)
	source, err := Live(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	var companion string
	_, err = backupDatabase(context.Background(), source, filepath.Join(home, "unpublished.db"), func(phase string) {
		if phase != "publication" {
			return
		}
		matches, _ := filepath.Glob(filepath.Join(home, ".arm-database-backup-*"))
		if len(matches) != 1 {
			t.Fatal(matches)
		}
		companion = matches[0] + "-journal"
		if e := os.WriteFile(companion, []byte("unverified operator evidence"), 0600); e != nil {
			t.Fatal(e)
		}
	})
	// A new companion introduced exactly at the final boundary must invalidate
	// publication and must not be silently deleted as supposedly owned output.
	if err == nil {
		t.Fatal("new companion accepted")
	}
	data, e := os.ReadFile(companion)
	if e != nil || string(data) != "unverified operator evidence" {
		t.Fatal("unknown companion deleted", e)
	}
}
