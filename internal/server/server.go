package server

import (
	"context"
	"crypto/subtle"
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
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/observe"
)

type Server struct {
	Manager     *manager.Manager
	home, token string
	stop        context.CancelFunc
	admission   sync.Mutex
	stopping    bool
	storageScan chan struct{}
	portScan    chan struct{}
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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &Server{Manager: m, home: home, token: local.ID() + local.ID(), stop: cancel}
	s.storageScan = make(chan struct{}, 1)
	s.portScan = make(chan struct{}, 1)
	ep := local.Endpoint{URL: "ws://" + listener.Addr().String() + "/v1/ws", Token: s.token, PID: os.Getpid()}
	if e = local.AtomicJSON(filepath.Join(home, "endpoint.json"), ep); e != nil {
		return e
	}
	defer os.Remove(filepath.Join(home, "endpoint.json"))
	m.Start()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ws", s.ws)
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
	if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")), []byte(s.token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if host, _, e := net.SplitHostPort(r.Host); e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		http.Error(w, "invalid host", http.StatusForbidden)
		return
	}
	c, e := websocket.Accept(w, r, nil)
	if e != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(65536)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
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
	var subscription context.CancelFunc
	requests := make(chan struct{}, 8)
	defer func() {
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
		if req.Operation == "subscribe" {
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
	if r.Operation == "acquire" || r.Operation == "run" || r.Operation == "pool_mutate" || r.Operation == "shutdown" {
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
	case "pool_mutate":
		var q model.PoolMutation
		if e := decode(&q); e != nil {
			return nil, e
		}
		return s.Manager.MutatePool(q)
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
			return s.Manager.Pools(q.All)
		}
		if r.Operation == "pool_history" {
			return s.Manager.PoolHistory(q.ID, q.After)
		}
		return s.Manager.PoolInspect(q.ID)
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
		return s.Manager.Status()
	case "resources":
		status, e := s.Manager.Status()
		return status.Resources, e
	case "acquire":
		var q model.Request
		if e := decode(&q); e != nil {
			return nil, e
		}
		return s.Manager.Acquire(q, nil)
	case "run":
		var q manager.RunRequest
		if e := decode(&q); e != nil {
			return nil, e
		}
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
		if s.Manager.Active() {
			return nil, model.Err("active_leases", "release leases and finish supervised work before stopping the daemon")
		}
		s.stopping = true
		go func() { time.Sleep(100 * time.Millisecond); s.stop() }()
		return map[string]bool{"stopping": true}, nil
	}
	return nil, model.Err("unknown_operation", "unsupported operation")
}
