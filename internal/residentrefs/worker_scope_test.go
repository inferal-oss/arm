package residentrefs

import "testing"

// These holders deliberately have no process claim. Neither a scope tag nor
// the right fixed descriptor numbers may turn caller metadata into custody.
func TestCoreScopeRejectsUnissuedRecipientsBeforeView(t *testing.T) {
	for _, scope := range []childCoreScope{legacyChildCore, workerChildCore, 255} {
		c := &ChildPending{state: &childState{scope: scope}}
		c.state.self = c
		for i := range c.state.entries {
			e := &originalEntry{census: c.state, number: i + 3}
			e.self = e
			c.state.entries[i] = e
		}
		s, f, image, control, err := c.NewOriginalRecipients()
		if err != nil {
			t.Fatal(err)
		}
		for _, check := range []func() error{s.RequireOriginalLegacyCore, s.RequireOriginalWorkerCore, f.RequireOriginalLegacyCore, f.RequireOriginalWorkerCore, image.RequireOriginalLegacyCore, image.RequireOriginalWorkerCore, control.RequireOriginalLegacyCore, control.RequireOriginalWorkerCore} {
			if check() != ErrOriginal {
				t.Fatal("scope metadata accepted without original process claim")
			}
		}
		if image.RequireOriginalWorkerServiceLink(s) != ErrOriginal {
			t.Fatal("unissued recipients acquired a service/image link")
		}
		for _, r := range c.state.recipients {
			if r.journal != nil || r.imageView != nil || r.materializeTried {
				t.Fatal("scope refusal claimed a view or materialized entries")
			}
		}
	}
}

func TestCoreScopeRejectsNilZeroAndCopies(t *testing.T) {
	var nilService *ServiceOriginal
	var nilFence *FenceOriginal
	var nilImage *ImageOriginal
	var nilControl *ControlOriginal
	s, f, image, control := &ServiceOriginal{}, &FenceOriginal{}, &ImageOriginal{}, &ControlOriginal{}
	s.self, f.self, image.self, control.self = s, f, image, control
	copyS, copyF, copyImage, copyControl := *s, *f, *image, *control
	for _, check := range []func() error{
		nilService.RequireOriginalWorkerCore, nilFence.RequireOriginalWorkerCore, nilImage.RequireOriginalWorkerCore, nilControl.RequireOriginalWorkerCore,
		s.RequireOriginalWorkerCore, f.RequireOriginalWorkerCore, image.RequireOriginalWorkerCore, control.RequireOriginalWorkerCore,
		copyS.RequireOriginalWorkerCore, copyF.RequireOriginalWorkerCore, copyImage.RequireOriginalWorkerCore, copyControl.RequireOriginalWorkerCore,
		copyS.RequireOriginalLegacyCore, copyF.RequireOriginalLegacyCore, copyImage.RequireOriginalLegacyCore, copyControl.RequireOriginalLegacyCore,
	} {
		if check() != ErrOriginal {
			t.Fatal("nil, zero or copied recipient accepted")
		}
	}
}

func TestCoreScopeMetadataPreservesOwnershipDuringOriginalPending(t *testing.T) {
	c := inertChildData() // pure synthetic metadata, never a process claim
	c.state.scope = workerChildCore
	service, _, image, _, err := c.NewOriginalRecipients()
	if err != nil {
		t.Fatal(err)
	}
	s := c.state
	for _, active := range []*originalOperation{nil, {census: s, recipient: service.state, kind: "read_at"}, {census: s, recipient: image.state, kind: "stat"}} {
		s.active = active
		if err := coreScopeMetadataLocked(service.state, service, workerChildCore); err != nil {
			t.Fatal("pending native operation changed immutable lineage", err)
		}
		if err := service.RequireOriginalWorkerCore(); err != ErrOriginal {
			t.Fatal("pure metadata helper minted a positive public original", err)
		}
	}
	if coreScopeMetadataLocked(service.state, service, legacyChildCore) != ErrOriginal {
		t.Fatal("scope mismatch lost its guard")
	}
	service.state.entries[0].number++
	if coreScopeMetadataLocked(service.state, service, workerChildCore) != ErrOriginal {
		t.Fatal("fixed slot identity lost its guard")
	}
	service.state.entries[0].number--
	s.firstError = ErrPending
	if coreScopeMetadataLocked(service.state, service, workerChildCore) != ErrOriginal {
		t.Fatal("sticky original failure lost its guard")
	}
	s.firstError = nil
	service.state.firstError = ErrOriginal
	if coreScopeMetadataLocked(service.state, service, workerChildCore) != ErrOriginal {
		t.Fatal("sticky recipient failure lost its guard")
	}
	if s.active == nil || s.active.kind != "stat" || service.state.journal != nil || image.state.imageView != nil {
		t.Fatal("scope metadata checking consumed or replaced native state")
	}
}
