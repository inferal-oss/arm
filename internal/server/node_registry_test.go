package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

func registryPair(t *testing.T, leftGrant, rightGrant mesh.Grant) (*Server, *Server, *mesh.Store, *mesh.Store) {
	return registryPairWith(t, leftGrant, rightGrant, meshFixture)
}

func registryPairWith(t *testing.T, leftGrant, rightGrant mesh.Grant, makeFixture func(*testing.T) *Server) (*Server, *Server, *mesh.Store, *mesh.Store) {
	t.Helper()
	left, right := makeFixture(t), makeFixture(t)
	makeStore := func(s *Server, name string) *mesh.Store {
		n, err := s.nodeMetadata()
		if err != nil {
			t.Fatal(err)
		}
		address := meshAddress(t)
		store, err := mesh.Init(s.home, n.Host, name, address, address)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.startMesh(store); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.closeMesh)
		return store
	}
	l, r := makeStore(left, "left"), makeStore(right, "right")
	pair := strings.Repeat("a", 64)
	for _, row := range []struct {
		s *mesh.Store
		i mesh.Identity
		g mesh.Grant
	}{{l, r.Config().Identity, leftGrant}, {r, l.Config().Identity, rightGrant}} {
		if _, err := row.s.PairReviewed(row.i, row.g, pair, "registry fixture", false); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		s      *mesh.Store
		target string
	}{{l, r.Config().Identity.Host}, {r, l.Config().Identity.Host}} {
		if _, err := row.s.Call(t.Context(), row.target, mesh.Request{Operation: "hello", Params: json.RawMessage(`{"pair":"` + pair + `"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Activate(r.Config().Identity.Host, pair); err != nil {
		t.Fatal(err)
	}
	if err := r.Activate(l.Config().Identity.Host, pair); err != nil {
		t.Fatal(err)
	}
	return left, right, l, r
}
func TestNodeRegistryLocalWithoutMeshNoInitialization(t *testing.T) {
	s := meshFixture(t)
	for i := 0; i < 2; i++ {
		list, err := s.nodeList()
		if err != nil || list.MeshState != "unconfigured" || len(list.Nodes) != 1 || !list.Nodes[0].Local || list.Nodes[0].Metadata == nil || list.Nodes[0].Trust.Inbound != nil || list.Nodes[0].Outbound != nil {
			t.Fatal(list, err)
		}
		entry, err := s.nodeInspect(t.Context(), list.Source)
		if err != nil || !entry.Local || entry.Host != list.Source {
			t.Fatal(entry, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(s.home, "mesh")); !os.IsNotExist(err) {
		t.Fatal("read initialized mesh", err)
	}
	for _, host := range []string{"localhost", "left", strings.Repeat("f", 32), ""} {
		if _, err := s.nodeInspect(t.Context(), host); err == nil {
			t.Fatal("unknown alias fell back", host)
		}
	}
}
func TestNodeRegistryDirectionalCurrentScopeAndCachedNoFanout(t *testing.T) {
	inbound := mesh.Grant{Permissions: []string{"message"}, Contacts: []string{"only-peer-can-contact-here"}}
	outbound := mesh.Grant{Permissions: []string{"observe-host"}}
	left, right, l, r := registryPair(t, inbound, outbound)
	target := r.Config().Identity.Host
	before, err := left.nodeList()
	if err != nil || len(before.Nodes) != 2 || before.Nodes[1].Outbound != nil || before.Nodes[1].Metadata != nil || before.Nodes[1].Health != "unobserved" {
		t.Fatal(before, err)
	}
	entry, err := left.nodeInspect(t.Context(), target)
	if err != nil || entry.Local || entry.Health != "reachable" || entry.Metadata == nil || entry.Metadata.Host != target || entry.Outbound == nil || entry.Outbound.Source != l.Config().Identity.Host || entry.Outbound.Target != target || entry.Outbound.Grant.Permissions[0] != "observe-host" || entry.Trust.Inbound.Permissions[0] != "message" || entry.ScopeFreshness.Stale || entry.MetadataFreshness.Stale {
		t.Fatal(entry, err)
	}
	if entry.Trust.Account == nil || entry.Trust.Account.UID != r.Config().Identity.Owner || entry.Trust.Account.Username != r.Config().Identity.OwnerUsername || entry.Trust.Account.Source != "paired-identity-snapshot" {
		t.Fatal("missing reviewed run-as account snapshot", entry.Trust)
	}
	// A message-only active caller may inspect its own rights without acquiring
	// the distinct right to read node metadata, contacts or leases.
	reverse, err := right.nodeInspect(t.Context(), l.Config().Identity.Host)
	if err != nil || reverse.Outbound == nil || reverse.Metadata != nil || reverse.Health != "reachable" || reverse.Outbound.Grant.Permissions[0] != "message" {
		t.Fatal(reverse, err)
	}
	// Returned snapshots cannot mutate the next reader's cache.
	entry.Outbound.Grant.Permissions[0] = "run"
	entry.Metadata.Host = "poison"
	cached, err := left.nodeList()
	if err != nil || cached.Nodes[1].Metadata.Host != target || cached.Nodes[1].Outbound.Grant.Permissions[0] != "observe-host" {
		t.Fatal(cached, err)
	}
	// Stop the target listener. Listing must return immediately from registry /
	// cache without trying the unreachable peer or changing original timestamps.
	right.closeMesh()
	start := time.Now()
	offline, err := left.nodeList()
	if err != nil || time.Since(start) > time.Second || offline.Nodes[1].MetadataFreshness.Received == nil {
		t.Fatal(offline, err)
	}
	refreshed, err := left.nodeInspect(t.Context(), target)
	if err != nil || refreshed.Health != "unavailable" || refreshed.Metadata == nil || !refreshed.MetadataFreshness.Stale || !refreshed.ScopeFreshness.Stale {
		t.Fatal(refreshed, err)
	}
	// Revocation invalidates old scope and metadata rather than relabeling them
	// as current evidence for this now-different registry revision.
	if err = l.Revoke(target, "fixture revoke"); err != nil {
		t.Fatal(err)
	}
	after, err := left.nodeList()
	if err != nil || after.Nodes[1].Outbound != nil || after.Nodes[1].Metadata != nil || after.Nodes[1].Trust.State != "revoked" {
		t.Fatal(after, err)
	}
}
func TestNodeRegistrySlotsExactSelectorsAndAccessScopeOnlyOwnRights(t *testing.T) {
	left, _, l, r := registryPair(t, mesh.Grant{}, mesh.Grant{})
	target := r.Config().Identity.Host
	left.nodeMu.Lock()
	left.nodeSlots = make(chan struct{}, 2)
	left.nodeInFlight = map[string]bool{}
	left.nodeCache = map[string]cachedNodeRead{}
	left.nodeInFlight[target] = true
	left.nodeMu.Unlock()
	if _, err := left.nodeInspect(t.Context(), target); err == nil {
		t.Fatal("parallel same-node read not bounded")
	}
	left.nodeMu.Lock()
	delete(left.nodeInFlight, target)
	left.nodeSlots <- struct{}{}
	left.nodeSlots <- struct{}{}
	left.nodeMu.Unlock()
	if _, err := left.nodeInspect(t.Context(), target); err == nil {
		t.Fatal("global chosen-read concurrency unbounded")
	}
	left.nodeMu.Lock()
	<-left.nodeSlots
	<-left.nodeSlots
	left.nodeMu.Unlock()
	entry, err := left.nodeInspect(t.Context(), target)
	if err != nil || entry.Outbound == nil || len(entry.Outbound.Grant.Permissions) != 0 || entry.Metadata != nil {
		t.Fatal(entry, err)
	}
	peer, _ := l.Resolve(target)
	for _, state := range []string{"pending", "revoked"} {
		peer.State = state
		if _, err := left.mesh.dispatch(t.Context(), peer, mesh.Request{Operation: "access_scope"}); err == nil {
			t.Fatal("inactive peer got current scope")
		}
	}
	ctx := context.WithValue(t.Context(), browserContextKey{}, true)
	for _, op := range []string{"node_list", "node_inspect"} {
		params := json.RawMessage(`{}`)
		if op == "node_inspect" {
			params = json.RawMessage(`{"host":"` + target + `"}`)
		}
		if _, err := left.dispatch(ctx, model.WireRequest{Operation: op, Params: params}); err != nil {
			t.Fatal(op, err)
		}
	}
	for _, params := range []string{`{"host":"localhost"}`, `{"host":"` + target + `","operation":"run"}`, `{"host":"left"}`} {
		if _, err := left.dispatch(ctx, model.WireRequest{Operation: "node_inspect", Params: json.RawMessage(params)}); err == nil {
			t.Fatal("selector/forwarder accepted", params)
		}
	}
	if _, err := left.mesh.dispatch(t.Context(), peer, mesh.Request{Operation: "node_list"}); err == nil {
		t.Fatal("registry exposed over remote closed API")
	}
	if !reflect.DeepEqual(entry.Outbound.Grant.Permissions, []string{}) {
		t.Fatal(entry.Outbound)
	}
}

func TestNodeRegistryCachedInventoryAgesWithoutRewritingObservationTimes(t *testing.T) {
	total := int64(100)
	observed := time.Now().Add(-5 * time.Second).UTC()
	entry := model.NodeEntry{Health: "unobserved"}
	peer := mesh.Peer{Identity: mesh.Identity{Host: strings.Repeat("f", 32), Fingerprint: "pin"}, Pair: "pair", Revision: 2, State: "active"}
	s := &Server{nodeCache: map[string]cachedNodeRead{peer.Identity.Host: {Fingerprint: "pin", Pair: "pair", Revision: 2, MetadataAt: time.Now().Add(-5 * time.Second), ScopeAt: time.Now(), Scope: &model.NodeAccessScope{Grant: model.NodeGrant{Permissions: []string{"observe-host"}}}, Metadata: &model.NodeMetadata{ReportedAt: observed, Inventory: model.NodeInventory{Memory: model.NodeInventorySize{Total: &total, ObservedAt: &observed, Known: true, StaleAfter: 3 * time.Second}}}}}}
	now := leaseclock.Sample{Boot: "fixture-boot", Tick: int64(10 * time.Second), Wall: time.Now().UTC()}
	cached := s.nodeCache[peer.Identity.Host]
	cached.metadataClock, cached.scopeClock = now, now
	cached.metadataClock.Tick -= int64(5 * time.Second)
	s.nodeCache[peer.Identity.Host] = cached
	s.applyNodeCacheAt(&entry, peer, now)
	if entry.Metadata.Inventory.Memory.Known || !entry.Metadata.Inventory.Memory.Stale || entry.Metadata.Inventory.Memory.Age < 5*time.Second || !entry.Metadata.ReportedAt.Equal(observed) || !entry.Metadata.Inventory.Memory.ObservedAt.Equal(observed) || entry.ScopeFreshness.Stale {
		t.Fatal(entry)
	}
}

func TestNodeRegistryNoFanoutAndChosenReadDeadline(t *testing.T) {
	left, right := meshFixture(t), meshFixture(t)
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var accepted atomic.Int32
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			conn, err := raw.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			go func() { defer conn.Close(); <-done }()
		}
	}()
	localMetadata, _ := left.nodeMetadata()
	remoteMetadata, _ := right.nodeMetadata()
	addr := meshAddress(t)
	l, err := mesh.Init(left.home, localMetadata.Host, "deadline-source", addr, addr)
	if err != nil {
		t.Fatal(err)
	}
	r, err := mesh.Init(right.home, remoteMetadata.Host, "silent-destination", raw.Addr().String(), raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err = left.startMesh(l); err != nil {
		t.Fatal(err)
	}
	defer left.closeMesh()
	pair := strings.Repeat("b", 64)
	if _, err = l.PairReviewed(r.Config().Identity, mesh.Grant{}, pair, "private deadline fixture", false); err != nil {
		t.Fatal(err)
	}
	if err = l.Prove(remoteMetadata.Host, pair); err != nil {
		t.Fatal(err)
	}
	if err = l.Activate(remoteMetadata.Host, pair); err != nil {
		t.Fatal(err)
	}
	if _, err = left.nodeList(); err != nil || accepted.Load() != 0 {
		t.Fatal("listing contacted silent peer", err, accepted.Load())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	entry, err := left.nodeInspect(ctx, remoteMetadata.Host)
	if err != nil || entry.Health != "unavailable" || entry.Outbound != nil || entry.Metadata != nil || time.Since(start) > time.Second || accepted.Load() != 1 {
		t.Fatal(entry, err, time.Since(start), accepted.Load())
	}
}

func TestNodeRegistryReadBoundsCanonicalEscaping(t *testing.T) {
	var value model.NodeMetadata
	raw := []byte(`{"schema_version":1,"hostname":"` + strings.Repeat("<", 6000) + `"}`)
	if err := decodeNodeRead(raw, &value); err == nil {
		t.Fatal("raw32KiB bypassed canonical output bound")
	}
}

func TestNodeRegistryPinnedPeerReplyMustMatchSourceTargetPairAndMetadataHost(t *testing.T) {
	left, right, l, r := registryPair(t, mesh.Grant{}, mesh.Grant{Permissions: []string{"observe-host"}})
	target, source := r.Config().Identity.Host, l.Config().Identity.Host
	right.closeMesh()
	store, err := mesh.Open(right.home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	listener, err := net.Listen("tcp", store.Config().Listen)
	if err != nil {
		t.Fatal(err)
	}
	var mode atomic.Int32
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var rpc mesh.Request
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			http.Error(w, "bad fixture input", 400)
			return
		}
		peer, _ := store.Resolve(source)
		var result any
		if rpc.Operation == "access_scope" {
			scope := model.NodeAccessScope{Source: source, Target: target, Pair: peer.Pair, Revision: peer.Revision, Grant: nodeGrant(peer.Grant), Observed: time.Now().UTC()}
			switch mode.Load() {
			case 0:
				scope.Source = strings.Repeat("f", 32)
			case 1:
				scope.Target = strings.Repeat("f", 32)
			case 2:
				scope.Pair = strings.Repeat("c", 64)
			case 3:
				scope.Revision = 0
			}
			result = scope
		} else {
			result = model.NodeMetadata{Host: strings.Repeat("f", 32), SchemaVersion: 1, ReportedAt: time.Now().UTC()}
		}
		raw, _ := json.Marshal(result)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mesh.Response{Result: raw})
	})}
	defer server.Close()
	go func() { _ = server.Serve(tls.NewListener(listener, store.ServerTLS())) }()
	for index, name := range []string{"source", "target", "pair", "revision", "metadata-host"} {
		t.Run(name, func(t *testing.T) {
			mode.Store(int32(index))
			left.nodeMu.Lock()
			left.nodeCache = map[string]cachedNodeRead{}
			left.nodeMu.Unlock()
			entry, err := left.nodeInspect(t.Context(), target)
			if err != nil || entry.Health != "unavailable" || entry.Metadata != nil || entry.Error == nil {
				t.Fatal(entry, err)
			}
			if index < 4 && entry.Outbound != nil {
				t.Fatal("mismatched scope entered cache", entry.Outbound)
			}
		})
	}
}

func TestNodeRegistryDisabledLocalOwnerMismatchStaysObservableWithoutRepair(t *testing.T) {
	s := meshFixture(t)
	metadata, err := s.nodeMetadata()
	if err != nil {
		t.Fatal(err)
	}
	addr := meshAddress(t)
	store, err := mesh.InitPrepared(s.home, metadata.Host, "disabled-owner-fixture", addr, addr)
	if err != nil {
		t.Fatal(err)
	}
	config := store.Config()
	store.Close()
	config.Identity.Owner = "4294967295"
	path := filepath.Join(s.home, "mesh", "config.json")
	raw, _ := json.Marshal(config)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	registry, err := s.nodeList()
	if err != nil || registry.MeshState != "unavailable" || registry.Error == nil || len(registry.Nodes) != 1 || !registry.Nodes[0].Local || registry.Nodes[0].Metadata.Host != metadata.Host || registry.Nodes[0].Trust.Account == nil || registry.Nodes[0].Trust.Account.UID == "4294967295" {
		t.Fatal(registry, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(raw) {
		t.Fatal("read repaired/rewrote mismatched owner evidence", err)
	}
}
