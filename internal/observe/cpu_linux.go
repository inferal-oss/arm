package observe

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// CPU affinity is per-thread on Linux. Require the bounded live thread inventory
// to agree rather than treating an arbitrary Go thread as the process's mask.
func linuxCPUAffinity(ctx context.Context) ([]int, error) {
	membership, err := readMonitorFile(ctx, "/proc/self/cgroup")
	if err != nil {
		return nil, err
	}
	f, err := os.Open("/proc/self/task")
	if err != nil {
		return nil, err
	}
	entries, readErr := f.ReadDir(4097)
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || len(entries) == 0 || len(entries) > 4096 {
		return nil, fmt.Errorf("complete native thread affinity inventory unavailable")
	}
	var common []int
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tid, err := strconv.Atoi(e.Name())
		if err != nil || tid <= 0 {
			return nil, fmt.Errorf("invalid native thread identity")
		}
		threadMembership, err := readMonitorFile(ctx, fmt.Sprintf("/proc/self/task/%d/cgroup", tid))
		if err != nil || !sameCPUThreadMembership(membership, threadMembership) {
			return nil, fmt.Errorf("thread CPU cgroup scopes differ or changed")
		}
		if _, err := linuxCPUQuotaNamespaceFile(fmt.Sprintf("/proc/self/task/%d/ns/cgroup", tid)); err != nil {
			return nil, err
		}
		mask := unix.NewCPUSet(cpuObservationMaxCPUs)
		if err := unix.SchedGetaffinityDynamic(tid, mask); err != nil {
			return nil, fmt.Errorf("thread affinity changed or is unavailable: %w", err)
		}
		ids := []int{}
		for i := 0; i < cpuObservationMaxCPUs; i++ {
			if mask.IsSet(i) {
				ids = append(ids, i)
			}
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("empty native thread affinity")
		}
		if common == nil {
			common = ids
		} else if !equalCPUIds(common, ids) {
			return nil, fmt.Errorf("thread affinity masks differ; process CPU eligibility is unknown")
		}
	}
	return common, nil
}
func equalCPUIds(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type linuxCPUScope struct {
	ids       []int
	capacity  float64
	signature string
	quotas    []cpuQuotaCounter
}

func linuxCPUCurrentScope(ctx context.Context) (linuxCPUScope, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s := linuxCPUScope{}
	namespace, err := linuxCPUQuotaNamespace()
	if err != nil {
		return s, err
	}
	ids, err := linuxCPUAffinity(ctx)
	if err != nil {
		return s, err
	}
	s.ids, s.capacity = ids, float64(len(ids))
	membership, err := readMonitorFile(ctx, "/proc/self/cgroup")
	if err != nil {
		return s, err
	}
	mounts, err := readMonitorFile(ctx, "/proc/self/mountinfo")
	if err != nil {
		return s, err
	}
	limits, identity, err := linuxCPUQuotas(ctx, membership, mounts)
	if err != nil {
		return s, err
	}
	s.quotas = limits
	for _, q := range limits {
		s.capacity = math.Min(s.capacity, q.capacity)
	}
	s.signature = fmt.Sprintf("namespace:%s;affinity:%v;membership:%s;limits:%s", namespace, ids, membership, identity)
	return s, nil
}
func collectCPUAvailability(ctx context.Context) (cpuAvailabilityRaw, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r := cpuAvailabilityRaw{scope: "process-uniform-thread-affinity+complete-cgroup-ancestry", method: "proc:stat+sched_getaffinity+cgroup", detail: "Recent eligible-CPU idle time, capped by remaining CPU quota in each visible cgroup ancestor. Other workloads reduce headroom. Initial cgroup namespace and full hierarchy mounts are verified; this estimate is not a scheduler guarantee."}
	before, err := linuxCPUCurrentScope(ctx)
	if err != nil {
		return r, err
	}
	data, err := readMonitorFile(ctx, "/proc/stat")
	if err != nil {
		return r, err
	}
	all, err := parseAdmissionCPU(data)
	if err != nil {
		return r, err
	}
	if err := validateCPURows(all); err != nil {
		return r, err
	}
	byID := map[string]cpuCounter{}
	for _, c := range all {
		byID[c.id] = c
	}
	for _, id := range before.ids {
		c, ok := byID[fmt.Sprintf("cpu%d", id)]
		if !ok {
			return r, fmt.Errorf("eligible CPU has no native counter; topology changed")
		}
		// The display monitor treats iowait as idle. Admission counts it as
		// unavailable, together with steal; Linux documents iowait as unreliable.
		if c.idle < c.parts[4] {
			return r, fmt.Errorf("invalid native I/O wait counter")
		}
		c.idle -= c.parts[4]
		c.busy, err = checkedAdd(c.busy, c.parts[4])
		if err != nil {
			return r, err
		}
		r.rows = append(r.rows, c)
	}
	after, err := linuxCPUCurrentScope(ctx)
	if err != nil {
		return r, err
	}
	if before.signature != after.signature {
		return r, fmt.Errorf("CPU affinity or cgroup limits changed during observation")
	}
	r.at, r.capacity, r.signature, r.quotas = time.Now(), after.capacity, after.signature, after.quotas
	return r, nil
}

type cpuCgroupMount struct {
	root, path, kind string
	controllers      map[string]bool
}

func cpuMountPath(s string) (string, error) {
	// mountinfo escapes these four bytes. Reject all other backslash escapes.
	for _, p := range []struct{ from, to string }{{`\040`, " "}, {`\011`, "\t"}, {`\012`, "\n"}, {`\134`, `\`}} {
		s = strings.ReplaceAll(s, p.from, p.to)
	}
	if strings.Contains(s, `\`) || !filepath.IsAbs(s) || filepath.Clean(s) != s {
		return "", fmt.Errorf("invalid cgroup mount path")
	}
	return s, nil
}
func parseCPUCgroupMounts(data []byte) ([]cpuCgroupMount, error) {
	mounts := []cpuCgroupMount{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		sep := -1
		for i, x := range f {
			if x == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+3 >= len(f) {
			continue
		}
		kind := f[sep+1]
		if kind != "cgroup" && kind != "cgroup2" {
			continue
		}
		if len(mounts) >= 64 || sep < 6 {
			return nil, fmt.Errorf("cgroup mount inventory exceeds bound or is invalid")
		}
		root, e := cpuMountPath(f[3])
		if e != nil {
			return nil, e
		}
		path, e := cpuMountPath(f[4])
		if e != nil {
			return nil, e
		}
		m := cpuCgroupMount{root: root, path: path, kind: kind, controllers: map[string]bool{}}
		for _, c := range strings.Split(f[sep+3], ",") {
			m.controllers[c] = true
		}
		mounts = append(mounts, m)
	}
	return mounts, nil
}
func parseCPUCgroupMembership(data []byte) (map[string]string, error) {
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		f := strings.SplitN(line, ":", 3)
		if len(f) != 3 {
			return nil, fmt.Errorf("invalid cgroup membership")
		}
		path := f[2]
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, fmt.Errorf("invalid cgroup membership path")
		}
		controllers := strings.Split(f[1], ",")
		if f[0] == "0" && f[1] == "" {
			controllers = []string{"v2"}
		}
		for _, c := range controllers {
			if _, ok := out[c]; ok {
				return nil, fmt.Errorf("duplicate cgroup controller membership")
			}
			out[c] = path
		}
	}
	return out, nil
}
func linuxCPUQuotas(ctx context.Context, membership, mountinfo []byte) ([]cpuQuotaCounter, string, error) {
	members, err := parseCPUCgroupMembership(membership)
	if err != nil {
		return nil, "", err
	}
	mounts, err := parseCPUCgroupMounts(mountinfo)
	if err != nil {
		return nil, "", err
	}
	quotas := []cpuQuotaCounter{}
	identities := []string{}
	found := map[string]bool{}
	for _, m := range mounts {
		controller := "cpu"
		if m.kind == "cgroup2" {
			controller = "v2"
		} else if !m.controllers["cpu"] {
			continue
		}
		member, ok := members[controller]
		if !ok {
			continue
		}
		if m.root != "/" {
			return nil, "", fmt.Errorf("CPU cgroup mount hides quota ancestors")
		}
		rel, err := filepath.Rel(m.root, member)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return nil, "", fmt.Errorf("CPU cgroup membership cannot be mapped to the complete hierarchy")
		}
		if found[controller] {
			return nil, "", fmt.Errorf("ambiguous CPU cgroup mounts")
		}
		found[controller] = true
		current := filepath.Join(m.path, rel)
		for depth := 0; ; depth++ {
			if depth >= 128 {
				return nil, "", fmt.Errorf("CPU cgroup hierarchy exceeds observation bound")
			}
			var stat unix.Stat_t
			if err := unix.Stat(current, &stat); err != nil {
				return nil, "", err
			}
			identity := fmt.Sprintf("%s:%d:%d", current, stat.Dev, stat.Ino)
			var capacity float64
			finite := false
			if controller == "v2" {
				data, e := readMonitorFile(ctx, filepath.Join(current, "cpu.max"))
				if e != nil && !os.IsNotExist(e) {
					return nil, "", e
				}
				if e == nil {
					capacity, finite, err = parseCPUQuota(data, true)
					if err != nil {
						return nil, "", err
					}
				}
			} else {
				q, e := readMonitorFile(ctx, filepath.Join(current, "cpu.cfs_quota_us"))
				if e != nil {
					return nil, "", e
				}
				p, e := readMonitorFile(ctx, filepath.Join(current, "cpu.cfs_period_us"))
				if e != nil {
					return nil, "", e
				}
				capacity, finite, err = parseCPUQuota([]byte(strings.TrimSpace(string(q))+" "+strings.TrimSpace(string(p))), false)
				if err != nil {
					return nil, "", err
				}
			}
			identities = append(identities, fmt.Sprintf("%s:%t:%g", identity, finite, capacity))
			if finite {
				name := "cpu.stat"
				if controller == "cpu" {
					if !m.controllers["cpuacct"] || members["cpuacct"] != member {
						return nil, "", fmt.Errorf("finite CPU quota has no matching ancestor usage scope")
					}
					name = "cpuacct.usage"
				}
				started := time.Now()
				data, e := readMonitorFile(ctx, filepath.Join(current, name))
				ended := time.Now()
				if e != nil {
					return nil, "", e
				}
				used, e := parseCPUQuotaUsage(data, controller == "v2")
				if e != nil {
					return nil, "", e
				}
				quotas = append(quotas, cpuQuotaCounter{id: identity, capacity: capacity, used: used, started: started, ended: ended})
			}
			if current == m.path {
				break
			}
			parent := filepath.Dir(current)
			if parent == current {
				return nil, "", fmt.Errorf("CPU cgroup escaped visible hierarchy")
			}
			current = parent
		}
	}
	for _, c := range []string{"cpu", "v2"} {
		if _, ok := members[c]; ok && !found[c] {
			return nil, "", fmt.Errorf("CPU cgroup hierarchy cannot be mapped to a visible mount")
		}
	}
	sort.Slice(quotas, func(i, j int) bool { return quotas[i].id < quotas[j].id })
	sort.Strings(identities)
	return quotas, strings.Join(identities, ";"), nil
}
func parseCPUQuota(data []byte, v2 bool) (float64, bool, error) {
	f := strings.Fields(string(data))
	if len(f) != 2 {
		return 0, false, fmt.Errorf("invalid CPU quota")
	}
	period, e := strconv.ParseUint(f[1], 10, 64)
	if e != nil || period == 0 {
		return 0, false, fmt.Errorf("invalid CPU quota period")
	}
	if v2 && f[0] == "max" || !v2 && f[0] == "-1" {
		return 0, false, nil
	}
	quota, e := strconv.ParseUint(f[0], 10, 64)
	if e != nil || quota == 0 {
		return 0, false, fmt.Errorf("invalid CPU quota capacity")
	}
	return float64(quota) / float64(period), true, nil
}
func parseCPUQuotaUsage(data []byte, v2 bool) (uint64, error) {
	if !v2 {
		n, e := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		return n, e
	}
	found := false
	var used uint64
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "usage_usec" {
			continue
		}
		if found || len(f) != 2 {
			return 0, fmt.Errorf("invalid CPU ancestor usage counter")
		}
		n, e := strconv.ParseUint(f[1], 10, 64)
		if e != nil || n > math.MaxUint64/1000 {
			return 0, fmt.Errorf("CPU ancestor usage counter overflow")
		}
		used = n * 1000
		found = true
	}
	if !found {
		return 0, fmt.Errorf("CPU ancestor usage counter unavailable")
	}
	return used, nil
}

// Admission must see iowait, interrupt and steal fields; a shortened display
// record cannot establish that the missing activity was zero.
func parseAdmissionCPU(data []byte) ([]cpuCounter, error) {
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && strings.HasPrefix(f[0], "cpu") && f[0] != "cpu" && len(f) < 9 {
			return nil, fmt.Errorf("complete per-CPU activity counters unavailable")
		}
	}
	rows, err := parseCPU(data, cpuObservationMaxCPUs)
	if err == nil {
		err = validateCPURows(rows)
	}
	return rows, err
}

// Linux UAPI include/uapi/linux/nsfs.h reserves this inode for the initial
// cgroup namespace. PID 1 may itself be container-local, so comparing against
// /proc/1/ns/cgroup or trusting mountinfo's '/' alone is insufficient.
const initialCgroupNamespaceInode = 0xEFFFFFFB

func cpuQuotaNamespaceIdentity(device, inode uint64, filesystem int64, kind int) (string, error) {
	if filesystem != unix.NSFS_MAGIC || kind != unix.CLONE_NEWCGROUP || inode != initialCgroupNamespaceInode {
		return "", fmt.Errorf("complete CPU quota ancestry requires the verified initial cgroup namespace")
	}
	return fmt.Sprintf("%d:%d:%d", device, inode, kind), nil
}
func linuxCPUQuotaNamespace() (string, error) {
	identities := []string{}
	// The process leader and current collection thread must both be initial.
	// The caller holds its OS thread through the complete native collection.
	for _, path := range []string{"/proc/self/ns/cgroup", "/proc/thread-self/ns/cgroup"} {
		identity, err := linuxCPUQuotaNamespaceFile(path)
		if err != nil {
			return "", err
		}
		identities = append(identities, identity)
	}
	return strings.Join(identities, ";"), nil
}

func sameCPUThreadMembership(leader, thread []byte) bool { return bytes.Equal(leader, thread) }
func linuxCPUQuotaNamespaceFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("CPU cgroup namespace proof unavailable: %w", err)
	}
	var stat unix.Stat_t
	var fs unix.Statfs_t
	err = unix.Fstat(int(f.Fd()), &stat)
	if err == nil {
		err = unix.Fstatfs(int(f.Fd()), &fs)
	}
	if err == nil && fs.Type != unix.NSFS_MAGIC {
		err = fmt.Errorf("CPU cgroup namespace handle is not NSFS")
	}
	kind := 0
	if err == nil {
		kind, err = unix.IoctlRetInt(int(f.Fd()), unix.NS_GET_NSTYPE)
	}
	closeErr := f.Close()
	if err != nil {
		return "", fmt.Errorf("CPU cgroup namespace proof unavailable: %w", err)
	}
	if closeErr != nil {
		return "", closeErr
	}
	identity, err := cpuQuotaNamespaceIdentity(uint64(stat.Dev), stat.Ino, fs.Type, kind)
	if err != nil {
		return "", err
	}
	return identity, nil
}
