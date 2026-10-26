package database

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// Only these two ARM bookkeeping roots are deletable. Storage/artifact roots
// never enter this inventory. Receipts contain identities/hashes, not payloads.
type ForgetTree struct {
	Mount   string        `json:"mount"`
	Name    string        `json:"name"`
	Root    File          `json:"root"`
	Entries []ForgetEntry `json:"entries"`
}
type ForgetEntry struct {
	Path string `json:"path"`
	File File   `json:"file"`
}

const maxForgetEntries = 1000
const maxForgetTreeBytes int64 = 1 << 30

func forgetTreeName(name string) bool {
	return name == "dashboard-pending" || name == "ssh-source-history"
}
func scanForgetTree(ctx context.Context, parent *Directory, name string) (ForgetTree, error) {
	return scanForgetTreeMount(ctx, parent, name, forgetMountIdentity)
}
func scanForgetTreeMount(ctx context.Context, parent *Directory, name string, probe forgetMountProbe) (ForgetTree, error) {
	tree := ForgetTree{Name: name, Root: File{Name: name}, Entries: []ForgetEntry{}}
	mount, err := probe(parent.FD)
	if err != nil {
		return tree, err
	}
	tree.Mount = mount
	if !forgetTreeName(name) {
		return tree, fmt.Errorf("unknown forget bookkeeping root")
	}
	d, err := resetChild(parent, name, false)
	if os.IsNotExist(err) {
		return tree, nil
	}
	if err != nil {
		return tree, err
	}
	defer d.Close()
	if err = requireForgetMount(d.FD, mount, probe); err != nil {
		return tree, err
	}
	tree.Root = d.identity
	tree.Root.Name = name
	var total int64
	var walk func(*Directory, string, int) error
	walk = func(dir *Directory, prefix string, depth int) error {
		if depth > 4 {
			return fmt.Errorf("forget bookkeeping depth exceeds four")
		}
		fd, err := unix.Openat(int(dir.FD.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(fd), dir.Path)
		names, err := f.Readdirnames(maxForgetEntries + 1)
		f.Close()
		if err != nil && err != io.EOF {
			return err
		}
		sort.Strings(names)
		for _, name := range names {
			if err = ctx.Err(); err != nil {
				return err
			}
			if !safeName(name) || len(name) > 128 || len(tree.Entries) >= maxForgetEntries {
				return fmt.Errorf("forget bookkeeping name/entry bound exceeded")
			}
			var st unix.Stat_t
			if err = unix.Fstatat(int(dir.FD.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			path := prefix + name
			if uint32(st.Mode)&unix.S_IFMT == unix.S_IFDIR {
				child, err := resetChild(dir, name, false)
				if err != nil {
					return err
				}
				if err = requireForgetMount(child.FD, mount, probe); err != nil {
					child.Close()
					return err
				}
				id := child.identity
				id.Name = name
				tree.Entries = append(tree.Entries, ForgetEntry{path, id})
				err = walk(child, path+"/", depth+1)
				child.Close()
				if err != nil {
					return err
				}
			} else {
				f, id, err := dir.openSource(name)
				if err != nil {
					return err
				}
				if f == nil {
					return fmt.Errorf("forget bookkeeping file disappeared")
				}
				if err = requireForgetMount(f, mount, probe); err != nil {
					f.Close()
					return err
				}
				total += id.Size
				if total > maxForgetTreeBytes {
					f.Close()
					return fmt.Errorf("forget bookkeeping exceeds one GiB")
				}
				id.SHA256, err = hashFile(ctx, f)
				f.Close()
				if err != nil {
					return err
				}
				tree.Entries = append(tree.Entries, ForgetEntry{path, id})
			}
		}
		return dir.Validate()
	}
	err = walk(d, "", 0)
	return tree, err
}
func sameForgetTree(a, b ForgetTree) bool {
	if a.Mount != b.Mount || a.Name != b.Name || !sameIdentity(a.Root, b.Root) || len(a.Entries) != len(b.Entries) {
		return false
	}
	for i, x := range a.Entries {
		y := b.Entries[i]
		if x.Path != y.Path || !sameIdentity(x.File, y.File) {
			return false
		}
		if x.File.Mode&unix.S_IFMT != unix.S_IFDIR && (x.File.Size != y.File.Size || x.File.SHA256 != y.File.SHA256) {
			return false
		}
	}
	return true
}
func validForgetTrees(plan ResetPlan) error {
	if !plan.Forget {
		if plan.ForgetMount != "" || len(plan.ForgetTrees) != 0 {
			return fmt.Errorf("ordinary reset cannot forget bookkeeping")
		}
		return nil
	}
	if len(plan.ForgetTrees) != 2 {
		return fmt.Errorf("forget bookkeeping scope incomplete")
	}
	totalEntries := 0
	for i, n := range []string{"dashboard-pending", "ssh-source-history"} {
		tree := plan.ForgetTrees[i]
		totalEntries += len(tree.Entries)
		if totalEntries > maxForgetEntries {
			return fmt.Errorf("combined forget bookkeeping exceeds 1000 entries")
		}

		if plan.ForgetMount == "" || len(plan.ForgetMount) > 2048 || tree.Mount != plan.ForgetMount || tree.Name != n || tree.Root.Name != n || len(tree.Entries) > maxForgetEntries {
			return fmt.Errorf("invalid forget bookkeeping root/entry scope")
		}
		if !tree.Root.Present {
			if len(tree.Entries) != 0 {
				return fmt.Errorf("absent forget root has entries")
			}
			continue
		}
		if tree.Root.Mode&unix.S_IFMT != unix.S_IFDIR || tree.Root.Mode&0077 != 0 || tree.Root.Owner != uint32Owner(plan.Actor.UID) {
			return fmt.Errorf("unsafe forget bookkeeping directory")
		}
		paths := map[string]bool{}
		var total int64
		for _, entry := range tree.Entries {
			p, f := entry.Path, entry.File
			if p == "" || p != filepath.ToSlash(filepath.Clean(p)) || strings.HasPrefix(p, "/") || len(strings.Split(p, "/")) > 5 || paths[p] || f.Name != filepath.Base(p) || !f.Present || f.Owner != tree.Root.Owner || f.Mode&0022 != 0 {
				return fmt.Errorf("invalid forget bookkeeping entry")
			}
			for _, part := range strings.Split(p, "/") {
				if !safeName(part) || len(part) > 128 {
					return fmt.Errorf("unsafe forget bookkeeping path")
				}
			}
			if parent := filepath.ToSlash(filepath.Dir(p)); parent != "." && !paths[parent] {
				return fmt.Errorf("forget bookkeeping parent omitted")
			}
			kind := f.Mode & unix.S_IFMT
			if kind == unix.S_IFDIR {
				if f.Mode&0077 != 0 || f.SHA256 != "" {
					return fmt.Errorf("unsafe forget directory")
				}
			} else if kind != unix.S_IFREG || !validDigest(f.SHA256) {
				return fmt.Errorf("unsafe forget file")
			}
			total += f.Size
			if total > maxForgetTreeBytes+maxForgetEntries*65536 {
				return fmt.Errorf("forget tree size bound exceeded")
			}
			paths[p] = true
		}
	}
	return nil
}
