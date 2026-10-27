package childowner

import (
	"fmt"
	"github.com/ebitengine/purego"
	"runtime"
	"sync"
	"syscall"
	"time"
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
	once     sync.Once
	err      error
	wait     func(int32, uint32, *darwinChildInfo, int32) int32
	errno    func() *int32
	listpids func(uint32, uint32, unsafe.Pointer, int32) int32
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
		}{{"waitid", &childNative.wait}, {"__error", &childNative.errno}, {"proc_listpids", &childNative.listpids}} {
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

// Called only after WNOWAIT proved the original unreaped leader terminal.
// XNU proc_listpids holds proc_list_lock over allproc AND zombproc. A fixed
// two-PID buffer returning only that anchor establishes no other group member
// at that membership snapshot. A full buffer, empty result, malformed return,
// or permission failure never establishes this witness. No EPERM is rewritten.
// https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/proc_info.c
func terminalOnlyGroup(pid int) (bool, error) {
	var ids [2]int32
	n := childNative.listpids(2, uint32(pid), unsafe.Pointer(&ids[0]), int32(unsafe.Sizeof(ids)))
	runtime.KeepAlive(&ids)
	return terminalOnlyPIDList(pid, ids, n)
}
func terminalOnlyPIDList(pid int, ids [2]int32, n int32) (bool, error) {
	if n <= 0 || n > int32(unsafe.Sizeof(ids)) || n%4 != 0 {
		return false, fmt.Errorf("invalid original group PID enumeration size %d", n)
	}
	return n == 4 && ids[0] == int32(pid), nil
}

const nativeTerminalGroupGrace = 250 * time.Millisecond

const nativePreflightSignals = true
