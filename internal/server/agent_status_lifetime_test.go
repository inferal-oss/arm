package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/agentstatus"
	"getarm.org/internal/client"
	"getarm.org/internal/local"
	"getarm.org/internal/manager"
)

func TestAgentStatusDurableJobsOutliveOneShotConnections(t *testing.T) {
	s, service, c, entry := statusFixture(t)
	s.token = local.ID() + local.ID()
	residentFixtureIdentity(t, s)
	h := httptest.NewServer(http.HandlerFunc(s.ws))
	s.authority = strings.TrimPrefix(h.URL, "http://")
	t.Cleanup(h.Close)
	ep := local.Endpoint{URL: "ws" + strings.TrimPrefix(h.URL, "http") + "/v1/ws", Token: s.token}
	hold := make(chan struct{})
	entered := make(chan struct{}, 2)
	for _, id := range []string{"block-one", "block-two"} {
		if err := s.statusSchedule(t.Context(), service, id, func(ctx context.Context) {
			entered <- struct{}{}
			select {
			case <-hold:
			case <-ctx.Done():
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		<-entered
	}
	var a agentstatus.Attempt
	if err := client.CallEndpoint(t.Context(), ep, "agent_status_request", statusParams{ID: entry, Key: "durable", Contact: &c, Confirm: true}, &a); err != nil {
		t.Fatal(err)
	}
	residentEventually(t, func() bool { return residentCallerCount(s) == 0 })
	close(hold)
	residentEventually(t, func() bool { st, _ := service.store.Update(nil); return st.Attempt(a.ID).State == "awaiting_reply" })
	st, _ := service.store.Update(nil)
	original := st.Attempt(a.ID)
	body, _ := json.Marshal(agentstatus.Account{Version: 1, RequestID: a.ID, Current: []string{"Synthetic work"}})
	if _, err := s.Manager.AgentMessage(t.Context(), manager.AgentMessageRequest{ID: c.ID, Session: c.Session, Registration: c.Registration, Key: "status-reply:" + a.ID, ReplyTo: original.MessageID, Body: string(body)}); err != nil {
		t.Fatal(err)
	}
	statusCall(t, s, "collect", statusParams{ID: a.ID})
	t.Setenv("ARM_SYNTHETIC_STATUS_KEY", "fixture-key")
	config := agentstatus.DefaultConfig()
	config.Enabled = true
	config.CredentialEnv = "ARM_SYNTHETIC_STATUS_KEY"
	statusCall(t, s, "config", statusParams{Config: &config, Confirm: true})
	release := make(chan struct{})
	modelEntered := make(chan struct{})
	service.generate = func(ctx context.Context, c agentstatus.Config, key, prompt string) (string, *agentstatus.Usage, error) {
		close(modelEntered)
		select {
		case <-release:
			doc, e := agentstatus.Document("<p>Synthetic contribution</p>")
			return doc, &agentstatus.Usage{Billing: "synthetic only"}, e
		case <-ctx.Done():
			return "", nil, errors.New("durable model cancelled on ordinary closure")
		}
	}
	var result any
	if err := client.CallEndpoint(t.Context(), ep, "agent_status_generate", statusParams{ID: a.ID, Confirm: true}, &result); err != nil {
		t.Fatal(err)
	}
	<-modelEntered
	residentEventually(t, func() bool { return residentCallerCount(s) == 0 })
	close(release)
	residentEventually(t, func() bool { st, _ := service.store.Update(nil); return st.Attempt(a.ID).State == "ready" })
}

func TestAgentStatusBrowserExpiryAndPrincipalCancellation(t *testing.T) {
	s, service, _, _ := statusFixture(t)
	principal := [32]byte{1}
	other := [32]byte{2}
	browser := func(p [32]byte, expiry time.Time) context.Context {
		ctx := context.WithValue(t.Context(), browserContextKey{}, true)
		ctx = context.WithValue(ctx, browserPrincipalKey{}, p)
		return context.WithValue(ctx, browserExpiryKey{}, expiry)
	}
	done := make(chan string, 3)
	run := func(name string) func(context.Context) {
		return func(ctx context.Context) { <-ctx.Done(); done <- name }
	}
	ctx := browser(principal, time.Now().Add(time.Second))
	if err := s.statusSchedule(ctx, service, "first", run("first")); err != nil {
		t.Fatal(err)
	}
	if err := s.statusSchedule(browser(other, time.Now().Add(time.Minute)), service, "other", run("other")); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(statusParams{CancelOwned: true, Confirm: true})
	if _, err := s.agentStatusOperation(ctx, "agent_status_cancel", raw); err != nil {
		t.Fatal(err)
	}
	select {
	case name := <-done:
		if name != "first" {
			t.Fatal("cancel crossed principals")
		}
	case <-time.After(time.Second):
		t.Fatal("scoped cancellation missed own job")
	}
	if err := s.statusSchedule(browser(principal, time.Now().Add(50*time.Millisecond)), service, "expiry", run("expiry")); err != nil {
		t.Fatal(err)
	}
	select {
	case name := <-done:
		if name != "expiry" {
			t.Fatal("unexpected principal cancelled")
		}
	case <-time.After(time.Second):
		t.Fatal("credential expiry failed to cancel")
	}
	select {
	case <-done:
		t.Fatal("other principal cancelled")
	default:
	}
	service.cancelAll()
}
