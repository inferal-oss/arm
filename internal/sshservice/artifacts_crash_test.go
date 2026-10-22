package sshservice

import (
	"context"
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
