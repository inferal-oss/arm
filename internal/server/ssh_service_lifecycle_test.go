package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/sshservice"
)

func TestSSHDisableReenableKeepsKeyButNeverRevivesPriorScope(t *testing.T) {
	f := newPublicSSHFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	peer, e := f.right.Resolve(f.identity.Source)
	if e != nil {
		t.Fatal(e)
	}
	oldScope, _, e := f.target.mesh.sshScope(ctx, peer)
	if e != nil {
		t.Fatal(e)
	}
	q := f.control("printf ready; sleep 30")
	client := f.dial(t, ctx, q)
	session, e := client.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	output, e := session.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = session.Start("printf ready; sleep 30"); e != nil {
		t.Fatal(e)
	}
	ready := make([]byte, 5)
	if _, e = output.Read(ready); e != nil || string(ready) != "ready" {
		t.Fatal(string(ready), e)
	}
	binding, _, e := f.target.Manager.SSHAccount(ctx)
	if e != nil {
		t.Fatal(e)
	}
	home, e := filepath.EvalSymlinks(f.target.home)
	if e != nil {
		t.Fatal(e)
	}
	change, e := sshservice.ReviewService(ctx, home, binding, false, "explicit test disable")
	if e != nil {
		t.Fatal(e)
	}
	if e = sshservice.ApplyService(ctx, home, binding, change); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(struct {
		Revision uint64 `json:"revision"`
	}{change.After.Revision})
	if _, e = f.target.sshNative(ctx, "ssh_close", raw); e != nil {
		t.Fatal(e)
	}
	wait := make(chan error, 1)
	go func() { wait <- session.Wait() }()
	select {
	case <-wait:
	case <-time.After(2 * time.Second):
		t.Fatal("disable left original SSH stream alive")
	}
	if e = f.target.mesh.revalidateSSH(ctx, oldScope); e == nil {
		t.Fatal("disabled service accepted old scope")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, e := f.target.Manager.LookupSSHKey(ctx, q.Source, q.Key)
		if e != nil {
			t.Fatal(e)
		}
		if status.Execution != nil && status.Execution.Lease != nil && status.Execution.Lease.State == "terminal" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disabled execution did not reconcile")
		}
		time.Sleep(10 * time.Millisecond)
	}
	enable, e := sshservice.ReviewService(ctx, home, binding, true, "explicit test re-enable")
	if e != nil {
		t.Fatal(e)
	}
	if e = sshservice.ApplyService(ctx, home, binding, enable); e != nil {
		t.Fatal(e)
	}
	if enable.After.PublicKey != f.identity.PublicKey || enable.After.Revision <= oldScope.ServiceRevision {
		t.Fatal("re-enable changed key or reused revision")
	}
	if e = f.target.mesh.revalidateSSH(ctx, oldScope); e == nil {
		t.Fatal("re-enable revived a prior live scope")
	}
	fresh := f.control("printf fresh-enabled")
	reconnected := f.dial(t, ctx, fresh)
	s, e := reconnected.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	out, e := s.CombinedOutput("printf fresh-enabled")
	if e != nil || string(out) != "fresh-enabled" {
		t.Fatal(string(out), e)
	}
}
