package database

import (
	"context"
	"errors"
	"fmt"
	"github.com/yrashk/arm/internal/local"
	"golang.org/x/sys/unix"
	"os"
)

type resetFence struct {
	Schema    int    `json:"schema"`
	Operation string `json:"operation"`
	Plan      string `json:"plan_sha256"`
	Owner     string `json:"owner"`
}

// Apply is deliberately separate from review. Callers must obtain explicit
// default-no confirmation of Plan before invoking it. A failed/uncertain apply
// never silently rolls back, restarts ARM or replays a destructive action.
func (r *ResetReview) Apply(ctx context.Context) (ResetResult, error) { return r.apply(ctx, nil) }

func (r *ResetReview) apply(ctx context.Context, checkpoint func(string) error) (ResetResult, error) {
	if r == nil || r.review == nil {
		return ResetResult{}, fmt.Errorf("live stopped reset review required")
	}
	if r.Plan.Forget != r.review.source.opaque {
		return ResetResult{}, fmt.Errorf("reset mode differs from the original review contract")
	}
	if err := validResetPlan(r.Plan, r.review.source.Directory.Path); err != nil {
		return ResetResult{}, err
	}
	if err := r.review.Validate(ctx); err != nil {
		return ResetResult{}, err
	}
	if err := r.prepareResetLocks(ctx); err != nil {
		return ResetResult{}, err
	}
	if _, err := exactResetFile(ctx, r.review.source.Directory, r.Plan.PriorSeed, false); err != nil {
		return ResetResult{}, err
	}
	s, err := openResetStore(r.Plan.Home, r.Plan.ID, true)
	if err != nil {
		return ResetResult{}, err
	}
	defer s.close()
	if err = s.checkForgetMount(r.Plan); err != nil {
		return ResetResult{}, err
	}
	receipt := ResetReceipt{Schema: ResetSchema, Plan: r.Plan, Phase: "staged", Revision: 1, Moves: map[string]string{}, Archives: map[string]string{}, Protected: append([]NamespaceEvidence{}, r.review.Namespaces...)}
	if r.Plan.Forget {
		receipt.ForgetMoves = map[string]string{}
		receipt.ForgetErased = map[string]bool{}
		for _, tree := range r.Plan.ForgetTrees {
			state := "absent"
			if tree.Root.Present {
				state = "source"
			}
			receipt.ForgetMoves[tree.Name] = state
		}
	}
	for _, f := range r.Plan.Files {
		if f.Present {
			receipt.Moves[f.Name] = "source"
		} else {
			receipt.Moves[f.Name] = "absent"
		}
	}
	for _, n := range receipt.Protected {
		if n.Present && (n.Name == "mesh" || n.Name == "mesh-lifecycle" || n.Name == "ssh-source-history") {
			receipt.Archives[n.Name] = "source"
		}
	}
	if err = s.saveReceipt(receipt, nil); err != nil {
		return ResetResult{}, err
	}
	receipt, file, err := s.readReceipt()
	if err != nil {
		return ResetResult{}, err
	}
	if err = resetCheckpoint(ctx, checkpoint, "receipt-staged"); err != nil {
		return resetResult(receipt), err
	}
	if err = s.buildResetSeed(ctx, &receipt, &file); err != nil {
		return resetResult(receipt), err
	}
	if err = resetCheckpoint(ctx, checkpoint, "seed-built"); err != nil {
		return resetResult(receipt), err
	}
	if err = r.review.Validate(ctx); err != nil {
		return resetResult(receipt), err
	}
	return s.finishReset(ctx, &receipt, &file, checkpoint)
}

func resetResult(r ResetReceipt) ResetResult {
	detail := "Reset is incomplete. ARM startup is fenced once intent is published; explicitly review and continue this operation. No automatic recovery or rollback was attempted."
	if r.Phase == "completed" {
		detail = "The receipt records erased SQLite files, but fence finalization still requires explicit continuation before startup is confirmed safe."
	}
	return ResetResult{Operation: r.Plan.ID, Completed: false, OldHost: r.Plan.OldHost, NewHost: r.Plan.NewHost, Receipt: resetReceiptPath(r.Plan.Home, r.Plan.ID), Preserved: r.Plan.Preserved, Detail: detail}
}
func resetCompletedResult(r ResetReceipt) ResetResult {
	result := resetResult(r)
	result.Completed = true
	result.Detail = "The reviewed SQLite files were erased. Artifacts and local archives were retained. The next backend start installs the reviewed fresh authority once; old mesh trust requires re-pairing."
	if r.Plan.Forget {
		result.Detail = "The exact SQLite file group was forgotten without a retained database copy. Obligations were NOT checked: processes and external resources were not stopped or proven reclaimed. The next backend start installs the reviewed fresh authority once; preserved storage children are not adopted and retired mesh requires re-pairing."
	}
	return result
}
func resetCheckpoint(ctx context.Context, hook func(string) error, stage string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if hook != nil {
		return hook(stage)
	}
	return nil
}

func (s *resetStore) finishReset(ctx context.Context, r *ResetReceipt, file *File, hook func(string) error) (ResetResult, error) {
	fail := func(err error) (ResetResult, error) { return resetResult(*r), err }
	if err := s.checkForgetMount(r.Plan); err != nil {
		return fail(err)
	}
	if err := s.validate(); err != nil {
		return fail(err)
	}
	if !r.SeedReady {
		return fail(fmt.Errorf("fresh authority is not durably constructed"))
	}
	if _, err := exactResetFile(ctx, s.operation, r.Seed, false); err != nil {
		return fail(err)
	}
	if err := s.validateResetNamespaces(ctx, *r); err != nil {
		return fail(err)
	}
	if err := s.validateResetProgress(ctx, *r); err != nil {
		return fail(err)
	}
	currentSeed, _, seedError := readResetJSON[ResetSeed](s.home, ResetSeedName)
	intendedSeed := ResetSeed{Schema: ResetSchema, Operation: r.Plan.ID, Authority: r.Plan.NewHost, PlanSHA256: r.Plan.SHA256, File: r.Seed}
	if seedError != nil && !os.IsNotExist(seedError) {
		return fail(seedError)
	}
	if seedError != nil || !sameResetJSON(currentSeed, intendedSeed) {
		if _, err := exactResetFile(ctx, s.home, r.Plan.PriorSeed, false); err != nil {
			return fail(err)
		}
	}
	fence := resetFence{ResetSchema, r.Plan.ID, r.Plan.SHA256, r.Plan.Actor.UID}
	held, marker, err := readResetJSON[resetFence](s.home, local.ResetFenceName)
	if os.IsNotExist(err) {
		for _, state := range r.Moves {
			if state != "source" && state != "absent" {
				return fail(fmt.Errorf("reset fence disappeared after a database move"))
			}
		}
		for _, state := range r.Archives {
			if state != "source" {
				return fail(fmt.Errorf("reset fence disappeared after retirement"))
			}
		}
		r.Phase, r.Step = "fenced", "fence"
		if err = s.updateReceipt(r, file); err != nil {
			return fail(err)
		}
		if err = resetCheckpoint(ctx, hook, "fence-intent"); err != nil {
			return fail(err)
		}
		if err = atomicResetJSON(s.home, local.ResetFenceName, fence, nil); err != nil {
			return fail(err)
		}
		held, marker, err = readResetJSON[resetFence](s.home, local.ResetFenceName)
	}
	if err != nil || !sameResetJSON(held, fence) {
		return fail(fmt.Errorf("reset fence is missing, unsafe or belongs to another exact plan"))
	}
	if err = resetCheckpoint(ctx, hook, "fence-published"); err != nil {
		return fail(err)
	}
	for _, expected := range r.Plan.Files {
		if err = s.moveResetDatabase(ctx, r, file, expected, hook); err != nil {
			return fail(err)
		}
	}
	for _, name := range []string{"mesh", "mesh-lifecycle", "ssh-source-history"} {
		if _, ok := r.Archives[name]; ok {
			if err = s.archiveResetNamespace(ctx, r, file, name, hook); err != nil {
				return fail(err)
			}
		}
	}
	for _, tree := range r.Plan.ForgetTrees {
		if err = s.moveForgetTree(ctx, r, file, tree, hook); err != nil {
			return fail(err)
		}
	}
	seed := ResetSeed{Schema: ResetSchema, Operation: r.Plan.ID, Authority: r.Plan.NewHost, PlanSHA256: r.Plan.SHA256, File: r.Seed}
	current, pointer, err := readResetJSON[ResetSeed](s.home, ResetSeedName)
	if err == nil && sameResetJSON(current, seed) {
		// Exact prior publication after a lost acknowledgement is observed;
		// nothing is sent to another authority or recreated.
	} else {
		if err != nil && !os.IsNotExist(err) {
			return fail(err)
		}
		var previous *File
		if r.Plan.PriorSeed.Present {
			if err != nil || !sameIdentity(pointer, r.Plan.PriorSeed) || pointer.SHA256 != r.Plan.PriorSeed.SHA256 {
				return fail(fmt.Errorf("previous seed pointer changed"))
			}
			previous = &r.Plan.PriorSeed
		} else if err == nil {
			return fail(fmt.Errorf("unexpected fresh-authority pointer appeared"))
		}
		r.Phase, r.Step = "seed_ready", "seed"
		if err = s.updateReceipt(r, file); err != nil {
			return fail(err)
		}
		if err = atomicResetJSON(s.home, ResetSeedName, seed, previous); err != nil {
			return fail(err)
		}
	}
	if err = resetCheckpoint(ctx, hook, "seed-pointer-published"); err != nil {
		return fail(err)
	}
	for _, expected := range r.Plan.Files {
		if err = s.eraseResetDatabase(ctx, r, file, expected, hook); err != nil {
			return fail(err)
		}
	}
	for _, tree := range r.Plan.ForgetTrees {
		if err = s.eraseForgetTree(ctx, r, file, tree, hook); err != nil {
			return fail(err)
		}
	}
	if err = s.validateForgetProgress(ctx, *r); err != nil {
		return fail(err)
	}
	if err = s.validateResetNamespaces(ctx, *r); err != nil {
		return fail(err)
	}
	if err = s.validateResetFileLocations(ctx, *r); err != nil {
		return fail(err)
	}
	if r.Phase != "completed" {
		now := SnapshotTime()
		r.Completed = &now
		r.Phase, r.Step = "completed", ""
		if err = s.updateReceipt(r, file); err != nil {
			return fail(err)
		}
	}
	if err = resetCheckpoint(ctx, hook, "completed-receipt"); err != nil {
		return fail(err)
	}
	// Only this exact private fence is removed, after the durable completion
	// receipt and authority pointer exist. An fsync failure remains explicit.
	_, nowMarker, err := readResetJSON[resetFence](s.home, local.ResetFenceName)
	if err != nil || !sameIdentity(marker, nowMarker) || marker.SHA256 != nowMarker.SHA256 {
		return fail(fmt.Errorf("reset fence changed before completion"))
	}
	if err = unix.Unlinkat(int(s.home.FD.Fd()), local.ResetFenceName, 0); err != nil {
		return fail(err)
	}
	if err = s.home.Sync(); err != nil {
		return fail(err)
	}
	return resetCompletedResult(*r), resetCheckpoint(ctx, hook, "fence-removed")
}

func (s *resetStore) moveResetDatabase(ctx context.Context, r *ResetReceipt, file *File, expected File, hook func(string) error) error {
	state := r.Moves[expected.Name]
	if state == "absent" {
		_, err := exactResetFile(ctx, s.home, expected, true)
		return err
	}
	if state == "quarantined" || state == "erased" {
		return nil
	}
	current, err := s.home.Observe(expected.Name, false) // source may retain 0644; use source policy below when present
	if err != nil {
		f, v, e := s.home.openSource(expected.Name)
		if e != nil {
			return e
		}
		if f != nil {
			f.Close()
		}
		current = v
	}
	if !current.Present {
		if r.Phase != "moving" || r.Step != expected.Name {
			return fmt.Errorf("reviewed database disappeared outside its journalled move")
		}
		if _, err = exactResetFile(ctx, s.operation, expected, true); err != nil {
			return err
		}
	} else {
		if _, err = exactResetFile(ctx, s.home, expected, true); err != nil {
			return err
		}
		if target, err := s.operation.Observe(expected.Name, false); err != nil || target.Present {
			return fmt.Errorf("database quarantine destination already exists")
		}
		r.Phase, r.Step = "moving", expected.Name
		if err = s.updateReceipt(r, file); err != nil {
			return err
		}
		if err = resetCheckpoint(ctx, hook, "before-move:"+expected.Name); err != nil {
			return err
		}
		if err = s.validate(); err != nil {
			return err
		}
		if _, err = exactResetFile(ctx, s.home, expected, true); err != nil {
			return err
		}
		var guard *os.File
		if r.Plan.Forget {
			guard, err = forgetFileGuard(s.home, expected, r.Plan.ForgetMount)
			if err != nil {
				return err
			}
			if guard != nil {
				defer guard.Close()
			}
			if err = s.checkForgetMount(r.Plan); err != nil {
				return err
			}
		}
		if err = renameResetExclusive(int(s.home.FD.Fd()), expected.Name, int(s.operation.FD.Fd()), expected.Name); err != nil {
			return err
		}
		if err = resetCheckpoint(ctx, hook, "after-move:"+expected.Name); err != nil {
			return err
		}
		if _, err = exactResetFile(ctx, s.operation, expected, true); err != nil {
			return err
		}
	}
	if err = errors.Join(s.home.Sync(), s.operation.Sync()); err != nil {
		return err
	}
	r.Moves[expected.Name] = "quarantined"
	r.Step = ""
	return s.updateReceipt(r, file)
}

func (s *resetStore) eraseResetDatabase(ctx context.Context, r *ResetReceipt, file *File, expected File, hook func(string) error) error {
	state := r.Moves[expected.Name]
	if state == "absent" || state == "erased" {
		return nil
	}
	if state != "quarantined" {
		return fmt.Errorf("database was not safely quarantined")
	}
	f, current, err := s.operation.openSource(expected.Name)
	if err != nil {
		return err
	}
	if f != nil {
		f.Close()
	}
	if !current.Present {
		if r.Phase != "erasing" || r.Step != expected.Name {
			return fmt.Errorf("quarantined database disappeared outside journalled erase")
		}
	} else {
		if _, err = exactResetFile(ctx, s.operation, expected, true); err != nil {
			return err
		}
		r.Phase, r.Step = "erasing", expected.Name
		if err = s.updateReceipt(r, file); err != nil {
			return err
		}
		if err = resetCheckpoint(ctx, hook, "before-erase:"+expected.Name); err != nil {
			return err
		}
		if err = s.validate(); err != nil {
			return err
		}
		if _, err = exactResetFile(ctx, s.operation, expected, true); err != nil {
			return err
		}
		var guard *os.File
		if r.Plan.Forget {
			guard, err = forgetFileGuard(s.operation, expected, r.Plan.ForgetMount)
			if err != nil {
				return err
			}
			if guard != nil {
				defer guard.Close()
			}
			if err = s.checkForgetMount(r.Plan); err != nil {
				return err
			}
		}
		if err = unix.Unlinkat(int(s.operation.FD.Fd()), expected.Name, 0); err != nil {
			return err
		}
		if err = resetCheckpoint(ctx, hook, "after-erase:"+expected.Name); err != nil {
			return err
		}
	}
	if err = s.operation.Sync(); err != nil {
		return err
	}
	r.Moves[expected.Name] = "erased"
	r.Step = ""
	return s.updateReceipt(r, file)
}

// Refuse a pathname introduced after the complete reviewed three-file scope.
func (s *resetStore) validateResetFileLocations(ctx context.Context, r ResetReceipt) error {
	for _, expected := range r.Plan.Files {
		f, v, err := s.home.openSource(expected.Name)
		if err != nil {
			return err
		}
		if f != nil {
			f.Close()
		}
		if v.Present {
			return fmt.Errorf("database pathname reappeared after reset: %s", expected.Name)
		}
		f, v, err = s.operation.openSource(expected.Name)
		if err != nil {
			return err
		}
		if f != nil {
			f.Close()
		}
		if v.Present {
			return fmt.Errorf("quarantined database still exists: %s", expected.Name)
		}
	}
	if !r.Plan.Forget {
		journal, err := s.home.Observe("state.db-journal", false)
		if err != nil || journal.Present {
			return fmt.Errorf("unreviewed SQLite journal appeared")
		}
	}
	return ctx.Err()
}

func (s *resetStore) archiveResetNamespace(ctx context.Context, r *ResetReceipt, file *File, name string, hook func(string) error) error {
	if !r.Plan.RetireMesh {
		return fmt.Errorf("mesh namespace retirement requires explicit --retire-mesh")
	}
	n, ok := resetNamespace(r.Protected, name)
	if !ok || !n.Present {
		return fmt.Errorf("retirement lacks exact reviewed namespace")
	}
	if r.Archives[name] == "archived" {
		return nil
	}
	source, err := resetChild(s.home, name, false)
	if os.IsNotExist(err) {
		if r.Phase != "retiring" || r.Step != name {
			return fmt.Errorf("mesh evidence disappeared outside journalled retirement")
		}
		if err = s.checkResetNamespace(ctx, n, true); err != nil {
			return err
		}
	} else {
		if err != nil {
			return err
		}
		source.Close()
		if err = s.checkResetNamespace(ctx, n, false); err != nil {
			return err
		}
		archive, e := resetChild(s.operation, name, false)
		if archive != nil {
			archive.Close()
		}
		if !os.IsNotExist(e) {
			return fmt.Errorf("mesh archive destination already exists or is unsafe")
		}
		r.Phase, r.Step = "retiring", name
		if err = s.updateReceipt(r, file); err != nil {
			return err
		}
		if err = resetCheckpoint(ctx, hook, "before-archive:"+name); err != nil {
			return err
		}
		if err = s.validate(); err != nil {
			return err
		}
		if err = s.checkResetNamespace(ctx, n, false); err != nil {
			return err
		}
		if err = renameResetExclusive(int(s.home.FD.Fd()), name, int(s.operation.FD.Fd()), name); err != nil {
			return err
		}
		if err = resetCheckpoint(ctx, hook, "after-archive:"+name); err != nil {
			return err
		}
		if err = s.checkResetNamespace(ctx, n, true); err != nil {
			return err
		}
	}
	if err = errors.Join(s.home.Sync(), s.operation.Sync()); err != nil {
		return err
	}
	r.Archives[name] = "archived"
	r.Step = ""
	return s.updateReceipt(r, file)
}

func resetNamespace(namespaces []NamespaceEvidence, name string) (NamespaceEvidence, bool) {
	for _, n := range namespaces {
		if n.Name == name {
			return n, true
		}
	}
	return NamespaceEvidence{}, false
}
