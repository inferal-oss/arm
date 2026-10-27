package syncfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func completeScopeClaimFields() ScopeClaimFields {
	return ScopeClaimFields{
		Key: "scope-operation-12345", Side: "local", PreparationLease: "original-preparation",
		Provider: "xfs-project-quota", ScopeID: "xfs-project:mount-1:42", MountID: "mount-1",
		ProjectRootID: "project-1", SelectedRootID: "selected-1", ParentRootID: "parent-1", ProjectID: 42,
		OwnerEpoch: 1, OwnershipDigest: strings.Repeat("a", 64), PromiseBytes: 4096,
	}
}

func TestCompleteScopeRefusesCallerClaimsWithoutOriginalOwner(t *testing.T) {
	fields := completeScopeClaimFields()
	claim, err := NewScopeClaim(fields)
	if err != nil {
		t.Fatal(err)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(base, "project"), filepath.Join(base, "project", "selected"), filepath.Join(base, "project", "home")}
	roots := make([]*Root, 0, len(paths))
	for _, path := range paths {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		root, err := OpenRoot(path)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		roots = append(roots, root)
	}
	for _, owner := range []*ExclusiveScopeOwner{nil, {}, {claim: claim}} {
		proof, err := VerifyCompleteScope(context.Background(), claim, owner, roots[0], roots[1], roots[2])
		if !errors.Is(err, ErrQuotaUnknown) || proof != (CompleteScopeProof{}) {
			t.Fatalf("caller claim or empty owner minted proof: %+v %v", proof, err)
		}
	}
	if binding, err := (CompleteScopeProof{}).BindingFor(claim); !errors.Is(err, ErrQuotaUnknown) || binding != (ScopeBinding{}) {
		t.Fatalf("zero proof yielded binding: %+v %v", binding, err)
	}
	withoutOwner := CompleteScopeProof{claim: claim, binding: ScopeBinding{provider: fields.Provider}}
	if binding, err := withoutOwner.BindingFor(claim); !errors.Is(err, ErrQuotaUnknown) || binding != (ScopeBinding{}) {
		t.Fatalf("native-looking tuple without owner yielded binding: %+v %v", binding, err)
	}
}

func TestCompleteScopeClaimRejectsMalformedIdentity(t *testing.T) {
	base := completeScopeClaimFields()
	cases := map[string]func(*ScopeClaimFields){
		"short key":            func(v *ScopeClaimFields) { v.Key = "short" },
		"key control":          func(v *ScopeClaimFields) { v.Key = "scope-operation-\n12345" },
		"wrong side":           func(v *ScopeClaimFields) { v.Side = "both" },
		"missing lease":        func(v *ScopeClaimFields) { v.PreparationLease = "" },
		"wrong provider":       func(v *ScopeClaimFields) { v.Provider = "caller-bool" },
		"missing mount":        func(v *ScopeClaimFields) { v.MountID = "" },
		"missing project root": func(v *ScopeClaimFields) { v.ProjectRootID = "" },
		"missing root":         func(v *ScopeClaimFields) { v.SelectedRootID = "" },
		"zero project":         func(v *ScopeClaimFields) { v.ProjectID = 0 },
		"zero epoch":           func(v *ScopeClaimFields) { v.OwnerEpoch = 0 },
		"overflow epoch":       func(v *ScopeClaimFields) { v.OwnerEpoch = ^uint64(0) },
		"zero promise":         func(v *ScopeClaimFields) { v.PromiseBytes = 0 },
		"bad owner digest":     func(v *ScopeClaimFields) { v.OwnershipDigest = strings.Repeat("z", 64) },
		"uppercase digest":     func(v *ScopeClaimFields) { v.OwnershipDigest = strings.Repeat("A", 64) },
		"short owner digest":   func(v *ScopeClaimFields) { v.OwnershipDigest = "ab" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			fields := base
			change(&fields)
			if claim, err := NewScopeClaim(fields); !errors.Is(err, ErrQuotaUnknown) || claim != (ScopeClaim{}) {
				t.Fatalf("malformed claim accepted: %+v %v", claim, err)
			}
		})
	}
}

func TestCompleteScopeRefusesCancelledContext(t *testing.T) {
	claim, err := NewScopeClaim(completeScopeClaimFields())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	proof, err := VerifyCompleteScope(ctx, claim, nil, nil, nil, nil)
	if !errors.Is(err, context.Canceled) || proof != (CompleteScopeProof{}) {
		t.Fatalf("cancelled verification minted proof: %+v %v", proof, err)
	}
}
