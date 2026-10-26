package sshservice

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

func TestArtifactCrashHelper(t *testing.T) {
	marker := -1
	for i, v := range os.Args {
		if v == "--" {
			marker = i
			break
		}
	}
	if marker < 0 {
		return
	}
	args := os.Args[marker+1:]
	if len(args) != 3 {
		os.Exit(2)
	}
	native := local.CurrentIdentity()
	b := Binding{Host: args[1], Owner: native.UID, Account: native.Username}
	s, e := ReadState(context.Background(), args[0], b)
	if e != nil || len(s.Pins) != 1 {
		os.Exit(3)
	}
	r := sourceFixture(t, args[0], b, s.Pins[0], "crash-boundary")
	if e = ReserveSource(context.Background(), args[0], b, r); e != nil {
		os.Exit(4)
	}
	if args[2] == "reserved" {
		os.Exit(21)
	}
	_, e = prepareArtifacts(context.Background(), args[0], b, r, func(stage string) {
		if stage == args[2] {
			os.Exit(21)
		}
	})
	if e != nil {
		os.Exit(5)
	}
	os.Exit(6)
}
func TestArtifactCrashBoundariesAndExplicitDeadWorkerRecovery(t *testing.T) {
	for _, stage := range []string{"reserved", "identity.pub", "known_hosts", "bound:agent.sock", "bound:proxy.sock", "published"} {
		t.Run(strings.ReplaceAll(stage, ":", "-"), func(t *testing.T) {
			home, b := stateFixture(t)
			statePin(t, home, b)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestArtifactCrashHelper$", "--", home, b.Host, stage)
			output, e := cmd.CombinedOutput()
			if e == nil || cmd.ProcessState.ExitCode() != 21 {
				t.Fatalf("crash checkpoint %s not reached: %v %s", stage, e, output)
			}
			s, e := ReadState(t.Context(), home, b)
			if e != nil || len(s.Intents) != 1 {
				t.Fatal("crash lost durable source evidence", e)
			}
			r := s.Intents[0]
			t.Cleanup(func() {
				for _, name := range append(append([]string{}, artifactNames...), ArtifactReceiptName) {
					os.Remove(filepath.Join(r.Directory.Path, name))
				}
				os.Remove(r.Directory.Path)
			})
			status := model.SSHKeyStatus{Schema: 1, Source: r.Source, Target: r.Request.Pin.Target, Key: r.Request.Key, State: "legacy-admitted", Execution: func() *model.SSHLookup { v := terminalReceipt(r); return &v }()}
			if e = RecoverArtifacts(t.Context(), home, b, r, status); stage != "published" {
				if e == nil {
					t.Fatal("partial manifest guessed safe", stage)
				}
				s, e = ReadState(t.Context(), home, b)
				if e != nil || len(s.Intents) != 1 {
					t.Fatal("partial evidence disappeared", e)
				}
				return
			} else if e != nil {
				t.Fatal("published exact artifacts cannot recover", e)
			}
			if e = SettleSource(t.Context(), home, b, r, *status.Execution); e != nil {
				t.Fatal(e)
			}
			s, e = ReadState(t.Context(), home, b)
			if e != nil || len(s.Intents) != 0 || len(s.Completions) != 1 {
				t.Fatal("explicit exact recovery did not settle", e)
			}
		})
	}
}
func TestArtifactRecoveryRefusesCurrentWorkerAndAbsentStatus(t *testing.T) {
	home, b := stateFixture(t)
	p := statePin(t, home, b)
	r := sourceFixture(t, home, b, p, "live-worker")
	if e := ReserveSource(t.Context(), home, b, r); e != nil {
		t.Fatal(e)
	}
	a, e := PrepareArtifacts(t.Context(), home, b, r)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Cleanup(context.Background())
	receipt := terminalReceipt(r)
	status := model.SSHKeyStatus{Schema: 1, Source: r.Source, Target: r.Request.Pin.Target, Key: r.Request.Key, State: "legacy-admitted", Execution: &receipt}
	if e = RecoverArtifacts(t.Context(), home, b, r, status); e == nil {
		t.Fatal("current worker cleanup permitted")
	}
	status.Execution = nil
	status.State = "absent"
	if e = RecoverArtifacts(t.Context(), home, b, r, status); e == nil {
		t.Fatal("absent snapshot cleanup permitted")
	}
}

// A retained prior-boot or old-format identity stays uncertain even when its
// real helper has exited. It must not gain cleanup authority by translation.
func TestArtifactRecoveryRetainsDifferentAndObsoleteWorkerNamespaces(t *testing.T) {
	home, b := stateFixture(t)
	statePin(t, home, b)
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestArtifactCrashHelper$", "--", home, b.Host, "published")
	out, err := cmd.CombinedOutput()
	if err == nil || cmd.ProcessState.ExitCode() != 21 {
		t.Fatalf("crash helper: %v %s", err, out)
	}
	s, err := ReadState(t.Context(), home, b)
	if err != nil || len(s.Intents) != 1 {
		t.Fatal("missing retained worker", err)
	}
	original := s.Intents[0]
	t.Cleanup(func() {
		for _, name := range append(append([]string{}, artifactNames...), ArtifactReceiptName) {
			os.Remove(filepath.Join(original.Directory.Path, name))
		}
		os.Remove(original.Directory.Path)
	})
	path := filepath.Join(home, "mesh", intentName(original))
	manifest, err := os.ReadFile(filepath.Join(original.Directory.Path, ArtifactReceiptName))
	if err != nil {
		t.Fatal(err)
	}
	for _, namespace := range []string{original.Worker.Namespace + "-other", "darwin:boot:retained-wall-time"} {
		changed := original
		changed.Worker.Namespace = namespace
		raw, err := json.Marshal(changed)
		if err != nil {
			t.Fatal(err)
		}
		// Controlled retained-record replacement simulates a prior namespace;
		// production never rewrites or adopts such identities.
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		err = RecoverArtifacts(t.Context(), home, b, changed, statusFixture(changed))
		if err == nil || !strings.Contains(err.Error(), "namespace differs") {
			t.Fatal("different retained namespace did not veto cleanup", err)
		}
		for _, name := range artifactNames {
			if _, err := os.Lstat(filepath.Join(original.Directory.Path, name)); err != nil {
				t.Fatal("artifact lost", err)
			}
		}
		after, err := os.ReadFile(filepath.Join(original.Directory.Path, ArtifactReceiptName))
		if err != nil || !bytes.Equal(after, manifest) {
			t.Fatal("receipt changed on refusal", err)
		}
		retained, err := ReadState(t.Context(), home, b)
		if err != nil || len(retained.Intents) != 1 || !exactIntent(retained.Intents[0], changed) || len(retained.Completions) != 0 || len(retained.Seals) != 0 {
			t.Fatal("namespace refusal settled or adopted retained state", err)
		}
	}
	raw, _ := json.Marshal(original)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverArtifacts(t.Context(), home, b, original, statusFixture(original)); err != nil {
		t.Fatal("independent original worker recovery failed", err)
	}
}
