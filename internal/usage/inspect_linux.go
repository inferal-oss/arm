package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var pageSize = uint64(os.Getpagesize())

func inspect(pid int) (Sample, error) {
	id, v, err := process(pid)
	if err != nil {
		return Sample{}, err
	}
	s := Sample{Identity: id, PPID: v.ppid, Name: v.comm, Stopped: v.state == "T" || v.state == "t"}
	seconds := float64(v.user+v.system) / linuxNative.hz
	resident := uint64(v.rss) * pageSize
	s.CPUSeconds, s.Resident = &seconds, &resident
	// /proc/PID/io requires ptrace read access; it is optional evidence.
	if b, err := boundedRead(filepath.Join("/proc", strconv.Itoa(pid), "io"), 4<<10); err == nil {
		if read, write, ok := parseIO(b); ok {
			s.DiskRead, s.DiskWrite = &read, &write
		}
	}
	return s, nil
}

func parseIO(data []byte) (uint64, uint64, bool) {
	values := map[string]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return 0, 0, false
		}
		values[key] = n
	}
	read, ok1 := values["read_bytes"]
	write, ok2 := values["write_bytes"]
	return read, write, ok1 && ok2
}

func listPIDs(limit int) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	out := []int{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if len(out) == limit {
			return out, fmt.Errorf("process enumeration limited to %d processes", limit)
		}
		out = append(out, pid)
	}
	return out, nil
}
