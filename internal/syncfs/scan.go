package syncfs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/yrashk/arm/internal/syncreconcile"
	"golang.org/x/sys/unix"
)

type Metadata struct{ Permissions, ModTimes bool }
type ScanLimits struct {
	Entries int
	Bytes   int64
}
type NamePolicy struct{ CaseInsensitive bool }

// DetectNamePolicy probes an operation-owned private scratch directory on the
// selected root's filesystem. Linux per-directory casefold is refused during
// traversal; Unicode names are conservatively unsupported on both platforms.
func DetectNamePolicy(selected, scratch *Root) (NamePolicy, error) {
	if e := selected.Check(); e != nil {
		return NamePolicy{}, e
	}
	if e := scratch.Check(); e != nil {
		return NamePolicy{}, e
	}
	a, e := statFD(selected.FD())
	if e != nil {
		return NamePolicy{}, e
	}
	b, e := statFD(scratch.FD())
	if e != nil {
		return NamePolicy{}, e
	}
	if a.Dev != b.Dev {
		return NamePolicy{}, fmt.Errorf("name-policy scratch must share filesystem")
	}
	var nonce [8]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return NamePolicy{}, e
	}
	name := ".ARMcase" + hex.EncodeToString(nonce[:])
	other := strings.ToLower(name)
	fd, e := unix.Openat(scratch.FD(), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return NamePolicy{}, e
	}
	defer unix.Close(fd)
	return finishNameProbe(scratch, fd, name, other)
}

func finishNameProbe(scratch *Root, fd int, name, other string) (NamePolicy, error) {
	created, e := statFD(fd)
	if e != nil {
		return NamePolicy{}, e
	}
	otherStat, lookup := statNamed(scratch.FD(), other)
	if lookup == nil && !sameObject(created, otherStat) {
		return NamePolicy{}, ErrChanged
	}
	bound, e := statNamed(scratch.FD(), name)
	if e != nil || !sameObject(created, bound) {
		return NamePolicy{}, ErrChanged
	}
	if e := scratch.Check(); e != nil {
		return NamePolicy{}, e
	}
	remove := unix.Unlinkat(scratch.FD(), name, 0)
	syncErr := unix.Fsync(scratch.FD())
	if remove != nil || syncErr != nil {
		return NamePolicy{}, fmt.Errorf("name probe cleanup: remove=%v sync=%v", remove, syncErr)
	}
	remaining, e := statFD(fd)
	if e != nil || remaining.Nlink != 0 {
		return NamePolicy{}, ErrChanged
	}
	if e := scratch.Check(); e != nil {
		return NamePolicy{}, e
	}
	if lookup != nil && !errors.Is(lookup, unix.ENOENT) {
		return NamePolicy{}, lookup
	}
	return NamePolicy{CaseInsensitive: lookup == nil}, nil
}
func (p NamePolicy) Key(name string) (string, error) {
	if e := validateRelative(name); e != nil {
		return "", e
	}
	for _, r := range name {
		if r > 127 || r < 32 {
			return "", fmt.Errorf("non-ASCII or control sync path requires native normalization adapter")
		}
	}
	if p.CaseInsensitive {
		return strings.ToLower(name), nil
	}
	return name, nil
}

func metadataHash(st unix.Stat_t, kind syncreconcile.Kind, p Metadata) (string, error) {
	if st.Mode&07000 != 0 {
		return "", fmt.Errorf("setuid, setgid and sticky modes require reviewed metadata adapter")
	}
	if kind == syncreconcile.Symlink && (p.Permissions || p.ModTimes) {
		return "", fmt.Errorf("requested symlink metadata fidelity unsupported")
	}
	value := "v1"
	if p.Permissions {
		value += fmt.Sprintf(":mode=%03o", st.Mode&0777)
	}
	if p.ModTimes {
		sec, _ := statMtime(st)
		value += fmt.Sprintf(":mtime=%d", sec)
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:]), nil
}
func stableStat(a, b unix.Stat_t) bool {
	am, an := statMtime(a)
	bm, bn := statMtime(b)
	ac, acn := statCtime(a)
	bc, bcn := statCtime(b)
	return sameObject(a, b) && a.Size == b.Size && a.Mode == b.Mode && am == bm && an == bn && ac == bc && acn == bcn
}
func fingerprintAt(ctx context.Context, fd int, name string, p Metadata, maxBytes int64) (syncreconcile.Entry, error) {
	var out syncreconcile.Entry
	if e := ctx.Err(); e != nil {
		return out, e
	}
	before, e := statNamed(fd, name)
	if e != nil {
		return out, e
	}
	switch before.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		out.Kind = syncreconcile.Directory
	case unix.S_IFREG:
		out.Kind = syncreconcile.File
	case unix.S_IFLNK:
		out.Kind = syncreconcile.Symlink
	default:
		return out, fmt.Errorf("unsupported special entry %q", name)
	}
	out.MetadataHash, e = metadataHash(before, out.Kind, p)
	if e != nil {
		return out, e
	}
	if out.Kind == syncreconcile.Directory {
		return out, nil
	}
	if out.Kind == syncreconcile.Symlink {
		buf := make([]byte, 4097)
		n, e := unix.Readlinkat(fd, name, buf)
		if e != nil {
			return out, e
		}
		if n >= len(buf) {
			return out, fmt.Errorf("symlink target exceeds bound")
		}
		after, e := statNamed(fd, name)
		if e != nil || !stableStat(before, after) {
			return out, ErrChanged
		}
		sum := sha256.Sum256(buf[:n])
		out.ContentHash = hex.EncodeToString(sum[:])
		out.Size = int64(n)
		return out, nil
	}
	if before.Size < 0 || before.Size > 64<<30 || before.Size > maxBytes {
		return out, fmt.Errorf("file exceeds scan bound")
	}
	opened, e := unix.Openat(fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return out, e
	}
	f := os.NewFile(uintptr(opened), name)
	defer f.Close()
	openedStat, e := statFD(opened)
	if e != nil || !stableStat(before, openedStat) {
		return out, ErrChanged
	}
	h := sha256.New()
	buf := make([]byte, 128<<10)
	var read int64
	for {
		if e := ctx.Err(); e != nil {
			return out, e
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			read += int64(n)
			if read > before.Size {
				return out, ErrChanged
			}
			_, _ = h.Write(buf[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return out, readErr
		}
		if n == 0 {
			return out, io.ErrNoProgress
		}
	}
	afterFD, e := statFD(opened)
	if e != nil || !stableStat(before, afterFD) || read != before.Size {
		return out, ErrChanged
	}
	afterName, e := statNamed(fd, name)
	if e != nil || !stableStat(before, afterName) {
		return out, ErrChanged
	}
	out.Size = read
	out.ContentHash = hex.EncodeToString(h.Sum(nil))
	return out, nil
}
func excludedKey(key string, exclusions []string) bool {
	for _, x := range exclusions {
		if key == x || strings.HasPrefix(key, x+"/") {
			return true
		}
	}
	return false
}

// Scan returns selected fingerprints and keys for all observed, candidate and
// excluded names. Candidate names include absent peer/baseline/resolution
// paths; exclusion ancestors are always keyed. It never follows symlinks.
func (r *Root) Scan(ctx context.Context, candidates, exclusions []string, policy NamePolicy, metadata Metadata, limits ScanLimits) (syncreconcile.Snapshot, error) {
	var snap syncreconcile.Snapshot
	if ctx == nil {
		return snap, fmt.Errorf("nil scan context")
	}
	if e := r.Check(); e != nil {
		return snap, e
	}
	if limits.Entries <= 0 || limits.Entries > syncreconcile.MaxEntries || limits.Bytes <= 0 || limits.Bytes > 1<<40 {
		return snap, fmt.Errorf("invalid scan budget")
	}
	snap.Entries = make(map[string]syncreconcile.Entry)
	snap.NameKeys = make(map[string]string)
	var nativeExcludes []string
	addKey := func(name string) error {
		key, e := policy.Key(name)
		if e != nil {
			return e
		}
		if old, ok := snap.NameKeys[name]; ok && old != key {
			return ErrChanged
		}
		snap.NameKeys[name] = key
		if len(snap.NameKeys) > syncreconcile.MaxEntries*3 {
			return fmt.Errorf("name-key budget exceeded")
		}
		return nil
	}
	for _, x := range exclusions {
		if e := addKey(x); e != nil {
			return snap, e
		}
		nativeExcludes = append(nativeExcludes, snap.NameKeys[x])
		for parent := path.Dir(x); parent != "."; parent = path.Dir(parent) {
			if e := addKey(parent); e != nil {
				return snap, e
			}
		}
	}
	slices.Sort(nativeExcludes)
	nativeExcludes = slices.Compact(nativeExcludes)
	for _, x := range candidates {
		if e := addKey(x); e != nil {
			return snap, e
		}
		for parent := path.Dir(x); parent != "."; parent = path.Dir(parent) {
			if e := addKey(parent); e != nil {
				return snap, e
			}
		}
	}
	var total int64
	var walk func(fd int, prefix string, depth int) error
	walk = func(fd int, prefix string, depth int) error {
		if depth > syncreconcile.MaxDepth {
			return fmt.Errorf("scan depth exceeded")
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := checkCasefoldDir(fd); e != nil {
			return e
		}
		startStat, e := statFD(fd)
		if e != nil {
			return e
		}
		dup, e := unix.Openat(fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return e
		}
		dir := os.NewFile(uintptr(dup), "scan")
		defer dir.Close()
		for {
			batch, e := dir.ReadDir(128)
			if e != nil && e != io.EOF {
				return e
			}
			for _, d := range batch {
				name := d.Name()
				rel := name
				if prefix != "" {
					rel = prefix + "/" + name
				}
				if err := addKey(rel); err != nil {
					return err
				}
				if excludedKey(snap.NameKeys[rel], nativeExcludes) {
					continue
				}
				if len(snap.Entries) >= limits.Entries {
					return fmt.Errorf("entry budget exceeded")
				}
				entry, err := fingerprintAt(ctx, fd, name, metadata, limits.Bytes-total)
				if err != nil {
					return fmt.Errorf("scan %q: %w", rel, err)
				}
				snap.Entries[rel] = entry
				if entry.Size > limits.Bytes-total {
					return fmt.Errorf("byte budget exceeded")
				}
				total += entry.Size
				if entry.Kind == syncreconcile.Directory {
					child, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
					if err != nil {
						return err
					}
					current, err := statNamed(fd, name)
					held, heldErr := statFD(child)
					if err != nil || heldErr != nil || !sameObject(current, held) {
						_ = unix.Close(child)
						return ErrChanged
					}
					err = walk(child, rel, depth+1)
					afterNamed, namedErr := statNamed(fd, name)
					afterHeld, heldErr := statFD(child)
					_ = unix.Close(child)
					if err != nil {
						return err
					}
					if namedErr != nil || heldErr != nil || !sameObject(afterNamed, afterHeld) {
						return ErrChanged
					}
				}
			}
			if e == io.EOF {
				break
			}
		}
		endStat, e := statFD(fd)
		if e != nil || !stableStat(startStat, endStat) {
			return ErrChanged
		}
		return nil
	}
	if e := walk(r.FD(), "", 0); e != nil {
		return snap, e
	}
	if e := r.Check(); e != nil {
		return snap, e
	}
	return snap, nil
}
