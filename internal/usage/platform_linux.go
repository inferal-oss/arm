package usage

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

var linuxNative struct {
	once      sync.Once
	namespace string
	hz        float64
	err       error
}

func boundedRead(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("process metadata exceeds its size limit")
	}
	return b, err
}
func clockTicks(data []byte) (float64, error) {
	word := strconv.IntSize / 8
	read := func(b []byte) uint64 {
		if word == 8 {
			return binary.NativeEndian.Uint64(b)
		}
		return uint64(binary.NativeEndian.Uint32(b))
	}
	if len(data)%(2*word) != 0 {
		return 0, fmt.Errorf("auxiliary vector has invalid size")
	}
	for i := 0; i < len(data); i += 2 * word {
		tag, value := read(data[i:]), read(data[i+word:])
		if tag == 17 && value > 0 {
			return float64(value), nil
		}
		if tag == 0 {
			break
		}
	}
	return 0, fmt.Errorf("AT_CLKTCK is unavailable")
}
func linuxInit() error {
	linuxNative.once.Do(func() {
		b, err := boundedRead("/proc/self/auxv", 16<<10)
		if err != nil {
			linuxNative.err = err
			return
		}
		linuxNative.hz, err = clockTicks(b)
		if err != nil {
			linuxNative.err = err
			return
		}
		boot, err := boundedRead("/proc/sys/kernel/random/boot_id", 128)
		if err != nil {
			linuxNative.err = err
			return
		}
		var st unix.Stat_t
		if err = unix.Stat("/proc/self/ns/pid", &st); err != nil {
			linuxNative.err = err
			return
		}
		linuxNative.namespace = fmt.Sprintf("linux:boot:%s:pidns:%d", strings.TrimSpace(string(boot)), st.Ino)
	})
	return linuxNative.err
}

type statValues struct {
	pgid         int
	start        string
	user, system uint64
	rss          int64
	state        string
}

func parseStat(data []byte) (statValues, error) {
	var v statValues
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return v, fmt.Errorf("process stat identity is malformed")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 22 {
		return v, fmt.Errorf("process stat counters are incomplete")
	}
	v.state = fields[0]
	var err error
	// A group outside this PID namespace is reported as zero. It cannot
	// match the positive captured group, but is not malformed metadata.
	if v.pgid, err = strconv.Atoi(fields[2]); err != nil || v.pgid < 0 {
		return v, fmt.Errorf("process group is unavailable")
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return v, err
	}
	v.start = strconv.FormatUint(start, 10)
	if v.user, err = strconv.ParseUint(fields[11], 10, 64); err != nil {
		return v, err
	}
	if v.system, err = strconv.ParseUint(fields[12], 10, 64); err != nil {
		return v, err
	}
	if v.rss, err = strconv.ParseInt(fields[21], 10, 64); err != nil || v.rss < 0 {
		return v, fmt.Errorf("resident page counter is invalid")
	}
	return v, nil
}
func process(pid int) (model.ProcessIdentity, statValues, error) {
	var id model.ProcessIdentity
	var values statValues
	if err := linuxInit(); err != nil {
		return id, values, err
	}
	path := filepath.Join("/proc", strconv.Itoa(pid))
	b, err := boundedRead(filepath.Join(path, "stat"), 64<<10)
	if err != nil {
		return id, values, err
	}
	values, err = parseStat(b)
	if err != nil {
		return id, values, err
	}
	var st unix.Stat_t
	if err = unix.Stat(path, &st); err != nil {
		return id, values, err
	}
	id = model.ProcessIdentity{PID: pid, PGID: values.pgid, UID: strconv.FormatUint(uint64(st.Uid), 10), Start: "linux:ticks:" + values.start, Namespace: linuxNative.namespace}
	return id, values, nil
}
func capture(pid int) (model.ProcessIdentity, error) { id, _, err := process(pid); return id, err }
func privateResident(data []byte) (uint64, error) {
	values := map[string]uint64{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			continue
		}
		if fields[0] != "Private_Clean:" && fields[0] != "Private_Dirty:" {
			continue
		}
		if fields[2] != "kB" {
			return 0, fmt.Errorf("unknown private memory unit")
		}
		n, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || n > math.MaxUint64/1024 {
			return 0, fmt.Errorf("invalid private memory counter")
		}
		if _, ok := values[fields[0]]; ok {
			return 0, fmt.Errorf("duplicate private memory counter")
		}
		values[fields[0]] = n * 1024
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	clean, ok1 := values["Private_Clean:"]
	dirty, ok2 := values["Private_Dirty:"]
	if !ok1 || !ok2 || dirty > math.MaxUint64-clean {
		return 0, fmt.Errorf("private memory counters unavailable or overflowing")
	}
	return clean + dirty, nil
}
func collect(ctx context.Context, s *Snapshot) {
	s.Method = "proc stat and smaps_rollup with process-start identities"
	s.MemoryMetric = "sum-private-resident"
	s.Detail = "Private_Clean plus Private_Dirty; excludes shared mappings, swap and memory absent from these counters. Non-atomic live group observation."
	directory, err := os.Open("/proc")
	if err != nil {
		s.Complete = false
		s.Detail += "; process enumeration unavailable"
		return
	}
	defer directory.Close()
	visited := 0
	unavailable := 0
	firstError := ""
	defer func() {
		if unavailable > 0 {
			s.Detail += fmt.Sprintf("; %d process identities unavailable (first: %s)", unavailable, firstError)
		}
	}()
	for {
		names, readErr := directory.Readdirnames(256)
		for _, name := range names {
			if ctx.Err() != nil {
				s.Complete = false
				s.Detail += "; collection cancelled"
				return
			}
			pid, err := strconv.Atoi(name)
			if err != nil || pid <= 0 {
				continue
			}
			visited++
			if visited > 32768 {
				s.Complete = false
				s.Detail += "; process scan limit reached"
				return
			}
			before, values, err := process(pid)
			if err != nil {
				if os.IsNotExist(err) {
					// Already exited processes are outside the live-member scope.
					continue
				}
				s.Complete = false
				unavailable++
				if firstError == "" {
					firstError = err.Error()
				}
				continue
			}
			if before.PGID != s.Root.PGID {
				continue
			}
			if before.UID != s.Root.UID {
				s.Complete = false
				continue
			}
			if values.state == "Z" || values.state == "X" {
				continue
			}
			if len(s.Members) == MaxMembers {
				s.Complete = false
				s.Detail += "; group member limit reached"
				return
			}
			p := model.ProcessUsage{Identity: before, MemoryMetric: s.MemoryMetric}
			raw, err := boundedRead(filepath.Join("/proc", name, "smaps_rollup"), 128<<10)
			if err == nil {
				if n, e := privateResident(raw); e == nil {
					p.AccountedMemoryBytes = &n
				} else {
					p.Detail = "private memory counters invalid"
				}
			}
			if p.AccountedMemoryBytes == nil {
				s.Complete = false
				p.Detail = "private memory counters unavailable"
			}
			after, counters, err := process(pid)
			if err != nil || after != before {
				s.Complete = false
				continue
			}
			seconds := (float64(counters.user) + float64(counters.system)) / linuxNative.hz
			p.CPUSeconds = &seconds
			if uint64(counters.rss) <= math.MaxUint64/uint64(os.Getpagesize()) {
				n := uint64(counters.rss) * uint64(os.Getpagesize())
				p.ResidentBytes = &n
			}
			s.Members = append(s.Members, p)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			s.Complete = false
			s.Detail += "; process enumeration incomplete"
			break
		}
	}
	if len(s.Members) == 0 {
		s.Complete = false
		s.Detail += "; no member counters were available"
	}
}
