package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"getarm.org/internal/agents"
	"getarm.org/internal/cargoprofile"
	"getarm.org/internal/database"
	"getarm.org/internal/local"
	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/meshops"
	"getarm.org/internal/model"
	"getarm.org/internal/nodestop"
	"getarm.org/internal/observe"
	"getarm.org/internal/presence"
	"getarm.org/internal/workspaceroute"
	"getarm.org/internal/workspacetransport"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type Server struct {
	peerCacheMu           sync.RWMutex
	peerCache             model.PeerSnapshot
	peerCacheErr          error
	peerSurveySlots       chan struct{}
	presenceMu            sync.RWMutex
	presenceDir           *presence.Directory
	presencePub           *presence.Publication
	presenceError         string
	presenceDone          chan struct{}
	peerSlots             chan struct{}
	generation            string
	nodeStopDirectory     *database.Directory
	nodeStopTarget        nodestop.Target
	nodeStopTargetMu      sync.RWMutex
	nodeStopUnavailable   string
	nodeStopMu            sync.Mutex
	nodeStopClosed        bool
	nodeStopAdminEnabled  bool // remains false until reviewed public recovery is delivered
	nodeStopLock          *os.File
	nodeStopClosing       bool
	nodeStopCalls         map[string]int
	nodeStopCallsWG       sync.WaitGroup
	nodeStopClosure       *nodestop.Closure
	runtimeExecutable     *mesh.RuntimeExecutable
	meshOperatorMu        sync.Mutex
	meshOperator          *meshops.Service
	nodeMu                sync.Mutex
	nodeCache             map[string]cachedNodeRead
	nodeSlots             chan struct{}
	nodeInFlight          map[string]bool
	workspaceStreamsMu    sync.Mutex
	workspaceStreams      map[*workspacetransport.Handler]struct{}
	routedStreams         map[*workspaceroute.Handler]struct{}
	workspaceCallsClosed  bool
	workspaceCallsContext context.Context
	workspaceCallsCancel  context.CancelFunc
	workspaceCalls        sync.WaitGroup
	graph                 *workspaceGraphState
	meshMu                sync.Mutex
	meshClosing           bool
	meshCloseEvidenceMu   sync.Mutex
	meshClosers           map[*meshService]*meshClose
	meshAdminMu           sync.Mutex
	mesh                  *meshService
	meshPending           *preparedMeshSetup
	Manager               *manager.Manager
	home, token           string
	stop                  context.CancelFunc
	admission             sync.Mutex
	stopping              bool
	storageScan           chan struct{}
	portScan              chan struct{}
	monitor               *observe.Monitor
	monitorSlots          chan struct{}
	authority             string
	browser               browserCredentials
	screenSlots           chan struct{}
	screenOnce            sync.Once
	lifecycleContext      context.Context
	shutdownWorker        sync.Once
	shutdownWG            sync.WaitGroup
	lifecycleMu           sync.Mutex
	lifecycleClosing      bool
}

func Serve(ctx context.Context, home, address string, capacity model.Resources) error {
	return serveNode(ctx, home, address, capacity, nil)
}

func serveNode(ctx context.Context, home, address string, capacity model.Resources, restart *nodestop.Restart) (resultErr error) {
	if e := local.Prepare(home); e != nil {
		return e
	}
	physical, e := filepath.EvalSymlinks(home)
	if e != nil {
		return e
	}
	home, e = filepath.Abs(physical)
	if e != nil {
		return e
	}
	if e = mesh.RegisterManagedExecutable(home); e != nil {
		return e
	}
	directory, e := database.OpenDirectory(home)
	if e != nil {
		return e
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	lock, _, e := directory.Open("daemon.lock", os.O_CREATE|os.O_RDWR)
	if e != nil {
		return e
	}
	if lock == nil {
		return fmt.Errorf("actual authority lock required")
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return fmt.Errorf("another ARM authority owns this state directory: %w", e)
	}
	defer func() { resultErr = errors.Join(resultErr, syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)) }()
	if e = checkNodeStopStartup(ctx, home, lock, restart); e != nil {
		return e
	}
	host, _, e := net.SplitHostPort(address)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return model.Err("invalid_address", "backend must listen on a literal loopback address")
	}
	if e = database.InstallResetSeed(ctx, home, lock); e != nil {
		return e
	}
	m, e := manager.Open(home, capacity)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var s *Server
	var listener net.Listener
	var monitorStop context.CancelFunc
	var monitorDone <-chan struct{}
	var runtimeExecutable *mesh.RuntimeExecutable
	var endpointPublished bool
	var httpClosed error
	var httpServer *http.Server
	defer func() {
		defer func() { traceStopServeEnd(s, resultErr) }()
		if s != nil {
			s.nodeStopMu.Lock()
			s.nodeStopClosing = true
			s.nodeStopMu.Unlock()
		}
		if cancel != nil {
			cancel()
		}
		if httpServer != nil {
			httpClosed = errors.Join(httpClosed, httpServer.Close())
		}
		if s != nil {
			s.waitPresence()
		}
		stages := []struct {
			name string
			err  error
		}{{"http", httpClosed}}
		var endpointErr error
		if endpointPublished {
			endpointErr = os.Remove(filepath.Join(home, "endpoint.json"))
		}
		stages = append(stages, struct {
			name string
			err  error
		}{"endpoint", endpointErr})
		if monitorStop != nil {
			monitorStop()
			<-monitorDone
		}
		stages = append(stages, struct {
			name string
			err  error
		}{"monitor", nil})
		if s != nil {
			s.lifecycleMu.Lock()
			s.lifecycleClosing = true
			s.lifecycleMu.Unlock()
			s.shutdownWG.Wait()
		}
		stages = append(stages, struct {
			name string
			err  error
		}{"lifecycle", nil})
		if s != nil {
			s.closeMeshOperator()
		}
		stages = append(stages, struct {
			name string
			err  error
		}{"operator", nil})
		if s != nil {
			s.closeWorkspaceStreams()
		}
		stages = append(stages, struct {
			name string
			err  error
		}{"workspace", nil})
		var meshErr error
		if s != nil {
			meshErr = s.closeStopMesh()
			s.nodeStopCallsWG.Wait()
		}
		stages = append(stages, struct {
			name string
			err  error
		}{"mesh", meshErr})
		managerErr := m.Close()
		stages = append(stages, struct {
			name string
			err  error
		}{"manager", managerErr})
		if runtimeExecutable != nil {
			resultErr = errors.Join(resultErr, runtimeExecutable.Close())
		}
		for _, stage := range stages {
			if stage.err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("%s teardown: %w", stage.name, stage.err))
			}
		}
		if listener != nil {
			e := listener.Close()
			if !errors.Is(e, net.ErrClosed) {
				resultErr = errors.Join(resultErr, e)
			}
		}
		if s != nil {
			s.nodeStopMu.Lock()
			closure := s.nodeStopClosure
			s.nodeStopMu.Unlock()
			if closure != nil {
				for _, stage := range stages {
					closure.Stage(stage.name, stage.err)
				}
				traceStopTeardown(s, resultErr)
				if resultErr != nil {
					resultErr = errors.Join(resultErr, fmt.Errorf("node stop has incomplete closure; no joined marker written"))
				} else {
					resultErr = errors.Join(resultErr, closure.WriteClosed(directory.FD, lock))
				}
			}
		}
	}()
	listener, e = net.Listen("tcp", address)
	if e != nil {
		return e
	}
	if e = m.TrackInfrastructure(listener.Addr()); e != nil {
		return e
	}
	if e = saveMeshStartupPolicy(home, address, capacity); e != nil {
		return e
	}
	runtimeExecutable, _ = mesh.CaptureRuntimeExecutable()
	if e = m.ConfigureCargoHelper(func() (string, cargoprofile.HelperRuntime, error) {
		path, e := runtimeExecutable.VerifiedPath()
		if e != nil {
			return "", cargoprofile.HelperRuntime{}, e
		}
		proof, e := runtimeExecutable.Proof()
		if e != nil {
			return "", cargoprofile.HelperRuntime{}, e
		}
		return path, cargoprofile.HelperRuntime{SHA256: proof.SHA256, Size: proof.Size, Version: proof.Version, Protocol: cargoprofile.HelperProtocol}, nil
	}); e != nil {
		return e
	}
	s = &Server{nodeStopAdminEnabled: true, Manager: m, home: home, generation: local.ID(), nodeStopDirectory: directory, nodeStopLock: lock, token: local.ID() + local.ID(), stop: cancel, runtimeExecutable: runtimeExecutable, peerSlots: make(chan struct{}, 8), peerSurveySlots: make(chan struct{}, 2)}
	s.lifecycleContext = ctx
	// A mesh configuration failure never disables the authenticated local authority.
	if meshErr := s.loadMesh(); meshErr != nil {
		fmt.Fprintln(os.Stderr, "ARM mesh unavailable:", meshErr)
	}
	if e = s.configureNodeStop(); e != nil {
		fmt.Fprintln(os.Stderr, "ARM node-stop unavailable:", e)
	}
	s.authority = listener.Addr().String()
	s.storageScan = make(chan struct{}, 1)
	s.portScan = make(chan struct{}, 1)
	initial, e := m.Status()
	if e != nil {
		return e
	}
	s.monitor = observe.NewMonitor(initial.Host, func() model.ResourceMeasurement { return m.Observation("disk") })
	s.monitorSlots = make(chan struct{}, 32)
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	monitorStop = stopMonitor
	monitorFinished := make(chan struct{})
	monitorDone = monitorFinished
	go func() { defer close(monitorFinished); s.monitor.Run(monitorCtx) }()
	ep := local.Endpoint{URL: "ws://" + listener.Addr().String() + "/v1/ws", Token: s.token, PID: os.Getpid(), Generation: s.generation}
	if e = local.AtomicJSON(filepath.Join(home, "endpoint.json"), ep); e != nil {
		return e
	}
	endpointPublished = true
	m.Start()
	s.startGraphRefresh()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ws", s.ws)
	mux.HandleFunc("/v1/peers", s.peerWebSocket)
	mux.HandleFunc("/v1/screen", s.browserScreen)
	s.registerBrowser(mux)
	h := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return ctx }}
	httpServer = h
	done := make(chan error, 1)
	httpJoined := false
	go func() { done <- h.Serve(listener) }()
	s.startPresence(ctx)
	traceStopPublished(s)
	select {
	case e = <-done:
		httpJoined = true
		if !errors.Is(e, http.ErrServerClosed) {
			httpClosed = e
			return e
		}
	case <-ctx.Done():
	}
	shutdownCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	httpClosed = h.Shutdown(shutdownCtx)
	if !httpJoined {
		serveErr := <-done
		if !errors.Is(serveErr, http.ErrServerClosed) {
			httpClosed = errors.Join(httpClosed, serveErr)
		}
	}
	return httpClosed
}

func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	leave, err := s.beginStopWork("websocket")
	if err != nil {
		http.Error(w, "backend stopping", http.StatusServiceUnavailable)
		return
	}
	defer leave()
	if !s.validHost(r.Host) {
		http.Error(w, "invalid host", http.StatusForbidden)
		return
	}
	admission, ok := s.authenticateAdmission(r, time.Now())
	browser, expires := admission.Browser, admission.Expires
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	nativeNode := r.Header.Values("Arm-Node-Context")
	if len(nativeNode) > 0 && (browser || len(nativeNode) != 1 || nativeNode[0] != "v1") {
		http.Error(w, "invalid native node negotiation", http.StatusForbidden)
		return
	}
	options := &websocket.AcceptOptions{}
	if browser {
		options.Subprotocols = []string{"arm.v1"}
	}
	c, e := websocket.Accept(w, r, options)
	if e != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(65536)
	ctx, cancel := context.WithCancel(r.Context())
	var wsWG sync.WaitGroup
	defer func() { cancel(); wsWG.Wait() }()
	if len(nativeNode) == 1 {
		bound, err := s.negotiateNativeNode(ctx, c)
		if err != nil {
			return
		}
		ctx = context.WithValue(ctx, nodeSessionKey{}, bound)
		wsWG.Add(1)
		go func(watchCtx context.Context, b nodeSession) {
			defer wsWG.Done()
			s.watchNodeSession(watchCtx, cancel, b)
		}(ctx, bound)
	}
	if browser {
		ctx = context.WithValue(ctx, browserContextKey{}, true)
		ctx = context.WithValue(ctx, browserExpiryKey{}, expires)
		ctx = context.WithValue(ctx, browserPrincipalKey{}, admission.Principal)
		if bound := admission.Node; bound != nil {
			ctx = context.WithValue(ctx, nodeSessionKey{}, *bound)
			if _, e := s.validateNodeSession(*bound); e != nil {
				_ = c.Close(websocket.StatusPolicyViolation, "node_context_changed")
				return
			}
			wsWG.Add(1)
			go func(watchCtx context.Context, b nodeSession) {
				defer wsWG.Done()
				s.watchNodeSession(watchCtx, cancel, b)
			}(ctx, *bound)
		}
		timer := time.AfterFunc(time.Until(expires), cancel)
		defer timer.Stop()
	}
	out := make(chan model.WireResponse, 64)
	writerDone := make(chan struct{})
	defer func() { cancel(); <-writerDone }()
	wsWG.Add(1)
	go func() {
		defer wsWG.Done()
		defer close(writerDone)
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-out:
				writeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				e := wsjson.Write(writeCtx, c, msg)
				stop()
				if e != nil {
					return
				}
			}
		}
	}()
	send := func(msg model.WireResponse) bool {
		select {
		case out <- msg:
			return true
		case <-ctx.Done():
			return false
		default:
			cancel()
			return false
		}
	}
	var workflowSendMu sync.Mutex
	var workflowIdentity string
	var workflowSubscription context.CancelFunc
	var subscription context.CancelFunc
	var monitoring context.CancelFunc
	monitorSlot := false
	requests := make(chan struct{}, 8)
	defer func() {
		if workflowSubscription != nil {
			workflowSubscription()
		}
		if monitoring != nil {
			monitoring()
		}
		if monitorSlot {
			<-s.monitorSlots
		}
		if subscription != nil {
			subscription()
		}
	}()
	for {
		var req model.WireRequest
		if e = wsjson.Read(ctx, c, &req); e != nil {
			return
		}
		resp := model.WireResponse{Version: 1, ID: req.ID}
		if req.Version != 1 || req.ID == "" {
			resp.Error = &model.Error{Code: "protocol_error", Message: "version 1 and a request ID are required"}
			if !send(resp) {
				return
			}
			continue
		}
		// Validate immutable local contexts too, before special subscription
		// handlers can bypass ordinary dispatch. Remote handlers remain closed
		// below and are independently destination-authorized by the gateway.
		if bound, ok := ctx.Value(nodeSessionKey{}).(nodeSession); ok {
			if _, err := s.validateNodeSession(bound); err != nil {
				send(model.WireResponse{Version: 1, ID: req.ID, Error: &model.Error{Code: "node_context_changed", Message: "selected node authority changed; reopen from Mesh"}})
				return
			}
		}

		if req.Operation == "mesh_operator_watch" && !remoteNodeContext(ctx) {
			var filter struct {
				Key string `json:"key,omitempty"`
			}
			if e = mesh.Decode(req.Params, &filter); e != nil {
				resp.Error = &model.Error{Code: "invalid_request", Message: "invalid workflow watch filter"}
				send(resp)
				continue
			}
			service, watchErr := s.meshOperatorService()
			if watchErr != nil {
				resp.Error = &model.Error{Code: "backend_stopping", Message: "operator watch is unavailable"}
				send(resp)
				continue
			}
			subctx, stop := context.WithCancel(ctx)
			changes, watchErr := service.Watch(subctx, filter.Key)
			if watchErr != nil {
				stop()
				var api *model.Error
				if errors.As(watchErr, &api) {
					resp.Error = api
				} else {
					resp.Error = &model.Error{Code: "operation_failed", Message: "operator watch unavailable"}
				}
				send(resp)
				continue
			}
			if workflowSubscription != nil {
				workflowSubscription()
			}
			workflowSubscription = stop
			watchIdentity := "mesh_operator:" + local.ID()
			workflowSendMu.Lock()
			workflowIdentity = watchIdentity
			resp.Result = map[string]string{"subscription": watchIdentity, "kind": "mesh_operator"}
			sent := send(resp)
			workflowSendMu.Unlock()
			if !sent {
				return
			}
			sendWorkflow := func(response model.WireResponse) bool {
				workflowSendMu.Lock()
				defer workflowSendMu.Unlock()
				if workflowIdentity != watchIdentity || subctx.Err() != nil {
					return false
				}
				return send(response)
			}
			wsWG.Add(1)
			go func() {
				defer wsWG.Done()
				for {
					select {
					case <-subctx.Done():
						return
					case change, ok := <-changes:
						if !ok {
							sendWorkflow(model.WireResponse{Version: 1, Subscription: watchIdentity, Error: &model.Error{Code: "subscription_lost", Message: "refresh workflow snapshots and establish a new read-only watch"}})
							return
						}
						if subctx.Err() != nil {
							return
						}
						if !sendWorkflow(model.WireResponse{Version: 1, Subscription: watchIdentity, Result: change}) {
							return
						}
					}
				}
			}()
			continue
		}
		if req.Operation == "subscribe" && !remoteNodeContext(ctx) {
			var p struct {
				After int64 `json:"after"`
			}
			if e = json.Unmarshal(req.Params, &p); e != nil || p.After < 0 {
				resp.Error = &model.Error{Code: "invalid_request", Message: "after must be a nonnegative event cursor"}
				send(resp)
				continue
			}
			if subscription != nil {
				subscription()
			}
			subctx, stop := context.WithCancel(ctx)
			subscription = stop
			resp.Result = map[string]any{"after": p.After}
			if !send(resp) {
				return
			}
			wsWG.Add(1)
			go func(after int64) {
				defer wsWG.Done()
				ticker := time.NewTicker(100 * time.Millisecond)
				defer ticker.Stop()
				for {
					events, e := s.Manager.Events(after)
					if e != nil {
						cancel()
						return
					}
					for _, ev := range events {
						ev := ev
						if !send(model.WireResponse{Version: 1, Event: &ev}) {
							return
						}
						after = ev.Sequence
					}
					select {
					case <-subctx.Done():
						return
					case <-ticker.C:
					}
				}
			}(p.After)
			continue
		}
		if (req.Operation == "monitor_subscribe" || req.Operation == "monitor_unsubscribe") && !remoteNodeContext(ctx) {
			if req.Operation == "monitor_unsubscribe" {
				if monitoring != nil {
					monitoring()
					monitoring = nil
				}
				if monitorSlot {
					<-s.monitorSlots
					monitorSlot = false
				}
				resp.Result = map[string]bool{"subscribed": false}
				if !send(resp) {
					return
				}
				continue
			}
			var q model.MonitorQuery
			if err := json.Unmarshal(req.Params, &q); err != nil {
				resp.Error = &model.Error{Code: "invalid_request", Message: "invalid monitor subscription parameters"}
				send(resp)
				continue
			}
			page, changed, err := s.monitor.Read(q)
			if err != nil {
				var api *model.Error
				if !errors.As(err, &api) {
					api = &model.Error{Code: "internal_error", Message: "monitor snapshot unavailable"}
				}
				resp.Error = api
				send(resp)
				continue
			}
			if !monitorSlot {
				select {
				case s.monitorSlots <- struct{}{}:
					monitorSlot = true
				default:
					resp.Error = &model.Error{Code: "monitor_busy", Message: "at most 32 monitor subscriptions may be active"}
					send(resp)
					continue
				}
			}
			if monitoring != nil {
				monitoring()
			}
			subctx, stop := context.WithCancel(ctx)
			monitoring = stop
			resp.Result = page
			if !send(resp) {
				return
			}
			wsWG.Add(1)
			go func(page model.MonitorPage, changed <-chan struct{}) {
				defer wsWG.Done()
				for {
					select {
					case <-subctx.Done():
						return
					case <-changed:
					}
					next, ch, e := s.monitor.Read(model.MonitorQuery{Epoch: page.Epoch, After: page.Cursor})
					if e != nil {
						cancel()
						return
					}
					select {
					case <-subctx.Done():
						return
					default:
					}
					if !send(model.WireResponse{Version: 1, Subscription: req.ID, Telemetry: &next}) {
						return
					}
					page, changed = next, ch
				}
			}(page, changed)
			continue
		}
		select {
		case requests <- struct{}{}:
			// Long polling must not block the reader: it needs to notice a
			// disconnected client and multiplex other requests and events.
			wsWG.Add(1)
			go func(req model.WireRequest) {
				defer wsWG.Done()
				defer func() { <-requests }()
				resp := model.WireResponse{Version: 1, ID: req.ID}
				result, err := s.dispatch(ctx, req)
				if err != nil {
					var re *model.Error
					if errors.As(err, &re) {
						resp.Error = re
					} else {
						resp.Error = &model.Error{Code: "internal_error", Message: "backend operation failed"}
					}
				} else {
					resp.Result = result
				}
				send(resp)
			}(req)
		default:
			resp.Error = &model.Error{Code: "too_many_requests", Message: "at most eight requests may be in flight on one connection"}
			if !send(resp) {
				return
			}
		}
	}
}

func (s *Server) dispatch(ctx context.Context, r model.WireRequest) (any, error) {
	leave, e := s.registerStopOperation(r.Operation)
	if e != nil {
		return nil, e
	}
	defer leave()
	if bound, ok := ctx.Value(nodeSessionKey{}).(nodeSession); ok {
		if r.Operation == "dashboard_login_link" {
			return nil, model.Err("forbidden", "scoped node sessions cannot issue local dashboard login links")
		}
		if !bound.Local {
			return s.nodeSessionOperation(ctx, bound, r)
		}
		if _, e := s.validateNodeSession(bound); e != nil {
			return nil, e
		}
	}
	if ctx.Value(browserContextKey{}) == true && !browserAllowedOperation(r.Operation) {
		return nil, model.Err("forbidden", "this dashboard session supports inspection, reviewed lease and pool controls and reviewed operator messaging; use the CLI for other changes")
	}
	if strings.HasPrefix(r.Operation, "workspace_") {
		return s.workspaceOperation(ctx, r.Operation, r.Params)
	}
	if strings.HasPrefix(r.Operation, "mesh_operator_") {
		return s.meshOperatorOperation(ctx, r.Operation, r.Params)
	}
	if strings.HasPrefix(r.Operation, "mesh_") {
		return s.meshOperation(ctx, r.Operation, r.Params)
	}
	if r.Operation == "acquire" || r.Operation == "run" || r.Operation == "pool_mutate" || r.Operation == "budget_mutate" {
		s.admission.Lock()
		defer s.admission.Unlock()
		if s.stopping {
			return nil, model.Err("backend_stopping", "backend no longer admits work")
		}
	}
	decode := func(v any) error {
		if len(r.Params) == 0 {
			return model.Err("invalid_request", "parameters are required")
		}
		if e := json.Unmarshal(r.Params, v); e != nil {
			return model.Err("invalid_request", e.Error())
		}
		return nil
	}
	var p struct {
		ID       string        `json:"id"`
		Key      string        `json:"key"`
		TTL      time.Duration `json:"ttl_ns"`
		Offset   int64         `json:"offset"`
		After    int64         `json:"after"`
		Audience string        `json:"audience"`
		Body     string        `json:"body"`
		Lease    string        `json:"lease"`
	}
	switch r.Operation {
	case "kube_contexts", "kube_nodes":
		return s.kubernetesInventory(ctx, r.Operation, r.Params)
	case "peers_status":
		return s.peerOverview(ctx)
	case "lease_review_list", "lease_review_inspect", "lease_review_policy", "lease_review_configure", "lease_review_reply":
		return s.leaseReviewOperation(r.Operation, r.Params)
	case "ssh_account", "ssh_close", "ssh_key_status", "ssh_key_seal":
		return s.sshNative(ctx, r.Operation, r.Params)
	case "provision_runtime":
		return s.provisionRuntime(r.Params)
	case "node_open":
		return s.openNode(ctx, r.Params)
	case "node_list":
		if err := nodeMetadataParams(r.Params); err != nil {
			return nil, err
		}
		return s.nodeList()
	case "node_inspect":
		var q struct {
			Host string `json:"host"`
		}
		if err := decodeNodeRead(r.Params, &q); err != nil {
			return nil, err
		}
		return s.nodeInspect(ctx, q.Host)
	case "node_metadata":
		if err := nodeMetadataParams(r.Params); err != nil {
			return nil, err
		}
		return s.nodeMetadata()
	case "budget_list", "budget_inspect", "budget_consumers", "budget_history", "budget_lookup", "budget_mutate":
		return s.budgetOperation(r.Operation, r.Params)
	case "bulk_end":
		var q model.BulkEndRequest
		if err := decode(&q); err != nil {
			return nil, err
		}
		return s.Manager.BulkEnd(q)
	case "shutdown_status":
		return s.Manager.ShutdownStatus()
	case "lease_action":
		var q model.LeaseAction
		if err := decode(&q); err != nil {
			return nil, err
		}
		if ctx.Value(browserContextKey{}) == true {
			if q.Action != "renew" && q.Action != "release" && q.Action != "cancel" {
				return nil, model.Err("forbidden", "dashboard lease controls support only extension, release and cancellation")
			}
			if (q.Action == "release" || q.Action == "cancel") && q.Consumers == nil {
				return nil, model.Err("invalid_request", "destructive lease controls require the reviewed attached leases")
			}
		}
		return s.Manager.LeaseAction(q)
	case "message_lookup":
		var q struct {
			Key string `json:"idempotency_key"`
		}
		if err := decode(&q); err != nil {
			return nil, err
		}
		return s.Manager.LookupMessage(q.Key)
	case "message_send":
		var q manager.MessageRequest
		if err := decode(&q); err != nil {
			return nil, err
		}
		if ctx.Value(browserContextKey{}) == true && (q.Audience == "operator" || q.Audience == "" || len(q.Targets) == 0) {
			return nil, model.Err("invalid_request", "dashboard sends require at least one explicitly reviewed agent recipient")
		}
		return s.Manager.SendMessage(ctx, q)
	case "dashboard_login_link":
		return s.dashboardLoginLink(r.Params)
	case "browser_ticket":
		return s.browser.issue(s.authority, time.Now())
	case "monitor":
		var q model.MonitorQuery
		if err := decode(&q); err != nil {
			return nil, err
		}
		page, _, err := s.monitor.Read(q)
		return page, err
	case "incidents":
		var q struct {
			Lease string `json:"lease"`
		}
		if err := decode(&q); err != nil {
			return nil, err
		}
		return s.Manager.Incidents(q.Lease)
	case "usage":
		var q struct {
			ID string `json:"id"`
		}
		if err := decode(&q); err != nil {
			return nil, err
		}
		return s.Manager.Usage(q.ID)
	case "pool_lookup":
		var q struct {
			Key string `json:"idempotency_key"`
		}
		if err := decode(&q); err != nil {
			return nil, err
		}
		return boundedPoolRead(s.Manager.LookupPoolMutation(q.Key))
	case "pool_children", "pool_consumers":
		var q model.PoolTreeQuery
		if e := decode(&q); e != nil {
			return nil, e
		}
		if r.Operation == "pool_children" {
			return boundedPoolRead(s.Manager.PoolChildren(q))
		}
		return boundedPoolRead(s.Manager.PoolConsumers(q))
	case "pool_mutate":
		q, e := manager.DecodePoolMutation(r.Params)
		if e != nil {
			return nil, e
		}
		if ctx.Value(browserContextKey{}) == true {
			if err := browserPoolMutation(q); err != nil {
				return nil, err
			}
		}
		return s.Manager.MutatePool(q)
	case "database_stats", "database_check", "database_backup":
		var q struct {
			Destination string `json:"destination"`
			Full        bool   `json:"full"`
		}
		if err := decode(&q); err != nil {
			return nil, err
		}
		return s.Manager.DatabaseMaintenance(ctx, strings.TrimPrefix(r.Operation, "database_"), q.Destination, q.Full)
	case "pool_history_page":
		var q model.PoolHistoryPageRequest
		if e := decode(&q); e != nil {
			return nil, e
		}
		return boundedPoolRead(s.Manager.PoolHistoryPage(q))
	case "pool_list", "pool_inspect", "pool_history":
		var q struct {
			ID    string `json:"id"`
			All   bool   `json:"all"`
			After int64  `json:"after"`
		}
		if e := decode(&q); e != nil {
			return nil, e
		}
		if r.Operation == "pool_list" {
			return boundedPoolRead(s.Manager.Pools(q.All))
		}
		if r.Operation == "pool_history" {
			return boundedPoolRead(s.Manager.PoolHistory(q.ID, q.After))
		}
		return boundedPoolRead(s.Manager.PoolInspect(q.ID))
	case "agent_message", "agent_message_lookup":
		var q manager.AgentMessageRequest
		if err := decode(&q); err != nil {
			return nil, err
		}
		if r.Operation == "agent_message_lookup" {
			return s.Manager.LookupAgentMessage(q.ID, q.Session, q.Registration, q.Key)
		}
		return s.Manager.AgentMessage(ctx, q)
	case "agent_inbox":
		var q manager.InboxRequest
		if e := decode(&q); e != nil {
			return nil, e
		}
		return s.Manager.AgentInbox(ctx, q)
	case "agent_receipt":
		var q manager.ReceiptRequest
		if e := decode(&q); e != nil {
			return nil, e
		}
		return s.Manager.AgentReceipt(q)
	case "ports_discover":
		select {
		case s.portScan <- struct{}{}:
			defer func() { <-s.portScan }()
		default:
			return nil, model.Err("observation_busy", "socket discovery is already running")
		}
		return s.Manager.DiscoverPorts(ctx)
	case "storage_list":
		return s.Manager.StorageList()
	case "storage_export", "storage_export_preview":
		var request model.StorageExportRequest
		if err := decode(&request); err != nil {
			return nil, err
		}
		select {
		case s.storageScan <- struct{}{}:
			defer func() { <-s.storageScan }()
		default:
			return nil, model.Err("observation_busy", "a storage scan/export preparation is running; inspect an existing operation or retry after it finishes")
		}
		if r.Operation == "storage_export_preview" {
			return s.Manager.StorageExportPreview(ctx, request)
		}
		return s.Manager.StorageExportCreate(ctx, request)
	case "storage_exports":
		var request struct {
			Lease string `json:"lease"`
		}
		if err := decode(&request); err != nil {
			return nil, err
		}
		return s.Manager.StorageExports(request.Lease)
	case "storage_export_inspect", "storage_export_reconcile", "storage_export_handoff":
		var request struct {
			ID     string `json:"id"`
			Reason string `json:"reason,omitempty"`
		}
		if err := decode(&request); err != nil {
			return nil, err
		}
		if r.Operation != "storage_export_handoff" && request.Reason != "" {
			return nil, model.Err("invalid_request", "reason is only accepted for export handoff")
		}
		switch r.Operation {
		case "storage_export_inspect":
			return s.Manager.StorageExportInspect(request.ID)
		case "storage_export_reconcile":
			return s.Manager.StorageExportReconcile(ctx, request.ID)
		default:
			return s.Manager.StorageExportHandoff(request.ID, request.Reason)
		}
	case "storage_retain":
		var request struct {
			ID       string        `json:"id"`
			Revision int           `json:"revision"`
			TTL      time.Duration `json:"ttl_ns"`
			Reason   string        `json:"reason"`
		}
		if err := decode(&request); err != nil {
			return nil, err
		}
		return s.Manager.StorageRetain(request.ID, request.Revision, request.TTL, request.Reason)
	case "storage_preview", "storage_cleanup", "storage_handoff":
		var q struct {
			ID string `json:"id"`
		}
		if e := decode(&q); e != nil {
			return nil, e
		}
		switch r.Operation {
		case "storage_preview":
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			return s.Manager.StoragePreview(ctx, q.ID)
		case "storage_cleanup":
			return s.Manager.StorageCleanup(q.ID)
		case "storage_handoff":
			return s.Manager.StorageHandoff(q.ID)
		}
	case "storage_inspect":
		select {
		case s.storageScan <- struct{}{}:
			defer func() { <-s.storageScan }()
		default:
			return nil, model.Err("observation_busy", "a storage scan is already running; retry after it finishes")
		}
		var q struct {
			Path string `json:"path"`
		}
		if e := decode(&q); e != nil {
			return nil, e
		}
		if !filepath.IsAbs(q.Path) {
			return nil, model.Err("invalid_request", "storage path must be absolute")
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		result, err := observe.Scan(ctx, q.Path)
		if err != nil {
			return nil, model.Err("observation_failed", err.Error())
		}
		return result, nil
	case "node_stop_observer_context":
		if len(r.Params) > 0 && string(r.Params) != "{}" {
			return nil, model.Err("invalid_request", "observer context has no parameters")
		}
		return s.nodeStopBinding()
	case "startup_inspect":
		if len(r.Params) > 0 && string(r.Params) != "{}" {
			return nil, model.Err("invalid_request", "startup inspection has no parameters")
		}
		return s.startupInspection()
	case "status":
		status, err := s.Manager.Status()
		return s.withServerCapabilities(status), err
	case "resources":
		status, e := s.Manager.Status()
		return status.Resources, e
	case "acquire":
		var q model.Request
		if e := decode(&q); e != nil {
			return nil, e
		}
		q.Peer, q.OriginKey, q.PeerCertificate, q.PeerGrantRevision = "", "", "", 0
		return s.Manager.Acquire(q, nil)
	case "run":
		var q manager.RunRequest
		if e := decode(&q); e != nil {
			return nil, e
		}
		q.Request.Peer, q.Request.OriginKey, q.Request.PeerCertificate, q.Request.PeerGrantRevision = "", "", "", 0
		return s.Manager.Acquire(q.Request, &q)
	case "inspect", "release", "cancel", "cancel_request", "reconcile", "renew", "output", "events", "message":
		if e := decode(&p); e != nil {
			return nil, e
		}
		switch r.Operation {
		case "inspect":
			return s.Manager.Inspect(p.ID)
		case "reconcile":
			return s.Manager.Reconcile(p.ID)
		case "release":
			return s.Manager.End(p.ID, "released")
		case "cancel":
			return s.Manager.End(p.ID, "cancelled")
		case "cancel_request":
			return map[string]bool{"cancelled": true}, s.Manager.CancelRequest(p.Key)
		case "renew":
			return s.Manager.Renew(p.ID, p.TTL)
		case "output":
			return s.Manager.Output(p.ID, p.Offset)
		case "events":
			return s.Manager.Events(p.After)
		case "message":
			return s.Manager.Message(ctx, p.Audience, p.Body, p.Lease)
		}
	case "agents":
		return s.Manager.Contacts()
	case "register":
		var q agents.Contact
		if e := decode(&q); e != nil {
			return nil, e
		}
		return s.Manager.RegisterContact(q)
	case "messages":
		return s.Manager.Messages()
	case "shutdown":
		var q model.ShutdownRequest
		if len(r.Params) > 0 && string(r.Params) != "null" {
			if err := decode(&q); err != nil {
				return nil, err
			}
		}
		return s.requestShutdown(q)
	}
	return nil, model.Err("unknown_operation", "unsupported operation")
}
