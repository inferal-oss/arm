package sshservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/local"
)

// This is an actual process-exit rollback journal, not a handcrafted SQLite
// header. The deliberately invalid rows are uncommitted and must disappear.
func TestSourceHistoryCrashJournalChild(t *testing.T) {
	path := os.Getenv("ARM_TEST_SOURCE_HISTORY_CRASH")
	if path == "" {
		return
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA synchronous=FULL", "PRAGMA cache_size=1", "PRAGMA cache_spill=ON"} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 32; i++ {
		if _, e = tx.Exec(`INSERT INTO source_history_operations(key,request,phase,data) VALUES(?,?,?,?)`, fmt.Sprintf("uncommitted-%d", i), "{}", "prepared", strings.Repeat("x", 8192)); e != nil {
			t.Fatal(e)
		}
	}
	info, e := os.Stat(path + "-journal")
	if e != nil || info.Size() < 512 {
		t.Fatal("real rollback journal not flushed", info, e)
	}
	os.Exit(0) // intentionally neither rollback nor close
}

func crashSourceHistoryCatalog(t *testing.T, home string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSourceHistoryCrashJournalChild$")
	cmd.Env = append(os.Environ(), "ARM_TEST_SOURCE_HISTORY_CRASH="+filepath.Join(home, SourceHistoryName, SourceHistoryCatalogName))
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("crash child: %v %s", e, out)
	}
}

func emptySourceHistoryFixture(t *testing.T) (string, Binding) {
	t.Helper()
	home, b := stateFixture(t)
	if e := withStateMutation(t.Context(), home, b, true, func(s *State, d *os.File) error { _, e := initializeHistory(t.Context(), s, d); return e }); e != nil {
		t.Fatal(e)
	}
	return home, b
}

func TestSourceHistoryRealJournalRecoveryAndLostAcknowledgements(t *testing.T) {
	for _, point := range []string{"", "after_recovery_fence", "after_sqlite_recovery", "after_recovery_validation", "before_recovery_completed", "after_recovery_completed"} {
		t.Run(point, func(t *testing.T) {
			home, b := emptySourceHistoryFixture(t)
			crashSourceHistoryCatalog(t, home)
			if _, e := ReadState(t.Context(), home, b); e == nil {
				t.Fatal("ordinary inspection recovered journal")
			}
			q, e := PlanSourceHistoryRecovery(t.Context(), home, b, "recover-original", "Verify original private rollback journal and all history")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = os.Stat(filepath.Join(home, "mesh", SourceHistoryRecoveryName)); !os.IsNotExist(e) {
				t.Fatal("plan wrote recovery fence", e)
			}
			lost := errors.New("test lost recovery acknowledgement")
			got, e := recoverSourceHistoryCatalog(t.Context(), home, b, q, func(stage string) error {
				if stage == point && point != "" {
					return lost
				}
				return nil
			})
			if point == "" {
				if e != nil {
					t.Fatal(e)
				}
			} else if !errors.Is(e, lost) {
				t.Fatal("wrong failure", got, e)
			}
			before, e := InspectSourceHistoryRecovery(t.Context(), home, b, q.Key)
			if e != nil {
				t.Fatal(e)
			}
			if point != "" && point != "after_recovery_completed" {
				if before.Completed != nil || before.Result != nil {
					t.Fatal("unknown recovery guessed settled")
				}
				if _, e = ReadState(t.Context(), home, b); e == nil {
					t.Fatal("pending durable fence allowed ordinary admission")
				}
			}
			retryPlan, e := PlanSourceHistoryRecovery(t.Context(), home, b, q.Key, q.Reason)
			if e != nil || !reflect.DeepEqual(retryPlan, q) {
				t.Fatal("lost original recovery request", e)
			}
			done, e := RecoverSourceHistoryCatalog(t.Context(), home, b, retryPlan)
			if e != nil || done.Completed == nil || done.Result == nil {
				t.Fatal("exact continuation failed", done, e)
			}
			if _, e = ReadState(t.Context(), home, b); e != nil {
				t.Fatal(e)
			}
			if e = ValidateSourceHistory(t.Context(), home, b); e != nil {
				t.Fatal(e)
			}
			again, e := RecoverSourceHistoryCatalog(t.Context(), home, b, q)
			if e != nil || !reflect.DeepEqual(again, done) {
				t.Fatal("completed exact recovery retry mutated evidence", e)
			}
			v, _, e := readSourceRecovery(t.Context(), home, q.Marker)
			if e != nil || len(v.Attempts) != 1 {
				t.Fatal("exact retry grew recovery history", e)
			}
		})
	}
}

func TestSourceHistoryRecoverySemanticFailureRetainsFence(t *testing.T) {
	home, b := emptySourceHistoryFixture(t)
	// Independently committed malformed family metadata survives SQLite rollback.
	db, e := sql.Open("sqlite", filepath.Join(home, SourceHistoryName, SourceHistoryCatalogName))
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO source_history_operations(key,request,phase,data) VALUES('forged','{}','completed','{}')`)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	crashSourceHistoryCatalog(t, home)
	q, e := PlanSourceHistoryRecovery(t.Context(), home, b, "semantic-refusal", "Retain unverified family evidence")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = RecoverSourceHistoryCatalog(t.Context(), home, b, q); e == nil {
		t.Fatal("SQLite recovery became family settlement")
	}
	got, e := InspectSourceHistoryRecovery(t.Context(), home, b, q.Key)
	if e != nil || got.Completed != nil {
		t.Fatal("semantic failure lost pending fence", e)
	}
	if _, e = ReadState(t.Context(), home, b); e == nil {
		t.Fatal("semantic failure admitted ordinary reads")
	}
	if _, e = os.Stat(filepath.Join(home, SourceHistoryName, SourceHistoryCatalogName+"-journal")); !os.IsNotExist(e) {
		t.Fatal("SQLite did not recover actual journal", e)
	}
	if _, e = RecoverSourceHistoryCatalog(t.Context(), home, b, q); e == nil {
		t.Fatal("gone journal erased semantic failure")
	}
}

func TestSourceHistoryRecoveryBoundsCanonicalAndNoNull(t *testing.T) {
	home, b := emptySourceHistoryFixture(t)
	m, e := historyCanonicalMarker(t.Context(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	r := SourceHistoryRecovery{Schema: 1, Marker: *m, Attempts: []SourceHistoryRecoveryAttempt{}}
	for i := 0; i < SourceHistoryRecoveryMaxAttempts; i++ {
		finished := now.Add(time.Duration(i) * time.Second)
		r.Attempts = append(r.Attempts, SourceHistoryRecoveryAttempt{Key: fmt.Sprint(i), Reason: "bounded explicit recovery", Actor: local.Identity{UID: b.Owner, Username: b.Account}, Journal: m.Catalog, JournalBytes: 4096, JournalSHA256: strings.Repeat("a", 64), Started: now, Completed: &finished, Result: &SourceHistoryRecoveryResult{CatalogSHA256: strings.Repeat("b", 64), CatalogBytes: 4096}})
	}
	if e = r.Validate(); e != nil {
		t.Fatal("largest attempt count must fit/reopen", e)
	}
	raw, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	var decoded SourceHistoryRecovery
	if e = decodeSourceRecovery(raw, &decoded); e != nil {
		t.Fatal(e)
	}
	// Exercise the real bounded record writer and reader at64, then reject65
	// before SQLite recovery. Historical journal witnesses are shape fixtures;
	// the newly reviewed journal itself is produced by an actual crashed child.
	if e = withStateMutation(t.Context(), home, b, true, func(s *State, d *os.File) error {
		return atomicStateRecord(t.Context(), s, d, SourceHistoryRecoveryName, raw, nil, SourceHistoryRecoveryMaxBytes)
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadState(t.Context(), home, b); e != nil {
		t.Fatal("successful64-attempt writer cannot reopen", e)
	}
	crashSourceHistoryCatalog(t, home)
	q, e := PlanSourceHistoryRecovery(t.Context(), home, b, "overflow-65", "Refuse new recovery without deleting exact old audit")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = RecoverSourceHistoryCatalog(t.Context(), home, b, q); e == nil {
		t.Fatal("recovery65 admitted beyond durable audit bound")
	}
	retained, e := os.ReadFile(filepath.Join(home, "mesh", SourceHistoryRecoveryName))
	if e != nil || string(retained) != string(raw) {
		t.Fatal("full audit refusal mutated historical evidence", e)
	}
	if _, e = os.Stat(filepath.Join(home, SourceHistoryName, SourceHistoryCatalogName+"-journal")); e != nil {
		t.Fatal("full audit refusal recovered journal", e)
	}
	r.Attempts = append(r.Attempts, r.Attempts[0])
	if r.Validate() == nil {
		t.Fatal("attempt65 accepted")
	}
	r.Attempts = r.Attempts[:SourceHistoryRecoveryMaxAttempts]
	for i := range r.Attempts {
		r.Attempts[i].Reason = strings.Repeat("\"", 4096)
	}
	if r.Validate() == nil {
		t.Fatal("escaped aggregate bytes not bounded")
	}
	if e = decodeSourceRecovery([]byte(strings.Replace(string(raw), `"archived_keys":0`, `"archived_keys":null`, 1)), &decoded); e == nil {
		t.Fatal("null recovery result accepted")
	}
}

func TestSourceHistoryStaleRecoveryBytesCreateNoFence(t *testing.T) {
	home, b := emptySourceHistoryFixture(t)
	crashSourceHistoryCatalog(t, home)
	q, e := PlanSourceHistoryRecovery(t.Context(), home, b, "stale-journal", "Only recover reviewed exact journal bytes")
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(home, SourceHistoryName, SourceHistoryCatalogName+"-journal")
	f, e := os.OpenFile(path, os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	var one [1]byte
	if _, e = f.ReadAt(one[:], 0); e != nil {
		f.Close()
		t.Fatal(e)
	}
	one[0] ^= 255
	if _, e = f.WriteAt(one[:], 0); e != nil {
		f.Close()
		t.Fatal(e)
	}
	f.Close()
	if _, e = RecoverSourceHistoryCatalog(t.Context(), home, b, q); e == nil {
		t.Fatal("stale reviewed journal accepted")
	}
	if _, e = os.Lstat(filepath.Join(home, "mesh", SourceHistoryRecoveryName)); !os.IsNotExist(e) {
		t.Fatal("stale review created durable recovery intent", e)
	}
}

func TestSourceHistoryRecoveryPreservesUnrelatedPendingWork(t *testing.T) {
	home, b, items := settledSourceHistoryFixture(t, 1)
	if _, e := ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "original")); e != nil {
		t.Fatal(e)
	}
	pending := sourceFixture(t, home, b, items[0].Request.Pin, "still-pending")
	if e := ReserveSource(t.Context(), home, b, pending); e != nil {
		t.Fatal(e)
	}
	crashSourceHistoryCatalog(t, home)
	q, e := PlanSourceHistoryRecovery(t.Context(), home, b, "recover-with-pending", "Validate catalog without settling live or unknown work")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = RecoverSourceHistoryCatalog(t.Context(), home, b, q); e != nil {
		t.Fatal(e)
	}
	s, e := ReadState(t.Context(), home, b)
	if e != nil || len(s.Intents) != 1 || !exactIntent(s.Intents[0], pending) || s.History.Archived != 1 {
		t.Fatalf("recovery changed unrelated source obligations: %v", e)
	}
	if _, e = os.Stat(pending.Directory.Path); e != nil {
		t.Fatal("recovery removed unrelated invocation directory", e)
	}
}
