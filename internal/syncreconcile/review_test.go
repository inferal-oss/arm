package syncreconcile

import (
	"strings"
	"testing"
)

func TestReviewRetainedNestedEditRestoresOnlyRequiredParents(t *testing.T) {
	d, a, b := dir(), file("base"), file("edited retained copy")
	r := baseRequest(entries("d", d, "d/f", a, "d/unchanged", a), entries("d", d, "d/f", a, "d/unchanged", a))
	first, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	r = nextReq(t, r, first, map[string]Entry{}, r.Remote.Entries)
	retained, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	requireActions(t, retained)
	r = nextReq(t, r, retained, map[string]Entry{}, entries("d", d, "d/f", b, "d/unchanged", a))
	p, err := Reconcile(r)
	if err != nil {
		t.Fatal("actual edit of retained nested copy was not handled", err)
	}
	requireNoConflicts(t, p)
	requireActions(t, p, act(Local, Create, "d", nil, ptr(d), Remote, "d"), act(Local, Create, "d/f", nil, ptr(b), Remote, "d/f"))
	baseline := confirmed(t, p)
	if !baseline.Local["d/unchanged"].Tombstone || baseline.Local["d/unchanged"].Present {
		t.Fatal("structural parent repair resurrected unchanged retained sibling", baseline)
	}
}

func TestReviewDirectoryDeletionAgainstNewDescendantIsConflict(t *testing.T) {
	d, a := dir(), file("new peer child")
	r := baseRequest(entries("d", d), entries("d", d))
	first, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	r = nextReq(t, r, first, map[string]Entry{}, entries("d", d, "d/new", a))
	r.RemotePolicy = Full
	p, err := Reconcile(r)
	if err != nil {
		t.Fatal("delete/edit should be a reviewable conflict", err)
	}
	if len(p.Conflicts()) == 0 || p.Conflicts()[0].Path != "d" || len(p.Actions()) != 0 {
		t.Fatal("directory delete discarded or silently resurrected peer child", p.Conflicts(), p.Actions())
	}
	if _, ok := p.ProposedBaseline(); ok {
		t.Fatal("unresolved directory edit/delete advanced baseline")
	}
}

func TestReviewExcludedSubtreeCannotBeMaskedByAncestorAction(t *testing.T) {
	for _, tc := range []struct {
		name     string
		source   map[string]Entry
		destPath string
		fold     bool
	}{
		{"replace-ancestor", entries("d", file("replacement")), "d", false},
		{"remove-ancestor", map[string]Entry{}, "d", false},
		{"casefold-ancestor", entries("D", file("replacement")), "D", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := baseRequest(tc.source, entries(tc.destPath, dir()))
			r.Direction = Push
			r.RemotePolicy = Full
			r.Binding.Exclusions = []string{"d/protected"}
			for _, s := range []Snapshot{r.Local, r.Remote} {
				s.NameKeys["d"] = "d"
				s.NameKeys["d/protected"] = "d/protected"
				if tc.fold {
					s.NameKeys["D"] = "d"
				}
			}
			if p, err := Reconcile(r); err == nil {
				t.Fatal("selected-out subtree could be removed or hidden", p.Actions())
			} else if !strings.Contains(err.Error(), "exclu") {
				t.Fatal("unexpected refusal", err)
			}
		})
	}
}

func TestReviewFullDirectoryDeletionRemovesUnchangedRetainedChild(t *testing.T) {
	d, a := dir(), file("receiver-only")
	// A prior additive pull leaves a selected child only at the receiver. Its
	// sender baseline is confirmed-empty, rather than a child tombstone.
	r := baseRequest(entries("d", d, "d/f", a), entries("d", d))
	r.Direction = Pull
	first, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	if b := confirmed(t, first); b.Remote["d/f"].Tombstone || b.Remote["d/f"].Present {
		t.Fatal("fixture is not an asymmetric confirmed-empty baseline", b)
	}
	r = nextReq(t, r, first, r.Local.Entries, map[string]Entry{})
	r.Direction = Both
	r.LocalPolicy = Full
	p, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	requireNoConflicts(t, p)
	requireActions(t, p, act(Local, Remove, "d/f", ptr(a), nil, "", ""), act(Local, Remove, "d", ptr(d), nil, "", ""))
}

func TestReviewKeepBothRequiresUnusedNativeNames(t *testing.T) {
	a, b := file("left"), file("right")
	r := baseRequest(entries("x", a), entries("x", b))
	r.Resolutions = []Resolution{{Path: "x", Choice: KeepBoth, ExpectedLocal: ptr(a), ExpectedRemote: ptr(b), LocalName: "X", RemoteName: "x.remote"}}
	for _, s := range []Snapshot{r.Local, r.Remote} {
		s.NameKeys["X"] = "x"
		s.NameKeys["x.remote"] = "x.remote"
	}
	if p, err := Reconcile(r); err == nil {
		t.Fatal("existing native conflict slot was accepted as an unused keep-both name", p.Actions())
	}
}

func TestReviewExcludedCanonicalReceiveSlotAndResolutionNames(t *testing.T) {
	a, b := file("left"), file("right")
	r := baseRequest(entries("D", dir(), "D/secret", a), entries("d", dir()))
	r.Direction = Push
	r.RemotePolicy = Full
	r.Binding.Exclusions = []string{"d/secret"}
	r.Local.NameKeys = names("D", "D/secret", "d", "d/secret")
	r.Remote.NameKeys = map[string]string{"D": "d", "D/secret": "d/secret", "d": "d", "d/secret": "d/secret"}
	if p, err := Reconcile(r); err == nil || !strings.Contains(err.Error(), "exclu") {
		t.Fatal("incoming case alias could write an excluded receive slot", p.Actions(), err)
	}
	r = baseRequest(entries("x", a), entries("x", b))
	r.Binding.Exclusions = []string{"saved"}
	r.Resolutions = []Resolution{{Path: "x", Choice: KeepBoth, ExpectedLocal: ptr(a), ExpectedRemote: ptr(b), LocalName: "Saved", RemoteName: "x.remote"}}
	for _, s := range []Snapshot{r.Local, r.Remote} {
		s.NameKeys["saved"] = "saved"
		s.NameKeys["Saved"] = "saved"
		s.NameKeys["x.remote"] = "x.remote"
	}
	if p, err := Reconcile(r); err == nil || !strings.Contains(err.Error(), "exclu") {
		t.Fatal("resolution name could alias an excluded path", p.Actions(), err)
	}
}

func TestReviewRetainedNestedEditsSurviveFullPolicyTransitionBothSides(t *testing.T) {
	for _, deleted := range []Side{Local, Remote} {
		t.Run(string(deleted), func(t *testing.T) {
			d, a, b := dir(), file("base"), file("actual retained edit")
			both := entries("d", d, "d/f", a, "d/unchanged", a)
			r := baseRequest(both, both)
			first, err := Reconcile(r)
			if err != nil {
				t.Fatal(err)
			}
			left, right := both, map[string]Entry{}
			if deleted == Local {
				left, right = right, left
			}
			r = nextReq(t, r, first, left, right)
			retained, err := Reconcile(r)
			if err != nil {
				t.Fatal(err)
			}
			requireActions(t, retained)
			edited := entries("d", d, "d/f", b, "d/unchanged", a)
			left, right = edited, map[string]Entry{}
			source, target := Local, Remote
			if deleted == Local {
				left, right = right, left
				source, target = Remote, Local
				r.RemotePolicy = Full
			} else {
				r.LocalPolicy = Full
			}
			r = nextReq(t, r, retained, left, right)
			p, err := Reconcile(r)
			if err != nil {
				t.Fatal(err)
			}
			requireNoConflicts(t, p)
			requireActions(t, p, act(source, Remove, "d/unchanged", ptr(a), nil, "", ""), act(target, Create, "d", nil, ptr(d), source, "d"), act(target, Create, "d/f", nil, ptr(b), source, "d/f"), act(source, Metadata, "d", ptr(d), ptr(d), source, "d"))
		})
	}
}

func TestReviewDirectoryResolutionBindsAllObservedRows(t *testing.T) {
	d, a, b := dir(), file("first edited child"), file("later edited child")
	r := baseRequest(entries("d", d), entries("d", d))
	first, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	r = nextReq(t, r, first, map[string]Entry{}, entries("d", d, "d/new", a))
	p, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Conflicts()) == 0 {
		t.Fatal("missing directory conflict")
	}
	r.Resolutions = []Resolution{{Path: "d", Choice: KeepRemote, ExpectedLocal: nil, ExpectedRemote: ptr(d)}}
	resolved, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	requireNoConflicts(t, resolved)
	requireActions(t, resolved, act(Local, Create, "d", nil, ptr(d), Remote, "d"), act(Local, Create, "d/new", nil, ptr(a), Remote, "d/new"))
	r.Remote.Entries["d/new"] = b
	changed, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Digest() == resolved.Digest() {
		t.Fatal("unchanged directory metadata laundered a changed child into the same approved plan")
	}
}

func TestReviewExclusionNativeKeysArePartOfReviewedDigest(t *testing.T) {
	a := file("selected")
	r := baseRequest(entries("f", a), entries("f", a))
	r.Binding.Exclusions = []string{"protected/child"}
	r.Local.NameKeys = names("f", "protected", "protected/child")
	r.Remote.NameKeys = names("f", "protected", "protected/child")
	before, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Remote.NameKeys["protected"] = "other"
	r.Remote.NameKeys["protected/child"] = "other/child"
	after, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	if after.Digest() == before.Digest() {
		t.Fatal("changed native exclusion scope reused the same approved plan digest")
	}
}

func TestReviewBindingCannotCollideThroughInvalidUTF8(t *testing.T) {
	for _, field := range []string{"local-id", "local-root", "remote-id", "remote-root", "selection", "metadata"} {
		t.Run(field, func(t *testing.T) {
			r := baseRequest(entries("f", file("value")), map[string]Entry{})
			switch field {
			case "local-id":
				r.Binding.Local.ID = "\xff"
			case "local-root":
				r.Binding.Local.RootID = "\xff"
			case "remote-id":
				r.Binding.Remote.ID = "\xff"
			case "remote-root":
				r.Binding.Remote.RootID = "\xff"
			case "selection":
				r.Binding.SelectionID = "\xff"
			case "metadata":
				r.Binding.MetadataID = "\xff"
			}
			if p, err := Reconcile(r); err == nil {
				t.Fatal("non-UTF8 binding accepted despite JSON replacement collapsing distinct identities", p.Digest())
			}
		})
	}
}

func TestReviewBaselineNativeScopeChangeCannotBecomeDeletion(t *testing.T) {
	lower, upper := file("lower"), file("upper")
	r := baseRequest(entries("a", lower, "A", upper), entries("a", lower, "A", upper))
	first, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	r = nextReq(t, r, first, entries("a", lower), entries("a", lower, "A", upper))
	r.RemotePolicy = Full
	r.Local.NameKeys = map[string]string{"a": "a", "A": "a"}
	r.Remote.NameKeys = names("a", "A")
	if p, err := Reconcile(r); err == nil {
		t.Fatal("changed native comparison scope inferred deletion of a previously distinct baseline entry", p.Actions())
	}
}

func TestReviewNewNativeExclusionCannotDeleteUnchangedPeer(t *testing.T) {
	d, a := dir(), file("protected by new native comparison")
	r := baseRequest(entries("D", d, "D/secret", a), entries("D", d, "D/secret", a))
	r.Binding.Exclusions = []string{"d/secret"}
	r.Local.NameKeys = names("D", "D/secret", "d", "d/secret")
	r.Remote.NameKeys = names("D", "D/secret", "d", "d/secret")
	first, err := Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	r = nextReq(t, r, first, map[string]Entry{}, r.Remote.Entries)
	r.RemotePolicy = Full
	r.Local.NameKeys = map[string]string{"D": "d", "D/secret": "d/secret", "d": "d", "d/secret": "d/secret"}
	if p, err := Reconcile(r); err == nil {
		t.Fatal("newly excluded baseline paths were treated as confirmed deletions", p.Actions())
	}
}

func TestReviewBindingFieldsHaveByteBounds(t *testing.T) {
	r := baseRequest(map[string]Entry{}, map[string]Entry{})
	r.Binding.Local.RootID = strings.Repeat("r", MaxBindingBytes+1)
	if _, err := Reconcile(r); err == nil {
		t.Fatal("unbounded root binding accepted")
	}
}
