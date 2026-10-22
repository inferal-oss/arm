package sharefs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/storage"
	"golang.org/x/sys/unix"
)

func (v Version) validate(owner uint32, mount string, dir bool) error {
	typ := uint32(unix.S_IFREG)
	if dir {
		typ = unix.S_IFDIR
	}
	if v.Mode&unix.S_IFMT != typ || v.Mode&07022 != 0 || v.Identity.Owner != owner || !v.Identity.BirthKnown || v.Identity.Inode == 0 || v.Identity.BornSeconds <= 0 || v.Identity.BornNanoseconds < 0 || v.Identity.BornNanoseconds >= 1e9 || v.Size < 0 || v.Mount == "" || len(v.Mount) > 128 || v.Mount != mount || v.ModifiedNanos < 0 || v.ModifiedNanos >= 1e9 || v.ChangedNanos < 0 || v.ChangedNanos >= 1e9 || v.Links == 0 || (!dir && v.Links != 1) {
		return fmt.Errorf("unsupported file identity, type, link, mode or mount")
	}
	return nil
}

func (m Manifest) Validate(l Limits) error {
	if err := l.validate(); err != nil {
		return err
	}
	if err := m.Binding.validate(); err != nil {
		return err
	}
	if m.Schema != 1 || m.ChunkBytes != ChunkBytes || len(m.Entries) < 1 || len(m.Entries) > l.Entries || !digest(m.SHA256) {
		return fmt.Errorf("unsupported or oversized share manifest")
	}
	if m.Entries[0].Path != "" || !m.Entries[0].Directory {
		return fmt.Errorf("share root entry required")
	}
	mount := m.Entries[0].Version.Mount
	seen := map[string]bool{}
	chunks, bytes := 0, 0
	for i, e := range m.Entries {
		if err := validPath(e.Path); err != nil {
			return err
		}
		if i > 0 && (e.Path <= m.Entries[i-1].Path || !seen[parent(e.Path)]) {
			return fmt.Errorf("share entries must have unique sorted paths and directory parents")
		}
		if err := e.Version.validate(m.Binding.Owner, mount, e.Directory); err != nil {
			return err
		}
		if e.Chunks == nil {
			return fmt.Errorf("explicit share chunk roster required")
		}
		want := int64(0)
		if !e.Directory {
			want = (e.Version.Size + ChunkBytes - 1) / ChunkBytes
		}
		if e.Version.Size > int64(MaxChunks)*ChunkBytes || want > int64(l.Chunks-chunks) || int64(len(e.Chunks)) != want {
			return ErrLimit
		}
		chunks += len(e.Chunks)
		for _, h := range e.Chunks {
			if !digest(h) {
				return fmt.Errorf("invalid chunk digest")
			}
		}
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		bytes += len(b) + 1
		if bytes > l.ManifestBytes {
			return ErrLimit
		}
		seen[e.Path] = e.Directory
	}
	h, n, err := manifestHash(m)
	if err != nil {
		return err
	}
	if n > l.ManifestBytes {
		return ErrLimit
	}
	if h != m.SHA256 {
		return fmt.Errorf("share manifest commitment differs")
	}
	return nil
}

// Build observes a complete bounded immutable byte set. It does not freeze the
// OS tree or grant publication; changed backing later causes unavailable reads.
func (e *Engine) Build(ctx context.Context, p *storage.Provider, a model.Artifact, b Binding) (Manifest, error) {
	if err := b.validate(); err != nil {
		return Manifest{}, err
	}
	if a.Identity == nil || b.Storage != a.Name || b.Owner != a.Identity.Owner {
		return Manifest{}, ErrDenied
	}
	if err := e.enter(ctx); err != nil {
		return Manifest{}, err
	}
	defer e.leave()
	root, err := p.OpenArtifactReadOnly(ctx, a)
	if err != nil {
		return Manifest{}, err
	}
	defer root.Close()
	rv, err := version(int(root.Fd()))
	if err != nil {
		return Manifest{}, err
	}
	if rv.Identity != *a.Identity {
		return Manifest{}, ErrChanged
	}
	if err = rv.validate(b.Owner, rv.Mount, true); err != nil {
		return Manifest{}, err
	}
	m := Manifest{Schema: 1, Binding: b, ChunkBytes: ChunkBytes, Entries: []Entry{}}
	buf := make([]byte, ChunkBytes)
	countChunks, manifestBytes := 0, 0
	var walk func(*os.File, string, Version) error
	walk = func(f *os.File, path string, v Version) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := v.Mode&unix.S_IFMT == unix.S_IFDIR
		if err := validPath(path); err != nil {
			return err
		}
		if err := v.validate(b.Owner, rv.Mount, dir); err != nil {
			return err
		}
		if len(m.Entries) >= e.limits.Entries {
			return ErrLimit
		}
		x := Entry{Path: path, Directory: dir, Version: v, Chunks: []string{}}
		if !dir {
			if v.Size > int64(e.limits.Chunks-countChunks)*ChunkBytes {
				return ErrLimit
			}
			for offset := int64(0); offset < v.Size; offset += ChunkBytes {
				if err := ctx.Err(); err != nil {
					return err
				}
				n := int(min(int64(ChunkBytes), v.Size-offset))
				if _, err := f.ReadAt(buf[:n], offset); err != nil {
					return err
				}
				h := sha256.Sum256(buf[:n])
				x.Chunks = append(x.Chunks, hex.EncodeToString(h[:]))
				countChunks++
			}
		}
		encoded, err := json.Marshal(x)
		if err != nil {
			return err
		}
		manifestBytes += len(encoded) + 1
		if manifestBytes > e.limits.ManifestBytes {
			return ErrLimit
		}
		m.Entries = append(m.Entries, x)
		if dir {
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				children, readErr := f.ReadDir(64)
				for _, d := range children {
					childPath := d.Name()
					if path != "" {
						childPath = path + "/" + childPath
					}
					if err := validPath(childPath); err != nil {
						return err
					}
					// Nonblocking/no-follow prevents FIFO/symlink opens from hanging.
					fd, err := unix.Openat(int(f.Fd()), d.Name(), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
					if err != nil {
						return err
					}
					child := os.NewFile(uintptr(fd), d.Name())
					cv, err := version(fd)
					if err == nil {
						err = walk(child, childPath, cv)
					}
					child.Close()
					if err != nil {
						return err
					}
				}
				if readErr == io.EOF {
					break
				}
				if readErr != nil {
					return readErr
				}
			}
		}
		after, err := version(int(f.Fd()))
		if err != nil {
			return err
		}
		if after != v {
			return ErrChanged
		}
		return nil
	}
	if err = walk(root, "", rv); err != nil {
		return Manifest{}, err
	}
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Path < m.Entries[j].Path })
	m.SHA256, _, err = manifestHash(m)
	if err != nil {
		return Manifest{}, err
	}
	if err = m.Validate(e.limits); err != nil {
		return Manifest{}, err
	}
	// Named-root checks after hashing reject a root renamed/replaced mid-build.
	check, err := p.OpenArtifactReadOnly(ctx, a)
	if err != nil {
		return Manifest{}, err
	}
	defer check.Close()
	v, err := version(int(check.Fd()))
	if err != nil {
		return Manifest{}, err
	}
	if v != rv {
		return Manifest{}, ErrChanged
	}
	if err = ctx.Err(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}
