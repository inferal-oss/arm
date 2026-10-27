package server

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"os/user"
	"time"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshforward"
	"github.com/inferal-oss/arm/internal/meshsshrelay"
	"github.com/inferal-oss/arm/internal/meshstream"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
	"github.com/inferal-oss/arm/internal/workspace"
	direct "github.com/inferal-oss/arm/internal/workspacetransport"
	"golang.org/x/crypto/ssh"
)

func sshRouteOuter(o meshsshrelay.Outer) direct.Outer {
	return direct.Outer{Source: o.Source, Target: o.Target, Pair: o.Pair, Certificate: o.Certificate, Revision: o.Revision, Expires: o.Expires}
}

func routedMembershipClass(o meshsshrelay.Open) string {
	if o.Class == meshsshrelay.ClassForward {
		return workspace.RouteForward
	}
	return workspace.RouteSSH
}

// routedSSHAdmission combines fresh workspace membership with an entirely
// separate local relay/destination grant. A messaging route never calls this.
func (m *meshService) routedSSHAdmission(ctx context.Context, outer meshsshrelay.Outer, o meshsshrelay.Open) (meshsshrelay.Permit, error) {
	if o.Class == meshsshrelay.ClassForward && !meshsshrelay.ForwardReviewEnabled {
		return meshsshrelay.Permit{}, model.Err("feature_unavailable", "TCP forwarding is disabled while acceptance gates remain incomplete")
	}
	if err := m.recheckWorkspaceOuter(sshRouteOuter(outer)); err != nil {
		return meshsshrelay.Permit{}, err
	}
	a, err := m.owner.Manager.WorkspaceRouteAccess(ctx, o.Workspace, o.Source, o.Target, routedMembershipClass(o))
	if err != nil {
		return meshsshrelay.Permit{}, err
	}
	if a.Generation != o.Generation || a.Local.Host != outer.Target || a.Local.Host != o.ImmediateTarget() ||
		a.OriginGeneration != o.SourceGeneration || a.DestinationGeneration != o.TargetGeneration ||
		a.Directory.Fingerprint != o.DirectoryPin || a.Directory.SPKI != o.DirectorySPKI ||
		a.Origin.Fingerprint != o.SourcePin || a.Destination.Fingerprint != o.TargetPin ||
		o.DirectoryRevision > a.DirectoryRevision {
		return meshsshrelay.Permit{}, model.Err("forbidden", "SSH route differs from fresh original/final pinned membership")
	}
	if outer.Source != o.Source {
		hop, err := m.owner.Manager.WorkspaceRouteAccess(ctx, o.Workspace, o.Source, outer.Source, routedMembershipClass(o))
		if err != nil || hop.Destination.Fingerprint != outer.Certificate {
			return meshsshrelay.Permit{}, model.Err("forbidden", "SSH relay's preceding member pin changed")
		}
	}
	policy, err := meshsshrelay.ReadPolicy(m.owner.home, a.Local.Host, a.Local.Fingerprint)
	if err != nil {
		return meshsshrelay.Permit{}, err
	}
	p := meshsshrelay.Permit{Local: a.Local, Origin: a.Origin, Destination: a.Destination, Expires: o.Expires}
	if outer.Expires.Before(p.Expires) {
		p.Expires = outer.Expires
	}
	if o.Final() {
		if o.Class == meshsshrelay.ClassForward {
			grant, e := policy.Forward(o)
			if e != nil {
				return meshsshrelay.Permit{}, e
			}
			account, e := user.Current()
			if e != nil || grant.Account != account.Username || grant.SourceSPKI != a.Origin.SPKI || grant.TargetSPKI != a.Destination.SPKI {
				return meshsshrelay.Permit{}, model.Err("forbidden", "TCP service account or original endpoint changed")
			}
			p.ForwardGrant = grant
			if grant.Expires.Before(p.Expires) {
				p.Expires = grant.Expires
			}
			p.Inner, e = m.store.RoutedForwardTLS(a.Local, a.Origin, true)
			if e != nil {
				return meshsshrelay.Permit{}, e
			}
			if !time.Now().Before(p.Expires) {
				return meshsshrelay.Permit{}, model.Err("forbidden", "TCP service grant expired")
			}
			if e = m.recheckWorkspaceOuter(sshRouteOuter(outer)); e != nil {
				return meshsshrelay.Permit{}, e
			}
			return p, nil
		}
		grant, err := policy.Destination(o)
		if err != nil {
			return meshsshrelay.Permit{}, err
		}
		if grant.SourceSPKI != a.Origin.SPKI || grant.TargetSPKI != a.Destination.SPKI {
			return meshsshrelay.Permit{}, model.Err("forbidden", "routed SSH source/target SPKI differs from review")
		}
		snapshot, err := m.sshSnapshot(ctx)
		if err != nil {
			return meshsshrelay.Permit{}, err
		}
		if grant.Account != snapshot.state.Binding.Account || grant.Owner != snapshot.state.Binding.Owner || grant.SSHHostKeyPin != sshPublicHash(snapshot.key.PublicKey()) {
			return meshsshrelay.Permit{}, model.Err("forbidden", "routed SSH destination account or host key changed")
		}
		if grant.Expires.Before(p.Expires) {
			p.Expires = grant.Expires
		}
		p.DestinationGrant = grant
		p.Inner, err = m.store.RoutedSSHTLS(a.Local, a.Origin, true)
		if err != nil {
			return meshsshrelay.Permit{}, err
		}
	} else {
		grant, err := policy.Relay(o)
		if err != nil {
			return meshsshrelay.Permit{}, err
		}
		if grant.Expires.Before(p.Expires) {
			p.Expires = grant.Expires
		}
		p.RelayGrant = grant
		p.TransitRate = grant.BytesPerSecond
	}
	if !time.Now().Before(p.Expires) {
		return meshsshrelay.Permit{}, model.Err("forbidden", "routed SSH grant expired")
	}
	if err = m.recheckWorkspaceOuter(sshRouteOuter(outer)); err != nil {
		return meshsshrelay.Permit{}, err
	}
	return p, nil
}

func (m *meshService) routedSSHScope(ctx context.Context, o meshsshrelay.Open, p meshsshrelay.Permit) (meshstream.Scope, sshDestinationSnapshot, error) {
	var none meshstream.Scope
	if !o.Final() {
		return none, sshDestinationSnapshot{}, model.Err("forbidden", "only final SSH destination has execution scope")
	}
	snapshot, err := m.sshSnapshot(ctx)
	if err != nil {
		return none, snapshot, err
	}
	g := p.DestinationGrant
	if o.Class != meshsshrelay.ClassSSHIdentity && g.Authorize(time.Now(), o, p.Origin.Fingerprint, p.Origin.SPKI, p.Destination.Fingerprint, p.Destination.SPKI,
		sshPublicHash(snapshot.key.PublicKey()), snapshot.state.Binding.Account, snapshot.state.Binding.Owner, time.Nanosecond, model.Resources{}) != nil {
		return none, snapshot, model.Err("forbidden", "current original-source destination SSH grant differs")
	}
	pair, err := g.VirtualPair()
	if err != nil {
		return none, snapshot, err
	}
	return meshstream.Scope{Source: o.Source, Target: o.Target, Pair: pair, Revision: g.Revision, Account: g.Account,
		Certificate: g.SourcePin, MaxTTL: g.MaxTTL, Limits: g.Limits, ValidUntil: g.Expires, Owner: g.Owner,
		HostKeySHA256: g.SSHHostKeyPin, ServiceRevision: snapshot.state.Service.Revision}, snapshot, nil
}

func (m *meshService) newSSHRouteHandler() *meshsshrelay.Handler {
	return m.newBoundedRouteHandler(false)
}
func (m *meshService) newForwardRouteHandler() *meshsshrelay.Handler {
	return m.newBoundedRouteHandler(true)
}
func (m *meshService) newBoundedRouteHandler(forward bool) *meshsshrelay.Handler {
	h := &meshsshrelay.Handler{}
	if forward {
		h.RoutePath, h.Subprotocol, h.AllowedClass = meshsshrelay.ForwardPath, meshsshrelay.ForwardProtocol, meshsshrelay.ClassForward
	}
	h.AuthorizeOuter = func(ctx context.Context, raw []byte) (meshsshrelay.Outer, error) {
		o, err := m.workspaceOuter(ctx, raw)
		return meshsshrelay.Outer{Source: o.Source, Target: o.Target, Pair: o.Pair, Certificate: o.Certificate, Revision: o.Revision, Expires: o.Expires}, err
	}
	h.Authorize = m.routedSSHAdmission
	h.ReserveTransit = m.reserveSSHRelayTransit
	if forward {
		h.ReserveFinal = m.reserveForwardFinal
	}
	h.Revalidate = func(ctx context.Context, outer meshsshrelay.Outer, o meshsshrelay.Open, p meshsshrelay.Permit) error {
		if err := meshsshrelay.CheckReservation(ctx); err != nil {
			return err
		}
		a, err := m.routedSSHAdmission(ctx, outer, o)
		if err != nil {
			return err
		}
		if a.Local != p.Local || a.Origin != p.Origin || a.Destination != p.Destination ||
			a.RelayGrant != p.RelayGrant || a.DestinationGrant != p.DestinationGrant || a.ForwardGrant != p.ForwardGrant || !time.Now().Before(p.Expires) {
			return model.Err("forbidden", "routed SSH membership, grant or endpoint identity changed")
		}
		return meshsshrelay.CheckReservation(ctx)
	}
	h.Forward = func(ctx context.Context, outer meshsshrelay.Outer, o meshsshrelay.Open, p meshsshrelay.Permit, previous net.Conn) {
		nextHost := o.Path[o.Hop+1]
		next, err := m.store.Resolve(nextHost)
		if err != nil || next.State != "active" || !next.Proven {
			return
		}
		a, err := m.owner.Manager.WorkspaceRouteAccess(ctx, o.Workspace, o.Source, nextHost, routedMembershipClass(o))
		if err != nil || a.Destination.Fingerprint != next.Identity.Fingerprint {
			return
		}
		outgoing := meshsshrelay.NextAuthority{Local: p.Local.Host, DirectPeer: nextHost, Pair: next.Pair,
			EnrollmentRevision: next.Revision, Workspace: o.Workspace, Generation: o.Generation, SSHRelayPermit: true, ValidUntil: a.Expires}
		if err = o.AuthorizeNext(time.Now(), outgoing); err != nil {
			return
		}
		conn, err := m.sshRouteHop(ctx, next, o, outgoing, func(ctx context.Context) error { return h.Revalidate(ctx, outer, o, p) })
		if err != nil {
			return
		}
		defer conn.Close()
		pacer, err := m.relayPacers.ForGrant(p.RelayGrant)
		if err != nil {
			return
		}
		copied := make(chan struct{})
		go func() {
			defer close(copied)
			_, _ = meshsshrelay.CopyPaced(ctx, conn, previous, pacer)
			_ = conn.Close()
			_ = previous.Close()
		}()
		_, _ = meshsshrelay.CopyPaced(ctx, previous, conn, pacer)
		_ = conn.Close()
		_ = previous.Close()
		<-copied
	}
	h.Final = func(ctx context.Context, outer meshsshrelay.Outer, o meshsshrelay.Open, p meshsshrelay.Permit, inner *tls.Conn) {
		if o.Class == meshsshrelay.ClassForward {
			m.serveRoutedForward(ctx, h, outer, o, p, inner)
			return
		}
		m.serveRoutedSSH(ctx, h, outer, o, p, inner)
	}
	return h
}

func (m *meshService) reserveForwardFinal(admission, lifetime context.Context, joined <-chan struct{}, outer meshsshrelay.Outer, o meshsshrelay.Open, p meshsshrelay.Permit) (meshsshrelay.TransitReservation, error) {
	if !o.Final() || o.Class != meshsshrelay.ClassForward || p.ForwardGrant.Validate() != nil {
		return meshsshrelay.TransitReservation{}, model.Err("forbidden", "exact reviewed TCP destination budget required")
	}
	var reservation meshsshrelay.TransitReservation
	err := meshsshrelay.WithPolicyRead(m.owner.home, p.Local.Host, p.Local.Fingerprint, func(policy meshsshrelay.Policy) error {
		grant, e := policy.Forward(o)
		if e != nil || grant != p.ForwardGrant {
			return model.Err("forbidden", "TCP destination grant changed before resource reservation")
		}
		if e = admission.Err(); e != nil {
			return e
		}
		ttl := time.Until(p.Expires)
		if ttl <= 0 || ttl > meshsshrelay.MaxLifetime {
			return model.Err("forbidden", "TCP destination lifetime differs")
		}
		hold, e := m.owner.Manager.AcquireRelayTransit(lifetime, model.Request{Key: "tcp-final:" + o.Route + ":" + p.Local.Host, Reason: "reviewed original-source TCP service " + o.Service + " from " + o.Source + " in workspace " + o.Workspace, Project: "mesh:" + o.Workspace, Resources: grant.Transit, TTL: ttl}, joined)
		if e != nil {
			return e
		}
		lease, e := hold.Lease()
		reservation = meshsshrelay.TransitReservation{Context: hold.Context(), CheckActive: hold.CheckActive, End: func() {
			if lease.ID != "" {
				if _, endErr := m.owner.Manager.End(lease.ID, "forward_closed"); endErr != nil {
					log.Printf("TCP destination hold end remains unconfirmed: %v", endErr)
				}
			}
		}, Finish: func() {
			if _, finishErr := hold.FinishAfterJoin(); finishErr != nil {
				log.Printf("TCP destination hold remains charged after join: %v", finishErr)
			}
		}}
		if e != nil {
			return e
		}
		if lease.State != "active" {
			return model.Err("capacity_exceeded", "TCP destination CPU/memory hold is not active")
		}
		return nil
	})
	return reservation, err
}

func (m *meshService) serveRoutedForward(ctx context.Context, h *meshsshrelay.Handler, outer meshsshrelay.Outer, o meshsshrelay.Open, p meshsshrelay.Permit, inner *tls.Conn) {
	if o.Class != meshsshrelay.ClassForward || h.Revalidate(ctx, outer, o, p) != nil {
		return
	}
	_ = inner.SetReadDeadline(time.Now().Add(meshsshrelay.RequestDeadline))
	q, err := meshforward.ReadRequest(inner)
	_ = inner.SetReadDeadline(time.Time{})
	if err != nil {
		return
	}
	binding, err := o.BindingDigest(time.Now())
	if err != nil || q.RouteBindingHash != binding {
		return
	}
	account, err := user.Current()
	if err != nil {
		return
	}
	who := meshforward.Identity{SourcePin: p.Origin.Fingerprint, SourceSPKI: p.Origin.SPKI, TargetPin: p.Destination.Fingerprint, TargetSPKI: p.Destination.SPKI, Account: account.Username}
	loader := func(check context.Context) (meshforward.Grant, error) {
		if err := h.Revalidate(check, outer, o, p); err != nil {
			return meshforward.Grant{}, err
		}
		policy, err := meshsshrelay.ReadPolicy(m.owner.home, p.Local.Host, p.Local.Fingerprint)
		if err != nil {
			return meshforward.Grant{}, err
		}
		return policy.Forward(o)
	}
	if err := p.ForwardGrant.Authorize(time.Now(), who.SourcePin, who.SourceSPKI, who.TargetPin, who.TargetSPKI, who.Account, q); err != nil {
		return
	}
	_ = m.forwardProxy.Open(ctx, q, who, inner, loader, func() error { return meshforward.WriteAccepted(inner, q) })
}

func (m *meshService) reserveSSHRelayTransit(admission, lifetime context.Context, joined <-chan struct{}, _ meshsshrelay.Outer, o meshsshrelay.Open, p meshsshrelay.Permit) (meshsshrelay.TransitReservation, error) {
	if o.Final() || p.RelayGrant.Validate() != nil || p.TransitRate != p.RelayGrant.BytesPerSecond {
		return meshsshrelay.TransitReservation{}, model.Err("forbidden", "reviewed SSH relay transit budget required")
	}
	var reservation meshsshrelay.TransitReservation
	err := meshsshrelay.WithPolicyRead(m.owner.home, p.Local.Host, p.Local.Fingerprint, func(policy meshsshrelay.Policy) error {
		grant, e := policy.Relay(o)
		if e != nil || grant != p.RelayGrant {
			return model.Err("forbidden", "SSH relay grant changed before transit reservation")
		}
		if e = admission.Err(); e != nil {
			return e
		}
		ttl := time.Until(p.Expires)
		if ttl <= 0 || ttl > meshsshrelay.MaxLifetime {
			return model.Err("forbidden", "SSH relay transit lifetime differs")
		}
		hold, e := m.owner.Manager.AcquireRelayTransit(lifetime, model.Request{
			Key:       "ssh-relay:" + o.Route + ":" + p.Local.Host,
			Reason:    "reviewed opaque " + o.Class + " transit from " + o.Source + " to " + o.Target + " via " + p.Local.Host + " in workspace " + o.Workspace,
			Project:   "mesh:" + o.Workspace,
			Resources: grant.Transit, TTL: ttl}, joined)
		if e != nil {
			return e
		}
		lease, e := hold.Lease()
		reservation = meshsshrelay.TransitReservation{Context: hold.Context(), CheckActive: hold.CheckActive, End: func() {
			if lease.ID != "" {
				if _, endErr := m.owner.Manager.End(lease.ID, "relay_closed"); endErr != nil {
					log.Printf("routed SSH transit hold end remains unconfirmed: %v", endErr)
				}
			}
		}, Finish: func() {
			if _, finishErr := hold.FinishAfterJoin(); finishErr != nil {
				log.Printf("routed SSH transit hold remains charged after join: %v", finishErr)
			}
		}}
		if e != nil {
			return e
		}
		if lease.State != "active" {
			return model.Err("capacity_exceeded", "SSH relay transit CPU/memory hold is not active")
		}
		return nil
	})
	return reservation, err
}

func (m *meshService) sshRouteHop(ctx context.Context, peer mesh.Peer, o meshsshrelay.Open, outgoing meshsshrelay.NextAuthority, check func(context.Context) error) (net.Conn, error) {
	raw, err := m.store.Call(ctx, peer.Identity.Host, mesh.Request{Operation: "access_scope", Params: json.RawMessage(`{}`)})
	if err != nil {
		return nil, err
	}
	var scope model.NodeAccessScope
	if err = mesh.Decode(raw, &scope); err != nil || scope.Source != m.store.Config().Identity.Host || scope.Target != peer.Identity.Host || scope.Pair != peer.Pair || scope.Revision < 1 {
		return nil, model.Err("forbidden", "outgoing SSH relay pair differs")
	}
	outgoing.EnrollmentRevision = scope.Revision
	next, err := o.Forward(time.Now(), outgoing)
	if err != nil {
		return nil, err
	}
	config, err := m.store.ClientTLS(peer)
	if err != nil {
		return nil, err
	}
	recheck := func(ctx context.Context) error {
		current, err := m.store.Resolve(peer.Identity.Host)
		if err != nil {
			return err
		}
		if current.State != "active" || !current.Proven || current.Pair != peer.Pair || current.Revision != peer.Revision || current.Identity != peer.Identity {
			return model.Err("forbidden", "outgoing SSH relay enrollment changed")
		}
		return check(ctx)
	}
	return meshsshrelay.DialCarrier(ctx, peer.Identity.Address, config, next, recheck)
}

func (m *meshService) serveRoutedSSH(ctx context.Context, h *meshsshrelay.Handler, outer meshsshrelay.Outer, o meshsshrelay.Open, p meshsshrelay.Permit, inner *tls.Conn) {
	if o.Class == meshsshrelay.ClassSync {
		return
	} // Sync requires its fixed worker gate.
	if err := inner.SetDeadline(time.Now().Add(meshsshrelay.RequestDeadline)); err != nil {
		return
	}
	q, err := meshsshrelay.ReadRequest(inner)
	if err != nil || h.Revalidate(ctx, outer, o, p) != nil {
		return
	}
	if o.Class == meshsshrelay.ClassSSHIdentity && q.Operation != meshsshrelay.OperationIdentity {
		return
	}
	scope, snapshot, err := m.routedSSHScope(ctx, o, p)
	if err != nil {
		return
	}
	recheck := func(ctx context.Context) error {
		if err := h.Revalidate(ctx, outer, o, p); err != nil {
			return err
		}
		actual, _, err := m.routedSSHScope(ctx, o, p)
		if err != nil {
			return err
		}
		if actual != scope {
			return model.Err("ssh_context_changed", "routed SSH grant/account/host key changed")
		}
		return nil
	}
	out := q.Reply()
	switch q.Operation {
	case meshsshrelay.OperationIdentity:
		local := snapshot.state.MeshIdentity()
		out.Identity = &model.SSHDestinationIdentity{Schema: 1, Source: scope.Source, Target: scope.Target, Owner: scope.Owner,
			Account: scope.Account, Home: snapshot.home, Pair: scope.Pair, Revision: scope.Revision, Certificate: local.Fingerprint,
			SPKI: local.SPKI, PublicKey: snapshot.state.PublicKey, ServiceRevision: scope.ServiceRevision,
			MaxTTL: int64(scope.MaxTTL), Limits: scope.Limits}
	case meshsshrelay.OperationLookup:
		status, e := m.owner.Manager.LookupSSHKey(ctx, scope.Source, q.Key)
		if e == nil && status.Fence != nil && status.Fence.Control != nil &&
			(status.Fence.Control.Certificate != scope.Certificate || status.Fence.Control.Pair != scope.Pair || status.Fence.Control.Revision != scope.Revision) {
			e = model.Err("forbidden", "routed SSH key belongs to another original endpoint grant")
		}
		if e == nil && status.Execution != nil && status.Execution.Operation != nil && status.Execution.Operation.Request.Certificate != scope.Certificate {
			e = model.Err("forbidden", "routed SSH key belongs to previous original certificate")
		}
		if e == nil {
			status, e = compactSSHKeyStatus(status)
		}
		if e == nil {
			out.Status = &status
		} else {
			err = e
		}
	case meshsshrelay.OperationSeal:
		c := *q.Commitment
		if c.Source != scope.Source || c.Target != scope.Target || c.Pair != scope.Pair || c.Revision != scope.Revision ||
			c.Certificate != scope.Certificate || c.Owner != scope.Owner || c.Account != scope.Account || c.HostKeySHA256 != scope.HostKeySHA256 {
			err = model.Err("forbidden", "routed SSH seal differs from original source and destination grant")
		} else {
			status, e := m.owner.Manager.SealSSHKey(ctx, c, q.Reason)
			if e == nil {
				status, e = compactSSHKeyStatus(status)
			}
			if e == nil {
				out.Status = &status
			} else {
				err = e
			}
		}
	case meshsshrelay.OperationExecute:
		control := *q.Control
		if err = control.Authorize(scope); err == nil {
			err = p.DestinationGrant.Authorize(time.Now(), o, p.Origin.Fingerprint, p.Origin.SPKI, p.Destination.Fingerprint,
				p.Destination.SPKI, scope.HostKeySHA256, scope.Account, scope.Owner, control.TTL, control.Resources)
		}
		if err == nil {
			out.Accepted = true
		}
	}
	if err == nil {
		err = recheck(ctx)
	}
	if err != nil {
		code := "ssh_unavailable"
		var api *model.Error
		if errors.As(err, &api) {
			code = api.Code
		}
		out = q.Reply()
		out.Failure = &meshsshrelay.Failure{Code: code, Message: "Routed SSH operation unavailable; inspect the retained original key. No execution was retried."}
	}
	if meshsshrelay.WriteResponse(inner, q, out) != nil {
		return
	}
	if q.Operation != meshsshrelay.OperationExecute || err != nil {
		return
	}
	if inner.SetDeadline(time.Time{}) != nil {
		return
	}
	control := *q.Control
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(control.PublicKey))
	if err != nil {
		return
	}
	service := sshservice.Server{HostKey: snapshot.key, Handshake: 10 * time.Second, StartTimeout: 10 * time.Second,
		Revalidate: recheck, Execute: func(ctx context.Context, execution sshservice.Execution, ch ssh.Channel) (sshservice.Process, error) {
			var process sshservice.Process
			err := meshsshrelay.WithPolicyRead(m.owner.home, p.Local.Host, p.Local.Fingerprint, func(policy meshsshrelay.Policy) error {
				grant, e := policy.Destination(o)
				if e != nil || grant != p.DestinationGrant {
					return model.Err("forbidden", "original-source SSH destination grant changed before exec")
				}
				if e = recheck(ctx); e != nil {
					return e
				}
				actual, _, e := m.routedSSHScope(ctx, o, p)
				if e != nil {
					return e
				}
				if actual != scope {
					return model.Err("ssh_context_changed", "routed SSH service/account/grant changed before exec")
				}
				if e = control.Authorize(actual); e != nil {
					return e
				}
				if e = grant.Authorize(time.Now(), o, p.Origin.Fingerprint, p.Origin.SPKI, p.Destination.Fingerprint, p.Destination.SPKI,
					actual.HostKeySHA256, actual.Account, actual.Owner, control.TTL, control.Resources); e != nil {
					return e
				}
				commitment, e := control.Commitment(actual.Certificate)
				if e != nil {
					return e
				}
				commandHash := sha256.Sum256([]byte(execution.Command))
				terminalHash := ""
				if execution.Terminal != nil {
					raw, _ := json.Marshal(execution.Terminal)
					sum := sha256.Sum256(raw)
					terminalHash = hex.EncodeToString(sum[:])
				}
				request := model.SSHExecutionRequest{Source: actual.Source, Target: actual.Target, Pair: actual.Pair,
					Revision: actual.Revision, Certificate: actual.Certificate, Account: actual.Account, Key: control.Key,
					PublicKeySHA256: sshPublicHash(key), Reason: control.Reason, TTL: control.TTL, Resources: control.Resources,
					CWD: control.CWD, Kind: execution.Kind, CommandSHA256: hex.EncodeToString(commandHash[:]), TerminalSHA256: terminalHash}
				if !commitment.Matches(request) {
					return model.Err("invalid_request", "routed SSH exec/shell/PTY differs from source intent")
				}
				_, process, e = m.owner.Manager.AcquireSSHCommitted(ctx, request, execution, ch, actual.Limits, actual.MaxTTL, actual.ValidUntil, commitment)
				if e != nil {
					return e
				}
				if process == nil {
					return model.Err("ssh_key_exists", "SSH key already submitted; inspect exact outcome without retry")
				}
				return nil
			})
			return process, err
		}}
	_ = service.Serve(ctx, inner, sshservice.Permit{Account: scope.Account, PublicKey: key})
}
