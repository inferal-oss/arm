package mesh

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	armBuild "getarm.org/internal/buildinfo"
	"getarm.org/internal/local"
	"golang.org/x/sys/unix"
)

const MaxArtifactBytes int64 = 256 << 20

// Artifact names trusted LOCAL executable bytes, not a network download source.
// Version is a declared expectation; the verified target finalizer must report
// its actual compiled version/protocol before it may start a node.
type Artifact struct {
	Schema   int            `json:"schema"`
	SHA256   string         `json:"sha256"`
	Size     int64          `json:"size_bytes"`
	OS       string         `json:"os"`
	Arch     string         `json:"arch"`
	Version  string         `json:"expected_arm_version"`
	Protocol int            `json:"expected_lifecycle_protocol"`
	Imported time.Time      `json:"imported_at"`
	Actor    local.Identity `json:"actor"`
	Reason   string         `json:"reason"`
}

func (a Artifact) Validate() error {
	digest, e := hex.DecodeString(a.SHA256)
	if a.Schema != 1 || e != nil || len(digest) != 32 || len(a.SHA256) != 64 || a.SHA256 != strings.ToLower(a.SHA256) || a.Size < 1 || a.Size > MaxArtifactBytes || (a.OS != "linux" && a.OS != "darwin") || (a.Arch != "arm64" && a.Arch != "amd64") || a.Version != armBuild.Version || a.Protocol != LifecycleProtocol || strings.TrimSpace(a.Reason) == "" || len(a.Reason) > 4096 {
		return fmt.Errorf("invalid trusted local ARM artifact")
	}
	return nil
}
func sourceArtifact(path string) (*os.File, os.FileInfo, error) {
	parent, e := filepath.EvalSymlinks(filepath.Dir(path))
	if e != nil {
		return nil, nil, e
	}
	fd, e := unix.Open(filepath.Join(parent, filepath.Base(path)), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	info, e := f.Stat()
	if e == nil {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || info.Size() < 1 || info.Size() > MaxArtifactBytes {
			e = fmt.Errorf("artifact source must be a bounded user-owned single-link regular executable")
		}
	}
	if e != nil {
		f.Close()
		return nil, nil, e
	}
	return f, info, nil
}

// InspectArtifact is read-only, including for a public-mode project build path.
func InspectArtifact(path, reason string) (Artifact, error) {
	var a Artifact
	f, before, e := sourceArtifact(path)
	if e != nil {
		return a, e
	}
	defer f.Close()
	info, e := buildinfo.Read(f)
	if e != nil || info.Path != "getarm.org/cmd/arm" {
		return a, fmt.Errorf("artifact is not an ARM Go executable")
	}
	a = Artifact{Schema: 1, Version: armBuild.Version, Protocol: LifecycleProtocol, Size: before.Size(), Imported: time.Now().UTC(), Actor: local.CurrentIdentity(), Reason: reason}
	cgo := ""
	for _, s := range info.Settings {
		switch s.Key {
		case "GOOS":
			a.OS = s.Value
		case "GOARCH":
			a.Arch = s.Value
		case "CGO_ENABLED":
			cgo = s.Value
		}
	}
	if cgo != "0" {
		return a, fmt.Errorf("provisioning requires a CGo-free companion artifact")
	}
	hash := sha256.New()
	n, e := io.Copy(hash, io.LimitReader(f, MaxArtifactBytes+1))
	if e != nil || n != before.Size() {
		return a, fmt.Errorf("artifact read changed or exceeded bound")
	}
	a.SHA256 = hex.EncodeToString(hash.Sum(nil))
	after, e := f.Stat()
	if e != nil || !sameArtifactStat(before, after) {
		return a, fmt.Errorf("artifact source changed while hashing")
	}
	return a, a.Validate()
}
func sameArtifactStat(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return false
	}
	x, xok := a.Sys().(*syscall.Stat_t)
	y, yok := b.Sys().(*syscall.Stat_t)
	return xok && yok && os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime() == b.ModTime() && x.Uid == y.Uid && x.Nlink == y.Nlink
}
func artifactDirectory(home string, create bool) (*os.File, error) {
	if create {
		if e := local.Prepare(home); e != nil {
			return nil, e
		}
		parent, e := directory(home)
		if e != nil {
			return nil, e
		}
		e = unix.Mkdirat(int(parent.Fd()), "mesh-artifacts", 0700)
		if e != nil && e != unix.EEXIST {
			parent.Close()
			return nil, e
		}
		if e = parent.Sync(); e != nil {
			parent.Close()
			return nil, e
		}
		parent.Close()
	}
	return directory(filepath.Join(home, "mesh-artifacts"))
}
func ArtifactPath(home string, a Artifact) string {
	return filepath.Join(home, "mesh-artifacts", "binary-"+a.SHA256)
}

// ImportArtifact copies verified bytes into ARM's private immutable catalog. It
// does not chmod/move the user's ordinary project build or execute the artifact.
func ImportArtifact(home, path, reason string) (Artifact, error) {
	a, e := InspectArtifact(path, reason)
	if e != nil {
		return a, e
	}
	if e := local.Prepare(home); e != nil {
		return a, e
	}
	unlock, e := LockLifecycle(home)
	if e != nil {
		return a, e
	}
	defer unlock()
	if e = local.CheckStateFence(home); e != nil {
		return a, e
	}
	dir, e := artifactDirectory(home, true)
	if e != nil {
		return a, e
	}
	defer dir.Close()
	entries, e := dir.ReadDir(65)
	if e != nil && e != io.EOF {
		return a, e
	}
	if len(entries) >= 64 {
		return a, fmt.Errorf("artifact catalog full; no entry removed")
	}
	f, before, e := sourceArtifact(path)
	if e != nil {
		return a, e
	}
	defer f.Close()
	tmp := ".import-" + local.ID()
	fd, e := unix.Openat(int(dir.Fd()), tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0700)
	if e != nil {
		return a, e
	}
	out := os.NewFile(uintptr(fd), tmp)
	defer func() { out.Close(); unix.Unlinkat(int(dir.Fd()), tmp, 0) }()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(out, h), io.LimitReader(f, MaxArtifactBytes+1))
	after, se := f.Stat()
	if e != nil || se != nil || n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 || !sameArtifactStat(before, after) {
		return a, fmt.Errorf("artifact changed during private import")
	}
	if e = out.Sync(); e != nil {
		return a, e
	}
	if e = out.Close(); e != nil {
		return a, e
	}
	name := "binary-" + a.SHA256
	e = unix.Linkat(int(dir.Fd()), tmp, int(dir.Fd()), name, 0)
	if e != nil && e != unix.EEXIST {
		return a, e
	}
	if e == nil {
		if e = unix.Unlinkat(int(dir.Fd()), tmp, 0); e != nil {
			return a, e
		}
		if e = dir.Sync(); e != nil {
			return a, e
		}
	}
	// Recheck the destination even on exact repeated import; never trust filename.
	verified, e := OpenArtifact(home, a)
	if e != nil {
		return a, e
	}
	verified.Close()
	manifest := filepath.Join(home, "mesh-artifacts", "artifact-"+a.SHA256+".json")
	if old, e := privateFile(manifest); e == nil {
		var existing Artifact
		if e = Decode(old, &existing); e != nil {
			return a, e
		}
		if existing.SHA256 != a.SHA256 || existing.Size != a.Size || existing.OS != a.OS || existing.Arch != a.Arch || existing.Version != a.Version || existing.Protocol != a.Protocol {
			return a, fmt.Errorf("artifact catalog conflict")
		}
		return existing, existing.Validate()
	} else if !os.IsNotExist(e) {
		return a, e
	}
	return a, save(manifest, a)
}
func OpenArtifact(home string, a Artifact) (*os.File, error) {
	if e := a.Validate(); e != nil {
		return nil, e
	}
	catalog, e := artifactDirectory(home, false)
	if e != nil {
		return nil, e
	}
	defer catalog.Close()
	fd, e := unix.Openat(int(catalog.Fd()), "binary-"+a.SHA256, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), "catalog artifact")
	info, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		f.Close()
		return nil, fmt.Errorf("catalog artifact must be a private owned single-link regular file")
	}
	if info.Mode().Perm() != 0700 {
		f.Close()
		return nil, fmt.Errorf("catalog artifact must be private executable")
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, MaxArtifactBytes+1))
	after, se := f.Stat()
	if e != nil || se != nil || n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 || !sameArtifactStat(info, after) {
		f.Close()
		return nil, fmt.Errorf("catalog artifact identity/digest changed")
	}
	// Prove the canonical catalog still identifies this opened directory after
	// hashing; an old renamed directory must not silently substitute its name.
	current, e := artifactDirectory(home, false)
	if e != nil {
		f.Close()
		return nil, e
	}
	beforeDir, be := catalog.Stat()
	afterDir, ae := current.Stat()
	current.Close()
	if be != nil || ae != nil || !os.SameFile(beforeDir, afterDir) {
		f.Close()
		return nil, fmt.Errorf("artifact catalog namespace changed")
	}
	_, e = f.Seek(0, 0)
	if e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
func Artifacts(home string) ([]Artifact, error) {
	dir, e := artifactDirectory(home, false)
	if os.IsNotExist(e) {
		return []Artifact{}, nil
	}
	if e != nil {
		return nil, e
	}
	defer dir.Close()
	entries, e := dir.ReadDir(65)
	if e != nil && e != io.EOF {
		return nil, e
	}
	if len(entries) > 64 {
		return nil, fmt.Errorf("artifact catalog exceeds bound")
	}
	out := []Artifact{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "artifact-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, e := privateFile(filepath.Join(home, "mesh-artifacts", entry.Name()))
		if e != nil {
			return nil, e
		}
		var a Artifact
		if e = Decode(raw, &a); e != nil {
			return nil, e
		}
		if e = a.Validate(); e != nil {
			return nil, e
		}
		if entry.Name() != "artifact-"+a.SHA256+".json" {
			return nil, fmt.Errorf("artifact manifest identity mismatch")
		}
		out = append(out, a)
	}
	return out, nil
}
