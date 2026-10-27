package server

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

func TestCustomNameRemoteReferencesStaySourceLocalAndRespectCurrentGrant(t *testing.T) {
	for _, tt := range []struct {
		name         string
		permissions  []string
		leaseVisible bool
	}{
		{"host-only", []string{"observe-host"}, false},
		{"own-hidden-local-lease", []string{"observe-host", "observe-own"}, false},
		{"all-visible", []string{"observe-host", "observe-all"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			left, right, source, target := registryPair(t, mesh.Grant{Permissions: []string{"message"}}, mesh.Grant{Permissions: tt.permissions})
			targetHost := target.Config().Identity.Host
			capacity := int64(1)
			pool, err := right.Manager.MutatePool(model.PoolMutation{Operation: "create", Scope: "global", Name: "private-destination-pool", Capacity: &capacity, Reason: "cosmetic scope fixture", Key: "name-remote-pool"})
			if err != nil {
				t.Fatal(err)
			}
			lease, err := right.Manager.Acquire(model.Request{TTL: time.Hour, Reason: "cosmetic scope fixture", Key: "name-remote-lease", Pools: []model.PoolClaim{{Pool: pool.Pool.ID, Quantity: 1}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			mutate := func(kind, id, name string, revision int64) (any, error) {
				raw, _ := json.Marshal(nameMutation{Operation: "set", Kind: kind, AuthorityHost: targetHost, ID: id, Name: name, ExpectedRevision: revision})
				return left.nameOperation(t.Context(), "name_mutate", raw)
			}
			if _, err = mutate("pool", pool.Pool.ID, "source-permits", 0); err != nil {
				t.Fatal("visible summary pool required private definition or claim authority", err)
			}
			_, err = mutate("lease", lease.ID, "source-build", 1)
			if (err == nil) != tt.leaseVisible {
				t.Fatal("naming widened lease visibility", tt.leaseVisible, err)
			}
			destinationDoc, err := right.Manager.CustomNames(t.Context())
			if err != nil || destinationDoc.Revision != 0 {
				t.Fatal("source label wrote destination metadata", destinationDoc, err)
			}
			raw, _ := json.Marshal(struct {
				Host    string
				Request mesh.Request
			}{targetHost, mesh.Request{Operation: "status", Params: json.RawMessage(`{}`)}})
			view, err := left.meshOperation(t.Context(), "mesh_dispatch", raw)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(encoded, []byte("source-permits")) || bytes.Contains(encoded, []byte("private-destination-pool")) || bytes.Contains(encoded, []byte("source-build")) != tt.leaseVisible {
				t.Fatal("source overlay changed redaction or omitted visible label", string(encoded))
			}
			params, _ := json.Marshal(map[string]string{"id": lease.ID})
			raw, _ = json.Marshal(struct {
				Host    string
				Request mesh.Request
			}{targetHost, mesh.Request{Operation: "release", Key: "name-does-not-grant-release", Params: params}})
			if _, err = left.meshOperation(t.Context(), "mesh_dispatch", raw); err == nil {
				t.Fatal("cosmetic alias granted lease mutation")
			}
			before, err := left.Manager.CustomNames(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err = target.Revoke(source.Config().Identity.Host, "naming grant removed"); err != nil {
				t.Fatal(err)
			}
			if _, err = mutate("pool", pool.Pool.ID, "after-revocation", before.Revision); err == nil {
				t.Fatal("stale visible snapshot authorized a new mapping")
			}
			after, err := left.Manager.CustomNames(t.Context())
			if err != nil || after.Revision != before.Revision {
				t.Fatal("revoked read changed source map", after, err)
			}
		})
	}
}
