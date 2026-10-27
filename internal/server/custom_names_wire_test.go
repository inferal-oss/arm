package server_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/customnames"
	"getarm.org/internal/model"
)

func TestCustomNamesActualWirePreservesExactCleanupAfterCorruption(t *testing.T) {
	home := backend(t)
	capacity := int64(1)
	var pool model.PoolChange
	if err := client.Call(t.Context(), home, "pool_mutate", model.PoolMutation{Operation: "create", Scope: "global", Name: "name-wire-permits", Capacity: &capacity, Reason: "name wire fixture", Key: "name-wire-pool"}, &pool); err != nil {
		t.Fatal(err)
	}
	var lease model.Lease
	if err := client.Call(t.Context(), home, "acquire", model.Request{TTL: time.Hour, Reason: "name wire fixture", Key: "name-wire-lease", Pools: []model.PoolClaim{{Pool: pool.Pool.ID, Quantity: 1}}}, &lease); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"operation": "set", "kind": "lease", "authority_host": lease.Host, "id": lease.ID, "binding_lease": "", "name": "wire-build-cache", "expected_revision": 0}
	var doc customnames.Document
	if err := client.Call(t.Context(), home, "name_mutate", request, &doc); err != nil || doc.Revision != 1 {
		t.Fatal(doc, err)
	}
	var inspected struct {
		model.Lease
		DisplayName string `json:"display_name"`
	}
	if err := client.Call(t.Context(), home, "inspect", map[string]string{"id": lease.ID}, &inspected); err != nil || inspected.ID != lease.ID || inspected.DisplayName != "wire-build-cache" || inspected.Revision != lease.Revision || !inspected.Expires.Equal(*lease.Expires) {
		t.Fatal("view changed entitlement or lost name", inspected, err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`UPDATE metadata SET value=? WHERE key=?`, "{", customnames.MetadataKey); err != nil {
		t.Fatal(err)
	}
	var released model.Lease
	diagnostic, err := client.CallWithDiagnostics(t.Context(), home, "release", map[string]string{"id": lease.ID}, &released)
	if err != nil || released.ID != lease.ID || released.State != "terminal" || diagnostic == nil || diagnostic.Code != "name_metadata_invalid" {
		t.Fatal("naming diagnostic obscured successful exact-ID cleanup", released, diagnostic, err)
	}
	var raw string
	if err = db.QueryRow(`SELECT value FROM metadata WHERE key=?`, customnames.MetadataKey).Scan(&raw); err != nil || raw != "{" {
		t.Fatal("cleanup repaired or dropped naming evidence", raw, err)
	}
}
