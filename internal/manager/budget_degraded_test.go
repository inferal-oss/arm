package manager

import (
	"github.com/yrashk/arm/internal/model"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func assertBudgetDegraded(t *testing.T, m *Manager) {
	t.Helper()
	status, err := m.Status()
	if err != nil || status.BudgetError == nil || status.BudgetError.Code != "invalid_accounting" {
		t.Fatal("degraded authority not observable", status.BudgetError, err)
	}
	if _, err = m.Acquire(request("degraded-fresh"), nil); errorCode(err) != "invalid_accounting" {
		t.Fatal("degraded authority admitted fresh work", err)
	}
	if _, err = m.MutateBudget(model.BudgetMutation{Operation: "create", Name: "forbidden", Reason: "no policy changes while evidence is uncertain", Key: "degraded-policy"}); errorCode(err) != "invalid_accounting" {
		t.Fatal("degraded authority changed policy", err)
	}
}
func TestBudgetDegradedRestartPreservesProviderCleanupAndOwnership(t *testing.T) {
	for _, damage := range []string{"missing-root", "malformed-root", "missing-binding", "malformed-binding"} {
		t.Run(damage, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "arm")
			capacity := model.Resources{CPU: 1, Disk: 4096}
			m, err := Open(home, capacity)
			if err != nil {
				t.Fatal(err)
			}
			node := makePolicyBudget(t, m, "self", "saved-lookup")
			var savedKey string
			if err = m.db.QueryRow(`SELECT key FROM budget_operations LIMIT 1`).Scan(&savedKey); err != nil {
				t.Fatal(err)
			}
			owned, err := m.Acquire(storageRequest("verified"), nil)
			if err != nil {
				t.Fatal(err)
			}
			unsafe, err := m.Acquire(storageRequest("unverified"), nil)
			if err != nil {
				t.Fatal(err)
			}
			payload := filepath.Join(unsafe.ManagedDir, "keep")
			if err = os.WriteFile(payload, []byte("preserve unverified owned-path contents"), 0600); err != nil {
				t.Fatal(err)
			}
			unsafe.Artifact.Identity = nil
			if err = m.put(unsafe, "fixture.unverified_identity"); err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(t.TempDir(), "foreign.txt")
			if err = os.WriteFile(foreign, []byte("unrelated data"), 0600); err != nil {
				t.Fatal(err)
			}
			holder, err := m.Acquire(request("holder"), nil)
			if err != nil {
				t.Fatal(err)
			}
			waitRequest := request("waiting")
			waitRequest.Queue = true
			waiting, err := m.Acquire(waitRequest, nil)
			if err != nil {
				t.Fatal(err)
			}
			rootID := ownerBudgetID(m.host, m.owner)
			switch damage {
			case "missing-root":
				_, err = m.db.Exec(`DELETE FROM budgets WHERE id=?`, rootID)
			case "malformed-root":
				_, err = m.db.Exec(`UPDATE budgets SET data='{}' WHERE id=?`, rootID)
			case "missing-binding":
				_, err = m.db.Exec(`DELETE FROM lease_budget_bindings WHERE lease=?`, owned.ID)
			case "malformed-binding":
				_, err = m.db.Exec(`UPDATE lease_budget_bindings SET data='not-json' WHERE lease=?`, owned.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			m.Close()
			m, err = Open(home, capacity)
			if err != nil {
				t.Fatal("policy corruption disabled resource authority", err)
			}
			defer m.Close()
			m.Start()
			assertBudgetDegraded(t, m)
			status, _ := m.Status()
			if status.Reserved.Disk != 2048 {
				t.Fatal("degraded startup freed storage obligations", status.Reserved)
			}
			seen, err := m.Inspect(owned.ID)
			if err != nil || seen.BudgetError == nil || seen.ManagedDir != owned.ManagedDir || seen.Artifact.Identity == nil {
				t.Fatal(seen, err)
			}
			if _, err = m.Renew(owned.ID, 3*time.Minute); errorCode(err) != "invalid_accounting" {
				t.Fatal("renewal passed degraded guard", err)
			}
			lookup, err := m.LookupBudgetMutation(savedKey)
			if err != nil || !lookup.Found || lookup.Change.Budget.ID != node.ID {
				t.Fatal("read-only mutation evidence unavailable", lookup, err)
			}
			if _, err = m.End(holder.ID, "released"); err != nil {
				t.Fatal(err)
			}
			if _, err = m.End(owned.ID, "released"); err != nil {
				t.Fatal("verified cleanup disabled by policy error", err)
			}
			storageEventually(t, func() bool { l, _ := m.Inspect(owned.ID); return l.State == "terminal" })
			if _, err = os.Lstat(owned.ManagedDir); !os.IsNotExist(err) {
				t.Fatal("owned empty namespace not cleaned", err)
			}
			m.tick()
			still, err := m.Inspect(waiting.ID)
			if err != nil || still.State != "queued" {
				t.Fatal("degraded FIFO queue dispatched", still, err)
			}
			found := false
			for _, blocker := range still.QueueInfo.Blockers {
				if blocker.Code == "invalid_accounting" {
					found = true
				}
			}
			if !found {
				t.Fatal(still.QueueInfo)
			}
			if _, err = m.End(waiting.ID, "cancelled"); err != nil {
				t.Fatal(err)
			}
			if _, err = m.End(unsafe.ID, "expired"); err != nil {
				t.Fatal(err)
			}
			storageEventually(t, func() bool { l, _ := m.Inspect(unsafe.ID); return l.State == "needs_attention" })
			status, _ = m.Status()
			if status.Reserved.Disk != 1024 {
				t.Fatal("unverified cleanup falsely freed obligation", status.Reserved)
			}
			if _, err = m.StorageCleanup(unsafe.ID); err != nil {
				t.Fatal("ordinary explicit cleanup retry disabled", err)
			}
			storageEventually(t, func() bool { l, _ := m.Inspect(unsafe.ID); return l.State == "needs_attention" })
			// needs_attention can be published just before the cleanup worker
			// releases its busy flag. Handoff must wait for that owned step.
			storageEventually(t, func() bool {
				_, err := m.StorageHandoff(unsafe.ID)
				if err != nil && errorCode(err) != "operation_busy" {
					t.Fatal("ordinary explicit handoff disabled", err)
				}
				return err == nil
			})
			for path, want := range map[string]string{payload: "preserve unverified owned-path contents", foreign: "unrelated data"} {
				raw, err := os.ReadFile(path)
				if err != nil || string(raw) != want {
					t.Fatal("degradation weakened ownership", path, string(raw), err)
				}
			}
			assertBudgetDegraded(t, m)
			var count int
			m.db.QueryRow(`SELECT COUNT(*) FROM budgets WHERE id=?`, rootID).Scan(&count)
			if damage == "missing-root" && count != 0 {
				t.Fatal("missing root repaired")
			}
			if damage == "malformed-root" {
				var raw string
				m.db.QueryRow(`SELECT data FROM budgets WHERE id=?`, rootID).Scan(&raw)
				if raw != "{}" {
					t.Fatal("corrupt root rewritten", raw)
				}
			}
		})
	}
}
func TestBudgetDegradationDoesNotSwallowGeneralSchemaFailure(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	m, err := Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`ALTER TABLE budgets RENAME TO old_budgets`); err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`CREATE TABLE budgets (wrong TEXT)`); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err = Open(home, model.Resources{CPU: 1})
	if err == nil {
		m.Close()
		t.Fatal("general SQL schema failure became policy-degraded startup")
	}
}
