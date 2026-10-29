package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/local"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"github.com/coder/websocket"
)

// Fixtures hold an actual private directory, just as Serve does. This supplies
// no authentication: caller proofs still require a real accepted WS ingress.
func residentFixtureIdentity(t *testing.T, s *Server) {
	t.Helper()
	if s.nodeStopDirectory != nil {
		return
	}
	home := s.home
	if home == "" {
		home = filepath.Join(t.TempDir(), "authority")
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	d, err := database.OpenDirectory(physical)
	if err != nil {
		t.Fatal(err)
	}
	s.nodeStopDirectory = d
	if s.generation == "" {
		s.generation = local.ID()
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
}

func residentEventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("original ingress did not reach expected state")
}
func residentCallerCount(s *Server) int {
	s.callers.Lock()
	defer s.callers.Unlock()
	return len(s.callers.entries)
}
func residentOnlyCaller(t *testing.T, s *Server, active bool) *authenticatedCaller {
	t.Helper()
	var caller *authenticatedCaller
	residentEventually(t, func() bool {
		s.callers.Lock()
		defer s.callers.Unlock()
		if len(s.callers.entries) != 1 {
			return false
		}
		for c, i := range s.callers.entries {
			if i.active == active {
				caller = c
				return true
			}
		}
		return false
	})
	return caller
}
func residentSocket(t *testing.T, s *Server, options *websocket.DialOptions) (*websocket.Conn, *httptest.Server) {
	t.Helper()
	return residentSocketHandler(t, s, options, http.HandlerFunc(s.ws))
}

func residentSocketHandler(t *testing.T, s *Server, options *websocket.DialOptions, handler http.Handler) (*websocket.Conn, *httptest.Server) {
	t.Helper()
	residentFixtureIdentity(t, s)
	h := httptest.NewServer(handler)
	s.authority = strings.TrimPrefix(h.URL, "http://")
	if options.HTTPHeader == nil {
		options.HTTPHeader = make(http.Header)
	}
	if len(options.Subprotocols) > 0 {
		options.HTTPHeader.Set("Origin", h.URL)
	}
	c, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(h.URL, "http"), options)
	if err != nil {
		h.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer h.Close()
		c.CloseNow()
		residentEventually(t, func() bool { return residentCallerCount(s) == 0 })
		h.Close()
	})
	return c, h
}
func residentNativeOptions(s *Server) *websocket.DialOptions {
	return &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + s.token}}}
}

func TestResidentCallerRequiresAcceptedOriginalIngress(t *testing.T) {
	s := meshFixture(t)
	residentFixtureIdentity(t, s)
	s.token = local.ID() + local.ID()
	req := httptest.NewRequest("GET", "http://127.0.0.1:1234/v1/ws", nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	pending, err := s.authenticateLocalIngress(req, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, caller := range []*authenticatedCaller{nil, {}, pending, {owner: s, proof: pending.proof}} {
		if _, err := s.prepareCall(caller, model.WireRequest{Operation: "status"}); err == nil {
			t.Fatal("non-accepted caller dispatched")
		}
	}
	if residentCallerCount(s) != 0 {
		t.Fatal("credential-only recognition leaked registry entry")
	}
	// The actual WS origin rejection occurs after credential recognition.
	h := httptest.NewServer(http.HandlerFunc(s.ws))
	defer h.Close()
	opts := residentNativeOptions(s)
	opts.HTTPHeader.Set("Origin", "http://invalid.example")
	if c, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(h.URL, "http"), opts); err == nil {
		c.CloseNow()
		t.Fatal("wrong-origin connection accepted")
	}
	if residentCallerCount(s) != 0 {
		t.Fatal("rejected WS leaked registry entry")
	}
}

func TestResidentPreparedCallOwnsBytesAndRejectsForeignRetiredReplay(t *testing.T) {
	s := meshFixture(t)
	s.token = local.ID() + local.ID()
	c, _ := residentSocket(t, s, residentNativeOptions(s))
	caller := residentOnlyCaller(t, s, true)
	// A raw byte copy must survive mutation of the caller's input buffer.
	raw := json.RawMessage(`{"after":0}`)
	call, err := s.prepareCall(caller, model.WireRequest{Version: 1, ID: "original", Operation: "events", Params: raw})
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = '['
	result := s.dispatchPrepared(call)
	if result.response.Error != nil || result.response.ID != "original" || result.disposition != dispatchDomainResult {
		t.Fatal(result)
	}
	if again := s.dispatchPrepared(call); again.disposition != dispatchNotInvoked || again.response.Error == nil {
		t.Fatal("prepared request invoked twice")
	}
	stolen := &authenticatedCaller{owner: s, proof: caller.proof}
	if _, err := s.prepareCall(stolen, model.WireRequest{Operation: "status"}); err == nil {
		t.Fatal("copied proof accepted")
	}
	other := &Server{}
	if _, err := other.prepareCall(caller, model.WireRequest{Operation: "status"}); err == nil {
		t.Fatal("foreign server accepted caller")
	}
	outstanding, err := s.prepareCall(caller, model.WireRequest{ID: "unexecuted", Operation: "status"})
	if err != nil {
		t.Fatal(err)
	}
	c.CloseNow()
	residentEventually(t, func() bool { return residentCallerCount(s) == 0 })
	if got := s.dispatchPrepared(outstanding); got.response.Error == nil || got.disposition != dispatchNotInvoked {
		t.Fatal("retired caller invoked operation")
	}
}

func TestResidentBrowserCallerRetainsRestrictionsAndPrincipal(t *testing.T) {
	s := meshFixture(t)
	link, err := s.browser.issue("127.0.0.1:1234", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ticket := strings.Split(link.URL, "#connect=")[1]
	token, expires, ok := s.browser.exchange(ticket, time.Now())
	if !ok {
		t.Fatal("exchange")
	}
	residentSocket(t, s, &websocket.DialOptions{Subprotocols: []string{"arm.v1", "arm.auth." + token}})
	caller := residentOnlyCaller(t, s, true)
	ctx, err := s.callerContext(caller)
	if err != nil || ctx.Value(browserContextKey{}) != true || ctx.Value(browserExpiryKey{}) != expires || ctx.Value(browserPrincipalKey{}) != caller.proof.admission.Principal {
		t.Fatal("verified browser context lost", err)
	}
	for _, op := range []string{"run_create", "run_append", "run_finish", "acquire", "provision_runtime"} {
		call, err := s.prepareCall(caller, model.WireRequest{ID: op, Operation: op, Params: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		got := s.dispatchPrepared(call)
		if got.response.Error == nil || got.response.Error.Code != "forbidden" || got.disposition != dispatchDomainResult {
			t.Fatal("browser operation escaped policy", op, got)
		}
	}
}

func TestResidentNegotiatingIngressHasNoDispatchAuthority(t *testing.T) {
	s := meshFixture(t)
	s.token = local.ID() + local.ID()
	opts := residentNativeOptions(s)
	opts.HTTPHeader.Set("Arm-Node-Context", "v1")
	c, _ := residentSocket(t, s, opts)
	caller := residentOnlyCaller(t, s, false)
	if _, err := s.prepareCall(caller, model.WireRequest{Operation: "status"}); err == nil {
		t.Fatal("negotiating caller gained authority")
	}
	response := nativeNodeRequest(t, t.Context(), c, "acquire", model.Request{Reason: "never admitted", TTL: time.Minute})
	if response.Error == nil || response.Error.Code != "protocol_error" {
		t.Fatal(response)
	}
	residentEventually(t, func() bool { return residentCallerCount(s) == 0 })
	if _, err := s.prepareCall(caller, model.WireRequest{Operation: "status"}); err == nil {
		t.Fatal("failed first frame retained authority")
	}
}

func TestResidentDirectReporterSurvivesCallerRetirementAndStopGate(t *testing.T) {
	s := meshFixture(t)
	s.token = local.ID() + local.ID()
	c, h := residentSocket(t, s, residentNativeOptions(s))
	caller := residentOnlyCaller(t, s, true)
	dispatch := func(caller *authenticatedCaller, op string, params any) dispatchResult {
		t.Helper()
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		call, err := s.prepareCall(caller, model.WireRequest{Version: 1, ID: op, Operation: op, Params: raw})
		if err != nil {
			t.Fatal(err)
		}
		return s.dispatchPrepared(call)
	}
	created := dispatch(caller, "run_create", model.RunCreateRequest{Key: "reporter", Command: []string{"fixture"}, CWD: t.TempDir(), Reason: "resident reporter fixture"})
	if created.response.Error != nil {
		t.Fatal(created.response.Error)
	}
	raw, _ := json.Marshal(created.response.Result)
	var run model.Run
	if err := json.Unmarshal(raw, &run); err != nil || run.ID == "" {
		t.Fatal("run result", err)
	}
	c.CloseNow()
	residentEventually(t, func() bool { return residentCallerCount(s) == 0 })
	// Reporting uses another actual accepted connection to the SAME resident.
	fresh, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(h.URL, "http"), residentNativeOptions(s))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.CloseNow()
	next := residentOnlyCaller(t, s, true)
	s.nodeStopMu.Lock()
	s.nodeStopClosed = true
	s.nodeStopMu.Unlock()
	appendResult := dispatch(next, "run_append", model.RunAppendRequest{ID: run.ID, Offset: 0, Data: []byte("resident output")})
	if appendResult.response.Error != nil {
		t.Fatal(appendResult.response.Error)
	}
	finish := dispatch(next, "run_finish", model.RunFinishRequest{ID: run.ID, ExitCode: 0, Outcome: "completed"})
	if finish.response.Error != nil {
		t.Fatal(finish.response.Error)
	}
	// The stop gate still refuses new promises, without a new auth-specific gate.
	refused := dispatch(next, "run_create", model.RunCreateRequest{Key: "blocked", Command: []string{"fixture"}, CWD: t.TempDir(), Reason: "blocked"})
	if refused.response.Error == nil || refused.response.Error.Code != "backend_stopping" {
		t.Fatal(refused)
	}
	s.nodeStopMu.Lock()
	count := s.nodeStopCalls["inspection"] + s.nodeStopCalls["rpc"]
	s.nodeStopMu.Unlock()
	if count != 0 {
		t.Fatal("operation stop registration leaked", count)
	}
}

func TestResidentCallerMasksInheritedAuthorityContext(t *testing.T) {
	s := meshFixture(t)
	s.token = local.ID() + local.ID()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), browserContextKey{}, true)
		ctx = context.WithValue(ctx, browserExpiryKey{}, time.Now().Add(-time.Hour))
		ctx = context.WithValue(ctx, browserPrincipalKey{}, [32]byte{1})
		ctx = context.WithValue(ctx, nodeSessionKey{}, nodeSession{Host: "forged", Source: "forged"})
		s.ws(w, r.WithContext(ctx))
	})
	residentSocketHandler(t, s, residentNativeOptions(s), handler)
	caller := residentOnlyCaller(t, s, true)
	ctx, err := s.callerContext(caller)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Value(browserContextKey{}) != false || ctx.Value(browserExpiryKey{}) != (time.Time{}) || ctx.Value(browserPrincipalKey{}) != ([32]byte{}) {
		t.Fatal("ambient browser authority survived")
	}
	if _, ok := ctx.Value(nodeSessionKey{}).(nodeSession); ok {
		t.Fatal("ambient node context survived")
	}
	call, err := s.prepareCall(caller, model.WireRequest{ID: "native", Operation: "run_create", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	result := s.dispatchPrepared(call)
	if result.response.Error == nil || result.response.Error.Code == "forbidden" || result.response.Error.Code == "node_context_changed" {
		t.Fatal("ambient restrictions changed native validation", result)
	}
}

func TestResidentAdmittedJobOutlivesOriginalIngress(t *testing.T) {
	s := meshFixture(t)
	s.token = local.ID() + local.ID()
	c, _ := residentSocket(t, s, residentNativeOptions(s))
	caller := residentOnlyCaller(t, s, true)
	release := filepath.Join(t.TempDir(), "finish")
	t.Cleanup(func() { _ = os.WriteFile(release, []byte("finish"), 0600) })
	request := manager.RunRequest{Request: model.Request{Resources: model.Resources{CPU: 0.01}, TTL: 10 * time.Second, Reason: "original resident lifetime", Key: "original-job"}, CWD: t.TempDir(), Command: []string{"/bin/sh", "-c", `printf ready; while [ ! -f "$1" ]; do sleep 0.02; done; printf done`, "fixture", release}}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	call, err := s.prepareCall(caller, model.WireRequest{ID: "job", Operation: "run", Params: raw})
	if err != nil {
		t.Fatal(err)
	}
	result := s.dispatchPrepared(call)
	if result.response.Error != nil {
		t.Fatal(result.response.Error)
	}
	encoded, err := json.Marshal(result.response.Result)
	if err != nil {
		t.Fatal(err)
	}
	var admitted model.Lease
	if err := json.Unmarshal(encoded, &admitted); err != nil || admitted.ID == "" {
		t.Fatal("lease result", err)
	}
	residentEventually(t, func() bool { data, _ := s.Manager.Output(admitted.ID, 0); return string(data) == "ready" })
	c.CloseNow()
	residentEventually(t, func() bool { return residentCallerCount(s) == 0 })
	live, err := s.Manager.Inspect(admitted.ID)
	if err != nil || live.Ended != nil || live.PID != admitted.PID {
		t.Fatal("ingress retirement changed job ownership", err)
	}
	if err := os.WriteFile(release, []byte("finish"), 0600); err != nil {
		t.Fatal(err)
	}
	residentEventually(t, func() bool {
		lease, err := s.Manager.Inspect(admitted.ID)
		return err == nil && lease.Ended != nil && lease.ExitCode != nil && *lease.ExitCode == 0 && lease.Outcome == "completed"
	})
	output, err := s.Manager.Output(admitted.ID, 0)
	if err != nil || string(output) != "readydone" {
		t.Fatal("original output incomplete", err)
	}
}

func TestResidentCallerRejectsIdentityChangeAndResurrection(t *testing.T) {
	s := meshFixture(t)
	s.token = local.ID() + local.ID()
	c, _ := residentSocket(t, s, residentNativeOptions(s))
	// Await an actual ordinary response before changing this test's otherwise
	// immutable identity; registry activation precedes the WS reader's readiness.
	response := nativeNodeRequest(t, t.Context(), c, "status", struct{}{})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	caller := residentOnlyCaller(t, s, true)
	originalGeneration := s.generation
	s.generation = local.ID()
	if _, err := s.prepareCall(caller, model.WireRequest{Operation: "status"}); err == nil {
		t.Fatal("changed resident generation accepted")
	}
	s.generation = originalGeneration
	originalDirectory := s.nodeStopDirectory
	s.nodeStopDirectory = nil
	if _, err := s.prepareCall(caller, model.WireRequest{Operation: "status"}); err == nil {
		t.Fatal("missing original directory accepted")
	}
	s.nodeStopDirectory = originalDirectory
	s.callers.Lock()
	ingress := s.callers.entries[caller]
	s.callers.Unlock()
	c.CloseNow()
	residentEventually(t, func() bool { return residentCallerCount(s) == 0 })
	if err := s.registerCallerIngress(caller, ingress.connection, context.Background()); err == nil {
		t.Fatal("retired proof resurrected")
	}
}

func TestResidentProjectionPreservesRawNumbersAndTypedErrors(t *testing.T) {
	s := meshFixture(t)
	s.token = local.ID() + local.ID()
	residentSocket(t, s, residentNativeOptions(s))
	ctx, err := s.callerContext(residentOnlyCaller(t, s, true))
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := &model.Error{Code: "naming_unavailable", Message: "synthetic diagnostic"}
	original := json.RawMessage(`{"integer":9007199254740993,"decimal":1.0000000000000001}`)
	projected := s.projectDispatch(ctx, model.WireRequest{ID: "exact", Operation: "status"}, sourceNamedRead{raw: original, diagnostic: diagnostic}, nil)
	encoded, err := json.Marshal(projected.response)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), string(original)) || projected.response.NamingDiagnostic != diagnostic {
		t.Fatal("raw values or diagnostic changed")
	}
	typed := &model.Error{Code: "remote_outcome_unknown", Message: "inspect original operation"}
	failed := s.projectDispatch(ctx, model.WireRequest{ID: "original-key", Operation: "renew"}, nil, typed)
	if failed.response.Error != typed || failed.response.ID != "original-key" || failed.disposition != dispatchDomainResult {
		t.Fatal("domain uncertainty changed")
	}
}

func TestResidentNativeNegotiationMasksInheritedPrincipalBeforeBinding(t *testing.T) {
	s := meshFixture(t)
	s.token = local.ID() + local.ID()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), browserContextKey{}, true)
		ctx = context.WithValue(ctx, browserExpiryKey{}, time.Now().Add(time.Hour))
		ctx = context.WithValue(ctx, browserPrincipalKey{}, [32]byte{1})
		ctx = context.WithValue(ctx, nodeSessionKey{}, nodeSession{Host: "forged", Source: "forged"})
		s.ws(w, r.WithContext(ctx))
	})
	opts := residentNativeOptions(s)
	opts.HTTPHeader.Set("Arm-Node-Context", "v1")
	c, _ := residentSocketHandler(t, s, opts, handler)
	status, err := s.Manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	first := nativeNodeRequest(t, t.Context(), c, "node_context_open", model.NodeSelection{Host: status.Host, Source: status.Host})
	if first.Error != nil {
		t.Fatal(first.Error)
	}
	caller := residentOnlyCaller(t, s, true)
	ctx, err := s.callerContext(caller)
	if err != nil {
		t.Fatal(err)
	}
	bound, ok := ctx.Value(nodeSessionKey{}).(nodeSession)
	if !ok || !bound.Local || !bound.expiresAt.IsZero() || bound.principal != ([32]byte{}) {
		t.Fatal("native binding inherited browser authority")
	}
	// Receiving this request ID also verifies the first-frame ACK has one writer.
	next := nativeNodeRequest(t, t.Context(), c, "status", struct{}{})
	if next.Error != nil {
		t.Fatal(next.Error)
	}
}
