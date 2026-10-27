package cli

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"getarm.org/internal/mesh"
	"getarm.org/internal/meshsshrelay"
	"getarm.org/internal/meshstream"
	"getarm.org/internal/model"
	"getarm.org/internal/sshservice"
	"golang.org/x/crypto/ssh"
)

type sshRoutedOptions struct {
	Target, Workspace, Reason, Key, CWD, Command string
	TTL                                          time.Duration
	Resources                                    model.Resources
	Terminal, HasCommand, Dry, UpdatePin, Yes    bool
}

func (a *app) sshRoutedCommand(store *mesh.Store, account sshLocalAccount, opt sshRoutedOptions) (int, error) {
	selection, e := a.selectSSHRoute(a.ctx, store, opt.Target, opt.Workspace)
	if e != nil {
		return 1, e
	}
	identity := selection.identity
	access := selection.access
	if identity.Schema != 1 || identity.Source != account.Binding.Host || identity.Target != opt.Target ||
		identity.Certificate != access.Destination.Fingerprint || identity.SPKI != access.Destination.SPKI ||
		identity.Owner == "" || identity.Account == "" || identity.Pair == "" || identity.Revision < 1 {
		return 1, model.Err("ssh_identity_changed", "routed destination SSH identity differs from pinned workspace endpoints")
	}
	if opt.CWD == "" {
		opt.CWD = identity.Home
	}
	kind := "shell"
	if opt.HasCommand {
		kind = "exec"
	}
	signer, e := sshservice.NewUserSigner(identity.Account, 30*time.Second)
	if e != nil {
		return 1, e
	}
	defer signer.Close()
	pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	hostHash, e := sshKeyHash(identity.PublicKey)
	if e != nil {
		return 1, e
	}
	control := meshstream.Control{Version: 1, Source: account.Binding.Host, Target: identity.Target, Pair: identity.Pair,
		Revision: identity.Revision, Account: identity.Account, Key: opt.Key, PublicKey: pub, Reason: opt.Reason, TTL: opt.TTL,
		Resources: opt.Resources, CWD: opt.CWD, TargetOwner: identity.Owner, HostKeySHA256: hostHash,
		Kind: kind, CommandSHA256: sshDigest([]byte(opt.Command)), PTY: opt.Terminal}
	if e = control.Validate(); e != nil {
		return 2, e
	}
	if opt.TTL > time.Duration(identity.MaxTTL) || !control.Resources.Fits(identity.Limits) {
		return 1, model.Err("capacity_exceeded", "routed SSH execution exceeds destination original-source grant")
	}
	pin := sshservice.Pin{Schema: 1, Source: account.Binding.Host, Target: identity.Target, Pair: identity.Pair, Revision: identity.Revision,
		TLSFingerprint: identity.Certificate, TLSSPKI: identity.SPKI, Account: identity.Account, PublicKey: identity.PublicKey, At: time.Now().UTC(),
		RouteWorkspace: access.Workspace, RouteGeneration: access.Generation,
		RouteDirectoryPin: access.Directory.Fingerprint, RouteDirectorySPKI: access.Directory.SPKI,
		RouteSourceGeneration: access.OriginGeneration, RouteTargetGeneration: access.DestinationGeneration}
	review, e := sshservice.ReviewPin(filepath.Join(a.home, "mesh"), pin)
	if e != nil {
		return 1, e
	}
	same := false
	if review.Before != nil {
		old := *review.Before
		next := pin
		next.At = old.At
		same = reflect.DeepEqual(old, next)
		if same {
			pin = old
		}
	}
	plan := map[string]any{"operation": "routed SSH execution", "workspace": access.Workspace, "generation": access.Generation,
		"route": selection.path, "source": access.Origin, "target": access.Destination, "account": identity.Account, "uid": identity.Owner,
		"host_key": identity.PublicKey, "before_pin": review.Before, "after_pin": pin, "key": opt.Key, "reason": opt.Reason,
		"ttl": opt.TTL.String(), "resources": control.Resources, "cwd": opt.CWD, "kind": kind, "command_sha256": control.CommandSHA256,
		"terminal": opt.Terminal, "effect": "Original-source destination lease through reviewed opaque SSH relays; same-account execution. Unknown outcomes require original-key lookup, never a new-key retry."}
	if opt.Dry {
		return 0, a.print(plan)
	}
	if !same {
		if review.Before != nil && !opt.UpdatePin {
			return 1, model.Err("ssh_pin_changed", "routed destination grant/account/host-key pin changed; review --dry-run then explicitly use --update-pin")
		}
		if e = a.meshConfirm(plan, opt.Yes, false); e != nil {
			return 1, e
		}
		if e = sshservice.CommitStatePin(a.ctx, a.home, account.Binding, review); e != nil {
			return 1, e
		}
	}
	dial := func(ctx context.Context) (net.Conn, error) {
		response, conn, e := selection.request(ctx, meshsshrelay.ClassSSH, hostHash, meshsshrelay.Request{Version: meshsshrelay.Version, Operation: meshsshrelay.OperationExecute, Control: &control})
		if e != nil {
			return nil, e
		}
		if !response.Accepted {
			_ = conn.Close()
			return nil, fmt.Errorf("routed SSH destination did not admit exact stream")
		}
		return conn, nil
	}
	lookup := func(ctx context.Context) (model.SSHKeyStatus, error) {
		response, conn, e := selection.request(ctx, meshsshrelay.ClassSSH, hostHash, meshsshrelay.Request{Version: meshsshrelay.Version, Operation: meshsshrelay.OperationLookup, Key: control.Key})
		if conn != nil {
			_ = conn.Close()
		}
		if e != nil {
			return model.SSHKeyStatus{}, e
		}
		if response.Status == nil {
			return model.SSHKeyStatus{}, fmt.Errorf("routed SSH exact key status missing")
		}
		return *response.Status, nil
	}
	return a.sshExecuteWithTransport(account, control, pin, signer, opt.Command, opt.HasCommand, store.Config().Identity.Fingerprint, dial, lookup)
}
