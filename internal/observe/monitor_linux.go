package observe

import (
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

func readMonitorFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if len(b) > 1<<20 {
		return nil, fmt.Errorf("native observation exceeds byte limit")
	}
	return b, err
}
func collectMonitor(ctx context.Context) monitorRaw {
	r := monitorRaw{at: time.Now(), cpuMethod: "proc:/proc/stat", diskMethod: "proc:/proc/diskstats", networkMethod: "proc:/proc/net/dev"}
	if data, err := readMonitorFile(ctx, "/proc/stat"); err != nil {
		r.cpuError = err
	} else {
		r.cpu, r.cpuError = parseMonitorCPU(data)
	}
	if data, err := readMonitorFile(ctx, "/proc/meminfo"); err != nil {
		r.memory = unavailable("host-memory", "proc:meminfo", err.Error())
		r.swap = r.memory
	} else {
		r.memory = meminfo(data)
		values := map[string]uint64{}
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) == 3 && f[2] == "kB" {
				n, e := strconv.ParseUint(f[1], 10, 64)
				if e == nil && n <= math.MaxUint64/1024 {
					values[f[0]] = n * 1024
				}
			}
		}
		total, ok := values["SwapTotal:"]
		free, found := values["SwapFree:"]
		r.swap = unavailable("host-swap", "proc:meminfo", "swap counters unavailable")
		if ok && found {
			r.swap = memoryValues(total, free, "host-swap", "proc:meminfo", time.Now())
		}
	}
	if data, err := readMonitorFile(ctx, "/proc/diskstats"); err != nil {
		r.diskError = err
	} else {
		r.disk, r.diskError = parseMonitorDisk(data, time.Now())
		for i, c := range r.disk {
			f := strings.Split(c.id, ":")
			var st unix.Stat_t
			if e := unix.Stat("/sys/dev/block/"+f[1]+":"+f[2], &st); e != nil {
				r.diskError = fmt.Errorf("block-device identity unavailable")
				continue
			}
			r.disk[i].id = fmt.Sprintf("%s:%d:%d", c.id, st.Dev, st.Ino)
		}
	}
	if data, err := readMonitorFile(ctx, "/proc/net/dev"); err != nil {
		r.networkError = err
	} else {
		r.network, r.networkError = parseMonitorNetwork(data, time.Now())
		ifs, e := net.Interfaces()
		if e != nil {
			r.networkError = e
		} else {
			identities := map[string]net.Interface{}
			for _, iface := range ifs {
				identities[iface.Name] = iface
			}
			for i, c := range r.network {
				iface, ok := identities[c.name]
				if !ok {
					r.networkError = fmt.Errorf("network interface identity changed during observation")
					continue
				}
				r.network[i].id = fmt.Sprintf("%s:%d:%s", c.id, iface.Index, iface.HardwareAddr)
			}
		}
	}
	return r
}
func parseMonitorCPU(data []byte) ([]cpuCounter, error) {
	rows := []cpuCounter{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || !strings.HasPrefix(f[0], "cpu") || f[0] == "cpu" {
			continue
		}
		if len(rows) == monitorCPUs {
			return rows, fmt.Errorf("CPU observation limited to %d rows", monitorCPUs)
		}
		if _, e := strconv.ParseUint(strings.TrimPrefix(f[0], "cpu"), 10, 32); e != nil || len(f) < 5 {
			return rows, fmt.Errorf("invalid per-CPU record")
		}
		c := cpuCounter{id: f[0]}
		// Linux guest times are already included in user/nice; never add them.
		for i := 1; i < len(f) && i <= 8; i++ {
			n, e := strconv.ParseUint(f[i], 10, 64)
			if e != nil {
				return rows, fmt.Errorf("invalid CPU counter")
			}
			c.parts[i-1] = n
			if i == 4 || i == 5 {
				c.idle, e = checkedAdd(c.idle, n)
			} else {
				c.busy, e = checkedAdd(c.busy, n)
			}
			if e != nil {
				return rows, e
			}
		}
		rows = append(rows, c)
	}
	if len(rows) == 0 {
		return rows, fmt.Errorf("per-CPU counters unavailable")
	}
	return rows, nil
}
func parseMonitorDisk(data []byte, at time.Time) ([]ioCounter, error) {
	rows := []ioCounter{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(rows) == monitorRows {
			return rows, fmt.Errorf("disk observation limited to %d rows", monitorRows)
		}
		if len(f) < 14 {
			return rows, fmt.Errorf("invalid diskstats row")
		}
		major, e1 := strconv.ParseUint(f[0], 10, 32)
		minor, e2 := strconv.ParseUint(f[1], 10, 32)
		r, e3 := strconv.ParseUint(f[5], 10, 64)
		w, e4 := strconv.ParseUint(f[9], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || r > math.MaxUint64/512 || w > math.MaxUint64/512 {
			return rows, fmt.Errorf("invalid diskstats counter")
		}
		// diskstats uses fixed 512-byte sectors independent of device geometry.
		rows = append(rows, ioCounter{id: fmt.Sprintf("linux-block:%d:%d:%s", major, minor, f[2]), name: f[2], read: r * 512, write: w * 512, at: at})
	}
	return rows, nil
}
func parseMonitorNetwork(data []byte, at time.Time) ([]ioCounter, error) {
	rows := []ioCounter{}
	for _, line := range strings.Split(string(data), "\n") {
		name, tail, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if len(rows) == monitorRows {
			return rows, fmt.Errorf("network observation limited to %d rows", monitorRows)
		}
		f := strings.Fields(tail)
		if name == "" || len(f) != 16 {
			return rows, fmt.Errorf("invalid network counter row")
		}
		r, e1 := strconv.ParseUint(f[0], 10, 64)
		w, e2 := strconv.ParseUint(f[8], 10, 64)
		if e1 != nil || e2 != nil {
			return rows, fmt.Errorf("invalid network byte counter")
		}
		rows = append(rows, ioCounter{id: "linux-interface:" + name, name: name, read: r, write: w, at: at})
	}
	return rows, nil
}
