package mesh

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/inferal-oss/arm/internal/local"
	"golang.org/x/sys/unix"
)

// ImportCompanion copies a reviewed stream only after caller consent. Metadata
// lookup never calls this function. Foreign bytes are inspected, never executed.
func ImportCompanion(home string, a Artifact, input io.Reader) (Artifact, error) {
	if e := a.Validate(); e != nil {
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
	manifest := filepath.Join(home, "mesh-artifacts", "artifact-"+a.SHA256+".json")
	if raw, e := privateFile(manifest); e == nil {
		var old Artifact
		if e = Decode(raw, &old); e != nil {
			return a, e
		}
		if old.Validate() != nil || old.SHA256 != a.SHA256 || old.Size != a.Size || old.OS != a.OS || old.Arch != a.Arch || old.Version != a.Version || old.Protocol != a.Protocol {
			return a, fmt.Errorf("existing artifact descriptor differs")
		}
		file, e := OpenArtifact(home, old)
		if e != nil {
			return a, e
		}
		file.Close()
		return old, nil
	} else if !os.IsNotExist(e) {
		return a, e
	}
	names, e := dir.Readdirnames(65)
	if e != nil && e != io.EOF {
		return a, e
	}
	if len(names) >= 64 {
		return a, fmt.Errorf("private artifact catalog full; no entry removed")
	}
	name := ".import-" + local.ID()
	fd, e := unix.Openat(int(dir.Fd()), name, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return a, e
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	defer unix.Unlinkat(int(dir.Fd()), name, 0)
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(file, hash), io.LimitReader(input, a.Size+1))
	if e != nil || n != a.Size || hex.EncodeToString(hash.Sum(nil)) != a.SHA256 {
		return a, fmt.Errorf("companion stream differs from reviewed executable")
	}
	info, e := buildinfo.Read(file)
	if e != nil || info.Path != "github.com/inferal-oss/arm/cmd/arm" {
		return a, fmt.Errorf("companion is not an ARM Go executable")
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	if settings["GOOS"] != a.OS || settings["GOARCH"] != a.Arch || settings["CGO_ENABLED"] != "0" {
		return a, fmt.Errorf("companion actual Go platform/CGo metadata differs")
	}
	if e = file.Chmod(0700); e != nil {
		return a, e
	}
	if e = file.Sync(); e != nil {
		return a, e
	}
	if e = unix.Linkat(int(dir.Fd()), name, int(dir.Fd()), "binary-"+a.SHA256, 0); e != nil && e != unix.EEXIST {
		return a, e
	}
	if e = unix.Unlinkat(int(dir.Fd()), name, 0); e != nil {
		return a, e
	}
	if e = dir.Sync(); e != nil {
		return a, e
	}
	verified, e := OpenArtifact(home, a)
	if e != nil {
		return a, e
	}
	verified.Close()
	return a, save(manifest, a)
}
