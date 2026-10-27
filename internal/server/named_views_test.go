package server

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
)

func withoutDisplayFields(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, child := range v {
			if k == "display_name" || k == "host_display_name" || k == "lease_display_name" {
				delete(v, k)
			} else {
				v[k] = withoutDisplayFields(child)
			}
		}
	case []any:
		for i := range v {
			v[i] = withoutDisplayFields(v[i])
		}
	}
	return v
}
func jsonNumbers(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var out any
	if err = d.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestNamedViewsPreserveOriginalWireValuesAndRecords(t *testing.T) {
	s := &Server{}
	l := model.Lease{ID: "0123456789abcdef0123456789abcdef", Host: "abcdef0123456789abcdef0123456789", Owner: "501", Request: model.Request{Reason: "unchanged", Resources: model.Resources{Memory: 9223372036854775807}}, Revision: 3}
	r := model.ResourceView{ID: "native/cpu", Host: l.Host, Kind: "cpu", BudgetTotal: json.Number("9223372036854775807"), Users: []model.ResourceUse{{Lease: l.ID, Quantity: json.Number("9007199254740993")}}}
	for _, tt := range []struct {
		op    string
		value any
	}{
		{"inspect", l}, {"status", model.Status{Host: l.Host, Leases: []model.Lease{l}, Resources: []model.ResourceView{r}}},
		{"resources", []model.ResourceView{r}}, {"storage_list", []model.Lease{l}},
		{"status", model.Status{}}, {"resources", []model.ResourceView(nil)}, {"resources", []model.ResourceView{}},
		{"storage_list", []model.Lease(nil)}, {"storage_list", []model.Lease{}},
	} {
		t.Run(tt.op, func(t *testing.T) {
			before := jsonNumbers(t, tt.value)
			view := s.namedView(tt.op, tt.value)
			if got := withoutDisplayFields(jsonNumbers(t, view)); !reflect.DeepEqual(got, before) {
				t.Fatalf("wire values changed\nbefore: %#v\nafter: %#v", before, got)
			}
			if !reflect.DeepEqual(jsonNumbers(t, tt.value), before) {
				t.Fatal("source record changed")
			}
		})
	}
	view := s.namedView("inspect", l)
	var old model.Lease
	raw, _ := json.Marshal(view)
	if err := json.Unmarshal(raw, &old); err != nil || !reflect.DeepEqual(old, l) {
		t.Fatal("ordinary existing client cannot decode additive view", err)
	}
	if view.(namedLease).DisplayName != readablename.FromID(readablename.Lease, l.ID) {
		t.Fatal(view)
	}
	raw, _ = json.Marshal(l)
	if bytes.Contains(raw, []byte("display_name")) {
		t.Fatal("display metadata persisted in model")
	}
}
func TestNamedViewsTouchOnlyDeclaredViews(t *testing.T) {
	s := &Server{}
	for _, op := range []string{"pool_mutate", "lease_review_inspect", "lease_review_reply", "node_stop_plan", "mesh_operator_stop_prepare", "workspace_admit", "events", "node_metadata"} {
		v := &struct {
			ID     string `json:"id"`
			Review string `json:"review"`
		}{ID: "unchanged", Review: "signed"}
		if s.namedView(op, v) != v {
			t.Fatal("review/receipt projected", op)
		}
	}
	for _, tt := range []struct {
		r    model.ResourceView
		want string
	}{
		{model.ResourceView{Kind: "cpu"}, "CPU"}, {model.ResourceView{Kind: "memory"}, "Memory"}, {model.ResourceView{Kind: "disk"}, "Disk"},
		{model.ResourceView{ID: "pool_11111111111111111111111111111111", Kind: "custom"}, readablename.FromID(readablename.Pool, "pool_11111111111111111111111111111111")},
		{model.ResourceView{Custom: &model.PoolView{Pool: model.Pool{Name: "operator chosen"}}}, "operator chosen"},
		{model.ResourceView{Port: &model.PortBinding{PortClaim: model.PortClaim{Name: "chosen port"}}}, "chosen port"},
		{model.ResourceView{Port: &model.PortBinding{PortClaim: model.PortClaim{Network: "tcp6", Address: "::1", Port: 1234}}}, "tcp6://[::1]:1234"},
	} {
		if got := resourceLabel(tt.r); got != tt.want {
			t.Fatal(got, tt.want)
		}
	}
}

func TestNamedViewsMeshSetupDefaultsAndSuppliedNames(t *testing.T) {
	for _, supplied := range []string{"", "operator-supplied"} {
		t.Run(supplied, func(t *testing.T) {
			s := meshFixture(t)
			t.Cleanup(s.closeMesh)
			status, err := s.Manager.Status()
			if err != nil {
				t.Fatal(err)
			}
			want := supplied
			if want == "" {
				want = readablename.FromID(readablename.Host, status.Host)
			}
			unconfigured, err := s.nodeList()
			if err != nil || len(unconfigured.Nodes) != 1 || unconfigured.Nodes[0].Name != "localhost" || unconfigured.Nodes[0].DisplayName != readablename.FromID(readablename.Host, status.Host) {
				t.Fatal(unconfigured, err)
			}
			prepared, err := s.prepareMeshSetup(mesh.PrepareRequest{Key: "default-name", IP: "127.0.0.1", Name: supplied})
			if err != nil || prepared.Identity.Host != status.Host || prepared.Identity.Name != want {
				t.Fatal(prepared, err)
			}
			s.closeMesh()
			registry, err := s.nodeList()
			if err != nil || registry.Nodes[0].Name != want || registry.Nodes[0].DisplayName != want {
				t.Fatal(registry, err)
			}
			// An existing reviewed identity keeps its original name, including
			// across a fresh setup request with a different supplied suggestion.
			prepared, err = s.prepareMeshSetup(mesh.PrepareRequest{Key: "keep-name", IP: "127.0.0.1", Name: "replacement-ignored"})
			if err != nil || prepared.Identity.Name != want {
				t.Fatal(prepared, err)
			}
		})
	}
}

func TestNamedViewsPeerReceiptsKeepFirstAndRetryShapes(t *testing.T) {
	s := &Server{}
	first := model.Lease{ID: "11111111111111111111111111111111", Revision: 2}
	retry, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"acquire", "run", "release", "cancel", "renew"} {
		for _, result := range []any{first, json.RawMessage(retry)} {
			before, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(s.namedPeerView(operation, result))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal(operation, string(before), string(after), err)
			}
		}
	}
}

func TestNamedViewsMeshLabelsFollowAuthorizationAndPolicyRedaction(t *testing.T) {
	s := meshFixture(t)
	capacity := int64(1)
	pool, err := s.Manager.MutatePool(model.PoolMutation{Operation: "create", Name: "private-pool-name-sentinel", Description: "private-description-sentinel", Scope: "host", Capacity: &capacity, Reason: "label fixture", Key: "label-pool"})
	if err != nil {
		t.Fatal(err)
	}
	budget, err := s.Manager.MutateBudget(model.BudgetMutation{Operation: "create", Name: "private-policy-sentinel", Reason: "label fixture", Key: "label-budget"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.Manager.Acquire(model.Request{Budget: budget.Budget.ID, Pools: []model.PoolClaim{{Pool: pool.Pool.ID, Quantity: 1}}, Reason: "public lease reason", TTL: time.Minute, Key: "label-lease"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Budget == nil || lease.PoolBinding == nil {
		t.Fatal("fixture lacks private policy evidence", lease)
	}
	service := &meshService{owner: s}
	for _, all := range []bool{false, true} {
		grant := mesh.Grant{Permissions: []string{"observe-host"}}
		if all {
			grant.Permissions = append(grant.Permissions, "observe-all")
		}
		peer := mesh.Peer{Identity: mesh.Identity{Host: strings.Repeat("a", 32)}, Grant: grant}
		value, err := service.dispatch(t.Context(), peer, mesh.Request{Operation: "status"})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(s.namedPeerView("status", value))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"private-pool-name-sentinel", "private-description-sentinel", "private-policy-sentinel", budget.Budget.ID, "budget_binding", "budget_policy", "budget_error", "pool_binding", "pool_error"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("label view leaked private policy", all, secret, string(raw))
			}
		}
		if !strings.Contains(string(raw), readablename.FromID(readablename.Pool, pool.Pool.ID)) {
			t.Fatal("pool summary label uses wrong domain", string(raw))
		}
		if strings.Contains(string(raw), readablename.FromID(readablename.Lease, lease.ID)) != all {
			t.Fatal("lease label ignored authorization scope", all, string(raw))
		}
	}
}

func TestNamedViewsPeerHostLabelWithoutLeaseDisclosure(t *testing.T) {
	host := strings.Repeat("d", 32)
	store, err := mesh.Init(t.TempDir()+"/mesh-home", host, "private-local-node-label", "127.0.0.1:40000", "127.0.0.1:40000")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := &Server{mesh: &meshService{store: store}}
	if s.hostDisplayName(host) != "private-local-node-label" {
		t.Fatal("fixture has no supplied local name")
	}
	clean, err := meshWithoutPolicy([]model.Lease{{ID: strings.Repeat("e", 32), Host: host, Request: model.Request{Budget: "private-budget-sentinel"}, BudgetError: &model.Error{Code: "private-policy-sentinel"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name  string
		value map[string]any
	}{
		{"host-only", map[string]any{"host": host}},
		{"leases-nil", map[string]any{"host": host, "leases": nil}},
		{"leases-typed-nil", map[string]any{"host": host, "leases": []model.Lease(nil)}},
		{"leases-empty", map[string]any{"host": host, "leases": []model.Lease{}}},
		{"leases-own-redacted", map[string]any{"host": host, "leases": clean}},
		{"preexisting-display", map[string]any{"host": host, "display_name": "private-local-node-label"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := jsonNumbers(t, tt.value)
			view, ok := s.namedPeerView("status", tt.value).(map[string]any)
			if !ok || view["host"] != host || view["display_name"] != readablename.FromID(readablename.Host, host) {
				t.Fatal("missing exact ID-derived remote host label", view)
			}
			if !reflect.DeepEqual(jsonNumbers(t, tt.value), before) {
				t.Fatal("source status map changed")
			}
			if !reflect.DeepEqual(withoutDisplayFields(jsonNumbers(t, view)), withoutDisplayFields(before)) {
				t.Fatal("original fields or null/empty shapes changed", view)
			}
			raw, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"private-local-node-label", "private-budget-sentinel", "private-policy-sentinel"} {
				if strings.Contains(string(raw), secret) {
					t.Fatal("remote label expanded local/private metadata", secret)
				}
			}
		})
	}
}
