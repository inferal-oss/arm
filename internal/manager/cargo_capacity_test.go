package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/cargoprofile"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

func TestCargoAdmissionBoundCannotPersistHistoryStartupWillRefuse(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	capacity := model.Resources{CPU: 1, Memory: 1024, Disk: 2048}
	m, e := Open(home, capacity)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if m != nil {
			_ = m.Close()
		}
	}()
	if e = m.ConfigureCargoHelper(func() (string, cargoprofile.HelperRuntime, error) {
		return "", cargoprofile.HelperRuntime{}, errors.New("no-spawn fixture must never launch")
	}); e != nil {
		t.Fatal(e)
	}
	store, e := m.Acquire(storageRequest("cargo-history-parent"), nil)
	if e != nil {
		t.Fatal(e)
	}
	past := time.Now().Add(-time.Second).UTC()
	r := request("cargo-history-template")
	r.Profile = "rust-build"
	r.StorageLease = store.ID
	r.StartDeadline = &past
	run := RunRequest{Request: r, Command: []string{"cargo", "check", "--offline"}, CWD: t.TempDir()}
	template, e := m.Acquire(r, &run)
	if e != nil || template.State != "terminal" || template.Outcome != "rejected" {
		t.Fatal(template, e)
	}
	// Use the actual transaction producer with valid small, terminal no-spawn
	// admissions. No compiler/tool invocation or 4096 public O(n) admissions.
	m.mu.Lock()
	tx, e := m.db.Begin()
	if e != nil {
		m.mu.Unlock()
		t.Fatal(e)
	}
	for i := 1; i < cargoRecordCountLimit-1; i++ {
		l := template
		l.ID = local.ID()
		l.Request.Key = fmt.Sprintf("cargo-history-%d", i)
		q := run
		q.Request = l.Request
		projectCargo(&l, cargoNewRecord(l, &q))
		raw, _ := json.Marshal(l)
		saved, _ := json.Marshal(q)
		if _, e = tx.Exec(`INSERT INTO leases(id,request_key,data,run) VALUES(?,?,?,?)`, l.ID, l.Request.Key, string(raw), string(saved)); e == nil {
			e = insertCargoRecord(tx, l, &q)
		}
		if e == nil {
			e = insertBudgetBinding(tx, l.ID, *l.Budget, "budget.bound")
		}
		if e != nil {
			_ = tx.Rollback()
			m.mu.Unlock()
			t.Fatal(i, e)
		}
	}
	e = tx.Commit()
	m.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	// The actual public admission at4096 succeeds, at4097 fails atomically.
	r.Key = "cargo-history-last"
	run.Request = r
	last, e := m.Acquire(r, &run)
	if e != nil || last.State != "terminal" {
		t.Fatal(last, e)
	}
	snapshot := func() map[string]int64 {
		t.Helper()
		m.mu.Lock()
		defer m.mu.Unlock()
		out := map[string]int64{}
		for _, table := range []string{"leases", "cargo_workloads", "events", "lease_budget_bindings", "pool_claim_bindings", "metadata"} {
			var count int64
			if e := m.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); e != nil {
				t.Fatal(e)
			}
			out[table] = count
		}
		return out
	}
	before := snapshot()
	if before["cargo_workloads"] != cargoRecordCountLimit {
		t.Fatal(before)
	}
	r.Key = "cargo-history-overflow"
	run.Request = r
	if _, e = m.Acquire(r, &run); errorCode(e) != "cargo_capacity_exhausted" {
		t.Fatal("overflow admission was not refused", e)
	}
	after := snapshot()
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("refusal leaked durable admission evidence", before, after)
	}
	r.Key = "cargo-history-last"
	run.Request = r
	retry, e := m.Acquire(r, &run)
	if e != nil || retry.ID != last.ID || retry.Revision != last.Revision {
		t.Fatal("complete history blocked exact existing retry", retry, e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	m = nil
	m, e = Open(home, capacity)
	if e != nil {
		t.Fatal("largest publicly admissible Cargo ledger cannot restart", e)
	}
	var count int
	if e = m.db.QueryRow(`SELECT COUNT(*) FROM cargo_workloads`).Scan(&count); e != nil || count != cargoRecordCountLimit {
		t.Fatal(count, e)
	}
}
