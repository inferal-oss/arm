package mesh

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/inferal-oss/arm/internal/meshssh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/storage"
	"golang.org/x/sys/unix"
)

// InstallOwnership is captured by publication, never inferred later from a
// matching path/hash. References are monotonic; historical sharing is preserved.
type InstallOwnership struct {
	Schema   int                `json:"schema"`
	Transfer meshssh.Transfer   `json:"transfer"`
	Root     model.FileIdentity `json:"root_identity"`
	Artifact model.FileIdentity `json:"artifact_identity"`
	Binary   model.FileIdentity `json:"binary_identity"`
	Homes    []string           `json:"referenced_homes"`
	Removing string             `json:"uninstall_key,omitempty"`
}

func installIdentity(f *os.File) (model.FileIdentity, error) {
	return storage.DirectoryIdentity(int(f.Fd()))
}
func readInstallOwnership(dir *os.File) (out InstallOwnership, err error) {
	fd, err := unix.Openat(int(dir.Fd()), "ownership.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return out, err
	}
	file := os.NewFile(uintptr(fd), "managed-ownership")
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return out, err
	}
	raw, err := readPrivate(file, info)
	if err != nil {
		return out, err
	}
	if err = Decode(raw, &out); err != nil {
		return out, err
	}
	if out.Schema != 1 || out.Transfer.Validate() != nil || !out.Root.BirthKnown || !out.Artifact.BirthKnown || !out.Binary.BirthKnown || len(out.Homes) > 64 || (out.Removing != "" && !ValidKey(out.Removing)) {
		return out, fmt.Errorf("managed ownership is missing or invalid")
	}
	seen := map[string]bool{}
	for _, home := range out.Homes {
		if !meshssh.ValidStateHome(home) || seen[home] {
			return out, fmt.Errorf("invalid managed home reference history")
		}
		seen[home] = true
	}
	return out, nil
}
func saveInstallOwnership(dir *os.File, proof InstallOwnership, exclusive bool) error {
	raw, e := json.MarshalIndent(proof, "", "  ")
	if e != nil {
		return e
	}
	return writePrivate(dir, "ownership.json", append(raw, '\n'), exclusive)
}
func captureInstallOwnership(root, artifact *os.File, q meshssh.Transfer) (*InstallOwnership, error) {
	binary, e := verifyProvisionBinary(artifact, "arm", q)
	if e != nil {
		return nil, e
	}
	defer binary.Close()
	p := &InstallOwnership{Schema: 1, Transfer: q, Homes: []string{}}
	if p.Root, e = installIdentity(root); e == nil {
		p.Artifact, e = installIdentity(artifact)
	}
	if e == nil {
		p.Binary, e = installIdentity(binary)
	}
	if e != nil {
		return nil, e
	}
	if e = saveInstallOwnership(artifact, *p, true); e != nil {
		return nil, e
	}
	return p, nil
}
func verifyInstallOwnership(root, artifact *os.File, q meshssh.Transfer, expected *InstallOwnership) (InstallOwnership, error) {
	var empty InstallOwnership
	if expected == nil || expected.Schema != 1 || expected.Transfer != q {
		return empty, fmt.Errorf("publication has no original ownership proof; files preserved")
	}
	current, e := readInstallOwnership(artifact)
	if e != nil || current.Transfer != q || current.Root != expected.Root || current.Artifact != expected.Artifact || current.Binary != expected.Binary {
		return empty, fmt.Errorf("original managed ownership changed or cannot be established")
	}
	if e = provisionRootNamed(root, q.Root); e != nil {
		return empty, e
	}
	named, e := privateChildDirectory(root, "artifact-"+q.SHA256)
	if e != nil {
		return empty, e
	}
	namedID, namedErr := installIdentity(named)
	named.Close()
	if namedErr != nil || namedID != current.Artifact {
		return empty, fmt.Errorf("original artifact current name was replaced or moved")
	}
	id, e := installIdentity(root)
	if e != nil || id != current.Root {
		return empty, fmt.Errorf("managed install root replaced")
	}
	id, e = installIdentity(artifact)
	if e != nil || id != current.Artifact {
		return empty, fmt.Errorf("managed artifact directory replaced")
	}
	f, e := verifyProvisionBinary(artifact, "arm", q)
	if e != nil {
		return empty, e
	}
	defer f.Close()
	id, e = installIdentity(f)
	if e != nil || id != current.Binary {
		return empty, fmt.Errorf("managed executable replaced even if bytes match")
	}
	return current, nil
}

// RegisterManagedExecutable records every ordinary Serve using newly published
// bytes. Unknown/legacy artifacts are not adopted and remain uninstall-ineligible.
func RegisterManagedExecutable(home string) error {
	path, e := os.Executable()
	if e != nil {
		return e
	}
	return registerManagedExecutable(path, home)
}
func registerManagedExecutable(path, home string) error {
	artifactPath := filepath.Dir(path)
	if filepath.Base(path) != "arm" || !regexp.MustCompile(`^artifact-[a-f0-9]{64}$`).MatchString(filepath.Base(artifactPath)) {
		return nil
	}
	artifact, e := directory(artifactPath)
	if e != nil {
		return e
	}
	proof, e := readInstallOwnership(artifact)
	artifact.Close()
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if path != filepath.Join(proof.Transfer.Root, "artifact-"+proof.Transfer.SHA256, "arm") || !meshssh.ValidStateHome(home) {
		return fmt.Errorf("managed executable/home differs from publication")
	}
	root, unlock, e := provisionExistingLock(proof.Transfer)
	if e != nil {
		return e
	}
	defer unlock()
	artifact, e = privateChildDirectory(root, filepath.Base(artifactPath))
	if e != nil {
		return e
	}
	defer artifact.Close()
	p, e := verifyInstallOwnership(root, artifact, proof.Transfer, &proof)
	if e != nil {
		return e
	}
	if p.Removing != "" {
		return fmt.Errorf("unresolved managed uninstall prevents ordinary startup")
	}
	for _, known := range p.Homes {
		if known == home {
			return nil
		}
	}
	if len(p.Homes) >= 64 {
		return fmt.Errorf("managed home reference history full")
	}
	p.Homes = append(p.Homes, home)
	return saveInstallOwnership(artifact, p, false)
}

// exclusiveInstall never presumes another referenced home is stopped. Reused,
// replaced, shared, unknown and extra namespace members conservatively refuse.
func exclusiveInstall(root *os.File, r ProvisionReceipt, home string) (*os.File, InstallOwnership, error) {
	var p InstallOwnership
	if r.ReusedBinary || r.Ownership == nil || r.StartHome != home || r.StartedHost == "" || (r.Phase != "started_verified" && r.Phase != "seeded_verified") {
		return nil, p, fmt.Errorf("installation reused, incomplete or lacks ownership proof; preserved")
	}
	dir, e := privateChildDirectory(root, "artifact-"+r.Request.SHA256)
	if e != nil {
		return nil, p, e
	}
	fail := func(e error) (*os.File, InstallOwnership, error) { dir.Close(); return nil, p, e }
	p, e = verifyInstallOwnership(root, dir, r.Request, r.Ownership)
	if e != nil {
		return fail(e)
	}
	if len(p.Homes) != 1 || p.Homes[0] != home {
		return fail(fmt.Errorf("missing or other-home executable references; preserved"))
	}
	names, e := dir.Readdirnames(4)
	if (e != nil && e != io.EOF) || len(names) != 2 {
		return fail(fmt.Errorf("unexpected artifact members; preserved"))
	}
	for _, n := range names {
		if n != "arm" && n != "ownership.json" {
			return fail(fmt.Errorf("unowned artifact member; preserved"))
		}
	}
	entries, e := installRootEntries(root, r.Request.Root)
	if e != nil || len(entries) > 512 {
		return fail(fmt.Errorf("install receipt roster unavailable or exceeds bound"))
	}
	for _, entry := range entries {
		if !regexp.MustCompile(`^receipt-[a-f0-9]{64}\.json$`).MatchString(entry.Name()) {
			continue
		}
		raw, e := installPrivateAt(root, entry.Name())
		var other ProvisionReceipt
		if e != nil || Decode(raw, &other) != nil || validateInstallReference(other, r.Request.Root, entry.Name()) != nil {
			return fail(fmt.Errorf("corrupt retained publication; cannot exclude sharing"))
		}
		if other.Binary == r.Binary && other.Request != r.Request {
			return fail(fmt.Errorf("another publication references these bytes; preserved"))
		}
	}
	return dir, p, nil
}

// A new openat handle has an independent directory offset; at most513 entries
// are allocated before refusing. The named root is rechecked on both sides.
func installRootEntries(root *os.File, path string) ([]os.DirEntry, error) {
	if e := provisionRootNamed(root, path); e != nil {
		return nil, e
	}
	fd, e := unix.Openat(int(root.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	entries, e := f.ReadDir(513)
	if e != nil && e != io.EOF {
		return nil, e
	}
	if len(entries) > 512 {
		return nil, fmt.Errorf("install receipt roster exceeds512 entries")
	}
	if e = provisionRootNamed(root, path); e != nil {
		return nil, e
	}
	return entries, nil
}
func installPrivateAt(dir *os.File, name string) ([]byte, error) {
	fd, e := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	return readPrivate(f, info)
}
func validateInstallReference(r ProvisionReceipt, root, name string) error {
	q := r.Request
	if q.Validate() != nil || q.Root != root || name != "receipt-"+q.Key+".json" || r.Schema != 1 || r.Actor.UID != q.ExpectedUID || q.ExpectedUID != fmt.Sprint(os.Geteuid()) || r.Version != q.Version || r.Protocol != q.Protocol || r.Binary != UninstallInstalledPath(q) || r.Updated.IsZero() {
		return fmt.Errorf("invalid publication reference envelope")
	}
	if r.Ownership != nil {
		p := r.Ownership
		if r.ReusedBinary || p.Schema != 1 || p.Transfer != q || !p.Root.BirthKnown || !p.Artifact.BirthKnown || !p.Binary.BirthKnown || len(p.Homes) != 0 || p.Removing != "" {
			return fmt.Errorf("invalid original publication ownership")
		}
	}
	switch r.Phase {
	case "publish_intent", "published_verified":
		if r.StartHome != "" || r.Startup != nil || r.Runtime != nil || r.StartedHost != "" || r.CatalogSeed != nil {
			return fmt.Errorf("publication has unexplained startup evidence")
		}
		return nil
	case "start_intent", "started_verified", "seed_intent", "seeded_verified":
	default:
		return fmt.Errorf("unknown publication reference phase")
	}
	if !meshssh.ValidStateHome(r.StartHome) || r.Startup == nil || r.Startup.Validate() != nil {
		return fmt.Errorf("publication startup policy/home invalid")
	}
	if r.Phase == "start_intent" {
		if r.Runtime != nil || r.StartedHost != "" || r.CatalogSeed != nil {
			return fmt.Errorf("start intent has unexplained completion")
		}
		return nil
	}
	p := r.Runtime
	if p == nil || !validID(r.StartedHost) || p.Host != r.StartedHost || p.UID != q.ExpectedUID || p.SHA256 != q.SHA256 || p.Size != q.Size || p.Version != q.Version || p.Protocol != q.Protocol || p.Captured.IsZero() || (p.Method != "kernel-self-executable" && p.Method != "startup-open-file-snapshot") {
		return fmt.Errorf("publication runtime evidence invalid")
	}
	if r.Phase == "started_verified" {
		if r.CatalogSeed != nil {
			return fmt.Errorf("unexpected seed evidence")
		}
		return nil
	}
	seed := r.CatalogSeed
	if seed == nil || seed.Request.Validate() != nil || seed.Request.Transfer != q || seed.Request.Home != r.StartHome || seed.Request.Host != r.StartedHost || seed.Destination != filepath.Join(r.StartHome, "mesh-companions", "catalog-"+seed.Request.Catalog.Digest) {
		return fmt.Errorf("publication catalog reference invalid")
	}
	if r.Phase == "seeded_verified" && seed.Identity == nil {
		return fmt.Errorf("verified seed lacks identity")
	}
	return nil
}
