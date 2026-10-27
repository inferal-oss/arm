package syncworker

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
	"github.com/inferal-oss/arm/internal/syncreconcile"
	"github.com/inferal-oss/arm/internal/syncservice"
	"golang.org/x/crypto/ssh"
)

type sshWorkerProcess struct{ result <-chan error }

func (p *sshWorkerProcess) Wait(ctx context.Context) (sshservice.Exit, error) {
	select {
	case err := <-p.result:
		if err != nil {
			return sshservice.Exit{Code: 1}, nil
		}
		return sshservice.Exit{Code: 0}, nil
	case <-ctx.Done():
		return sshservice.Exit{}, ctx.Err()
	}
}
func (*sshWorkerProcess) Signal(context.Context, string) error {
	return errors.New("sync worker signals unsupported")
}
func (*sshWorkerProcess) Resize(context.Context, sshservice.Window) error {
	return errors.New("sync worker PTY unsupported")
}
func (*sshWorkerProcess) JoinTransport() {}
func signer(t *testing.T) ssh.Signer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// This uses ARM's actual SSH service boundary, authenticated public key,
// session exec admission callback, channel requests and exit status. Only the
// Manager admission is a test hold; product dispatch remains gated on it.
func TestFixedSyncWorkerOverARMSSHSession(t *testing.T) {
	p := setupPair(t)
	write(t, p.local, "selected", "large enough for one rsync exchange")
	write(t, p.remote, "peer", "second batch in reverse direction")
	req := requestPair(t, p, syncreconcile.Both, syncreconcile.Additive, nil, nil)
	plan, err := syncreconcile.Reconcile(req)
	if err != nil {
		t.Fatal(err)
	}
	resources := model.Resources{CPU: 1, Memory: 64 << 20, Disk: 64 << 20}
	key := "sync-key-" + strings.Repeat("s", 16)
	local, rows, err := syncservice.NewIntent(key, FixedCommandSHA256(), hash("SSH fixture control"), "SSH fixture", syncreconcile.Local, req, plan, resources, resources, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	remote, other, err := syncservice.NewIntent(key, local.WorkerCommandSHA256, local.SSHControlSHA256, local.Reason, syncreconcile.Remote, req, plan, resources, resources, local.Created)
	if err != nil || !reflect.DeepEqual(rows, other) {
		t.Fatalf("remote intent: %v", err)
	}
	reserveTest(t, p.localDB, local, rows)
	reserveTest(t, p.remoteDB, remote, rows)
	lh := &testHold{intent: local, root: req.Binding.Local.RootID, disk: 64 << 20}
	rh := &testHold{intent: remote, root: req.Binding.Remote.RootID, disk: 64 << 20}
	le := &endpoint{Side: syncreconcile.Local, Root: p.local, WorkParent: p.localWork, Names: p.names, Metadata: p.metadata, Ledger: &syncservice.Ledger{DB: p.localDB, Key: key, Side: syncreconcile.Local}, Hold: lh}
	re := &endpoint{Side: syncreconcile.Remote, Root: p.remote, WorkParent: p.remoteWork, Names: p.names, Metadata: p.metadata, Ledger: &syncservice.Ledger{DB: p.remoteDB, Key: key, Side: syncreconcile.Remote}, Hold: rh}
	hostKey, userKey := signer(t), signer(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	server := sshservice.Server{HostKey: hostKey, Handshake: time.Second, StartTimeout: time.Second, Revalidate: func(context.Context) error { return nil }, Execute: func(executionCtx context.Context, q sshservice.Execution, ch ssh.Channel) (sshservice.Process, error) {
		if q.Kind != "exec" || q.Command != FixedCommand || q.Terminal != nil {
			return nil, errors.New("unexpected SSH execution")
		}
		prepared, e := syncservice.ReadPreparation(ctx, ch, rh)
		if e != nil {
			return nil, e
		}
		re.Prepared = &prepared
		closeOriginal, e := sshservice.OriginalTransportCloser(executionCtx, ch)
		if e != nil {
			return nil, e
		}
		remoteSession, e := newOwnedSingleStreamSession(ch, func() error { return nil }, closeOriginal)
		if e != nil {
			return nil, e
		}
		re.Session = remoteSession
		result := make(chan error, 1)
		go func() { result <- runEndpoint(ctx, re) }()
		return &sshWorkerProcess{result: result}, nil
	}}
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			serverDone <- e
			return
		}
		serverDone <- server.Serve(ctx, conn, sshservice.Permit{Account: "armuser", PublicKey: userKey.PublicKey()})
	}()
	clientSide, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	clientConn, channels, requests, err := ssh.NewClientConn(clientSide, "arm-fixture", &ssh.ClientConfig{User: "armuser", Auth: []ssh.AuthMethod{ssh.PublicKeys(userKey)}, HostKeyCallback: ssh.FixedHostKey(hostKey.PublicKey()), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client := ssh.NewClient(clientConn, channels, requests)
	defer client.Close()
	ch, reqs, err := client.OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	wrote := make(chan error, 1)
	go func() {
		wrote <- syncservice.WritePreparation(ch, syncservice.Preparation{Version: syncservice.Schema, Intent: local, Manifest: rows})
	}()
	accepted, err := ch.SendRequest("exec", true, ssh.Marshal(struct{ Command string }{FixedCommand}))
	if err != nil || !accepted {
		t.Fatalf("fixed SSH command rejected: %v", err)
	}
	if writeErr := <-wrote; writeErr != nil {
		t.Fatal(writeErr)
	}
	waitExit := func() error {
		for req := range reqs {
			if req.Type == "exit-status" {
				var status struct{ Status uint32 }
				if ssh.Unmarshal(req.Payload, &status) != nil || status.Status != 0 {
					return fmt.Errorf("remote SSH worker exit %d", status.Status)
				}
				return nil
			}
		}
		return errors.New("missing SSH exit status")
	}
	ls, err := newOwnedSingleStreamSession(ch, waitExit, clientSide.Close)
	if err != nil {
		t.Fatal(err)
	}
	le.Session = ls
	le.PreparedSent = true
	if err = runEndpoint(ctx, le); err != nil {
		t.Fatal(err)
	}
	if !lh.charged || !rh.charged || !ls.joined {
		t.Fatal("SSH worker bypassed hold or transport join")
	}
	if contents(t, p.remote, "selected") != "large enough for one rsync exchange" {
		t.Fatal("SSH rsync payload changed")
	}
	if contents(t, p.local, "peer") != "second batch in reverse direction" {
		t.Fatal("reverse SSH rsync payload changed")
	}
	if baseline, e := syncservice.LoadBaseline(t.Context(), p.localDB, req.Binding, syncreconcile.Local); e != nil || baseline == nil {
		t.Fatalf("SSH baseline: %v", e)
	}
	_ = client.Close()
	cancel()
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("ARM SSH service did not join")
	}
}
