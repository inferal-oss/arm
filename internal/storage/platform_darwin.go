package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

func mountIdentity(fd int) (string, error) {
	var s unix.Statfs_t
	if err := unix.Fstatfs(fd, &s); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", s.Fsid.Val), nil
}

func durableIdentity(fd int, name string) (model.FileIdentity, error) {
	var s unix.Stat_t
	var err error
	if name == "" {
		err = unix.Fstat(fd, &s)
	} else {
		err = unix.Fstatat(fd, name, &s, unix.AT_SYMLINK_NOFOLLOW)
	}
	if err != nil {
		return model.FileIdentity{}, err
	}
	if s.Btim.Sec == 0 && s.Btim.Nsec == 0 {
		return model.FileIdentity{}, fmt.Errorf("creation-time identity unavailable; automatic managed cleanup unsupported")
	}
	id := Identity(&s)
	id.BornSeconds = s.Btim.Sec
	id.BornNanoseconds = s.Btim.Nsec
	id.BirthKnown = true
	return id, nil
}
func renameExclusive(fd int, from, to string) error {
	return unix.RenameatxNp(fd, from, fd, to, unix.RENAME_EXCL)
}

func mountAt(fd int, name string) (string, error) {
	child, err := unix.Openat(fd, name, unix.O_EVTONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	defer unix.Close(child)
	return mountIdentity(child)
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > (1<<20)-b.Len() {
		return 0, fmt.Errorf("activity output limit exceeded")
	}
	return b.Buffer.Write(p)
}
func activeUsers(ctx context.Context, path string, identities map[model.FileIdentity]bool) ([]int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-a", "-u", strconv.Itoa(os.Geteuid()), "+D", path, "-F", "pft")
	cmd.WaitDelay = 250 * time.Millisecond
	var out, stderr boundedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stderr.Len() > 0 {
		return nil, fmt.Errorf("open-file activity inspection reported warnings; cleanup refused")
	}
	if err != nil {
		var exit *exec.ExitError
		// +D expands into multiple search terms. Native lsof can return 1
		// when some candidates have no users even while other fields match.
		// Warnings, timeouts and other failures still make activity unknown.
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("open-file inspection failed: %w", err)
		}
	}
	active := map[int]bool{}
	pid := 0
	fd := ""
	for _, line := range strings.Split(out.String(), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			var err error
			pid, err = strconv.Atoi(line[1:])
			if err != nil || pid <= 0 {
				return nil, fmt.Errorf("unrecognized activity process identity")
			}
			fd = ""
		case 'f':
			fd = line[1:]
		case 't':
			// The provider itself pins directory descriptors. Its own regular
			// files, mappings and cwd still count as active use.
			_, numeric := strconv.Atoi(fd)
			if pid == os.Getpid() && numeric == nil && line[1:] == "DIR" {
				continue
			}
			if pid > 0 {
				active[pid] = true
			}
		default:
			return nil, fmt.Errorf("unrecognized activity field")
		}
	}
	result := []int{}
	for pid := range active {
		result = append(result, pid)
	}
	sort.Ints(result)
	return result, nil
}
