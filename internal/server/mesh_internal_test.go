package server

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func meshFixture(t *testing.T) *Server {
	t.Helper()
	home := filepath.Join(t.TempDir(), "state")
	m, e := manager.Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Close() })
	return &Server{Manager: m, home: home, lifecycleContext: context.Background()}
}
func meshAddress(t *testing.T) string {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	return listener.Addr().String()
}
func TestMeshHostSummaryNeverLeaksCustomContactBindings(t *testing.T) {
	s := meshFixture(t)
	contact, e := s.Manager.RegisterContact(agents.Contact{ID: "private-contact", Adapter: "generic", Delivery: "poll", Session: uuid.NewString(), CWD: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	zero := int64(0)
	pool, e := s.Manager.MutatePool(model.PoolMutation{Operation: "create", Name: "private pool", Scope: "host", Capacity: &zero, Agent: contact.ID, Description: "private-description-sentinel", Project: "private-project-sentinel", Reason: "private pool", Key: "pool"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Manager.Acquire(model.Request{Pools: []model.PoolClaim{{Pool: pool.Pool.ID, Quantity: 1}}, Queue: true, Reason: "private-queued-reason", TTL: time.Minute, Key: "queued"}, nil); e != nil {
		t.Fatal(e)
	}
	service := &meshService{owner: s}
	for _, all := range []bool{false, true} {
		g := mesh.Grant{Permissions: []string{"observe-host"}}
		if all {
			g.Permissions = append(g.Permissions, "observe-all")
		}
		value, e := service.dispatch(t.Context(), mesh.Peer{Identity: mesh.Identity{Host: strings.Repeat("a", 32)}, Grant: g}, mesh.Request{Operation: "status"})
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(value)
		for _, secret := range []string{contact.Session, contact.Registration, contact.CWD, "private-description-sentinel", "private-project-sentinel"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("summary leaked contact/filesystem metadata", all, secret)
			}
		}
		if !all && strings.Contains(string(raw), "private-queued-reason") {
			t.Fatal("read-only summary leaked reason")
		}
		if !strings.Contains(string(raw), "observation_at") || !strings.Contains(string(raw), "stale") {
			t.Fatal("summary omitted memory freshness")
		}
	}
}
func TestMeshMessageLookupUsesCurrentAudienceGrant(t *testing.T) {
	s := meshFixture(t)
	peerID := strings.Repeat("a", 32)
	key := "operator-message"
	if _, e := s.Manager.SendMessage(t.Context(), manager.MessageRequest{Peer: peerID, OriginKey: key, Key: mesh.Key(peerID, key), Audience: "operator", Body: "original", Targets: []agents.Contact{}}); e != nil {
		t.Fatal(e)
	}
	service := &meshService{owner: s}
	peer := mesh.Peer{Identity: mesh.Identity{Host: peerID}, Grant: mesh.Grant{Permissions: []string{"message"}, OperatorInbox: true}}
	params, _ := json.Marshal(map[string]string{"idempotency_key": key})
	if _, e := service.dispatch(t.Context(), peer, mesh.Request{Operation: "message_lookup", Params: params}); e != nil {
		t.Fatal(e)
	}
	peer.Grant.OperatorInbox = false
	if _, e := service.dispatch(t.Context(), peer, mesh.Request{Operation: "message_lookup", Params: params}); e == nil {
		t.Fatal("removed audience still readable")
	}
	for _, operation := range []string{"browser_ticket", "mesh_dispatch", "daemon_stop", "message_send", "pool_mutate", "subscribe", "agents"} {
		if _, e := service.dispatch(t.Context(), peer, mesh.Request{Operation: operation, Params: json.RawMessage(`{}`)}); e == nil {
			t.Fatal("full local RPC exposed", operation)
		}
	}
}
func TestMeshRejectsCopiedIdentityAndRetainsOneInfrastructureEntry(t *testing.T) {
	s := meshFixture(t)
	status, e := s.Manager.Status()
	if e != nil {
		t.Fatal(e)
	}
	address := meshAddress(t)
	wrong, e := mesh.Init(filepath.Join(t.TempDir(), "wrong"), strings.Repeat("a", 32), "wrong", address, address)
	if e != nil {
		t.Fatal(e)
	}
	defer wrong.Close()
	if e = s.startMesh(wrong); e == nil {
		t.Fatal("copied host identity accepted")
	}
	store, e := mesh.Init(s.home, status.Host, "own", address, address)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.startMesh(store); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		s.closeMesh()
		store, e = mesh.Open(s.home)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.startMesh(store); e != nil {
			t.Fatal(e)
		}
	}
	defer s.closeMesh()
	status, e = s.Manager.Status()
	if e != nil {
		t.Fatal(e)
	}
	n := 0
	for _, binding := range status.Infrastructure {
		if binding.Name == "arm-mesh" {
			n++
		}
	}
	if n != 1 {
		t.Fatal("listener infrastructure duplicated", n)
	}
	s.closeMesh()
	status, _ = s.Manager.Status()
	for _, binding := range status.Infrastructure {
		if binding.Name == "arm-mesh" {
			t.Fatal("closed listener still protected")
		}
	}
	s.lifecycleClosing = true
	store, e = mesh.Open(s.home)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	if e = s.startMesh(store); e == nil {
		t.Fatal("mesh started while closing")
	}
}

func TestMeshMessageLookupDoesNotRetainRemovedContactScope(t *testing.T) {
	s := meshFixture(t)
	contact, e := s.Manager.RegisterContact(agents.Contact{ID: "contact", Adapter: "generic", Delivery: "poll", Session: uuid.NewString(), CWD: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	peerID := strings.Repeat("a", 32)
	key := "contact-message"
	msg, e := s.Manager.SendMessage(t.Context(), manager.MessageRequest{Peer: peerID, OriginKey: key, Key: mesh.Key(peerID, key), Audience: contact.ID, Body: "original", Targets: []agents.Contact{contact}})
	if e != nil {
		t.Fatal(e)
	}
	service := &meshService{owner: s}
	peer := mesh.Peer{Identity: mesh.Identity{Host: peerID}, Grant: mesh.Grant{Permissions: []string{"message"}, Contacts: []string{contact.ID}}}
	params, _ := json.Marshal(map[string]string{"idempotency_key": key})
	value, e := service.dispatch(t.Context(), peer, mesh.Request{Operation: "message_lookup", Params: params})
	if e != nil || value.(manager.MessageLookup).Message.ID != msg.ID {
		t.Fatal("authorized lookup failed", value, e)
	}
	peer.Grant.Contacts = nil
	if _, e = service.dispatch(t.Context(), peer, mesh.Request{Operation: "message_lookup", Params: params}); e == nil {
		t.Fatal("removed contact's future reply remained accessible")
	}
	peer.Grant.OperatorInbox = true
	if _, e = service.dispatch(t.Context(), peer, mesh.Request{Operation: "message", Key: "spoof", Params: json.RawMessage(`{"origin_peer":"pretend-operator","audience":"operator","body":"body","targets":[]}`)}); e == nil {
		t.Fatal("caller supplied authenticated message origin accepted")
	}
}
func TestNativeClientCannotAssertPeerAdmissionIdentity(t *testing.T) {
	s := meshFixture(t)
	request := model.Request{Peer: "spoofed-peer", PeerCertificate: "spoofed-pin", PeerGrantRevision: 99, OriginKey: "spoofed-origin", Key: "native", Reason: "native caller metadata", Resources: model.Resources{CPU: .25}, TTL: time.Second}
	params, _ := json.Marshal(request)
	value, e := s.dispatch(t.Context(), model.WireRequest{Operation: "acquire", Params: params})
	if e != nil {
		t.Fatal(e)
	}
	lease := value.(model.Lease)
	if lease.Request.Peer != "" || lease.Request.PeerCertificate != "" || lease.Request.PeerGrantRevision != 0 || lease.Request.OriginKey != "" {
		t.Fatal("native attribution impersonated mesh authentication", lease)
	}
}
