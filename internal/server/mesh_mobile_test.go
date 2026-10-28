package server

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"getarm.org/internal/mesh"
)

// This uses real pinned TLS requests and exact original listener restart. The
// long-absence case retains old display timestamps across a real outage. Native
// clock controls separately test elapsed freshness; no host suspend is simulated.
func TestMobilePeerTransportRecoveryPreservesEnrollment(t *testing.T) {
	for _, absence := range []string{"short", "aged-observations"} {
		t.Run(absence, func(t *testing.T) {
			left, right, l, r := registryPair(t, mesh.Grant{}, mesh.Grant{Permissions: []string{"observe-host"}})
			joinMobileFixtures(t, left, right)
			target := r.Config().Identity.Host
			beforeLeft, beforeRight := l.Config(), r.Config()
			configPath := filepath.Join(right.home, "mesh", "config.json")
			beforeBytes, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			online, err := left.nodeInspect(t.Context(), target)
			if err != nil || online.Health != "reachable" || online.MetadataFreshness.Stale || online.ScopeFreshness.Stale {
				t.Fatal("initial fresh pinned observation", online, err)
			}
			if absence == "aged-observations" {
				left.nodeMu.Lock()
				cached := left.nodeCache[target]
				cached.MetadataAt = time.Now().Add(-30 * 24 * time.Hour)
				cached.ScopeAt = cached.MetadataAt
				left.nodeCache[target] = cached
				left.nodeMu.Unlock()
				list, err := left.nodeList()
				if err != nil || list.Nodes[1].MetadataFreshness.Received == nil || !list.Nodes[1].MetadataFreshness.Received.Equal(cached.MetadataAt) {
					t.Fatal("retained old display timestamps were rewritten", list, err)
				}
			}
			if err := right.closeStopMesh(); err != nil {
				t.Fatal("original transport did not join", err)
			}
			offline, err := left.nodeInspect(t.Context(), target)
			if err != nil || offline.Health != "unavailable" || !offline.MetadataFreshness.Stale || !offline.ScopeFreshness.Stale || offline.Metadata == nil {
				t.Fatal("offline observation lost or marked current", offline, err)
			}
			if !reflect.DeepEqual(l.Config(), beforeLeft) {
				t.Fatal("transport disappearance changed source trust")
			}
			if err = right.loadMesh(); err != nil {
				t.Fatal("same approved listener failed to restart", err)
			}
			right.meshMu.Lock()
			reopened := right.mesh.store.Config()
			right.meshMu.Unlock()
			if !reflect.DeepEqual(reopened, beforeRight) {
				t.Fatal("restart changed identity, grants or enrollment generation")
			}
			rejoined, err := left.nodeInspect(t.Context(), target)
			if err != nil || rejoined.Health != "reachable" || rejoined.MetadataFreshness.Stale || rejoined.ScopeFreshness.Stale || !rejoined.MetadataFreshness.Received.After(*offline.MetadataFreshness.Received) || !rejoined.ScopeFreshness.Received.After(*offline.ScopeFreshness.Received) {
				t.Fatal("fresh read did not restore same peer", rejoined, err)
			}
			afterBytes, err := os.ReadFile(configPath)
			if err != nil || !bytes.Equal(beforeBytes, afterBytes) {
				t.Fatal("recovery rewrote durable trust", err)
			}
			if err = l.Revoke(target, "explicit disconnect control"); err != nil {
				t.Fatal(err)
			}
			denied, err := left.nodeInspect(t.Context(), target)
			if err != nil || denied.Trust.State != "revoked" || denied.Health != "revoked" || denied.Metadata != nil || denied.Outbound != nil {
				t.Fatal("revocation inherited recovery observations", denied, err)
			}
		})
	}
}
