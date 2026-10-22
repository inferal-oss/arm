// Package meshops coordinates operator-reviewed direct peer lifecycle operations.
// It has no dependency on CLI or server and never accepts arbitrary shell input.
package meshops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/yrashk/arm/internal/buildinfo"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshssh"
	"github.com/yrashk/arm/internal/model"
	"net"
	"reflect"
	"time"
)

type Coordinator struct {
	Context       context.Context
	Home          string
	Machine       bool
	Inspect       func() (mesh.LifecycleInspection, error)
	Ensure        func(mesh.StartupPolicy) (mesh.LifecycleInspection, error)
	Call          func(context.Context, string, any, any) error
	SSH           func(context.Context, string, string, any, meshssh.Identity, any) (meshssh.Identity, error)
	Print         func(any) error
	Confirm       func(any, bool, bool) error
	guard         *mesh.CoordinationGuard
	attemptParent string
	heartbeatStop context.CancelFunc
}
type Bootstrap struct {
	Home, Action, Host, Pair, Reason string
	ReplaceRevoked                   bool
	ExpectedRevision                 *int
	Identity                         mesh.Identity
	Grant                            mesh.Grant
}
type Helper struct {
	Home, Action                string
	ExpectedHost, ExpectedOwner string
	Startup                     mesh.StartupPolicy
	Prepare                     mesh.PrepareRequest
	Apply                       mesh.ApplySetup
	Revoke                      DisconnectRequest
}
type DisconnectRequest struct {
	Key, Host, Pair, Fingerprint, Reason string
	Revision                             int
}
type ConnectRequest struct {
	Destination    string     `json:"ssh_destination"`
	RemoteHome     string     `json:"remote_home"`
	Binary         string     `json:"remote_binary"`
	LocalIP        string     `json:"local_ip,omitempty"`
	RemoteIP       string     `json:"remote_ip,omitempty"`
	Reason         string     `json:"reason"`
	Forward        mesh.Grant `json:"source_permissions_on_target"`
	Reverse        mesh.Grant `json:"target_permissions_on_source"`
	Public         bool       `json:"allow_public_listen"`
	ReplaceRevoked bool       `json:"replace_revoked"`
}
type Plan struct {
	Operation      string           `json:"operation"`
	Key            string           `json:"key"`
	Request        ConnectRequest   `json:"request"`
	SSH            meshssh.Identity `json:"ssh_identity"`
	Local          any              `json:"local"`
	Remote         any              `json:"remote"`
	LocalPrepared  any              `json:"local_prepared,omitempty"`
	RemotePrepared any              `json:"remote_prepared,omitempty"`
	Warning        string           `json:"warning"`
}

func (a *Coordinator) connect(key string, request ConnectRequest, yes, dry bool) (int, error) {
	own, err := a.Inspect()
	if err != nil {
		return 1, err
	}
	sshIdentity := meshssh.Identity{}
	ssh := func(q any, result any) error {
		observed, e := a.ssh(request.Destination, request.Binary, q, sshIdentity, result)
		if e == nil {
			sshIdentity = observed
		}
		return e
	}
	var remote mesh.LifecycleInspection
	if err = ssh(Helper{Home: request.RemoteHome, Action: "lifecycle_inspect"}, &remote); err != nil {
		return 1, err
	}
	if remote.Protocol != mesh.LifecycleProtocol || remote.Version != buildinfo.Version {
		return 1, model.Err("mesh_version_mismatch", "automatic setup requires the same installed ARM version and lifecycle protocol; no binary was installed")
	}
	if remote.Route == nil {
		return 1, model.Err("unsupported_route", "target helper did not report authenticated direct SSH interface addresses")
	}
	var inferredLocal, inferredRemote string
	if remote.Route.Synthetic && (request.LocalIP == "" || request.RemoteIP == "") {
		routeCtx, cancel := context.WithTimeout(a.Context, 3*time.Second)
		canonical, e := meshssh.CanonicalHost(routeCtx, request.Destination)
		if e != nil {
			cancel()
			return 1, e
		}
		addresses, e := net.DefaultResolver.LookupIPAddr(routeCtx, canonical)
		cancel()
		if e != nil {
			return 1, fmt.Errorf("canonical SSH target DNS unavailable: %w", e)
		}
		dns := []string{}
		for _, address := range addresses {
			if len(dns) >= 64 {
				return 1, fmt.Errorf("SSH target DNS exceeds route bound")
			}
			if address.Zone != "" {
				return 1, fmt.Errorf("scoped SSH DNS address unsupported")
			}
			dns = append(dns, address.IP.String())
		}
		inferredRemote, e = mesh.SyntheticTarget(*remote.Route, remote.PrivateIPs, dns)
		if e != nil {
			return 1, e
		}
		inferredLocal, e = mesh.RouteSource(inferredRemote)
		if e != nil {
			return 1, e
		}
	}
	plan := Plan{Operation: "connect-direct-peers", Key: key, Request: request, SSH: sshIdentity, Local: lifecycleNodePreview(own, remote.Host), Remote: lifecycleNodePreview(remote, own.Host), Warning: "Preparation may restart ordinary backends and restore previously approved enabled listeners. No NEW listener, endpoint or peer trust is enabled until this review. Declining closes new pending reservations or lets them expire; private identities, ordinary backends and pre-existing trust/listeners remain. Reverse grants default to none. Existing backend budgets are preserved."}
	if request.Forward.Has("run") || request.Reverse.Has("run") {
		plan.Warning += " RUN executes as the destination OS user with that user's file and credential access; it is not a sandbox."
	}
	if dry || (a.Machine && !yes) {
		if err = a.Print(plan); err != nil {
			return 1, err
		}
		if dry {
			return 0, nil
		}
		return 1, model.Err("confirmation_required", "read-only plan above; pass --yes to authorize private setup and reviewed trust")
	}
	if err = local.Prepare(a.Home); err != nil {
		return 1, err
	}
	digest, _ := mesh.ReviewDigest(request)
	if a.guard == nil {
		if prior, ok, e := mesh.ReadCoordination(a.Home, key); e != nil {
			return 1, e
		} else if ok {
			if prior.ParentKey != "" {
				return 1, model.Err("original_intent_required", "this key identifies a recovery attempt; retry the original immutable intent key, not its child receipt")
			}
			if prior.Kind != "connect" || prior.Digest != digest {
				return 1, model.Err("idempotency_conflict", "operator key belongs to another immutable intent")
			}
			if prior.Phase == "completed" {
				return a.restoreCompleted(key, request, prior, own, remote, sshIdentity, yes)
			}
			if prior.Phase == "incomplete" && prior.Worker == "" && prior.ResolvedBy == "" {
				a.attemptParent = key
			} else {
				return 1, model.Err("terminal_or_active_key", "operation key is already recorded; inspect its receipt and use a fresh key for a new intent")
			}
		}
	}
	if err = a.begin(key, "connect", digest, "ssh:"+request.Destination); err != nil {
		return 1, err
	}
	if err = a.recordRequest(StartRequest{Key: key, Kind: "connect", Connect: &request}); err != nil {
		return 1, err
	}
	if remote.Host != "" {
		if err = a.guard.BindTarget("host:" + remote.Host); err != nil {
			return 1, err
		}
	}
	operation, exists, err := mesh.ReadLifecycleOperation(a.Home, key)
	if err != nil {
		return 1, err
	}
	if exists && (operation.Kind != "connect" || operation.Digest != digest || operation.SSHAccount != sshIdentity.Account || operation.SSHHostKey != sshIdentity.HostKey) {
		return 1, model.Err("idempotency_conflict", "lifecycle key belongs to a different request or SSH identity")
	}
	if !exists {
		operation = mesh.LifecycleOperation{Key: key, Kind: "connect", Digest: digest, At: time.Now().UTC(), Phase: "inspected", SSHLocator: request.Destination, SSHAccount: sshIdentity.Account, SSHHostKey: sshIdentity.HostKey, RemoteHome: remote.Home, Binary: request.Binary, Reason: request.Reason}
	}

	if exists && operation.Phase == "connected" && own.Identity != nil && remote.Identity != nil && operation.LocalIdentity != nil && operation.RemoteIdentity != nil && *own.Identity == *operation.LocalIdentity && *remote.Identity == *operation.RemoteIdentity {
		active := func(config *mesh.Config, identity mesh.Identity, grant mesh.Grant) bool {
			if config == nil || !config.ListenerEnabled() {
				return false
			}
			for _, peer := range config.Peers {
				if peer.Identity == identity && peer.Pair == operation.Pair && peer.State == "active" && reflect.DeepEqual(peer.Grant, grant) {
					return true
				}
			}
			return false
		}
		if own.Running && remote.Running && active(own.Config, *remote.Identity, request.Reverse) && active(remote.Config, *own.Identity, request.Forward) {
			return a.verifyRecoveredConnection(key, request, operation, own, remote, sshIdentity, yes)
		}
	}
	if operation.LocalIdentity != nil && own.Identity != nil && operation.LocalIdentity.Fingerprint != own.Identity.Fingerprint {
		return 1, model.Err("setup_changed", "local pin changed since recorded operation")
	}
	if operation.RemoteIdentity != nil && remote.Identity != nil && operation.RemoteIdentity.Fingerprint != remote.Identity.Fingerprint {
		return 1, model.Err("setup_changed", "remote pin changed since recorded operation")
	}
	own, err = a.Ensure(own.Startup)
	if err != nil {
		return 1, err
	}
	if err = ssh(Helper{Home: request.RemoteHome, Action: "lifecycle_ensure", Startup: remote.Startup, ExpectedHost: remote.Host, ExpectedOwner: remote.Owner.UID}, &remote); err != nil {
		return 1, err
	}
	if remote.Route == nil {
		return 1, model.Err("unsupported_route", "target direct route disappeared")
	}
	if own.Host == remote.Host {
		return 1, model.Err("wrong_host", "cannot pair a node with itself or a copied host ledger")
	}
	if err = a.guard.BindTarget("host:" + remote.Host); err != nil {
		return 1, err
	}
	operation.LocalHost, operation.RemoteHost = own.Host, remote.Host
	if operation.Pair == "" {
		sum := sha256.Sum256([]byte("arm-connect:" + key + ":" + own.Host + ":" + remote.Host))
		operation.Pair = hex.EncodeToString(sum[:])
	}
	if err = a.guard.SaveLifecycle(operation); err != nil {
		return 1, err
	}
	failure := func(phase string, e error) (int, error) {
		operation.Phase = phase
		operation.Detail = fmt.Sprintf("%.1024s", e)
		_ = a.guard.SaveLifecycle(operation)
		return 1, model.Err("mesh_connect_incomplete", fmt.Sprintf("connection key %q, target %s, phase %s: %v; inspect with arm mesh lookup KEY and both peer records; retry exactly the same connect options/key", key, remote.Host, phase, e))
	}
	lip, rip := request.LocalIP, request.RemoteIP
	if lip == "" {
		lip = inferredLocal
	}
	if rip == "" {
		rip = inferredRemote
	}
	if lip == "" {
		lip = remote.Route.ClientIP
	}
	if rip == "" {
		rip = remote.Route.ServerIP
	}
	var lp, rp mesh.PreparedSetup
	prep := func(host, ip string, identity *mesh.Identity) mesh.PrepareRequest {
		q := mesh.PrepareRequest{Key: key, IP: ip, AllowPublic: request.Public, ExpectedHost: host}
		if identity != nil {
			q.ExpectedFingerprint = identity.Fingerprint
		}
		return q
	}
	if err = a.call("mesh_setup_prepare", prep(own.Host, lip, own.Identity), &lp); err != nil {
		return failure("local-prepare", err)
	}
	defer func() {
		if !lp.Existing {
			var ignored any
			_ = a.cleanupCall("mesh_setup_abort", map[string]string{"Key": key, "Token": lp.Token}, &ignored)
		}
	}()
	if err = ssh(Helper{Home: request.RemoteHome, Action: "lifecycle_prepare", Prepare: prep(remote.Host, rip, remote.Identity)}, &rp); err != nil {
		return failure("remote-prepare", err)
	}
	defer func() {
		if !rp.Existing {
			var ignored any
			_ = a.cleanupSSH(request.Destination, request.Binary, Helper{Home: request.RemoteHome, Action: "lifecycle_abort", Apply: mesh.ApplySetup{Key: key, Token: rp.Token}}, sshIdentity, &ignored)
		}
	}()
	plan.Local, plan.Remote = lifecycleNodePreview(own, remote.Host), lifecycleNodePreview(remote, own.Host)
	plan.LocalPrepared, plan.RemotePrepared = lifecyclePreparedPreview(lp), lifecyclePreparedPreview(rp)
	// The durable review binds identities, endpoints, grants and prior revisions,
	// not ephemeral reservation tokens or response timestamps.

	priorRevision := func(config *mesh.Config, host string) int {
		if config != nil {
			for _, peer := range config.Peers {
				if peer.Identity.Host == host {
					return peer.Revision
				}
			}
		}
		return 0
	}
	if operation.ReviewedDigest == "" {
		operation.ReviewedLocalRevision = priorRevision(own.Config, remote.Host)
		operation.ReviewedRemoteRevision = priorRevision(remote.Config, own.Host)
	}
	reviewed := struct {
		SSH                           meshssh.Identity
		Local, Remote                 mesh.Identity
		LocalListen, RemoteListen     string
		Forward, Reverse              mesh.Grant
		LocalRevision, RemoteRevision int
	}{sshIdentity, lp.Identity, rp.Identity, lp.Listen, rp.Listen, request.Forward, request.Reverse, operation.ReviewedLocalRevision, operation.ReviewedRemoteRevision}
	reviewDigest, _ := mesh.ReviewDigest(reviewed)
	if operation.ReviewedDigest != "" && operation.ReviewedDigest != reviewDigest {
		return failure("review-changed", model.Err("setup_changed", "pins/endpoints or prior trust changed; inspect before a fresh reviewed operation"))
	}
	if err = a.Confirm(plan, yes, false); err != nil {
		return 1, err
	}
	if err = a.markApproved(); err != nil {
		return 1, err
	}
	operation.ReviewedDigest = reviewDigest
	operation.LocalIdentity = &lp.Identity
	operation.RemoteIdentity = &rp.Identity
	operation.Phase = "reviewed"
	operation.Detail = ""
	if err = a.guard.SaveLifecycle(operation); err != nil {
		return 1, err
	}
	apply := func(p mesh.PreparedSetup) mesh.ApplySetup {
		return mesh.ApplySetup{Key: key, Token: p.Token, Fingerprint: p.Identity.Fingerprint, Listen: p.Listen}
	}
	var ignored any
	if err = a.call("mesh_setup_apply", apply(lp), &ignored); err != nil {
		return failure("local-listener", err)
	}
	if err = ssh(Helper{Home: request.RemoteHome, Action: "lifecycle_apply", Apply: apply(rp)}, &ignored); err != nil {
		return failure("remote-listener", err)
	}
	localPrior, remotePrior := operation.ReviewedLocalRevision, operation.ReviewedRemoteRevision
	pair := Bootstrap{ExpectedRevision: &remotePrior, Home: request.RemoteHome, Action: "pair", Identity: lp.Identity, Grant: request.Forward, Pair: operation.Pair, Reason: request.Reason, ReplaceRevoked: request.ReplaceRevoked}
	var targetPeer, sourcePeer mesh.Peer
	if err = ssh(pair, &targetPeer); err != nil {
		return failure("target-pair", err)
	}
	if err = a.call("mesh_pair", struct {
		Identity         mesh.Identity
		Grant            mesh.Grant
		Pair, Reason     string
		ReplaceRevoked   bool
		ExpectedRevision *int
	}{rp.Identity, request.Reverse, operation.Pair, request.Reason, request.ReplaceRevoked, &localPrior}, &sourcePeer); err != nil {
		return failure("source-pair", err)
	}
	operation.LocalRevision, operation.RemoteRevision = sourcePeer.Revision, targetPeer.Revision
	operation.Phase = "paired"
	if err = a.guard.SaveLifecycle(operation); err != nil {
		return failure("pair-journal", err)
	}
	if err = a.call("mesh_prove", map[string]string{"Host": remote.Host, "Pair": operation.Pair}, &ignored); err != nil {
		return failure("proof", err)
	}
	if err = a.call("mesh_activate", map[string]string{"Host": remote.Host, "Pair": operation.Pair}, nil); err != nil {
		return failure("local-activate", err)
	}
	if err = ssh(Bootstrap{Home: request.RemoteHome, Action: "activate", Host: own.Host, Pair: operation.Pair}, &ignored); err != nil {
		return failure("remote-activate", err)
	}
	var peers []mesh.Peer
	if err = a.call("mesh_peers", nil, &peers); err != nil {
		return failure("final-local-read", err)
	}
	for _, p := range peers {
		if p.Identity.Host == remote.Host {
			operation.LocalRevision = p.Revision
		}
	}
	if err = ssh(Bootstrap{Home: request.RemoteHome, Action: "peers"}, &peers); err != nil {
		return failure("final-remote-read", err)
	}
	for _, p := range peers {
		if p.Identity.Host == own.Host {
			operation.RemoteRevision = p.Revision
		}
	}
	operation.Phase = "connected"
	operation.Detail = ""
	if err = a.guard.SaveLifecycle(operation); err != nil {
		return failure("final-journal", err)
	}
	return 0, a.Print(map[string]any{"connected": remote.Host, "name": rp.Identity.Name, "key": key, "pair": operation.Pair, "local_identity": lp.Identity, "remote_identity": rp.Identity, "source_permissions_on_target": request.Forward, "target_permissions_on_source": request.Reverse})
}

func (a *Coordinator) disconnect(key, host, reason string, yes, dry bool) (int, error) {
	inspection, err := a.Inspect()
	if err != nil {
		return 1, err
	}
	if inspection.Config == nil {
		return 1, model.Err("mesh_unavailable", "node has no configured peer trust")
	}
	resolved, err := mesh.ResolveConfig(*inspection.Config, host)
	if err != nil {
		return 1, err
	}
	peer := &resolved
	connections, err := mesh.LifecycleConnections(a.Home)
	if err != nil {
		return 1, err
	}
	var connection *mesh.LifecycleOperation
	for _, c := range connections {
		if c.RemoteHost == peer.Identity.Host && c.Pair == peer.Pair {
			copy := c
			if connection == nil || connection.At.Before(copy.At) {
				connection = &copy
			}
		}
	}
	if connection == nil || connection.LocalIdentity == nil || connection.RemoteIdentity == nil || connection.LocalIdentity.Host != connection.LocalHost || connection.RemoteIdentity.Host != connection.RemoteHost {
		return 1, model.Err("connection_metadata_missing", "no automatic connection record for this enrollment; use mesh revoke locally and explicitly revoke the opposite node")
	}
	digest, _ := mesh.ReviewDigest(struct{ Host, Pair, Reason string }{peer.Identity.Host, peer.Pair, reason})
	op, exists, err := mesh.ReadLifecycleOperation(a.Home, key)
	if err != nil {
		return 1, err
	}
	if exists {
		if op.Kind != "disconnect" || op.Digest != digest {
			return 1, model.Err("idempotency_conflict", "disconnect key belongs to another request")
		}
	} else {
		op = *connection
		op.Key = key
		op.Kind = "disconnect"
		op.Digest = digest
		op.At = time.Now().UTC()
		op.Reason = reason
		op.Phase = "inspected"
	}
	plan := map[string]any{"operation": "disconnect-direct-peer", "key": key, "host": peer.Identity, "pair": op.Pair, "local_revision": op.LocalRevision, "remote_revision": op.RemoteRevision, "ssh_account": op.SSHAccount, "ssh_host_key": op.SSHHostKey, "effect": "immediately revoke this exact enrollment locally, close sessions, then revoke matching remote trust over verified SSH; existing leases keep expiry and cleanup obligations; other peers remain unchanged", "reason": reason}
	if a.guard == nil && !dry && !(a.Machine && !yes) {
		if prior, ok, e := mesh.ReadCoordination(a.Home, key); e != nil {
			return 1, e
		} else if ok {
			if prior.ParentKey != "" {
				return 1, model.Err("original_intent_required", "this key identifies a recovery attempt; retry the original immutable intent key, not its child receipt")
			}
			if prior.Kind != "disconnect" || prior.Digest != digest {
				return 1, model.Err("idempotency_conflict", "disconnect key belongs to another enrollment or reason")
			}
			if prior.Phase == "completed" {
				if peer.Pair != op.Pair || peer.Identity != *op.RemoteIdentity || peer.State != "revoked" {
					return 1, model.Err("setup_changed", "original disconnected enrollment has changed")
				}
				return 0, a.Print(prior.Result)
			}
			if prior.Phase == "incomplete" && prior.Worker == "" && prior.ResolvedBy == "" {
				a.attemptParent = key
			} else {
				return 1, model.Err("terminal_or_active_key", "disconnect receipt is terminal or active; use a fresh intent key")
			}
		}
	}
	if !dry && !(a.Machine && !yes) {
		if err = a.begin(key, "disconnect", digest, "host:"+peer.Identity.Host); err != nil {
			return 1, err
		}
		if err = a.recordRequest(StartRequest{Key: key, Kind: "disconnect", Disconnect: &DisconnectDraft{Host: peer.Identity.Host, Pair: peer.Pair, Reason: reason}}); err != nil {
			return 1, err
		}
	}
	if err = a.Confirm(plan, yes, dry); err != nil {
		return 1, err
	}
	if dry {
		return 0, nil
	}
	if err = a.markApproved(); err != nil {
		return 1, err
	}
	if err = a.guard.SaveLifecycle(op); err != nil {
		return 1, err
	}
	if !inspection.Running {
		if _, err = a.Ensure(inspection.Startup); err != nil {
			return 1, err
		}
	}
	var result any
	q := DisconnectRequest{Key: key, Host: op.RemoteHost, Pair: op.Pair, Fingerprint: op.RemoteIdentity.Fingerprint, Revision: op.LocalRevision, Reason: reason}
	if err = a.call("mesh_setup_revoke", q, &result); err != nil {
		return 1, model.Err("mesh_disconnect_incomplete", fmt.Sprintf("disconnect key %q target %s local outcome unresolved: %v; repeat exact host/reason/key", key, op.RemoteHost, err))
	}
	op.Phase = "local-disconnected"
	if err = a.guard.SaveLifecycle(op); err != nil {
		return 1, err
	}
	remoteQ := Helper{Home: op.RemoteHome, Action: "lifecycle_revoke", Revoke: DisconnectRequest{Key: key, Host: op.LocalHost, Pair: op.Pair, Fingerprint: op.LocalIdentity.Fingerprint, Revision: op.RemoteRevision, Reason: reason}}
	// Local denial is immediate. An unreachable SSH destination remains pending;
	// an available helper may restore its selected ordinary backend to commit revocation.
	_, err = a.ssh(op.SSHLocator, op.Binary, remoteQ, meshssh.Identity{Account: op.SSHAccount, HostKey: op.SSHHostKey}, &result)
	if err != nil {
		op.Phase = "remote-pending"
		if a.Context.Err() != nil {
			return 1, model.Err("mesh_disconnect_incomplete", fmt.Sprintf("disconnect key %q target %s: locally revoked, remote result unknown; retry exact host/reason/key", key, op.RemoteHost))
		}
		op.Detail = fmt.Sprintf("%.1024s", err)
		_ = a.guard.SaveLifecycle(op)
		return 0, a.Print(map[string]any{"local_disconnected": op.RemoteHost, "remote_pending": true, "key": key, "detail": op.Detail, "retry": "repeat disconnect with the same host, reason and --key"})
	}
	op.Phase = "disconnected"
	op.Detail = ""
	if err = a.guard.SaveLifecycle(op); err != nil {
		return 1, err
	}
	return 0, a.Print(map[string]any{"disconnected": op.RemoteHost, "remote_pending": false, "key": key})
}

func lifecycleIdentityPreview(identity mesh.Identity) map[string]any {
	return map[string]any{"host": identity.Host, "name": identity.Name, "owner_uid": identity.Owner, "owner_username": identity.OwnerUsername, "address": identity.Address, "certificate_sha256": identity.Fingerprint, "public_key_sha256": identity.SPKI, "certificate_expires": identity.Expires}
}
func lifecycleNodePreview(inspection mesh.LifecycleInspection, peerHost string) map[string]any {
	result := map[string]any{"running": inspection.Running, "host": inspection.Host, "home": inspection.Home, "os": inspection.OS, "arch": inspection.Arch, "version": inspection.Version, "owner": inspection.Owner, "configured_startup": inspection.Startup, "startup_provenance": inspection.StartupSource, "ssh_route": inspection.Route, "assigned_private_interfaces": inspection.PrivateIPs}
	if inspection.Identity != nil {
		result["identity"] = lifecycleIdentityPreview(*inspection.Identity)
	} else {
		result["identity_status"] = "not created; actual public pins will be shown after private preparation"
	}
	if inspection.Config != nil {
		for _, peer := range inspection.Config.Peers {
			if peer.Identity.Host == peerHost {
				result["prior_trust"] = map[string]any{"identity": lifecycleIdentityPreview(peer.Identity), "pair": peer.Pair, "revision": peer.Revision, "state": peer.State, "grant": peer.Grant}
			}
		}
	}
	return result
}
func lifecyclePreparedPreview(prepared mesh.PreparedSetup) map[string]any {
	return map[string]any{"identity": lifecycleIdentityPreview(prepared.Identity), "listen": prepared.Listen, "reservation_expires": prepared.Expires, "existing_listener": prepared.Existing}
}
