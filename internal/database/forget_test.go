package database

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

const forgetPayload = "PRIVATE original ledger payload, never a receipt or seed"

func forgetFixture(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range append([]string{"daemon.lock"}, forgetDatabaseNames...) {
		payload := []byte(nil)
		if name != "daemon.lock" {
			payload = []byte(forgetPayload + name)
		}
		if err = os.WriteFile(filepath.Join(home, name), payload, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}
func forgetSnapshot(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data := []byte(nil)
		if info.Mode().IsRegular() {
			data, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		out[path] = fmt.Sprintf("%v:%x", info.Mode(), sha256.Sum256(data))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
func forgetNoPayload(t *testing.T, home string) {
	t.Helper()
	if err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte(forgetPayload)) {
			t.Errorf("old database payload remains at %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestForgetOpaqueGroupErasedWithoutCopy(t *testing.T) {
	home := forgetFixture(t)
	before := forgetSnapshot(t, home)
	r, err := ReviewForget(t.Context(), home, "explicit loss of corrupt fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Plan.Proof.Complete || r.Plan.Proof.Settled || !r.Plan.Forget || r.Plan.OldHost != "" || len(r.Plan.Files) != 4 || r.review.source.DB != nil || r.review.source.temp != "" {
		t.Fatal("forget fabricated proof or copied/opened SQLite")
	}
	if !reflect.DeepEqual(before, forgetSnapshot(t, home)) {
		t.Fatal("review changed source")
	}
	result, err := r.Apply(t.Context())
	r.Close()
	if err != nil || !result.Completed {
		t.Fatal(result, err)
	}
	forgetNoPayload(t, home)
	for _, name := range forgetDatabaseNames {
		if _, err = os.Lstat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Fatal("old group remains", name, err)
		}
	}
	c, err := ReviewResetContinuation(t.Context(), home, result.Operation)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.Receipt.Plan.Forget || c.Receipt.Plan.Proof.Settled {
		t.Fatal("receipt lost explicit forget contract")
	}
	if replay, err := c.Continue(t.Context()); err != nil || !replay.Completed {
		t.Fatal(replay, err)
	}
	forgetNoPayload(t, home)
}
func TestForgetJournaledInterruptionContinuation(t *testing.T) {
	for _, stage := range []string{"receipt-staged", "seed-built", "fence-intent", "fence-published", "before-move:state.db-journal", "after-move:state.db-journal", "seed-pointer-published", "before-erase:state.db-journal", "after-erase:state.db-journal", "completed-receipt", "fence-removed"} {
		t.Run(stage, func(t *testing.T) {
			home := forgetFixture(t)
			r, err := ReviewForget(t.Context(), home, "explicit interrupted loss", false)
			if err != nil {
				t.Fatal(err)
			}
			id := r.Plan.ID
			lost := errors.New("lost acknowledgement")
			_, err = r.apply(t.Context(), func(at string) error {
				if at == stage {
					return lost
				}
				return nil
			})
			r.Close()
			if !errors.Is(err, lost) {
				t.Fatal("wrong interruption", err)
			}
			c, err := ReviewResetContinuation(t.Context(), home, id)
			if err != nil {
				t.Fatal(err)
			}
			if !c.Receipt.Plan.Forget || c.Receipt.Plan.ID != id {
				t.Fatal("immutable intent changed")
			}
			result, err := c.Continue(t.Context())
			c.Close()
			if err != nil || !result.Completed {
				t.Fatal(result, err)
			}
			forgetNoPayload(t, home)
		})
	}
}
func TestForgetNativeOwnershipAndBusyFences(t *testing.T) {
	for _, damage := range []string{"busy", "symlink", "hardlink", "journal-symlink", "journal-hardlink", "home-alias", "wrong-mode"} {
		t.Run(damage, func(t *testing.T) {
			home := forgetFixture(t)
			path := filepath.Join(home, "state.db")
			if damage == "busy" {
				f, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
					t.Fatal(err)
				}
			}
			if damage == "journal-symlink" || damage == "journal-hardlink" {
				path += "-journal"
			}
			if damage == "symlink" || damage == "journal-symlink" {
				if err := os.Rename(path, path+".keep"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".keep", path); err != nil {
					t.Fatal(err)
				}
			}
			if damage == "hardlink" || damage == "journal-hardlink" {
				if err := os.Link(path, path+".keep"); err != nil {
					t.Fatal(err)
				}
			}
			if damage == "home-alias" {
				alias := home + "-alias"
				if err := os.Symlink(home, alias); err != nil {
					t.Fatal(err)
				}
				defer os.Remove(alias)
				home = alias
			}
			if damage == "wrong-mode" {
				if err := os.Chmod(path, 0666); err != nil {
					t.Fatal(err)
				}
			}
			// A symlink root is not traversed by the snapshot walker; target snapshots
			// are additionally covered by replacement/preview/CLI tests.
			before := forgetSnapshot(t, home)
			if r, err := ReviewForget(t.Context(), home, "must refuse unsafe native target", false); err == nil {
				r.Close()
				t.Fatal("unsafe forget accepted")
			}
			if !reflect.DeepEqual(before, forgetSnapshot(t, home)) {
				t.Fatal("refusal mutated source")
			}
		})
	}
}
func TestForgetRevalidatesReplacementAndModeBeforeMutation(t *testing.T) {
	for _, damage := range []string{"replacement", "mode-bit", "forged-settlement", "missing-journal"} {
		t.Run(damage, func(t *testing.T) {
			home := forgetFixture(t)
			r, err := ReviewForget(t.Context(), home, "bound exact intent", false)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			switch damage {
			case "replacement":
				path := filepath.Join(home, "state.db")
				raw, _ := os.ReadFile(path)
				if err = os.Rename(path, path+".keep"); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "mode-bit":
				r.Plan.Forget = false
			case "forged-settlement":
				r.Plan.Proof.Settled = true
			case "missing-journal":
				r.Plan.Files = r.Plan.Files[:3]
			}
			r.Plan.SHA256, err = planDigest(r.Plan)
			if err != nil {
				t.Fatal(err)
			}
			before := forgetSnapshot(t, home)
			if _, err = r.Apply(t.Context()); err == nil {
				t.Fatal("changed review erased state")
			}
			if !reflect.DeepEqual(before, forgetSnapshot(t, home)) {
				t.Fatal("changed review mutated state")
			}
		})
	}
}
func TestForgetCancellationRequiresExplicitContinuation(t *testing.T) {
	home := forgetFixture(t)
	r, err := ReviewForget(t.Context(), home, "explicit cancel fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result, err := r.apply(ctx, func(at string) error {
		if at == "before-erase:state.db-journal" {
			cancel()
			return ctx.Err()
		}
		return nil
	})
	r.Close()
	if !errors.Is(err, context.Canceled) || result.Completed {
		t.Fatal(result, err)
	}
	c, err := ReviewResetContinuation(t.Context(), home, result.Operation)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	result, err = c.Continue(t.Context())
	if err != nil || !result.Completed {
		t.Fatal(result, err)
	}
	forgetNoPayload(t, home)
}

func attachForgetBookkeeping(t *testing.T, home string) {
	t.Helper()
	for _, name := range []string{"mesh", "dashboard-pending", "ssh-source-history", "ssh-source-history/packs", "ssh-source-history/packs/pack-private"} {
		if err := os.Mkdir(filepath.Join(home, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"mesh/ssh-pins.lock", "dashboard-pending/.lock", "ssh-source-history/.worker.lock", "ssh-source-history/packs/.historypack.lock"} {
		if err := os.WriteFile(filepath.Join(home, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"dashboard-pending/uncertain.json", "ssh-source-history/catalog.db", "ssh-source-history/packs/pack-private/manifest.json", "ssh-source-history/packs/pack-private/records.bin"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(forgetPayload+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestForgetPendingAndHistoryErasedWithLostAcknowledgements(t *testing.T) {
	for _, stage := range []string{"before-move:forget:dashboard-pending", "after-move:forget:dashboard-pending", "after-move:forget:ssh-source-history", "after-erase:forget:dashboard-pending/uncertain.json", "after-erase:forget:dashboard-pending/.lock", "after-erase:forget:dashboard-pending/", "after-erase:forget:ssh-source-history/packs/pack-private/records.bin", "after-erase:forget:ssh-source-history/packs/.historypack.lock", "after-erase:forget:ssh-source-history/packs", "after-erase:forget:ssh-source-history/catalog.db", "after-erase:forget:ssh-source-history/.worker.lock", "after-erase:forget:ssh-source-history/"} {
		t.Run(stage, func(t *testing.T) {
			home := forgetFixture(t)
			attachForgetBookkeeping(t, home)
			r, err := ReviewForget(t.Context(), home, "forget old client and history intent", true)
			if err != nil {
				t.Fatal(err)
			}
			lost := errors.New("lost exact native bookkeeping acknowledgement")
			result, err := r.apply(t.Context(), func(at string) error {
				if at == stage {
					return lost
				}
				return nil
			})
			r.Close()
			if !errors.Is(err, lost) {
				t.Fatal("wrong interruption", result, err)
			}
			c, err := ReviewResetContinuation(t.Context(), home, result.Operation)
			if err != nil {
				t.Fatal(err)
			}
			result, err = c.Continue(t.Context())
			c.Close()
			if err != nil || !result.Completed {
				t.Fatal(result, err)
			}
			forgetNoPayload(t, home)
			for _, name := range []string{"dashboard-pending", "ssh-source-history"} {
				if _, err := os.Lstat(filepath.Join(home, name)); !os.IsNotExist(err) {
					t.Fatal("old authority bookkeeping remains live", name, err)
				}
			}
		})
	}
}
func TestForgetBookkeepingNativeChangesRefuse(t *testing.T) {
	for _, damage := range []string{"symlink", "hardlink", "replacement", "new-entry", "worker-busy", "archive-replacement"} {
		t.Run(damage, func(t *testing.T) {
			home := forgetFixture(t)
			attachForgetBookkeeping(t, home)
			path := filepath.Join(home, "ssh-source-history", "catalog.db")
			if damage == "symlink" {
				os.Rename(path, path+".keep")
				os.Symlink(path+".keep", path)
			}
			if damage == "hardlink" {
				os.Link(path, path+".keep")
			}
			if damage == "worker-busy" {
				f, err := os.Open(filepath.Join(home, "ssh-source-history/.worker.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
					t.Fatal(err)
				}
			}
			before := forgetSnapshot(t, home)
			r, err := ReviewForget(t.Context(), home, "exact native exclusion", true)
			if damage == "symlink" || damage == "hardlink" || damage == "worker-busy" {
				if err == nil {
					r.Close()
					t.Fatal("unsafe bookkeeping accepted")
				}
				if !reflect.DeepEqual(before, forgetSnapshot(t, home)) {
					t.Fatal("unsafe refusal changed state")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if damage == "replacement" {
				os.Rename(path, path+".keep")
				os.WriteFile(path, []byte(forgetPayload+"replacement"), 0600)
			}
			if damage == "new-entry" {
				os.WriteFile(filepath.Join(home, "dashboard-pending/new-intent.json"), []byte("new intent"), 0600)
			}
			if damage != "archive-replacement" {
				before = forgetSnapshot(t, home)
				if _, err = r.Apply(t.Context()); err == nil {
					t.Fatal("changed bookkeeping erased")
				}
				if !reflect.DeepEqual(before, forgetSnapshot(t, home)) {
					t.Fatal("changed review mutated source")
				}
				return
			}
			replaced := false
			result, err := r.apply(t.Context(), func(at string) error {
				if at == "before-erase:forget:ssh-source-history/catalog.db" {
					op := filepath.Join(home, ResetDirectoryName, "reset-"+r.Plan.ID, "ssh-source-history/catalog.db")
					raw, _ := os.ReadFile(op)
					os.Rename(op, op+".keep")
					os.WriteFile(op, raw, 0600)
					replaced = true
				}
				return nil
			})
			if !replaced || err == nil || result.Completed {
				t.Fatal("quarantined replacement erased", result, err)
			}
		})
	}
}
func TestForgetDescriptorMountTransitionsAndUnavailableIdentityRefuse(t *testing.T) {
	home := forgetFixture(t)
	attachForgetBookkeeping(t, home)
	d, err := OpenDirectory(home)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, suffix := range []string{"ssh-source-history", "catalog.db", "pack-private", "records.bin"} {
		t.Run(suffix, func(t *testing.T) {
			probe := func(f *os.File) (string, error) {
				if filepath.Base(f.Name()) == suffix {
					return "same-device-different-mount", nil
				}
				return "original-mount", nil
			}
			before := forgetSnapshot(t, home)
			if _, err := scanForgetTreeMount(t.Context(), d, "ssh-source-history", probe); err == nil {
				t.Fatal("mount transition accepted")
			}
			if !reflect.DeepEqual(before, forgetSnapshot(t, home)) {
				t.Fatal("mount refusal changed source")
			}
		})
	}
	if err = requireForgetMount(d.FD, "original", func(*os.File) (string, error) { return "", errors.New("mount metadata unavailable") }); err == nil {
		t.Fatal("missing mount metadata accepted")
	}
}

func TestForgetRemovesPendingGenerationAgainstStaleOpenWriter(t *testing.T) {
	home := forgetFixture(t)
	attachForgetBookkeeping(t, home)
	old, err := os.Open(filepath.Join(home, "dashboard-pending"))
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	lock, err := os.OpenFile(filepath.Join(home, "dashboard-pending/.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	oldInfo, err := old.Stat()
	if err != nil {
		t.Fatal(err)
	}
	r, err := ReviewForget(t.Context(), home, "rotate pending generation", true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Apply(t.Context())
	r.Close()
	if err != nil || !result.Completed {
		t.Fatal(result, err)
	}
	if err = os.Mkdir(filepath.Join(home, "dashboard-pending"), 0700); err != nil {
		t.Fatal(err)
	}
	current, err := os.Stat(filepath.Join(home, "dashboard-pending"))
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(oldInfo, current) {
		t.Fatal("pending directory generation reused")
	}
	// Model a preempted client that opened both old descriptors before reset,
	// then acquires the old lock only after reset releases it. The existing TUI
	// named-generation recheck rejects this owner; even a raw old-dir write fails.
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Openat(int(old.Fd()), "stale-intent.json", unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY, 0600)
	if err == nil {
		unix.Close(fd)
		t.Fatal("stale descriptor recreated intent in removed generation")
	}
	if _, err = os.Lstat(filepath.Join(home, "dashboard-pending/stale-intent.json")); !os.IsNotExist(err) {
		t.Fatal("old intent repopulated new live authority", err)
	}
}

func TestForgetSubprocessHelper(t *testing.T) {
	if os.Getenv("ARM_TEST_FORGET_PROCESS") != "1" {
		return
	}
	home, id, stage := os.Getenv("ARM_TEST_FORGET_HOME"), os.Getenv("ARM_TEST_FORGET_ID"), os.Getenv("ARM_TEST_FORGET_STAGE")
	if !strings.HasPrefix(filepath.Base(home), "arm-forget-process-") || !validHost(id) {
		t.Fatal("helper accepts only owned disposable fixtures")
	}
	c, err := ReviewResetContinuation(t.Context(), home, id)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	result, err := c.continueReset(t.Context(), func(at string) error {
		if at == stage {
			os.Exit(73)
		}
		return nil
	})
	if err != nil || !result.Completed {
		t.Fatal(result, err)
	}
}
func TestForgetActualProcessLossAndKeyedContinuation(t *testing.T) {
	root, err := os.MkdirTemp("", "arm-forget-process-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	fixture := forgetFixture(t)
	// Move only this test's disposable fixture, preserving exact owned inodes.
	if err = os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(fixture, root); err != nil {
		t.Fatal(err)
	}
	attachForgetBookkeeping(t, root)
	r, err := ReviewForget(t.Context(), root, "actual process loss fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	lost := errors.New("staged for independent process")
	result, err := r.apply(t.Context(), func(at string) error {
		if at == "receipt-staged" {
			return lost
		}
		return nil
	})
	r.Close()
	if !errors.Is(err, lost) {
		t.Fatal(result, err)
	}
	run := func(stage string) error {
		cmd := exec.Command(os.Args[0], "-test.run=^TestForgetSubprocessHelper$")
		cmd.Env = append(os.Environ(), "ARM_TEST_FORGET_PROCESS=1", "ARM_TEST_FORGET_HOME="+root, "ARM_TEST_FORGET_ID="+result.Operation, "ARM_TEST_FORGET_STAGE="+stage)
		out, err := cmd.CombinedOutput()
		if stage == "finish" && err != nil {
			t.Fatalf("continuation subprocess: %s %v", out, err)
		}
		return err
	}
	var exit *exec.ExitError
	if err = run("after-erase:forget:ssh-source-history/catalog.db"); !errors.As(err, &exit) || exit.ExitCode() != 73 {
		t.Fatal("process did not lose exact unlink acknowledgement", err)
	}
	if err = run("finish"); err != nil {
		t.Fatal(err)
	}
	forgetNoPayload(t, root)
}

func TestForgetCombinedInventoryAndReceiptBudgetBeforeMutation(t *testing.T) {
	home := forgetFixture(t)
	r, err := ReviewForget(t.Context(), home, "bounded receipt fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	before := forgetSnapshot(t, home)
	plan := r.Plan
	plan.ForgetTrees = append([]ForgetTree{}, plan.ForgetTrees...)
	tree := plan.ForgetTrees[0]
	tree.Root = r.review.source.Directory.identity
	tree.Root.Name = tree.Name
	tree.Entries = []ForgetEntry{}
	prefix := ""
	for _, letter := range []string{"a", "b", "c", "d"} {
		name := strings.Repeat(letter, 128)
		id := tree.Root
		id.Name = name
		path := prefix + name
		tree.Entries = append(tree.Entries, ForgetEntry{path, id})
		prefix = path + "/"
	}
	for i := 0; i < 700; i++ {
		name := strings.Repeat("x", 124) + fmt.Sprintf("%04d", i)
		id := plan.Files[0]
		id.Name = name
		id.Size = 1
		tree.Entries = append(tree.Entries, ForgetEntry{prefix + name, id})
	}
	plan.ForgetTrees[0] = tree
	if err = validForgetTrees(plan); err != nil {
		t.Fatal("synthetic bounded inventory is invalid", err)
	}
	if err = checkForgetReceiptBudget(plan); err == nil {
		t.Fatal("large inventory admitted without space for erase progress")
	}
	plan.ForgetTrees[1].Root = tree.Root
	plan.ForgetTrees[1].Root.Name = "ssh-source-history"
	plan.ForgetTrees[1].Entries = append([]ForgetEntry{}, tree.Entries...)
	if err = validForgetTrees(plan); err == nil {
		t.Fatal("combined progress collection can exceed reader bound")
	}
	if !reflect.DeepEqual(before, forgetSnapshot(t, home)) {
		t.Fatal("budget refusal changed source")
	}
}
