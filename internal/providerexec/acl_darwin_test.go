package providerexec

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

func aclFixtureFile(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "acl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}
func nativeACLBytes(count uint32, flags uint32) []byte {
	raw := make([]byte, 44+int(count)*24)
	binary.LittleEndian.PutUint32(raw[:4], 0x012cc16d)
	binary.LittleEndian.PutUint32(raw[36:40], count)
	for i := 0; i < int(count); i++ {
		binary.LittleEndian.PutUint32(raw[44+i*24+16:44+i*24+20], flags)
	}
	return raw
}
func TestNativeACLErrnoAndFaultsAreFailClosed(t *testing.T) {
	f := aclFixtureFile(t)
	if digest, err := selectedACLEvidence(f, aclNativeCalls{errno: func() *int32 { return nil }}); err == nil || digest != "" {
		t.Fatal("missing native errno pointer became absence")
	}
	for _, number := range []int32{0, int32(unix.EACCES), int32(unix.EOPNOTSUPP), int32(unix.ENOENT), 260} {
		var nativeError int32 = int32(unix.ENOENT)
		calls := aclNativeCalls{errno: func() *int32 { return &nativeError }, get: func(int32, int32) (uintptr, int32) {
			if nativeError != 0 {
				t.Fatal("stale errno was not cleared")
			}
			nativeError = number
			return 0, number
		}}
		digest, err := selectedACLEvidence(f, calls)
		if number == int32(unix.ENOENT) {
			if err != nil || digest == "" {
				t.Fatal("exact absent-ACL errno refused", err)
			}
		} else if err == nil || digest != "" {
			t.Fatal("nil/error promoted to ACL absence", number)
		}
	}
	for _, target := range []string{"negative-size", "oversize", "short-copy", "bad-framing", "free-error"} {
		t.Run(target, func(t *testing.T) {
			raw := nativeACLBytes(1, 2)
			size := int64(len(raw))
			if target == "negative-size" {
				size = -1
			}
			if target == "oversize" {
				size = maxNativeACLBytes + 1
			}
			freed, copied := 0, 0
			nativeError := int32(0)
			calls := aclNativeCalls{errno: func() *int32 { return &nativeError }, get: func(int32, int32) (uintptr, int32) { return 1, 0 }, size: func(uintptr) int64 { return size }, free: func(uintptr) int32 {
				freed++
				if target == "free-error" {
					return -1
				}
				return 0
			}, copy: func(dst *byte, _ uintptr, n int64) int64 {
				copied++
				if target == "short-copy" {
					return n - 1
				}
				if target == "bad-framing" {
					raw[0] = 0
				}
				copy(unsafe.Slice(dst, int(n)), raw)
				return n
			}}
			digest, err := selectedACLEvidence(f, calls)
			if err == nil || digest != "" || freed != 1 {
				t.Fatal("ACL fault accepted or not freed", target, digest, err, freed)
			}
			if (target == "negative-size" || target == "oversize") && copied != 0 {
				t.Fatal("copy occurred before native size bound")
			}
		})
	}
}
func TestNativeACLInterruptedReadHasThreeAttemptBound(t *testing.T) {
	f := aclFixtureFile(t)
	for _, target := range []string{"transient-absence", "persistent", "different-error", "positive"} {
		t.Run(target, func(t *testing.T) {
			attempts, freed := 0, 0
			var nativeError int32 = int32(unix.ENOENT)
			raw := nativeACLBytes(1, 2)
			calls := aclNativeCalls{errno: func() *int32 { return &nativeError }, get: func(int32, int32) (uintptr, int32) {
				attempts++
				if nativeError != 0 {
					t.Fatal("retry inherited stale errno")
				}
				if attempts == 1 || target == "persistent" {
					nativeError = int32(unix.EINTR)
					return 0, int32(unix.EINTR)
				}
				switch target {
				case "transient-absence":
					nativeError = int32(unix.ENOENT)
				case "different-error":
					nativeError = int32(unix.EACCES)
				case "positive":
					// A positive allocation with errno EINTR is not retried.
					nativeError = int32(unix.EINTR)
					return 1, int32(unix.EINTR)
				}
				return 0, nativeError
			}, size: func(uintptr) int64 { return int64(len(raw)) }, copy: func(dst *byte, _ uintptr, n int64) int64 {
				copy(unsafe.Slice(dst, int(n)), raw)
				return n
			}, free: func(uintptr) int32 { freed++; return 0 }}
			digest, err := selectedACLEvidence(f, calls)
			expectedAttempts := 2
			if target == "persistent" {
				expectedAttempts = 3
			}
			if attempts != expectedAttempts {
				t.Fatal("incorrect retry bound", attempts)
			}
			positive := target == "positive" || target == "transient-absence"
			if positive != (err == nil && digest != "") {
				t.Fatal("incorrect ACL evidence", digest, err)
			}
			if freed != map[bool]int{false: 0, true: 1}[target == "positive"] {
				t.Fatal("allocated ACL not freed exactly once", freed)
			}
		})
	}
}

// The later TLS value is deliberately contradictory. Decisions must use the
// errno saved with the exact native return, not that later mutable TLS slot.
func TestNativeACLUsesCapturedErrnoInsteadOfLaterThreadValue(t *testing.T) {
	f := aclFixtureFile(t)
	for _, target := range []struct {
		name            string
		captured, later int32
		attempts        int
		absent          bool
	}{
		{"captured-absence-later-interrupted", int32(unix.ENOENT), int32(unix.EINTR), 1, true},
		{"captured-interrupted-later-absence", int32(unix.EINTR), int32(unix.ENOENT), 3, false},
		{"captured-260-later-absence", 260, int32(unix.ENOENT), 1, false},
		{"captured-zero-later-absence", 0, int32(unix.ENOENT), 1, false},
	} {
		t.Run(target.name, func(t *testing.T) {
			callsMade := 0
			var later int32 = int32(unix.ENOENT)
			calls := aclNativeCalls{errno: func() *int32 { return &later }, get: func(int32, int32) (uintptr, int32) {
				if later != 0 {
					t.Fatal("errno not cleared before native call")
				}
				callsMade++
				later = target.later
				return 0, target.captured
			}}
			digest, err := selectedACLEvidence(f, calls)
			if callsMade != target.attempts || target.absent != (err == nil && digest != "") {
				t.Fatal("later TLS value selected ACL policy", callsMade, digest, err)
			}
		})
	}
}

func TestNativeACLClosedFramingAndMaximumDenyOnly(t *testing.T) {
	empty := nativeACLBytes(0, 0)
	if err := validateNativeACL(empty); err != nil {
		t.Fatal(err)
	}
	maximum := nativeACLBytes(128, 2|0x10|0x20|0x40|0x80|0x100)
	if len(maximum) != maxNativeACLBytes {
		t.Fatal("bad mechanical ABI bound")
	}
	if err := validateNativeACL(maximum); err != nil {
		t.Fatal(err)
	}
	none := append([]byte(nil), empty...)
	binary.LittleEndian.PutUint32(none[36:40], 0xffffffff)
	if err := validateNativeACL(none); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{nil, empty[:43], append(empty, 0), nativeACLBytes(129, 2), nativeACLBytes(1, 1), nativeACLBytes(1, 3), nativeACLBytes(1, 2|0x800)} {
		if err := validateNativeACL(bad); err == nil {
			t.Fatal("unsupported ACL framing/count/entry accepted")
		}
	}
}
func TestActualNativeACLAbsentEmptyAndDenyOnlyControls(t *testing.T) {
	if err := initSelectedACL(); err != nil {
		t.Fatal(err)
	}
	f := aclFixtureFile(t)
	if out, err := exec.Command("/bin/chmod", "-N", f.Name()).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	acl, number := getSelectedACL(f)
	if acl != 0 {
		selectedACL.calls.free(acl)
		t.Fatal("actual removed ACL did not yield absence", number)
	}
	if number != int32(unix.ENOENT) {
		t.Fatal("actual absent errno", number)
	}
	if digest, err := imageACL(f); err != nil || digest == "" {
		t.Fatal(digest, err)
	}
	var init func(int32) uintptr
	var set func(int32, uintptr, int32) int32
	for _, symbol := range []struct {
		name   string
		target any
	}{{"acl_init", &init}, {"acl_set_fd_np", &set}} {
		pointer, err := purego.Dlsym(selectedACL.library, symbol.name)
		if err != nil {
			t.Fatal(err)
		}
		purego.RegisterFunc(symbol.target, pointer)
	}
	empty := init(0)
	if empty == 0 {
		t.Fatal("native empty ACL allocation failed")
	}
	if set(int32(f.Fd()), empty, 0x100) != 0 {
		selectedACL.calls.free(empty)
		t.Fatal("native empty ACL set failed")
	}
	if selectedACL.calls.free(empty) != 0 {
		t.Fatal("native empty ACL release failed")
	}
	acl, number = getSelectedACL(f)
	if acl != 0 {
		if selectedACL.calls.free(acl) != 0 {
			t.Fatal("native observed empty ACL release failed")
		}
	} else if number != int32(unix.ENOENT) {
		t.Fatal("native empty ACL became unknown", number)
	}
	t.Logf("actual requested empty ACL: allocated=%t errno=%d (kernel may canonicalize empty to absent)", acl != 0, number)
	if _, err := imageACL(f); err != nil {
		t.Fatal("actual empty/absent control refused", err)
	}

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "provider")
	copyNativeForSelection(t, path)
	selected, err := PinImage(path, false)
	if err != nil {
		t.Fatal(err)
	}
	ownedDir, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		current, err := os.Lstat(root)
		if err != nil || !os.SameFile(ownedDir, current) {
			t.Error("changed owned ACL fixture retained", root)
			return
		}
		if out, err := exec.Command("/bin/chmod", "-N", root).CombinedOutput(); err != nil {
			t.Error("owned ACL fixture cleanup", err, string(out))
		}
	})
	if out, e := exec.Command("/bin/chmod", "+a", "everyone deny delete", root).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	// Only this newly owned fixture is changed; no existing home ACL is touched.
	current, err := PinImage(path, false)
	if err != nil {
		t.Fatal("safe common home-style deny-only ACL refused", err)
	}
	if current.stamp != selected.stamp || current.identity != selected.identity || current.sha != selected.sha {
		t.Fatal("test changed leaf rather than only ancestor ACL")
	}
	if sameAncestors(current.ancestors, selected.ancestors) {
		t.Fatal("ancestor ACL evidence was not bound")
	}
	if selected.check() == nil {
		t.Fatal("safe changed ancestor ACL silently inherited original selection")
	}
	t.Log("actual deny-only directory control accepted; changed ACL invalidated old selection")
}
