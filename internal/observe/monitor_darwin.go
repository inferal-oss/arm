package observe

import (
	"context"
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

var monitorMach struct {
	once           sync.Once
	err            error
	host           func() uint32
	task           func() uint32
	processors     func(uint32, int32, *uint32, *unsafe.Pointer, *uint32) int32
	deallocate     func(uint32, uintptr, uintptr) int32
	portDeallocate func(uint32, uint32) int32
	statistics     func(uint32, int32, unsafe.Pointer, *uint32) int32
	statistics64   func(uint32, int32, unsafe.Pointer, *uint32) int32
	sysctl         func(*int32, uint32, unsafe.Pointer, *uintptr, unsafe.Pointer, uintptr) int32
}

func initMonitorMach() error {
	monitorMach.once.Do(func() {
		h, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			monitorMach.err = err
			return
		}
		for _, symbol := range []struct {
			name   string
			target any
		}{{"mach_host_self", &monitorMach.host}, {"mach_task_self", &monitorMach.task}, {"host_processor_info", &monitorMach.processors}, {"vm_deallocate", &monitorMach.deallocate}, {"mach_port_deallocate", &monitorMach.portDeallocate}, {"host_statistics", &monitorMach.statistics}, {"host_statistics64", &monitorMach.statistics64}, {"sysctl", &monitorMach.sysctl}} {
			p, e := purego.Dlsym(h, symbol.name)
			if e != nil {
				monitorMach.err = e
				return
			}
			purego.RegisterFunc(symbol.target, p)
		}
	})
	return monitorMach.err
}
func collectMonitor(ctx context.Context) monitorRaw {
	r := monitorRaw{at: time.Now(), cpuMethod: "Mach:host_processor_info", diskMethod: "ioreg:IOBlockStorageDriver Statistics", networkMethod: "sysctl:NET_RT_IFLIST2"}
	r.cpu, r.cpuError = darwinMonitorCPU()
	r.memory = darwinMonitorMemory()
	r.swap = unavailable("host-swap", "sysctl:vm.swapusage", "swap counters unavailable")
	if b, e := unix.SysctlRaw("vm.swapusage"); e == nil && len(b) == 32 {
		total, free := binary.LittleEndian.Uint64(b[:8]), binary.LittleEndian.Uint64(b[8:16])
		r.swap = memoryValues(total, free, "host-swap", "sysctl:vm.swapusage", time.Now())
	}
	r.network, r.networkError = darwinMonitorNetwork()
	if data, e := command(ctx, "/usr/sbin/ioreg", "-a", "-r", "-c", "IOBlockStorageDriver", "-d", "1"); e != nil {
		r.diskError = e
	} else {
		r.disk, r.diskError = darwinMonitorDisk(data, time.Now())
	}
	return r
}
func darwinMonitorCPU() ([]cpuCounter, error) {
	if err := initMonitorMach(); err != nil {
		return nil, err
	}
	host := monitorMach.host()
	task := monitorMach.task()
	defer monitorMach.portDeallocate(task, host)
	var count, cpus uint32
	var data unsafe.Pointer
	// PROCESSOR_CPU_LOAD_INFO: four 32-bit ticks per processor. The kernel
	// allocates count integers; release the full byte range, not CPU count.
	status := monitorMach.processors(host, 2, &cpus, &data, &count)
	if data != nil && count > 0 {
		defer monitorMach.deallocate(task, uintptr(data), uintptr(count)*4)
	}
	if status != 0 || data == nil || cpus == 0 || cpus > monitorCPUs || count != cpus*4 {
		return nil, fmt.Errorf("native CPU array unavailable or exceeds %d CPUs", monitorCPUs)
	}
	ticks := unsafe.Slice((*[4]uint32)(data), int(cpus))
	rows := make([]cpuCounter, 0, cpus)
	for i, c := range ticks {
		rows = append(rows, cpuCounter{id: fmt.Sprintf("cpu%d", i), busy: uint64(c[0]) + uint64(c[1]) + uint64(c[3]), idle: uint64(c[2]), parts: [8]uint64{uint64(c[0]), uint64(c[1]), uint64(c[2]), uint64(c[3])}})
	}
	runtime.KeepAlive(data)
	return rows, nil
}
func darwinMonitorMemory() model.ResourceMeasurement {
	return memoryAvailabilityValues(MemoryAvailability(context.Background()))
}
func darwinMonitorNetwork() ([]ioCounter, error) {
	if err := initMonitorMach(); err != nil {
		return nil, err
	}
	mib := [6]int32{unix.CTL_NET, unix.AF_ROUTE, 0, 0, unix.NET_RT_IFLIST2, 0}
	var size uintptr
	if monitorMach.sysctl(&mib[0], 6, nil, &size, nil, 0) != 0 || size == 0 || size > 1<<20 {
		return nil, fmt.Errorf("native interface buffer unavailable or exceeds byte limit")
	}
	data := make([]byte, int(size))
	if monitorMach.sysctl(&mib[0], 6, unsafe.Pointer(&data[0]), &size, nil, 0) != 0 || size > uintptr(len(data)) {
		return nil, fmt.Errorf("native interface buffer changed during observation")
	}
	runtime.KeepAlive(data)
	return parseDarwinMonitorNetwork(data[:size], time.Now())
}
func parseDarwinMonitorNetwork(data []byte, at time.Time) ([]ioCounter, error) {
	rows := []ioCounter{}
	for len(data) > 0 {
		if len(data) < 4 {
			return rows, fmt.Errorf("truncated network counter header")
		}
		n := int(binary.LittleEndian.Uint16(data[:2]))
		if n < 4 || n > len(data) {
			return rows, fmt.Errorf("invalid network counter length")
		}
		if data[2] != unix.RTM_VERSION {
			return rows, fmt.Errorf("unsupported network counter version")
		}
		if data[3] == unix.RTM_IFINFO2 {
			if len(rows) == monitorRows {
				return rows, fmt.Errorf("network observation limited to %d rows", monitorRows)
			}
			var h unix.IfMsghdr2
			if n < int(unsafe.Sizeof(h)) {
				return rows, fmt.Errorf("short native interface record")
			}
			copy(unsafe.Slice((*byte)(unsafe.Pointer(&h)), int(unsafe.Sizeof(h))), data[:n])
			// RTAX_IFP carries sockaddr_dl in this same atomic kernel buffer;
			// avoid a second inventory call racing interface replacement.
			name, hardware := "", ""
			offset := int(unsafe.Sizeof(h))
			for bit := uint(0); bit < 32; bit++ {
				if uint32(h.Addrs)&(1<<bit) == 0 {
					continue
				}
				if offset+2 > n {
					return rows, fmt.Errorf("truncated interface address")
				}
				length := int(data[offset])
				if length < 2 || offset+length > n {
					return rows, fmt.Errorf("invalid interface address length")
				}
				sa := data[offset : offset+length]
				if bit == unix.RTAX_IFP {
					if length < 8 || sa[1] != unix.AF_LINK || binary.LittleEndian.Uint16(sa[2:4]) != h.Index || 8+int(sa[5])+int(sa[6]) > length {
						return rows, fmt.Errorf("invalid interface identity")
					}
					name = string(sa[8 : 8+int(sa[5])])
					hardware = fmt.Sprintf("%x", sa[8+int(sa[5]):8+int(sa[5])+int(sa[6])])
				}
				offset += (length + 3) &^ 3
			}
			if name == "" {
				return rows, fmt.Errorf("interface identity unavailable")
			}
			rows = append(rows, ioCounter{id: fmt.Sprintf("darwin-interface:%d:%s:%s", h.Index, name, hardware), name: name, read: h.Data.Ibytes, write: h.Data.Obytes, at: at})
		}
		data = data[n:]
	}
	return rows, nil
}
func darwinMonitorDisk(data []byte, at time.Time) ([]ioCounter, error) {
	value, err := propertyList(data)
	if err != nil {
		return nil, err
	}
	array, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("disk provider did not report an array")
	}
	rows := []ioCounter{}
	for _, item := range array {
		if len(rows) == monitorRows {
			return rows, fmt.Errorf("disk observation limited to %d rows", monitorRows)
		}
		v, ok := item.(map[string]any)
		if !ok {
			return rows, fmt.Errorf("invalid disk driver record")
		}
		id, okID := v["IORegistryEntryID"].(uint64)
		stats, okStats := v["Statistics"].(map[string]any)
		read, okRead := stats["Bytes (Read)"].(uint64)
		write, okWrite := stats["Bytes (Write)"].(uint64)
		if !okID || !okStats || !okRead || !okWrite {
			return rows, fmt.Errorf("disk driver byte counters unavailable")
		}
		name, _ := v["IORegistryEntryName"].(string)
		rows = append(rows, ioCounter{id: fmt.Sprintf("darwin-block-driver:%d", id), name: fmt.Sprintf("%s (%d)", name, id), read: read, write: write, at: at})
	}
	return rows, nil
}
