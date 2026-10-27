package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/workspace"
	wire "getarm.org/internal/workspacetransport"
)

func (m *meshService) workspaceOuter(ctx context.Context, raw []byte) (wire.Outer, error) {
	peer, e := m.store.PeerByCert(raw)
	if e != nil {
		return wire.Outer{}, e
	}
	local := m.store.Config().Identity
	var result wire.Outer
	_, e = m.store.WithPeer(peer.Identity.Host, func(p mesh.Peer) (any, error) {
		if p.Identity.Fingerprint != peer.Identity.Fingerprint || !p.Proven {
			return nil, model.Err("forbidden", "workspace transport requires the exact active proven direct pair")
		}
		result = wire.Outer{Source: p.Identity.Host, Target: local.Host, Pair: p.Pair, Revision: p.Revision, Certificate: p.Identity.Fingerprint, Expires: minStamp(p.Identity.Expires, local.Expires)}
		return true, nil
	})
	return result, e
}
func minStamp(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func (m *meshService) recheckWorkspaceOuter(expected wire.Outer) error {
	_, e := m.store.WithPeer(expected.Source, func(p mesh.Peer) (any, error) {
		if !p.Proven || p.Pair != expected.Pair || p.Revision != expected.Revision || p.Identity.Fingerprint != expected.Certificate {
			return nil, model.Err("forbidden", "direct workspace transport enrollment changed")
		}
		return true, nil
	})
	return e
}
func (m *meshService) workspaceAdmission(ctx context.Context, outer wire.Outer, o wire.Open) (wire.Admission, error) {
	if e := m.recheckWorkspaceOuter(outer); e != nil {
		return wire.Admission{}, e
	}
	permit, e := m.owner.Manager.WorkspaceTransport(ctx, o.Workspace, o.Generation, o.Source, o.Target, o.Class, o.Consent)
	if e != nil {
		return wire.Admission{}, e
	}
	if permit.Local.Host != outer.Target || permit.Remote.Host != outer.Source || permit.Remote.Fingerprint != outer.Certificate {
		return wire.Admission{}, model.Err("forbidden", "workspace identities differ from exact direct transport")
	}
	config, e := m.store.WorkspaceTLS(permit.Local, permit.Remote, true)
	if e != nil {
		return wire.Admission{}, e
	}
	if e = m.recheckWorkspaceOuter(outer); e != nil {
		return wire.Admission{}, e
	}
	return wire.Admission{Local: permit.Local, Remote: permit.Remote, Expires: minStamp(permit.Expires, outer.Expires), TLS: config}, nil
}
func (m *meshService) newWorkspaceHandler() *wire.Handler {
	h := &wire.Handler{AuthorizeOuter: m.workspaceOuter, Authorize: m.workspaceAdmission}
	h.Revalidate = func(ctx context.Context, outer wire.Outer, o wire.Open, admission wire.Admission) error {
		actual, e := m.workspaceAdmission(ctx, outer, o)
		if e != nil {
			return e
		}
		if actual.Local != admission.Local || actual.Remote != admission.Remote || !time.Now().Before(admission.Expires) {
			return model.Err("forbidden", "workspace stream authority changed")
		}
		return nil
	}
	h.Handle = func(ctx context.Context, outer wire.Outer, o wire.Open, admission wire.Admission, conn *tls.Conn) {
		q, e := wire.ReadRequest(conn, o)
		if e != nil {
			return
		}
		if e = h.Revalidate(ctx, outer, o, admission); e != nil {
			return
		}
		result := q.Response()
		switch q.Operation {
		case wire.OperationProof:
			var p workspace.Proof
			p, e = m.owner.Manager.WorkspaceProveJoin(ctx, admission.Remote, *q.Challenge)
			if e == nil {
				result.Proof = &p
			}
		case wire.OperationStatus:
			var status workspace.AdmissionStatus
			status, e = m.owner.Manager.WorkspaceOwnAdmissionStatus(ctx, admission.Remote, *q.Status)
			if e == nil {
				result.Status = &status
			}
		case wire.OperationRefresh:
			permit, err := m.owner.Manager.WorkspaceTransport(ctx, o.Workspace, o.Generation, o.Source, o.Target, o.Class, o.Consent)
			e = err
			if e == nil && permit.Principal != q.Refresh.Source {
				e = model.Err("forbidden", "refresh source is not the TLS-derived current member")
			}
			if e == nil {
				var snapshot workspace.Snapshot
				snapshot, e = m.owner.Manager.WorkspaceIssue(ctx, *q.Refresh)
				if e == nil {
					var pages []workspace.Page
					pages, e = workspace.Pages(snapshot, workspace.MaxPageBytes-1024)
					if e == nil {
						// Preflight the complete stream before sending any membership page.
						total := 0
						for _, page := range pages {
							r := q.Response()
							r.Page = &page
							raw, _ := json.Marshal(r)
							total += len(raw) + 4
						}
						if total > wire.MaxStreamBytes-32768 {
							e = model.Err("response_too_large", "complete directory exceeds bounded stream; nothing truncated")
						} else {
							for _, page := range pages {
								if h.Revalidate(ctx, outer, o, admission) != nil {
									return
								}
								r := q.Response()
								r.Page = &page
								if wire.WriteReply(conn, q, r) != nil {
									return
								}
							}
							return
						}
					}
				}
			}
		}
		if e != nil {
			var api *model.Error
			if errors.As(e, &api) {
				result.Failure = &wire.Failure{Code: api.Code, Message: "Workspace operation unavailable; inspect local intent and current authority."}
			} else {
				result.Failure = &wire.Failure{Code: "workspace_unavailable", Message: "Workspace operation unavailable; no action was retried."}
			}
		}
		_ = wire.WriteReply(conn, q, result)
	}
	m.owner.workspaceStreamsMu.Lock()
	if m.owner.workspaceCallsClosed {
		h.Stop()
	}
	if m.owner.workspaceStreams == nil {
		m.owner.workspaceStreams = map[*wire.Handler]struct{}{}
	}
	// Stopped handlers with no workers need no retained lifecycle slot.
	for old := range m.owner.workspaceStreams {
		if old.Finished() {
			delete(m.owner.workspaceStreams, old)
		}
	}
	m.owner.workspaceStreams[h] = struct{}{}
	m.owner.workspaceStreamsMu.Unlock()
	return h
}

func (s *Server) workspaceService() (*meshService, error) {
	s.meshMu.Lock()
	m := s.mesh
	s.meshMu.Unlock()
	if m == nil {
		return nil, model.Err("mesh_unavailable", "workspace transport requires an explicitly enabled direct mesh listener")
	}
	return m, nil
}
func (s *Server) workspaceDial(ctx context.Context, id, generation, source, target, class, consent string) (*tls.Conn, wire.Open, wire.Admission, error) {
	m, e := s.workspaceService()
	if e != nil {
		return nil, wire.Open{}, wire.Admission{}, e
	}
	permit, e := s.Manager.WorkspaceTransport(ctx, id, generation, source, target, class, consent)
	if e != nil {
		return nil, wire.Open{}, wire.Admission{}, e
	}
	peer, e := m.store.Resolve(target)
	if e != nil {
		return nil, wire.Open{}, wire.Admission{}, e
	}
	if peer.State != "active" || !peer.Proven || peer.Identity.Fingerprint != permit.Remote.Fingerprint {
		return nil, wire.Open{}, wire.Admission{}, model.Err("forbidden", "exact active proven direct peer required; no directory identity was promoted")
	}
	// Destination revision is directional. Read it from the live destination,
	// never mistake the local inbound grant revision for the remote one.
	raw, e := m.store.Call(ctx, target, mesh.Request{Operation: "access_scope", Params: json.RawMessage(`{}`)})
	if e != nil {
		return nil, wire.Open{}, wire.Admission{}, e
	}
	var scope model.NodeAccessScope
	if e = mesh.Decode(raw, &scope); e != nil || scope.Source != source || scope.Target != target || scope.Pair != peer.Pair || scope.Revision < 1 {
		return nil, wire.Open{}, wire.Admission{}, model.Err("forbidden", "destination current transport scope differs")
	}
	outer, e := m.store.ClientTLS(peer)
	if e != nil {
		return nil, wire.Open{}, wire.Admission{}, e
	}
	inner, e := m.store.WorkspaceTLS(permit.Local, permit.Remote, false)
	if e != nil {
		return nil, wire.Open{}, wire.Admission{}, e
	}
	o := wire.Open{Version: wire.Version, Workspace: id, Generation: generation, Route: local.ID(), Source: source, Target: target, Path: []string{source, target}, Class: class, Consent: consent, Expires: minStamp(time.Now().Add(wire.MaxLifetime), permit.Expires), Pair: peer.Pair, Revision: scope.Revision}
	admission := wire.Admission{Local: permit.Local, Remote: permit.Remote, Expires: o.Expires, TLS: inner}
	check := func(ctx context.Context) error {
		current, e := m.store.Resolve(target)
		if e != nil {
			return e
		}
		if current.State != "active" || !current.Proven || current.Pair != peer.Pair || current.Revision != peer.Revision || current.Identity != peer.Identity {
			return model.Err("forbidden", "source direct enrollment changed")
		}
		actual, e := s.Manager.WorkspaceTransport(ctx, id, generation, source, target, class, consent)
		if e != nil {
			return e
		}
		if actual.Local != permit.Local || actual.Remote != permit.Remote {
			return model.Err("forbidden", "workspace identity changed")
		}
		return m.store.WorkspaceIdentityCurrent(permit.Local)
	}
	conn, e := wire.Dial(ctx, peer.Identity.Address, outer, o, admission, check)
	return conn, o, admission, e
}
func workspaceWireRequest(o wire.Open, operation string) wire.Request {
	return wire.Request{Version: wire.Version, Route: o.Route, Workspace: o.Workspace, Generation: o.Generation, Source: o.Source, Target: o.Target, Operation: operation}
}
func wireReplyError(r wire.Reply) error {
	if r.Failure != nil {
		return model.Err(r.Failure.Code, r.Failure.Message)
	}
	return nil
}

type workspaceProofTransport struct{ server *Server }

func (t workspaceProofTransport) Prove(ctx context.Context, c workspace.Challenge) (workspace.Proof, error) {
	conn, o, _, e := t.server.workspaceDial(ctx, c.Workspace, c.Generation, c.Directory.Host, c.Candidate.Host, wire.ClassJoinProof, c.ConsentHash)
	if e != nil {
		return workspace.Proof{}, e
	}
	defer conn.Close()
	q := workspaceWireRequest(o, wire.OperationProof)
	q.Challenge = &c
	if e = wire.WriteRequest(conn, o, q); e != nil {
		return workspace.Proof{}, e
	}
	r, e := wire.ReadReply(conn, q)
	if e != nil {
		return workspace.Proof{}, e
	}
	if e = wireReplyError(r); e != nil {
		return workspace.Proof{}, e
	}
	return *r.Proof, nil
}

// Server shutdown joins hijacked workspace streams before Manager.Close can
// close SQLite. Stop alone is used under meshMu on a mesh disable/replacement.
func (s *Server) closeWorkspaceStreams() {
	s.workspaceStreamsMu.Lock()
	s.workspaceCallsClosed = true
	if s.workspaceCallsCancel != nil {
		s.workspaceCallsCancel()
	}
	handlers := make([]*wire.Handler, 0, len(s.workspaceStreams))
	for h := range s.workspaceStreams {
		handlers = append(handlers, h)
	}
	s.workspaceStreamsMu.Unlock()
	for _, h := range handlers {
		h.Stop()
	}
	s.closeRoutedStreams()
	s.workspaceCalls.Wait()
	for _, h := range handlers {
		h.Wait()
	}
}
