package manager

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/model"
)

func TestOwnerUsernameIsDurableSnapshotNotMutableAuthorization(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	capacity := model.Resources{CPU: 2, Memory: 1024, Disk: 2048}
	m, err := Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			m.Close()
		}
	}()
	m.ownerUsername = "account-at-creation"
	l, err := m.Acquire(request("record-owner"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if l.Owner != m.owner || l.OwnerUsername != "account-at-creation" {
		t.Fatalf("missing durable account: %+v", l)
	}
	events, err := m.Events(0)
	if err != nil {
		t.Fatal(err)
	}
	var created model.Lease
	for _, ev := range events {
		if ev.Entity == l.ID && ev.Type == "lease.created" {
			if err = json.Unmarshal(ev.Data, &created); err != nil {
				t.Fatal(err)
			}
		}
	}
	if created.OwnerUsername != l.OwnerUsername || created.Owner != l.Owner {
		t.Fatalf("journal lost account snapshot: %+v", created)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	m, err = Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	closed = false
	m.ownerUsername = "account-after-rename"
	s, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if s.OwnerUsername != "account-after-rename" || len(s.Leases) != 1 || s.Leases[0].OwnerUsername != "account-at-creation" || s.Resources[0].Users[0].OwnerUsername != "account-at-creation" {
		t.Fatalf("status relabelled historical identity: %+v", s)
	}
	ended, err := m.End(l.ID, "released")
	if err != nil || ended.Owner != l.Owner || ended.OwnerUsername != l.OwnerUsername {
		t.Fatalf("release changed owner identity: %+v %v", ended, err)
	}

	legacy, err := m.Acquire(request("historical-uid-only"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`UPDATE leases SET data=json_remove(data,'$.owner_username') WHERE id=?`, legacy.ID); err != nil {
		t.Fatal(err)
	}
	historical, err := m.Renew(legacy.ID, 2*time.Minute)
	if err != nil || historical.OwnerUsername != "" || historical.Owner != m.owner {
		t.Fatalf("invented historical username: %+v %v", historical, err)
	}
}

func TestPoolAndMessageActorSnapshotsSurviveRenameAndRetry(t *testing.T) {
	m := newManager(t)
	m.ownerUsername = "creator"
	capacity := int64(2)
	pool, err := m.MutatePool(model.PoolMutation{Operation: "create", Name: "devices", Scope: "global", Capacity: &capacity, Reason: "test actor recording", Key: "owner-pool"})
	if err != nil {
		t.Fatal(err)
	}
	if pool.Actor != m.owner || pool.ActorUsername != "creator" || pool.Pool.OwnerUsername != "creator" {
		t.Fatalf("missing creator attribution: %+v", pool)
	}
	request := MessageRequest{Key: "owner-message", Audience: "operator", Body: "operator question", Targets: []agents.Contact{}}
	msg, err := m.SendMessage(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Owner != m.owner || msg.OwnerUsername != "creator" {
		t.Fatalf("missing message owner: %+v", msg)
	}
	m.ownerUsername = "renamed"
	delta := int64(1)
	change, err := m.MutatePool(model.PoolMutation{Operation: "adjust", ID: pool.Pool.ID, Delta: &delta, Reason: "test renamed actor", Key: "owner-pool-adjust"})
	if err != nil {
		t.Fatal(err)
	}
	if change.ActorUsername != "renamed" || change.Pool.OwnerUsername != "creator" {
		t.Fatalf("mutation rewrote creator or lost actor: %+v", change)
	}
	updated, err := m.SendMessage(context.Background(), request)
	if err != nil || updated.ID != msg.ID || updated.OwnerUsername != "creator" {
		t.Fatalf("retry rewrote message identity: %+v %v", updated, err)
	}
	history, err := m.PoolHistory(pool.Pool.ID, 0)
	if err != nil || len(history.Entries) != 2 || history.Entries[0].ActorUsername != "creator" || history.Entries[1].ActorUsername != "renamed" {
		t.Fatalf("journal lost account snapshots: %+v %v", history, err)
	}
}
