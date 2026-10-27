package mesh

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/inferal-oss/arm/internal/buildinfo"
)

// RuntimeProof is native-account-only evidence for installation/startup. It is
// not node metadata, a peer permission, or authority to signal a PID.
type RuntimeProof struct {
	Host     string    `json:"host"`
	UID      string    `json:"uid"`
	SHA256   string    `json:"sha256"`
	Size     int64     `json:"size_bytes"`
	Version  string    `json:"arm_version"`
	Protocol int       `json:"lifecycle_protocol"`
	Method   string    `json:"method"`
	Captured time.Time `json:"captured_at"`
}

// RuntimeExecutable pins the startup file before publishing a native endpoint.
// Linux opens the kernel's actual image. Darwin retains a startup file snapshot;
// every later proof rechecks the open native inode AND its public pathname.
// A replaced/changed executable never inherits the old daemon's proof.
type RuntimeExecutable struct {
	file   *os.File
	path   string
	before os.FileInfo
	proof  RuntimeProof
}

func CaptureRuntimeExecutable() (*RuntimeExecutable, error) {
	path, e := os.Executable()
	if e != nil {
		return nil, e
	}
	openPath := path
	method := "startup-open-file-snapshot"
	if runtime.GOOS == "linux" {
		openPath = "/proc/self/exe"
		method = "kernel-self-executable"
	}
	f, e := os.Open(openPath)
	if e != nil {
		return nil, e
	}
	fail := func(e error) (*RuntimeExecutable, error) { f.Close(); return nil, e }
	before, e := f.Stat()
	if e != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0022 != 0 || before.Size() < 1 || before.Size() > MaxArtifactBytes {
		return fail(fmt.Errorf("runtime executable unavailable, writable by another account or exceeds provisioning bound"))
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, MaxArtifactBytes+1))
	if e != nil || n != before.Size() {
		return fail(fmt.Errorf("runtime executable read failed or changed size"))
	}
	r := &RuntimeExecutable{file: f, path: path, before: before, proof: RuntimeProof{SHA256: hex.EncodeToString(h.Sum(nil)), Size: n, Version: buildinfo.Version, Protocol: LifecycleProtocol, Method: method, Captured: time.Now().UTC()}}
	if _, e = r.Proof(); e != nil {
		return fail(e)
	}
	return r, nil
}
func (r *RuntimeExecutable) Proof() (RuntimeProof, error) {
	if r == nil || r.file == nil {
		return RuntimeProof{}, fmt.Errorf("startup executable image was not verified")
	}
	after, e := r.file.Stat()
	if e != nil || !sameArtifactStat(r.before, after) {
		return RuntimeProof{}, fmt.Errorf("startup executable identity changed")
	}
	named, e := os.Stat(r.path)
	if e != nil || !sameArtifactStat(r.before, named) {
		return RuntimeProof{}, fmt.Errorf("running artifact pathname was replaced/changed; startup proof unavailable")
	}
	return r.proof, nil
}
func (r *RuntimeExecutable) Close() error {
	if r == nil || r.file == nil {
		return nil
	}
	return r.file.Close()
}

// VerifiedPath is internal launch authority derived from the startup image.
// It is never a native/browser/peer request parameter or a public RPC result.
func (r *RuntimeExecutable) VerifiedPath() (string, error) {
	if _, e := r.Proof(); e != nil {
		return "", e
	}
	return r.path, nil
}
