package syncfs

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// This fake exercises the exclusion lifecycle only. It does not represent a
// durable Manager owner or native quota and cannot be a positive XFS fixture.
type testScopeFence struct {
	mu                        sync.Mutex
	claim                     ScopeClaim
	project, selected, parent *Root
	live, held                bool
	acquired, released        int
	failCheck                 int
	checks                    int
	returnErrorWithHold       bool
}

func (*testScopeFence) scopeOwnerFence() {}

func (f *testScopeFence) TryHoldOriginal(ctx context.Context, claim ScopeClaim, project, selected, parent *Root) (scopeOwnerHold, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrQuotaUnknown
	}
	if !f.mu.TryLock() {
		return nil, ErrQuotaUnknown
	}
	if !f.live || claim != f.claim || project != f.project || selected != f.selected || parent != f.parent {
		f.mu.Unlock()
		return nil, ErrQuotaUnknown
	}
	f.held = true
	f.acquired++
	hold := &testScopeHold{fence: f}
	if f.returnErrorWithHold {
		return hold, ErrQuotaUnknown
	}
	return hold, nil
}

type testScopeHold struct{ fence *testScopeFence }

func (h *testScopeHold) CheckOriginal(ctx context.Context, claim ScopeClaim, project, selected, parent *Root) error {
	f := h.fence
	f.checks++
	if ctx == nil || ctx.Err() != nil || !f.held || !f.live || f.failCheck == f.checks ||
		claim != f.claim || project != f.project || selected != f.selected || parent != f.parent {
		return ErrQuotaUnknown
	}
	return nil
}

func (h *testScopeHold) Release() {
	f := h.fence
	f.held = false
	f.released++
	f.mu.Unlock()
}

func testOwnerFixture(t *testing.T) (ScopeClaim, *Root, *Root, *Root, *testScopeFence) {
	t.Helper()
	claim, err := NewScopeClaim(completeScopeClaimFields())
	if err != nil {
		t.Fatal(err)
	}
	// The injected verifier never opens these placeholders.
	project, selected, parent := &Root{}, &Root{}, &Root{}
	fence := &testScopeFence{claim: claim, project: project, selected: selected, parent: parent, live: true}
	return claim, project, selected, parent, fence
}

func testScopeBinding(claim ScopeClaim) ScopeBinding {
	f := claim.fields
	return ScopeBinding{
		provider: f.Provider, scopeID: f.ScopeID, mountID: f.MountID,
		projectRootID: f.ProjectRootID, selectedRootID: f.SelectedRootID, parentRootID: f.ParentRootID,
		ownershipDigest: f.OwnershipDigest, projectID: f.ProjectID, ownerEpoch: f.OwnerEpoch,
		hardBytes: 8192, usedBytes: 0,
	}
}

func TestOriginalScopeConstructorRequiresLiveSealedHold(t *testing.T) {
	claim, project, selected, parent, fence := testOwnerFixture(t)
	invalid := claim
	invalid.fields.Provider = "caller-supplied"
	if owner, err := newExclusiveScopeOwner(t.Context(), invalid, project, selected, parent, fence); !errors.Is(err, ErrQuotaUnknown) || owner != nil {
		t.Fatalf("unvalidated claim produced owner: %v %v", owner, err)
	}
	if owner, err := newExclusiveScopeOwner(t.Context(), claim, project, selected, parent, nil); !errors.Is(err, ErrQuotaUnknown) || owner != nil {
		t.Fatalf("missing fence produced owner: %v %v", owner, err)
	}
	fence.live = false
	if owner, err := newExclusiveScopeOwner(t.Context(), claim, project, selected, parent, fence); !errors.Is(err, ErrQuotaUnknown) || owner != nil {
		t.Fatalf("closed fence produced owner: %v %v", owner, err)
	}
	fence.live = true
	fence.returnErrorWithHold = true
	if owner, err := newExclusiveScopeOwner(t.Context(), claim, project, selected, parent, fence); !errors.Is(err, ErrQuotaUnknown) || owner != nil || fence.held || fence.acquired != fence.released {
		t.Fatalf("failed acquisition leaked owner or hold: %v %v acquired=%d released=%d", owner, err, fence.acquired, fence.released)
	}
	fence.returnErrorWithHold = false
	owner, err := newExclusiveScopeOwner(t.Context(), claim, project, selected, parent, fence)
	if err != nil || owner == nil || fence.held || fence.acquired != fence.released {
		t.Fatalf("constructor leaked hold: owner=%v err=%v acquired=%d released=%d", owner, err, fence.acquired, fence.released)
	}
	if hold, err := owner.holdFor(t.Context(), claim, parent, selected, project); !errors.Is(err, ErrQuotaUnknown) || hold != nil {
		t.Fatalf("swapped held roots acquired owner: %v %v", hold, err)
	}
	proof := CompleteScopeProof{claim: claim, binding: testScopeBinding(claim), owner: owner, project: project, selected: selected, parent: parent}
	fence.live = false
	if binding, err := proof.BindingFor(t.Context(), claim); !errors.Is(err, ErrQuotaUnknown) || binding != (ScopeBinding{}) {
		t.Fatalf("lost owner yielded cached binding: %v %v", binding, err)
	}
}

func TestCompleteScopeHoldCoversVerificationAndReleasesOnRefusal(t *testing.T) {
	claim, project, selected, parent, fence := testOwnerFixture(t)
	owner, err := newExclusiveScopeOwner(t.Context(), claim, project, selected, parent, fence)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := verifyCompleteScopeWith(context.Background(), claim, owner, project, selected, parent,
		func(context.Context, ScopeClaim, *Root, *Root, *Root) (ScopeBinding, error) {
			if !fence.held {
				t.Error("native verifier ran outside original exclusion")
			}
			if hold, err := owner.holdFor(t.Context(), claim, project, selected, parent); !errors.Is(err, ErrQuotaUnknown) || hold != nil {
				t.Error("overlapping owner hold was accepted")
			}
			return ScopeBinding{}, ErrQuotaUnsupported
		})
	if !errors.Is(err, ErrQuotaUnsupported) || proof != (CompleteScopeProof{}) || fence.held || fence.acquired != fence.released {
		t.Fatalf("refusal leaked proof or hold: proof=%v err=%v acquired=%d released=%d", proof, err, fence.acquired, fence.released)
	}
}

func TestCompleteScopeCancellationAndOwnerLossReleaseHold(t *testing.T) {
	for _, mode := range []string{"cancelled", "owner lost", "binding mismatch"} {
		t.Run(mode, func(t *testing.T) {
			claim, project, selected, parent, fence := testOwnerFixture(t)
			owner, err := newExclusiveScopeOwner(t.Context(), claim, project, selected, parent, fence)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "owner lost" {
				fence.failCheck = 3 // constructor, entry, then final check
			}
			proof, err := verifyCompleteScopeWith(ctx, claim, owner, project, selected, parent,
				func(context.Context, ScopeClaim, *Root, *Root, *Root) (ScopeBinding, error) {
					if !fence.held {
						t.Error("verifier ran without exclusion")
					}
					if mode == "cancelled" {
						cancel()
					}
					if mode == "binding mismatch" {
						return ScopeBinding{provider: "test-only"}, nil
					}
					return testScopeBinding(claim), nil
				})
			want := ErrQuotaUnknown
			if mode == "cancelled" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || proof != (CompleteScopeProof{}) || fence.held || fence.acquired != fence.released {
				t.Fatalf("lost authority minted proof or leaked hold: proof=%v err=%v acquired=%d released=%d", proof, err, fence.acquired, fence.released)
			}
		})
	}
}
