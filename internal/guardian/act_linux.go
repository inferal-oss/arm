package guardian

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

const (
	ioprioWhoProcess = 1
	ioprioClassShift = 13
	ioprioClassIdle  = 3
	lowNice          = 10
	lowNiceValue     = lowNice
)

func ioprioGet(pid int) (int, error) {
	r, _, e := unix.Syscall(unix.SYS_IOPRIO_GET, ioprioWhoProcess, uintptr(pid), 0)
	if e != 0 {
		return 0, e
	}
	return int(r), nil
}
func ioprioSet(pid, value int) error {
	_, _, e := unix.Syscall(unix.SYS_IOPRIO_SET, ioprioWhoProcess, uintptr(pid), uintptr(value))
	if e != 0 {
		return e
	}
	return nil
}

// threads lists a process's task IDs. Linux priorities are per thread, so
// changing only the main thread would leave existing workers untouched.
func threads(pid int) []int {
	entries, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(pid), "task"))
	if err != nil {
		return []int{pid}
	}
	out := []int{}
	for _, e := range entries {
		if tid, err := strconv.Atoi(e.Name()); err == nil {
			out = append(out, tid)
		}
	}
	return out
}

// lower sets the idle I/O class, which the owner can undo, and raises the
// nice value, which only a privileged process can lower again. Each thread
// changes; the main thread's previous values are recorded for restore.
func lower(id model.ProcessIdentity) (Priority, error) {
	var p Priority
	if err := revalidate(id); err != nil {
		return p, err
	}
	prevIO, ioErr := ioprioGet(id.PID)
	prio, niceErr := unix.Getpriority(unix.PRIO_PROCESS, id.PID)
	nice := 20 - prio // the raw system call returns 20-nice
	for _, tid := range threads(id.PID) {
		if ioErr == nil && ioprioSet(tid, ioprioClassIdle<<ioprioClassShift) == nil {
			p.IOPrio = &prevIO
		}
		if niceErr == nil && nice < lowNice && unix.Setpriority(unix.PRIO_PROCESS, tid, lowNice) == nil {
			p.Nice = &nice
		}
	}
	if p.IOPrio == nil && p.Nice == nil {
		return p, fmt.Errorf("priority unchanged")
	}
	return p, nil
}

func restore(id model.ProcessIdentity, p Priority) error {
	if err := sameProcess(id); err != nil {
		return err
	}
	var failed error
	kept := false
	for _, tid := range threads(id.PID) {
		if p.IOPrio != nil {
			if err := ioprioSet(tid, *p.IOPrio); err != nil {
				failed = err
			}
		}
		if p.Nice != nil {
			if err := unix.Setpriority(unix.PRIO_PROCESS, tid, *p.Nice); errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
				kept = true // expected for an unprivileged owner
			} else if err != nil {
				failed = err
			}
		}
	}
	if failed == nil && kept {
		return errNiceKept
	}
	return failed
}

// signal opens a process descriptor first, then revalidates, so PID reuse
// after the check cannot redirect the signal.
func signal(id model.ProcessIdentity, sig syscall.Signal) error {
	fd, err := unix.PidfdOpen(id.PID, 0)
	if err != nil {
		if err := revalidate(id); err != nil {
			return err
		}
		return unix.Kill(id.PID, sig)
	}
	defer unix.Close(fd)
	if err := revalidate(id); err != nil {
		return err
	}
	return unix.PidfdSendSignal(fd, sig, nil, 0)
}

// reclaim asks the kernel to page out a frozen service unit's memory. It
// applies only when the cgroup holds exactly the group's processes.
func reclaim(g *Group) (string, error) {
	if g.Kind != "service-unit" || len(g.Members) == 0 {
		return "", errUnsupported
	}
	dir := filepath.Join("/sys/fs/cgroup", g.Members[0].Cgroup)
	procs, err := os.ReadFile(filepath.Join(dir, "cgroup.procs"))
	if err != nil {
		return "", err
	}
	members := map[string]bool{}
	for _, m := range g.Members {
		members[strconv.Itoa(m.Identity.PID)] = true
	}
	for _, line := range splitLines(procs) {
		if !members[line] {
			return "", fmt.Errorf("cgroup contains processes outside the group")
		}
	}
	amount := strconv.FormatUint(g.Memory, 10)
	if err := os.WriteFile(filepath.Join(dir, "memory.reclaim"), []byte(amount), 0); err != nil {
		return "", err
	}
	return "requested reclaim of " + amount + " bytes from " + dir, nil
}

// inferredPriority is empty: previous Linux values are unknown, so nothing
// is restored from an intent without saved values.
func inferredPriority() Priority { return Priority{} }
