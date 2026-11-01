package residentservice

import (
	"context"
	"testing"

	"getarm.org/internal/residentrefs"
)

func TestWorkerServeBirthFenceLinkRejectsUnissuedViewsAndRecipients(t *testing.T) {
	for _, view := range []*WorkerServeBirthView{nil, {}} {
		if _, err := view.ValidateOriginalFence(context.Background(), &residentrefs.FenceOriginal{}); err == nil {
			t.Fatal("unissued view linked a fence")
		}
	}
	p, charge := workerChargeForgeryData(t)
	b := &WorkerBirthBinding{child: p, charge: charge}
	b.self, p.birth = b, b
	v := &WorkerServeBirthView{birth: b}
	v.self, b.serve, b.serveTried = v, v, true
	copyView := *v
	for _, view := range []*WorkerServeBirthView{v, &copyView} {
		for _, fence := range []*residentrefs.FenceOriginal{nil, {}} {
			if _, err := view.ValidateOriginalFence(context.Background(), fence); err == nil {
				t.Fatal("copied/unissued birth or fence acquired a link")
			}
		}
	}
	assertWorkerNoNativeData(t, p.custody)
}
