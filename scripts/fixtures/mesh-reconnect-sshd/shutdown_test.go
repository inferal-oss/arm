package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// These actual loopback connections use only in-memory fixture keys and a
// disposable fixed sleep helper, never ARM, external SSH or user credentials.
func TestPrivateSSHShutdownJoinsActualOwnedHelper(t *testing.T) {
	for _, trigger := range []string{"parent-cancel", "connection-close"} {
		t.Run(trigger, func(t *testing.T) {
			root, e := os.MkdirTemp("", "arm-reconnect-shutdown-")
			if e != nil {
				t.Fatal(e)
			}
			canonical, e := filepath.EvalSymlinks(root)
			if e != nil {
				t.Fatal(e)
			}
			root = canonical
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("Owned setup/failure fixture retained: %s", root)
				}
			})
			rootInfo, e := os.Lstat(root)
			if e != nil {
				t.Fatal(e)
			}
			image, ready, events := filepath.Join(root, "fixed-helper"), filepath.Join(root, "child-ready"), filepath.Join(root, "events.jsonl")
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
			script := "#!/bin/sh\numask 077\nprintf '%s\\n' \"$$\" > " + quote(ready) + "\nexec /bin/sleep 60\n"
			if e = os.WriteFile(image, []byte(script), 0700); e != nil {
				t.Fatal(e)
			}
			log, e := os.OpenFile(events, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				t.Fatal(e)
			}
			defer log.Close()
			_, private, e := ed25519.GenerateKey(rand.Reader)
			if e != nil {
				t.Fatal(e)
			}
			signer, e := ssh.NewSignerFromKey(private)
			if e != nil {
				t.Fatal(e)
			}
			config := &ssh.ServerConfig{PublicKeyCallback: func(metadata ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
				if metadata.User() != "owned-fixture" || !bytes.Equal(key.Marshal(), signer.PublicKey().Marshal()) {
					return nil, fmt.Errorf("wrong fixture key")
				}
				return &ssh.Permissions{}, nil
			}}
			config.AddHostKey(signer)
			endpoint := &endpoint{options: options{arm: image, home: root, control: filepath.Join(root, "absent-control")}, config: config, sessions: make(chan struct{}, 2), eventsFile: log}
			listener, e := net.Listen("tcp4", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			served := make(chan struct{})
			go func() {
				defer close(served)
				conn, e := listener.Accept()
				if e == nil {
					endpoint.serve(ctx, conn)
				}
			}()
			var client *ssh.Client
			pid := 0
			cleanup := func() {
				cancel()
				listener.Close()
				if client != nil {
					client.Close()
				}
				select {
				case <-served:
				case <-time.After(5 * time.Second):
					t.Errorf("owned serve not joined during cleanup")
				}
				// Original buggy serve may return before its detached session. Wait for
				// actual event-after-Cmd.Wait and native ESRCH before fixture deletion.
				deadline := time.Now().Add(5 * time.Second)
				for pid > 0 && syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if pid > 0 && syscall.Kill(pid, 0) != syscall.ESRCH {
					t.Errorf("owned helper native absence unproved; retained %s", root)
				}
				log.Close()
				if t.Failed() {
					t.Logf("Failure diagnostic fixture retained: %s", root)
					return
				}
				after, e := os.Lstat(root)
				if e != nil || !os.SameFile(rootInfo, after) {
					t.Errorf("fixture root identity changed; retained %s", root)
					return
				}
				entries, e := os.ReadDir(root)
				if e != nil || len(entries) != 3 {
					t.Errorf("fixture roster changed; retained %s", root)
					return
				}
				for _, path := range []string{image, ready, events} {
					info, e := os.Lstat(path)
					if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
						t.Errorf("fixture member changed; retained %s", root)
						return
					}
				}
				for _, path := range []string{image, ready, events} {
					if e = os.Remove(path); e != nil {
						t.Error(e)
						return
					}
				}
				if e = os.Remove(root); e != nil {
					t.Error(e)
				}
			}
			defer cleanup()
			conn, e := net.DialTimeout("tcp4", listener.Addr().String(), 2*time.Second)
			if e != nil {
				t.Fatal(e)
			}
			conn.SetDeadline(time.Now().Add(8 * time.Second))
			transport, channels, requests, e := ssh.NewClientConn(conn, listener.Addr().String(), &ssh.ClientConfig{User: "owned-fixture", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
				if !bytes.Equal(key.Marshal(), signer.PublicKey().Marshal()) {
					return fmt.Errorf("fixture host key mismatch")
				}
				return nil
			}})
			if e != nil {
				conn.Close()
				t.Fatal(e)
			}
			client = ssh.NewClient(transport, channels, requests)
			session, e := client.NewSession()
			if e != nil {
				t.Fatal(e)
			}
			input, e := session.StdinPipe()
			if e != nil {
				t.Fatal(e)
			}
			if e = session.Start(image + " mesh bootstrap"); e != nil {
				t.Fatal(e)
			}
			raw, _ := json.Marshal(packet{Home: root, Action: "lifecycle_inspect"})
			if _, e = input.Write(raw); e != nil {
				t.Fatal(e)
			}
			if e = input.Close(); e != nil {
				t.Fatal(e)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				raw, e := os.ReadFile(ready)
				if e == nil && len(bytes.TrimSpace(raw)) > 0 {
					pid, e = strconv.Atoi(strings.TrimSpace(string(raw)))
					if e != nil || pid < 1 {
						t.Fatalf("invalid owned helper ready PID %q", raw)
					}
					break
				}
				if (e != nil && !os.IsNotExist(e)) || time.Now().After(deadline) {
					t.Fatalf("bounded actual child readiness: %v", e)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if e = syscall.Kill(pid, 0); e != nil {
				t.Fatalf("actual child not live before trigger: %v", e)
			}
			before, _ := os.ReadFile(events)
			if len(before) != 0 {
				t.Fatal("helper already exited before shutdown trigger")
			}
			if trigger == "parent-cancel" {
				cancel()
			} else {
				client.Close()
			}
			select {
			case <-served:
			case <-time.After(3 * time.Second):
				t.Fatal("serve failed bounded cancellation/connection-close join")
			}
			if e = syscall.Kill(pid, 0); e != syscall.ESRCH {
				t.Fatalf("serve returned before actual child native absence: %v", e)
			}
			raw, e = os.ReadFile(events)
			if e != nil {
				t.Fatal(e)
			}
			var event observation
			if e = json.Unmarshal(bytes.TrimSpace(raw), &event); e != nil || !event.ChildStarted || !event.WaitFailed || event.Exit != -1 {
				t.Fatalf("expected actual killed child Wait receipt: %#v %v", event, e)
			}
			t.Logf("actual child %d live before %s, then sole Wait receipt and native ESRCH before serve returned; owned fixture cleaned", pid, trigger)
		})
	}
}

// A self-signalled helper must fail while the SSH transport remains live. This
// is distinct from connection cancellation, where no reply can be delivered.
func TestPrivateSSHSignalledHelperIsNotSuccessfulOnLiveConnection(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(root, "fixed-self-signal-helper")
	// Signal only this helper's own current PID; never a saved process/group ID.
	if err = os.WriteFile(image, []byte("#!/bin/sh\nprintf 'partial-before-signal\\n'\nkill -TERM \"$$\"\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	log, err := os.OpenFile(filepath.Join(root, "events.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PublicKeyCallback: func(metadata ssh.ConnMetadata, public ssh.PublicKey) (*ssh.Permissions, error) {
		if metadata.User() != "owned-fixture" || !bytes.Equal(public.Marshal(), signer.PublicKey().Marshal()) {
			return nil, fmt.Errorf("wrong fixture account/key")
		}
		return &ssh.Permissions{}, nil
	}}
	config.AddHostKey(signer)
	endpoint := &endpoint{options: options{arm: image, home: root, control: filepath.Join(root, "absent-control")}, config: config, sessions: make(chan struct{}, 2), eventsFile: log}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	served := make(chan struct{})
	go func() {
		defer close(served)
		connection, e := listener.Accept()
		if e == nil {
			endpoint.serve(ctx, connection)
		}
	}()
	var client *ssh.Client
	defer func() {
		cancel()
		listener.Close()
		if client != nil {
			client.Close()
		}
		select {
		case <-served:
		case <-time.After(3 * time.Second):
			t.Error("owned self-signal connection did not join")
		}
	}()
	connection, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.SetDeadline(time.Now().Add(4 * time.Second))
	transport, channels, requests, err := ssh.NewClientConn(connection, listener.Addr().String(), &ssh.ClientConfig{User: "owned-fixture", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: func(_ string, _ net.Addr, public ssh.PublicKey) error {
		if !bytes.Equal(public.Marshal(), signer.PublicKey().Marshal()) {
			return fmt.Errorf("wrong fixture host key")
		}
		return nil
	}})
	if err != nil {
		connection.Close()
		t.Fatal(err)
	}
	client = ssh.NewClient(transport, channels, requests)
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	input, _ := json.Marshal(packet{Home: root, Action: "lifecycle_inspect"})
	session.Stdin = bytes.NewReader(input)
	output, err := session.CombinedOutput(image + " mesh bootstrap")
	exit, ok := err.(*ssh.ExitError)
	if !ok || exit.ExitStatus() == 0 {
		t.Fatalf("self-signalled helper reported SSH success: output=%q error=%v", output, err)
	}
	if len(output) != 0 {
		t.Fatalf("incomplete helper output forwarded: %q", output)
	}
	// A rejected global keepalive is a valid response on the still-live transport.
	if _, _, err = client.SendRequest("fixture-transport-still-live", true, nil); err != nil {
		t.Fatal("transport closed rather than returning a failed helper status", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var event observation
	if err = json.Unmarshal(bytes.TrimSpace(raw), &event); err != nil || !event.ChildStarted || !event.WaitFailed || event.Exit != -1 {
		t.Fatalf("missing actual self-signal Wait receipt: %#v %v", event, err)
	}
	t.Logf("actual signalled helper exit%d returned SSH failure%d on a live connection", event.Exit, exit.ExitStatus())
}
