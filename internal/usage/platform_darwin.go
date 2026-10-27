package usage

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"sync"
	"unsafe"

	"getarm.org/internal/model"
	"github.com/ebitengine/purego"
)

// Layouts correspond to public proc_bsdinfo and rusage_info_v0 on macOS
// amd64/arm64. Native return sizes and identity fields are checked at runtime.
type bsdInfo struct {
	Flags, Status, XStatus, PID, PPID, UID, GID, RUID, RGID, SVUID, SVGID, Reserved uint32
	Comm                                                                            [16]byte
	Name                                                                            [32]byte
	Files, PGID, Jobc, Tdev, Tpgid                                                  uint32
	Nice                                                                            int32
	StartSec, StartUsec                                                             uint64
}
type rusageInfo struct {
	UUID                                                                                [16]byte
	User, System, IdleWake, Interrupt, Pageins, Wired, Resident, Footprint, Start, Exit uint64
}

var native struct {
	once         sync.Once
	err          error
	namespace    string
	pidinfo      func(int32, int32, uint64, *bsdInfo, int32) int32
	listpids     func(uint32, uint32, unsafe.Pointer, int32) int32
	rusage       func(int32, int32, *rusageInfo) int32
	timebase     func(*[2]uint32) int32
	sysctlbyname func(string, *[darwinBootUUIDSize]byte, *uintptr, unsafe.Pointer, uintptr) int32
	numer, denom uint32
}

func nativeInit() error {
	native.once.Do(func() {
		handle, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			native.err = err
			return
		}
		// libSystem re-exports libproc. Keep the native library loaded for the
		// lifetime of these function pointers; no caller-selected library paths.
		for _, symbol := range []struct {
			name   string
			target any
		}{{"proc_pidinfo", &native.pidinfo}, {"proc_listpids", &native.listpids}, {"proc_pid_rusage", &native.rusage}, {"mach_timebase_info", &native.timebase}, {"sysctlbyname", &native.sysctlbyname}} {
			ptr, err := purego.Dlsym(handle, symbol.name)
			if err != nil {
				native.err = err
				return
			}
			purego.RegisterFunc(symbol.target, ptr)
		}
		var tb [2]uint32
		if native.timebase(&tb) != 0 || tb[0] == 0 || tb[1] == 0 {
			native.err = fmt.Errorf("Mach timebase is unavailable")
			return
		}
		native.numer, native.denom = tb[0], tb[1]
		native.namespace, native.err = darwinBootNamespace(func(raw *[darwinBootUUIDSize]byte, size *uintptr) error {
			if native.sysctlbyname("kern.bootsessionuuid", raw, size, nil, 0) != 0 {
				return fmt.Errorf("kern.bootsessionuuid unavailable")
			}
			runtime.KeepAlive(raw)
			return nil
		})
	})
	return native.err
}
func bsd(pid int) (bsdInfo, error) {
	var info bsdInfo
	if err := nativeInit(); err != nil {
		return info, err
	}
	n := native.pidinfo(int32(pid), 3, 0, &info, int32(unsafe.Sizeof(info)))
	runtime.KeepAlive(&info)
	if n != int32(unsafe.Sizeof(info)) || int(info.PID) != pid || info.StartSec == 0 || info.StartUsec >= 1_000_000 {
		return info, fmt.Errorf("process identity unavailable")
	}
	return info, nil
}
func identity(info bsdInfo) model.ProcessIdentity {
	return model.ProcessIdentity{PID: int(info.PID), PGID: int(info.PGID), UID: strconv.FormatUint(uint64(info.UID), 10), Start: fmt.Sprintf("darwin:%d:%d", info.StartSec, info.StartUsec), Namespace: native.namespace}
}
func capture(pid int) (model.ProcessIdentity, error) {
	b, err := bsd(pid)
	if err != nil {
		return model.ProcessIdentity{}, err
	}
	return identity(b), nil
}
func collect(ctx context.Context, s *Snapshot) {
	s.Method = "libproc proc_listpids/proc_pidinfo/proc_pid_rusage"
	s.MemoryMetric = "sum-process-physical-footprint"
	s.Detail = "Native task-ledger footprint including compression and platform charges; not exclusive physical bytes. Non-atomic live group observation."
	var ids [MaxMembers + 1]int32
	size := native.listpids(2, uint32(s.Root.PGID), unsafe.Pointer(&ids[0]), int32(unsafe.Sizeof(ids)))
	runtime.KeepAlive(&ids)
	if size < 0 || size%4 != 0 {
		s.Complete = false
		s.Detail += "; group enumeration failed"
		return
	}
	n := int(size) / 4
	if n == 0 {
		s.Complete = false
		s.Detail += "; group enumeration unavailable"
		return
	}
	if n > MaxMembers {
		n = MaxMembers
		s.Complete = false
		s.Detail += "; group member limit reached"
	}
	for _, pid := range ids[:n] {
		if ctx.Err() != nil {
			s.Complete = false
			s.Detail += "; collection cancelled"
			return
		}
		if pid <= 0 {
			continue
		}
		before, err := capture(int(pid))
		if err != nil || before.PGID != s.Root.PGID || before.UID != s.Root.UID {
			s.Complete = false
			continue
		}
		var r rusageInfo
		if native.rusage(pid, 0, &r) != 0 {
			s.Complete = false
			continue
		}
		runtime.KeepAlive(&r)
		after, err := capture(int(pid))
		if err != nil || before != after {
			s.Complete = false
			continue
		}
		p := model.ProcessUsage{Identity: before, MemoryMetric: s.MemoryMetric}
		seconds := (float64(r.User) + float64(r.System)) * float64(native.numer) / float64(native.denom) / 1e9
		if !math.IsInf(seconds, 0) && !math.IsNaN(seconds) {
			p.CPUSeconds = &seconds
		}
		resident, footprint := r.Resident, r.Footprint
		p.ResidentBytes = &resident
		p.AccountedMemoryBytes = &footprint
		s.Members = append(s.Members, p)
	}
	if len(s.Members) == 0 {
		s.Complete = false
		s.Detail += "; no member counters were available"
	}
}

// XNU initializes this read-only UUID once in IOPMrootDomain::start. In
// contrast, clock_set_calendar_microtime adjusts kern.boottime with wall time.
// Read only the fixed uuid_string_t representation; no wall-time fallback or
// translation of retained identities can grant attribution or cleanup authority.
const darwinBootUUIDSize = 37

func darwinBootNamespace(read func(*[darwinBootUUIDSize]byte, *uintptr) error) (string, error) {
	var raw [darwinBootUUIDSize]byte
	size := uintptr(len(raw))
	if err := read(&raw, &size); err != nil {
		return "", fmt.Errorf("Darwin boot session identity: %w", err)
	}
	if size != darwinBootUUIDSize || raw[36] != 0 {
		return "", fmt.Errorf("Darwin boot session UUID has invalid size or terminator")
	}
	nonzero := false
	for i, c := range raw[:36] {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return "", fmt.Errorf("Darwin boot session UUID has invalid separator")
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F') {
			return "", fmt.Errorf("Darwin boot session UUID has invalid hex digit")
		}
		nonzero = nonzero || c != '0'
	}
	if !nonzero {
		return "", fmt.Errorf("Darwin boot session UUID is nil")
	}
	return "darwin:bootsessionuuid:" + string(raw[:36]), nil
}
