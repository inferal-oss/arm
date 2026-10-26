package syncservice

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/syncfs"
	"github.com/yrashk/arm/internal/syncreconcile"
	_ "modernc.org/sqlite"
)

func digest(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func content(s string) syncreconcile.Entry {
	return syncreconcile.Entry{Kind: syncreconcile.File, Size: int64(len(s)), ContentHash: digest(s), MetadataHash: digest("mode:0644")}
}
func request() syncreconcile.Request {
	e := content("source bytes")
	keys := map[string]string{"f": "f"}
	return syncreconcile.Request{Binding: syncreconcile.Binding{Local: syncreconcile.Endpoint{ID: "source/enrolled/account", RootID: "source/root/native"}, Remote: syncreconcile.Endpoint{ID: "target/enrolled/account", RootID: "target/root/native"}, SelectionID: "all", MetadataID: "mode"}, Direction: syncreconcile.Push, LocalPolicy: syncreconcile.Additive, RemotePolicy: syncreconcile.Full, Local: syncreconcile.Snapshot{Entries: map[string]syncreconcile.Entry{"f": e}, NameKeys: keys}, Remote: syncreconcile.Snapshot{Entries: map[string]syncreconcile.Entry{}, NameKeys: map[string]string{"f": "f"}}, FirstRun: true}
}
func promises() model.Resources { return model.Resources{CPU: 1, Memory: 1 << 20, Disk: 1 << 20} }
func intents(t *testing.T) (Intent, Intent, []ManifestRow) {
	t.Helper()
	r := request()
	p, err := syncreconcile.Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	local, rows, err := NewIntent("sync-key-12345678", digest("arm sync-worker fixed"), digest("reviewed SSH control"), "test sync", syncreconcile.Local, r, p, promises(), promises(), now)
	if err != nil {
		t.Fatal(err)
	}
	remote, other, err := NewIntent(local.Key, local.WorkerCommandSHA256, local.SSHControlSHA256, local.Reason, syncreconcile.Remote, r, p, promises(), promises(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, other) {
		t.Fatal("endpoint manifests differ")
	}
	return local, remote, rows
}
func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err = db.ExecContext(ctx, `PRAGMA synchronous=FULL`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = InstallSchemaTx(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = VerifyDurableSQLite(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}
func reserve(t *testing.T, db *sql.DB, i Intent, rows []ManifestRow) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = ReserveTx(ctx, tx, i, rows, observedRows(i.Side, rows)); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func observedRows(side syncreconcile.Side, rows []ManifestRow) syncreconcile.Snapshot {
	s := syncreconcile.Snapshot{Entries: map[string]syncreconcile.Entry{}, NameKeys: map[string]string{}}
	for _, row := range rows {
		if row.Side != side {
			continue
		}
		s.NameKeys[row.Path] = row.NameKey
		if row.Entry != nil {
			s.Entries[row.Path] = *row.Entry
		}
	}
	return s
}

func TestDurableDualReceiptBaselineGate(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	local, remote, rows := intents(t)
	reserve(t, db, local, rows)
	reserve(t, db, remote, rows)
	localLedger := &Ledger{DB: db, Key: local.Key, Side: syncreconcile.Local}
	remoteLedger := &Ledger{DB: db, Key: remote.Key, Side: syncreconcile.Remote}
	if _, err := remoteLedger.CompleteOwnSide(ctx); !errors.Is(err, ErrUnknown) {
		t.Fatalf("incomplete effects accepted: %v", err)
	}
	if _, err := remoteLedger.ConfirmBaseline(ctx); !errors.Is(err, ErrUnconfirmed) {
		t.Fatalf("baseline before receipts: %v", err)
	}
	localReceipt, err := localLedger.CompleteOwnSide(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actions := expectedActions(remote, syncreconcile.Remote)
	if len(actions) != 1 {
		t.Fatalf("expected one remote action: %#v", actions)
	}
	r := syncfs.Receipt{Key: remote.Key, PlanDigest: remote.PlanDigest, Generation: remote.Generation, Side: syncreconcile.Remote, Index: 0, Action: actions[0], State: "prepared"}
	if err = remoteLedger.Prepare(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err = remoteLedger.Prepare(ctx, r); !errors.Is(err, ErrUnknown) {
		t.Fatalf("prepared effect replay accepted: %v", err)
	}
	if _, err = remoteLedger.CompleteOwnSide(ctx); !errors.Is(err, ErrUnknown) {
		t.Fatalf("prepared effect treated as complete: %v", err)
	}
	r.State = "applied"
	if err = remoteLedger.Outcome(ctx, r); err != nil {
		t.Fatal(err)
	}
	remoteReceipt, err := remoteLedger.CompleteOwnSide(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = localLedger.RecordPeerReceipt(ctx, remoteReceipt); err != nil {
		t.Fatal(err)
	}
	if err = remoteLedger.RecordPeerReceipt(ctx, localReceipt); err != nil {
		t.Fatal(err)
	}
	lb, err := localLedger.ConfirmBaseline(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := remoteLedger.ConfirmBaseline(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lb, rb) || lb.Seal != local.Proposal.Seal {
		t.Fatal("different confirmed baselines")
	}
	lookup, err := remoteLedger.Lookup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lookup.State != "confirmed" || len(lookup.Effects) != 1 || lookup.Effects[0].State != "applied" {
		t.Fatalf("lookup %#v", lookup)
	}
	// A lost confirmation reply may be inspected again; it does not authorize
	// another native application under the original key.
	if _, err = remoteLedger.ConfirmBaseline(ctx); err != nil {
		t.Fatal(err)
	}
	if err = remoteLedger.Prepare(ctx, syncfs.Receipt{Key: r.Key, PlanDigest: r.PlanDigest, Generation: r.Generation, Side: r.Side, Index: r.Index, Action: r.Action, State: "prepared"}); !errors.Is(err, ErrUnknown) {
		t.Fatalf("confirmed operation replay accepted: %v", err)
	}
}

func TestCompletedEffectEvidenceIsRequiredForLookupAndConfirmation(t *testing.T) {
	for _, mutation := range []struct {
		name, query string
	}{
		{"deleted", `DELETE FROM sync_effects WHERE key=? AND owner_side='remote'`},
		{"prepared_corrupt", `UPDATE sync_effects SET prepared='{}' WHERE key=? AND owner_side='remote'`},
		{"outcome_corrupt", `UPDATE sync_effects SET outcome='{}' WHERE key=? AND owner_side='remote'`},
		{"outcome_empty", `UPDATE sync_effects SET outcome=x'' WHERE key=? AND owner_side='remote'`},
		{"outcome_missing", `UPDATE sync_effects SET outcome=NULL WHERE key=? AND owner_side='remote'`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			ctx := context.Background()
			db := openDB(t)
			local, remote, rows := intents(t)
			reserve(t, db, local, rows)
			reserve(t, db, remote, rows)
			ll := &Ledger{DB: db, Key: local.Key, Side: syncreconcile.Local}
			rl := &Ledger{DB: db, Key: remote.Key, Side: syncreconcile.Remote}
			localReceipt, err := ll.CompleteOwnSide(ctx)
			if err != nil {
				t.Fatal(err)
			}
			r := syncfs.Receipt{Key: remote.Key, PlanDigest: remote.PlanDigest, Generation: remote.Generation, Side: syncreconcile.Remote, Index: 0, Action: expectedActions(remote, syncreconcile.Remote)[0], State: "prepared"}
			if err = rl.Prepare(ctx, r); err != nil {
				t.Fatal(err)
			}
			r.State = "applied"
			if err = rl.Outcome(ctx, r); err != nil {
				t.Fatal(err)
			}
			remoteReceipt, err := rl.CompleteOwnSide(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = rl.RecordPeerReceipt(ctx, localReceipt); err != nil {
				t.Fatal(err)
			}
			if err = ll.RecordPeerReceipt(ctx, remoteReceipt); err != nil {
				t.Fatal(err)
			}
			if _, err = db.ExecContext(ctx, mutation.query, remote.Key); err != nil {
				t.Fatal(err)
			}
			if _, err = rl.Lookup(ctx); err == nil {
				t.Fatal("lookup accepted missing or corrupt completed effect")
			}
			if _, err = rl.ConfirmBaseline(ctx); err == nil {
				t.Fatal("baseline confirmed missing or corrupt effect")
			}
			var count int
			if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_baselines`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("baseline advanced: %d, %v", count, err)
			}
		})
	}
}

func TestOperationRowIdentityMatchesIntent(t *testing.T) {
	for _, change := range []struct {
		name, query string
		value       any
	}{
		{"pair", `UPDATE sync_operations SET pair=? WHERE key=? AND side='remote'`, digest("different pair")},
		{"generation", `UPDATE sync_operations SET generation=? WHERE key=? AND side='remote'`, 42},
	} {
		t.Run(change.name, func(t *testing.T) {
			ctx := context.Background()
			db := openDB(t)
			_, remote, rows := intents(t)
			reserve(t, db, remote, rows)
			ledger := &Ledger{DB: db, Key: remote.Key, Side: syncreconcile.Remote}
			receipt := syncfs.Receipt{Key: remote.Key, PlanDigest: remote.PlanDigest, Generation: remote.Generation, Side: syncreconcile.Remote, Index: 0, Action: expectedActions(remote, syncreconcile.Remote)[0], State: "prepared"}
			if err := ledger.Prepare(ctx, receipt); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, change.query, change.value, remote.Key); err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.Lookup(ctx); err == nil {
				t.Fatal("mismatched operation identity accepted")
			}
			receipt.State = "applied"
			if err := ledger.Outcome(ctx, receipt); err == nil {
				t.Fatal("cached effect intent ignored changed row identity")
			}
		})
	}
}

func TestUndurableSQLiteJournalModesAreRejected(t *testing.T) {
	for _, mode := range []string{"OFF", "MEMORY"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			db := openDB(t)
			if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=`+mode); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `PRAGMA synchronous=FULL`); err != nil {
				t.Fatal(err)
			}
			if err := VerifyDurableSQLite(ctx, db); err == nil {
				t.Fatal("undurable journal accepted")
			}
			local, _, rows := intents(t)
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err = ReserveTx(ctx, tx, local, rows, observedRows(local.Side, rows)); err == nil {
				t.Fatal("reservation accepted undurable journal")
			}
		})
	}
}

func TestLargePlanEffectJournal(t *testing.T) {
	const count = 768
	r := request()
	r.Local.Entries = make(map[string]syncreconcile.Entry, count)
	r.Local.NameKeys = make(map[string]string, count)
	r.Remote.NameKeys = make(map[string]string, count)
	for n := 0; n < count; n++ {
		name := fmt.Sprintf("f-%04d", n)
		r.Local.Entries[name] = content(name)
		r.Local.NameKeys[name] = name
		r.Remote.NameKeys[name] = name
	}
	plan, err := syncreconcile.Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	resources := model.Resources{CPU: 1, Memory: 32 << 20, Disk: 32 << 20}
	i, rows, err := NewIntent("large-sync-key-12345", digest("fixed worker"), digest("large SSH control"), "large journal", syncreconcile.Remote, r, plan, resources, resources, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	reserve(t, db, i, rows)
	l := &Ledger{DB: db, Key: i.Key, Side: i.Side}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	actions := expectedActions(i, i.Side)
	if len(actions) != count {
		t.Fatalf("action count %d", len(actions))
	}
	for index, action := range actions {
		receipt := syncfs.Receipt{Key: i.Key, PlanDigest: i.PlanDigest, Generation: i.Generation, Side: i.Side, Index: index, Action: action, State: "prepared"}
		if err = l.Prepare(ctx, receipt); err != nil {
			t.Fatalf("prepare %d: %v", index, err)
		}
		receipt.State = "applied"
		if err = l.Outcome(ctx, receipt); err != nil {
			t.Fatalf("outcome %d: %v", index, err)
		}
	}
	completed, err := l.CompleteOwnSide(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if completed.EffectCount != count {
		t.Fatalf("effect count %d", completed.EffectCount)
	}
	lookup, err := l.Lookup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(lookup.Effects) != count {
		t.Fatalf("lookup effects %d", len(lookup.Effects))
	}
}

func TestUnknownOutcomeRetainsOriginalKey(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	_, remote, rows := intents(t)
	reserve(t, db, remote, rows)
	l := &Ledger{DB: db, Key: remote.Key, Side: syncreconcile.Remote}
	a := expectedActions(remote, syncreconcile.Remote)[0]
	r := syncfs.Receipt{Key: remote.Key, PlanDigest: remote.PlanDigest, Generation: remote.Generation, Side: syncreconcile.Remote, Index: 0, Action: a, State: "prepared"}
	if err := l.Prepare(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.State = "unknown"
	r.Error = "lost native completion"
	if err := l.Outcome(ctx, r); err != nil {
		t.Fatal(err)
	}
	lookup, err := l.Lookup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lookup.State != "unknown" || len(lookup.Effects) != 1 || lookup.Effects[0].State != "unknown" {
		t.Fatalf("lost outcome %#v", lookup)
	}
	if _, err = l.CompleteOwnSide(ctx); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown outcome accepted: %v", err)
	}
	if err = l.Prepare(ctx, r); err == nil {
		t.Fatal("unknown effect replay accepted")
	}
}

func TestReserveExactManifestAndKey(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	local, _, rows := intents(t)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]ManifestRow(nil), rows...)
	tampered[0].NameKey = "altered"
	if err = ReserveTx(ctx, tx, local, tampered, observedRows(local.Side, rows)); err == nil {
		t.Fatal("changed manifest accepted")
	}
	tx.Rollback()
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	observed := observedRows(local.Side, rows)
	observed.Entries["f"] = content("concurrent edit")
	if err = ReserveTx(ctx, tx, local, rows, observed); err == nil {
		t.Fatal("changed held-root observation accepted")
	}
	tx.Rollback()
	reserve(t, db, local, rows)
	if _, err = LoadBaseline(ctx, db, local.Binding, syncreconcile.Local); !errors.Is(err, ErrUnknown) {
		t.Fatalf("pending first run treated as fresh pair: %v", err)
	}
	other := local
	other.Key = "another-sync-key-123456"
	other.SSHKey = other.Key
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = ReserveTx(ctx, tx, other, rows, observedRows(other.Side, rows)); !errors.Is(err, ErrUnknown) {
		t.Fatalf("second first-run key accepted: %v", err)
	}
	tx.Rollback()
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = ReserveTx(ctx, tx, local, rows, observedRows(local.Side, rows)); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("duplicate original key accepted: %v", err)
	}
	tx.Rollback()
}

func TestPreparedEffectSurvivesReopenWithoutReplay(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reopen.db")
	open := func() *sql.DB {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err = db.ExecContext(ctx, `PRAGMA synchronous=FULL`); err != nil {
			t.Fatal(err)
		}
		return db
	}
	db := open()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = InstallSchemaTx(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_, remote, rows := intents(t)
	reserve(t, db, remote, rows)
	l := &Ledger{DB: db, Key: remote.Key, Side: syncreconcile.Remote}
	r := syncfs.Receipt{Key: remote.Key, PlanDigest: remote.PlanDigest, Generation: remote.Generation, Side: syncreconcile.Remote, Index: 0, Action: expectedActions(remote, syncreconcile.Remote)[0], State: "prepared"}
	if err = l.Prepare(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db = open()
	defer db.Close()
	l.DB = db
	lookup, err := l.Lookup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lookup.State != "applying" || len(lookup.Effects) != 1 || lookup.Effects[0].State != "prepared" {
		t.Fatalf("prepared evidence lost after reopen: %#v", lookup)
	}
	if err = l.Prepare(ctx, r); !errors.Is(err, ErrUnknown) {
		t.Fatalf("prepared effect replay accepted: %v", err)
	}
}

func TestLaterGenerationRequiresExactPriorBaseline(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	r := request()
	r.Direction = syncreconcile.Both
	r.Remote.Entries["f"] = r.Local.Entries["f"]
	first, err := syncreconcile.Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	baseline, ok := first.ProposedBaseline()
	if !ok {
		t.Fatal("missing initial proposal")
	}
	r.FirstRun = false
	r.Baseline = &baseline
	r.ExpectedGeneration = baseline.Generation
	second, err := syncreconcile.Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	local, rows, err := NewIntent("later-sync-key-12345", digest("fixed worker"), digest("later SSH control"), "later generation", syncreconcile.Local, r, second, promises(), promises(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = ReserveTx(ctx, tx, local, rows, observedRows(local.Side, rows)); err == nil {
		t.Fatal("missing prior baseline accepted")
	}
	tx.Rollback()
	raw, _ := json.Marshal(baseline)
	if _, err = db.ExecContext(ctx, `INSERT INTO sync_baselines(pair,generation,seal,record) VALUES(?,?,?,?)`, pairID(baseline.Binding), baseline.Generation, baseline.Seal, raw); err != nil {
		t.Fatal(err)
	}
	reserve(t, db, local, rows)
	remote := local
	remote.Side = syncreconcile.Remote
	reserve(t, db, remote, rows)
	l := &Ledger{DB: db, Key: local.Key, Side: syncreconcile.Local}
	rr := &Ledger{DB: db, Key: remote.Key, Side: syncreconcile.Remote}
	lr, err := l.CompleteOwnSide(ctx)
	if err != nil {
		t.Fatal(err)
	}
	remoteReceipt, err := rr.CompleteOwnSide(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.RecordPeerReceipt(ctx, remoteReceipt); err != nil {
		t.Fatal(err)
	}
	if err = rr.RecordPeerReceipt(ctx, lr); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE sync_baselines SET seal=? WHERE pair=?`, digest("corrupt"), pairID(baseline.Binding)); err != nil {
		t.Fatal(err)
	}
	if _, err = l.ConfirmBaseline(ctx); err == nil {
		t.Fatal("corrupt prior baseline accepted")
	}
	if _, err = db.ExecContext(ctx, `UPDATE sync_baselines SET seal=? WHERE pair=?`, baseline.Seal, pairID(baseline.Binding)); err != nil {
		t.Fatal(err)
	}
	got, err := l.ConfirmBaseline(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Generation != 2 {
		t.Fatalf("generation %d", got.Generation)
	}
}

func TestNativePublisherUsesDurableLedger(t *testing.T) {
	ctx := context.Background()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"source", "receiver", "work"} {
		if err = os.Mkdir(filepath.Join(base, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	open := func(name string) *syncfs.Root {
		r, e := syncfs.OpenRoot(filepath.Join(base, name))
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { r.Close() })
		return r
	}
	src, dst, workRoot := open("source"), open("receiver"), open("work")
	data := []byte("verified native bytes")
	if err = os.WriteFile(filepath.Join(src.Path(), "f"), data, 0600); err != nil {
		t.Fatal(err)
	}
	names, err := syncfs.DetectNamePolicy(dst, workRoot)
	if err != nil {
		t.Fatal(err)
	}
	limits := syncfs.ScanLimits{Entries: 100, Bytes: 1 << 20}
	local, err := src.Scan(ctx, nil, nil, names, syncfs.Metadata{}, limits)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := dst.Scan(ctx, []string{"f"}, nil, names, syncfs.Metadata{}, limits)
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := src.NativeID()
	if err != nil {
		t.Fatal(err)
	}
	receiverID, err := dst.NativeID()
	if err != nil {
		t.Fatal(err)
	}
	r := syncreconcile.Request{Binding: syncreconcile.Binding{Local: syncreconcile.Endpoint{ID: "source/account", RootID: sourceID}, Remote: syncreconcile.Endpoint{ID: "target/account", RootID: receiverID}, SelectionID: "all", MetadataID: "content"}, Direction: syncreconcile.Push, LocalPolicy: syncreconcile.Additive, RemotePolicy: syncreconcile.Full, Local: local, Remote: remote, FirstRun: true}
	plan, err := syncreconcile.Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = syncfs.VerifyPlanRoot(plan, syncreconcile.Local, src); err != nil {
		t.Fatal(err)
	}
	if err = syncfs.VerifyPlanRoot(plan, syncreconcile.Remote, src); err == nil {
		t.Fatal("source root accepted as the reviewed receiver")
	}
	i, rows, err := NewIntent("native-sync-key-12345", digest("fixed worker"), digest("native SSH control"), "native ledger test", syncreconcile.Remote, r, plan, promises(), promises(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = ReserveTx(ctx, tx, i, rows, remote); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ledger := &Ledger{DB: db, Key: i.Key, Side: syncreconcile.Remote}
	w, err := syncfs.CreateWorkspace(dst, workRoot, syncfs.Reservation{Key: i.Key, PlanDigest: plan.Digest(), Generation: plan.Generation(), Side: syncreconcile.Remote, DiskBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	if err = os.WriteFile(filepath.Join(w.StagePath(), "f"), data, 0600); err != nil {
		t.Fatal(err)
	}
	publisher, err := syncfs.NewBoundPublisher(dst, w, plan, names, syncfs.Metadata{}, nil, ledger)
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := publisher.ApplyAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].State != "applied" {
		t.Fatalf("receipts %#v", receipts)
	}
	if err = dst.Check(); err != nil {
		t.Fatal(err)
	}
	receipt, err := ledger.CompleteOwnSide(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.EffectCount != 1 {
		t.Fatalf("effect count %d", receipt.EffectCount)
	}
	got, err := os.ReadFile(filepath.Join(dst.Path(), "f"))
	if err != nil || string(got) != string(data) {
		t.Fatalf("published bytes %q: %v", got, err)
	}
}

func TestExactNonexecutionSealAllowsReviewedNewKey(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	req := request()
	plan, err := syncreconcile.Reconcile(req)
	if err != nil {
		t.Fatal(err)
	}
	controlFor := func(key string) model.SSHControlCommitment {
		return model.SSHControlCommitment{Schema: 1, Source: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Pair: strings.Repeat("c", 64), Revision: 1, Certificate: strings.Repeat("d", 64), Owner: "1000", Account: "operator", HostKeySHA256: strings.Repeat("e", 64), Key: key, PublicKeySHA256: strings.Repeat("f", 64), Reason: "sync reviewed", TTL: time.Minute, Resources: promises(), CWD: "/tmp", Kind: "exec", CommandSHA256: digest("arm sync-worker fixed")}
	}
	key := "sealed-sync-key-12345"
	control := controlFor(key)
	controlDigest, err := control.Digest()
	if err != nil {
		t.Fatal(err)
	}
	i, rows, err := NewIntent(key, control.CommandSHA256, controlDigest, control.Reason, syncreconcile.Local, req, plan, promises(), promises(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	reserve(t, db, i, rows)
	l := &Ledger{DB: db, Key: key, Side: syncreconcile.Local}
	seal := model.SSHKeyFence{Schema: 1, Source: control.Source, Target: control.Target, Key: key, State: "sealed", Control: &control, ControlSHA256: controlDigest, Actor: "peer:" + control.Target, Reason: "exact nonexecution", At: time.Now().UTC()}
	if err = seal.Validate(); err != nil {
		t.Fatal(err)
	}
	if err = l.RecordNonexecution(ctx, seal); err != nil {
		t.Fatal(err)
	}
	lookup, err := l.Lookup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lookup.State != "sealed" || lookup.Nonexecution == nil {
		t.Fatalf("seal evidence missing %#v", lookup)
	}
	if _, err = l.CompleteOwnSide(ctx); !errors.Is(err, ErrUnknown) {
		t.Fatalf("sealed sync completed: %v", err)
	}
	baseline, err := LoadBaseline(ctx, db, i.Binding, syncreconcile.Local)
	if err != nil || baseline != nil {
		t.Fatalf("sealed first run blocked fresh review: %v %#v", err, baseline)
	}
	newKey := "new-sync-key-12345678"
	newControl := controlFor(newKey)
	newDigest, err := newControl.Digest()
	if err != nil {
		t.Fatal(err)
	}
	next, newRows, err := NewIntent(newKey, newControl.CommandSHA256, newDigest, newControl.Reason, syncreconcile.Local, req, plan, promises(), promises(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	reserve(t, db, next, newRows)
}
