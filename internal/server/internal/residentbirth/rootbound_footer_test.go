package residentbirth

import (
	"context"
	"strings"
	"testing"

	"getarm.org/internal/server/internal/residentservice"
)

func TestP1FooterRequiresActualVerifiedFenceAndCompletedService(t *testing.T) {
	for _, p := range []*InheritedFencePending{nil, {}, {state: &inheritedFenceState{}}} {
		if err := p.AppendP1RootBoundFooterOriginal(context.Background(), &residentservice.ChildPending{}, strings.Repeat("a", 64)); err != errOriginalFence {
			t.Fatal(err)
		}
		if _, err := p.P1RootBoundFooterOriginalData(); err != errOriginalFence {
			t.Fatal(err)
		}
	}
	p := &InheritedFencePending{}
	s := &inheritedFenceState{self: p, rootContext: context.Background()}
	p.state = s
	if err := p.AppendP1RootBoundFooterOriginal(context.Background(), &residentservice.ChildPending{}, strings.Repeat("a", 64)); err != errOriginalFence {
		t.Fatal(err)
	}
	copyFence := *p
	if err := copyFence.AppendP1RootBoundFooterOriginal(context.Background(), &residentservice.ChildPending{}, strings.Repeat("a", 64)); err != errOriginalFence {
		t.Fatal(err)
	}
	if s.footerTried || s.footer != nil || s.active != nil || s.verifyTried || s.bytesRead != 0 {
		t.Fatal("refusal spent an original read/write/native phase")
	}
}
