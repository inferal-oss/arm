package usage

import (
	"bytes"
	"fmt"
	"runtime"
	"unsafe"
)

// rusageInfoV2 extends rusage_info_v0 with child and storage counters.
type rusageInfoV2 struct {
	rusageInfo
	ChildUser, ChildSystem, ChildIdleWake, ChildInterrupt, ChildPageins, ChildElapsed uint64
	DiskRead, DiskWrite                                                               uint64
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func inspect(pid int) (Sample, error) {
	info, err := bsd(pid)
	if err != nil {
		return Sample{}, err
	}
	// pbi_status SSTOP is 4 (xnu bsd/sys/proc.h).
	s := Sample{Identity: identity(info), PPID: int(info.PPID), Name: cString(info.Name[:]), Stopped: info.Status == 4}
	if s.Name == "" {
		s.Name = cString(info.Comm[:])
	}
	var r rusageInfoV2
	// RUSAGE_INFO_V2. Another user's process refuses without privilege.
	if native.rusage(int32(pid), 2, &r.rusageInfo) == 0 {
		runtime.KeepAlive(&r)
		seconds := (float64(r.User) + float64(r.System)) * float64(native.numer) / float64(native.denom) / 1e9
		resident, footprint, read, write := r.Resident, r.Footprint, r.DiskRead, r.DiskWrite
		s.CPUSeconds, s.Resident, s.Footprint, s.DiskRead, s.DiskWrite = &seconds, &resident, &footprint, &read, &write
	}
	return s, nil
}

func listPIDs(limit int) ([]int, error) {
	if err := nativeInit(); err != nil {
		return nil, err
	}
	ids := make([]int32, limit+1)
	// PROC_ALL_PIDS returns bytes written.
	size := native.listpids(1, 0, unsafe.Pointer(&ids[0]), int32(len(ids)*4))
	runtime.KeepAlive(ids)
	if size < 0 || size%4 != 0 {
		return nil, fmt.Errorf("process enumeration failed")
	}
	n := int(size) / 4
	out := make([]int, 0, n)
	for _, id := range ids[:min(n, limit)] {
		if id > 0 {
			out = append(out, int(id))
		}
	}
	if n > limit {
		return out, fmt.Errorf("process enumeration limited to %d processes", limit)
	}
	return out, nil
}
