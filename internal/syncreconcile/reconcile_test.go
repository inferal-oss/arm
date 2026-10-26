package syncreconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func file(s string) Entry {
	return Entry{Kind: File, Size: int64(len(s)), ContentHash: hash(s), MetadataHash: hash("mode:0644")}
}
func dir() Entry { return Entry{Kind: Directory, MetadataHash: hash("mode:0755")} }
func entries(kv ...any) map[string]Entry {
	m := map[string]Entry{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1].(Entry)
	}
	return m
}
func names(paths ...string) map[string]string {
	m := map[string]string{}
	for _, p := range paths {
		m[p] = p
	}
	return m
}
func baseRequest(l, r map[string]Entry) Request {
	keys := map[string]string{}
	for p := range l {
		keys[p] = p
	}
	for p := range r {
		keys[p] = p
	}
	return Request{Binding: Binding{Local: Endpoint{"host-a/account", "root-a/native-1"}, Remote: Endpoint{"host-b/account", "root-b/native-2"}, SelectionID: "all-v1", MetadataID: "mode-mtime-v1"}, Direction: Both, LocalPolicy: Additive, RemotePolicy: Additive, Local: Snapshot{l, keys}, Remote: Snapshot{r, keys}, FirstRun: true}
}
func act(s Side, k ActionKind, p string, b, a *Entry, from Side, sp string) Action {
	return Action{Side: s, Kind: k, Path: p, Before: b, After: a, Source: from, SourcePath: sp}
}
func ptr(e Entry) *Entry { return &e }
func requireActions(t *testing.T, p Plan, want ...Action) {
	t.Helper()
	got := p.Actions()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("actions\n got: %#v\nwant: %#v", got, want)
	}
}
func requireNoConflicts(t *testing.T, p Plan) {
	t.Helper()
	if got := p.Conflicts(); len(got) > 0 {
		t.Fatalf("conflicts: %#v", got)
	}
}
func confirmed(t *testing.T, p Plan) ConfirmedBaseline {
	t.Helper()
	b, ok := p.ProposedBaseline()
	if !ok {
		t.Fatal("no proposal")
	}
	return b
}
func nextReq(t *testing.T, r Request, p Plan, l, remote map[string]Entry) Request {
	t.Helper()
	b := confirmed(t, p)
	r.FirstRun = false
	r.Baseline = &b
	r.ExpectedGeneration = b.Generation
	r.Local.Entries = l
	r.Remote.Entries = remote
	for path := range l {
		r.Local.NameKeys[path] = path
		r.Remote.NameKeys[path] = path
	}
	for path := range remote {
		r.Local.NameKeys[path] = path
		r.Remote.NameKeys[path] = path
	}
	return r
}

func TestFirstRunAndOneWayPolicies(t *testing.T) {
	a, b := file("a"), file("b")
	r := baseRequest(entries("only-local", a), entries("only-remote", b))
	p, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	requireNoConflicts(t, p)
	requireActions(t, p, act(Remote, Create, "only-local", nil, ptr(a), Local, "only-local"), act(Local, Create, "only-remote", nil, ptr(b), Remote, "only-remote"))
	baseline := confirmed(t, p)
	if baseline.Generation != 1 || !baseline.Local["only-local"].Present || !baseline.Remote["only-remote"].Present {
		t.Fatalf("bad proposal %#v", baseline)
	}
	for _, tc := range []struct {
		name      string
		direction Direction
		policy    Policy
		want      []Action
	}{
		{"push-additive", Push, Additive, []Action{act(Remote, Create, "src", nil, ptr(a), Local, "src")}},
		{"push-full", Push, Full, []Action{act(Remote, Remove, "dst", ptr(b), nil, "", ""), act(Remote, Create, "src", nil, ptr(a), Local, "src")}},
		{"pull-additive", Pull, Additive, []Action{act(Local, Create, "dst", nil, ptr(b), Remote, "dst")}},
		{"pull-full", Pull, Full, []Action{act(Local, Remove, "src", ptr(a), nil, "", ""), act(Local, Create, "dst", nil, ptr(b), Remote, "dst")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := baseRequest(entries("src", a), entries("dst", b))
			r.Direction = tc.direction
			if tc.direction == Push {
				r.RemotePolicy = tc.policy
			} else {
				r.LocalPolicy = tc.policy
			}
			p, err := Reconcile(r)
			if err != nil {
				t.Fatal(err)
			}
			requireActions(t, p, tc.want...)
		})
	}
}

func TestAllPolicyPairsDeletionAndConvergence(t *testing.T) {
	a := file("base")
	for _, lp := range []Policy{Additive, Full} {
		for _, rp := range []Policy{Additive, Full} {
			t.Run(string(lp)+"/"+string(rp), func(t *testing.T) {
				r := baseRequest(entries("f", a), entries("f", a))
				r.LocalPolicy = lp
				r.RemotePolicy = rp
				initial, err := Reconcile(r)
				if err != nil {
					t.Fatal(err)
				}
				r = nextReq(t, r, initial, map[string]Entry{}, entries("f", a))
				p, err := Reconcile(r)
				if err != nil {
					t.Fatal(err)
				}
				requireNoConflicts(t, p)
				if rp == Full {
					requireActions(t, p, act(Remote, Remove, "f", ptr(a), nil, "", ""))
				} else {
					requireActions(t, p)
				}
				b := confirmed(t, p)
				if !b.Local["f"].Tombstone {
					t.Fatal("missing origin tombstone")
				}
				remote := map[string]Entry{}
				if rp == Additive {
					remote["f"] = a
				}
				r = nextReq(t, r, p, map[string]Entry{}, remote)
				again, err := Reconcile(r)
				if err != nil {
					t.Fatal(err)
				}
				requireActions(t, again)
				if rp == Additive { // Changing the receiver to full applies the retained tombstone.
					r.RemotePolicy = Full
					changedPolicy, err := Reconcile(r)
					if err != nil {
						t.Fatal(err)
					}
					requireActions(t, changedPolicy, act(Remote, Remove, "f", ptr(a), nil, "", ""))
				}
			})
		}
	}
}

func TestRemoteDeletionAllPolicyPairs(t *testing.T) {
	a := file("base")
	for _, lp := range []Policy{Additive, Full} {
		for _, rp := range []Policy{Additive, Full} {
			t.Run(string(lp)+"/"+string(rp), func(t *testing.T) {
				r := baseRequest(entries("f", a), entries("f", a))
				r.LocalPolicy = lp
				r.RemotePolicy = rp
				initial, _ := Reconcile(r)
				r = nextReq(t, r, initial, entries("f", a), map[string]Entry{})
				p, err := Reconcile(r)
				if err != nil {
					t.Fatal(err)
				}
				if lp == Full {
					requireActions(t, p, act(Local, Remove, "f", ptr(a), nil, "", ""))
				} else {
					requireActions(t, p)
				}
				b := confirmed(t, p)
				if !b.Remote["f"].Tombstone {
					t.Fatal("missing remote deletion tombstone")
				}
				if lp == Additive {
					r = nextReq(t, r, p, entries("f", a), map[string]Entry{})
					again, err := Reconcile(r)
					if err != nil {
						t.Fatal(err)
					}
					requireActions(t, again)
				}
			})
		}
	}
}

func TestRetainedCopyLaterEditIsNewChange(t *testing.T) {
	a, b := file("base"), file("edited-retained")
	r := baseRequest(entries("f", a), entries("f", a))
	p, _ := Reconcile(r)
	r = nextReq(t, r, p, map[string]Entry{}, entries("f", a))
	p, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	requireActions(t, p)
	r = nextReq(t, r, p, map[string]Entry{}, entries("f", b))
	p, err = Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	requireActions(t, p, act(Local, Create, "f", nil, ptr(b), Remote, "f"))
	r = nextReq(t, r, p, entries("f", b), entries("f", b))
	p, err = Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	requireActions(t, p)
}

func TestChangedStatesAndConflictResolutions(t *testing.T) {
	a, b, c := file("base"), file("local"), file("remote")
	for _, tc := range []struct {
		name    string
		l, r    map[string]Entry
		actions []Action
		reason  string
	}{
		{"one-edit", entries("f", b), entries("f", a), []Action{act(Remote, Overwrite, "f", ptr(a), ptr(b), Local, "f")}, ""},
		{"equal-edits", entries("f", b), entries("f", b), nil, ""},
		{"different-edits", entries("f", b), entries("f", c), nil, "concurrent changes"},
		{"delete-edit", map[string]Entry{}, entries("f", c), nil, "concurrent changes"},
		{"both-delete", map[string]Entry{}, map[string]Entry{}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := baseRequest(entries("f", a), entries("f", a))
			first, _ := Reconcile(r)
			r = nextReq(t, r, first, tc.l, tc.r)
			p, err := Reconcile(r)
			if err != nil {
				t.Fatal(err)
			}
			requireActions(t, p, tc.actions...)
			if tc.reason == "" {
				requireNoConflicts(t, p)
				b := confirmed(t, p)
				if tc.name == "both-delete" && (!b.Local["f"].Tombstone || !b.Remote["f"].Tombstone) {
					t.Fatal("both deletion tombstones missing")
				}
				return
			}
			cs := p.Conflicts()
			if len(cs) != 1 || cs[0].Path != "f" || cs[0].Reason != tc.reason {
				t.Fatalf("conflicts: %#v", cs)
			}
			if _, ok := p.ProposedBaseline(); ok {
				t.Fatal("conflicted plan has proposal")
			}
			for _, choice := range []ResolutionChoice{KeepLocal, KeepRemote, Delete} {
				r.Resolutions = []Resolution{{Path: "f", Choice: choice, ExpectedLocal: cs[0].Local, ExpectedRemote: cs[0].Remote}}
				resolved, err := Reconcile(r)
				if err != nil {
					t.Fatal(err)
				}
				requireNoConflicts(t, resolved)
				_ = confirmed(t, resolved)
			}
		})
	}
	r := baseRequest(entries("f", b), entries("f", c))
	p, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	cs := p.Conflicts()
	if len(cs) != 1 {
		t.Fatal(cs)
	}
	r.Resolutions = []Resolution{{Path: "f", Choice: KeepBoth, ExpectedLocal: ptr(b), ExpectedRemote: ptr(c), LocalName: "f.local", RemoteName: "f.remote"}}
	r.Local.NameKeys["f.local"] = "f.local"
	r.Local.NameKeys["f.remote"] = "f.remote"
	r.Remote.NameKeys["f.local"] = "f.local"
	r.Remote.NameKeys["f.remote"] = "f.remote"
	p, err = Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	requireNoConflicts(t, p)
	requireActions(t, p, act(Local, Remove, "f", ptr(b), nil, "", ""), act(Remote, Remove, "f", ptr(c), nil, "", ""), act(Local, Create, "f.local", nil, ptr(b), Local, "f"), act(Remote, Create, "f.local", nil, ptr(b), Local, "f"), act(Local, Create, "f.remote", nil, ptr(c), Remote, "f"), act(Remote, Create, "f.remote", nil, ptr(c), Remote, "f"))
}

func TestDirectoryResolutionCoversSubtree(t *testing.T) {
	a := file("a")
	r := baseRequest(entries("x", a, "x-keep", a), entries("x", dir(), "x/y", a, "x-keep", a))
	p, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	cs := p.Conflicts()
	if len(cs) != 1 || cs[0].Path != "x" {
		t.Fatalf("conflicts %#v", cs)
	}
	r.Resolutions = []Resolution{{Path: "x", Choice: KeepLocal, ExpectedLocal: ptr(a), ExpectedRemote: ptr(dir())}}
	p, err = Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	requireActions(t, p, act(Remote, Remove, "x/y", ptr(a), nil, "", ""), act(Remote, Overwrite, "x", ptr(dir()), ptr(a), Local, "x"))
}

func TestBindingUnknownBaselineAndRepresentation(t *testing.T) {
	r := baseRequest(entries("f", file("x")), map[string]Entry{})
	r.FirstRun = false
	if _, err := Reconcile(r); err == nil {
		t.Fatal("lost baseline accepted")
	}
	r.FirstRun = true
	p, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	r = nextReq(t, r, p, r.Local.Entries, r.Remote.Entries)
	r.Binding.Local.RootID = "replaced"
	if _, err := Reconcile(r); err == nil {
		t.Fatal("replaced root accepted")
	}
	r.Binding.Local.RootID = "root-a/native-1"
	r.Binding.SelectionID = "changed"
	if _, err := Reconcile(r); err == nil {
		t.Fatal("selection change accepted")
	}
	r.Binding.SelectionID = "all-v1"
	r.Baseline.Local["f"] = State{}
	if _, err := Reconcile(r); err == nil || !strings.Contains(err.Error(), "seal") {
		t.Fatalf("partial baseline accepted: %v", err)
	}
	r.Baseline.Local["f"] = State{Present: true, Entry: file("x")}
	r.ExpectedGeneration = 0
	if _, err := Reconcile(r); err == nil {
		t.Fatal("stale generation accepted")
	}
	r = baseRequest(entries("a", file("a")), entries("A", file("A")))
	r.Remote.NameKeys["a"] = "A"
	if _, err := Reconcile(r); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("wanted case collision, got %v", err)
	}
	r = baseRequest(entries("é", file("a")), entries("e\u0301", file("b")))
	r.Local.NameKeys["é"] = "e\u0301"
	r.Remote.NameKeys["é"] = "e\u0301"
	if _, err := Reconcile(r); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("wanted Unicode collision, got %v", err)
	}
}

func TestDeterminismAndCopies(t *testing.T) {
	a, b := file("a"), file("b")
	r := baseRequest(entries("z", a, "a", dir(), "a/x", b), map[string]Entry{})
	p, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	first := p.Digest()
	actions := p.Actions()
	actions[0].After.ContentHash = "altered"
	baseline := confirmed(t, p)
	baseline.Local["z"] = State{}
	if got := p.Actions()[0].After.ContentHash; got == "altered" {
		t.Fatal("actions alias plan")
	}
	if !confirmedState(p, "z") {
		t.Fatal("baseline aliases plan")
	}
	for i := 0; i < 20; i++ {
		again, err := Reconcile(r)
		if err != nil {
			t.Fatal(err)
		}
		if again.Digest() != first || !reflect.DeepEqual(again.Actions(), p.Actions()) {
			t.Fatal("nondeterministic plan")
		}
	}
}
func confirmedState(p Plan, path string) bool {
	b, _ := p.ProposedBaseline()
	return b.Local[path].Present
}

func TestBoundsAndExcludedScope(t *testing.T) {
	r := baseRequest(entries("safe", file("x")), entries("outside", file("y")))
	r.Direction = Push
	r.RemotePolicy = Full
	r.Binding.Exclusions = []string{"outside"}
	delete(r.Remote.Entries, "outside")
	r.Local.NameKeys["outside"] = "outside"
	p, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	requireActions(t, p, act(Remote, Create, "safe", nil, ptr(file("x")), Local, "safe"))
	r.Local.Entries["../escape"] = file("bad")
	if _, err := Reconcile(r); err == nil {
		t.Fatal("traversal accepted")
	}
	delete(r.Local.Entries, "../escape")
	r.Local.Entries[strings.Repeat("a", MaxComponentBytes+1)] = file("bad")
	if _, err := Reconcile(r); err == nil {
		t.Fatal("long component accepted")
	}
}

func TestOneSidedTypeChangeIsConflict(t *testing.T) {
	a := file("a")
	r := baseRequest(entries("x", a), entries("x", a))
	first, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	r = nextReq(t, r, first, entries("x", dir()), entries("x", a))
	p, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Conflicts(); len(got) != 1 || got[0].Reason != "type collision" {
		t.Fatalf("type collision: %#v", got)
	}
}

func TestDirectoryMetadataFollowsChildren(t *testing.T) {
	oldDir, newDir := dir(), dir()
	newDir.MetadataHash = hash("mode:0700")
	a, b := file("a"), file("b")
	r := baseRequest(entries("d", oldDir, "d/f", a), entries("d", oldDir, "d/f", a))
	first, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	r = nextReq(t, r, first, entries("d", newDir, "d/f", b), entries("d", oldDir, "d/f", a))
	p, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	requireActions(t, p, act(Remote, Overwrite, "d/f", ptr(a), ptr(b), Local, "d/f"), act(Remote, Metadata, "d", ptr(oldDir), ptr(newDir), Local, "d"))
}

func TestUnchangedDirectoryMetadataRestoredAfterChildPublication(t *testing.T) {
	d, a, b := dir(), file("a"), file("b")
	r := baseRequest(entries("d", d, "d/f", a), entries("d", d, "d/f", a))
	first, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	r = nextReq(t, r, first, entries("d", d, "d/f", b), entries("d", d, "d/f", a))
	p, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	requireActions(t, p, act(Remote, Overwrite, "d/f", ptr(a), ptr(b), Local, "d/f"), act(Remote, Metadata, "d", ptr(d), ptr(d), Remote, "d"))
}

func TestResolutionRejectsStaleFingerprint(t *testing.T) {
	a, b := file("a"), file("b")
	r := baseRequest(entries("x", a), entries("x", b))
	r.Resolutions = []Resolution{{Path: "x", Choice: KeepLocal, ExpectedLocal: ptr(b), ExpectedRemote: ptr(b)}}
	if _, err := Reconcile(r); err == nil || !strings.Contains(err.Error(), "stale resolution") {
		t.Fatalf("stale resolution: %v", err)
	}
}

func TestRepeatedRoundProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	versions := []Entry{file("zero"), file("one"), file("two"), file("three")}
	r := baseRequest(entries("f", versions[0]), entries("f", versions[0]))
	first, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	local, remote := entries("f", versions[0]), entries("f", versions[0])
	r = nextReq(t, r, first, local, remote)
	for round := 0; round < 300; round++ {
		mutate := func(m map[string]Entry) {
			switch rng.Intn(5) {
			case 0:
				delete(m, "f")
			case 1, 2, 3:
				m["f"] = versions[rng.Intn(len(versions))]
			}
		}
		mutate(local)
		mutate(remote)
		if rng.Intn(2) == 0 {
			r.LocalPolicy = Full
		} else {
			r.LocalPolicy = Additive
		}
		if rng.Intn(2) == 0 {
			r.RemotePolicy = Full
		} else {
			r.RemotePolicy = Additive
		}
		r.Local.Entries = local
		r.Remote.Entries = remote
		p, err := Reconcile(r)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if cs := p.Conflicts(); len(cs) > 0 {
			if len(p.Actions()) != 0 {
				t.Fatalf("round %d: conflict with actions", round)
			}
			if _, ok := p.ProposedBaseline(); ok {
				t.Fatalf("round %d: conflict with proposal", round)
			}
			r.Resolutions = []Resolution{{Path: "f", Choice: KeepLocal, ExpectedLocal: cs[0].Local, ExpectedRemote: cs[0].Remote}}
			p, err = Reconcile(r)
			if err != nil {
				t.Fatalf("round %d resolution: %v", round, err)
			}
			r.Resolutions = nil
		}
		for _, a := range p.Actions() {
			m := local
			if a.Side == Remote {
				m = remote
			}
			old, ok := m[a.Path]
			if !reflect.DeepEqual(entryPtr(old, ok), a.Before) {
				t.Fatalf("round %d: action precondition mismatch: %#v", round, a)
			}
			if a.Kind == Remove {
				delete(m, a.Path)
			} else {
				m[a.Path] = *a.After
			}
		}
		b := confirmed(t, p)
		for _, item := range []struct {
			states  map[string]State
			entries map[string]Entry
		}{{b.Local, local}, {b.Remote, remote}} {
			e, ok := item.entries["f"]
			s := item.states["f"]
			if ok != s.Present || (ok && s.Entry != e) {
				t.Fatalf("round %d: proposed baseline differs from actions: %#v %#v", round, item.states, item.entries)
			}
		}
		r = nextReq(t, r, p, local, remote)
		stable, err := Reconcile(r)
		if err != nil {
			t.Fatalf("round %d repeat: %v", round, err)
		}
		if len(stable.Actions()) != 0 || len(stable.Conflicts()) != 0 {
			t.Fatalf("round %d did not converge: %#v %#v", round, stable.Actions(), stable.Conflicts())
		}
	}
}
