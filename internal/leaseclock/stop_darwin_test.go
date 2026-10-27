package leaseclock

import (
	"fmt"
	"github.com/ebitengine/purego"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var stopNative struct {
	once sync.Once
	err  error
	wait func(int32, uint32, *stopSiginfo, int32) int32
}

// Same probed arm64 LP64 layout as scripts/formal_process.py. Only this native
// test observation uses it; runtime lease expiry does not depend on waitid.
type stopSiginfo struct {
	Signo, Errno, Code, PID int32
	UID                     uint32
	Status                  int32
	Address, Value, Band    uint64
	Pad                     [7]uint64
}

func observeStopped(pid int) (bool, error) {
	stopNative.once.Do(func() {
		if runtime.GOARCH != "arm64" || unsafe.Sizeof(stopSiginfo{}) != 104 {
			stopNative.err = fmt.Errorf("unverified Darwin stop ABI")
			return
		}
		h, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			stopNative.err = err
			return
		}
		ptr, err := purego.Dlsym(h, "waitid")
		if err != nil {
			stopNative.err = err
			return
		}
		purego.RegisterFunc(&stopNative.wait, ptr)
	})
	if stopNative.err != nil {
		return false, stopNative.err
	}
	var info stopSiginfo
	if stopNative.wait(1, uint32(pid), &info, syscall.WSTOPPED|syscall.WEXITED|syscall.WNOHANG|syscall.WNOWAIT) != 0 {
		return false, fmt.Errorf("native non-reaping stop observation failed")
	}
	if info.PID == 0 {
		return false, nil
	}
	if info.PID != int32(pid) || info.Signo != int32(syscall.SIGCHLD) || info.Errno != 0 || info.Code != 5 || info.Status != int32(syscall.SIGSTOP) {
		return false, fmt.Errorf("child not stopped: non-reaping siginfo %+v", info)
	}
	return true, nil
}
