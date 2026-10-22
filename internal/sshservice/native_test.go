package sshservice

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestNativeOpenSSHUsesOnlyEphemeralAgentAndPrivateProxy(t *testing.T) {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("OpenSSH unavailable; native protocol gate unverified")
	}
	proxyPath, proxyListener := socketFixture(t)
	dir := filepath.Dir(proxyPath)
	agentPath := filepath.Join(dir, "agent.sock")
	agentListener, err := net.ListenUnix("unix", &net.UnixAddr{Name: agentPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer agentListener.Close()
	if err = os.Chmod(agentPath, 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := NewUserSigner("armuser", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer signer.Close()
	host := testKey(t)
	id := strings.Repeat("a", 32)
	public := filepath.Join(dir, "identity.pub")
	known := filepath.Join(dir, "known_hosts")
	if err = os.WriteFile(public, ssh.MarshalAuthorizedKey(signer.PublicKey()), 0600); err != nil {
		t.Fatal(err)
	}
	line, _ := KnownHostsLine(id, host.PublicKey())
	if err = os.WriteFile(known, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	agentDone := make(chan error, 1)
	go func() {
		c, err := agentListener.AcceptUnix()
		if err != nil {
			agentDone <- err
			return
		}
		agentDone <- ServeAgentConnection(ctx, c, signer)
	}()
	var calls atomic.Int32
	serverDone := make(chan error, 1)
	go func() {
		c, err := proxyListener.AcceptUnix()
		if err != nil {
			serverDone <- err
			return
		}
		s := Server{HostKey: host, Handshake: time.Second, StartTimeout: time.Second, Revalidate: func(context.Context) error { return nil }, Execute: func(ctx context.Context, q Execution, ch ssh.Channel) (Process, error) {
			calls.Add(1)
			if q.Command != "synthetic command only" || q.Kind != "exec" {
				return nil, io.ErrUnexpectedEOF
			}
			_, _ = ch.Write([]byte("native private reply\n"))
			p := &fixtureProcess{done: make(chan struct{})}
			close(p.done)
			return p, nil
		}}
		serverDone <- s.Serve(ctx, c, Permit{"armuser", signer.PublicKey()})
	}()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args, err := OpenSSHArguments(Invocation{exe, proxyPath, agentPath, public, known, id, "armuser", false})
	if err != nil {
		t.Fatal(err)
	}
	args = append(args, "synthetic command only")
	command := exec.CommandContext(ctx, sshPath, args...)
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "SSH_AUTH_SOCK=/intentionally-unusable-general-agent"}
	out, err := command.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 23 {
		t.Fatal("native SSH exit status lost", string(out), err)
	}
	if string(out) != "native private reply\n" {
		t.Fatal(string(out))
	}
	if calls.Load() != 1 {
		t.Fatal("native command not routed once")
	}
	cancel()
	agentListener.Close()
	for _, done := range []<-chan error{serverDone, agentDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("private native worker leaked")
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "agent.sock" && entry.Name() != "proxy.sock" && entry.Name() != "identity.pub" && entry.Name() != "known_hosts" {
			t.Fatal("unexpected private key/config artifact", entry.Name())
		}
	}
}
