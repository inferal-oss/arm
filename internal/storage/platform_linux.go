package storage

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

func mountIdentity(fd int) (string, error) {
	var s unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &s); err != nil {
		return "", err
	}
	if s.Mask&unix.STATX_MNT_ID == 0 {
		return "", fmt.Errorf("kernel does not report mount identity; cleanup unavailable")
	}
	return strconv.FormatUint(s.Mnt_id, 10), nil
}

func durableIdentity(fd int, name string) (model.FileIdentity, error) {
	flags := unix.AT_SYMLINK_NOFOLLOW
	if name == "" {
		flags |= unix.AT_EMPTY_PATH
	}
	var s unix.Statx_t
	if err := unix.Statx(fd, name, flags, unix.STATX_BASIC_STATS|unix.STATX_BTIME, &s); err != nil {
		return model.FileIdentity{}, err
	}
	if s.Mask&unix.STATX_BTIME == 0 {
		return model.FileIdentity{}, fmt.Errorf("filesystem creation-time identity unavailable; automatic managed cleanup unsupported")
	}
	return model.FileIdentity{Device: unix.Mkdev(s.Dev_major, s.Dev_minor), Inode: s.Ino, Owner: s.Uid, BornSeconds: s.Btime.Sec, BornNanoseconds: int64(s.Btime.Nsec), BirthKnown: true}, nil
}
func renameExclusive(fd int, from, to string) error {
	return unix.Renameat2(fd, from, fd, to, unix.RENAME_NOREPLACE)
}

func mountAt(fd int, name string) (string, error) {
	var s unix.Statx_t
	if err := unix.Statx(fd, name, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &s); err != nil {
		return "", err
	}
	if s.Mask&unix.STATX_MNT_ID == 0 {
		return "", fmt.Errorf("mount identity unavailable")
	}
	return strconv.FormatUint(s.Mnt_id, 10), nil
}
func activityIdentity(path string) (model.FileIdentity, bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return model.FileIdentity{}, false, nil
	}
	if err != nil {
		return model.FileIdentity{}, false, err
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return model.FileIdentity{}, false, fmt.Errorf("process file identity unavailable")
	}
	return model.FileIdentity{Device: uint64(s.Dev), Inode: s.Ino, Owner: s.Uid}, info.IsDir(), nil
}
func activeUsers(ctx context.Context, path string, identities map[model.FileIdentity]bool) ([]int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	active := map[int]bool{}
	for _, entry := range processes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		base := filepath.Join("/proc", entry.Name())
		id, _, err := activityIdentity(base)
		if err != nil {
			return nil, err
		}
		if id.Owner != uint32(os.Geteuid()) || id.Inode == 0 {
			continue
		}
		for _, name := range []string{"cwd", "exe", "root"} {
			id, _, err := activityIdentity(filepath.Join(base, name))
			if err != nil {
				return nil, err
			}
			if identities[id] {
				active[pid] = true
			}
		}
		fds, err := os.ReadDir(filepath.Join(base, "fd"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, fd := range fds {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			id, isDir, err := activityIdentity(filepath.Join(base, "fd", fd.Name()))
			if err != nil {
				return nil, err
			}
			if identities[id] && !(pid == os.Getpid() && isDir) {
				active[pid] = true
			}
		}
		maps, err := os.Open(filepath.Join(base, "maps"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// Mappings can retain an allocation after its descriptor closes.
		scanner := bufio.NewScanner(io.LimitReader(maps, 4<<20+1))
		scanner.Buffer(make([]byte, 4096), 1<<20)
		size := 0
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				maps.Close()
				return nil, err
			}
			size += len(scanner.Bytes()) + 1
			fields := strings.Fields(scanner.Text())
			if len(fields) < 5 {
				continue
			}
			dev := strings.Split(fields[3], ":")
			if len(dev) != 2 {
				continue
			}
			major, e1 := strconv.ParseUint(dev[0], 16, 32)
			minor, e2 := strconv.ParseUint(dev[1], 16, 32)
			inode, e3 := strconv.ParseUint(fields[4], 10, 64)
			if e1 != nil || e2 != nil || e3 != nil {
				maps.Close()
				return nil, fmt.Errorf("unrecognized process mapping")
			}
			id := model.FileIdentity{Device: unix.Mkdev(uint32(major), uint32(minor)), Inode: inode, Owner: uint32(os.Geteuid())}
			if inode != 0 && identities[id] {
				active[pid] = true
			}
		}
		err = scanner.Err()
		maps.Close()
		if err != nil {
			return nil, err
		}
		if size >= 4<<20 {
			return nil, fmt.Errorf("process mapping inspection exceeds limit")
		}
	}
	result := []int{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for pid := range active {
		result = append(result, pid)
	}
	sort.Ints(result)
	return result, nil
}
