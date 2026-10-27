package providerexec

import (
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf8"

	"getarm.org/internal/model"
	"getarm.org/internal/storage"
)

type Image struct {
	path, sha string
	size      int64
	identity  model.FileIdentity
	stamp     fileStamp
	ancestors []directoryWitness
	aclSHA    string
	valid     bool
}
type fileStamp struct {
	Device, Inode                            uint64
	Mode                                     uint32
	UID                                      uint32
	Links                                    uint64
	Size                                     int64
	MtimeSec, MtimeNsec, CtimeSec, CtimeNsec int64
}

func hash(b []byte) string { x := sha256.Sum256(b); return hex.EncodeToString(x[:]) }
func PinImage(path string, runtimeImage bool) (Image, error) {
	limit := int64(MaxImageBytes)
	if runtimeImage {
		limit = MaxRuntimeBytes
	}
	return pinImage(path, limit)
}
func pinImage(path string, limit int64) (Image, error) {
	var zero Image
	if limit <= 0 || limit > MaxRuntimeBytes {
		return zero, fmt.Errorf("selected image byte limit outside absolute runtime bound")
	}
	if len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return zero, fmt.Errorf("absolute selected image path required")
	}
	f, ancestors, err := openSelectedImage(path)
	if err != nil {
		return zero, err
	}
	defer f.Close()
	before, err := stamp(f)
	if err != nil {
		return zero, err
	}
	if before.UID != uint32(os.Geteuid()) || before.Mode&syscall.S_IFMT != syscall.S_IFREG || before.Mode&0022 != 0 || before.Mode&07000 != 0 || before.Mode&0100 == 0 || before.Links != 1 || before.Size < 1 || before.Size > limit {
		return zero, fmt.Errorf("private owned bounded native executable required")
	}
	id, err := storage.DirectoryIdentity(int(f.Fd()))
	if err != nil || !id.BirthKnown {
		return zero, fmt.Errorf("durable native image identity unavailable")
	}
	acl, err := imageACL(f)
	if err != nil {
		return zero, err
	}
	if err = nativeFormat(f); err != nil {
		return zero, err
	}
	if _, err = f.Seek(0, 0); err != nil {
		return zero, err
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, limit+1))
	if err != nil || n != before.Size {
		return zero, fmt.Errorf("selected image changed during bounded read")
	}
	after, err := stamp(f)
	if err != nil || before != after {
		return zero, fmt.Errorf("selected image version changed")
	}
	// Rewalk after reading: a stable open descriptor alone cannot establish that
	// the selected path still names the same protected namespace and leaf.
	again, observed, err := openSelectedImage(path)
	if err != nil {
		return zero, err
	}
	defer again.Close()
	last, err := stamp(again)
	lastID, identityErr := storage.DirectoryIdentity(int(again.Fd()))
	lastACL, aclErr := imageACL(again)
	if err != nil || identityErr != nil || aclErr != nil || lastACL != acl || last != before || lastID != id || !sameAncestors(ancestors, observed) {
		return zero, fmt.Errorf("selected image namespace changed during read")
	}
	return Image{path: strings.Clone(path), sha: hex.EncodeToString(h.Sum(nil)), size: n, identity: id, stamp: before, ancestors: ancestors, aclSHA: acl, valid: true}, nil
}
func (i Image) SHA256() string { return i.sha }
func imageSizeWithin(size, limit int64) bool {
	return limit > 0 && limit <= MaxRuntimeBytes && size > 0 && size <= limit
}
func (i Image) check() error { return i.checkWithin(MaxRuntimeBytes) }
func (i Image) checkWithin(limit int64) error {
	// Validate the saved/private-packet bound before opening or hashing. A
	// runtime-sized pin never silently inherits the smaller provider allowance.
	if !i.valid || !imageSizeWithin(i.size, limit) {
		return fmt.Errorf("selected image size outside role byte bound")
	}
	v, err := pinImage(i.path, i.size)
	if err != nil || v.sha != i.sha || v.identity != i.identity || v.stamp != i.stamp || v.aclSHA != i.aclSHA || !sameAncestors(v.ancestors, i.ancestors) {
		return fmt.Errorf("selected image identity/bytes changed")
	}
	return nil
}
func nativeFormat(f *os.File) error {
	switch runtime.GOOS {
	case "linux":
		e, err := elf.NewFile(f)
		if err != nil {
			return fmt.Errorf("native ELF required")
		}
		if e.Type != elf.ET_EXEC && e.Type != elf.ET_DYN {
			return fmt.Errorf("native ELF entrypoint required")
		}
		if runtime.GOARCH == "arm64" && e.Machine == elf.EM_AARCH64 || runtime.GOARCH == "amd64" && e.Machine == elf.EM_X86_64 {
			return nil
		}
	case "darwin":
		m, err := macho.NewFile(f)
		if err != nil {
			return fmt.Errorf("native Mach-O required")
		}
		if m.Type != macho.TypeExec {
			return fmt.Errorf("native Mach-O entrypoint required")
		}
		if runtime.GOARCH == "arm64" && m.Cpu == macho.CpuArm64 || runtime.GOARCH == "amd64" && m.Cpu == macho.CpuAmd64 {
			return nil
		}
	}
	return fmt.Errorf("selected native executable architecture unsupported")
}
