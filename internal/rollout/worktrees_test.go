package rollout

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRepositoryScopeCoversRegisteredFutureWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	f := setup(t)
	git(t, f.root, "init", "--quiet")
	git(t, f.root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "Fixture")
	first := filepath.Join(t.TempDir(), "existing worktree")
	git(t, f.root, "worktree", "add", "--quiet", "--detach", first)
	primary, err := canonical(f.root)
	if err != nil {
		t.Fatal(err)
	}
	f.change(Change{Action: "onboard", Binding: &Binding{Kind: "git", Root: f.root}, Mode: "all"})
	batch := OnboardBatch{
		Version: 1, Context: OnboardReviewContext{Version: 1, Digest: strings.Repeat("a", 64)},
		Changes: []Change{{Action: "onboard", Project: "demo", Mode: "all", Reason: "Review the local repository", Binding: &Binding{Kind: WorktreesKind, Root: first}}},
	}
	plan, err := f.PreviewOnboardBatch(t.Context(), batch)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := f.ApplyOnboardBatch(t.Context(), batch, plan.Review)
	if err != nil {
		t.Fatal(err)
	}
	after := applied.Projects[0].After
	if len(after.Bindings) != 1 || after.Bindings[0].Root != primary || after.Mode != "all" {
		t.Fatal("repository binding did not replace the primary checkout binding", plan)
	}
	future := filepath.Join(t.TempDir(), "later worktree")
	git(t, f.root, "worktree", "add", "--quiet", "--detach", future)
	nested := filepath.Join(future, "subdir")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	check := func(cwd string, wantEligible bool) Decision {
		t.Helper()
		d, err := f.Check(Query{CWD: cwd})
		if err != nil || d.Eligible != wantEligible {
			t.Fatal(cwd, d, err)
		}
		return d
	}
	for _, cwd := range []string{f.root, first, future, nested} {
		check(cwd, true)
	}
	futureRoot, err := canonical(future)
	if err != nil {
		t.Fatal(err)
	}
	roots := RegisteredWorktreeRoots(t.Context(), after.Bindings[0])
	if len(roots) != 3 || roots[0] != primary || !slices.Contains(roots, futureRoot) {
		t.Fatal("wrong registry roots", roots)
	}
	pointer, err := os.ReadFile(filepath.Join(future, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	impostor := t.TempDir()
	if err = os.WriteFile(filepath.Join(impostor, ".git"), pointer, 0600); err != nil {
		t.Fatal(err)
	}
	check(impostor, false)
	redirect := filepath.Join(t.TempDir(), "registered")
	git(t, f.root, "worktree", "add", "--quiet", "--detach", redirect)
	moved := redirect + "-moved"
	if err = os.Rename(redirect, moved); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(moved, redirect); err != nil {
		t.Fatal(err)
	}
	check(redirect, false)
	git(t, nested, "init", "--quiet")
	check(nested, false)
	if err = os.RemoveAll(filepath.Join(nested, ".git")); err != nil {
		t.Fatal(err)
	}
	f.change(Change{Action: "project", Project: "isolated", Binding: &Binding{Kind: "git", Root: first}})
	if d := check(first, false); d.Project == nil || d.Project.ID != "isolated" {
		t.Fatal("explicit checkout lost its narrower policy", d)
	}
	q := Query{CWD: future, Adapter: "codex", Session: uuid.NewString()}
	d, err := f.Identify(Identification{Query: q, Protocol: Protocol, Needs: "Build resources"})
	if err != nil || d.Session == nil {
		t.Fatal(d, err)
	}
	f.ack(q, d)
	f.change(Change{Action: "remove-binding", Binding: &Binding{Root: primary}})
	f.change(Change{Action: "project", Binding: &Binding{Kind: WorktreesKind, Root: future}})
	d, err = f.Check(q)
	if err != nil || d.Coordinate || d.Eligible || d.Session == nil || d.Session.State != "revoked" {
		t.Fatal("replacing a removed scope restored participation", d, err)
	}
}
