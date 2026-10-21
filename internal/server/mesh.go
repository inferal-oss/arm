package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"
)

// Mesh is a separate transport and closed RPC surface. It never authenticates a
// peer by forwarding the privileged local bearer token or browser credential.
type meshService struct {
	owner       *Server
	store       *mesh.Store
	http        *http.Server
	listener    net.Listener
	mu          sync.Mutex
	connections map[net.Conn]struct{}
	slots       chan struct{}
	peerSlots   sync.Map
}

func (s *Server) loadMesh() error {
	store, e := mesh.Open(s.home)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if e := s.startMesh(store); e != nil {
		store.Close()
		return e
	}
	return nil
}
func (s *Server) startMesh(store *mesh.Store) error {
	s.meshMu.Lock()
	defer s.meshMu.Unlock()
	if s.mesh != nil {
		return fmt.Errorf("mesh already running")
	}
	if e := s.meshAdmission(); e != nil {
		return e
	}
	status, e := s.Manager.Status()
	if e != nil {
		return e
	}
	if store.Config().Identity.Host != status.Host {
		return fmt.Errorf("mesh identity does not match this host ledger")
	}
	listener, e := net.Listen("tcp", store.Config().Listen)
	if e != nil {
		return e
	}
	if e = s.Manager.TrackMeshInfrastructure(listener.Addr()); e != nil {
		listener.Close()
		return e
	}
	service := &meshService{owner: s, store: store, listener: listener, connections: map[net.Conn]struct{}{}, slots: make(chan struct{}, 32)}
	service.http = &http.Server{Handler: http.HandlerFunc(service.serve), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192, ConnState: func(c net.Conn, state http.ConnState) {
		service.mu.Lock()
		defer service.mu.Unlock()
		if state == http.StateNew {
			service.connections[c] = struct{}{}
		}
		if state == http.StateClosed || state == http.StateHijacked {
			delete(service.connections, c)
		}
	}}
	store.OnChange(service.revoke)
	s.mesh = service
	go func() {
		_ = service.http.Serve(tls.NewListener(&meshLimitedListener{Listener: listener, slots: make(chan struct{}, 64)}, store.ServerTLS()))
	}()
	return nil
}
func (m *meshService) revoke(host string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for connection := range m.connections {
		if c, ok := connection.(*tls.Conn); ok {
			state := c.ConnectionState()
			if len(state.PeerCertificates) == 1 {
				cert := state.PeerCertificates[0]
				if len(cert.DNSNames) == 1 && cert.DNSNames[0] == mesh.SAN(host) {
					_ = connection.Close()
				}
			}
		}
	}
}

func (s *Server) closeMesh() {
	s.meshMu.Lock()
	defer s.meshMu.Unlock()
	if s.mesh != nil {
		_ = s.mesh.http.Close()
		closeErr := s.mesh.listener.Close()
		if closeErr == nil || errors.Is(closeErr, net.ErrClosed) {
			s.Manager.RemoveMeshInfrastructure()
		}
		_ = s.mesh.store.Close()
		s.mesh = nil
	}
}
func (m *meshService) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	respond := func(value any, e error) {
		response := struct {
			Result any          `json:"result,omitempty"`
			Error  *model.Error `json:"error,omitempty"`
		}{Result: value}
		if e != nil {
			response.Result = nil
			var api *model.Error
			if errors.As(e, &api) {
				response.Error = api
			} else {
				response.Error = &model.Error{Code: "mesh_error", Message: "mesh operation failed; inspect target host logs"}
			}
		}
		encoded, encodeErr := json.Marshal(response)
		if encodeErr != nil || len(encoded) > 4<<20 {
			encoded = []byte(`{"error":{"code":"response_too_large","message":"response exceeds mesh bound; narrow the query"}}`)
		}
		_, _ = w.Write(encoded)
	}
	if r.Method != "POST" || r.URL.Path != "/v1/mesh" || r.TLS == nil || len(r.TLS.PeerCertificates) != 1 || r.Header.Get("Origin") != "" {
		respond(nil, model.Err("forbidden", "not a mesh request"))
		return
	}
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	default:
		respond(nil, model.Err("busy", "mesh request limit reached"))
		return
	}
	peer, e := m.store.PeerByCert(r.TLS.PeerCertificates[0].Raw)
	if e != nil {
		respond(nil, model.Err("forbidden", "peer no longer trusted"))
		return
	}
	slotValue, _ := m.peerSlots.LoadOrStore(peer.Identity.Host, make(chan struct{}, 4))
	peerSlot := slotValue.(chan struct{})
	select {
	case peerSlot <- struct{}{}:
		defer func() { <-peerSlot }()
	default:
		respond(nil, model.Err("busy", "peer request limit reached"))
		return
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, 65537))
	if e != nil || len(raw) > 65536 {
		respond(nil, model.Err("invalid_request", "mesh request exceeds 64KiB"))
		return
	}

	var request mesh.Request
	if e = mesh.Decode(raw, &request); e != nil {
		respond(nil, e)
		return
	}
	identity := m.store.Config().Identity
	if request.Version != 1 || request.Host != identity.Host {
		respond(nil, model.Err("wrong_host", "explicit stable target host/version mismatch"))
		return
	}
	if request.Operation == "hello" {
		var params struct {
			Pair string `json:"pair"`
		}
		if e = mesh.Decode(request.Params, &params); e == nil && peer.State == "pending" {
			e = m.store.Prove(peer.Identity.Host, params.Pair)
		}
		respond(identity, e)
		return
	}
	if mesh.Mutating(request.Operation) && !mesh.ValidKey(request.Key) {
		respond(nil, model.Err("invalid_request", "remote mutation needs an exact-retry key up to 128 bytes"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	value, e := m.store.WithPeer(peer.Identity.Host, func(current mesh.Peer) (any, error) { return m.dispatch(ctx, current, request) })
	respond(value, e)
}
func (m *meshService) dispatch(ctx context.Context, peer mesh.Peer, request mesh.Request) (any, error) {
	grant := peer.Grant
	require := func(permission string) error {
		if !grant.Has(permission) {
			return model.Err("forbidden", "peer lacks explicit "+permission+" grant")
		}
		return nil
	}
	switch request.Operation {
	case "status", "resources":
		if e := require("observe-host"); e != nil {
			return nil, e
		}
		status, e := m.owner.Manager.Status()
		if e != nil {
			return nil, e
		}
		resources := make([]map[string]any, 0, len(status.Resources))
		for _, resource := range status.Resources {
			resources = append(resources, map[string]any{"id": resource.ID, "host": resource.Host, "kind": resource.Kind, "unit": resource.Unit, "budget_total": resource.BudgetTotal, "reserved": resource.Reserved, "available": resource.Available})
		}
		if request.Operation == "resources" {
			return resources, nil
		}
		leases := []model.Lease{}
		if grant.Has("observe-own") || grant.Has("observe-all") {
			for _, lease := range status.Leases {
				if grant.Has("observe-all") || lease.Request.Peer == peer.Identity.Host {
					leases = append(leases, lease)
				}
			}
		}
		var memory any
		if status.Memory != nil {
			b := status.Memory
			memory = map[string]any{"observation_at": b.Observation.At, "quality": b.Observation.Quality, "total_bytes": b.Observation.Total, "estimated_available_bytes": b.Observation.Available, "capacity_bytes": b.Capacity, "reserved_bytes": b.Reserved, "available_bytes": b.Available, "ceiling_bytes": b.Ceiling, "deficit_bytes": b.Deficit, "stale": b.Stale, "age_ns": b.Age, "stale_after_ns": b.StaleAfter}
		}
		return map[string]any{"host": status.Host, "capacity": status.Capacity, "reserved": status.Reserved, "leases": leases, "resources": resources, "capabilities": status.Capabilities, "memory": memory, "reported_at": time.Now().UTC()}, nil
	case "contacts":
		if e := require("contact-read"); e != nil {
			return nil, e
		}
		contacts, e := m.owner.Manager.Contacts()
		if e != nil {
			return nil, e
		}
		allowed := contacts[:0]
		for _, contact := range contacts {
			if slices.Contains(grant.Contacts, contact.ID) {
				allowed = append(allowed, contact)
			}
		}
		return allowed, nil
	case "acquire", "run":
		if e := require("lease-own"); e != nil {
			return nil, e
		}
		var run manager.RunRequest
		var q model.Request
		var e error
		if request.Operation == "run" {
			if e = require("run"); e != nil {
				return nil, e
			}
			e = mesh.Decode(request.Params, &run)
			q = run.Request
		} else {
			e = mesh.Decode(request.Params, &q)
		}
		if e != nil {
			return nil, e
		}
		if q.Host != "" && q.Host != request.Host {
			return nil, model.Err("wrong_host", "claims must target this authority")
		}
		q.Host = request.Host
		q.Key = request.Key
		q.PeerCertificate = peer.Identity.Fingerprint
		q.PeerGrantRevision = peer.Revision
		if request.Operation == "run" {
			return m.owner.Manager.AcquirePeer(peer.Identity.Host, q, &run, grant.Limits, grant.MaxTTL)
		}
		return m.owner.Manager.AcquirePeer(peer.Identity.Host, q, nil, grant.Limits, grant.MaxTTL)
	case "inspect", "output", "release", "cancel", "renew":
		var q struct {
			ID     string        `json:"id"`
			TTL    time.Duration `json:"ttl_ns,omitempty"`
			Offset int64         `json:"offset,omitempty"`
		}
		if e := mesh.Decode(request.Params, &q); e != nil {
			return nil, e
		}
		permission := "observe-own"
		if mesh.Mutating(request.Operation) {
			permission = "lease-own"
		}
		if e := require(permission); e != nil {
			return nil, e
		}
		l, e := m.owner.Manager.PeerLease(peer.Identity.Host, q.ID)
		if e != nil {
			return nil, e
		}
		if request.Operation == "inspect" {
			return l, nil
		}
		if request.Operation == "output" {
			if e = require("run"); e != nil {
				return nil, e
			}
			return m.owner.Manager.Output(l.ID, q.Offset)
		}
		canonical, _ := json.Marshal(q)
		return m.owner.Manager.PeerOnceAuthenticated(peer.Identity.Host, request.Key, request.Operation, canonical, peer.Identity.Fingerprint, peer.Revision, func() (any, error) {
			switch request.Operation {
			case "release":
				return m.owner.Manager.End(l.ID, "released")
			case "cancel":
				return m.owner.Manager.End(l.ID, "cancelled")
			default:
				return m.owner.Manager.PeerRenew(peer.Identity.Host, l.ID, q.TTL, grant.Limits, grant.MaxTTL, grant.Has("run"))
			}
		})
	case "message_lookup":
		if e := require("message"); e != nil {
			return nil, e
		}
		var q struct {
			Key string `json:"idempotency_key"`
		}
		if e := mesh.Decode(request.Params, &q); e != nil {
			return nil, e
		}
		if !mesh.ValidKey(q.Key) {
			return nil, model.Err("invalid_request", "bounded original message key required")
		}
		lookup, e := m.owner.Manager.LookupMessage(mesh.Key(peer.Identity.Host, q.Key))
		if e != nil {
			return nil, e
		}
		if lookup.Found && lookup.Message != nil {
			msg := lookup.Message
			if msg.OriginHost != peer.Identity.Host || (msg.Audience == "operator" && !grant.OperatorInbox) || (msg.Audience != "operator" && !slices.Contains(grant.Contacts, msg.Audience)) {
				return nil, model.Err("forbidden", "recorded message audience is outside current peer scope")
			}
		}
		return lookup, nil
	case "message":
		if e := require("message"); e != nil {
			return nil, e
		}
		var q manager.MessageRequest
		if e := mesh.Decode(request.Params, &q); e != nil {
			return nil, e
		}
		if q.Lease != "" || q.ReplyTo != "" {
			return nil, model.Err("invalid_request", "initial peering message cannot reference unrelated local lease/thread")
		}
		if q.Audience == "broadcast" {
			return nil, model.Err("forbidden", "peer broadcast is not enabled")
		}
		if q.Audience == "operator" {
			if !grant.OperatorInbox || len(q.Targets) != 0 {
				return nil, model.Err("forbidden", "operator inbox is not granted")
			}
		} else {
			if !slices.Contains(grant.Contacts, q.Audience) || len(q.Targets) != 1 || q.Targets[0].ID != q.Audience {
				return nil, model.Err("forbidden", "exact recipient is outside peer contact grant")
			}
		}
		q.Peer = peer.Identity.Host
		q.OriginKey = request.Key
		q.PeerCertificate = peer.Identity.Fingerprint
		q.PeerGrantRevision = peer.Revision
		q.Key = mesh.Key(peer.Identity.Host, request.Key)
		return m.owner.Manager.SendMessage(ctx, q)
	default:
		return nil, model.Err("forbidden", "operation is outside the mesh allowlist")
	}
}
func (s *Server) meshOperation(ctx context.Context, operation string, raw json.RawMessage) (any, error) {
	if operation != "mesh_dispatch" {
		s.meshAdminMu.Lock()
		defer s.meshAdminMu.Unlock()
	}
	s.meshMu.Lock()
	service := s.mesh
	s.meshMu.Unlock()
	if operation == "mesh_init" || operation == "mesh_renew" {
		if e := s.meshAdmission(); e != nil {
			return nil, e
		}
	}
	if operation == "mesh_recovery_identity" {
		store, e := mesh.OpenRecovery(s.home)
		if e != nil {
			return nil, e
		}
		defer store.Close()
		status, e := s.Manager.Status()
		if e != nil {
			return nil, e
		}
		if store.Config().Identity.Host != status.Host {
			return nil, model.Err("wrong_host", "mesh identity differs from ledger")
		}
		return store.Config(), nil
	}
	if operation == "mesh_renew" {
		var q struct{ Reason string }
		if e := mesh.Decode(raw, &q); e != nil {
			return nil, e
		}
		if q.Reason == "" || len(q.Reason) > 4096 {
			return nil, model.Err("invalid_request", "renewal reason required")
		}
		s.closeMesh()
		store, e := mesh.OpenRecovery(s.home)
		if e != nil {
			return nil, e
		}
		defer store.Close()
		status, e := s.Manager.Status()
		if e != nil {
			return nil, e
		}
		if store.Config().Identity.Host != status.Host {
			return nil, model.Err("wrong_host", "mesh identity differs from ledger")
		}
		identity, e := store.Renew(q.Reason)
		if e != nil {
			return nil, e
		}
		fresh, e := mesh.Open(s.home)
		if e != nil {
			return nil, e
		}
		if e = s.startMesh(fresh); e != nil {
			fresh.Close()
			return identity, e
		}
		return identity, nil
	}
	if operation == "mesh_init" {
		var q struct{ Name, Listen, Address string }
		if e := mesh.Decode(raw, &q); e != nil {
			return nil, e
		}
		if service != nil {
			return nil, model.Err("already_initialized", "mesh already initialized")
		}
		status, e := s.Manager.Status()
		if e != nil {
			return nil, e
		}
		store, e := mesh.Init(s.home, status.Host, q.Name, q.Listen, q.Address)
		if e != nil {
			return nil, e
		}
		if e = s.startMesh(store); e != nil {
			store.Close()
			return nil, e
		}
		return store.Config().Identity, nil
	}
	if service == nil {
		return nil, model.Err("mesh_unavailable", "mesh is disabled; use mesh init explicitly")
	}
	switch operation {
	case "mesh_identity":
		return service.store.Config().Identity, nil
	case "mesh_peers":
		return service.store.Config().Peers, nil
	case "mesh_pair":
		var q struct {
			Identity       mesh.Identity
			Grant          mesh.Grant
			Pair, Reason   string
			ReplaceRevoked bool
		}
		if e := mesh.Decode(raw, &q); e != nil {
			return nil, e
		}
		return service.store.PairReviewed(q.Identity, q.Grant, q.Pair, q.Reason, q.ReplaceRevoked)
	case "mesh_activate", "mesh_revoke":
		var q struct{ Host, Pair, Reason string }
		if e := mesh.Decode(raw, &q); e != nil {
			return nil, e
		}
		if operation == "mesh_revoke" {
			return nil, service.store.Revoke(q.Host, q.Reason)
		}
		if e := service.store.Activate(q.Host, q.Pair); e != nil {
			return nil, e
		}
		return map[string]any{"activated": q.Host, "pairing_key": q.Pair}, nil
	case "mesh_prove":
		var q struct{ Host, Pair string }
		if e := mesh.Decode(raw, &q); e != nil {
			return nil, e
		}
		data, e := json.Marshal(map[string]string{"pair": q.Pair})
		if e != nil {
			return nil, e
		}
		result, e := service.store.Call(ctx, q.Host, mesh.Request{Operation: "hello", Params: data})
		if e != nil {
			return nil, e
		}
		peer, e := service.store.Resolve(q.Host)
		if e != nil {
			return nil, e
		}
		if e = service.store.Prove(peer.Identity.Host, q.Pair); e != nil {
			return nil, e
		}
		return result, nil
	case "mesh_dispatch":
		var q struct {
			Host    string
			Request mesh.Request
		}
		if e := mesh.Decode(raw, &q); e != nil {
			return nil, e
		}
		peer, e := service.store.Resolve(q.Host)
		if e != nil {
			return nil, e
		}
		if q.Request.Operation == "acquire" || q.Request.Operation == "run" {
			var params map[string]json.RawMessage
			if e = mesh.Decode(q.Request.Params, &params); e != nil {
				return nil, e
			}
			hostJSON, _ := json.Marshal(peer.Identity.Host)
			// RunRequest embeds Request, so the claim host is a flat field for
			// both acquisition and supervised execution.
			params["host"] = hostJSON
			q.Request.Params, _ = json.Marshal(params)
		}
		return service.store.Call(ctx, q.Host, q.Request)
	}
	return nil, model.Err("forbidden", "unknown local mesh operation")
}

// Bound accepted sockets before TLS parsing. Closing any layer releases its slot.
type meshLimitedListener struct {
	net.Listener
	slots chan struct{}
}
type meshLimitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *meshLimitedConn) Close() error { e := c.Conn.Close(); c.once.Do(c.release); return e }
func (l *meshLimitedListener) Accept() (net.Conn, error) {
	for {
		c, e := l.Listener.Accept()
		if e != nil {
			return nil, e
		}
		select {
		case l.slots <- struct{}{}:
			return &meshLimitedConn{Conn: c, release: func() { <-l.slots }}, nil
		default:
			_ = c.Close()
		}
	}
}

func (s *Server) meshAdmission() error {
	s.lifecycleMu.Lock()
	closing := s.lifecycleClosing
	s.lifecycleMu.Unlock()
	if closing || (s.lifecycleContext != nil && s.lifecycleContext.Err() != nil) {
		return model.Err("backend_stopping", "mesh setup unavailable during shutdown")
	}
	status, e := s.Manager.Status()
	if e != nil {
		return e
	}
	if status.Draining {
		return model.Err("backend_stopping", "mesh setup unavailable during drain")
	}
	return nil
}
