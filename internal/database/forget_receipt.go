package database

import (
	"fmt"
	"golang.org/x/sys/unix"
	"path/filepath"
	"strings"
)

func validForgetProtected(plan ResetPlan, protected []NamespaceEvidence) error {
	names := []string{filepath.Join(plan.Home, "mesh-lifecycle.lock"), "storage", "mesh", "mesh-lifecycle"}
	if len(plan.Namespaces) != len(names) || len(protected) != len(names) {
		return fmt.Errorf("forget protected native namespace scope incomplete")
	}
	for _, name := range names {
		before, n := resetNamespace(plan.Namespaces, name)
		after, m := resetNamespace(protected, name)
		if !n || !m {
			return fmt.Errorf("forget protected namespace omitted")
		}
		if name != names[0] {
			if !reflectForgetNamespace(before, after) {
				return fmt.Errorf("forget protected namespace differs from exact review")
			}
			continue
		}
		if !after.Present || after.Identity.Owner != uint32Owner(plan.Actor.UID) || after.Identity.Mode != unix.S_IFREG|0600 || after.Identity.Size != 0 || len(after.Files) != 1 || !sameIdentity(after.Identity, after.Files[0]) {
			return fmt.Errorf("forget lifecycle lock lacks native exclusion")
		}
		if before.Present && !reflectForgetNamespace(before, after) {
			return fmt.Errorf("forget lifecycle lock changed")
		}
	}
	return nil
}
func reflectForgetNamespace(a, b NamespaceEvidence) bool {
	// Existing native snapshots carry no row contents. Exact JSON equality also
	// retains file hashes, sizes and any future reviewed namespace fields.
	return sameResetJSON(a, b)
}
func validForgetStep(plan ResetPlan, step string) bool {
	if !strings.HasPrefix(step, "forget:") {
		return false
	}
	path := strings.TrimPrefix(step, "forget:")
	for _, tree := range plan.ForgetTrees {
		if path == tree.Name || path == tree.Name+"/" {
			return true
		}
		for _, entry := range tree.Entries {
			if path == tree.Name+"/"+entry.Path {
				return true
			}
		}
	}
	return false
}
func validForgetReceipt(r ResetReceipt) error {
	if !r.Plan.Forget {
		if len(r.ForgetMoves) != 0 || len(r.ForgetErased) != 0 {
			return fmt.Errorf("ordinary receipt cannot forget bookkeeping")
		}
		return nil
	}
	if len(r.ForgetMoves) != 2 {
		return fmt.Errorf("forget receipt lost bookkeeping progress")
	}
	keys := map[string]bool{}
	for _, tree := range r.Plan.ForgetTrees {
		state := r.ForgetMoves[tree.Name]
		if !oneOf(state, "source", "absent", "quarantined", "erased") || tree.Root.Present == (state == "absent") {
			return fmt.Errorf("forget receipt bookkeeping presence/progress differs")
		}
		keys[tree.Name+"/"] = true
		for _, entry := range tree.Entries {
			keys[tree.Name+"/"+entry.Path] = true
		}
		if state == "erased" && !r.ForgetErased[tree.Name+"/"] {
			return fmt.Errorf("forget root erasure lacks exact progress")
		}
		if r.Phase == "completed" && state != "absent" && state != "erased" {
			return fmt.Errorf("completed forget retains bookkeeping payload obligations")
		}
	}
	for key, v := range r.ForgetErased {
		if !v || !keys[key] {
			return fmt.Errorf("unknown forget bookkeeping erase progress")
		}
	}
	return nil
}
