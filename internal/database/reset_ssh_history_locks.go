package database

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Lifecycle lock is already held. A recovery continuation may find the whole
// exact old namespace in either its journaled source or archive location.
func (s *resetStore) resetHistoryLockDirectory(r ResetReceipt, name string) (*Directory, error) {
	n, ok := resetNamespace(r.Protected, name)
	if !ok || !n.Present {
		return nil, fmt.Errorf("source history lock namespace absent from reviewed scope")
	}
	parent := s.home
	if r.Archives[name] == "archived" {
		parent = s.operation
	} else if r.Phase == "retiring" && r.Step == name {
		if _, e := os.Lstat(filepath.Join(s.home.Path, name)); os.IsNotExist(e) {
			parent = s.operation
		}
	}
	d, e := resetChild(parent, name, false)
	if e != nil {
		return nil, e
	}
	if !sameIdentity(d.identity, n.Identity) {
		d.Close()
		return nil, fmt.Errorf("source history reset lock directory changed")
	}
	return d, nil
}
func (s *resetStore) lockSSHSourceHistory(r ResetReceipt) ([]*os.File, error) {
	n, ok := resetNamespace(r.Protected, sourceHistoryProofName)
	if !ok || !n.Present {
		return nil, nil
	}
	if e := validSSHHistoryEvidence(n); e != nil {
		return nil, e
	}
	locks := []*os.File{}
	fail := func(e error) ([]*os.File, error) {
		for _, f := range locks {
			unlockReset(f)
		}
		return nil, e
	}
	mesh, e := s.resetHistoryLockDirectory(r, "mesh")
	if e != nil {
		return nil, e
	}
	defer mesh.Close()
	meshEvidence, _ := resetNamespace(r.Protected, "mesh")
	var pin File
	for _, f := range meshEvidence.Files {
		if f.Name == "ssh-pins.lock" {
			pin = f
		}
	}
	if !pin.Present || pin.Size != 0 || pin.Mode != unix.S_IFREG|0600 {
		return fail(fmt.Errorf("reviewed source SSH lock missing/unsafe"))
	}
	f, e := plainHistoryLock(mesh, pin.Name, historyProofID(pin))
	if e != nil {
		return fail(e)
	}
	locks = append(locks, f)
	history, e := s.resetHistoryLockDirectory(r, sourceHistoryProofName)
	if e != nil {
		return fail(e)
	}
	defer history.Close()
	f, e = plainHistoryLock(history, ".worker.lock", historyProofID(n.Files[0]))
	if e != nil {
		return fail(e)
	}
	locks = append(locks, f)
	return locks, nil
}
