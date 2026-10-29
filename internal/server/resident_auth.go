package server

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/model"
	"github.com/coder/websocket"
)

type residentCallerContextKey struct{}

// authenticatedCaller is an opaque, original ingress identity. Possessing a
// copied value/proof does not register it, and credentials alone cannot activate it.
type authenticatedCaller struct {
	registered atomic.Bool
	owner      *Server
	proof      *ingressProof
}

type ingressProof struct {
	generation string
	directory  *database.Directory
	admission  browserAdmission
	nativeNode bool
}

type residentIngress struct {
	proof       *ingressProof
	connection  *websocket.Conn
	ctx         context.Context
	bound       *nodeSession
	active      bool
	negotiating bool
	negotiated  bool
}

type residentCallers struct {
	sync.Mutex
	entries map[*authenticatedCaller]*residentIngress
}

type ingressFailure struct {
	status  int
	message string
}

func (e *ingressFailure) Error() string { return e.message }

// authenticateLocalIngress recognizes credentials but creates no registry entry.
// The WS acceptor must still accept the original connection, complete required
// node negotiation and activate it. Abandoned HTTP handshakes retain no proof.
func (s *Server) authenticateLocalIngress(r *http.Request, now time.Time) (*authenticatedCaller, error) {
	if !s.validHost(r.Host) {
		return nil, &ingressFailure{http.StatusForbidden, "invalid host"}
	}
	admission, ok := s.authenticateAdmission(r, now)
	if !ok {
		return nil, &ingressFailure{http.StatusUnauthorized, "unauthorized"}
	}
	native := r.Header.Values("Arm-Node-Context")
	if len(native) > 0 && (admission.Browser || len(native) != 1 || native[0] != "v1") {
		return nil, &ingressFailure{http.StatusForbidden, "invalid native node negotiation"}
	}
	if s.generation == "" || s.nodeStopDirectory == nil || !validIngressDirectory(s.nodeStopDirectory) {
		return nil, &ingressFailure{http.StatusServiceUnavailable, "resident ingress unavailable"}
	}
	if admission.Node != nil {
		bound := *admission.Node
		admission.Node = &bound
	}
	return &authenticatedCaller{owner: s, proof: &ingressProof{generation: s.generation, directory: s.nodeStopDirectory, admission: admission, nativeNode: len(native) == 1}}, nil
}

// registerCallerIngress is called only after websocket.Accept succeeded. Pending
// ingress has no operation authority, including while native negotiation blocks.
func (s *Server) registerCallerIngress(caller *authenticatedCaller, c *websocket.Conn, ctx context.Context) error {
	if caller == nil || caller.owner != s || caller.proof == nil || c == nil || ctx == nil || ctx.Err() != nil {
		return model.Err("forbidden", "original resident ingress required")
	}
	p := caller.proof
	if p.generation != s.generation || p.directory != s.nodeStopDirectory || p.directory == nil || !validIngressDirectory(p.directory) {
		return model.Err("forbidden", "resident ingress identity changed")
	}
	ingress := &residentIngress{proof: p, connection: c, ctx: ctx}
	if p.admission.Node != nil {
		b := *p.admission.Node
		ingress.bound = &b
	}
	s.callers.Lock()
	defer s.callers.Unlock()
	if s.callers.entries == nil {
		s.callers.entries = make(map[*authenticatedCaller]*residentIngress)
	}
	if _, exists := s.callers.entries[caller]; exists {
		return model.Err("forbidden", "ingress already registered")
	}
	if !caller.registered.CompareAndSwap(false, true) {
		return model.Err("forbidden", "ingress identity already used")
	}
	s.callers.entries[caller] = ingress
	return nil
}

// negotiateCallerNode preserves the original first-frame reader and sole ACK
// writer. The caller remains pending until the WS acceptor explicitly activates it.
func (s *Server) negotiateCallerNode(ctx context.Context, caller *authenticatedCaller, c *websocket.Conn) (nodeSession, error) {
	s.callers.Lock()
	ingress := s.callers.entries[caller]
	valid := caller != nil && caller.owner == s && ingress != nil && ingress.proof == caller.proof && ingress.connection == c && !ingress.active && !ingress.negotiating && !ingress.negotiated && ingress.proof.nativeNode
	if valid {
		ingress.negotiating = true
	}
	s.callers.Unlock()
	if !valid {
		return nodeSession{}, model.Err("forbidden", "pending native ingress required")
	}
	bound, err := s.negotiateNativeNode(ctx, c)
	if err != nil {
		return nodeSession{}, err
	}
	s.callers.Lock()
	defer s.callers.Unlock()
	if s.callers.entries[caller] != ingress || ingress.ctx.Err() != nil {
		return nodeSession{}, model.Err("forbidden", "ingress retired during negotiation")
	}
	ingress.bound = &bound
	ingress.negotiating = false
	ingress.negotiated = true
	return bound, nil
}

func (s *Server) activateCallerIngress(caller *authenticatedCaller) error {
	s.callers.Lock()
	ingress := s.callers.entries[caller]
	if caller == nil || caller.owner != s || ingress == nil || ingress.proof != caller.proof || ingress.active || ingress.proof.nativeNode && !ingress.negotiated {
		s.callers.Unlock()
		return model.Err("forbidden", "accepted and negotiated ingress required")
	}
	s.callers.Unlock()
	if err := s.validateIngress(ingress); err != nil {
		return err
	}
	s.callers.Lock()
	defer s.callers.Unlock()
	if s.callers.entries[caller] != ingress || ingress.ctx.Err() != nil {
		return model.Err("forbidden", "ingress retired")
	}
	ingress.active = true
	return nil
}

func (s *Server) retireCallerIngress(caller *authenticatedCaller) {
	if caller != nil {
		caller.registered.Store(true)
	}
	s.callers.Lock()
	delete(s.callers.entries, caller)
	s.callers.Unlock()
}

// validateIngress performs no domain effect and holds no registry lock across
// directory/node inspection or peer I/O. Node handlers retain their own current
// pre/post-read authorization checks after this ingress check.
func (s *Server) validateIngress(ingress *residentIngress) error {
	p := ingress.proof
	if ingress.ctx.Err() != nil {
		return ingress.ctx.Err()
	}
	if p.generation != s.generation || p.directory != s.nodeStopDirectory || p.directory == nil || !validIngressDirectory(p.directory) {
		return model.Err("forbidden", "resident ingress identity changed")
	}
	if p.admission.Browser && !time.Now().Before(p.admission.Expires) {
		return model.Err("forbidden", "dashboard session expired")
	}
	if ingress.bound != nil {
		if _, err := s.validateNodeSession(*ingress.bound); err != nil {
			return model.Err("node_context_changed", "selected node authority changed; reopen from Mesh")
		}
	}
	return nil
}

// callerContext uses only the original ingress context and frozen admission.
// There is no context/role argument by which another entry can imply native
// authority, remove cancellation or substitute a selected destination.
func (s *Server) callerContext(caller *authenticatedCaller) (context.Context, error) {
	s.callers.Lock()
	ingress := s.callers.entries[caller]
	valid := caller != nil && caller.owner == s && ingress != nil && ingress.active && ingress.proof == caller.proof
	s.callers.Unlock()
	if !valid {
		return nil, model.Err("forbidden", "active original resident caller required")
	}
	if err := s.validateIngress(ingress); err != nil {
		return nil, err
	}
	s.callers.Lock()
	current := s.callers.entries[caller] == ingress && ingress.active && ingress.ctx.Err() == nil
	s.callers.Unlock()
	if !current {
		return nil, model.Err("forbidden", "ingress retired")
	}
	ctx := ingress.proof.context(ingress.ctx, ingress.bound)
	return context.WithValue(ctx, residentCallerContextKey{}, caller), nil
}

// Restore only credential-derived values, including their absence, before any
// first-frame binding as well as before ordinary dispatch. This creates no
// caller/registry authority and cannot activate a pending connection.
func (p *ingressProof) context(ctx context.Context, bound *nodeSession) context.Context {
	ctx = context.WithValue(ctx, residentCallerContextKey{}, struct{}{})
	ctx = context.WithValue(ctx, browserContextKey{}, p.admission.Browser)
	ctx = context.WithValue(ctx, browserExpiryKey{}, p.admission.Expires)
	ctx = context.WithValue(ctx, browserPrincipalKey{}, p.admission.Principal)
	if bound != nil {
		return context.WithValue(ctx, nodeSessionKey{}, *bound)
	}
	return context.WithValue(ctx, nodeSessionKey{}, struct{}{})
}

// Validate both the original retained descriptor and its current directory name.
func validIngressDirectory(d *database.Directory) bool {
	if d == nil || d.FD == nil {
		return false
	}
	if _, err := d.FD.Stat(); err != nil {
		return false
	}
	return d.Validate() == nil
}
