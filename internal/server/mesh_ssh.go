package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshstream"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sshservice"
	"golang.org/x/crypto/ssh"
)

type sshDestinationSnapshot struct {
	state *sshservice.State
	key   ssh.Signer
	home  string
}

func sshPublicHash(k ssh.PublicKey) string {
	h := sha256.Sum256(k.Marshal())
	return hex.EncodeToString(h[:])
}

// Snapshot is read-only: neither stream admission nor inventory creates service
// files, keys, or a listener. Setup is a separately reviewed native operation.
func (m *meshService) sshSnapshot(ctx context.Context) (sshDestinationSnapshot, error) {
	var out sshDestinationSnapshot
	binding, home, e := m.owner.Manager.SSHAccount(ctx)
	if e != nil {
		return out, e
	}
	stateHome, e := filepath.EvalSymlinks(m.owner.home)
	if e != nil {
		return out, e
	}
	state, e := sshservice.ReadState(ctx, stateHome, binding)
	if e != nil {
		return out, e
	}
	if state.Service == nil || !state.Service.Enabled {
		return out, model.Err("ssh_unavailable", "destination SSH service is not explicitly enabled")
	}
	key, e := sshservice.LoadHostKey(filepath.Join(stateHome, "mesh"), binding.Host)
	if e != nil {
		return out, e
	}
	if strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key.PublicKey()))) != state.Service.PublicKey {
		return out, model.Err("ssh_unavailable", "destination SSH host key changed during inventory")
	}
	return sshDestinationSnapshot{state, key, home}, nil
}

// Caller may already hold Store.WithPeer. Never recursively read Store.Config
// here: a queued revocation writer could deadlock a nested read lock.
func (m *meshService) sshScope(ctx context.Context, peer mesh.Peer) (meshstream.Scope, sshDestinationSnapshot, error) {
	var none meshstream.Scope
	if peer.State != "active" || !peer.Proven || !peer.Grant.Has("ssh-exec") || !peer.Grant.Has("lease-own") {
		return none, sshDestinationSnapshot{}, model.Err("forbidden", "active proven direct ssh-exec and lease-own grants required")
	}
	remote, e := mesh.ValidateIdentity(peer.Identity)
	if e != nil {
		return none, sshDestinationSnapshot{}, e
	}
	snapshot, e := m.sshSnapshot(ctx)
	if e != nil {
		return none, snapshot, e
	}
	localIdentity := snapshot.state.MeshIdentity()
	localCert, e := mesh.ValidateIdentity(localIdentity)
	if e != nil {
		return none, snapshot, e
	}
	until := remote.NotAfter
	if localCert.NotAfter.Before(until) {
		until = localCert.NotAfter
	}
	b := snapshot.state.Binding
	scope := meshstream.Scope{Source: peer.Identity.Host, Target: b.Host, Pair: peer.Pair, Account: b.Account, Certificate: peer.Identity.Fingerprint, Revision: peer.Revision, MaxTTL: peer.Grant.MaxTTL, Limits: peer.Grant.Limits, ValidUntil: until, Owner: b.Owner, HostKeySHA256: sshPublicHash(snapshot.key.PublicKey()), ServiceRevision: snapshot.state.Service.Revision}
	return scope, snapshot, nil
}
func (m *meshService) revalidateSSH(ctx context.Context, expected meshstream.Scope) error {
	_, e := m.store.WithPeer(expected.Source, func(peer mesh.Peer) (any, error) {
		actual, _, e := m.sshScope(ctx, peer)
		if e != nil {
			return nil, e
		}
		if actual != expected {
			return nil, model.Err("ssh_context_changed", "SSH authority, account, key, service or grant changed")
		}
		return nil, nil
	})
	return e
}
func (m *meshService) newSSHHandler() *meshstream.Handler {
	return &meshstream.Handler{Limits: meshstream.DefaultLimits(), Authorize: func(ctx context.Context, raw []byte) (meshstream.Scope, error) {
		var out meshstream.Scope
		peer, e := m.store.PeerByCert(raw)
		if e != nil {
			return out, e
		}
		_, e = m.store.WithPeer(peer.Identity.Host, func(current mesh.Peer) (any, error) {
			if current.Identity.Fingerprint != peer.Identity.Fingerprint {
				return nil, model.Err("forbidden", "SSH source certificate changed")
			}
			var e error
			out, _, e = m.sshScope(ctx, current)
			return nil, e
		})
		return out, e
	}, Revalidate: m.revalidateSSH, Handle: func(ctx context.Context, scope meshstream.Scope, control meshstream.Control, conn net.Conn) {
		snapshot, e := m.sshSnapshot(ctx)
		if e != nil || snapshot.state.Service.Revision != scope.ServiceRevision || sshPublicHash(snapshot.key.PublicKey()) != scope.HostKeySHA256 {
			return
		}
		key, _, _, _, e := ssh.ParseAuthorizedKey([]byte(control.PublicKey))
		if e != nil {
			return
		}
		service := sshservice.Server{HostKey: snapshot.key, Handshake: 10 * time.Second, StartTimeout: 10 * time.Second, Revalidate: func(ctx context.Context) error { return m.revalidateSSH(ctx, scope) }, Execute: func(ctx context.Context, execution sshservice.Execution, ch ssh.Channel) (sshservice.Process, error) {
			var process sshservice.Process
			_, e := m.store.WithPeer(scope.Source, func(peer mesh.Peer) (any, error) {
				actual, _, e := m.sshScope(ctx, peer)
				if e != nil {
					return nil, e
				}
				if actual != scope {
					return nil, model.Err("ssh_context_changed", "SSH enrollment/service changed before exec")
				}
				if e = control.Authorize(actual); e != nil {
					return nil, e
				}
				commitment, e := control.Commitment(actual.Certificate)
				if e != nil {
					return nil, e
				}
				commandHash := sha256.Sum256([]byte(execution.Command))
				terminalHash := ""
				if execution.Terminal != nil {
					raw, _ := json.Marshal(execution.Terminal)
					h := sha256.Sum256(raw)
					terminalHash = hex.EncodeToString(h[:])
				}
				q := model.SSHExecutionRequest{Source: scope.Source, Target: scope.Target, Pair: scope.Pair, Revision: scope.Revision, Certificate: scope.Certificate, Account: scope.Account, Key: control.Key, PublicKeySHA256: sshPublicHash(key), Reason: control.Reason, TTL: control.TTL, Resources: control.Resources, CWD: control.CWD, Kind: execution.Kind, CommandSHA256: hex.EncodeToString(commandHash[:]), TerminalSHA256: terminalHash}
				if !commitment.Matches(q) {
					return nil, model.Err("invalid_request", "SSH exec/shell/PTY differs from exact control")
				}
				_, process, e = m.owner.Manager.AcquireSSHCommitted(ctx, q, execution, ch, actual.Limits, actual.MaxTTL, actual.ValidUntil, commitment)
				if e != nil {
					return nil, e
				}
				if process == nil {
					return nil, model.Err("ssh_key_exists", "SSH key already submitted; inspect exact outcome without retry")
				}
				return nil, nil
			})
			return process, e
		}}
		_ = service.Serve(ctx, conn, sshservice.Permit{Account: scope.Account, PublicKey: key})
	}}
}
