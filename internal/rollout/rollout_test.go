package rollout

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fixture struct {
	Store
	t    *testing.T
	at   time.Time
	root string
}

func setup(t *testing.T) *fixture {
	f := &fixture{t: t, at: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), root: t.TempDir()}
	f.Store = Store{Home: filepath.Join(t.TempDir(), "state"), Now: func() time.Time { return f.at }}
	return f
}
func (f *fixture) change(c Change) Plan {
	f.t.Helper()
	if c.Project == "" {
		c.Project = "demo"
	}
	if c.Reason == "" {
		c.Reason = "Explicit operator decision"
	}
	p, e := f.Preview(c)
	if e != nil {
		f.t.Fatal(e)
	}
	applied, e := f.Apply(c, p.Review)
	if e != nil {
		f.t.Fatal(e)
	}
	return applied
}
func (f *fixture) project(mode string) {
	f.t.Helper()
	f.change(Change{Action: "project", Binding: &Binding{Kind: "directory", Root: f.root}})
	if mode != "disabled" {
		f.change(Change{Action: "policy", Mode: mode})
	}
}
func (f *fixture) identify(start time.Time) (Identification, Decision) {
	f.t.Helper()
	in := Identification{Query: Query{CWD: f.root, Adapter: "generic", Session: uuid.NewString()}, Started: start, Protocol: Protocol, Needs: "Bounded CPU and memory for tests", Capabilities: []string{"poll"}}
	d, e := f.Identify(in)
	if e != nil {
		f.t.Fatal(e)
	}
	return in, d
}
func (f *fixture) ack(q Query, d Decision) Decision {
	f.t.Helper()
	out, e := f.Acknowledge(q, d.Session.Identity, d.Project.Revision)
	if e != nil {
		f.t.Fatal(e)
	}
	if !out.Coordinate {
		f.t.Fatal("ack did not enroll", out)
	}
	return out
}

func TestDisabledReadsAndDeniedWritesCreateNothing(t *testing.T) {
	f := setup(t)
	q := Query{CWD: f.root, Adapter: "generic", Session: uuid.NewString()}
	d, e := f.Check(q)
	if e != nil || d.Coordinate || d.Eligible || d.Project != nil {
		t.Fatal(d, e)
	}
	if _, e = f.Read(); e != nil {
		t.Fatal(e)
	}
	c := Change{Action: "project", Project: "demo", Binding: &Binding{Kind: "directory", Root: f.root}, Reason: "Review boundary"}
	p, e := f.Preview(c)
	if e != nil || p.After.Mode != "disabled" {
		t.Fatal(p, e)
	}
	if _, e = f.Apply(c, "wrong digest"); e == nil {
		t.Fatal("unreviewed change applied")
	}
	if _, e = f.Identify(Identification{Query: q, Protocol: Protocol, Needs: "CPU"}); e == nil {
		t.Fatal("disabled identification accepted")
	}
	if _, e = f.Acknowledge(q, "missing", 0); e == nil {
		t.Fatal("disabled acknowledgment accepted")
	}
	if _, e = os.Stat(f.Home); !os.IsNotExist(e) {
		t.Fatal("disabled operation created state", e)
	}
}

func TestManualCohortRequiresSeparateAcknowledgment(t *testing.T) {
	f := setup(t)
	f.project("observe")
	a, da := f.identify(f.at.Add(-time.Hour))
	b, db := f.identify(time.Time{})
	if da.Eligible || da.Coordinate || da.Session.State != "observed" {
		t.Fatal(da)
	}
	f.change(Change{Action: "invite", Sessions: []string{da.Session.Identity, db.Session.Identity}})
	if _, e := f.Acknowledge(a.Query, da.Session.Identity, da.Project.Revision+1); e == nil {
		t.Fatal("stale policy accepted")
	}
	da, e := f.Check(a.Query)
	if e != nil || !da.Eligible || da.Coordinate || da.Session.State != "invited" {
		t.Fatal(da, e)
	}
	f.ack(a.Query, da)
	db, e = f.Check(b.Query)
	if e != nil || db.Coordinate || !db.Eligible {
		t.Fatal("cohort invitation auto-enrolled peer", db, e)
	}
	// No contact, backend, resource ledger or instruction files are created.
	entries, e := os.ReadDir(f.Home)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.Name() != "rollout.json" && entry.Name() != "rollout.lock" {
			t.Fatal("unexpected side effect", entry.Name())
		}
	}
}

func TestNewSessionsUseOriginalStartAndNotNewTasks(t *testing.T) {
	f := setup(t)
	f.project("observe")
	old, oldD := f.identify(f.at.Add(-time.Hour))
	f.at = f.at.Add(time.Minute)
	f.change(Change{Action: "policy", Mode: "new-sessions"})
	cutoff := f.at
	f.at = f.at.Add(time.Minute)
	d, e := f.Identify(old)
	if e != nil || d.Eligible || d.Session.Identity != oldD.Session.Identity {
		t.Fatal("existing session treated as new task", d, e)
	}
	old.Started = f.at
	if _, e = f.Identify(old); e == nil {
		t.Fatal("existing session rewrote start time")
	}
	_, unknown := f.identify(time.Time{})
	if unknown.Eligible {
		t.Fatal("unknown start adopted", unknown)
	}
	_, same := f.identify(cutoff)
	if same.Eligible {
		t.Fatal("cutoff session adopted", same)
	}
	in, newD := f.identify(f.at.Add(-time.Second))
	if !newD.Eligible || newD.Coordinate {
		t.Fatal(newD)
	}
	f.ack(in.Query, newD)
	f.change(Change{Action: "invite", Sessions: []string{oldD.Session.Identity}})
	old.Started = cutoff.Add(-time.Minute - time.Hour)
	oldCheck, e := f.Check(old.Query)
	if e != nil || !oldCheck.Eligible {
		t.Fatal("explicit old-session exception failed", oldCheck, e)
	}
}

func TestPauseRevokeAndDisabledPreserveExistingParticipationRecords(t *testing.T) {
	f := setup(t)
	f.project("all")
	a, da := f.identify(time.Time{})
	f.ack(a.Query, da)
	b, db := f.identify(time.Time{})
	f.change(Change{Action: "pause"})
	da, e := f.Check(a.Query)
	if e != nil || !da.Coordinate {
		t.Fatal("pause ended existing adoption", da, e)
	}
	db, e = f.Check(b.Query)
	if e != nil || db.Eligible {
		t.Fatal("pause allowed enrollment", db, e)
	}
	f.change(Change{Action: "resume"})
	f.change(Change{Action: "revoke", Sessions: []string{da.Session.Identity}})
	da, e = f.Check(a.Query)
	if e != nil || da.Coordinate || da.Eligible || da.Session.State != "revoked" {
		t.Fatal(da, e)
	}
	f.at = f.at.Add(Liveness + time.Second)
	da, e = f.Identify(a)
	if e != nil || da.Eligible || da.Session.State != "revoked" {
		t.Fatal("expiry erased revocation", da, e)
	}
	f.change(Change{Action: "invite", Sessions: []string{da.Session.Identity}})
	da, e = f.Check(a.Query)
	if e != nil {
		t.Fatal(e)
	}
	f.ack(a.Query, da)
	f.change(Change{Action: "policy", Mode: "disabled"})
	da, e = f.Check(a.Query)
	if e != nil || da.Coordinate || da.Session.State != "enrolled" || !strings.Contains(da.Obligations, "remain in force") {
		t.Fatal("disable discarded obligations", da, e)
	}
	st, e := f.Read()
	if e != nil {
		t.Fatal(e)
	}
	if st.History[0].Policy.Mode != "disabled" {
		t.Fatal("audit snapshot changed retroactively")
	}
}

func TestStaleIdentitiesCannotInheritInvitationsOrEnrollment(t *testing.T) {
	f := setup(t)
	f.project("observe")
	in, d := f.identify(time.Time{})
	f.change(Change{Action: "invite", Sessions: []string{d.Session.Identity}})
	old := d.Session.Identity
	f.at = f.at.Add(Liveness)
	if _, e := f.Acknowledge(in.Query, old, d.Project.Revision); e == nil {
		t.Fatal("expired invitation acknowledged")
	}
	d, e := f.Identify(in)
	if e != nil || d.Session.Identity == old || d.Session.State != "observed" || d.Eligible {
		t.Fatal("stale invitation transferred", d, e)
	}
	if _, e = f.Preview(Change{Action: "invite", Project: "demo", Sessions: []string{old}, Reason: "Attempt stale invite"}); e == nil {
		t.Fatal("old identity invited")
	}
	f.change(Change{Action: "invite", Sessions: []string{d.Session.Identity}})
	f.ack(in.Query, d)
	f.at = f.at.Add(Liveness)
	d, e = f.Identify(in)
	if e != nil || d.Coordinate || d.Session.State != "observed" {
		t.Fatal("expired enrollment silently restored", d, e)
	}
	st, e := f.Read()
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, event := range st.History {
		if event.Action == "identify" {
			count++
		}
	}
	if count != 3 {
		t.Fatal("identity replacements missing from audit", count)
	}
}

func TestReviewDigestBindsStateRequestAndCanonicalDirectory(t *testing.T) {
	f := setup(t)
	f.project("observe")
	c := Change{Action: "policy", Project: "demo", Mode: "all", Reason: "Expand explicit pilot"}
	p, e := f.Preview(c)
	if e != nil {
		t.Fatal(e)
	}
	f.identify(time.Time{})
	if _, e = f.Apply(c, p.Review); e == nil {
		t.Fatal("stale review applied")
	}
	p, e = f.Preview(c)
	if e != nil {
		t.Fatal(e)
	}
	c.Mode = "new-sessions"
	if _, e = f.Apply(c, p.Review); e == nil {
		t.Fatal("changed request applied")
	}
	c = Change{Action: "project", Project: "other", Binding: &Binding{Kind: "directory", Root: filepath.Join(f.root, "bound")}, Reason: "Review original directory"}
	if e = os.Mkdir(c.Binding.Root, 0700); e != nil {
		t.Fatal(e)
	}
	p, e = f.Preview(c)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(c.Binding.Root, c.Binding.Root+"-old"); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(c.Binding.Root, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Apply(c, p.Review); e == nil {
		t.Fatal("replacement directory matched review")
	}
}

func TestDirectoryOverlapSymlinksAndClientMappingsContainAdoption(t *testing.T) {
	f := setup(t)
	f.project("all")
	nested := filepath.Join(f.root, "private")
	if e := os.Mkdir(nested, 0700); e != nil {
		t.Fatal(e)
	}
	f.change(Change{Action: "project", Project: "nested", Binding: &Binding{Kind: "directory", Root: nested}})
	d, e := f.Check(Query{CWD: nested})
	if e != nil || d.Project.ID != "nested" || d.Eligible {
		t.Fatal("broad policy bypassed nested disable", d, e)
	}
	sibling := f.root + "-sibling"
	if e = os.Mkdir(sibling, 0700); e != nil {
		t.Fatal(e)
	}
	defer os.Remove(sibling)
	d, e = f.Check(Query{CWD: sibling})
	if e != nil || d.Project != nil {
		t.Fatal("prefix sibling matched", d, e)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if e = os.Symlink(nested, alias); e != nil {
		t.Fatal(e)
	}
	d, e = f.Check(Query{CWD: alias})
	if e != nil || d.Project.ID != "nested" {
		t.Fatal("symlink escaped nested policy", d, e)
	}
	q := Query{CWD: f.root, Adapter: "codex", ClientProject: "client-project"}
	d, e = f.Check(q)
	if e != nil || d.Eligible || !strings.Contains(d.Why, "client project") {
		t.Fatal("unbound client got directory grant", d, e)
	}
	f.change(Change{Action: "bind", Client: &ClientBinding{Adapter: "codex", ID: "client-project"}})
	d, e = f.Check(q)
	if e != nil || !d.Eligible {
		t.Fatal("explicit client binding failed", d, e)
	}
	q.CWD = sibling
	d, e = f.Check(q)
	if e != nil || d.Project != nil {
		t.Fatal("client mapping escaped root", d, e)
	}
	// Retargeting a configured root invalidates its recorded identity.
	if e = os.Rename(nested, nested+"-old"); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(nested, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Check(Query{CWD: nested}); e == nil {
		t.Fatal("replaced directory accepted")
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	b, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal("git fixture", e, string(b))
	}
	return strings.TrimSpace(string(b))
}
func TestGitScopeExcludesNestedReposAndUnboundWorktrees(t *testing.T) {
	if _, e := exec.LookPath("git"); e != nil {
		t.Skip("git unavailable")
	}
	f := setup(t)
	git(t, f.root, "init", "--quiet")
	git(t, f.root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "Fixture")
	f.change(Change{Action: "project", Binding: &Binding{Kind: "git", Root: f.root}})
	f.change(Change{Action: "policy", Mode: "all"})
	sub := filepath.Join(f.root, "sub")
	if e := os.Mkdir(sub, 0700); e != nil {
		t.Fatal(e)
	}
	d, e := f.Check(Query{CWD: sub})
	if e != nil || !d.Eligible {
		t.Fatal(d, e)
	}
	git(t, sub, "init", "--quiet")
	d, e = f.Check(Query{CWD: sub})
	if e != nil || d.Eligible {
		t.Fatal("nested Git repo inherited policy", d, e)
	}
	wt := filepath.Join(t.TempDir(), "worktree")
	git(t, f.root, "worktree", "add", "--quiet", "--detach", wt)
	d, e = f.Check(Query{CWD: wt})
	if e != nil || d.Project != nil {
		t.Fatal("worktree silently inherited policy", d, e)
	}
	f.change(Change{Action: "project", Binding: &Binding{Kind: "git", Root: wt}})
	d, e = f.Check(Query{CWD: wt})
	if e != nil || !d.Eligible {
		t.Fatal("explicit worktree binding failed", d, e)
	}
	t.Setenv("GIT_DIR", filepath.Join(sub, ".git"))
	t.Setenv("GIT_WORK_TREE", sub)
	d, e = f.Check(Query{CWD: f.root})
	if e != nil || !d.Eligible {
		t.Fatal("Git environment redirected scope", d, e)
	}
}

func TestUnsupportedProtocolBoundsAndCorruptLedgerFailClosed(t *testing.T) {
	f := setup(t)
	f.project("all")
	in := Identification{Query: Query{CWD: f.root, Adapter: "generic", Session: uuid.NewString()}, Protocol: Protocol + 1, Needs: "CPU"}
	if _, e := f.Identify(in); e == nil {
		t.Fatal("unsupported protocol accepted")
	}
	in.Protocol = Protocol
	in.Needs = strings.Repeat("x", 513)
	if _, e := f.Identify(in); e == nil {
		t.Fatal("unbounded metadata accepted")
	}
	before, e := os.ReadFile(filepath.Join(f.Home, "rollout.json"))
	if e != nil {
		t.Fatal(e)
	}
	st, e := f.Read()
	if e != nil {
		t.Fatal(e)
	}
	st.Version++
	b, _ := json.Marshal(st)
	if e = os.WriteFile(filepath.Join(f.Home, "rollout.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Check(Query{CWD: f.root}); e == nil {
		t.Fatal("unknown ledger version adopted")
	}
	if e = os.WriteFile(filepath.Join(f.Home, "rollout.json"), before, 0600); e != nil {
		t.Fatal(e)
	}
	st, e = f.Read()
	if e != nil {
		t.Fatal(e)
	}
	for len(st.History) < maxEvents {
		st.Revision++
		p := st.Projects[0]
		st.History = append(st.History, Event{Revision: st.Revision, Project: p.ID, Policy: &p})
	}
	if e = f.save(st); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Preview(Change{Action: "policy", Project: "demo", Mode: "observe", Reason: "Check bounded history"}); e == nil {
		t.Fatal("audit overflow silently trimmed history")
	}
}

func TestGitRepositoryReplacementRequiresReviewedRebinding(t *testing.T) {
	if _, e := exec.LookPath("git"); e != nil {
		t.Skip("git unavailable")
	}
	f := setup(t)
	git(t, f.root, "init", "--quiet")
	f.change(Change{Action: "project", Binding: &Binding{Kind: "git", Root: f.root}})
	f.change(Change{Action: "policy", Mode: "all"})
	if e := os.Rename(filepath.Join(f.root, ".git"), filepath.Join(f.root, ".git-old")); e != nil {
		t.Fatal(e)
	}
	git(t, f.root, "init", "--quiet")
	d, e := f.Check(Query{CWD: f.root})
	if e != nil || d.Eligible || !strings.Contains(d.Why, "identity changed") {
		t.Fatal("replacement repository inherited policy", d, e)
	}
	f.change(Change{Action: "project", Binding: &Binding{Kind: "git", Root: f.root}})
	d, e = f.Check(Query{CWD: f.root})
	if e != nil || !d.Eligible {
		t.Fatal("explicit rebind did not recover", d, e)
	}
}

func TestLedgerMissingVersionUnknownFieldsAndNonregularFilesRefuse(t *testing.T) {
	f := setup(t)
	f.project("all")
	path := filepath.Join(f.Home, "rollout.json")
	for _, raw := range []string{`{"revision":0}`, `{"version":1,"revision":0,"future_policy":true}`, `{"version":1} {"version":1}`} {
		if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := f.Read(); e == nil {
			t.Fatal("invalid ledger accepted", raw)
		}
	}
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(t.TempDir(), "ledger")
	if e := os.WriteFile(outside, []byte(`{"version":1}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, path); e != nil {
		t.Fatal(e)
	}
	if _, e := f.Read(); e == nil {
		t.Fatal("symlink ledger accepted")
	}
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e := syscall.Mkfifo(path, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := f.Read(); e == nil {
		t.Fatal("FIFO ledger accepted")
	}
}

func TestClockRollbackAndRosterDoNotImplyLiveness(t *testing.T) {
	f := setup(t)
	f.project("all")
	in, d := f.identify(time.Time{})
	f.ack(in.Query, d)
	f.at = f.at.Add(-time.Second)
	d, e := f.Check(in.Query)
	if e != nil || d.Live || d.Coordinate {
		t.Fatal("clock rollback revived identity", d, e)
	}
	if _, e = f.Identify(in); e == nil {
		t.Fatal("clock rollback refreshed identity")
	}
	st, e := f.Read()
	if e != nil {
		t.Fatal(e)
	}
	roster := f.Roster(st, "demo")
	if len(roster) != 1 || roster[0].Live || roster[0].Coordinate {
		t.Fatal("roster claimed live participation", roster)
	}
}

func TestConcurrentApplyCannotConsumeOneReviewTwice(t *testing.T) {
	f := setup(t)
	f.project("observe")
	c := Change{Action: "policy", Project: "demo", Mode: "all", Reason: "One reviewed expansion"}
	p, e := f.Preview(c)
	if e != nil {
		t.Fatal(e)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() { <-start; _, err := f.Apply(c, p.Review); results <- err }()
	}
	close(start)
	successes := 0
	for range 2 {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal("one reviewed change applied more than once", successes)
	}
	st, e := f.Read()
	if e != nil || st.Revision != p.Revision+1 || st.Projects[0].Revision != p.Before.Revision+1 {
		t.Fatal(st, e)
	}
}
