package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *resetStore) validateForgetProgress(ctx context.Context, r ResetReceipt) error {
	for _, tree := range r.Plan.ForgetTrees {
		state := r.ForgetMoves[tree.Name]
		if state == "absent" || state == "erased" {
			for _, parent := range []*Directory{s.home, s.operation} {
				d, e := resetChild(parent, tree.Name, false)
				if d != nil {
					d.Close()
				}
				if !os.IsNotExist(e) {
					return fmt.Errorf("absent/erased forget root reappeared")
				}
			}
			continue
		}
		parent := s.forgetTreeParent(r, tree)
		if parent == s.home {
			d, e := resetChild(s.operation, tree.Name, false)
			if d != nil {
				d.Close()
			}
			if !os.IsNotExist(e) {
				return fmt.Errorf("forget bookkeeping quarantine already exists")
			}
		}
		if parent == s.operation {
			if _, e := os.Lstat(filepath.Join(s.home.Path, tree.Name)); !os.IsNotExist(e) {
				return fmt.Errorf("forgotten bookkeeping source reappeared")
			}
		}
		current, e := scanForgetTree(ctx, parent, tree.Name)
		if e != nil {
			return e
		}
		expected := tree
		expected.Entries = []ForgetEntry{}
		for _, entry := range tree.Entries {
			if !r.ForgetErased[tree.Name+"/"+entry.Path] {
				expected.Entries = append(expected.Entries, entry)
			}
		}
		// At most the exact durable next unlink/rmdir can have lost acknowledgement.
		if !sameForgetTree(expected, current) && r.Phase == "erasing" && strings.HasPrefix(r.Step, "forget:"+tree.Name+"/") {
			path := strings.TrimPrefix(r.Step, "forget:"+tree.Name+"/")
			if path == "" {
				expected.Root = File{Name: tree.Name}
				expected.Entries = []ForgetEntry{}
			} else {
				kept := []ForgetEntry{}
				for _, e := range expected.Entries {
					if e.Path != path {
						kept = append(kept, e)
					}
				}
				expected.Entries = kept
			}
		}
		if !sameForgetTree(expected, current) {
			return fmt.Errorf("forget bookkeeping identity/bytes/membership changed")
		}
	}
	return ctx.Err()
}
