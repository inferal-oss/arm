package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
)

func TestNamedPoolViewsPreserveExactReviewAndWireShapes(t *testing.T) {
	id := strings.Repeat("a", 32)
	use := model.ResourceUse{Lease: id, Quantity: json.Number("9223372036854775807")}
	review := &model.PoolReview{Authority: strings.Repeat("b", 32), Consumers: []model.LeaseRevision{{ID: id, Revision: 7}}, Hierarchy: &model.PoolHierarchyReview{Schema: 1, SHA256: strings.Repeat("c", 64)}}
	pool := model.PoolView{Pool: model.Pool{ID: "pool_" + strings.Repeat("d", 32), Capacity: 9223372036854775807}, Review: review, Reserved: 9223372036854775807, Users: []model.ResourceUse{use}, Queued: []model.ResourceUse{use}}
	for _, tt := range []struct {
		name  string
		value any
	}{
		{"list", []model.PoolView{pool}}, {"inspect", pool},
		{"consumers", model.PoolConsumerPage{Entries: []model.PoolConsumer{{Use: use, Claims: []model.BoundPoolClaim{}}}, More: true, Cursor: "unchanged"}},
		{"list-nil", []model.PoolView(nil)}, {"list-empty", []model.PoolView{}},
		{"uses-nil", model.PoolView{}}, {"uses-empty", model.PoolView{Users: []model.ResourceUse{}, Queued: []model.ResourceUse{}}},
		{"entries-nil", model.PoolConsumerPage{}}, {"entries-empty", model.PoolConsumerPage{Entries: []model.PoolConsumer{}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			view, err := boundedPoolRead(tt.value, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(withoutDisplayFields(jsonNumbers(t, view)), jsonNumbers(t, tt.value)) {
				t.Fatal("projection changed canonical review, integer or nil/empty/omitted fields")
			}
			after, err := json.Marshal(tt.value)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("projection modified source", err)
			}
			if strings.Contains(string(before), id) {
				raw, _ := json.Marshal(view)
				if !bytes.Contains(raw, []byte(readablename.FromID(readablename.Lease, id))) {
					t.Fatal("consumer label missing")
				}
			}
		})
	}
	request := model.PoolMutation{Operation: "adjust", ID: pool.Pool.ID, Reviewed: review, Key: "exact-retry-key", Reason: "same review"}
	for _, value := range []any{request, model.PoolChange{Pool: pool.Pool}, json.RawMessage(`{"idempotency_key":"exact-retry-key"}`)} {
		before, _ := json.Marshal(value)
		after, _ := json.Marshal(namedPoolRead(value))
		if !bytes.Equal(before, after) {
			t.Fatal("request, signed review or retained receipt changed")
		}
	}
}

func TestNamedPoolReadBudgetCountsFinalProjectedBytes(t *testing.T) {
	pool := model.PoolView{Users: make([]model.ResourceUse, 800)}
	for i := range pool.Users {
		pool.Users[i].Lease = strings.Repeat("a", 32)
	}
	base, err := json.Marshal(pool)
	if err != nil {
		t.Fatal(err)
	}
	padding := (3 << 20) - 1 - len(base)
	for i := range pool.Users {
		n := padding / len(pool.Users)
		if i < padding%len(pool.Users) {
			n++
		}
		if n < 0 || n > 4096 {
			t.Fatal("fixture reason exceeds actual input bound", n)
		}
		pool.Users[i].Reason = strings.Repeat("x", n)
	}
	before, _ := json.Marshal(pool)
	if len(before) != (3<<20)-1 {
		t.Fatal("unprojected fixture is not just below budget", len(before))
	}
	value, err := boundedPoolRead(pool, nil)
	var api *model.Error
	if value != nil || !errors.As(err, &api) || api.Code != "response_too_large" {
		t.Fatal("labels bypassed final response budget", err)
	}
	after, _ := json.Marshal(pool)
	if !bytes.Equal(before, after) {
		t.Fatal("oversized read truncated authoritative consumers")
	}
}

func TestNamedPoolReadsAreLocalReadOnlyAndKeepCanonicalLease(t *testing.T) {
	s := meshFixture(t)
	capacity := int64(2)
	change, err := s.Manager.MutatePool(model.PoolMutation{Operation: "create", Scope: "global", Name: "named consumer fixture", Capacity: &capacity, Reason: "view check", Key: "named-consumer-pool"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.Manager.Acquire(model.Request{Reason: "view check", TTL: time.Hour, Key: "named-consumer-lease", Pools: []model.PoolClaim{{Pool: change.Pool.ID, Quantity: 1}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	for _, browser := range []bool{false, true} {
		for _, op := range []string{"pool_list", "pool_inspect", "pool_consumers"} {
			ctx := context.WithValue(t.Context(), browserContextKey{}, browser)
			params, _ := json.Marshal(model.PoolTreeQuery{ID: change.Pool.ID})
			if op == "pool_list" {
				params = []byte(`{"all":true}`)
			}
			value, err := s.dispatch(ctx, model.WireRequest{Operation: op, Params: params})
			if err != nil {
				t.Fatal(op, err)
			}
			raw, err := json.Marshal(value)
			if err != nil || !bytes.Contains(raw, []byte(lease.ID)) || !bytes.Contains(raw, []byte(readablename.FromID(readablename.Lease, lease.ID))) {
				t.Fatal("missing canonical ID or label", op, err, string(raw))
			}
		}
	}
	after, err := s.Manager.Status()
	if err != nil || after.Cursor != before.Cursor {
		t.Fatal("name reads wrote audit/lifecycle state", err)
	}
	if !reflect.DeepEqual(before.Leases, after.Leases) {
		t.Fatal("name reads modified canonical leases")
	}
}
