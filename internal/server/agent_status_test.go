package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"getarm.org/internal/agents"
	"getarm.org/internal/agentstatus"
	"getarm.org/internal/local"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"github.com/google/uuid"
)

func statusFixture(t *testing.T) (*Server, *statusService, agents.Contact, string) {
	t.Helper()
	s := meshFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	s.lifecycleContext = ctx
	t.Cleanup(func() { cancel(); s.shutdownWG.Wait() })
	c, err := s.Manager.RegisterContact(agents.Contact{ID: "fixture-agent", Adapter: "generic", Delivery: "poll", Session: uuid.NewString(), CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	service, err := s.agentStatusService()
	if err != nil {
		t.Fatal(err)
	}
	host, _ := s.Manager.HostIdentity()
	id := (agentstatus.Identity{Host: host, Contact: c}).Key()
	statusCall(t, s, "list", statusParams{})
	return s, service, c, id
}
func statusCall(t *testing.T, s *Server, op string, q statusParams) any {
	t.Helper()
	raw, _ := json.Marshal(q)
	value, err := s.dispatch(t.Context(), model.WireRequest{Operation: "agent_status_" + op, Params: raw})
	if err != nil {
		t.Fatal(op, err)
	}
	return value
}
func statusAttempt(t *testing.T, value any) agentstatus.Attempt {
	t.Helper()
	raw, _ := json.Marshal(value)
	var a agentstatus.Attempt
	if json.Unmarshal(raw, &a) != nil {
		t.Fatal("decode synthetic status result")
	}
	return a
}

func TestAgentStatusSetupProjectionFailurePreservesInventory(t *testing.T) {
	s, _, _, _ := statusFixture(t)
	first := statusCall(t, s, "list", statusParams{}).(map[string]any)
	setup := first["project_setup"].(agentstatus.SetupProjection)
	if len(setup.Projects) != 0 || setup.Diagnostic != "" {
		t.Fatal(setup)
	}
	dir := filepath.Join(s.home, "status-onboarding-v1")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"version":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	second := statusCall(t, s, "list", statusParams{}).(map[string]any)
	setup = second["project_setup"].(agentstatus.SetupProjection)
	if setup.Diagnostic == "" || second["total"] != first["total"] || len(second["rows"].([]statusRow)) == 0 {
		t.Fatal("setup failure blocked inventory", setup)
	}
}
func statusContribution(t *testing.T, s *Server, service *statusService, c agents.Contact, entry, key string) agentstatus.Attempt {
	t.Helper()
	a := statusAttempt(t, statusCall(t, s, "request", statusParams{ID: entry, Key: key, Contact: &c, Confirm: true}))
	residentEventually(t, func() bool {
		st, e := service.store.Update(nil)
		return e == nil && st.Attempt(a.ID).State == "awaiting_reply"
	})
	st, _ := service.store.Update(nil)
	a = *st.Attempt(a.ID)
	body, _ := json.Marshal(agentstatus.Account{Version: 1, RequestID: a.ID, Current: []string{"Investigating; completion unknown"}, Questions: []string{"Which review gate remains?"}})
	_, err := s.Manager.AgentMessage(t.Context(), manager.AgentMessageRequest{ID: c.ID, Session: c.Session, Registration: c.Registration, Key: "status-reply:" + a.ID, ReplyTo: a.MessageID, Body: string(body)})
	if err != nil {
		t.Fatal(err)
	}
	a = statusAttempt(t, statusCall(t, s, "collect", statusParams{ID: a.ID}))
	if a.State != "contributed" {
		t.Fatal("owning account not collected", a.State)
	}
	return a
}
func TestAgentStatusParticipationAndSingleModelReservation(t *testing.T) {
	s, service, c, entry := statusFixture(t)
	a := statusContribution(t, s, service, c, entry, "one")
	var calls atomic.Int32
	service.generate = func(ctx context.Context, c agentstatus.Config, key, prompt string) (string, *agentstatus.Usage, error) {
		calls.Add(1)
		if !strings.Contains(prompt, "completion unknown") {
			t.Error("account uncertainty lost")
		}
		doc, err := agentstatus.Document("<h2>Current work</h2><p>Reported investigation; outcome unknown.</p>")
		return doc, &agentstatus.Usage{Billing: "synthetic usage only"}, err
	}
	t.Setenv("ARM_SYNTHETIC_STATUS_KEY", "fixture-key")
	config := agentstatus.DefaultConfig()
	config.Enabled = true
	config.CredentialEnv = "ARM_SYNTHETIC_STATUS_KEY"
	config.MaxDailyRequests = 1
	statusCall(t, s, "config", statusParams{Config: &config, Confirm: true})
	statusCall(t, s, "generate", statusParams{ID: a.ID, Confirm: true})
	residentEventually(t, func() bool { st, _ := service.store.Update(nil); return st.Attempt(a.ID).State == "ready" })
	statusCall(t, s, "generate", statusParams{ID: a.ID, Confirm: true})
	st, _ := service.store.Update(nil)
	if calls.Load() != 1 || st.DailyCalls != 1 || st.Report(entry) == nil {
		t.Fatal("duplicate generation or missing report")
	}
	replay := statusAttempt(t, statusCall(t, s, "request", statusParams{ID: entry, Key: "one", Contact: &c, Confirm: true}))
	if replay.ID != a.ID {
		t.Fatal("original request key changed")
	}
	next := statusContribution(t, s, service, c, entry, "two")
	raw, _ := json.Marshal(statusParams{ID: next.ID, Confirm: true})
	if _, err := s.dispatch(t.Context(), model.WireRequest{Operation: "agent_status_generate", Params: raw}); err == nil {
		t.Fatal("daily limit bypassed")
	}
	if calls.Load() != 1 {
		t.Fatal("refused generation dispatched")
	}
}
func TestAgentStatusCancelAndRegistrationReplacementFenceLateResults(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "replacement"}[replace], func(t *testing.T) {
			s, service, c, entry := statusFixture(t)
			a := statusContribution(t, s, service, c, entry, "one")
			t.Setenv("ARM_SYNTHETIC_STATUS_KEY", "fixture-key")
			config := agentstatus.DefaultConfig()
			config.Enabled = true
			config.CredentialEnv = "ARM_SYNTHETIC_STATUS_KEY"
			statusCall(t, s, "config", statusParams{Config: &config, Confirm: true})
			entered := make(chan struct{})
			service.generate = func(ctx context.Context, c agentstatus.Config, key, prompt string) (string, *agentstatus.Usage, error) {
				close(entered)
				<-ctx.Done()
				return "<p>Late synthetic result</p>", &agentstatus.Usage{Billing: "unknown synthetic outcome"}, errors.New("cancelled")
			}
			statusCall(t, s, "generate", statusParams{ID: a.ID, Confirm: true})
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("model worker did not start")
			}
			want := "cancelled"
			if replace {
				c.Session = uuid.NewString()
				if _, err := s.Manager.RegisterContact(c); err != nil {
					t.Fatal(err)
				}
				want = "superseded"
			} else {
				statusCall(t, s, "cancel", statusParams{ID: a.ID})
			}
			residentEventually(t, func() bool {
				st, _ := service.store.Update(nil)
				v := st.Attempt(a.ID)
				return v.State == want && v.Usage != nil
			})
			st, _ := service.store.Update(nil)
			if st.Attempt(a.ID).HTML != "" || st.DailyCalls != 1 {
				t.Fatal("late result published or reservation erased")
			}
		})
	}
}
func TestAgentStatusPendingIngressAndNodeScope(t *testing.T) {
	s := meshFixture(t)
	residentFixtureIdentity(t, s)
	s.token = local.ID() + local.ID()
	r := httptest.NewRequest("GET", "http://127.0.0.1:1234/v1/ws", nil)
	r.Header.Set("Authorization", "Bearer "+s.token)
	pending, err := s.authenticateLocalIngress(r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"list", "read", "request", "collect", "generate", "cancel", "config"} {
		t.Run(op, func(t *testing.T) {
			if _, err := s.prepareCall(pending, model.WireRequest{Operation: "agent_status_" + op, Params: json.RawMessage(`{}`)}); err == nil {
				t.Fatal("pending caller accepted")
			}
			ctx := context.WithValue(t.Context(), nodeSessionKey{}, nodeSession{Local: true})
			if _, err := s.agentStatusOperation(ctx, "agent_status_"+op, json.RawMessage(`{}`)); err == nil {
				t.Fatal("node scope accepted")
			}
		})
	}
	if _, err := os.Lstat(filepath.Join(s.home, "status-v1")); !os.IsNotExist(err) {
		t.Fatal("refused caller created status state")
	}
	if browserAllowedOperation("agent_status_config") {
		t.Fatal("browser can configure credential reference")
	}
}
func TestAgentStatusSchedulerBoundAndCallerCancellation(t *testing.T) {
	s, service, _, _ := statusFixture(t)
	caller, cancel := context.WithCancel(t.Context())
	var active, maxActive atomic.Int32
	entered := make(chan struct{}, 2)
	run := func(ctx context.Context) {
		n := active.Add(1)
		for {
			old := maxActive.Load()
			if old >= n || maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		active.Add(-1)
	}
	for i := 0; i < 2; i++ {
		if err := s.statusSchedule(caller, service, string(rune('a'+i)), run); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("workers not started")
		}
	}
	for i := 0; i < 16; i++ {
		if err := s.statusSchedule(caller, service, "queue-"+string(rune('a'+i)), run); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.statusSchedule(caller, service, "overflow", run); err == nil {
		t.Fatal("queue bound bypassed")
	}
	cancel()
	if active.Load() != 2 {
		t.Fatal("ordinary caller cancellation stopped accepted durable jobs")
	}
	service.cancelAll()
	residentEventually(t, func() bool { service.mu.Lock(); defer service.mu.Unlock(); return len(service.jobs) == 0 })
	if maxActive.Load() != 2 {
		t.Fatal("worker concurrency changed")
	}
}
