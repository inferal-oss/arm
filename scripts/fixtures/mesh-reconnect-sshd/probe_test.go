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
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/meshssh"
	"golang.org/x/crypto/ssh"
)

func TestFixedProbeCommandBindsExactScriptImageAndHome(t *testing.T) {
	s := endpoint{options: options{arm: "/private/selected-arm", home: "/private/state with 'quotes'"}}
	probe, err := meshssh.FixedReadOnlyProbe(s.arm, s.home)
	if err != nil {
		t.Fatal(err)
	}
	exact := strings.Join(probe.Remote, " ")
	got, ok := s.fixedCommand(exact)
	if !ok || got.action != "read_only_probe" || got.argv[0] != "/bin/sh" || got.input != "ARM_READONLY_PROBE\n" {
		t.Fatal(got, ok)
	}
	if got, ok := s.fixedCommand(s.arm + " mesh bootstrap"); !ok || got.action != "bootstrap" {
		t.Fatal(got, ok)
	}
	otherImage, _ := meshssh.FixedReadOnlyProbe("/private/other-arm", s.home)
	otherHome, _ := meshssh.FixedReadOnlyProbe(s.arm, "/private/other-state")
	for _, bad := range []string{exact + "; id", exact + " ", strings.Replace(exact, "set -eu", "exit 0", 1), strings.Join(otherImage.Remote, " "), strings.Join(otherHome.Remote, " "), "/bin/sh -c id", s.arm + " mesh bootstrap; id"} {
		if _, ok := s.fixedCommand(bad); ok {
			t.Fatal("changed command accepted", bad)
		}
	}
}

func TestActualFixedReadOnlyProbeAndRefusalsOverPrivateSSH(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	image := filepath.Join(root, "must-not-execute-arm")
	marker := filepath.Join(root, "arm-executed")
	if err = os.WriteFile(image, []byte("#!/bin/sh\nprintf executed > '"+marker+"'\nexit 91\n"), 0700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "state with 'quotes'")
	probe, err := meshssh.FixedReadOnlyProbe(image, home)
	if err != nil {
		t.Fatal(err)
	}
	exact := strings.Join(probe.Remote, " ")
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, command, input string
		positive             bool
	}{
		{"actual-native-readonly", exact, probe.Input, true},
		{"missing-marker", exact, "", false},
		{"extra-input", exact, probe.Input + "extra", false},
		{"changed-marker", exact, "ARM_OTHER_PROBE\n", false},
		{"changed-script", strings.Replace(exact, "set -eu", "exit 0", 1), probe.Input, false},
		{"extra-shell-operation", exact + "; id", probe.Input, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			logpath := filepath.Join(root, test.name+".events")
			log, err := os.OpenFile(logpath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			control := filepath.Join(root, test.name+".control")
			controlRaw := []byte(`{"key":"unrelated-drop","stage":"after"}`)
			if err = os.WriteFile(control, controlRaw, 0600); err != nil {
				t.Fatal(err)
			}
			controlBefore, err := os.Lstat(control)
			if err != nil {
				t.Fatal(err)
			}
			config := &ssh.ServerConfig{PublicKeyCallback: func(metadata ssh.ConnMetadata, public ssh.PublicKey) (*ssh.Permissions, error) {
				if metadata.User() != "owned-fixture" || !bytes.Equal(public.Marshal(), signer.PublicKey().Marshal()) {
					return nil, fmt.Errorf("wrong fixture key")
				}
				return &ssh.Permissions{}, nil
			}}
			config.AddHostKey(signer)
			s := &endpoint{options: options{arm: image, home: home, control: control}, config: config, sessions: make(chan struct{}, 2), eventsFile: log}
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			served := make(chan struct{})
			go func() {
				defer close(served)
				connection, e := listener.Accept()
				if e == nil {
					s.serve(ctx, connection)
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
					t.Error("owned probe connection/session/child Wait not joined")
				}
			}()
			conn, err := net.DialTimeout("tcp4", listener.Addr().String(), 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			conn.SetDeadline(time.Now().Add(8 * time.Second))
			transport, channels, requests, err := ssh.NewClientConn(conn, listener.Addr().String(), &ssh.ClientConfig{User: "owned-fixture", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: func(_ string, _ net.Addr, public ssh.PublicKey) error {
				if !bytes.Equal(public.Marshal(), signer.PublicKey().Marshal()) {
					return fmt.Errorf("wrong pinned fixture key")
				}
				return nil
			}})
			if err != nil {
				conn.Close()
				t.Fatal(err)
			}
			client = ssh.NewClient(transport, channels, requests)
			session, err := client.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			session.Stdin = strings.NewReader(test.input)
			var stdout, stderr bytes.Buffer
			session.Stdout = &stdout
			session.Stderr = &stderr
			err = session.Run(test.command)
			raw, readErr := os.ReadFile(logpath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if test.positive {
				if err != nil {
					t.Fatalf("actual native fixed probe failed: %v stderr=%s", err, stderr.String())
				}
				lines := strings.SplitN(stdout.String(), "\n", 10)
				wantOS := "Linux"
				if runtime.GOOS == "darwin" {
					wantOS = "Darwin"
				}
				if len(lines) != 10 || lines[0] != "ARM_PROBE_V1" || lines[1] != wantOS || lines[3] != strconv.Itoa(os.Getuid()) || lines[4] != root || lines[6] != image || lines[7] != home || strings.TrimSpace(lines[9]) != "null" {
					t.Fatalf("not actual selected native facts: %q", stdout.String())
				}
				if n, e := strconv.Atoi(lines[8]); e != nil || n < 1 {
					t.Fatal("native CPU count", lines[8], e)
				}
				var event observation
				if json.Unmarshal(bytes.TrimSpace(raw), &event) != nil || event.Action != "read_only_probe" || !event.ChildStarted || event.WaitFailed || event.Exit != 0 || event.Fault != "" || event.InputSHA != digest([]byte(probe.Input)) {
					t.Fatalf("native Start/Wait event not positive: %s", raw)
				}
			} else if err == nil || len(raw) != 0 || stdout.Len() != 0 {
				t.Fatalf("forged command/input reached native probe: error=%v events=%s stdout=%s", err, raw, stdout.String())
			}
			if _, e := os.Stat(home); !os.IsNotExist(e) {
				t.Fatal("probe created ARM state", e)
			}
			if _, e := os.Stat(marker); !os.IsNotExist(e) {
				t.Fatal("probe executed installed ARM", e)
			}
			controlAfter, e := os.Lstat(control)
			if e != nil || !os.SameFile(controlBefore, controlAfter) {
				t.Fatal("probe consumed drop control", e)
			}
			got, e := os.ReadFile(control)
			if e != nil || !bytes.Equal(got, controlRaw) {
				t.Fatal("probe changed drop control", e)
			}
		})
	}
}
