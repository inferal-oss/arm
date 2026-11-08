package guardian

import (
	"bytes"
	"encoding/binary"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

var darwinNative struct {
	once    sync.Once
	err     error
	pidpath func(int32, unsafe.Pointer, uint32) int32
	pidinfo func(int32, int32, uint64, unsafe.Pointer, int32) int32
}

func initNative() error {
	darwinNative.once.Do(func() {
		h, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			darwinNative.err = err
			return
		}
		for _, s := range []struct {
			name   string
			target any
		}{{"proc_pidpath", &darwinNative.pidpath}, {"proc_pidinfo", &darwinNative.pidinfo}} {
			p, err := purego.Dlsym(h, s.name)
			if err != nil {
				darwinNative.err = err
				return
			}
			purego.RegisterFunc(s.target, p)
		}
	})
	return darwinNative.err
}

// native returns the executable path. Darwin has no cgroups.
func native(pid int) (string, string) {
	if initNative() != nil {
		return "", ""
	}
	var buf [4096]byte // PROC_PIDPATHINFO_MAXSIZE
	n := darwinNative.pidpath(int32(pid), unsafe.Pointer(&buf[0]), uint32(len(buf)))
	runtime.KeepAlive(&buf)
	if n <= 0 || int(n) > len(buf) {
		return "", ""
	}
	return string(buf[:n]), ""
}

// classify reads the arguments only of browser helpers, through
// KERN_PROCARGS2, and only to find the Chromium --type switch.
func classify(pid int, p Process) string {
	if !browser(p) || !strings.Contains(p.Name, "Helper") {
		return ""
	}
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return ""
	}
	// int argc; executable path; NUL padding; argc arguments; environment.
	argc := int(int32(binary.LittleEndian.Uint32(raw[:4])))
	rest := raw[4:]
	if i := bytes.IndexByte(rest, 0); i >= 0 {
		rest = rest[i:]
	}
	rest = bytes.TrimLeft(rest, "\x00")
	args := bytes.SplitN(rest, []byte{0}, argc+1)
	if len(args) > argc {
		args = args[:argc] // drop the environment
	}
	return chromiumType(bytes.Join(args, []byte{0}))
}

// workingDirectory reads the current directory through PROC_PIDVNODEPATHINFO.
func workingDirectory(pid int) string {
	if initNative() != nil {
		return ""
	}
	// struct proc_vnodepathinfo: two vnode_info_path { vnode_info (152 bytes); char path[1024]; }.
	var buf [2 * (152 + 1024)]byte
	n := darwinNative.pidinfo(int32(pid), 9, 0, unsafe.Pointer(&buf[0]), int32(len(buf)))
	runtime.KeepAlive(&buf)
	if n != int32(len(buf)) {
		return ""
	}
	path := buf[152 : 152+1024]
	for i, c := range path {
		if c == 0 {
			return string(path[:i])
		}
	}
	return ""
}
