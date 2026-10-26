package database_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
)

func proofFixture(t *testing.T, active bool) string {
	t.Helper()
	return proofFixtureResources(t, active, model.Resources{CPU: 1})
}

func proofFixtureResources(t *testing.T, active bool, resources model.Resources) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(home, 0700)
	if err = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := manager.Open(home, model.Resources{CPU: 2, Memory: 4096, Disk: 4096})
	if err != nil {
		t.Fatal(err)
	}
	capacity := int64(9223372036854775807)
	pool, err := m.MutatePool(model.PoolMutation{Operation: "create", Name: "exact permits", Scope: "global", Capacity: &capacity, Reason: "private settled proof", Key: "pool-create"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.Acquire(model.Request{Resources: resources, Pools: []model.PoolClaim{{Pool: pool.Pool.ID, Quantity: 1}}, TTL: time.Minute, Reason: "fixture owned claim", Key: "fixture-claim"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		if _, err = m.End(l.ID, "released"); err != nil {
			t.Fatal(err)
		}
	}
	contact, err := m.RegisterContact(agents.Contact{ID: "fixture-agent", Adapter: "generic", Delivery: "poll", Session: "11111111-1111-4111-8111-111111111111", CWD: home})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.SendMessage(context.Background(), manager.MessageRequest{Key: "fixture-send", Audience: contact.ID, Body: "private message is never in proof output", Targets: []agents.Contact{contact}}); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	return home
}
func prove(t *testing.T, home string) (database.SettledProof, error) {
	t.Helper()
	s, err := database.Offline(context.Background(), home)
	if err != nil {
		return database.SettledProof{}, err
	}
	defer s.Close()
	return database.ProveDatabaseSettled(context.Background(), s, strconv.Itoa(os.Geteuid()))
}
func edit(t *testing.T, home, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func TestSettledProofActualWriterHistoryAndActiveBlock(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(strconv.FormatBool(active), func(t *testing.T) {
			home := proofFixture(t, active)
			p, err := prove(t, home)
			if err != nil {
				t.Fatal(err)
			}
			if !p.Complete || p.Settled == active || p.Tables["leases"] != 1 || len(p.SHA256) != 64 {
				t.Fatalf("incorrect proof %+v", p)
			}
			raw, _ := json.Marshal(p)
			if strings.Contains(string(raw), "private message") {
				t.Fatal("proof leaked message body")
			}
		})
	}
}
func TestSettledProofRefusesUnknownSchemaAndEvidence(t *testing.T) {
	for _, kind := range []string{"table", "trigger", "metadata", "duplicate", "null-time", "other-owner", "orphan-operation", "pending-delivery", "unknown-delivery", "missing-parent", "null-count", "missing-resource", "oversize-record", "pool-request-mismatch", "lost-pool-journal", "sequence-behind", "future-subpool", "token-constraint-collision"} {
		t.Run(kind, func(t *testing.T) {
			home := proofFixture(t, false)
			switch kind {
			case "table":
				edit(t, home, `CREATE TABLE unfamiliar_obligations(data TEXT)`)
			case "trigger":
				edit(t, home, `CREATE TRIGGER unfamiliar BEFORE DELETE ON leases BEGIN SELECT 1; END`)
			case "metadata":
				edit(t, home, `INSERT INTO metadata VALUES('future_pending','{}')`)
			case "duplicate":
				edit(t, home, `UPDATE leases SET data=replace(data,'"state":"terminal"','"state":"active","state":"terminal"')`)
			case "null-time":
				edit(t, home, `UPDATE leases SET data=json_set(data,'$.ended_at',NULL)`)
			case "other-owner":
				edit(t, home, `UPDATE leases SET data=json_set(data,'$.owner','4294967294')`)
			case "orphan-operation":
				edit(t, home, `UPDATE message_operations SET message='missing'`)
			case "pending-delivery":
				edit(t, home, `UPDATE messages SET data=json_set(data,'$.deliveries.fixture-agent.state','pending')`)
			case "unknown-delivery":
				edit(t, home, `UPDATE messages SET data=json_set(data,'$.deliveries.fixture-agent.state','future')`)
			case "null-count":
				edit(t, home, `UPDATE leases SET data=json_set(data,'$.request.resources.memory_bytes',NULL)`)
			case "missing-resource":
				edit(t, home, `UPDATE leases SET data=json_remove(data,'$.request.resources.cpu')`)
			case "oversize-record":
				edit(t, home, `UPDATE leases SET data=?`, strings.Repeat("x", database.MaxRecordBytes+1))
			case "pool-request-mismatch":
				edit(t, home, `UPDATE pool_operations SET request=json_set(request,'$.capacity',0)`)
			case "lost-pool-journal":
				edit(t, home, `DELETE FROM pool_changes`)
			case "token-constraint-collision":
				edit(t, home, `ALTER TABLE leases RENAME TO previous_leases`)
				edit(t, home, `CREATE TABLE leases(id TEXT PRIMARY KEY,request_key TEXTUNIQUE,data TEXT NOT NULL,run TEXT)`)
				edit(t, home, `INSERT INTO leases SELECT * FROM previous_leases`)
				edit(t, home, `DROP TABLE previous_leases`)
			case "future-subpool":
				edit(t, home, `UPDATE pools SET data=json_set(data,'$.parent','unrouted-future-parent')`)
			case "sequence-behind":
				edit(t, home, `UPDATE sqlite_sequence SET seq=0 WHERE name='events'`)
			case "missing-parent":
				edit(t, home, `UPDATE lease_budget_bindings SET data=json_set(data,'$.path',json('["missing"]'),'$.selected','missing')`)
			}
			p, err := prove(t, home)
			if err == nil && p.Settled {
				t.Fatalf("unsafe %s was settled: %+v", kind, p)
			}
		})
	}
}
func TestFencedManagerDoesNotCreateOrMigrateDatabase(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(home, 0700)
	if err = os.WriteFile(filepath.Join(home, local.ResetFenceName), []byte(`{"phase":"completed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if m, err := manager.Open(home, model.Resources{CPU: 1}); err == nil {
		m.Close()
		t.Fatal("fenced manager opened")
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 1 || entries[0].Name() != local.ResetFenceName {
		t.Fatal("fenced startup changed home")
	}
}

func TestSettledProofRefusesForeignAuthenticatedOwnerAndCancellation(t *testing.T) {
	home := proofFixture(t, false)
	s, err := database.Offline(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if p, err := database.ProveDatabaseSettled(context.Background(), s, "4294967294"); err == nil || p.Settled || p.Complete {
		t.Fatal("caller supplied owner authorized another authority")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := database.ProveDatabaseSettled(ctx, s, strconv.Itoa(os.Geteuid())); err == nil || p.Settled || p.Complete {
		t.Fatal("cancelled proof certified settlement")
	}
}
