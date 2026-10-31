package residentrefs

import "testing"

func TestOriginalServiceFenceLinkRejectsInertCopiedAndForeignCensus(t *testing.T) {
	child := inertChildData() // never a genuine process claim or native fixture
	service, fence, _, _, err := child.NewOriginalRecipients()
	if err != nil {
		t.Fatal(err)
	}
	foreign := inertChildData()
	_, otherFence, _, _, err := foreign.NewOriginalRecipients()
	if err != nil {
		t.Fatal(err)
	}
	copyService, copyFence := *service, *fence
	for _, s := range []*ServiceOriginal{nil, {}, service, &copyService} {
		for _, f := range []*FenceOriginal{nil, {}, fence, &copyFence, otherFence} {
			if err := s.ValidateOriginalFenceLink(f); err != ErrOriginal {
				t.Fatal("inert/copied/foreign relationship accepted", err)
			}
		}
	}
	for _, c := range []*ChildPending{child, foreign} {
		if c.state.active != nil || c.state.firstError != nil || c.state.sealed || c.state.delegated {
			t.Fatal("pure link changed original state")
		}
		for _, e := range c.state.entries {
			if e.file != nil || e.cloexecTried || e.materializeTried || e.closeTried {
				t.Fatal("pure link touched native entry")
			}
		}
	}
}
