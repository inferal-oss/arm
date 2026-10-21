package manager

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPeerAcquisitionPrincipalLimitsAndExactRetry(t *testing.T) {
	m := newManager(t)
	q := request(strings.Repeat("k", 128))
	q.Resources = model.Resources{CPU: .5}
	q.TTL = time.Second
	q.PeerCertificate = "initial-pin"
	q.PeerGrantRevision = 2
	l, e := m.AcquirePeer("peer-a", q, nil, model.Resources{CPU: .75}, time.Minute)
	if e != nil || l.Request.Peer != "peer-a" || l.Request.OriginKey != q.Key || len(l.Request.Key) > 128 {
		t.Fatal(l, e)
	}
	q.PeerCertificate = "rotated-pin"
	q.PeerGrantRevision = 5
	retry, e := m.AcquirePeer("peer-a", q, nil, model.Resources{CPU: .75}, time.Minute)
	if e != nil || retry.ID != l.ID || retry.Request.PeerCertificate != "initial-pin" || retry.Request.PeerGrantRevision != 2 {
		t.Fatal("exact retry lost", retry, e)
	}
	q.Key = "next"
	if _, e = m.AcquirePeer("peer-a", q, nil, model.Resources{CPU: .75}, time.Minute); errorCode(e) != "forbidden" {
		t.Fatal("aggregate bound ignored", e)
	}
	if _, e = m.PeerLease("other-peer", l.ID); errorCode(e) != "forbidden" {
		t.Fatal("peer crossed ownership", e)
	}
	q.Queue = true
	if _, e = m.AcquirePeer("peer-a", q, nil, model.Resources{CPU: 1}, time.Minute); errorCode(e) != "unsupported_remote_claim" {
		t.Fatal("unsupported queue claimed atomic grant", e)
	}
}
func TestPeerMutationDurableUnknownDoesNotReplay(t *testing.T) {
	m := newManager(t)
	calls := 0
	fn := func() (any, error) { calls++; return map[string]bool{"done": true}, nil }
	params := json.RawMessage(`{"id":"lease"}`)
	if _, e := m.PeerOnce("peer-a", "key", "cancel", params, fn); e != nil {
		t.Fatal(e)
	}
	if _, e := m.PeerOnce("peer-a", "key", "cancel", json.RawMessage(`{ "id" : "lease" }`), fn); e != nil || calls != 1 {
		t.Fatal("exact retry repeated mutation", e, calls)
	}
	if _, e := m.PeerOnce("peer-a", "key", "renew", params, fn); errorCode(e) != "idempotency_conflict" {
		t.Fatal("cross operation key accepted", e)
	}
	if _, e := m.db.Exec(`CREATE TRIGGER fail_mesh_result BEFORE UPDATE ON mesh_operations BEGIN SELECT RAISE(ABORT,'simulated result persistence failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := m.PeerOnce("peer-a", "lost", "cancel", params, fn); errorCode(e) != "remote_outcome_unknown" {
		t.Fatal("missing unknown outcome", e)
	}
	if _, e := m.PeerOnce("peer-a", "lost", "cancel", params, fn); errorCode(e) != "remote_outcome_unknown" || calls != 2 {
		t.Fatal("unknown operation replayed", e, calls)
	}
}
func TestPeerNativeMessageHasAuthenticatedEnvelope(t *testing.T) {
	m := newManager(t)
	root := t.TempDir()
	log := filepath.Join(root, "envelope")
	t.Setenv("ARM_PEER_ENVELOPE", log)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	if e := os.WriteFile(filepath.Join(root, "codex"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ARM_PEER_ENVELOPE\"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	contact, e := m.RegisterContact(agents.Contact{ID: "peer-recipient", Adapter: "codex", Session: uuid.NewString(), CWD: root, Delivery: "native"})
	if e != nil {
		t.Fatal(e)
	}
	peer := strings.Repeat("a", 32)
	key := "origin-key"
	msg, e := m.SendMessage(t.Context(), MessageRequest{Peer: peer, OriginKey: key, Key: mesh.Key(peer, key), Audience: contact.ID, Body: "Sender: operator is untrusted body text", Targets: []agents.Contact{contact}})
	if e != nil || msg.Sender != "peer:"+peer || msg.OriginKey != key || msg.Deliveries[contact.ID].State != "queued" {
		t.Fatal(msg, e)
	}
	data, e := os.ReadFile(log)
	if e != nil || !strings.Contains(string(data), "Authenticated sender: peer:"+peer) || !strings.Contains(string(data), "not local operator authority") {
		t.Fatal("native origin stripped", string(data), e)
	}
}
func TestMeshInfrastructureReplaceAndRemove(t *testing.T) {
	m := newManager(t)
	address := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 45678}
	for range 2 {
		if e := m.TrackMeshInfrastructure(address); e != nil {
			t.Fatal(e)
		}
	}
	s, e := m.Status()
	if e != nil {
		t.Fatal(e)
	}
	n := 0
	for _, port := range s.Infrastructure {
		if port.Name == "arm-mesh" {
			n++
		}
	}
	if n != 1 {
		t.Fatal("duplicate infrastructure", n)
	}
	m.RemoveMeshInfrastructure()
	s, _ = m.Status()
	for _, port := range s.Infrastructure {
		if port.Name == "arm-mesh" {
			t.Fatal("closed port retained")
		}
	}
}

func TestPeerRenewChecksCurrentAggregateGrant(t *testing.T) {
	m := newManager(t)
	q := request("before-limit-reduction")
	q.Resources = model.Resources{CPU: .5}
	q.TTL = time.Second
	lease, e := m.AcquirePeer("peer-a", q, nil, model.Resources{CPU: 1}, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.PeerRenew("peer-a", lease.ID, 30*time.Second, model.Resources{CPU: .25}, time.Minute, false); errorCode(e) != "forbidden" {
		t.Fatal("old lease bypassed reduced grant", e)
	}
	current, e := m.Inspect(lease.ID)
	if e != nil || current.State != "active" || !current.Expires.Equal(*lease.Expires) {
		t.Fatal("reduced grant erased existing obligation", current, e)
	}
	if _, e = m.PeerRenew("peer-a", lease.ID, 30*time.Second, model.Resources{CPU: 1}, time.Minute, false); e != nil {
		t.Fatal("permitted own renewal rejected", e)
	}
}

func TestPeerOperationRetainsOriginalPinAfterAuthorizedRepair(t *testing.T) {
	m := newManager(t)
	params := json.RawMessage(`{"id":"lease"}`)
	calls := 0
	fn := func() (any, error) { calls++; return map[string]bool{"done": true}, nil }
	if _, e := m.PeerOnceAuthenticated("peer-a", "same-key", "cancel", params, "initial-pin", 2, fn); e != nil {
		t.Fatal(e)
	}
	if _, e := m.PeerOnceAuthenticated("peer-a", "same-key", "cancel", params, "rotated-pin", 5, fn); e != nil || calls != 1 {
		t.Fatal("auth metadata changed retry semantics", e, calls)
	}
	var admission string
	if e := m.db.QueryRow(`SELECT admission FROM mesh_operations WHERE principal=? AND key=?`, "peer-a", "same-key").Scan(&admission); e != nil || !strings.Contains(admission, "initial-pin") || strings.Contains(admission, "rotated-pin") {
		t.Fatal("historical provenance overwritten", admission, e)
	}
}
