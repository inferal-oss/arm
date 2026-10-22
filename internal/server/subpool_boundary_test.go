package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
)

func TestSubpoolReadNativeOnlyAndMeshProjection(t *testing.T) {
	s := meshFixture(t)
	service := &meshService{owner: s}
	peer := mesh.Peer{Identity: mesh.Identity{Host: strings.Repeat("a", 32)}, Grant: mesh.Grant{Permissions: []string{"observe-host", "observe-all", "lease-own"}}}
	for _, op := range []string{"pool_children", "pool_consumers"} {
		raw := json.RawMessage(`{}`)
		value, err := s.dispatch(context.WithValue(t.Context(), browserContextKey{}, true), model.WireRequest{Operation: op, Params: raw})
		var api *model.Error
		if value != nil || !errors.As(err, &api) || api.Code != "forbidden" {
			t.Fatal(op, value, err)
		}
		value, err = service.dispatch(t.Context(), peer, mesh.Request{Operation: op, Params: raw})
		if value != nil || !errors.As(err, &api) || api.Code != "forbidden" {
			t.Fatal(op, value, err)
		}
	}
	lease := model.Lease{ID: "fixture", PoolBinding: &model.PoolClaimBinding{Authority: "private-pool-ancestor"}, PoolError: &model.Error{Code: "invalid_accounting", Message: "private-pool-detail"}}
	for _, value := range []any{lease, []model.Lease{lease}, json.RawMessage(`{"id":"fixture","pool_binding":{"authority":"private-pool-ancestor"},"pool_error":{"code":"invalid_accounting","message":"private-pool-detail"}}`)} {
		projected, err := meshWithoutPolicy(value)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(projected)
		for _, key := range []string{"pool_binding", "pool_error", "private-pool"} {
			if strings.Contains(string(raw), key) {
				t.Fatal("new local pool evidence leaked", string(raw))
			}
		}
	}
}
