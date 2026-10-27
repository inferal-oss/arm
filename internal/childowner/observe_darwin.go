package childowner

import (
	"fmt"
	"github.com/ebitengine/purego"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

type darwinChildInfo struct {
	Signo, Errno, Code, PID int32
	UID                     uint32
	Status                  int32
	Address, Value, Band    uint64
	Pad                     [7]uint64
}

var childNative struct {
	once  sync.Once
	err   error
	wait  func(int32, uint32, *darwinChildInfo, int32) int32
	errno func() *int32
}

func observeTerminal(pid int) (terminal, error) {
	childNative.once.Do(func() {
		if unsafe.Sizeof(darwinChildInfo{}) != 104 || unsafe.Offsetof(darwinChildInfo{}.PID) != 12 || unsafe.Offsetof(darwinChildInfo{}.Status) != 20 {
			childNative.err = fmt.Errorf("unsupported Darwin child observation ABI")
			return
		}
		h, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			childNative.err = err
			return
		}
		for _, s := range []struct {
			name   string
			target any
		}{{"waitid", &childNative.wait}, {"__error", &childNative.errno}} {
			ptr, err := purego.Dlsym(h, s.name)
			if err != nil {
				childNative.err = err
				return
			}
			purego.RegisterFunc(s.target, ptr)
		}
	})
	if childNative.err != nil {
		return terminal{}, childNative.err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var info darwinChildInfo
	if childNative.wait(1, uint32(pid), &info, syscall.WEXITED|syscall.WNOHANG|syscall.WNOWAIT) != 0 {
		return terminal{}, syscall.Errno(*childNative.errno())
	}
	if info.PID == 0 {
		return terminal{}, nil
	}
	if info.PID != int32(pid) || info.Signo != int32(syscall.SIGCHLD) || info.Errno != 0 {
		return terminal{}, fmt.Errorf("crossed or malformed Darwin child observation")
	}
	switch info.Code {
	case 1:
		return terminal{ready: true, code: int(info.Status) & 255}, nil
	case 2, 3:
		if info.Status > 0 && info.Status < 32 {
			return terminal{ready: true, signal: syscall.Signal(info.Status)}, nil
		}
	}
	return terminal{}, fmt.Errorf("unsupported Darwin terminal observation code=%d status=%d", info.Code, info.Status)
}
