package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
)

func forgetCLIState(t *testing.T) string {
	t.Helper()
	home := resetCLIState(t, false, false)
	m, err := manager.Open(home, model.Resources{CPU: 2, Disk: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	capacity := int64(2)
	pool, err := m.MutatePool(model.PoolMutation{Operation: "create", Scope: "global", Name: "forget owned permit", Capacity: &capacity, Reason: "private fixture", Key: "fixture-pool"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Acquire(model.Request{Pools: []model.PoolClaim{{Pool: pool.Pool.ID, Quantity: 1}}, TTL: time.Hour, Reason: "outstanding counted obligation", Key: "fixture-lease"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`DROP TABLE host_pressure_state; DROP TABLE host_pressure_outbox; CREATE TABLE future_obligations(payload TEXT); INSERT INTO future_obligations VALUES('PRIVATE old database payload must disappear')`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	return home
}
func TestDatabaseForgetPreviewAndDeclinePreserveUnsupportedActiveLedger(t *testing.T) {
	for _, kind := range []string{"dry-run", "pipe", "terminal-no", "terminal-eof"} {
		t.Run(kind, func(t *testing.T) {
			home := forgetCLIState(t)
			before := resetCLITree(t, home)
			var out, errout bytes.Buffer
			a := app{ctx: context.Background(), home: home, in: strings.NewReader("n\n"), out: &out, err: &errout}
			args := []string{"remove", "--forget", "--reason", "intentional ledger loss"}
			if kind == "dry-run" {
				args = append(args, "--dry-run")
			}
			if strings.HasPrefix(kind, "terminal") {
				a.outputTTY = true
				a.inputTTY = true
			}
			if kind == "terminal-eof" {
				a.in = strings.NewReader("")
			}
			code, err := a.database(args)
			if kind == "dry-run" {
				if err != nil || code != 0 {
					t.Fatal(code, err)
				}
			} else if err == nil || code == 0 {
				t.Fatal("forget proceeded without confirmation")
			}
			if strings.HasPrefix(kind, "terminal") {
				if !strings.Contains(out.String(), "FORGET MODE") || !strings.Contains(out.String(), "[y/N]") || strings.Contains(out.String(), "0 leases") {
					t.Fatal("misleading forget preview", out.String())
				}
			} else {
				var preview databaseRemovePreview
				if err := json.Unmarshal(out.Bytes(), &preview); err != nil || !preview.Plan.Forget || preview.Plan.Proof.Complete || preview.Plan.Proof.Settled || len(preview.Plan.Files) != 4 {
					t.Fatal("forget presented fake settlement", err)
				}
			}
			if !reflect.DeepEqual(before, resetCLITree(t, home)) {
				t.Fatal("unconfirmed forget changed state")
			}
		})
	}
}
func TestDatabaseForgetExplicitlyErasesUnsupportedActiveLedgerAndPreservesLaterDB(t *testing.T) {
	home := forgetCLIState(t)
	var out, errout bytes.Buffer
	if code := Main([]string{"--home", home, "database", "remove", "--yes", "--reason", "ordinary still strict"}, strings.NewReader(""), &out, &errout); code == 0 {
		t.Fatal("ordinary remove forgot unsupported/unsettled evidence")
	}
	out.Reset()
	errout.Reset()
	if code := Main([]string{"--home", home, "database", "remove", "--forget", "--yes", "--reason", "explicit loss"}, strings.NewReader(""), &out, &errout); code != 0 {
		t.Fatal(code, out.String(), errout.String())
	}
	var outcome databaseRemoveOutcome
	if err := json.Unmarshal(out.Bytes(), &outcome); err != nil || !outcome.Result.Completed || !outcome.Plan.Forget || outcome.Plan.Proof.Settled {
		t.Fatal(outcome, err)
	}
	for _, entry := range resetCLITree(t, home) {
		if strings.Contains(entry.Data, "PRIVATE old database payload must disappear") {
			t.Fatal("original database copy retained")
		}
	}
	// Completion lookup is immutable. A subsequent current DB cannot become the
	// target of an old key, even though ordinary removal uses the same command.
	lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = database.InstallResetSeed(t.Context(), home, lock); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	m, err := manager.Open(home, model.Resources{CPU: 2, Disk: 1 << 30})
	if err != nil {
		lock.Close()
		t.Fatal(err)
	}
	status, err := m.Status()
	if err != nil || status.Host != outcome.Plan.NewHost {
		t.Fatal("new authority was not installed", err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	before := resetCLITree(t, home)
	out.Reset()
	errout.Reset()
	if code := Main([]string{"--home", home, "database", "remove", "--continue", outcome.Result.Operation, "--yes"}, strings.NewReader(""), &out, &errout); code != 0 {
		t.Fatal(code, errout.String())
	}
	if !reflect.DeepEqual(before, resetCLITree(t, home)) {
		t.Fatal("completed continuation touched a later database")
	}
	out.Reset()
	errout.Reset()
	if code := Main([]string{"--home", home, "database", "remove", "--continue", outcome.Result.Operation, "--forget", "--yes"}, strings.NewReader(""), &out, &errout); code == 0 {
		t.Fatal("continuation accepted mode mutation")
	}
}
