package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/nodeavailability"
)

func TestNodeAvailabilityActivatedAuthorityAndPrivacy(t *testing.T) {
	s := meshFixture(t)
	update := nodeavailability.Update{Key: "owner-profile-1", Profile: "intermittent", Reason: "private owner declaration"}
	raw, _ := json.Marshal(update)
	// Authenticated local browser shares local owner authority; peer grants do
	// not make another node the owner of this declaration.
	ctx := context.WithValue(t.Context(), browserContextKey{}, true)
	value, err := s.dispatch(ctx, model.WireRequest{Operation: "node_availability_set_v1", Params: raw})
	if err != nil || value.(nodeavailability.Document).Profile != "intermittent" {
		t.Fatal("local owner setting unavailable", value, err)
	}
	service := &meshService{owner: s}
	peer := mesh.Peer{Identity: mesh.Identity{Host: strings.Repeat("b", 32)}, Grant: mesh.Grant{Permissions: []string{"observe-host"}}}
	value, err = service.dispatch(t.Context(), peer, mesh.Request{Operation: "node_availability_v1", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal("authorized profile read unavailable", err)
	}
	projected, _ := json.Marshal(value)
	if strings.Contains(string(projected), update.Key) || strings.Contains(string(projected), update.Reason) || strings.Contains(string(projected), `"owner":`) {
		t.Fatal("private profile evidence leaked", string(projected))
	}
	for _, op := range []string{"node_availability_set_v1", "node_availability_read_v1", "node_availability_inspect_v1"} {
		if _, err := service.dispatch(t.Context(), peer, mesh.Request{Operation: op, Params: raw}); err == nil {
			t.Fatal("peer obtained owner/gateway operation", op)
		}
	}
	peer.Grant = mesh.Grant{}
	if _, err := service.dispatch(t.Context(), peer, mesh.Request{Operation: "node_availability_v1", Params: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("profile read bypassed observe-host")
	}
	if _, err := s.dispatch(t.Context(), model.WireRequest{Operation: "node_availability_read_v1", Params: json.RawMessage(`{"host":"unknown-peer"}`)}); err == nil {
		t.Fatal("unknown peer fell back to local declaration")
	}
}

func TestNodeAvailabilityActivatedPinnedChosenRead(t *testing.T) {
	left, right, _, r := registryPair(t, mesh.Grant{}, mesh.Grant{Permissions: []string{"observe-host"}})
	joinMobileFixtures(t, left, right)
	_, err := right.Manager.SetNodeAvailability(nodeavailability.Update{Key: "mobile-owner", Profile: "intermittent", Reason: "private device declaration"})
	if err != nil {
		t.Fatal(err)
	}
	host := r.Config().Identity.Host
	raw, _ := json.Marshal(map[string]string{"host": host})
	value, err := left.dispatch(t.Context(), model.WireRequest{Operation: "node_availability_read_v1", Params: raw})
	if err != nil {
		t.Fatal("chosen direct read failed", err)
	}
	report := value.(nodeavailability.Report)
	if report.Host != host || report.OwnerDeclared.Profile != "intermittent" {
		t.Fatal("chosen declaration rebound", report)
	}
}

func TestMeshAvailabilityActivatedStopWaitsForStartupOwner(t *testing.T) {
	s := &Server{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observer := &meshAvailability{cancel: cancel, done: make(chan struct{})}
	returned := make(chan struct{})
	entered := make(chan struct{})
	s.meshAdminMu.Lock()
	go func() { close(entered); s.closeStartupMeshAvailability(); close(returned) }()
	<-entered
	select {
	case <-returned:
		s.meshAdminMu.Unlock()
		t.Fatal("startup observer closure accepted before retention")
	case <-time.After(30 * time.Millisecond):
	}
	s.meshAvailability.Store(observer)
	s.meshAdminMu.Unlock()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		close(observer.done)
		<-returned
		t.Fatal("original startup observer not cancelled")
	}
	select {
	case <-returned:
		close(observer.done)
		t.Fatal("startup observer original join skipped")
	default:
	}
	// The join releases admin exclusion before waiting for the actual worker.
	if !s.meshAdminMu.TryLock() {
		close(observer.done)
		<-returned
		t.Fatal("observer joined under administration lock")
	}
	s.meshAdminMu.Unlock()
	close(observer.done)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("original startup observer not joined")
	}
}

// This is deliberately a product-asset assertion, not just a source-module
// import test. The authored mount must be rebuilt into the served embed before
// the activated composition can pass its interface gate.
func TestNodeAvailabilityActivatedEmbeddedPanel(t *testing.T) {
	s := &Server{authority: "127.0.0.1:1234"}
	mux := http.NewServeMux()
	s.registerBrowser(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://"+s.authority+"/assets/arm.mjs", nil))
	if response.Code != http.StatusOK || response.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(response.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatal("availability product asset unavailable or unguarded", response.Code)
	}
	for _, required := range []string{"node_availability_read_v1", "node_availability_inspect_v1", "node_availability_set_v1", "Owner availability is advisory", "Mobile / intermittent", "Read current declaration", "Save owner availability"} {
		if !strings.Contains(response.Body.String(), required) {
			t.Fatal("served bundle does not contain activated availability panel", required)
		}
	}
}
