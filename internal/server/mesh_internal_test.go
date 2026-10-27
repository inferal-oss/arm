package server

import (
	"context"
	"encoding/json"
	"getarm.org/internal/agents"
	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"github.com/google/uuid"
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

func TestMeshInspectReadScopeAndExactGenerationDoNotExpandOtherOperations(t *testing.T) {
	s := meshFixture(t)
	s.generation = strings.Repeat("a", 32)
	l, e := s.Manager.Acquire(model.Request{Resources: model.Resources{CPU: .25}, Reason: "private local purpose", TTL: time.Minute, Key: "inspect-local"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	service := &meshService{owner: s}
	peer := mesh.Peer{Identity: mesh.Identity{Host: strings.Repeat("b", 32)}, Grant: mesh.Grant{Permissions: []string{"observe-all"}}}
	good, _ := json.Marshal(map[string]string{"id": l.ID, "backend_generation": s.generation})
	v, e := service.dispatch(t.Context(), peer, mesh.Request{Operation: "inspect", Params: good})
	if e != nil || v.(model.Lease).Request.Reason != l.Request.Reason {
		t.Fatal("observe-all read refused", v, e)
	}
	peer.Grant.Permissions = []string{"observe-own"}
	if _, e = service.dispatch(t.Context(), peer, mesh.Request{Operation: "inspect", Params: good}); e == nil {
		t.Fatal("same UID read foreign-origin lease")
	}
	own, e := s.Manager.AcquirePeer(peer.Identity.Host, model.Request{Resources: model.Resources{CPU: .25}, Reason: "own origin purpose", TTL: time.Minute, Key: "own-inspect"}, nil, model.Resources{CPU: 1}, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	ownRead, _ := json.Marshal(map[string]string{"id": own.ID, "backend_generation": s.generation})
	if value, e := service.dispatch(t.Context(), peer, mesh.Request{Operation: "inspect", Params: ownRead}); e != nil || value.(model.Lease).ID != own.ID {
		t.Fatal("observe-own lost exact origin read", value, e)
	}
	peer.Pair = strings.Repeat("d", 64)
	peer.Revision = 1
	if _, e = service.dispatch(t.Context(), peer, mesh.Request{Operation: "inspect", Params: ownRead, Expected: &mesh.ExpectedEnrollment{Pair: peer.Pair, Revision: 2}}); e == nil {
		t.Fatal("stale Expected enrollment read lease data")
	}
	peer.Grant.Permissions = []string{"node-stop"}
	if _, e = service.dispatch(t.Context(), peer, mesh.Request{Operation: "inspect", Params: good}); e == nil {
		t.Fatal("node-stop became a lease read grant")
	}
	peer.Grant.Permissions = []string{"observe-all", "observe-own", "lease-own", "run"}
	for _, gen := range []string{strings.Repeat("c", 32), "", strings.Repeat("A", 32), "short"} {
		raw, _ := json.Marshal(map[string]string{"id": l.ID, "backend_generation": gen})
		if _, e = service.dispatch(t.Context(), peer, mesh.Request{Operation: "inspect", Params: raw}); e == nil {
			t.Fatal("invalid/stale generation accepted", gen)
		}
	}
	for _, op := range []string{"output", "release", "cancel", "renew"} {
		for _, generation := range []string{`""`, `null`, `"` + s.generation + `"`} {
			raw := json.RawMessage(`{"id":"` + l.ID + `","backend_generation":` + generation + `}`)
			if _, e = service.dispatch(t.Context(), peer, mesh.Request{Operation: op, Params: raw, Key: "not-sent"}); e == nil {
				t.Fatal("read generation field accepted by other operation", op, generation)
			}
		}
	}
	if _, e = service.dispatch(t.Context(), peer, mesh.Request{Operation: "inspect", Params: json.RawMessage(`{"id":"` + l.ID + `","backend_generation":null}`)}); e == nil {
		t.Fatal("null generation accepted")
	}
	after, e := s.Manager.Inspect(l.ID)
	if e != nil || after.Revision != l.Revision || after.State != l.State {
		t.Fatal("read or rejected mutation changed lease", after, e)
	}
	if !browserAllowedOperation("mesh_operator_stop_lease") || browserAllowedOperation("mesh_dispatch") {
		t.Fatal("browser lease-read wrapper missing or generic dispatch exposed")
	}
}
