package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"syscall"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/meshstream"
	"getarm.org/internal/model"
	"getarm.org/internal/sshservice"
	"getarm.org/internal/usage"
	"golang.org/x/crypto/ssh"
)

type sshLocalAccount struct {
	Binding   sshservice.Binding `json:"binding"`
	Home      string             `json:"home"`
	StateHome string             `json:"state_home"`
}

func (a *app) sshAccount() (sshLocalAccount, error) {
	var v sshLocalAccount
	e := a.call("ssh_account", struct{}{}, &v)
	return v, e
}
func sshDigest(v []byte) string { h := sha256.Sum256(v); return hex.EncodeToString(h[:]) }
func sshKeyHash(v string) (string, error) {
	k, _, _, _, e := ssh.ParseAuthorizedKey([]byte(v))
	if e != nil {
		return "", e
	}
	return sshDigest(k.Marshal()), nil
}
func sshRPC(ctx context.Context, s *mesh.Store, target, op, key string, expected *mesh.ExpectedEnrollment, params, result any) error {
	raw, e := json.Marshal(params)
	if e != nil {
		return e
	}
	raw, e = s.Call(ctx, target, mesh.Request{Operation: op, Key: key, Expected: expected, Params: raw})
	if e != nil {
		return e
	}
	if status, ok := result.(*model.SSHKeyStatus); ok {
		return sshservice.DecodeKeyStatus(raw, status)
	}
	return mesh.Decode(raw, result)
}
func (a *app) sshCommand(args []string) (int, error) {
	if len(args) > 0 {
		switch args[0] {
		case "setup", "disable":
			return a.sshServiceCommand(args[0], args[1:])
		case "pending":
			return a.sshPending(args[1:])
		case "lookup", "seal":
			return a.sshRecover(args[0], args[1:])
		}
	}
	f := a.flags("ssh")
	reason := f.String("reason", "", "required execution purpose; command text is never persisted")
	ttl := f.Duration("ttl", 10*time.Minute, "destination execution lease lifetime")
	cpu := f.Float64("cpu", 1, "logical-core budget")
	memory := f.String("memory", "0", "memory reservation in exact bytes or units")
	disk := f.String("disk", "0", "disk reservation in exact bytes or units")
	cwd := f.String("cwd", "", "absolute destination directory (default destination account home)")
	key := f.String("key", "", "unique source intent key (existing keys are inspection-only)")
	workspaceID := f.String("workspace", "", "exact messaging workspace for SSH relay when the destination has no direct link")
	yes := f.Bool("yes", false, "accept concrete initial host-key pin review")
	update := f.Bool("update-pin", false, "allow separately reviewed changed enrollment/account/host-key pin")
	dry := f.Bool("dry-run", false, "review identity/admission without writes or execution")
	tty := f.Bool("t", false, "request a terminal (requires terminal stdin/stdout)")
	noTTY := f.Bool("T", false, "disable terminal allocation")
	f.BoolVar(&a.forceJSON, "json", a.forceJSON, "reject raw execution and use structured administrative output")
	command := ""
	hasCommand := false
	head := args
	for i, v := range args {
		if v == "--" {
			head = args[:i]
			if len(args[i+1:]) != 1 {
				return 2, fmt.Errorf("provide exactly one command string after --, or omit -- for a shell")
			}
			command = args[i+1]
			hasCommand = true
			break
		}
	}
	if e := a.parse(f, head, false); e != nil {
		return 2, e
	}
	if len(f.Args()) != 1 {
		return 2, fmt.Errorf("usage: arm ssh NODE --reason TEXT [OPTIONS] [-- COMMAND_STRING]")
	}
	if a.forceJSON && !*dry {
		return 2, model.Err("invalid_request", "SSH execution emits raw stdout/stderr; omit --json, or use administrative pending/lookup commands")
	}
	if *tty && *noTTY || *tty && (!a.inputTTY || !a.outputTTY) {
		return 2, model.Err("invalid_request", "-t requires terminal input/output and cannot combine with -T")
	}
	if strings.ContainsRune(command, 0) || len(command) > 32768 || hasCommand && command == "" {
		return 2, model.Err("invalid_request", "one nonempty command of at most32768 bytes without NUL required")
	}
	mem, e := sshBytes(*memory)
	if e != nil {
		return 2, e
	}
	ds, e := sshBytes(*disk)
	if e != nil {
		return 2, e
	}
	if *key == "" {
		*key = local.ID()
	}
	if !historyText(*key, 128) {
		return 2, model.Err("invalid_request", "valid exact SSH key required")
	}
	if e = a.sshSourcePrecheck(f.Arg(0), *key); e != nil {
		return 1, e
	}
	account, e := a.sshAccount()
	if e != nil {
		return 1, e
	}
	a.home = account.StateHome
	store, e := mesh.Open(a.home)
	if e != nil {
		return 1, e
	}
	defer store.Close()
	peer, e := store.Resolve(f.Args()[0])
	if *workspaceID != "" || e != nil {
		terminal := !*noTTY && a.inputTTY && a.outputTTY
		if *tty {
			terminal = true
		}
		return a.sshRoutedCommand(store, account, sshRoutedOptions{Target: f.Args()[0], Workspace: *workspaceID, Reason: *reason, Key: *key,
			CWD: *cwd, Command: command, TTL: *ttl, Resources: model.Resources{CPU: *cpu, Memory: mem, Disk: ds}, Terminal: terminal,
			HasCommand: hasCommand, Dry: *dry, UpdatePin: *update, Yes: *yes})
	}
	if peer.State != "active" || !peer.Proven {
		return 1, model.Err("forbidden", "SSH requires an active proven direct peer")
	}
	var scope model.NodeAccessScope
	if e = sshRPC(a.ctx, store, peer.Identity.Host, "access_scope", "", nil, struct{}{}, &scope); e != nil {
		return 1, e
	}
	expected := &mesh.ExpectedEnrollment{Pair: scope.Pair, Revision: scope.Revision}
	var identity model.SSHDestinationIdentity
	if e = sshRPC(a.ctx, store, peer.Identity.Host, "ssh_identity", "", expected, struct{}{}, &identity); e != nil {
		return 1, e
	}
	if identity.Schema != 1 || identity.Source != account.Binding.Host || identity.Target != peer.Identity.Host || identity.Pair != scope.Pair || identity.Revision != scope.Revision || identity.Certificate != peer.Identity.Fingerprint || identity.SPKI != peer.Identity.SPKI || identity.Owner != peer.Identity.Owner || identity.Account != peer.Identity.OwnerUsername {
		return 1, model.Err("ssh_identity_changed", "destination SSH identity differs from pinned mesh identity")
	}
	if *cwd == "" {
		*cwd = identity.Home
	}
	kind := "shell"
	if hasCommand {
		kind = "exec"
	}
	terminal := !*noTTY && (a.inputTTY && a.outputTTY)
	if *tty {
		terminal = true
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
	control := meshstream.Control{Version: 1, Source: account.Binding.Host, Target: identity.Target, Pair: identity.Pair, Revision: identity.Revision, Account: identity.Account, Key: *key, PublicKey: pub, Reason: *reason, TTL: *ttl, Resources: model.Resources{CPU: *cpu, Memory: mem, Disk: ds}, CWD: *cwd, TargetOwner: identity.Owner, HostKeySHA256: hostHash, Kind: kind, CommandSHA256: sshDigest([]byte(command)), PTY: terminal}
	if e = control.Validate(); e != nil {
		return 2, e
	}
	if *ttl > time.Duration(identity.MaxTTL) || !control.Resources.Fits(identity.Limits) {
		return 1, model.Err("capacity_exceeded", "requested SSH lifetime/resources exceed current directional destination grant")
	}
	pin := sshservice.Pin{Schema: 1, Source: account.Binding.Host, Target: identity.Target, Pair: identity.Pair, Revision: identity.Revision, TLSFingerprint: identity.Certificate, TLSSPKI: identity.SPKI, Account: identity.Account, PublicKey: identity.PublicKey, At: time.Now().UTC()}
	pr, e := sshservice.ReviewPin(filepath.Join(a.home, "mesh"), pin)
	if e != nil {
		return 1, e
	}
	same := false
	if pr.Before != nil {
		old := *pr.Before
		next := pin
		next.At = old.At
		same = reflect.DeepEqual(old, next)
		if same {
			pin = old
		}
	}
	plan := map[string]any{"operation": "SSH execution", "target": identity.Target, "account": identity.Account, "uid": identity.Owner, "host_key": identity.PublicKey, "before_pin": pr.Before, "after_pin": pin, "key": *key, "reason": *reason, "ttl": ttl.String(), "resources": control.Resources, "cwd": *cwd, "kind": kind, "command_sha256": control.CommandSHA256, "terminal": terminal, "effect": "A fresh destination-owned execution lease; same-account code execution, not a sandbox. No forwarding or file grants. Unknown outcomes require exact lookup; commands are never automatically retried."}
	if *dry {
		return 0, a.print(plan)
	}
	if !same {
		if pr.Before != nil && !*update {
			return 1, model.Err("ssh_pin_changed", "destination enrollment/account/key changed; inspect --dry-run then explicitly use --update-pin")
		}
		if e = a.meshConfirm(plan, *yes, false); e != nil {
			return 1, e
		}
		if e = sshservice.CommitStatePin(a.ctx, a.home, account.Binding, pr); e != nil {
			return 1, e
		}
	}
	return a.sshExecute(store, peer, account, control, pin, signer, command, hasCommand)
}
func (a *app) sshServiceCommand(action string, args []string) (int, error) {
	f := a.flags("ssh " + action)
	reason := f.String("reason", "", "required purpose")
	yes := f.Bool("yes", false, "accept concrete service/key change")
	dry := f.Bool("dry-run", false, "review without writes")
	if e := a.parse(f, args, false); e != nil {
		return 2, e
	}
	account, e := a.sshAccount()
	if e != nil {
		return 1, e
	}
	review, e := sshservice.ReviewService(a.ctx, account.StateHome, account.Binding, action == "setup", *reason)
	if e != nil {
		return 1, e
	}
	plan := map[string]any{"operation": action, "before": review.Before, "after": review.After, "effect": "Explicit destination SSH service identity; ssh-exec plus lease-own grants remain required. Same-account code execution, not a sandbox. Disabling closes current SSH streams; admitted leases retain reconciliation obligations."}
	if e = a.meshConfirm(plan, *yes, *dry); e != nil {
		return 1, e
	}
	if *dry {
		return 0, nil
	}
	if e = sshservice.ApplyService(a.ctx, account.StateHome, account.Binding, review); e != nil {
		return 1, e
	}
	if action == "disable" {
		var result any
		if e = a.call("ssh_close", struct {
			Revision uint64 `json:"revision"`
		}{review.After.Revision}, &result); e != nil {
			return 1, model.Err("ssh_outcome_unknown", fmt.Sprintf("service revision%d is durably disabled but stream-close acknowledgement unavailable: %v", review.After.Revision, e))
		}
	}
	return 0, a.print(review.After)
}
func (a *app) sshOfflineState() (*sshservice.State, string, sshservice.Binding, error) {
	home, _, binding, e := a.sshOfflineAuthority()
	if e != nil {
		return nil, "", binding, e
	}

	s, e := sshservice.ReadState(a.ctx, home, binding)
	return s, home, binding, e
}
func (a *app) sshPending(args []string) (int, error) {
	f := a.flags("ssh pending")
	if e := a.parse(f, args, false); e != nil {
		return 2, e
	}
	s, _, _, e := a.sshOfflineState()
	if e != nil {
		return 1, e
	}
	return 0, a.print(struct {
		Pending   []sshservice.SourceIntent `json:"pending"`
		Completed int                       `json:"completed"`
		Sealed    int                       `json:"sealed"`
	}{s.Intents, len(s.Completions), len(s.Seals)})
}
func (a *app) sshRecover(action string, args []string) (int, error) {
	f := a.flags("ssh " + action)
	key := f.String("key", "", "exact original key")
	reason := f.String("reason", "", "required nonexecution seal purpose")
	yes := f.Bool("yes", false, "confirm exact nonexecution seal")
	dry := f.Bool("dry-run", false, "review without dispatch")
	cleanup := f.Bool("cleanup", false, "explicitly review exact artifact cleanup after terminal/nonexecution proof and original worker exit")
	if e := a.parse(f, args, false); e != nil {
		return 2, e
	}
	if len(f.Args()) != 1 || *key == "" {
		return 2, fmt.Errorf("NODE and exact --key required")
	}
	state, home, b, e := a.sshOfflineState()
	if e != nil {
		return 1, e
	}
	_, registry, _, e := a.sshOfflineAuthority()
	if e != nil {
		return 1, e
	}
	localTarget, e := historyTarget(registry, f.Arg(0))
	if e != nil {
		return 1, e
	}
	if action == "lookup" {
		receipt, e := sshservice.LookupSourceHistory(a.ctx, home, b, localTarget, *key)
		if e != nil {
			return 1, model.Err("ssh_evidence_unavailable", e.Error())
		}
		if receipt.Found {
			if *cleanup {
				return 1, model.Err("invalid_request", "local settled evidence is read-only; no pending artifact cleanup is available")
			}
			return 0, a.print(struct {
				Source  string                         `json:"source"`
				Receipt sshservice.SourceHistoryLookup `json:"receipt"`
			}{"local", receipt})
		}
	}
	store, e := mesh.Open(home)
	if e != nil {
		return 1, e
	}
	defer store.Close()
	var intent *sshservice.SourceIntent
	for _, r := range state.Intents {
		if r.Request.Pin.Target == localTarget && r.Request.Key == *key {
			copy := r
			intent = &copy
		}
	}
	if intent == nil {
		return 1, model.Err("not_found", "exact pending source intent required; no execution retry is available")
	}
	original := intent.Request.Pin
	var peer mesh.Peer
	if original.RouteWorkspace == "" {
		peer, e = store.Resolve(localTarget)
		if e != nil {
			return 1, e
		}
		if original.Pair != peer.Pair || original.TLSFingerprint != peer.Identity.Fingerprint || original.TLSSPKI != peer.Identity.SPKI {
			return 1, model.Err("ssh_context_changed", "original target pin/enrollment changed; preserve pending evidence for destination operator inspection")
		}
	}
	expected := &mesh.ExpectedEnrollment{Pair: original.Pair, Revision: original.Revision}
	var status model.SSHKeyStatus
	if action == "seal" {
		control, e := intent.ControlCommitment()
		if e != nil {
			return 1, e
		}
		if strings.TrimSpace(*reason) == "" {
			return 2, model.Err("invalid_request", "reason required")
		}
		plan := map[string]any{"operation": "seal SSH key against late execution", "control": control, "reason": *reason, "effect": "If absent, destination atomically prevents this exact key from ever executing. Existing admitted work is inspected, never killed or converted to nonexecution. Lost acknowledgement remains lookup-only."}
		if e = a.meshConfirm(plan, *yes, *dry); e != nil {
			return 1, e
		}
		if *dry {
			return 0, nil
		}
		if original.RouteWorkspace != "" {
			status, e = a.recoverRoutedSSH(store, *intent, "seal", control, *reason)
		} else {
			e = sshRPC(a.ctx, store, peer.Identity.Host, "ssh_key_seal", *key, expected, struct {
				Control model.SSHControlCommitment `json:"control"`
				Reason  string                     `json:"reason"`
			}{control, *reason}, &status)
		}
		if e != nil {
			return 1, sshUnknown(*intent, e)
		}
	} else {
		if original.RouteWorkspace != "" {
			status, e = a.recoverRoutedSSH(store, *intent, "lookup", model.SSHControlCommitment{}, "")
		} else {
			e = sshRPC(a.ctx, store, peer.Identity.Host, "ssh_key_status", "", expected, struct {
				Key string `json:"key"`
			}{*key}, &status)
		}
		if e != nil {
			return 1, e
		}
	}
	if e = sshservice.ValidateKeyStatus(*intent, status); e != nil {
		return 1, sshUnknown(*intent, e)
	}
	if *dry && !*cleanup {
		return 0, a.print(status)
	}
	if *cleanup {
		receipt, inspectErr := sshservice.ArtifactReceipt(a.ctx, *intent)
		if inspectErr != nil {
			return 1, inspectErr
		}
		if e = a.meshConfirm(map[string]any{"operation": "remove exact stopped SSH invocation artifacts", "receipt": receipt, "destination_status": status, "effect": "Only the fixed public files and socket entries at exact recorded identities are removed. Original worker must be proved absent; changed/partial artifacts remain preserved."}, *yes, *dry); e != nil {
			return 1, e
		}
		if *dry {
			return 0, nil
		}
		if e = sshservice.RecoverArtifacts(a.ctx, home, b, *intent, status); e != nil {
			return 1, e
		}
	}
	// Retain uncertainty while invocation artifacts exist. This read never guesses
	// cleanup from process exit or initiates any network mutation on its own.
	if status.Source != intent.Source || status.Target != original.Target || status.Key != *key {
		return 1, model.Err("ssh_evidence_unavailable", "destination key status correlation failed")
	}
	if _, e = os.Lstat(intent.Directory.Path); os.IsNotExist(e) {
		e = nil // Absence is artifact state only; it is not destination settlement.
		if status.State == "sealed" && status.Fence != nil {
			e = sshservice.SettleSourceSeal(a.ctx, home, b, *intent, *status.Fence)
		} else if status.Execution != nil && status.Execution.Lease != nil && status.Execution.Lease.State == "terminal" {
			e = sshservice.SettleSource(a.ctx, home, b, *intent, *status.Execution)
		}
		if e != nil {
			return 1, sshUnknown(*intent, e)
		}
	}
	return 0, a.print(status)
}
func sshUnknown(intent sshservice.SourceIntent, e error) error {
	return model.Err("ssh_outcome_unknown", fmt.Sprintf("SSH target %s key %q remains pending/unknown: %v. Use arm ssh pending and explicit lookup; never repeat execution with another key", intent.Request.Pin.Target, intent.Request.Key, e))
}

var sshQuantityPattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)(B|KiB|MiB|GiB|TiB|KB|MB|GB|TB)?$`)

func sshBytes(v string) (int64, error) {
	m := sshQuantityPattern.FindStringSubmatch(v)
	if m == nil {
		return 0, model.Err("invalid_request", "exact nonnegative byte quantity required")
	}
	n, ok := new(big.Rat).SetString(m[1])
	if !ok {
		return 0, fmt.Errorf("invalid bytes")
	}
	scale := map[string]int64{"": 1, "B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40, "KB": 1000, "MB": 1000000, "GB": 1000000000, "TB": 1000000000000}[m[2]]
	n.Mul(n, new(big.Rat).SetInt64(scale))
	if !n.IsInt() || !n.Num().IsInt64() {
		return 0, model.Err("invalid_request", "byte quantity must be a whole number within int64")
	}
	return n.Num().Int64(), nil
}

// Hidden helper accepts only a private socket, never network/node/command data.
func sshProxyMain(args []string, in io.Reader, out, errout io.Writer) int {
	if len(args) != 2 || args[0] != "--socket" {
		fmt.Fprintln(errout, "private SSH socket required")
		return 2
	}
	input, ok := in.(io.ReadCloser)
	if !ok {
		fmt.Fprintln(errout, "private SSH proxy requires closable stdin")
		return 2
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e := sshservice.Proxy(ctx, args[1], input, out); e != nil {
		fmt.Fprintln(errout, "SSH private proxy closed:", e)
		return 1
	}
	return 0
}

func (a *app) sshExecute(store *mesh.Store, peer mesh.Peer, account sshLocalAccount, control meshstream.Control, pin sshservice.Pin, signer *sshservice.UserSigner, command string, hasCommand bool) (int, error) {
	return a.sshExecuteWithTransport(account, control, pin, signer, command, hasCommand, store.Config().Identity.Fingerprint,
		func(ctx context.Context) (net.Conn, error) {
			policy, e := store.ClientTLS(peer)
			if e != nil {
				return nil, e
			}
			return meshstream.Dial(ctx, peer.Identity.Address, policy, control, meshstream.DefaultLimits())
		}, func(ctx context.Context) (model.SSHKeyStatus, error) {
			var status model.SSHKeyStatus
			e := sshRPC(ctx, store, control.Target, "ssh_key_status", "", &mesh.ExpectedEnrollment{Pair: control.Pair, Revision: control.Revision}, struct {
				Key string `json:"key"`
			}{control.Key}, &status)
			return status, e
		})
}

func (a *app) sshExecuteWithTransport(account sshLocalAccount, control meshstream.Control, pin sshservice.Pin, signer *sshservice.UserSigner,
	command string, hasCommand bool, sourceCertificate string, dial func(context.Context) (net.Conn, error), lookup func(context.Context) (model.SSHKeyStatus, error)) (int, error) {
	binary, e := exec.LookPath("ssh")
	if e != nil {
		return 1, e
	}
	runtime, e := mesh.CaptureRuntimeExecutable()
	if e != nil {
		return 1, e
	}
	defer runtime.Close()
	self, e := runtime.VerifiedPath()
	if e != nil {
		return 1, e
	}
	worker, e := usage.Capture(os.Getpid())
	if e != nil {
		return 1, e
	}
	dir, e := sshservice.CreateInvocationDirectory(a.ctx, account.StateHome, account.Binding)
	if e != nil {
		return 1, e
	}
	intent := sshservice.SourceIntent{Schema: 1, Source: control.Source, Owner: account.Binding.Owner, Request: sshservice.SourceRequest{Key: control.Key, Pin: pin, TargetOwner: control.TargetOwner, SourceCertificate: sourceCertificate, PublicKey: control.PublicKey, Reason: control.Reason, TTL: control.TTL, Resources: control.Resources, CWD: control.CWD, Kind: control.Kind, CommandSHA256: control.CommandSHA256, PTY: control.PTY}, Worker: worker, Directory: dir, Created: time.Now().UTC()}
	if e = sshservice.ReserveSource(a.ctx, account.StateHome, account.Binding, intent); e != nil {
		if refusal, definite := sshReservationRefusal(a.ctx, intent, e); definite {
			return 1, refusal
		}

		return 1, sshUnknown(intent, e)
	}
	artifacts, e := sshservice.PrepareArtifacts(a.ctx, account.StateHome, account.Binding, intent)
	if e != nil {
		return 1, sshUnknown(intent, e)
	}
	defer artifacts.Close()
	args, e := sshservice.OpenSSHArguments(sshservice.Invocation{Executable: self, ProxySocket: artifacts.Path("proxy.sock"), AgentSocket: artifacts.Path("agent.sock"), PublicIdentity: artifacts.Path("identity.pub"), KnownHosts: artifacts.Path("known_hosts"), Host: control.Target, Account: control.Account, Terminal: control.PTY})
	if e != nil {
		return 1, sshUnknown(intent, e)
	}
	if hasCommand {
		args = append(args, command)
	}
	ctx, cancel := context.WithTimeout(a.ctx, control.TTL+15*time.Second)
	defer cancel()
	agentDone := make(chan struct{})
	go func() {
		defer close(agentDone)
		ctx, done := context.WithTimeout(ctx, 30*time.Second)
		defer done()
		c, e := artifacts.Accept(ctx, "agent.sock")
		if e == nil {
			defer c.Close()
			_ = sshservice.ServeAgentConnection(ctx, c, signer)
		}
	}()
	streamDone := make(chan error, 1)
	go func() {
		c, e := artifacts.Accept(ctx, "proxy.sock")
		if e != nil {
			streamDone <- e
			return
		}
		defer c.Close()
		remote, e := dial(ctx)
		if e != nil {
			streamDone <- e
			return
		}
		defer remote.Close()
		upload := make(chan struct{})
		go func() { _, _ = io.CopyBuffer(remote, c, make([]byte, 32<<10)); close(upload) }()
		_, e = io.CopyBuffer(c, remote, make([]byte, 32<<10))
		c.Close()
		remote.Close()
		select {
		case <-upload:
		case <-ctx.Done():
		}
		streamDone <- e
	}()
	if _, e = runtime.VerifiedPath(); e != nil {
		return 1, sshUnknown(intent, e)
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = a.in
	cmd.Stdout = a.out
	cmd.Stderr = a.err
	cmd.Env = sshEnvironment(os.Environ())
	cmd.WaitDelay = 2 * time.Second
	runErr := cmd.Start()
	if runErr == nil {
		resize := make(chan os.Signal, 1)
		signal.Notify(resize, syscall.SIGWINCH)
		stopped := make(chan struct{})
		joined := make(chan struct{})
		go func() {
			defer close(joined)
			for {
				select {
				case <-stopped:
					return
				case <-resize:
					if cmd.Process != nil {
						_ = cmd.Process.Signal(syscall.SIGWINCH)
					}
				}
			}
		}()
		runErr = cmd.Wait()
		signal.Stop(resize)
		close(stopped)
		<-joined
	}
	code := 0
	if runErr != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			code = exit.ExitCode()
			if code < 0 {
				code = 1
			}
		} else {
			code = 1
		}
	}
	cancel()
	artifacts.Close()
	select {
	case <-agentDone:
	case <-time.After(time.Second):
	}
	select {
	case <-streamDone:
	case <-time.After(time.Second):
	}
	lookupCtx, lookupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer lookupCancel()
	status, e := lookup(lookupCtx)
	lookupErr := e
	if lookupErr == nil {
		if e = sshservice.ValidateKeyStatus(intent, status); e != nil {
			return code, sshUnknown(intent, e)
		}
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanupCancel()
	// These are our own closed runtime handles, not recovered artifacts. A lost
	// network response does not prevent their exact cleanup, and never settles
	// the source. A received contradictory envelope preserves all evidence.
	if e = artifacts.Cleanup(cleanupCtx); e != nil {
		return code, sshUnknown(intent, e)
	}
	if lookupErr != nil || status.Execution == nil {
		return code, sshUnknown(intent, fmt.Errorf("exact terminal receipt unavailable after OpenSSH exit: %v", lookupErr))
	}
	if e = sshservice.SettleSource(cleanupCtx, account.StateHome, account.Binding, intent, *status.Execution); e != nil {
		return code, sshUnknown(intent, e)
	}
	return code, nil
}
func sshEnvironment(env []string) []string {
	out := []string{}
	for _, v := range env {
		name, _, _ := strings.Cut(v, "=")
		switch name {
		case "SSH_AUTH_SOCK", "SSH_AGENT_PID", "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE", "DISPLAY":
			continue
		}
		out = append(out, v)
	}
	return out
}
