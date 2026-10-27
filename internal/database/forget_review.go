package database

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"strings"
	"time"

	"getarm.org/internal/local"
)

var forgetDatabaseNames = []string{"state.db", "state.db-wal", "state.db-shm", "state.db-journal"}

const forgetScope = "NOT CHECKED: the operator explicitly chooses to forget the ledger without proof of schema support or settled obligations. Processes, leases, remote claims and external resources are not stopped, released or proven reclaimed."

func resetDatabaseNames(plan ResetPlan) []string {
	if plan.Forget {
		return forgetDatabaseNames
	}
	return databaseNames
}
func forgetProof(owner string, at time.Time) SettledProof {
	return SettledProof{Schema: "not-checked-explicit-forget-v1", Owner: owner, Observed: at, Tables: map[string]int64{}, Blockers: []ProofBlocker{}, Scope: forgetScope}
}

// forgetSource never opens SQLite or copies an old database payload. Only exact
// native file identity and streaming hashes are observed under daemon exclusion.
func forgetSource(ctx context.Context, home string) (_ *Source, err error) {
	d, err := OpenDirectory(home)
	if err != nil {
		return nil, err
	}
	mount, err := forgetMountIdentity(d.FD)
	if err != nil {
		d.Close()
		return nil, err
	}
	s := &Source{Directory: d, opaque: true, mount: mount}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	s.lock, err = lockResetAuthority(d)
	if err != nil {
		return nil, err
	}
	for _, name := range forgetDatabaseNames {
		f, _, e := d.openSource(name)
		if e != nil {
			return nil, e
		}
		if f != nil {
			e = requireForgetMount(f, mount, forgetMountIdentity)
			f.Close()
			if e != nil {
				return nil, e
			}
		}
	}
	s.Files, err = sourceInventoryNames(ctx, d, forgetDatabaseNames, true)
	if err != nil {
		return nil, err
	}
	if err = s.Validate(ctx, true); err != nil {
		return nil, err
	}
	return s, nil
}

// ReviewForget is a distinct explicit database-loss contract. It does not
// represent unknown obligations as settled and never invokes the proof reader.
// Non-database namespaces keep their native identity/coordination checks.
func ReviewForget(ctx context.Context, home, reason string, retireMesh bool) (_ *ResetReview, err error) {
	if len(reason) > 4096 {
		return nil, fmt.Errorf("reset reason must be at most 4096 bytes")
	}
	if err = local.CheckStateFence(home); err != nil {
		return nil, err
	}
	review := &SettledReview{}
	defer func() {
		if err != nil {
			review.Close()
		}
	}()
	review.source, err = forgetSource(ctx, home)
	if err != nil {
		return nil, err
	}
	d := review.source.Directory
	if err = review.existingLock(d, "mesh-lifecycle.lock"); err != nil {
		return nil, err
	}
	storage, err := review.child("storage")
	if err != nil {
		return nil, err
	}
	if storage != nil {
		review.Namespaces = append(review.Namespaces, NamespaceEvidence{Name: "storage", Present: true, Identity: storage.identity, Files: []File{}})
	}
	for _, name := range []string{"mesh", "mesh-lifecycle"} {
		child, e := review.child(name)
		if e != nil {
			return nil, e
		}
		if child != nil {
			if e = requireForgetMount(child.FD, review.source.mount, forgetMountIdentity); e != nil {
				return nil, e
			}
			if _, _, e = review.readNamespace(ctx, child, MaxRecordBytes, 512); e != nil {
				return nil, e
			}
			review.Mesh = &MeshRetirement{Required: true, Scope: "Archive this exact local identity/lifecycle namespace with --retire-mesh; no remote revocation, cleanup or settlement is implied."}
		}
	}
	// These exact bounded bookkeeping roots are forgotten, not adopted by the
	// next authority. Their worker/client locks remain held through erasure.
	for _, name := range []string{"dashboard-pending", "ssh-source-history"} {
		tree, e := scanForgetTree(ctx, d, name)
		if e != nil {
			return nil, e
		}
		if tree.Root.Present {
			child, e := resetChild(d, name, false)
			if e != nil {
				return nil, e
			}
			held, e := lockForgetTree(child, tree)
			child.Close()
			if e != nil {
				return nil, e
			}
			review.locks = append(review.locks, held...)
		}
		review.forgetTrees = append(review.forgetTrees, tree)
	}
	if review.forgetTrees[1].Root.Present {
		// Source history also coordinates with the exact native SSH pin lock.
		mesh, e := resetChild(d, "mesh", false)
		if e != nil {
			return nil, e
		}
		var pin File
		for _, n := range review.Namespaces {
			if n.Name == "mesh" {
				for _, f := range n.Files {
					if f.Name == "ssh-pins.lock" {
						pin = f
					}
				}
			}
		}
		if !pin.Present || pin.Size != 0 || pin.Mode != unix.S_IFREG|0600 {
			mesh.Close()
			return nil, fmt.Errorf("existing private mesh/ssh-pins.lock required for source-history exclusion")
		}
		f, e := plainHistoryLock(mesh, pin.Name, historyProofID(pin))
		mesh.Close()
		if e != nil {
			return nil, e
		}
		review.locks = append(review.locks, f)
	}
	at := SnapshotTime()
	review.Proof = forgetProof(local.CurrentIdentity().UID, at)
	// Retained prior reset provenance must still be exact. An unconsumed or
	// damaged pointer is never converted into permission to replace authority.
	authority := ""
	seed, _, e := readResetJSON[ResetSeed](d, ResetSeedName)
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if e == nil {
		authority = seed.Authority
	}
	prior, err := reviewResetHistory(d, authority)
	if err != nil {
		return nil, err
	}
	if err = review.Validate(ctx); err != nil {
		return nil, err
	}
	plan := ResetPlan{ForgetMount: review.source.mount, ForgetTrees: append([]ForgetTree{}, review.forgetTrees...), Schema: ResetSchema, Forget: true, ID: local.ID(), Home: d.Path, Actor: local.CurrentIdentity(), Reason: strings.TrimSpace(reason), Created: at, NewHost: local.ID(), Proof: review.Proof, Namespaces: append([]NamespaceEvidence{}, review.Namespaces...), Files: append([]File{}, review.source.Files...), Mesh: review.Mesh, RetireMesh: retireMesh, PriorSeed: prior}
	plan.Effects = []string{
		"FORGET: permanently erase the exact state.db, WAL, SHM and rollback journal without checking schema or settled obligations. No old database copy or backup is retained after successful completion.",
		forgetScope,
		"Erase the exact reviewed local dashboard-pending intents and ssh-source-history catalog/packs, so no old bookkeeping is adopted by the new authority.",
		"Discard all ledger history and idempotency records; install a new local authority once on the next backend start. Old authority is unverified because the ledger is not read.",
		"A crash or interruption can retain original database inodes in the private reset operation until explicit --continue finishes erasure; no rollback or automatic restart occurs.",
	}
	if plan.Mesh != nil {
		plan.Effects = append(plan.Effects, "With --retire-mesh, archive local mesh identity and lifecycle files; re-pairing is required. Remote resources/trust remain unchanged.")
	}
	plan.Preserved = []string{"Managed files, processes, external resources, operator configuration and installed software remain untouched; no cleanup or reclamation is claimed.", "Minimal reset receipts retain file identities/hashes and this intent, not old database contents. The fresh seed contains only new authority metadata.", "Existing storage children are not adopted. An existing storage root without proven infrastructure metadata remains unavailable to the new authority."}
	if err = validForgetTrees(plan); err != nil {
		return nil, err
	}
	plan.SHA256, err = planDigest(plan)
	if err != nil {
		return nil, err
	}
	encoded, e := json.Marshal(plan)
	if e != nil || len(encoded) > MaxRecordBytes {
		return nil, fmt.Errorf("complete forget plan exceeds one MiB; no intent created")
	}
	if err = checkForgetReceiptBudget(plan); err != nil {
		return nil, err
	}
	return &ResetReview{Plan: plan, review: review}, nil
}
