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

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/observe"
)

type Server struct {
	meshOperatorMu   sync.Mutex
	meshOperator     *meshops.Service
	nodeMu           sync.Mutex
	nodeCache        map[string]cachedNodeRead
	nodeSlots        chan struct{}
	nodeInFlight     map[string]bool
	meshMu           sync.Mutex
	meshAdminMu      sync.Mutex
	mesh             *meshService
	meshPending      *preparedMeshSetup
	Manager          *manager.Manager
	home, token      string
	stop             context.CancelFunc
	admission        sync.Mutex
	stopping         bool
	storageScan      chan struct{}
	portScan         chan struct{}
	monitor          *observe.Monitor
	monitorSlots     chan struct{}
	authority        string
	browser          browserCredentials
	lifecycleContext context.Context
	shutdownWorker   sync.Once
	shutdownWG       sync.WaitGroup
	lifecycleMu      sync.Mutex
	lifecycleClosing bool
}

func Serve(ctx context.Context, home, address string, capacity model.Resources) error {
	if e := local.Prepare(home); e != nil {
		return e
	}
	lock, e := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return fmt.Errorf("another ARM authority owns this state directory: %w", e)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
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
	defer m.Close()
	listener, e := net.Listen("tcp", address)
	if e != nil {
		return e
	}
	defer listener.Close()
	if e = m.TrackInfrastructure(listener.Addr()); e != nil {
		return e
	}
	if e = saveMeshStartupPolicy(home, address, capacity); e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &Server{Manager: m, home: home, token: local.ID() + local.ID(), stop: cancel}
	s.lifecycleContext = ctx
	// A mesh configuration failure never disables the authenticated local authority.
	if meshErr := s.loadMesh(); meshErr != nil {
		fmt.Fprintln(os.Stderr, "ARM mesh unavailable:", meshErr)
	}
	defer s.closeMesh()
	defer s.closeMeshOperator()
	defer func() {
		s.lifecycleMu.Lock()
		s.lifecycleClosing = true
		cancel()
		s.lifecycleMu.Unlock()
		s.shutdownWG.Wait()
	}()
	s.authority = listener.Addr().String()
	s.storageScan = make(chan struct{}, 1)
	s.portScan = make(chan struct{}, 1)
	initial, e := m.Status()
	if e != nil {
		return e
	}
	s.monitor = observe.NewMonitor(initial.Host, func() model.ResourceMeasurement { return m.Observation("disk") })
	s.monitorSlots = make(chan struct{}, 32)
	monitorCtx, monitorStop := context.WithCancel(ctx)
	monitorDone := make(chan struct{})
	go func() { defer close(monitorDone); s.monitor.Run(monitorCtx) }()
	defer func() { monitorStop(); <-monitorDone }()
	ep := local.Endpoint{URL: "ws://" + listener.Addr().String() + "/v1/ws", Token: s.token, PID: os.Getpid()}
	if e = local.AtomicJSON(filepath.Join(home, "endpoint.json"), ep); e != nil {
		return e
	}
	defer os.Remove(filepath.Join(home, "endpoint.json"))
	m.Start()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ws", s.ws)
	s.registerBrowser(mux)
	h := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- h.Serve(listener) }()
	select {
	case e = <-done:
		if !errors.Is(e, http.ErrServerClosed) {
			return e
		}
	case <-ctx.Done():
	}
	shutdownCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	return h.Shutdown(shutdownCtx)
}

func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
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
	defer cancel()
	if len(nativeNode) == 1 {
		bound, err := s.negotiateNativeNode(ctx, c)
		if err != nil {
			return
		}
		ctx = context.WithValue(ctx, nodeSessionKey{}, bound)
		go s.watchNodeSession(ctx, cancel, bound)
	}
	if browser {
		ctx = context.WithValue(ctx, browserContextKey{}, true)
		ctx = context.WithValue(ctx, browserExpiryKey{}, expires)
		ctx = context.WithValue(ctx, browserPrincipalKey{}, admission.Principal)
		if bound := admission.Node; bound != nil {
			ctx = context.WithValue(ctx, nodeSessionKey{}, *bound)
			if _, e := s.validateNodeSession(*bound); e != nil {
				return
			}
			go s.watchNodeSession(ctx, cancel, *bound)
		}
		timer := time.AfterFunc(time.Until(expires), cancel)
		defer timer.Stop()
	}
	out := make(chan model.WireResponse, 64)
	go func() {
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
			go func() {
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
			go func(after int64) {
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
			go func(page model.MonitorPage, changed <-chan struct{}) {
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
			go func(req model.WireRequest) {
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
	if bound, ok := ctx.Value(nodeSessionKey{}).(nodeSession); ok {
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
		var q model.PoolMutation
		if e := decode(&q); e != nil {
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
