package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/local"
)

// ResetReview retains stopped authority while a concrete default-no preview is
// shown. Merely reviewing creates no entry in the user's state directory.
type ResetReview struct {
	Plan   ResetPlan `json:"plan"`
	review *SettledReview
}

func (r *ResetReview) Close() error {
	if r == nil || r.review == nil {
		return nil
	}
	err := r.review.Close()
	r.review = nil
	return err
}

func ReviewReset(ctx context.Context, home, reason string, retireMesh bool) (*ResetReview, error) {
	if len(reason) > 4096 {
		return nil, fmt.Errorf("reset reason must be at most 4096 bytes")
	}
	review, err := ReviewSettled(ctx, home, local.CurrentIdentity().UID)
	if err != nil {
		return nil, err
	}
	r := &ResetReview{review: review}
	fail := func(err error) (*ResetReview, error) { r.Close(); return nil, err }
	prior, err := reviewResetHistory(review.source.Directory, review.Proof.Host)
	if err != nil {
		return fail(err)
	}
	plan := ResetPlan{Schema: ResetSchema, ID: local.ID(), Home: review.source.Directory.Path, Actor: local.CurrentIdentity(), Reason: strings.TrimSpace(reason), Created: time.Now().UTC(), OldHost: review.Proof.Host, NewHost: local.ID(), Proof: review.Proof, Namespaces: append([]NamespaceEvidence{}, review.Namespaces...), Files: append([]File{}, review.source.Files...), Mesh: review.Mesh, RetireMesh: retireMesh, StorageRoot: review.Proof.StorageRoot, PriorSeed: prior}
	if plan.Mesh == nil {
		if n, ok := resetNamespace(plan.Namespaces, "mesh-lifecycle"); ok && n.Present {
			plan.Mesh = &MeshRetirement{Host: plan.OldHost, Required: true, Scope: "Archive local mesh lifecycle evidence only. No remote cleanup, trust revocation or resource release is implied."}
		}
	}
	plan.Preserved = []string{"Managed storage children and handed-off artifacts remain untouched and are not adopted by the new ledger.", "Operator configuration, agent instructions, client settings and completed reset receipts remain.", "Remote claims remain owned by their destination and retain their original authority and expiry."}
	plan.Effects = []string{"Permanently erase ONLY the reviewed state.db, state.db-wal and state.db-shm files; audit history and old idempotency keys are lost.", "Create a different local authority; old local operation keys and mesh trust cannot replay into the fresh ledger.", "Retain a private reset receipt, necessary coordination locks and a one-use prebuilt fresh-database seed. Explicit continuation completes interrupted work; startup never replays deletion."}
	plan.Effects = append(plan.Effects, "Discard database-held agent registrations, messages, custom-pool definitions and policy budgets. New requests must register/recreate those definitions; separate operator configuration remains.")
	if plan.Mesh != nil {
		plan.Effects = append(plan.Effects, "Archive the reviewed local mesh identity/key and lifecycle records; re-pairing is required. No remote trust is revoked.")
	}
	plan.SHA256, err = planDigest(plan)
	if err != nil {
		return fail(err)
	}
	r.Plan = plan
	return r, nil
}

// reviewResetHistory refuses incomplete/unfamiliar evidence. Receipt history is
// never silently purged to make another reset appear safe.
func reviewResetHistory(home *Directory, authority string) (File, error) {
	seed, seedFile, err := readResetJSON[ResetSeed](home, ResetSeedName)
	if err != nil && !os.IsNotExist(err) {
		return seedFile, err
	}
	if os.IsNotExist(err) {
		seedFile = File{Name: ResetSeedName}
	}
	container, err := resetChild(home, ResetDirectoryName, false)
	if os.IsNotExist(err) {
		if seedFile.Present {
			return seedFile, fmt.Errorf("seed pointer has no retained reset history")
		}
		return seedFile, nil
	}
	if err != nil {
		return seedFile, err
	}
	defer container.Close()
	names, err := resetNames(container, maxResetReceipts)
	if err != nil {
		return seedFile, err
	}
	foundSeed := false
	for _, name := range names {
		if !strings.HasPrefix(name, "reset-") || !validHost(strings.TrimPrefix(name, "reset-")) {
			return seedFile, fmt.Errorf("unknown maintenance entry retained")
		}
		op, err := resetChild(container, name, false)
		if err != nil {
			return seedFile, err
		}
		s := &resetStore{home: home, container: container, operation: op}
		receipt, _, err := s.readReceipt()
		op.Close()
		if err != nil {
			return seedFile, err
		}
		if receipt.Phase != "completed" || !receipt.SeedReady {
			return seedFile, fmt.Errorf("reset %s remains incomplete; explicitly inspect/continue that operation", receipt.Plan.ID)
		}
		if seedFile.Present && receipt.Plan.ID == seed.Operation {
			foundSeed = true
			if !seed.Installed || seed.Schema != ResetSchema || seed.Authority != authority || seed.Authority != receipt.Plan.NewHost || seed.PlanSHA256 != receipt.Plan.SHA256 || !sameResetJSON(seed.File, receipt.Seed) {
				return seedFile, fmt.Errorf("fresh authority seed is unconsumed or inconsistent; start ARM to finish installation before another reset")
			}
		}
	}
	if len(names) > 0 && (!seedFile.Present || !foundSeed) {
		return seedFile, fmt.Errorf("reset history has no matching consumed authority seed")
	}
	if err = home.Validate(); err != nil {
		return seedFile, err
	}
	return seedFile, nil
}

func resetReceiptPath(home, id string) string {
	return filepath.Join(home, ResetDirectoryName, "reset-"+id, resetReceiptName)
}
