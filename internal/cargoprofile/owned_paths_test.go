package cargoprofile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"getarm.org/internal/model"
	"getarm.org/internal/storage"
)

func allocationFixture(t *testing.T) (string, model.FileIdentity) {
	t.Helper()
	root := fixtureRoot(t)
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	id, e := storage.DirectoryIdentity(int(f.Fd()))
	if e != nil {
		t.Fatal(e)
	}
	return root, id
}
func TestOwnedCargoRootsRequireExclusiveDurableBirths(t *testing.T) {
	root, id := allocationFixture(t)
	var steps []BirthStep
	owned, e := CreateOwnedPaths(context.Background(), root, id, "0123456789abcdef0123456789abcdef", func(s BirthStep) error { steps = append(steps, s); return nil })
	if e != nil {
		t.Fatal(e)
	}
	if len(steps) != 8 {
		t.Fatal("before/after evidence missing", len(steps))
	}
	for i := 0; i < 8; i += 2 {
		if steps[i].Step != i/2+1 || steps[i].Born != nil || steps[i+1].Born == nil || steps[i+1].Born.Inode == 0 || steps[i].Path != steps[i+1].Path {
			t.Fatal(steps)
		}
	}
	if owned.Roots().Target == owned.Roots().Build || owned.Roots().Build == owned.Roots().Control {
		t.Fatal("output/control roots not distinct")
	}
	if e = owned.Revalidate(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = CreateOwnedPaths(context.Background(), root, id, "0123456789abcdef0123456789abcdef", func(BirthStep) error { return nil }); e == nil {
		t.Fatal("existing tree adopted")
	}
	if e = os.Chmod(owned.Control.Path, 0755); e != nil {
		t.Fatal(e)
	}
	if e = owned.Revalidate(context.Background()); e == nil {
		t.Fatal("changed private mode accepted")
	}
}
func TestCargoBornAckFailureRetainsEvidenceAndNeverAdopts(t *testing.T) {
	root, id := allocationFixture(t)
	stop := errors.New("durable acknowledgement lost")
	partial, e := CreateOwnedPaths(context.Background(), root, id, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", func(s BirthStep) error {
		if s.Born != nil {
			return stop
		}
		return nil
	})
	if !errors.Is(e, stop) || partial.Root.Identity.Inode == 0 {
		t.Fatal(partial, e)
	}
	if _, e = os.Stat(partial.Root.Path); e != nil {
		t.Fatal("uncertain born directory erased", e)
	}
	if _, e = CreateOwnedPaths(context.Background(), root, id, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", func(BirthStep) error { return nil }); e == nil {
		t.Fatal("partial born tree reused")
	}
	root2, id2 := allocationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	if _, e = CreateOwnedPaths(ctx, root2, id2, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", func(BirthStep) error { cancel(); return nil }); e == nil {
		t.Fatal("cancelled birth proceeded")
	}
	items, e := os.ReadDir(root2)
	if e != nil || len(items) != 0 {
		t.Fatal("mutation after cancelled intent", items, e)
	}
}
func TestOwnedCargoPathsRejectReplacementAndSymlink(t *testing.T) {
	root, id := allocationFixture(t)
	owned, e := CreateOwnedPaths(context.Background(), root, id, "cccccccccccccccccccccccccccccccc", func(BirthStep) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(owned.Target.Path, owned.Target.Path+"-retained"); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(owned.Target.Path, 0700); e != nil {
		t.Fatal(e)
	}
	if e = owned.Revalidate(context.Background()); e == nil {
		t.Fatal("same-path replacement accepted")
	}
	if e = os.Remove(owned.Target.Path); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(filepath.Base(owned.Target.Path)+"-retained", owned.Target.Path); e != nil {
		t.Fatal(e)
	}
	if e = owned.Revalidate(context.Background()); e == nil {
		t.Fatal("symlink target accepted")
	}
	for _, bad := range []string{"", "x", "cargo-x", "../cccccccccccccccccccccccccccccccc"} {
		copy := owned
		copy.Root.Name = bad
		if e = copy.Revalidate(context.Background()); e == nil {
			t.Fatal("malformed root accepted", bad)
		}
	}
}
