package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBudgetNativeOnlyClosedBrowserAndMeshBoundary(t *testing.T) {
	s := meshFixture(t)
	change := model.BudgetMutation{Operation: "create", Name: "native policy", Reason: "test exact native authority", Key: "native-budget-key"}
	raw, _ := json.Marshal(change)
	result, err := s.dispatch(t.Context(), model.WireRequest{Operation: "budget_mutate", Params: raw})
	if err != nil || result == nil {
		t.Fatal(result, err)
	}
	peer := mesh.Peer{Identity: mesh.Identity{Host: strings.Repeat("b", 32)}, Grant: mesh.Grant{Permissions: []string{"observe-host", "observe-all", "observe-own", "lease-own", "run", "message", "contact-read"}, OperatorInbox: true}}
	service := &meshService{owner: s}
	for _, op := range []string{"budget_mutate", "budget_list", "budget_inspect", "budget_consumers", "budget_history", "budget_lookup"} {
		ctx := context.WithValue(t.Context(), browserContextKey{}, true)
		value, err := s.dispatch(ctx, model.WireRequest{Operation: op, Params: raw})
		var api *model.Error
		if !errors.As(err, &api) || api.Code != "forbidden" || value != nil {
			t.Fatalf("browser %s: %#v %v", op, value, err)
		}
		value, err = service.dispatch(t.Context(), peer, mesh.Request{Operation: op, Key: "peer-key", Params: raw})
		if !errors.As(err, &api) || api.Code != "forbidden" || value != nil {
			t.Fatalf("mesh %s: %#v %v", op, value, err)
		}
	}
	lookup, err := s.Manager.LookupBudgetMutation(change.Key)
	if err != nil || !lookup.Found {
		t.Fatal(lookup, err)
	}
}

func TestBudgetMeshLeaseProjectionAppliesToFreshStatusAndDurableReplay(t *testing.T) {
	s := meshFixture(t)
	service := &meshService{owner: s}
	peer := mesh.Peer{Identity: mesh.Identity{Host: strings.Repeat("d", 32)}, Grant: mesh.Grant{Permissions: []string{"observe-host", "observe-own", "observe-all", "lease-own"}, Limits: model.Resources{CPU: 4}, MaxTTL: time.Hour}}
	node, err := s.Manager.MutateBudget(model.BudgetMutation{Operation: "create", Name: "private-policy", Reason: "private local metadata", Key: "private-policy"})
	if err != nil {
		t.Fatal(err)
	}
	cpu := float64(1)
	_, err = s.Manager.MutateBudget(model.BudgetMutation{Operation: "set", ID: node.Budget.ID, Revision: &node.Budget.Revision, Limits: &model.BudgetLimits{CPU: &cpu}, Reason: "private local ceiling", Key: "private-policy-set"})
	if err != nil {
		t.Fatal(err)
	}
	// An explicitly authorized observe-all peer can see ordinary local lease
	// attribution, but not gain the new native-only policy definition surface.
	localLease, err := s.Manager.Acquire(model.Request{Budget: node.Budget.ID, Resources: model.Resources{CPU: .1}, Reason: "local private policy", TTL: time.Minute, Key: "local-private"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	check := func(value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range []string{"budget_binding", "budget_policy", "budget_error", node.Budget.ID, "private-policy"} {
			if strings.Contains(string(raw), token) {
				t.Fatalf("mesh projection leaked %s: %s", token, raw)
			}
		}
	}
	status, err := service.dispatch(t.Context(), peer, mesh.Request{Operation: "status"})
	if err != nil {
		t.Fatal(err)
	}
	check(status)
	req, _ := json.Marshal(model.Request{Resources: model.Resources{CPU: .1}, TTL: time.Minute, Reason: "peer holder"})
	acquired, err := service.dispatch(t.Context(), peer, mesh.Request{Operation: "acquire", Host: localLease.Host, Key: "peer-budget-holder", Params: req})
	if err != nil {
		t.Fatal(err)
	}
	check(acquired)
	lease := acquired.(model.Lease)
	if lease.Budget != nil || lease.Request.Budget != "" {
		t.Fatal(lease)
	}
	// Inspection has a closed schema; a renewal-only field must not be
	// accepted merely to accommodate this redaction/replay fixture.
	invalid, err := json.Marshal(map[string]any{"id": lease.ID, "ttl_ns": int64(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.dispatch(t.Context(), peer, mesh.Request{Operation: "inspect", Params: invalid})
	var api *model.Error
	if !errors.As(err, &api) || api.Code != "invalid_request" {
		t.Fatalf("inspection accepted an unknown renewal field: %v", err)
	}
	for _, op := range []string{"inspect", "renew", "release"} {
		fields := map[string]any{"id": lease.ID}
		if op == "renew" {
			fields["ttl_ns"] = int64(2 * time.Minute)
		}
		params, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		request := mesh.Request{Operation: op, Key: "peer-projection-" + op, Params: params}
		first, err := service.dispatch(t.Context(), peer, request)
		if err != nil {
			t.Fatal(op, err)
		}
		check(first)
		again, err := service.dispatch(t.Context(), peer, request)
		if err != nil {
			t.Fatal(op, err)
		}
		check(again)
	}
	// Local evidence remains intact and authoritative after the projection.
	native, err := s.Manager.Inspect(localLease.ID)
	if err != nil || native.Budget == nil || native.Budget.Selected != node.Budget.ID || len(native.BudgetPolicy) != 2 {
		t.Fatal(native, err)
	}
}

func TestBudgetMeshDegradedStatusAndLeaseDoNotExposePolicyFailure(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	capacity := model.Resources{CPU: 2}
	native, err := manager.Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := native.Acquire(model.Request{Resources: model.Resources{CPU: .1}, TTL: time.Minute, Reason: "peer recovery fixture", Key: "peer-fixture"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	native.Close()
	database, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Preserve valid root metadata while making its schema carry a private error
	// sentinel. The native degraded-status detail must never become mesh data.
	var rootID string
	var raw []byte
	if err = database.QueryRow(`SELECT id,data FROM budgets WHERE parent IS NULL`).Scan(&rootID, &raw); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	fields["private-policy-damage-sentinel"] = json.RawMessage(`true`)
	raw, _ = json.Marshal(fields)
	if _, err = database.Exec(`UPDATE budgets SET data=? WHERE id=?`, string(raw), rootID); err != nil {
		t.Fatal(err)
	}
	// Attribution here is test fixture construction, never a forwarded caller
	// field. It makes the preexisting exact lease visible to observe-own.
	var leaseRaw []byte
	if err = database.QueryRow(`SELECT data FROM leases WHERE id=?`, lease.ID).Scan(&leaseRaw); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(leaseRaw, &lease)
	peerID := strings.Repeat("e", 32)
	lease.Request.Peer = peerID
	leaseRaw, _ = json.Marshal(lease)
	if _, err = database.Exec(`UPDATE leases SET data=? WHERE id=?`, string(leaseRaw), lease.ID); err != nil {
		t.Fatal(err)
	}
	database.Close()
	native, err = manager.Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	status, err := native.Status()
	if err != nil || status.BudgetError == nil || !strings.Contains(status.BudgetError.Message, "private-policy-damage-sentinel") {
		t.Fatal(status.BudgetError, err)
	}
	service := &meshService{owner: &Server{Manager: native, home: home}}
	peer := mesh.Peer{Identity: mesh.Identity{Host: peerID}, Grant: mesh.Grant{Permissions: []string{"observe-host", "observe-own"}}}
	params, _ := json.Marshal(map[string]string{"id": lease.ID})
	for _, op := range []string{"status", "inspect"} {
		value, err := service.dispatch(t.Context(), peer, mesh.Request{Operation: op, Params: params})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(value)
		for _, token := range []string{"budget_error", "budget_binding", "budget_policy", "private-policy-damage-sentinel"} {
			if strings.Contains(string(encoded), token) {
				t.Fatalf("%s exposed %s: %s", op, token, encoded)
			}
		}
	}
}
