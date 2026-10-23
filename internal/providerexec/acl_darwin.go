//go:build darwin

package providerexec

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
)

// Public SDK ABI facts: sys/acl.h + sys/kauth.h. The existing purego dependency
// binds the trusted fixed libSystem only; no caller path or C compiler is used.
const maxNativeACLBytes = 44 + 128*24

type aclNativeCalls struct {
	errno func() *int32
	get   func(int32, int32) (uintptr, int32)
	size  func(uintptr) int64
	copy  func(*byte, uintptr, int64) int64
	free  func(uintptr) int32
}

var selectedACL struct {
	library uintptr
	once    sync.Once
	err     error
	calls   aclNativeCalls
}

func initSelectedACL() error {
	selectedACL.once.Do(func() {
		library, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			selectedACL.err = err
			return
		}
		selectedACL.library = library
		// Keep the handle loaded for the lifetime of all registered functions.
		for _, symbol := range []struct {
			name   string
			target any
		}{{"acl_size", &selectedACL.calls.size}, {"acl_copy_ext_native", &selectedACL.calls.copy}, {"acl_free", &selectedACL.calls.free}, {"__error", &selectedACL.calls.errno}} {
			pointer, e := purego.Dlsym(library, symbol.name)
			if e != nil {
				selectedACL.err = e
				return
			}
			purego.RegisterFunc(symbol.target, pointer)
		}
		getACL, err := purego.Dlsym(library, "acl_get_fd_np")
		if err != nil {
			selectedACL.err = err
			return
		}
		selectedACL.calls.get = func(fd, kind int32) (uintptr, int32) {
			// SyscallN captures errno inside the native bridge, before returning
			// to Go/runtime code that may change the same OS-thread TLS slot.
			acl, _, captured := purego.SyscallN(getACL, uintptr(fd), uintptr(kind))
			return acl, int32(captured) // errno is C int, not uintptr.
		}
	})
	return selectedACL.err
}
func imageACL(f *os.File) (digest string, err error) {
	if err = initSelectedACL(); err != nil {
		return "", fmt.Errorf("native image ACL inspection unavailable: %w", err)
	}
	var fs unix.Statfs_t
	if err = unix.Fstatfs(int(f.Fd()), &fs); err != nil {
		return "", err
	}
	name := string(bytes.TrimRight(fs.Fstypename[:], "\x00"))
	if name != "apfs" && name != "hfs" {
		return "", fmt.Errorf("native image ACL filesystem authorization model unsupported")
	}
	return selectedACLEvidence(f, selectedACL.calls)
}

func selectedACLEvidence(f *os.File, calls aclNativeCalls) (digest string, err error) {
	acl, nativeError := getACLOnThread(f, calls)
	if acl == 0 {
		// acl_get_fd_np is fd-only. On the known supported local filesystems,
		// ENOENT denotes absent FILESEC_ACL after successful descriptor stat;
		// all nil/zero/other errors are unknown, never positive absence.
		if nativeError == int32(unix.ENOENT) {
			return hash([]byte("darwin:known-local-filesystem:fd-acl-absent")), nil
		}
		return "", fmt.Errorf("native image ACL unknown (errno=%d)", nativeError)
	}
	defer func() {
		if calls.free(acl) != 0 && err == nil {
			digest = ""
			err = fmt.Errorf("native image ACL release failed")
		}
	}()
	size := calls.size(acl)
	if size < 44 || size > maxNativeACLBytes {
		return "", fmt.Errorf("native image ACL size outside fixed bound")
	}
	var raw [maxNativeACLBytes]byte
	copied := calls.copy(&raw[0], acl, size)
	runtime.KeepAlive(&raw)
	if copied != size {
		return "", fmt.Errorf("native image ACL copy incomplete")
	}
	if err = validateNativeACL(raw[:int(size)]); err != nil {
		return "", err
	}
	return hash(raw[:int(size)]), nil
}

// Conservative supported policy: empty or deny-only ACLs. We do not resolve
// principals, assume a preceding deny neutralizes an allow, or rewrite ACLs.
// Even read-only allow entries are presently unsupported and fail closed.
func validateNativeACL(raw []byte) error {
	if len(raw) < 44 || len(raw) > maxNativeACLBytes || binary.LittleEndian.Uint32(raw[:4]) != 0x012cc16d {
		return fmt.Errorf("unknown native ACL framing")
	}
	count := binary.LittleEndian.Uint32(raw[36:40])
	flags := binary.LittleEndian.Uint32(raw[40:44])
	if flags & ^uint32(0x00030000) != 0 {
		return fmt.Errorf("unsupported native ACL flags")
	}
	if count == 0xffffffff {
		if len(raw) != 44 {
			return fmt.Errorf("native ACL absence has trailing bytes")
		}
		return nil
	}
	if count > 128 || len(raw) != 44+int(count)*24 {
		return fmt.Errorf("native ACL entry roster mismatch")
	}
	for i := 0; i < int(count); i++ {
		offset := 44 + i*24
		flags = binary.LittleEndian.Uint32(raw[offset+16 : offset+20])
		if flags&0xf != 2 || flags & ^uint32(0x1f2) != 0 {
			return fmt.Errorf("image ACL contains unsupported allow or entry flags")
		}
	}
	return nil
}

func getSelectedACL(f *os.File) (uintptr, int32) { return getACLOnThread(f, selectedACL.calls) }

func getACLOnThread(f *os.File, calls aclNativeCalls) (uintptr, int32) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	nativeError := calls.errno()
	if nativeError == nil {
		return 0, -1
	}
	// Only an interrupted, nil result may be retried. Each attempt clears and
	// uses bridge-captured errno on this same locked OS thread. A positive allocation is
	// returned immediately to its sole freeing caller, even if errno is stale.
	for attempt := 0; attempt < 3; attempt++ {
		*nativeError = 0
		acl, saved := calls.get(int32(f.Fd()), 0x100)
		runtime.KeepAlive(f)
		if acl != 0 || saved != int32(unix.EINTR) || attempt == 2 {
			return acl, saved
		}
	}
	panic("bounded native ACL attempt loop exhausted")
}
