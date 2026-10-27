package usage

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"unsafe"

	"getarm.org/internal/model"
	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

// PROC_PIDUNIQIDENTIFIERINFO has an asserted 56-byte ABI in XNU's
// bsd/sys/proc_info_private.h. Unsupported native returns yield no credit.
type cpuUniqueInfo struct {
	UUID                           [16]byte
	Unique, ParentUnique           uint64
	Version, OriginalParentVersion int32
	Reserved                       [2]uint64
}

var cpuNative struct {
	once    sync.Once
	err     error
	pidinfo func(int32, int32, uint64, unsafe.Pointer, int32) int32
}

func initCPUNative() error {
	if err := nativeInit(); err != nil {
		return err
	}
	cpuNative.once.Do(func() {
		h, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			cpuNative.err = err
			return
		}
		p, err := purego.Dlsym(h, "proc_pidinfo")
		if err != nil {
			cpuNative.err = err
			return
		}
		purego.RegisterFunc(&cpuNative.pidinfo, p)
	})
	return cpuNative.err
}
func cpuUnique(pid int) (cpuUniqueInfo, error) {
	var v cpuUniqueInfo
	if err := initCPUNative(); err != nil {
		return v, err
	}
	if unsafe.Sizeof(v) != 56 || cpuNative.pidinfo(int32(pid), 17, 0, unsafe.Pointer(&v), 56) != 56 || v.Unique == 0 {
		return v, fmt.Errorf("native unique process identity unavailable")
	}
	return v, nil
}
func captureCPUCounter(pid int) (CPUProcessCounter, error) {
	var out CPUProcessCounter
	before, err := bsd(pid)
	if err != nil {
		return out, err
	}
	if before.Status == 5 {
		return out, fmt.Errorf("zombie process has no live CPU credit")
	}
	unique, err := cpuUnique(pid)
	if err != nil {
		return out, err
	}
	var counters rusageInfo
	if native.rusage(int32(pid), 0, &counters) != 0 {
		return out, fmt.Errorf("native process CPU counters unavailable")
	}
	afterUnique, err := cpuUnique(pid)
	if err != nil || unique.Unique != afterUnique.Unique || unique.ParentUnique != afterUnique.ParentUnique || unique.Version != afterUnique.Version {
		return out, fmt.Errorf("native process identity or ancestry changed")
	}
	after, err := bsd(pid)
	if err != nil || identity(before) != identity(after) || before.PPID != after.PPID || after.Status == 5 {
		return out, fmt.Errorf("native process start or parent changed")
	}
	out.Identity = identity(before)
	out.ParentPID = int(before.PPID)
	out.Cookie = strconv.FormatUint(unique.Unique, 10)
	out.ParentCookie = strconv.FormatUint(unique.ParentUnique, 10)
	out.User, out.System, out.Unit = counters.User, counters.System, CPUUnitDarwinMach
	return out, nil
}
func captureCPUAnchor(root model.ProcessIdentity) (string, error) {
	p, err := captureCPUCounter(root.PID)
	if err != nil {
		return "", err
	}
	if p.Identity != root {
		return "", fmt.Errorf("original gated process identity changed")
	}
	return p.Cookie, nil
}
func collectCPU(ctx context.Context, cut *CPUCut, owners []CPUOwner) {
	cut.Method = "libproc:raw-recount+unique-process-parent-identities"
	if err := initCPUNative(); err != nil {
		cut.Detail = err.Error()
		return
	}
	for _, owner := range owners {
		if ctx.Err() != nil {
			cut.Detail = "CPU collection deadline reached"
			return
		}
		root, err := captureCPUCounter(owner.Root.PID)
		if err != nil || root.Identity != owner.Root || root.Cookie != owner.Cookie {
			continue
		}
		var ids [CPUProcessLimit + 1]int32
		n := native.listpids(2, uint32(owner.Root.PGID), unsafe.Pointer(&ids[0]), int32(unsafe.Sizeof(ids)))
		if n <= 0 || n%4 != 0 || int(n) > int(unsafe.Sizeof(ids)) {
			continue
		}
		records := []CPUProcessCounter{root}
		seen := map[int]bool{root.Identity.PID: true}
		for _, pid := range ids[:int(n)/4] {
			if ctx.Err() != nil {
				cut.Detail = "CPU collection deadline reached"
				break
			}
			if pid <= 0 || seen[int(pid)] {
				continue
			}
			seen[int(pid)] = true
			if len(records) >= CPUProcessLimit-len(cut.Members) {
				cut.Detail = "CPU member limit reached"
				break
			}
			p, err := captureCPUCounter(int(pid))
			if err != nil || p.Identity.UID != owner.Root.UID || p.Identity.PGID != owner.Root.PGID {
				continue
			}
			records = append(records, p)
		}
		retainCPUAncestry(cut, owner, records)
		if len(cut.Members) == CPUProcessLimit {
			return
		}
	}
}

// XNU boot derives the 10ms hz_tick_interval from its integer Mach frequency.
// Require an exact supported conversion rather than infer Q from wall time.
func CPUClockQuantum() (uint64, string, error) {
	if err := initCPUNative(); err != nil {
		return 0, "", err
	}
	if err := darwinCPUFamilyCheck(); err != nil {
		return 0, "", err
	}
	q, err := cpuMachQuantum(native.numer, native.denom)
	if err != nil {
		return 0, "", err
	}
	return q, CPUUnitDarwinMach, nil
}

var cpuFamily struct {
	once sync.Once
	err  error
}

func darwinCPUFamilyCheck() error {
	cpuFamily.once.Do(func() {
		release, err := unix.Sysctl("kern.osrelease")
		if err != nil {
			cpuFamily.err = err
			return
		}
		version, err := unix.Sysctl("kern.version")
		if err != nil {
			cpuFamily.err = err
			return
		}
		translated, err := unix.SysctlUint32("sysctl.proc_translated")
		if err != nil {
			cpuFamily.err = err
			return
		}
		if !darwinCPUAccountingFamily(runtime.GOARCH, release, version, int32(translated)) {
			cpuFamily.err = fmt.Errorf("managed CPU credit requires native ARM64 Darwin25/XNU12377 release recount accounting")
		}
	})
	return cpuFamily.err
}
