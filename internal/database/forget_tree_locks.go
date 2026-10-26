package database

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func treeLockFile(tree ForgetTree, path string) (File, bool) {
	for _, e := range tree.Entries {
		if e.Path == path {
			return e.File, true
		}
	}
	return File{}, false
}
func lockForgetTree(d *Directory, tree ForgetTree) ([]*os.File, error) {
	if !tree.Root.Present {
		return nil, nil
	}
	lock := ".lock"
	if tree.Name == "ssh-source-history" {
		lock = ".worker.lock"
	}
	id, ok := treeLockFile(tree, lock)
	if !ok || id.Size != 0 || id.Mode != unix.S_IFREG|0600 {
		return nil, fmt.Errorf("existing private %s/%s required for native bookkeeping exclusion", tree.Name, lock)
	}
	f, err := plainHistoryLock(d, lock, historyProofID(id))
	if err != nil {
		return nil, err
	}
	locks := []*os.File{f}
	if id, ok = treeLockFile(tree, "packs/.historypack.lock"); ok {
		packs, err := resetChild(d, "packs", false)
		if err != nil {
			unlockReset(f)
			return nil, err
		}
		lock, err := plainHistoryLock(packs, ".historypack.lock", historyProofID(id))
		packs.Close()
		if err != nil {
			unlockReset(f)
			return nil, err
		}
		locks = append(locks, lock)
	}
	return locks, nil
}
func (s *resetStore) forgetTreeParent(r ResetReceipt, tree ForgetTree) *Directory {
	if r.ForgetMoves[tree.Name] != "source" {
		return s.operation
	}
	if r.Phase == "moving" && r.Step == "forget:"+tree.Name {
		if _, err := os.Lstat(filepath.Join(s.home.Path, tree.Name)); os.IsNotExist(err) {
			return s.operation
		}
	}
	return s.home
}
func (s *resetStore) lockForgetTrees(r ResetReceipt) ([]*os.File, error) {
	locks := []*os.File{}
	fail := func(e error) ([]*os.File, error) {
		for _, f := range locks {
			unlockReset(f)
		}
		return nil, e
	}
	for _, tree := range r.Plan.ForgetTrees {
		if !tree.Root.Present || r.ForgetMoves[tree.Name] == "erased" {
			continue
		}
		// Native locks are intentionally erased last within each root. A lost
		// acknowledgement at final root deletion needs no remaining worker lock.
		worker := map[string]string{"dashboard-pending": ".lock", "ssh-source-history": ".worker.lock"}[tree.Name]
		key := tree.Name + "/" + worker
		if r.ForgetErased[key] {
			continue
		}
		if r.Phase == "erasing" && r.Step == "forget:"+key {
			if _, e := os.Lstat(filepath.Join(s.forgetTreeParent(r, tree).Path, tree.Name, worker)); os.IsNotExist(e) {
				continue
			}
		}
		parent := s.forgetTreeParent(r, tree)
		d, e := resetChild(parent, tree.Name, false)
		if os.IsNotExist(e) && r.Phase == "erasing" && r.Step == "forget:"+tree.Name+"/" {
			continue
		}
		if e != nil {
			return fail(e)
		}
		if !sameIdentity(d.identity, tree.Root) {
			d.Close()
			return fail(fmt.Errorf("forget bookkeeping root replaced"))
		}
		remaining := tree
		remaining.Entries = []ForgetEntry{}
		for _, entry := range tree.Entries {
			key := tree.Name + "/" + entry.Path
			if r.ForgetErased[key] {
				continue
			}
			if r.Phase == "erasing" && r.Step == "forget:"+key {
				if _, e := os.Lstat(filepath.Join(d.Path, entry.Path)); os.IsNotExist(e) {
					continue
				}
			}
			remaining.Entries = append(remaining.Entries, entry)
		}
		held, e := lockForgetTree(d, remaining)
		d.Close()
		if e != nil {
			return fail(e)
		}
		locks = append(locks, held...)
	}
	return locks, nil
}
