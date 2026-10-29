package rollout

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectOnboardingIsAtomicAndReusesReviewedBinding(t *testing.T) {
	f := setup(t)
	c := Change{Action: "onboard", Project: "demo", Binding: &Binding{Kind: "directory", Root: f.root}, Mode: "new-sessions", Reason: "Explicit project onboarding"}
	p, e := f.Preview(c)
	if e != nil || p.After.Mode != "new-sessions" {
		t.Fatal(p, e)
	}
	if _, e = os.Stat(f.Home); !os.IsNotExist(e) {
		t.Fatal("onboarding preview initialized state", e)
	}
	if _, e = f.Apply(c, p.Review); e != nil {
		t.Fatal(e)
	}
	st, e := f.Read()
	if e != nil || len(st.Projects) != 1 || len(st.Projects[0].Bindings) != 1 || len(st.History) != 1 || st.Revision != 1 || !st.Projects[0].NewAfter.Equal(f.at) {
		t.Fatal(st, e)
	}
	cutoff := st.Projects[0].NewAfter
	f.change(Change{Action: "pause"})
	p, e = f.Preview(c)
	if e != nil || !p.Unchanged || !p.After.Paused || !p.After.NewAfter.Equal(cutoff) {
		t.Fatal("existing project state was broadened", p, e)
	}
	if _, e = f.Apply(c, p.Review); e != nil {
		t.Fatal(e)
	}
	st, e = f.Read()
	if e != nil || st.Revision != 2 || len(st.Projects[0].Bindings) != 1 || !st.Projects[0].Paused {
		t.Fatal(st, e)
	}
	if len(st.Sessions) != 0 {
		t.Fatal("onboarding enrolled sessions")
	}
}

func TestProjectOnboardingUnchangedPreservesInvitationsAndLedgerBytes(t *testing.T) {
	f := setup(t)
	f.project("observe")
	in, d := f.identify(time.Time{})
	f.change(Change{Action: "invite", Sessions: []string{d.Session.Identity}})
	before, e := os.ReadFile(filepath.Join(f.Home, "rollout.json"))
	if e != nil {
		t.Fatal(e)
	}
	c := Change{Action: "onboard", Project: "demo", Binding: &Binding{Kind: "directory", Root: f.root}, Mode: "observe", Reason: "Keep current project policy"}
	p, e := f.Preview(c)
	if e != nil || !p.Unchanged {
		t.Fatal(p, e)
	}
	if _, e = f.Apply(c, p.Review); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(filepath.Join(f.Home, "rollout.json"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("unchanged onboarding rewrote policy or invitations", e)
	}
	d, e = f.Check(in.Query)
	if e != nil || !d.Eligible || d.Session.State != "invited" {
		t.Fatal(d, e)
	}
}

func TestProjectOnboardingInvalidPolicyLeavesNoPartialProject(t *testing.T) {
	f := setup(t)
	c := Change{Action: "onboard", Project: "demo", Binding: &Binding{Kind: "directory", Root: f.root}, Mode: "unknown", Reason: "Invalid onboarding choice"}
	if _, e := f.Preview(c); e == nil {
		t.Fatal("unknown policy accepted")
	}
	if _, e := os.Stat(f.Home); !os.IsNotExist(e) {
		t.Fatal("invalid onboarding initialized state", e)
	}
	c.Mode = "all"
	p, e := f.Preview(c)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(f.root, f.root+"-old"); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(f.root, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Apply(c, p.Review); e == nil {
		t.Fatal("replacement directory inherited a reviewed grant")
	}
	if _, e = os.Stat(filepath.Join(f.Home, "rollout.json")); !os.IsNotExist(e) {
		t.Fatal("stale onboarding created policy", e)
	}
}
