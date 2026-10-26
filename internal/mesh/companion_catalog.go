package mesh

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/inferal-oss/arm/internal/companions"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/meshssh"
	"golang.org/x/sys/unix"
)

type CatalogIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	Owner  uint32 `json:"owner"`
	Mode   uint32 `json:"mode"`
}

func catalogIdentity(f *os.File) (CatalogIdentity, error) {
	var s unix.Stat_t
	if e := unix.Fstat(int(f.Fd()), &s); e != nil {
		return CatalogIdentity{}, e
	}
	return CatalogIdentity{uint64(s.Dev), s.Ino, s.Uid, uint32(s.Mode)}, nil
}

type CompanionCatalog struct {
	Catalog   *companions.Catalog
	directory *os.File
	Path      string
}

func (c *CompanionCatalog) Close() error {
	if c == nil {
		return nil
	}
	return c.directory.Close()
}

// Private catalog files are always opened relative to the verified directory;
// no caller-provided path or filesystem spelling becomes cleanup authority.
type catalogFiles struct {
	directory *os.File
	path      string
}

func (c catalogFiles) Open(name string) (fs.File, error) {
	if !strings.HasPrefix(name, "generated/") {
		return nil, fmt.Errorf("unknown catalog member")
	}
	name = strings.TrimPrefix(name, "generated/")
	if name != "manifest.json" && !regexp.MustCompile(`^arm-(darwin|linux)-(amd64|arm64)\.gz$`).MatchString(name) {
		return nil, fmt.Errorf("unknown catalog filename")
	}
	if e := provisionRootNamed(c.directory, c.path); e != nil {
		return nil, e
	}
	fd, e := unix.Openat(int(c.directory.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	info, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || s.Mode&07777 != 0600 || s.Uid != uint32(os.Geteuid()) || s.Nlink != 1 || info.Size() < 1 || info.Size() > companions.MaxPayloadBytes {
		f.Close()
		return nil, fmt.Errorf("catalog files require bounded private owned single-link identities")
	}
	return f, nil
}
func privateChildDirectory(parent *os.File, name string) (*os.File, error) {
	fd, e := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil || st.Uid != uint32(os.Geteuid()) || (st.Mode&unix.S_IFMT) != unix.S_IFDIR || (st.Mode&07777) != 0700 {
		f.Close()
		return nil, fmt.Errorf("catalog child directory must be private and owned")
	}
	return f, nil
}
func readCompanionCatalog(path, digest, version string, protocol int) (*CompanionCatalog, error) {
	dir, e := directory(path)
	if e != nil {
		return nil, e
	}
	fail := func(e error) (*CompanionCatalog, error) { dir.Close(); return nil, e }
	identity, e := catalogIdentity(dir)
	if e != nil || identity.Mode != unix.S_IFDIR|0700 {
		return fail(fmt.Errorf("catalog directory requires exact protected mode"))
	}
	names, e := dir.Readdirnames(6)
	if e != nil && e != io.EOF {
		return fail(e)
	}
	if len(names) != 5 {
		return fail(fmt.Errorf("catalog must contain exactly manifest plus four payloads"))
	}
	found := map[string]bool{}
	for _, name := range names {
		if found[name] {
			return fail(fmt.Errorf("duplicate catalog filename"))
		}
		found[name] = true
	}
	if !found["manifest.json"] {
		return fail(fmt.Errorf("catalog manifest missing"))
	}
	catalog, e := companions.Load(catalogFiles{dir, path}, version, protocol)
	if e != nil {
		return fail(e)
	}
	_, actual, e := companions.Canonical(catalog.Manifest())
	if e != nil || actual != digest {
		return fail(fmt.Errorf("catalog directory/manifest digest differs"))
	}
	return &CompanionCatalog{catalog, dir, path}, nil
}

// OpenCompanionCatalog is a read-only source provider for an onward-provisioning
// thin node. Multiple matching catalogs and any partial/unfamiliar entry refuse.
func OpenCompanionCatalog(home, version string, protocol int) (*CompanionCatalog, error) {
	parentPath := filepath.Join(home, "mesh-companions")
	parent, e := directory(parentPath)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	defer parent.Close()
	identity, e := catalogIdentity(parent)
	if e != nil || identity.Mode != unix.S_IFDIR|0700 {
		return nil, fmt.Errorf("catalog namespace requires exact protected mode")
	}
	names, e := parent.Readdirnames(65)
	if e != nil && e != io.EOF {
		return nil, e
	}
	if len(names) > 64 {
		return nil, fmt.Errorf("companion catalog namespace exceeds bound")
	}
	var selected *CompanionCatalog
	for _, name := range names {
		if regexp.MustCompile(`^lock-[a-f0-9]{64}$`).MatchString(name) {
			if _, e := privateFile(filepath.Join(parentPath, name)); e != nil {
				if selected != nil {
					selected.Close()
				}
				return nil, e
			}
			continue
		}
		if !regexp.MustCompile(`^catalog-[a-f0-9]{64}$`).MatchString(name) {
			if selected != nil {
				selected.Close()
			}
			return nil, fmt.Errorf("partial or unfamiliar companion catalog entry; inspect exact provisioning receipt")
		}
		candidate, e := readCompanionCatalog(filepath.Join(parentPath, name), strings.TrimPrefix(name, "catalog-"), version, protocol)
		if e != nil {
			if selected != nil {
				selected.Close()
			}
			return nil, e
		}
		if selected != nil {
			selected.Close()
			candidate.Close()
			return nil, fmt.Errorf("multiple matching companion catalogs; select an explicit artifact instead of guessing")
		}
		if e = provisionRootNamed(parent, parentPath); e != nil {
			candidate.Close()
			return nil, e
		}
		selected = candidate
	}
	return selected, nil
}
func VerifyCompanionCatalog(home string, seed meshssh.CatalogSeed, expected *CatalogIdentity) (CatalogIdentity, error) {
	path := filepath.Join(home, "mesh-companions", "catalog-"+seed.Digest)
	catalog, e := readCompanionCatalog(path, seed.Digest, seed.Manifest.Version, seed.Manifest.Protocol)
	if e != nil {
		return CatalogIdentity{}, e
	}
	defer catalog.Close()
	identity, e := catalogIdentity(catalog.directory)
	if e != nil || expected != nil && identity != *expected {
		return identity, fmt.Errorf("catalog directory identity changed")
	}
	for _, p := range seed.Manifest.Artifacts {
		stream, _, e := catalog.Catalog.Open(p.OS, p.Arch)
		if e != nil {
			return identity, e
		}
		_, e = io.Copy(io.Discard, stream)
		ce := stream.Close()
		if e != nil {
			return identity, e
		}
		if ce != nil {
			return identity, ce
		}
	}
	if e = provisionRootNamed(catalog.directory, path); e != nil {
		return identity, e
	}
	return identity, nil
}

// PublishCompanionCatalog accepts exactly four reviewed gzip frames plus EOF.
// It never replaces a catalog and preserves partial stages on any uncertainty.
func PublishCompanionCatalog(ctx context.Context, q meshssh.CatalogSeedRequest, input io.Reader, born func(CatalogIdentity) error) (CatalogIdentity, error) {
	return publishCompanionCatalog(ctx, q, input, born, nil)
}

func publishCompanionCatalog(ctx context.Context, q meshssh.CatalogSeedRequest, input io.Reader, born func(CatalogIdentity) error, runtimeCheck func() error) (CatalogIdentity, error) {
	var result CatalogIdentity
	if e := q.Validate(); e != nil {
		return result, e
	}
	if e := local.CheckStateFence(q.Home); e != nil {
		return result, e
	}
	unlock, e := LockLifecycle(q.Home)
	if e != nil {
		return result, e
	}
	defer unlock()
	if e = local.CheckStateFence(q.Home); e != nil {
		return result, e
	}
	// The native daemon proof is deliberately inside the same lifecycle fence
	// as publication; a reset/recreated home cannot inherit a prior proof.
	if runtimeCheck != nil {
		if e = runtimeCheck(); e != nil {
			return result, e
		}
	}
	home, e := directory(q.Home)
	if e != nil {
		return result, e
	}
	defer home.Close()
	if e = unix.Mkdirat(int(home.Fd()), "mesh-companions", 0700); e != nil && e != unix.EEXIST {
		return result, e
	}
	if e = home.Sync(); e != nil {
		return result, e
	}
	parentPath := filepath.Join(q.Home, "mesh-companions")
	parent, e := privateChildDirectory(home, "mesh-companions")
	if e != nil {
		return result, e
	}
	defer parent.Close()
	lockFD, e := unix.Openat(int(parent.Fd()), "lock-"+q.Catalog.Digest, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return result, e
	}
	lock := os.NewFile(uintptr(lockFD), "catalog lock")
	defer lock.Close()
	info, e := lock.Stat()
	if e != nil {
		return result, e
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || s.Uid != uint32(os.Geteuid()) || s.Nlink != 1 || s.Mode&07777 != 0600 {
		return result, fmt.Errorf("unsafe companion publication lock")
	}
	if e = unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return result, fmt.Errorf("catalog publication busy: %w", e)
	}
	defer unix.Flock(lockFD, unix.LOCK_UN)
	stageName := "stage-" + q.Transfer.Key
	stagePath := filepath.Join(parentPath, stageName)
	if e = unix.Mkdirat(int(parent.Fd()), stageName, 0700); e != nil {
		return result, fmt.Errorf("catalog stage already exists or unavailable; no resend: %w", e)
	}
	if e = parent.Sync(); e != nil {
		return result, e
	}
	stage, e := privateChildDirectory(parent, stageName)
	if e != nil {
		return result, e
	}
	defer stage.Close()
	for _, p := range q.Catalog.Manifest.Artifacts {
		if e = ctx.Err(); e != nil {
			return result, e
		}
		fd, e := unix.Openat(int(stage.Fd()), p.Name+".gz", unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
		if e != nil {
			return result, e
		}
		out := os.NewFile(uintptr(fd), p.Name)
		n, copyErr := io.CopyN(out, input, p.CompressedSize)
		syncErr := out.Sync()
		closeErr := out.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil || n != p.CompressedSize {
			return result, fmt.Errorf("catalog frame incomplete")
		}
	}
	var extra [1]byte
	n, e := input.Read(extra[:])
	if n != 0 || e != io.EOF {
		return result, fmt.Errorf("catalog frames require exact EOF with no trailing bytes")
	}
	raw, _, e := companions.Canonical(q.Catalog.Manifest)
	if e != nil {
		return result, e
	}
	if e = writePrivate(stage, "manifest.json", raw, true); e != nil {
		return result, e
	}
	if e = provisionRootNamed(home, q.Home); e != nil {
		return result, e
	}
	if e = provisionRootNamed(stage, stagePath); e != nil {
		return result, e
	}
	staged, e := readCompanionCatalog(stagePath, q.Catalog.Digest, q.Transfer.Version, q.Transfer.Protocol)
	if e != nil {
		return result, e
	}
	for _, p := range q.Catalog.Manifest.Artifacts {
		stream, _, err := staged.Catalog.Open(p.OS, p.Arch)
		if err == nil {
			_, err = io.Copy(io.Discard, stream)
			ce := stream.Close()
			if err == nil {
				err = ce
			}
		}
		if err != nil {
			staged.Close()
			return result, err
		}
	}
	staged.Close()
	if runtimeCheck != nil {
		if e = runtimeCheck(); e != nil {
			return result, e
		}
	}
	finalName := "catalog-" + q.Catalog.Digest
	// Directory publication is atomic and never replaces an existing name.
	// Persist the staged native identity before making it reader-visible.
	final, openErr := privateChildDirectory(parent, finalName)
	if openErr == nil {
		defer final.Close()
		result, e = catalogIdentity(final)
		if e != nil {
			return result, e
		}
		if _, e = VerifyCompanionCatalog(q.Home, q.Catalog, &result); e != nil {
			return result, e
		}
		if e = born(result); e != nil {
			return result, e
		}
		for _, p := range q.Catalog.Manifest.Artifacts {
			if e = unix.Unlinkat(int(stage.Fd()), p.Name+".gz", 0); e != nil {
				return result, e
			}
		}
		if e = unix.Unlinkat(int(stage.Fd()), "manifest.json", 0); e != nil {
			return result, e
		}
		if e = stage.Sync(); e != nil {
			return result, e
		}
		if e = unix.Unlinkat(int(parent.Fd()), stageName, unix.AT_REMOVEDIR); e != nil {
			return result, e
		}
	} else {
		if openErr != unix.ENOENT {
			return result, openErr
		}
		result, e = catalogIdentity(stage)
		if e != nil {
			return result, e
		}
		if e = born(result); e != nil {
			return result, e
		}
		if e = ctx.Err(); e != nil {
			return result, e
		}
		if e = provisionRootNamed(home, q.Home); e != nil {
			return result, e
		}
		if e = provisionRootNamed(parent, parentPath); e != nil {
			return result, e
		}
		if e = provisionRootNamed(stage, stagePath); e != nil {
			return result, e
		}
		if e = renameCatalogExclusive(int(parent.Fd()), stageName, finalName); e != nil {
			return result, e
		}
	}
	if e = parent.Sync(); e != nil {
		return result, e
	}
	if e = provisionRootNamed(home, q.Home); e != nil {
		return result, e
	}
	if e = provisionRootNamed(parent, parentPath); e != nil {
		return result, e
	}
	if _, e = VerifyCompanionCatalog(q.Home, q.Catalog, &result); e != nil {
		return result, e
	}
	return result, nil
}
