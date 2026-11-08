package observe

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// darwinPressure reads the run queue, the kernel memory-status level and
// vm_statistics64 swap counters. macOS has no stall-time counters.
func darwinPressure() pressureRaw {
	p := pressureRaw{loadMethod: "sysctl:vm.loadavg", memoryMethod: "sysctl:kern.memorystatus_vm_pressure_level", vmMethod: "Mach HOST_VM_INFO64 swapins/swapouts"}
	p.load, p.loadErr = darwinLoad()
	if level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level"); err != nil {
		p.memErr = err
	} else {
		// 1 normal, 2 warning, 4 critical (XNU kVMPressure levels).
		p.memLevel = int(level)
	}
	p.vm, p.compressor, p.pageSize, p.vmErr = darwinVM()
	return p
}

func darwinLoad() ([3]float64, error) {
	var load [3]float64
	// struct loadavg { fixpt_t ldavg[3]; long fscale; } on 64-bit Darwin.
	b, err := unix.SysctlRaw("vm.loadavg")
	if err != nil {
		return load, err
	}
	if len(b) != 24 {
		return load, fmt.Errorf("unexpected vm.loadavg size %d", len(b))
	}
	scale := int64(binary.LittleEndian.Uint64(b[16:24]))
	if scale <= 0 {
		return load, fmt.Errorf("invalid vm.loadavg scale")
	}
	for i := range load {
		load[i] = float64(binary.LittleEndian.Uint32(b[4*i:])) / float64(scale)
	}
	return load, nil
}

func darwinVM() ([4]uint64, uint64, uint64, error) {
	var counters [4]uint64
	if err := initMonitorMach(); err != nil {
		return counters, 0, 0, err
	}
	page, err := unix.SysctlUint32("hw.pagesize")
	if err != nil || page == 0 {
		return counters, 0, 0, fmt.Errorf("page size unavailable")
	}
	// vm_statistics64 revision-1 prefix in 32-bit words; 64-bit counters
	// are naturally aligned. See xnu osfmk/mach/vm_statistics.h.
	var stats [38]uint32
	count := uint32(len(stats))
	host := monitorMach.host()
	defer monitorMach.portDeallocate(monitorMach.task(), host)
	if monitorMach.statistics64(host, 4, unsafe.Pointer(&stats[0]), &count) != 0 || count < 38 {
		return counters, 0, 0, fmt.Errorf("vm_statistics64 unavailable")
	}
	runtime.KeepAlive(&stats)
	word := func(i int) uint64 { return uint64(stats[i]) | uint64(stats[i+1])<<32 }
	counters = [4]uint64{word(10), word(28), word(30), word(26)}
	return counters, uint64(stats[32]), uint64(page), nil
}
