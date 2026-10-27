package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshstream"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
	"github.com/inferal-oss/arm/internal/usage"
	"golang.org/x/crypto/ssh"
)

type publicSSHFixture struct {
	source, target *Server
	left, right    *mesh.Store
	identity       model.SSHDestinationIdentity
	peer           mesh.Peer
	signer         ssh.Signer
}

func newPublicSSHFixture(t *testing.T) *publicSSHFixture {
	t.Helper()
	grant := mesh.Grant{Permissions: []string{"ssh-exec", "lease-own"}, Limits: model.Resources{CPU: 1}, MaxTTL: time.Minute}
	a, b, left, right := registryPair(t, grant, grant)
	binding, _, e := b.Manager.SSHAccount(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	stateHome, e := filepath.EvalSymlinks(b.home)
	if e != nil {
		t.Fatal(e)
	}
	review, e := sshservice.ReviewService(t.Context(), stateHome, binding, true, "Explicit private public-path SSH fixture")
	if e != nil {
		t.Fatal(e)
	}
	if e = sshservice.ApplyService(t.Context(), stateHome, binding, review); e != nil {
		t.Fatal(e)
	}
	peer, e := right.Resolve(left.Config().Identity.Host)
	if e != nil {
		t.Fatal(e)
	}
	remote, e := left.Resolve(right.Config().Identity.Host)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := left.Call(t.Context(), remote.Identity.Host, mesh.Request{Operation: "ssh_identity", Expected: &mesh.ExpectedEnrollment{Pair: peer.Pair, Revision: peer.Revision}, Params: json.RawMessage(`{}`)})
	if e != nil {
		t.Fatal(e)
	}
	var identity model.SSHDestinationIdentity
	if e = json.Unmarshal(raw, &identity); e != nil {
		t.Fatal(e)
	}
	_, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	signer, e := ssh.NewSignerFromKey(private)
	if e != nil {
		t.Fatal(e)
	}
	return &publicSSHFixture{a, b, left, right, identity, remote, signer}
}
func publicSSHHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func (f *publicSSHFixture) control(command string) meshstream.Control {
	host, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(f.identity.PublicKey))
	return meshstream.Control{Version: 1, Source: f.identity.Source, Target: f.identity.Target, Pair: f.identity.Pair, Revision: f.identity.Revision, Account: f.identity.Account, Key: local.ID(), PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(f.signer.PublicKey()))), Reason: "public private SSH fixture", TTL: time.Minute, Resources: model.Resources{CPU: 1}, CWD: filepath.Dir(f.target.home), TargetOwner: f.identity.Owner, HostKeySHA256: sshPublicHash(host), Kind: "exec", CommandSHA256: publicSSHHash(command)}
}
func (f *publicSSHFixture) dial(t *testing.T, ctx context.Context, q meshstream.Control) *ssh.Client {
	t.Helper()
	policy, e := f.left.ClientTLS(f.peer)
	if e != nil {
		t.Fatal(e)
	}
	conn, e := meshstream.Dial(ctx, f.peer.Identity.Address, policy, q, meshstream.DefaultLimits())
	if e != nil {
		t.Fatal(e)
	}
	host, _, _, _, e := ssh.ParseAuthorizedKey([]byte(f.identity.PublicKey))
	if e != nil {
		t.Fatal(e)
	}
	cc, channels, requests, e := ssh.NewClientConn(conn, "arm-"+q.Target, &ssh.ClientConfig{User: q.Account, Auth: []ssh.AuthMethod{ssh.PublicKeys(f.signer)}, HostKeyCallback: ssh.FixedHostKey(host), Timeout: time.Second})
	if e != nil {
		conn.Close()
		t.Fatal(e)
	}
	client := ssh.NewClient(cc, channels, requests)
	t.Cleanup(func() { client.Close() })
	return client
}
func (f *publicSSHFixture) status(t *testing.T, q meshstream.Control) model.SSHKeyStatus {
	t.Helper()
	params, _ := json.Marshal(struct {
		Key string `json:"key"`
	}{q.Key})
	raw, e := f.left.Call(t.Context(), q.Target, mesh.Request{Operation: "ssh_key_status", Expected: &mesh.ExpectedEnrollment{Pair: q.Pair, Revision: q.Revision}, Params: params})
	if e != nil {
		t.Fatal(e)
	}
	var status model.SSHKeyStatus
	if e = json.Unmarshal(raw, &status); e != nil {
		t.Fatal(e)
	}
	return status
}
func TestPublicSSHDirectPeerActualExecutionAndReceipt(t *testing.T) {
	f := newPublicSSHFixture(t)
	command := "printf private-public-ssh;printf separate-stderr >&2;exit 23"
	q := f.control(command)
	sourceHome, e := filepath.EvalSymlinks(f.source.home)
	if e != nil {
		t.Fatal(e)
	}
	binding, _, e := f.source.Manager.SSHAccount(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	pin := sshservice.Pin{Schema: 1, Source: q.Source, Target: q.Target, Pair: q.Pair, Revision: q.Revision, TLSFingerprint: f.identity.Certificate, TLSSPKI: f.identity.SPKI, Account: q.Account, PublicKey: f.identity.PublicKey, At: time.Now().UTC()}
	pinReview, e := sshservice.ReviewPin(filepath.Join(sourceHome, "mesh"), pin)
	if e != nil {
		t.Fatal(e)
	}
	if e = sshservice.CommitStatePin(t.Context(), sourceHome, binding, pinReview); e != nil {
		t.Fatal(e)
	}
	dir, e := sshservice.CreateInvocationDirectory(t.Context(), sourceHome, binding)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.Remove(dir.Path) })
	worker, e := usage.Capture(os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	intent := sshservice.SourceIntent{Schema: 1, Source: q.Source, Owner: binding.Owner, Request: sshservice.SourceRequest{Key: q.Key, Pin: pin, TargetOwner: q.TargetOwner, SourceCertificate: f.left.Config().Identity.Fingerprint, PublicKey: q.PublicKey, Reason: q.Reason, TTL: q.TTL, Resources: q.Resources, CWD: q.CWD, Kind: q.Kind, CommandSHA256: q.CommandSHA256, PTY: q.PTY}, Worker: worker, Directory: dir, Created: time.Now().UTC()}
	if e = sshservice.ReserveSource(t.Context(), sourceHome, binding, intent); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client := f.dial(t, ctx, q)
	session, e := client.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	out, e := session.CombinedOutput(command)
	exit, ok := e.(*ssh.ExitError)
	if !ok || exit.ExitStatus() != 23 || !strings.Contains(string(out), "private-public-ssh") || !strings.Contains(string(out), "separate-stderr") {
		t.Fatal(string(out), e)
	}
	status := f.status(t, q)
	if status.State != "admitted" || status.Execution == nil || status.Execution.Lease == nil || status.Execution.Lease.State != "terminal" || status.Execution.Lease.Outcome != "failed" || status.Fence == nil {
		t.Fatal("public execution has no exact terminal lease", status)
	}
	if status.Execution.Lease.Usage != nil || status.Execution.Lease.Workload != nil {
		t.Fatal("public receipt retained diagnostic observations")
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), "printf private-public-ssh") {
		t.Fatal("command leaked in public receipt")
	}
	if e = os.Remove(dir.Path); e != nil {
		t.Fatal(e)
	}
	if e = sshservice.SettleSource(t.Context(), sourceHome, binding, intent, *status.Execution); e != nil {
		t.Fatal("actual root-policy receipt could not settle source", e)
	}
	state, e := sshservice.ReadState(t.Context(), sourceHome, binding)
	if e != nil || len(state.Intents) != 0 || len(state.Completions) != 1 {
		t.Fatal("exact source terminal result not durable", e)
	}
}
func TestPublicSSHSealPreventsDelayedExecAndStaleScope(t *testing.T) {
	f := newPublicSSHFixture(t)
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	command := "printf forbidden > '" + marker + "'"
	q := f.control(command)
	c, e := q.Commitment(f.left.Config().Identity.Fingerprint)
	if e != nil {
		t.Fatal(e)
	}
	params, _ := json.Marshal(struct {
		Control model.SSHControlCommitment `json:"control"`
		Reason  string                     `json:"reason"`
	}{c, "Explicitly prevent the original delayed invocation"})
	req := mesh.Request{Operation: "ssh_key_seal", Key: q.Key, Expected: &mesh.ExpectedEnrollment{Pair: q.Pair, Revision: q.Revision}, Params: params}
	if _, e = f.left.Call(t.Context(), q.Target, req); e != nil {
		t.Fatal(e)
	}
	status := f.status(t, q)
	if status.State != "sealed" || status.Execution != nil {
		t.Fatal("seal not durable", status)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client := f.dial(t, ctx, q)
	session, e := client.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = session.CombinedOutput(command); e == nil {
		t.Fatal("sealed key executed")
	}
	if _, e = os.Lstat(marker); !os.IsNotExist(e) {
		t.Fatal("sealed delayed exec had side effects", e)
	}
	request := mesh.Request{Operation: "ssh_key_status", Expected: &mesh.ExpectedEnrollment{Pair: q.Pair, Revision: q.Revision + 1}, Params: json.RawMessage(`{"key":"` + q.Key + `"}`)}
	if _, e = f.left.Call(t.Context(), q.Target, request); e == nil {
		t.Fatal("stale enrollment status accepted")
	}
}
func TestPublicSSHRevocationClosesHijackedRuntimeWithoutDeadlock(t *testing.T) {
	f := newPublicSSHFixture(t)
	command := "printf ready;exec sleep 30"
	q := f.control(command)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client := f.dial(t, ctx, q)
	session, e := client.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	stdout, e := session.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = session.Start(command); e != nil {
		t.Fatal(e)
	}
	ready := make([]byte, 5)
	if _, e = stdout.Read(ready); e != nil || string(ready) != "ready" {
		t.Fatal("requested process did not start", string(ready), e)
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	start := time.Now()
	if e = f.right.Revoke(q.Source, "Explicit private fixture revoke"); e != nil {
		t.Fatal(e)
	}
	if time.Since(start) > time.Second {
		t.Fatal("revocation waited on manager cleanup/trust lock")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("hijacked SSH stream survived revoke")
	}
	end := time.Now().Add(3 * time.Second)
	for time.Now().Before(end) {
		s, e := f.target.Manager.LookupSSHKey(t.Context(), q.Source, q.Key)
		if e == nil && s.Execution != nil && s.Execution.Lease.State == "terminal" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("revoked process did not reconcile")
}
func TestSSHIdentityInventoryDoesNotInitializeKeys(t *testing.T) {
	grant := mesh.Grant{Permissions: []string{"ssh-exec", "lease-own"}, Limits: model.Resources{CPU: 1}, MaxTTL: time.Minute}
	_, target, left, right := registryPair(t, grant, grant)
	p, e := right.Resolve(left.Config().Identity.Host)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = left.Call(t.Context(), right.Config().Identity.Host, mesh.Request{Operation: "ssh_identity", Expected: &mesh.ExpectedEnrollment{Pair: p.Pair, Revision: p.Revision}, Params: json.RawMessage(`{}`)}); e == nil {
		t.Fatal("unconfigured SSH service reported enabled")
	}
	for _, name := range []string{"ssh-host-key.json", sshservice.ServiceFile, sshservice.StateLockName} {
		if _, e = os.Lstat(filepath.Join(target.home, "mesh", name)); !os.IsNotExist(e) {
			t.Fatal("inventory initialized SSH namespace", name, e)
		}
	}
}
