package rollout

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectRemovalPreservesHistoryAndFencesAdoption(t *testing.T) {
	f := setup(t)
	f.project("all")
	in, d := f.identify(time.Time{})
	f.ack(in.Query, d)
	before, _ := os.ReadFile(filepath.Join(f.Home, "rollout.json"))
	c := Change{Action: "remove-project", Project: "demo", Reason: "Retire reviewed project"}
	plan, err := f.Preview(c)
	if err != nil || !plan.After.Removed || plan.After.Mode != "disabled" || len(plan.Affected) != 1 || plan.Affected[0].State != "revoked" {
		t.Fatal(plan, err)
	}
	after, _ := os.ReadFile(filepath.Join(f.Home, "rollout.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("preview changed ledger")
	}
	if _, err = f.Apply(c, plan.Review); err != nil {
		t.Fatal(err)
	}
	st, err := f.Read()
	if err != nil || len(st.Projects) != 1 || len(st.Sessions) != 1 || st.Sessions[0].State != "revoked" || st.History[len(st.History)-1].Action != "remove-project" || !st.History[len(st.History)-1].Policy.Removed {
		t.Fatal(st, err)
	}
	d, err = f.Check(in.Query)
	if err != nil || d.Coordinate || d.Eligible || !strings.Contains(d.Why, "removed") {
		t.Fatal(d, err)
	}
	if _, err = f.Identify(in); err == nil {
		t.Fatal("removed project allowed discovery")
	}
	for _, action := range []string{"project", "onboard", "policy", "resume", "remove-project"} {
		change := Change{Action: action, Project: "demo", Reason: "Must not resurrect", Mode: "all"}
		if action == "project" || action == "onboard" {
			change.Binding = &Binding{Kind: "directory", Root: f.root}
			change.Mode = ""
		}
		if action != "onboard" && action != "policy" {
			change.Mode = ""
		}
		if _, err = f.Preview(change); err == nil {
			t.Fatal("removed project ID reused", action)
		}
	}
	// A different, explicitly reviewed project may claim the same directory.
	f.change(Change{Action: "project", Project: "replacement", Binding: &Binding{Kind: "directory", Root: f.root}})
	d, err = f.Check(in.Query)
	if err != nil || d.Project == nil || d.Project.ID != "replacement" || d.Coordinate || d.Eligible {
		t.Fatal(d, err)
	}
}

func TestBindingRemovalBlocksAncestorFallbackAndCanBeRebound(t *testing.T) {
	f := setup(t)
	parent := f.root
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	f.project("all")
	f.change(Change{Action: "project", Project: "child", Binding: &Binding{Kind: "directory", Root: child}})
	f.change(Change{Action: "policy", Project: "child", Mode: "all"})
	f.root = child
	in, d := f.identify(time.Time{})
	f.ack(in.Query, d)
	f.change(Change{Action: "remove-binding", Project: "child", Binding: &Binding{Root: child}})
	d, err := f.Check(in.Query)
	if err != nil || d.Eligible || d.Coordinate || !strings.Contains(d.Why, "removed") {
		t.Fatal("ancestor granted adoption", d, err)
	}
	f.change(Change{Action: "project", Project: "child", Binding: &Binding{Kind: "directory", Root: child}})
	d, err = f.Check(in.Query)
	if err != nil || d.Coordinate || d.Eligible || d.Session == nil || d.Session.State != "revoked" {
		t.Fatal("rebind inherited enrollment", d, err)
	}
	// Removing the project retains the same deny fence and does not fall back.
	f.change(Change{Action: "remove-project", Project: "child"})
	d, err = f.Check(in.Query)
	if err != nil || d.Coordinate || d.Eligible || !strings.Contains(d.Why, "removed") {
		t.Fatal(d, err)
	}
}

func TestBindingRemovalAllowsMissingPathsAndRequiresExactReview(t *testing.T) {
	f := setup(t)
	f.project("observe")
	if err := os.Rename(f.root, f.root+"-moved"); err != nil {
		t.Fatal(err)
	}
	c := Change{Action: "remove-binding", Project: "demo", Binding: &Binding{Root: f.root}, Reason: "Remove obsolete path"}
	plan, err := f.Preview(c)
	if err != nil {
		t.Fatal("missing path was not removable", err)
	}
	f.change(Change{Action: "pause"})
	if _, err = f.Apply(c, plan.Review); err == nil {
		t.Fatal("stale review applied")
	}
	plan, err = f.Preview(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Apply(c, "wrong"); err == nil {
		t.Fatal("unreviewed removal applied")
	}
	if _, err = f.Apply(c, plan.Review); err != nil {
		t.Fatal(err)
	}
	st, err := f.Read()
	if err != nil || !st.Projects[0].Bindings[0].Removed || !st.Projects[0].Paused {
		t.Fatal(st, err)
	}
	if _, err = f.Preview(c); err == nil {
		t.Fatal("duplicate removal accepted")
	}
	for _, bad := range []Change{
		{Action: "remove-binding"},
		{Action: "remove-binding", Binding: &Binding{Root: "relative"}},
		{Action: "remove-binding", Binding: &Binding{Root: f.root}, Client: &ClientBinding{Adapter: "codex", ID: "demo"}},
		{Action: "remove-project", Binding: &Binding{Root: f.root}},
	} {
		bad.Project, bad.Reason = "demo", "Reject ambiguous selector"
		if _, err = f.Preview(bad); err == nil {
			t.Fatal("bad removal accepted", bad)
		}
	}
}

func TestClientBindingRemovalRevokesOnlyItsParticipants(t *testing.T) {
	f := setup(t)
	f.project("all")
	f.change(Change{Action: "bind", Client: &ClientBinding{Adapter: "generic", ID: "client"}})
	in, d := f.identify(time.Time{})
	f.ack(in.Query, d)
	// A second identity explicitly reports the client-project mapping.
	other, _ := f.identify(time.Time{})
	other.ClientProject = "client"
	d, err := f.Identify(other)
	if err != nil {
		t.Fatal(err)
	}
	f.ack(other.Query, d)
	f.change(Change{Action: "remove-binding", Client: &ClientBinding{Adapter: "generic", ID: "client"}})
	d, err = f.Check(other.Query)
	if err != nil || d.Coordinate || d.Eligible {
		t.Fatal(d, err)
	}
	d, err = f.Check(in.Query)
	if err != nil || !d.Coordinate {
		t.Fatal("unrelated participant revoked", d, err)
	}
	f.change(Change{Action: "bind", Client: &ClientBinding{Adapter: "generic", ID: "client"}})
	d, err = f.Identify(other)
	if err != nil || d.Session.State != "revoked" || d.Coordinate {
		t.Fatal("mapping restored an old grant", d, err)
	}
}

func TestBoundaryInspectionReportsStaleRootsWithoutChangingLedger(t *testing.T) {
	f := setup(t)
	f.project("all")
	st, _ := f.Read()
	if got := InspectBoundaries(t.Context(), st.Projects); len(got) != 1 || got[0].State != "ready" {
		t.Fatal(got)
	}
	before, _ := os.ReadFile(filepath.Join(f.Home, "rollout.json"))
	if err := os.Rename(f.root, f.root+"-old"); err != nil {
		t.Fatal(err)
	}
	if got := InspectBoundaries(t.Context(), st.Projects); got[0].State != "stale" || !strings.Contains(got[0].Detail, "missing") {
		t.Fatal(got)
	}
	if err := os.Mkdir(f.root, 0700); err != nil {
		t.Fatal(err)
	}
	if got := InspectBoundaries(t.Context(), st.Projects); got[0].State != "stale" || !strings.Contains(got[0].Detail, "identity") {
		t.Fatal(got)
	}
	after, _ := os.ReadFile(filepath.Join(f.Home, "rollout.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("status rewrote ledger")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := InspectBoundaries(ctx, st.Projects); got[0].State != "unknown" {
		t.Fatal(got)
	}
	f.change(Change{Action: "remove-binding", Binding: &Binding{Root: f.root}})
	st, _ = f.Read()
	if got := InspectBoundaries(ctx, st.Projects); got[0].State != "removed" {
		t.Fatal(got)
	}
}

func TestBoundaryInspectionDetectsChangedGitMetadata(t *testing.T) {
	f := setup(t)
	if err := exec.Command("git", "-C", f.root, "init", "--quiet").Run(); err != nil {
		t.Fatal(err)
	}
	f.change(Change{Action: "project", Binding: &Binding{Kind: "git", Root: f.root}})
	st, _ := f.Read()
	if got := InspectBoundaries(t.Context(), st.Projects); got[0].State != "ready" {
		t.Fatal(got)
	}
	if err := os.Rename(filepath.Join(f.root, ".git"), filepath.Join(f.root, ".git-old")); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", f.root, "init", "--quiet").Run(); err != nil {
		t.Fatal(err)
	}
	if got := InspectBoundaries(t.Context(), st.Projects); got[0].State != "stale" {
		t.Fatal(got)
	}
}
