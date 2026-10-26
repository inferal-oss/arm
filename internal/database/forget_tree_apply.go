package database

import (
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"sort"
)

func (s *resetStore) moveForgetTree(ctx context.Context, r *ResetReceipt, file *File, tree ForgetTree, hook func(string) error) error {
	if r.ForgetMoves[tree.Name] != "source" {
		return nil
	}
	step := "forget:" + tree.Name
	if s.forgetTreeParent(*r, tree) == s.home {
		r.Phase, r.Step = "moving", step
		if e := s.updateReceipt(r, file); e != nil {
			return e
		}
		if e := resetCheckpoint(ctx, hook, "before-move:"+step); e != nil {
			return e
		}
		if e := s.validate(); e != nil {
			return e
		}
		if e := s.validateForgetProgress(ctx, *r); e != nil {
			return e
		}
		if e := renameResetExclusive(int(s.home.FD.Fd()), tree.Name, int(s.operation.FD.Fd()), tree.Name); e != nil {
			return e
		}
		if e := resetCheckpoint(ctx, hook, "after-move:"+step); e != nil {
			return e
		}
	}
	if e := s.home.Sync(); e != nil {
		return e
	}
	if e := s.operation.Sync(); e != nil {
		return e
	}
	r.ForgetMoves[tree.Name] = "quarantined"
	r.Step = ""
	return s.updateReceipt(r, file)
}
func (s *resetStore) eraseForgetTree(ctx context.Context, r *ResetReceipt, file *File, tree ForgetTree, hook func(string) error) error {
	if oneOf(r.ForgetMoves[tree.Name], "absent", "erased") {
		return nil
	}
	entries := append([]ForgetEntry{}, tree.Entries...)
	// Descendants before directories; empty worker locks last. File descriptors
	// remain held through final root erasure, including continuation recovery.
	sort.SliceStable(entries, func(i, j int) bool {
		worker := func(p string) bool { return p == ".lock" || p == ".worker.lock" }
		if worker(entries[i].Path) != worker(entries[j].Path) {
			return !worker(entries[i].Path)
		}
		return entries[i].Path > entries[j].Path
	})
	entries = append(entries, ForgetEntry{Path: "", File: tree.Root})
	for _, entry := range entries {
		key := tree.Name + "/" + entry.Path
		if r.ForgetErased[key] {
			continue
		}
		r.Phase, r.Step = "erasing", "forget:"+key
		if e := s.updateReceipt(r, file); e != nil {
			return e
		}
		if e := resetCheckpoint(ctx, hook, "before-erase:forget:"+key); e != nil {
			return e
		}
		if e := s.validate(); e != nil {
			return e
		}
		if e := s.validateForgetProgress(ctx, *r); e != nil {
			return e
		}
		path := filepath.Join(s.operation.Path, tree.Name, entry.Path)
		if _, e := os.Lstat(path); e == nil {
			parent, e := OpenDirectory(filepath.Dir(path))
			if e != nil {
				return e
			}
			if e = requireForgetMount(parent.FD, r.Plan.ForgetMount, forgetMountIdentity); e != nil {
				parent.Close()
				return e
			}
			var target *os.File
			if entry.File.Mode&unix.S_IFMT == unix.S_IFDIR {
				child, e := resetChild(parent, filepath.Base(path), false)
				if e != nil {
					parent.Close()
					return e
				}
				if !sameIdentity(child.identity, entry.File) {
					child.Close()
					parent.Close()
					return fmt.Errorf("forget erase directory replaced")
				}
				target = child.FD
			} else {
				target, e = forgetFileGuard(parent, entry.File, r.Plan.ForgetMount)
				if e != nil {
					parent.Close()
					return e
				}
				if target == nil {
					parent.Close()
					return fmt.Errorf("forget erase file disappeared after validation")
				}
			}
			if e = requireForgetMount(target, r.Plan.ForgetMount, forgetMountIdentity); e != nil {
				target.Close()
				parent.Close()
				return e
			}
			// Full tree validation immediately above binds every remaining inode/hash;
			// unlink is relative to the independently pinned private parent.
			flags := 0
			if entry.File.Mode&unix.S_IFMT == unix.S_IFDIR {
				flags = unix.AT_REMOVEDIR
			}
			e = unix.Unlinkat(int(parent.FD.Fd()), filepath.Base(path), flags)
			if e == nil {
				e = parent.Sync()
			}
			target.Close()
			parent.Close()
			if e != nil {
				return e
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		if e := resetCheckpoint(ctx, hook, "after-erase:forget:"+key); e != nil {
			return e
		}
		r.ForgetErased[key] = true
		r.Step = ""
		if e := s.updateReceipt(r, file); e != nil {
			return e
		}
	}
	r.ForgetMoves[tree.Name] = "erased"
	return s.updateReceipt(r, file)
}
