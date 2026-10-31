package residentrefs

import (
	"bytes"
	"context"
	"encoding/binary"
	"sync"
	"testing"
)

// PURE pending/data cases. Never fcntl/stat/wrap/close fd3..15 in the test
// process. A fixed-entry census without actual role/native checks is not proof.
// Inert fixtures can exercise pure delegation but fail the process-original
// gate before every native operation. No production factory/reset is exposed.
func inertChildData() *ChildPending {
	c := &ChildPending{}
	s := &childState{self: c}
	c.state = s
	for i := range s.entries {
		e := &originalEntry{census: s, number: i + 3}
		e.self = e
		s.entries[i] = e
	}
	return c
}
func TestFixedChildPendingRegistersAllBeforeEffectsAndRejectsCopies(t *testing.T) {
	var wg sync.WaitGroup
	winners := make(chan *ChildPending, 32)
	failures := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := NewFixedChildPending()
			if err == nil {
				winners <- c
			} else {
				if c != nil {
					failures <- ErrPending
				} else {
					failures <- err
				}
			}
		}()
	}
	wg.Wait()
	close(winners)
	close(failures)
	if len(winners) != 1 || len(failures) != 31 {
		t.Fatal("process claim not unique")
	}
	original := <-winners
	for err := range failures {
		if err != ErrOriginal {
			t.Fatal(err)
		}
	}
	if c, err := NewFixedChildPending(); c != nil || err != ErrOriginal {
		t.Fatal("claim replay", err)
	}
	if err := ClaimLegacyReceiverExclusion(); err != ErrOriginal {
		t.Fatal("legacy overlap accepted", err)
	}
	copied := *original
	if s, f, i, c, err := copied.NewOriginalRecipients(); err != ErrOriginal || s != nil || f != nil || i != nil || c != nil {
		t.Fatal("copied census adopted")
	}
	state := original.InspectOriginal()
	if state.Registered != 13 || state.MaterializedEntries != 0 || state.Delegated || state.RecipientsRegistered || state.NativeOutcome != "unknown" {
		t.Fatal(state)
	}
	service, fence, image, control, err := original.NewOriginalRecipients()
	if err != nil {
		t.Fatal(err)
	}
	if err = original.DelegateOriginalRecipients(service, fence, image, control); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = original.SealOriginalDescriptors(ctx); err != context.Canceled {
		t.Fatal(err)
	}
	state = original.InspectOriginal()
	if state.Sealed || !state.SealAttempted || !state.RootCloseAuthorityWithdrawn || state.MaterializedEntries != 0 || state.FirstError != context.Canceled {
		t.Fatal(state)
	}
	for _, entry := range original.state.entries {
		if entry.file != nil || entry.cloexecTried || entry.materializeTried || entry.closeTried {
			t.Fatal("native original touched before canceled seal")
		}
	}
}
func TestFixedChildPureOneWayDelegationRejectsForeignCopiedAndReplay(t *testing.T) {
	original := inertChildData()
	service, fence, image, control, err := original.NewOriginalRecipients()
	if err != nil {
		t.Fatal(err)
	}
	foreign := inertChildData()
	foreignService, _, _, _, err := foreign.NewOriginalRecipients()
	if err != nil {
		t.Fatal(err)
	}
	copied := *service
	for _, bad := range []*ServiceOriginal{foreignService, &copied} {
		if err = original.DelegateOriginalRecipients(bad, fence, image, control); err != ErrOriginal {
			t.Fatal("foreign/copied move accepted", err)
		}
	}
	if original.InspectOriginal().RootCloseAuthorityWithdrawn {
		t.Fatal("refusal withdrew original donor")
	}
	if err = original.DelegateOriginalRecipients(service, fence, image, control); err != nil {
		t.Fatal(err)
	}
	state := original.InspectOriginal()
	if !state.Delegated || state.DelegatedEntries != 13 || !state.RootCloseAuthorityWithdrawn || state.MaterializedEntries != 0 {
		t.Fatal(state)
	}
	if err = original.DelegateOriginalRecipients(service, fence, image, control); err != ErrOriginal {
		t.Fatal("move replay accepted", err)
	}
	if err = original.SealOriginalDescriptors(context.Background()); err != ErrOriginal {
		t.Fatal("inert fixture acquired native authority", err)
	}
	for _, entry := range original.state.entries {
		if entry.cloexecTried || entry.file != nil {
			t.Fatal("inert fixture touched native descriptor")
		}
	}
}
func TestFixedChildNonclosingViewsRefuseBeforeMaterialization(t *testing.T) {
	original := inertChildData()
	service, _, image, _, err := original.NewOriginalRecipients()
	if err != nil {
		t.Fatal(err)
	}
	journal, err := service.ClaimJournalOriginalView()
	if err != nil {
		t.Fatal(err)
	}
	view, err := image.ClaimImageOriginalView()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = journal.StatOriginal(context.Background()); err != ErrOriginal {
		t.Fatal(err)
	}
	if _, err = view.ReadAtOriginal(context.Background(), make([]byte, 1), 0); err != ErrOriginal {
		t.Fatal(err)
	}
	copied := *journal
	if _, err = copied.WriteAtOriginal(context.Background(), []byte("synthetic"), 0); err != ErrOriginal {
		t.Fatal(err)
	}
	if original.InspectOriginal().MaterializedEntries != 0 {
		t.Fatal("view materialized a borrowed FD")
	}
}
func TestOriginalFrameDataBoundsIncludeCompleteHeader(t *testing.T) {
	payload := bytes.Repeat([]byte{'a'}, MaxWireBytes-wireHeaderBytes)
	frame, err := EncodeFrameData(payload)
	if err != nil || len(frame) != MaxWireBytes {
		t.Fatal(len(frame), err)
	}
	if int(binary.BigEndian.Uint32(frame[:4])) != len(payload) || !bytes.Equal(frame[wireHeaderBytes:], payload) {
		t.Fatal("frame data changed")
	}
	for _, value := range [][]byte{nil, append(payload, 'b'), bytes.Repeat([]byte{'a'}, MaxWireBytes)} {
		if _, err = EncodeFrameData(value); err != ErrOriginal {
			t.Fatal("oversized or empty data accepted")
		}
	}
}
