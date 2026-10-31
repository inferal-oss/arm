package gitmeta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("ARM_GITMETA_FIXTURE") == "1" {
		gitMetadataFixture()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func gitMetadataFixture() {
	if len(os.Args) != 12 || os.Args[1] != "-c" || os.Args[2] != "core.fsmonitor=false" || os.Args[3] != "-c" || os.Args[4] != "core.hooksPath="+os.DevNull || os.Args[5] != "-C" || os.Args[7] != "rev-parse" || strings.Join(os.Args[8:], " ") != "--path-format=absolute --show-toplevel --absolute-git-dir --git-common-dir" {
		os.Exit(71)
	}
	for key, want := range map[string]string{"GIT_OPTIONAL_LOCKS": "0", "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_SYSTEM": os.DevNull, "GIT_CONFIG_GLOBAL": os.DevNull, "GIT_TERMINAL_PROMPT": "0", "GIT_NO_REPLACE_OBJECTS": "1"} {
		if os.Getenv(key) != want {
			os.Exit(72)
		}
	}
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_CONFIG_PARAMETERS"} {
		if _, exists := os.LookupEnv(key); exists {
			os.Exit(73)
		}
	}
	var paths []string
	if json.Unmarshal([]byte(os.Getenv("ARM_GITMETA_PATHS")), &paths) != nil || len(paths) != 3 {
		os.Exit(74)
	}
	switch os.Getenv("ARM_GITMETA_MODE") {
	case "failure":
		fmt.Fprint(os.Stderr, "synthetic failure")
		os.Exit(7)
	case "bare":
		fmt.Fprint(os.Stderr, "synthetic bare repository refusal")
		os.Exit(128)
	case "unsupported":
		fmt.Fprintln(os.Stdout, "relative\nextra\nunsupported\noutput")
		return
	case "stdout-limit":
		fmt.Fprint(os.Stdout, strings.Repeat("x", 128<<10))
		return
	case "stderr-limit":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 128<<10))
		return
	case "wait":
		if os.WriteFile(paths[0]+"-ready", []byte("ready"), 0600) != nil {
			os.Exit(76)
		}
		time.Sleep(time.Minute)
		return
	case "changed":
		if os.Rename(paths[0], paths[0]+"-old") != nil || os.Mkdir(paths[0], 0700) != nil {
			os.Exit(75)
		}
	}
	fmt.Fprint(os.Stdout, strings.Join(paths, "\n")+"\n")
}

func resolverFixture(t *testing.T, ctx context.Context, linked bool) (*Resolver, []string) {
	t.Helper()
	base := t.TempDir()
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "checkout")
	common := filepath.Join(base, "separate-metadata")
	git := common
	if linked {
		git = filepath.Join(base, "linked-metadata")
	}
	for _, path := range []string{root, common, git} {
		if err = os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	paths := []string{root, git, common}
	raw, _ := json.Marshal(paths)
	t.Setenv("ARM_GITMETA_FIXTURE", "1")
	t.Setenv("ARM_GITMETA_PATHS", string(raw))
	t.Setenv("ARM_GITMETA_MODE", "")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_CONFIG_PARAMETERS"} {
		t.Setenv(key, "ignored-fixture-value")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	budget := NewBudget(ctx)
	t.Cleanup(budget.Close)
	return &Resolver{budget: budget, binary: binary}, paths
}

func TestGitMetadataClosedProbeSeparateAndLinkedRelationships(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(fmt.Sprint(linked), func(t *testing.T) {
			resolver, paths := resolverFixture(t, t.Context(), linked)
			info, err := resolver.Resolve(paths[0])
			if err != nil || info.Root.Path != paths[0] || info.Git.Path != paths[1] || info.Common.Path != paths[2] || info.Primary() == linked || info.Root.Identity == "" {
				t.Fatal("closed probe did not retain actual relationships", err)
			}
			if usage := resolver.budget.Usage(); usage.Probes != 1 || usage.Bytes != int64(len(strings.Join(paths, "\n"))+1) {
				t.Fatal("probe/output accounting mismatch", usage)
			}
		})
	}
}

func TestGitMetadataUnsupportedAndFailedProbesStayCharged(t *testing.T) {
	for _, mode := range []string{"failure", "bare", "unsupported", "stdout-limit", "stderr-limit"} {
		t.Run(mode, func(t *testing.T) {
			resolver, paths := resolverFixture(t, t.Context(), false)
			t.Setenv("ARM_GITMETA_MODE", mode)
			if _, err := resolver.Resolve(paths[0]); err == nil {
				t.Fatal("failed metadata was accepted")
			}
			if usage := resolver.budget.Usage(); usage.Probes != 1 || usage.Bytes <= 0 {
				t.Fatal("failed attempt was not charged", usage)
			}
		})
	}
}

func TestGitMetadataSharedProbeAndOutputBudgets(t *testing.T) {
	parentDeadline := time.Now().Add(time.Second)
	parent, cancel := context.WithDeadline(t.Context(), parentDeadline)
	bounded := NewBudget(parent)
	deadline, exists := bounded.Context().Deadline()
	if !exists || deadline.After(parentDeadline) || deadline.After(bounded.started.Add(OperationTimeout)) {
		t.Fatal("operation deadline exceeded parent or entry budget")
	}
	bounded.Close()
	cancel()
	standalone := NewBudget(t.Context())
	deadline, exists = standalone.Context().Deadline()
	if !exists || deadline.After(standalone.started.Add(OperationTimeout)) {
		t.Fatal("operation did not retain its entry deadline")
	}
	standalone.Close()

	resolver, paths := resolverFixture(t, t.Context(), false)
	resolver.budget.usage.Probes = MaxProbes - 1
	if _, err := resolver.Resolve(paths[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(paths[0]); !errors.Is(err, ErrBudget) || resolver.budget.Usage().Probes != MaxProbes {
		t.Fatal("exhausted shared probe budget was reset", err)
	}
	resolver, paths = resolverFixture(t, t.Context(), false)
	resolver.budget.usage.Bytes = MaxOutputBytes - 1
	if _, err := resolver.Resolve(paths[0]); !errors.Is(err, ErrBudget) || resolver.budget.Usage().Bytes <= MaxOutputBytes {
		t.Fatal("failed output did not consume the shared byte budget", err)
	}
	if _, err := resolver.Resolve(paths[0]); !errors.Is(err, ErrBudget) || resolver.budget.Usage().Probes != 1 {
		t.Fatal("an exhausted operation started another probe", err)
	}
	parent, cancel = context.WithCancel(t.Context())
	resolver, paths = resolverFixture(t, parent, false)
	if _, err := resolver.Resolve(paths[0]); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := resolver.Resolve(paths[0]); !errors.Is(err, context.Canceled) || resolver.budget.Usage().Probes != 1 {
		t.Fatal("cancelled caller started another probe", err)
	}
}

func TestGitMetadataCancellationJoinsOriginalProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	resolver, paths := resolverFixture(t, ctx, false)
	t.Setenv("ARM_GITMETA_MODE", "wait")
	ready := make(chan bool, 1)
	go func() {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(paths[0] + "-ready"); err == nil {
				cancel()
				ready <- true
				return
			}
			time.Sleep(time.Millisecond)
		}
		cancel()
		ready <- false
	}()
	started := time.Now()
	_, err := resolver.Resolve(paths[0])
	if !<-ready || !errors.Is(err, context.Canceled) {
		t.Fatal("probe did not retain parent cancellation", err)
	}
	if time.Since(started) > 2*time.Second || resolver.budget.Usage().Probes != 1 {
		t.Fatal("original owned probe exceeded its cancellation boundary")
	}
}

func TestGitMetadataRejectsChangedDirectoryAndMissingExecutable(t *testing.T) {
	resolver, paths := resolverFixture(t, t.Context(), false)
	t.Setenv("ARM_GITMETA_MODE", "changed")
	if _, err := resolver.Resolve(paths[0]); !errors.Is(err, ErrChanged) {
		t.Fatal("replaced selected directory was accepted", err)
	}
	resolver, paths = resolverFixture(t, t.Context(), false)
	resolver.binary = filepath.Join(t.TempDir(), "missing-executable")
	if _, err := resolver.Resolve(paths[0]); !errors.Is(err, ErrUnsupported) || resolver.budget.Usage().Probes != 1 {
		t.Fatal("failed start did not remain refused/charged", err)
	}
}
