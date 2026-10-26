package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"golang.org/x/sys/unix"
)

// Only newly created coordination infrastructure may differ from the reviewed
// namespaces. A damaged receipt must not omit the pending roster or retire a
// different directory while still claiming the original settled proof.
func validResetProtected(plan ResetPlan, protected []NamespaceEvidence) error {
	if plan.Forget {
		return validForgetProtected(plan, protected)
	}
	reviewed := map[string]NamespaceEvidence{}
	for _, n := range plan.Namespaces {
		if err := validSSHHistoryEvidence(n); err != nil {
			return err
		}
		if !oneOf(n.Name, "storage", "mesh", "mesh-lifecycle", "ssh-source-history", "dashboard-pending", filepath.Join(plan.Home, "mesh-lifecycle.lock"), filepath.Join(plan.Home, "dashboard-pending", ".lock")) {
			return fmt.Errorf("unrecognized reviewed reset namespace")
		}
		if _, exists := reviewed[n.Name]; exists {
			return fmt.Errorf("duplicate reviewed reset namespace")
		}
		reviewed[n.Name] = n
	}
	actual := map[string]NamespaceEvidence{}
	for _, n := range protected {
		if _, exists := actual[n.Name]; exists {
			return fmt.Errorf("duplicate protected reset namespace")
		}
		actual[n.Name] = n
	}
	lifecycleLock := filepath.Join(plan.Home, "mesh-lifecycle.lock")
	pendingLock := filepath.Join(plan.Home, "dashboard-pending", ".lock")
	expectedActual := 6
	if _, ok := reviewed["ssh-source-history"]; ok {
		expectedActual++
	}
	if len(actual) != expectedActual {
		return fmt.Errorf("protected reset namespace scope is incomplete")
	}
	if _, exists := reviewed[lifecycleLock]; !exists {
		return fmt.Errorf("reviewed lifecycle lock observation missing")
	}
	names := []string{"storage", "mesh", "mesh-lifecycle"}
	if _, ok := reviewed["ssh-source-history"]; ok {
		names = append(names, "ssh-source-history")
	}
	for _, name := range names {
		before, ok := reviewed[name]
		after, exists := actual[name]
		if !ok || !exists || !reflect.DeepEqual(before, after) {
			return fmt.Errorf("protected artifact/mesh namespace differs from exact review")
		}
	}
	for _, name := range []string{lifecycleLock, pendingLock} {
		after, exists := actual[name]
		if !exists || !after.Present || after.Identity.Owner != uint32Owner(plan.Actor.UID) || after.Identity.Mode&unix.S_IFMT != unix.S_IFREG || after.Identity.Mode&0077 != 0 || after.Identity.Size != 0 || len(after.Files) != 1 || !sameIdentity(after.Identity, after.Files[0]) {
			return fmt.Errorf("protected coordination lock is incomplete or unsafe")
		}
		if before, ok := reviewed[name]; ok && before.Present && !reflect.DeepEqual(before, after) {
			return fmt.Errorf("protected existing lock differs from review")
		}
	}
	before, ok := reviewed["dashboard-pending"]
	after, exists := actual["dashboard-pending"]
	if !ok || !exists || !after.Present || after.Identity.Mode&unix.S_IFMT != unix.S_IFDIR || after.Identity.Mode&0077 != 0 || after.Identity.Owner != uint32Owner(plan.Actor.UID) || len(after.Files) != 1 || after.Files[0].Name != ".lock" || after.Files[0].Size != 0 || after.Files[0].SHA256 != hashEmptyResetFile || !sameIdentity(after.Files[0], actual[pendingLock].Identity) {
		return fmt.Errorf("protected pending roster is not the exact empty locked namespace")
	}
	expectedReviewed := 5
	if _, ok := reviewed["ssh-source-history"]; ok {
		expectedReviewed++
	}
	if before.Present {
		expectedReviewed++
		if _, exists := reviewed[pendingLock]; !exists {
			return fmt.Errorf("reviewed pending lock observation missing")
		}
	}
	if len(reviewed) != expectedReviewed {
		return fmt.Errorf("reviewed namespace scope is incomplete")
	}
	if before.Present {
		if !sameIdentity(before.Identity, after.Identity) {
			return fmt.Errorf("protected pending namespace changed identity")
		}
		for _, file := range before.Files {
			if file.Name != ".lock" || !reflect.DeepEqual(file, after.Files[0]) {
				return fmt.Errorf("reviewed pending roster differs")
			}
		}
	}
	return nil
}

const hashEmptyResetFile = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func (s *resetStore) validateResetNamespaces(ctx context.Context, r ResetReceipt) error {
	if len(r.Protected) == 0 {
		return fmt.Errorf("protected reset namespace evidence is missing")
	}
	if _, known := resetNamespace(r.Protected, "ssh-source-history"); !known && !r.Plan.Forget {
		if _, err := os.Lstat(filepath.Join(s.home.Path, "ssh-source-history")); !os.IsNotExist(err) {
			return fmt.Errorf("unreviewed source history cannot be retired or adopted")
		}
	}
	for _, n := range r.Protected {
		archived := r.Archives[n.Name] == "archived"
		if !archived && r.Phase == "retiring" && r.Step == n.Name {
			if _, err := os.Lstat(filepath.Join(s.home.Path, n.Name)); os.IsNotExist(err) {
				archived = true
			}
		}
		if err := s.checkResetNamespace(ctx, n, archived); err != nil {
			return err
		}
		if archived {
			if _, err := os.Lstat(filepath.Join(s.home.Path, n.Name)); !os.IsNotExist(err) {
				return fmt.Errorf("retired mesh namespace reappeared")
			}
		}
	}
	return nil
}

func (s *resetStore) checkResetNamespace(ctx context.Context, n NamespaceEvidence, archive bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if filepath.IsAbs(n.Name) {
		if archive {
			return fmt.Errorf("coordination lock cannot be archived")
		}
		var d *Directory
		var name string
		switch n.Name {
		case filepath.Join(s.home.Path, "mesh-lifecycle.lock"):
			d, name = s.home, "mesh-lifecycle.lock"
		case filepath.Join(s.home.Path, "dashboard-pending", ".lock"):
			opened, err := resetChild(s.home, "dashboard-pending", false)
			if err != nil {
				return err
			}
			defer opened.Close()
			d, name = opened, ".lock"
		default:
			return fmt.Errorf("unrecognized absolute reset namespace")
		}
		if !n.Present {
			return fmt.Errorf("protected coordination lock was never established")
		}
		v, err := d.Observe(name, false)
		if err != nil || !sameIdentity(n.Identity, v) || v.Size != 0 {
			return fmt.Errorf("protected coordination lock changed")
		}
		return nil
	}
	if !oneOf(n.Name, "storage", "mesh", "mesh-lifecycle", "ssh-source-history", "dashboard-pending") {
		return fmt.Errorf("unrecognized reset namespace")
	}
	parent := s.home
	if archive {
		if n.Name != "mesh" && n.Name != "mesh-lifecycle" && n.Name != "ssh-source-history" {
			return fmt.Errorf("unrecognized retirement namespace")
		}
		parent = s.operation
	}
	d, err := resetChild(parent, n.Name, false)
	if !n.Present {
		if !os.IsNotExist(err) {
			if d != nil {
				d.Close()
			}
			return fmt.Errorf("unreviewed namespace appeared")
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer d.Close()
	if !sameIdentity(d.identity, n.Identity) {
		return fmt.Errorf("protected reset namespace identity changed")
	}
	if s.forgetMount != "" {
		if err := requireForgetMount(d.FD, s.forgetMount, forgetMountIdentity); err != nil {
			return err
		}
	}
	if n.Name == "ssh-source-history" {
		return checkSSHHistoryEvidencePath(ctx, filepath.Join(parent.Path, n.Name), n)
	}
	if n.Name == "storage" {
		return nil
	} // Old children deliberately remain unadopted.
	names, err := resetNames(d, len(n.Files))
	if err != nil {
		return err
	}
	if len(names) != len(n.Files) {
		return fmt.Errorf("protected reset namespace membership changed")
	}
	for i, v := range n.Files {
		if names[i] != v.Name {
			return fmt.Errorf("protected reset namespace names changed")
		}
		if s.forgetMount != "" {
			guard, e := forgetFileGuard(d, v, s.forgetMount)
			if e != nil {
				return e
			}
			if guard != nil {
				guard.Close()
			}
		}
		if _, err = exactResetFile(ctx, d, v, false); err != nil {
			return err
		}
	}
	if err = parent.Validate(); err != nil {
		return err
	}
	return nil
}

func (s *resetStore) lockProtectedNamespaces(r ResetReceipt) ([]*os.File, error) {
	locks := []*os.File{}
	fail := func(err error) ([]*os.File, error) {
		for _, f := range locks {
			unlockReset(f)
		}
		return nil, err
	}
	items := []struct{ dir, name string }{{"", "mesh-lifecycle.lock"}, {"dashboard-pending", ".lock"}}
	if r.Plan.Forget {
		items = items[:1]
	}
	for _, item := range items {
		d := s.home
		if item.dir != "" {
			opened, err := resetChild(s.home, item.dir, false)
			if err != nil {
				return fail(err)
			}
			defer opened.Close()
			d = opened
		}
		f, v, err := d.Open(item.name, unix.O_RDONLY)
		if err != nil {
			return fail(err)
		}
		if f == nil {
			return fail(fmt.Errorf("protected reset lock is missing"))
		}
		if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
			f.Close()
			return fail(fmt.Errorf("protected reset namespace is busy: %w", err))
		}
		n, ok := resetNamespace(r.Protected, filepath.Join(d.Path, item.name))
		if !ok || !n.Present || !sameIdentity(n.Identity, v) {
			f.Close()
			return fail(fmt.Errorf("protected reset lock identity changed"))
		}
		locks = append(locks, f)
	}
	if r.Plan.Forget {
		held, e := s.lockForgetTrees(r)
		if e != nil {
			return fail(e)
		}
		locks = append(locks, held...)
		if len(r.Plan.ForgetTrees) == 2 && r.Plan.ForgetTrees[1].Root.Present {
			mesh, e := s.resetHistoryLockDirectory(r, "mesh")
			if e != nil {
				return fail(e)
			}
			n, _ := resetNamespace(r.Protected, "mesh")
			var pin File
			for _, f := range n.Files {
				if f.Name == "ssh-pins.lock" {
					pin = f
				}
			}
			f, e := plainHistoryLock(mesh, pin.Name, historyProofID(pin))
			mesh.Close()
			if e != nil {
				return fail(e)
			}
			locks = append(locks, f)
		}
		return locks, nil
	}
	historyLocks, err := s.lockSSHSourceHistory(r)
	if err != nil {
		return fail(err)
	}
	locks = append(locks, historyLocks...)
	return locks, nil
}
