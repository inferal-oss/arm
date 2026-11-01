package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"getarm.org/internal/agents"
	"getarm.org/internal/agentstatus"
	"getarm.org/internal/local"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"getarm.org/internal/rollout"
)

type statusJob struct {
	id     string
	ctx    context.Context
	cancel context.CancelFunc
	run    func(context.Context)
}
type statusControl struct {
	cancel    context.CancelFunc
	browser   bool
	principal [32]byte
}
type statusService struct {
	mu       sync.Mutex
	store    *agentstatus.Store
	queue    chan statusJob
	jobs     map[string]statusControl
	started  bool
	provider *agentstatus.Provider
	generate func(context.Context, agentstatus.Config, string, string) (string, *agentstatus.Usage, error)
}
type statusParams struct {
	ID          string              `json:"id"`
	Offset      int                 `json:"offset"`
	Key         string              `json:"key"`
	Contact     *agents.Contact     `json:"contact,omitempty"`
	Config      *agentstatus.Config `json:"config,omitempty"`
	Confirm     bool                `json:"confirm"`
	CancelOwned bool                `json:"cancel_owned,omitempty"`
}
type statusRow struct {
	agentstatus.Entry
	Current  bool                 `json:"current"`
	Latest   *agentstatus.Attempt `json:"latest,omitempty"`
	ReportID string               `json:"report_id,omitempty"`
}

func (s *Server) agentStatusService() (*statusService, error) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if s.status != nil {
		return s.status, nil
	}
	store, err := agentstatus.OpenStore(s.home)
	if err != nil {
		return nil, model.Err("status_unavailable", err.Error())
	}
	s.status = &statusService{store: store, queue: make(chan statusJob, 16), jobs: map[string]statusControl{}, provider: agentstatus.NewProvider()}
	s.status.generate = s.status.provider.Generate
	return s.status, nil
}
func (s *Server) statusSchedule(ctx context.Context, service *statusService, id string, run func(context.Context)) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.lifecycleClosing {
		return model.Err("backend_stopping", "status service is stopping")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if _, exists := service.jobs[id]; exists {
		return nil
	}
	if len(service.jobs) >= 18 {
		return model.Err("status_busy", "two workers and sixteen queued status jobs are occupied")
	}
	parent := s.lifecycleContext
	if parent == nil {
		parent = context.Background()
	}
	if ctx.Err() != nil {
		return model.Err("status_cancelled", "request cancelled before job acceptance")
	}
	browser, _ := ctx.Value(browserContextKey{}).(bool)
	principal, _ := ctx.Value(browserPrincipalKey{}).([32]byte)
	deadline := time.Now().Add(time.Minute)
	if browser {
		expiry, ok := ctx.Value(browserExpiryKey{}).(time.Time)
		if !ok || principal == ([32]byte{}) || !time.Now().Before(expiry) {
			return model.Err("forbidden", "verified current browser principal and expiry required")
		}
		if expiry.Before(deadline) {
			deadline = expiry
		}
	}
	owned, cancel := context.WithDeadline(parent, deadline)
	job := statusJob{id, owned, cancel, run}
	select {
	case service.queue <- job:
		service.jobs[id] = statusControl{cancel, browser, principal}
	default:
		cancel()
		return model.Err("status_busy", "status queue is full")
	}
	if !service.started {
		service.started = true
		for i := 0; i < 2; i++ {
			s.shutdownWG.Add(1)
			go func() {
				defer s.shutdownWG.Done()
				for {
					select {
					case <-parent.Done():
						service.mu.Lock()
						for _, control := range service.jobs {
							control.cancel()
						}
						service.mu.Unlock()
						service.provider.Close()
						return
					case job := <-service.queue:
						func() {
							defer func() { job.cancel(); service.mu.Lock(); delete(service.jobs, job.id); service.mu.Unlock() }()
							job.run(job.ctx)
						}()
					}
				}
			}()
		}
	}
	return nil
}
func (service *statusService) cancel(id string) {
	service.mu.Lock()
	defer service.mu.Unlock()
	for _, key := range []string{id, "generate:" + id} {
		if control, ok := service.jobs[key]; ok {
			control.cancel()
		}
	}
}
func (service *statusService) cancelAll() {
	service.mu.Lock()
	defer service.mu.Unlock()
	for _, control := range service.jobs {
		control.cancel()
	}
}
func strippedAttempt(a *agentstatus.Attempt) *agentstatus.Attempt {
	if a == nil {
		return nil
	}
	v := *a
	v.HTML = ""
	v.Account = nil
	return &v
}

func (s *Server) agentStatusOperation(ctx context.Context, op string, raw json.RawMessage) (any, error) {
	// A node context, even local-host-bound, cannot use the operator status store.
	if _, ok := ctx.Value(nodeSessionKey{}).(nodeSession); ok {
		return nil, model.Err("forbidden", "agent status requires a local operator session")
	}
	if len(raw) == 0 || len(raw) > 16<<10 {
		return nil, model.Err("invalid_request", "bounded status parameters required")
	}
	var q statusParams
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&q) != nil || dec.Decode(new(any)) != io.EOF || q.Offset < 0 || len(q.ID) > 128 || len(q.Key) > 100 {
		return nil, model.Err("invalid_request", "invalid status parameters")
	}
	service, err := s.agentStatusService()
	if err != nil {
		return nil, err
	}
	contacts, err := s.Manager.Contacts()
	if err != nil {
		return nil, err
	}
	host, err := s.Manager.HostIdentity()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	full := false
	state, err := service.store.Update(func(st *agentstatus.State) error {
		var e error
		full, e = st.Observe(host, contacts, now)
		for i := range st.Attempts {
			a := &st.Attempts[i]
			current := false
			for _, c := range contacts {
				if c.ID == a.Identity.Contact.ID && c.Session == a.Identity.Contact.Session && c.Registration == a.Identity.Contact.Registration {
					current = true
					break
				}
			}
			if !current && (agentstatus.Pending(a.State) || a.State == "contributed" || a.State == "late_reply") {
				a.State = "superseded"
				a.Error = "original registration is no longer current"
				service.cancel(a.ID)
			}
			if a.State == "awaiting_reply" && now.After(a.Deadline) {
				a.State = "timed_out"
			}
			if a.State == "pending_delivery" && now.Sub(a.Created) > time.Minute {
				a.State = "unknown"
				a.Error = "delivery deadline passed; inspect the original recorded message key"
				service.cancel(a.ID)
			}
			if a.State == "generating" && a.CallStarted != nil && now.Sub(*a.CallStarted) > time.Minute {
				a.State = "unknown"
				a.Error = "generation deadline passed; provider outcome uncertain"
				service.cancel(a.ID)
			}
		}
		return e
	})
	if err != nil {
		return nil, model.Err("status_unavailable", err.Error())
	}
	switch op {
	case "agent_status_list":
		rows := []statusRow{}
		for _, entry := range state.Entries {
			current := false
			for _, c := range contacts {
				if (agentstatus.Identity{Host: host, Contact: c}).Key() == entry.ID {
					current = true
					break
				}
			}
			row := statusRow{Entry: entry, Current: current, Latest: strippedAttempt(state.Latest(entry.ID))}
			if report := state.Report(entry.ID); report != nil {
				row.ReportID = report.ID
			}
			rows = append(rows, row)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
		if q.Offset > len(rows) {
			return nil, model.Err("invalid_request", "status offset exceeds retained inventory")
		}
		end := min(q.Offset+16, len(rows))
		setup := (agentstatus.OnboardingStore{Home: s.home}).ProjectSetup(ctx, func(ctx context.Context) ([]rollout.Project, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			policy, err := (rollout.Store{Home: s.home}).Read()
			return policy.Projects, err
		})
		return map[string]any{"version": 1, "rows": rows[q.Offset:end], "next": end, "total": len(rows), "capacity_reached": full, "config": state.Config, "project_setup": setup, "scope": "local authority; retained identities observed since status inventory began"}, nil
	case "agent_status_read":
		for _, e := range state.Entries {
			if e.ID == q.ID {
				return map[string]any{"entry": e, "latest": state.Latest(e.ID), "report": state.Report(e.ID)}, nil
			}
		}
		return nil, model.Err("not_found", "status identity is not retained")
	case "agent_status_config":
		if q.Config == nil {
			return state.Config, nil
		}
		if !q.Confirm {
			return nil, model.Err("invalid_request", "explicit configuration confirmation required")
		}
		if err = q.Config.Validate(); err != nil {
			return nil, model.Err("invalid_request", err.Error())
		}
		st, err := service.store.Update(func(st *agentstatus.State) error {
			if q.Config.Revision != st.Config.Revision {
				return model.Err("stale_revision", "inspect current summary configuration")
			}
			st.Config = *q.Config
			st.Config.Revision++
			for i := range st.Attempts {
				if st.Attempts[i].State == "generating" {
					st.Attempts[i].State = "unknown"
					st.Attempts[i].Error = "configuration changed; provider billing outcome unknown"
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		service.cancelAll()
		return st.Config, nil
	case "agent_status_request":
		return s.agentStatusRequest(ctx, service, q, state, host, contacts, now)
	case "agent_status_collect":
		return s.agentStatusCollect(ctx, service, q.ID)
	case "agent_status_generate":
		if !q.Confirm {
			return nil, model.Err("invalid_request", "explicit model-call confirmation required")
		}
		return s.agentStatusGenerate(ctx, service, q.ID)
	case "agent_status_cancel":
		if q.CancelOwned {
			browser, _ := ctx.Value(browserContextKey{}).(bool)
			principal, _ := ctx.Value(browserPrincipalKey{}).([32]byte)
			if !browser || principal == ([32]byte{}) || !q.Confirm {
				return nil, model.Err("forbidden", "explicit original browser principal cancellation required")
			}
			service.mu.Lock()
			owned := []string{}
			for key, control := range service.jobs {
				if control.browser && control.principal == principal {
					owned = append(owned, strings.TrimPrefix(key, "generate:"))
				}
			}
			service.mu.Unlock()
			_, err := service.store.Update(func(st *agentstatus.State) error {
				for _, id := range owned {
					if a := st.Attempt(id); a != nil && a.State != "ready" {
						a.State = "cancelled"
						a.Error = "principal-scoped cancellation requested; provider outcome may be unknown"
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			for _, id := range owned {
				service.cancel(id)
			}
			return map[string]any{"cancelled_owned": len(owned)}, nil
		}
		st, err := service.store.Update(func(st *agentstatus.State) error {
			a := st.Attempt(q.ID)
			if a == nil {
				return model.Err("not_found", "status attempt not retained")
			}
			if a.State == "ready" {
				return nil
			}
			a.State = "cancelled"
			if a.CallStarted != nil {
				a.Error = "cancel requested; provider billing outcome may be unknown"
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		service.cancel(q.ID)
		return strippedAttempt(st.Attempt(q.ID)), nil
	}
	return nil, model.Err("invalid_request", "unsupported status operation")
}

func statusBody(id string) string {
	return `ARM status contribution request v1 (presentation only; no enrollment, resource or execution changes). Request ` + id + `. Please contribute your own account of current work, planned work, notes, questions and blockers. Reply with ONE JSON object only, at most 8192 UTF-8 bytes; arrays contain at most 12 text entries, each at most 1024 bytes. Unknown/absent work stays empty. Do not include secrets or unrelated private context. Schema: {"version":1,"request_id":"` + id + `","current":[],"planned":[],"notes":[],"questions":[],"blockers":[]}. Use your exact existing ARM registration and reply to this original message with arm agent reply --id YOUR_ID --session YOUR_SESSION --registration YOUR_REGISTRATION --message ORIGINAL_MESSAGE_ID --key status-reply:` + id + ` --text -. Keep that reply key on retries. A separate configured summarizer will synthesize this account as static HTML. Do not start another agent or change your own model for this request.`
}
func (s *Server) agentStatusRequest(ctx context.Context, service *statusService, q statusParams, state agentstatus.State, host string, contacts []agents.Contact, now time.Time) (any, error) {
	if q.Key == "" || q.Contact == nil || !q.Confirm {
		return nil, model.Err("invalid_request", "reviewed contact, original key and explicit send confirmation required")
	}
	identity := agentstatus.Identity{Host: host, Contact: *q.Contact}
	if identity.Key() != q.ID {
		return nil, model.Err("invalid_request", "reviewed identity differs from selected status entry")
	}
	valid := false
	for _, c := range contacts {
		if c == *q.Contact {
			valid = true
		}
	}
	if !valid {
		return nil, model.Err("contact_changed", "review the current exact recipient settings")
	}
	digest := agentstatus.RequestDigest(q.ID, *q.Contact)
	var attempt agentstatus.Attempt
	created := false
	_, err := service.store.Update(func(st *agentstatus.State) error {
		for _, a := range st.Attempts {
			if a.Key == q.Key {
				if a.Digest != digest {
					return model.Err("idempotency_conflict", "status key belongs to a different exact request")
				}
				attempt = a
				return nil
			}
		}
		st.Prune(now)
		if len(st.Attempts) >= agentstatus.MaxAttempts {
			return model.Err("status_capacity", "status attempt history is full")
		}
		last := st.Latest(q.ID)
		generation := 1
		if last != nil {
			if agentstatus.Pending(last.State) {
				return model.Err("status_pending", "inspect or cancel the retained attempt before requesting another")
			}
			generation = last.Generation + 1
		}
		attempt = agentstatus.Attempt{ID: local.ID(), Key: q.Key, Digest: digest, EntryID: q.ID, Identity: identity, Generation: generation, Provider: st.Config.Provider, Model: st.Config.Model, State: "pending_delivery", Created: now, Deadline: now.Add(2 * time.Minute)}
		st.Attempts = append(st.Attempts, attempt)
		created = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !created {
		return strippedAttempt(&attempt), nil
	}
	err = s.statusSchedule(ctx, service, attempt.ID, func(job context.Context) {
		if job.Err() != nil {
			_, _ = service.store.Update(func(st *agentstatus.State) error {
				a := st.Attempt(attempt.ID)
				if a != nil && a.State == "pending_delivery" {
					a.State = "cancelled"
					a.Error = "delivery cancelled before worker entry"
				}
				return nil
			})
			return
		}
		msg, sendErr := s.Manager.SendMessage(job, manager.MessageRequest{Key: "status:" + attempt.Key, Audience: attempt.Identity.Contact.ID, Body: statusBody(attempt.ID), Targets: []agents.Contact{attempt.Identity.Contact}})
		_, _ = service.store.Update(func(st *agentstatus.State) error {
			a := st.Attempt(attempt.ID)
			if a == nil {
				return nil
			}
			a.MessageID = msg.ID
			if a.State != "pending_delivery" {
				return nil
			}
			if sendErr != nil {
				a.State = "unknown"
				a.Error = "delivery outcome uncertain; inspect the original recorded message key"
				return nil
			}
			a.State = "awaiting_reply"
			if d, ok := msg.Deliveries[a.Identity.Contact.ID]; ok {
				if d.State == "failed" || d.State == "superseded" {
					a.State = "failed"
					a.Error = "delivery failed or recipient changed"
				} else if d.State == "unknown" {
					a.State = "unknown"
					a.Error = "delivery outcome uncertain"
				}
			}
			return nil
		})
	})
	if err != nil {
		_, _ = service.store.Update(func(st *agentstatus.State) error {
			a := st.Attempt(attempt.ID)
			a.State = "failed"
			a.Error = "status queue refused delivery; no message was sent"
			return nil
		})
		return nil, err
	}
	return strippedAttempt(&attempt), nil
}

func (s *Server) agentStatusCollect(ctx context.Context, service *statusService, id string) (any, error) {
	state, err := service.store.Update(nil)
	if err != nil {
		return nil, err
	}
	a := state.Attempt(id)
	if a == nil {
		return nil, model.Err("not_found", "status attempt not retained")
	}
	if a.State == "cancelled" || a.State == "superseded" || a.CallStarted != nil {
		return strippedAttempt(a), nil
	}
	evidence, err := s.Manager.AgentStatusEvidence(ctx, "status:"+a.Key, a.ID, a.Identity.Contact)
	if err != nil {
		return nil, err
	}
	if evidence.Original.ID == "" {
		return strippedAttempt(a), nil
	}
	var account *agentstatus.Account
	replyID := ""
	var at time.Time
	late := false
	for _, reply := range evidence.Replies {
		v, e := agentstatus.DecodeAccount(reply.Body)
		if e == nil && v.RequestID == a.ID {
			if account != nil {
				old, _ := json.Marshal(account)
				current, _ := json.Marshal(v)
				if !bytes.Equal(old, current) {
					return nil, model.Err("status_ambiguous_reply", "different structured accounts exist for this exact request; evidence preserved")
				}
			}
			account = &v
			replyID = reply.ID
			late = late || reply.Created.After(a.Deadline)
		}
	}
	if account == nil {
		return map[string]any{"attempt": strippedAttempt(a), "note": "No valid structured account for this request from its original exact recipient"}, nil
	}
	at = time.Now().UTC()
	st, err := service.store.Update(func(st *agentstatus.State) error {
		current := st.Attempt(id)
		if current == nil || current.State == "cancelled" || current.State == "superseded" || current.CallStarted != nil {
			return nil
		}
		current.MessageID = evidence.Original.ID
		current.ReplyID = replyID
		current.ReplyAt = &at
		current.Account = account
		current.State = "late_reply"
		current.Error = ""
		if !late && !at.After(current.Deadline) {
			current.State = "contributed"
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return strippedAttempt(st.Attempt(id)), nil
}

func (s *Server) agentStatusGenerate(ctx context.Context, service *statusService, id string) (any, error) {
	state, err := service.store.Update(nil)
	if err != nil {
		return nil, err
	}
	a := state.Attempt(id)
	if a == nil {
		return nil, model.Err("not_found", "status attempt not retained")
	}
	if a.CallStarted != nil || a.HTML != "" {
		return strippedAttempt(a), nil
	}
	if a.Account == nil || a.State != "contributed" && a.State != "late_reply" {
		return nil, model.Err("status_no_account", "collect a structured contribution from the owning agent first")
	}
	c := state.Config
	if !c.Enabled || c.Provider != a.Provider || c.Model != a.Model {
		return nil, model.Err("status_provider_disabled", "configure and explicitly enable the exact provider/model selected for this request")
	}
	credential := os.Getenv(c.CredentialEnv)
	if credential == "" {
		return nil, model.Err("status_credential_unavailable", "the configured credential variable is unavailable to this backend; no native login credentials are read")
	}
	prompt, err := agentstatus.SynthesisPrompt(*a.Account)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	_, err = service.store.Update(func(st *agentstatus.State) error {
		current := st.Attempt(id)
		if current == nil || current.CallStarted != nil || current.Account == nil || st.Config != c || current.State != "contributed" && current.State != "late_reply" {
			return model.Err("status_changed", "attempt or configuration changed")
		}
		day := now.Format("2006-01-02")
		if st.Day != day {
			st.Day = day
			st.DailyCalls = 0
		}
		if st.DailyCalls >= c.MaxDailyRequests {
			return model.Err("status_daily_limit", "configured daily request limit reached")
		}
		st.DailyCalls++
		current.CallStarted = &now
		current.ReservedBytes = agentstatus.FinalizationReserve
		current.State = "generating"
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = s.statusSchedule(ctx, service, "generate:"+id, func(job context.Context) {
		// At most two model workers own one bounded invalidation watcher each.
		call, cancel := context.WithCancel(job)
		if _, current := s.Manager.StatusContactSnapshot(a.Identity.Contact); !current {
			cancel()
		}
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			for {
				changed, current := s.Manager.StatusContactSnapshot(a.Identity.Contact)
				if !current {
					_, _ = service.store.Update(func(st *agentstatus.State) error {
						v := st.Attempt(id)
						if v != nil && v.State == "generating" {
							v.State = "superseded"
							v.Error = "original recipient registration/settings changed; provider outcome may be unknown"
						}
						return nil
					})
					cancel()
					return
				}
				select {
				case <-call.Done():
					return
				case <-changed:
				}
			}
		}()
		html, usage, generateErr := service.generate(call, c, credential, prompt)
		cancel()
		<-finished
		credential = ""
		_, _ = service.store.Update(func(st *agentstatus.State) error {
			current := st.Attempt(id)
			if current == nil {
				return nil
			}
			current.Usage = usage
			current.ReservedBytes = 0
			if current.State != "generating" || st.Config != c {
				return nil
			}
			if generateErr != nil {
				current.State = "failed"
				current.Error = generateErr.Error()
				if job.Err() != nil {
					current.State = "unknown"
				}
				return nil
			}
			current.HTML = html
			at := time.Now().UTC()
			current.Generated = &at
			current.State = "ready"
			current.Error = ""
			return nil
		})
	})
	if err != nil {
		_, _ = service.store.Update(func(st *agentstatus.State) error {
			current := st.Attempt(id)
			current.State = "failed"
			current.ReservedBytes = 0
			current.Error = "queue refused generation; no provider request was started; daily reservation retained"
			return nil
		})
		return nil, err
	}
	return map[string]string{"id": id, "state": "generating"}, nil
}
