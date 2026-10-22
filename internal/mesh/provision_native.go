package mesh

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/yrashk/arm/internal/buildinfo"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/meshssh"
	"golang.org/x/sys/unix"
)

// ProvisionReceipt is target-side publication/start evidence. It is not peer
// trust and never grants remote execution. Unknown intent remains inspectable.
type ProvisionReceipt struct {
	ReusedBinary bool             `json:"reused_binary,omitempty"`
	Runtime      *RuntimeProof    `json:"runtime,omitempty"`
	Schema       int              `json:"schema"`
	Request      meshssh.Transfer `json:"request"`
	Actor        local.Identity   `json:"actor"`
	Phase        string           `json:"phase"`
	Binary       string           `json:"binary"`
	Version      string           `json:"actual_arm_version"`
	Protocol     int              `json:"actual_lifecycle_protocol"`
	Updated      time.Time        `json:"updated_at"`
	StartedHost  string           `json:"started_host,omitempty"`
	StartHome    string           `json:"start_home,omitempty"`
	Startup      *StartupPolicy   `json:"startup,omitempty"`
}

func provisionPaths(q meshssh.Transfer) (string, string, string) {
	return filepath.Join(q.Root, "stage-"+q.Key), filepath.Join(q.Root, "artifact-"+q.SHA256), filepath.Join(q.Root, "receipt-"+q.Key+".json")
}
func checkProvisionRequest(q meshssh.Transfer) error {
	if e := q.Validate(); e != nil {
		return e
	}
	if q.ExpectedUID != strconv.Itoa(os.Geteuid()) || q.OS != runtime.GOOS || q.Arch != runtime.GOARCH || q.Version != buildinfo.Version || q.Protocol != LifecycleProtocol {
		return fmt.Errorf("provisioned artifact actual account/platform/build/protocol differs from reviewed descriptor")
	}
	return nil
}
func provisionLock(q meshssh.Transfer) (*os.File, func(), error) {
	if e := checkProvisionRequest(q); e != nil {
		return nil, nil, e
	}
	root, e := directory(q.Root)
	if e != nil {
		return nil, nil, e
	}
	fd, e := unix.Openat(int(root.Fd()), ".provision.lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		root.Close()
		return nil, nil, e
	}
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Mode&0077 != 0 {
		unix.Close(fd)
		root.Close()
		return nil, nil, fmt.Errorf("unsafe provisioning lock")
	}
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		unix.Close(fd)
		root.Close()
		return nil, nil, fmt.Errorf("provisioning target busy: %w", e)
	}
	return root, func() { unix.Flock(fd, unix.LOCK_UN); unix.Close(fd); root.Close() }, nil
}
func verifyProvisionBinary(dir *os.File, name string, q meshssh.Transfer) (*os.File, error) {
	fd, e := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	info, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	var st unix.Stat_t
	e = unix.Fstat(fd, &st)
	if e != nil || !info.Mode().IsRegular() || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || info.Mode().Perm() != 0700 || info.Size() != q.Size {
		f.Close()
		return nil, fmt.Errorf("unsafe provisioned binary identity/mode/size")
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, MaxArtifactBytes+1))
	after, se := f.Stat()
	if e != nil || se != nil || n != q.Size || hex.EncodeToString(h.Sum(nil)) != q.SHA256 || !sameArtifactStat(info, after) {
		f.Close()
		return nil, fmt.Errorf("provisioned binary digest/identity changed")
	}
	var current unix.Stat_t
	if e = unix.Fstatat(int(dir.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW); e != nil || current.Dev != st.Dev || current.Ino != st.Ino || current.Uid != st.Uid || current.Nlink != st.Nlink || current.Mode != st.Mode || current.Size != st.Size {
		f.Close()
		return nil, fmt.Errorf("provisioned binary path changed")
	}
	return f, nil
}
func readProvision(q meshssh.Transfer) (ProvisionReceipt, error) {
	var r ProvisionReceipt
	_, _, path := provisionPaths(q)
	raw, e := privateFile(path)
	if e != nil {
		return r, e
	}
	if e = Decode(raw, &r); e != nil {
		return r, e
	}
	if r.Schema != 1 || r.Request != q || r.Actor.UID != q.ExpectedUID || r.Version != buildinfo.Version || r.Protocol != LifecycleProtocol || r.Binary != filepath.Join(q.Root, "artifact-"+q.SHA256, "arm") {
		return r, fmt.Errorf("provision receipt differs from exact reviewed request")
	}
	return r, nil
}
func provisionRootNamed(root *os.File, path string) error {
	named, e := directory(path)
	if e != nil {
		return e
	}
	defer named.Close()
	a, e := root.Stat()
	if e != nil {
		return e
	}
	b, e := named.Stat()
	if e != nil || !os.SameFile(a, b) {
		return fmt.Errorf("provision root identity changed")
	}
	return nil
}
func saveProvision(q meshssh.Transfer, r *ProvisionReceipt) error {
	r.Updated = time.Now().UTC()
	_, _, path := provisionPaths(q)
	return save(path, r)
}

// FinalizeProvision runs only after the fixed SSH script verifies the staged
// artifact SHA256. It independently verifies the executing file and publishes
// without replacement, syncing bytes and parent directories before receipt ACK.
func FinalizeProvision(q meshssh.Transfer) (ProvisionReceipt, error) {
	var r ProvisionReceipt
	root, unlock, e := provisionLock(q)
	if e != nil {
		return r, e
	}
	defer unlock()
	stagePath, finalPath, _ := provisionPaths(q)
	if prior, e := readProvision(q); e == nil {
		return prior, fmt.Errorf("provision receipt already exists; inspect exact outcome instead of publishing again")
	} else if !os.IsNotExist(e) {
		return r, e
	}
	self, e := os.Executable()
	if e != nil || self != filepath.Join(stagePath, "arm") {
		return r, fmt.Errorf("finalizer must execute the exact reviewed private stage")
	}
	stage, e := directory(stagePath)
	if e != nil {
		return r, e
	}
	defer stage.Close()
	binary, e := verifyProvisionBinary(stage, "arm", q)
	if e != nil {
		return r, e
	}
	if e = binary.Sync(); e != nil {
		binary.Close()
		return r, e
	}
	binary.Close()
	r = ProvisionReceipt{Schema: 1, Request: q, Actor: local.CurrentIdentity(), Phase: "publish_intent", Binary: filepath.Join(finalPath, "arm"), Version: buildinfo.Version, Protocol: LifecycleProtocol}
	if e = saveProvision(q, &r); e != nil {
		return r, e
	}
	fresh := true
	if e = unix.Mkdirat(int(root.Fd()), filepath.Base(finalPath), 0700); e == unix.EEXIST {
		fresh = false
	} else if e != nil {
		return r, e
	}
	if e = root.Sync(); e != nil {
		return r, e
	}
	final, e := directory(finalPath)
	if e != nil {
		return r, e
	}
	defer final.Close()
	if fresh {
		if e = unix.Linkat(int(stage.Fd()), "arm", int(final.Fd()), "arm", 0); e != nil {
			return r, e
		}
		if e = final.Sync(); e != nil {
			return r, e
		}
	} else {
		existing, e := verifyProvisionBinary(final, "arm", q)
		if e != nil {
			return r, e
		}
		existing.Close()
		r.ReusedBinary = true
	}
	if e = unix.Unlinkat(int(stage.Fd()), "arm", 0); e != nil {
		return r, e
	}
	if e = stage.Sync(); e != nil {
		return r, e
	}
	check, e := verifyProvisionBinary(final, "arm", q)
	if e != nil {
		return r, e
	}
	check.Close()
	if e = provisionRootNamed(root, q.Root); e != nil {
		return r, e
	}
	r.Phase = "published_verified"
	if e = saveProvision(q, &r); e != nil {
		return r, e
	}
	return r, nil
}
func LookupProvision(q meshssh.Transfer) (ProvisionReceipt, error) {
	var r ProvisionReceipt
	if e := checkProvisionRequest(q); e != nil {
		return r, e
	}
	r, e := readProvision(q)
	if e != nil {
		return r, e
	}
	_, finalPath, _ := provisionPaths(q)
	dir, e := directory(finalPath)
	if e != nil {
		return r, e
	}
	defer dir.Close()
	f, e := verifyProvisionBinary(dir, "arm", q)
	if e != nil {
		return r, e
	}
	f.Close()
	return r, nil
}

// BeginProvisionStart durably fences one backend start. A prior start intent is
// NEVER resent; lookup may verify a started receipt but absence is not proof.
func BeginProvisionStart(q meshssh.Transfer, home string, startup StartupPolicy) (ProvisionReceipt, error) {
	var r ProvisionReceipt
	_, unlock, e := provisionLock(q)
	if e != nil {
		return r, e
	}
	defer unlock()
	r, e = LookupProvision(q)
	if e != nil {
		return r, e
	}
	if r.Phase != "published_verified" {
		return r, fmt.Errorf("start already attempted or unsettled; inspect exact provision receipt; no start was resent")
	}
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return r, fmt.Errorf("exact canonical backend home required")
	}
	if e = startup.Validate(); e != nil {
		return r, e
	}
	r.Phase = "start_intent"
	r.StartHome = home
	r.Startup = &startup
	return r, saveProvision(q, &r)
}
func FinishProvisionStart(q meshssh.Transfer, proof RuntimeProof, home string, startup StartupPolicy) (ProvisionReceipt, error) {
	var r ProvisionReceipt
	_, unlock, e := provisionLock(q)
	if e != nil {
		return r, e
	}
	defer unlock()
	r, e = LookupProvision(q)
	if e != nil {
		return r, e
	}
	if r.Phase != "start_intent" || r.StartHome != home || r.Startup == nil || *r.Startup != startup || len(proof.Host) != 32 || proof.SHA256 != q.SHA256 || proof.Size != q.Size || proof.UID != q.ExpectedUID || proof.Version != q.Version || proof.Protocol != q.Protocol || proof.Captured.IsZero() || (proof.Method != "kernel-self-executable" && proof.Method != "startup-open-file-snapshot") {
		return r, fmt.Errorf("backend start differs from durable exact intent")
	}
	r.Phase = "started_verified"
	r.StartedHost = proof.Host
	r.Runtime = &proof
	return r, saveProvision(q, &r)
}
