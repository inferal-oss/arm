package agentstatus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"getarm.org/internal/local"

	"golang.org/x/sys/unix"
)

// Store is independent of the resource database. Its lock spans bounded
// read/modify/atomic-publication operations, never messaging or model calls.
type Store struct {
	mu       sync.Mutex
	dir      string
	identity os.FileInfo
}

func OpenStore(home string) (*Store, error) {
	dir := filepath.Join(home, "status-v1")
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("status directory unavailable")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return nil, fmt.Errorf("status directory must be an owned private regular directory")
	}
	return &Store{dir: dir, identity: info}, nil
}
func checkedFile(fd int) (*os.File, error) {
	f := os.NewFile(uintptr(fd), "status record")
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		f.Close()
		return nil, fmt.Errorf("status file must be an owned private regular file")
	}
	return f, nil
}
func (s *Store) Update(change func(*State) error) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var empty State
	info, err := os.Lstat(s.dir)
	if err != nil || !os.SameFile(s.identity, info) || !info.IsDir() || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return empty, fmt.Errorf("status directory identity changed")
	}
	dirfd, err := unix.Open(s.dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return empty, fmt.Errorf("status directory unavailable")
	}
	defer unix.Close(dirfd)
	var stat unix.Stat_t
	if unix.Fstat(dirfd, &stat) != nil || stat.Ino != info.Sys().(*syscall.Stat_t).Ino || uint64(stat.Dev) != uint64(info.Sys().(*syscall.Stat_t).Dev) {
		return empty, fmt.Errorf("status directory identity changed")
	}
	fd, err := unix.Openat(dirfd, "lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return empty, fmt.Errorf("status lock unavailable")
	}
	lock, err := checkedFile(fd)
	if err != nil {
		return empty, err
	}
	defer lock.Close()
	if err = unix.Flock(fd, unix.LOCK_EX); err != nil {
		return empty, fmt.Errorf("status lock unavailable")
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	copyFD, e := duplicatePrivateFD(dirfd)
	if e != nil {
		return empty, fmt.Errorf("status directory inspection unavailable")
	}
	directory := os.NewFile(uintptr(copyFD), "status directory")
	names, readErr := directory.Readdirnames(4)
	directory.Close()
	if readErr != nil && readErr != io.EOF {
		return empty, fmt.Errorf("status directory inspection unavailable")
	}
	for _, name := range names {
		if name != "lock" && name != "state.json" {
			return empty, fmt.Errorf("status directory contains interrupted publication or unrecognized evidence; preserved for inspection")
		}
	}
	state := NewState()
	fd, err = unix.Openat(dirfd, "state.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err == nil {
		f, e := checkedFile(fd)
		if e != nil {
			return empty, e
		}
		raw, e := io.ReadAll(io.LimitReader(f, MaxStateBytes+1))
		f.Close()
		if e != nil || len(raw) > MaxStateBytes {
			return empty, fmt.Errorf("status state exceeds bound or cannot be read")
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		state = State{}
		if dec.Decode(&state) != nil || dec.Decode(new(any)) != io.EOF || state.Validate() != nil {
			return empty, fmt.Errorf("unsupported or malformed status state; preserved")
		}
		if state.Entries == nil || state.Attempts == nil || state.Config.Revision < 0 || state.DailyCalls < 0 {
			return empty, fmt.Errorf("incomplete status state; preserved")
		}
	} else if err != unix.ENOENT {
		return empty, fmt.Errorf("status state unavailable")
	}
	if change == nil {
		return state, nil
	}
	if err = change(&state); err != nil {
		return empty, err
	}
	if err = state.Validate(); err != nil {
		return empty, err
	}
	raw, err := json.Marshal(state)
	reserved := 0
	for _, attempt := range state.Attempts {
		if attempt.ReservedBytes < 0 || attempt.ReservedBytes > FinalizationReserve {
			return empty, fmt.Errorf("invalid status finalization reserve")
		}
		reserved += attempt.ReservedBytes
	}
	if err != nil || len(raw)+reserved > MaxStateBytes || len(state.Entries) > MaxIdentities || len(state.Attempts) > MaxAttempts {
		return empty, fmt.Errorf("status capacity reached; no evidence was discarded")
	}
	// Exclusive temporary files and descriptor-relative rename avoid following
	// attacker-controlled leaf aliases. The existing state is never truncated.
	name := ".state-" + local.ID()
	fd, err = unix.Openat(dirfd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return empty, fmt.Errorf("status publication unavailable")
	}
	f := os.NewFile(uintptr(fd), "status publication")
	defer unix.Unlinkat(dirfd, name, 0)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return empty, fmt.Errorf("status publication failed")
	}
	// Refuse directory replacement during the publication attempt.
	current, e := os.Lstat(s.dir)
	if e != nil || !os.SameFile(s.identity, current) {
		return empty, fmt.Errorf("status directory changed before publication")
	}
	var heldLock, namedLock unix.Stat_t
	if unix.Fstat(int(lock.Fd()), &heldLock) != nil || unix.Fstatat(dirfd, "lock", &namedLock, unix.AT_SYMLINK_NOFOLLOW) != nil || heldLock.Ino != namedLock.Ino || heldLock.Dev != namedLock.Dev || namedLock.Nlink != 1 || namedLock.Mode&unix.S_IFMT != unix.S_IFREG || namedLock.Uid != uint32(os.Getuid()) || namedLock.Mode&0777 != 0600 {
		return empty, fmt.Errorf("status lock identity or ownership changed")
	}
	if err = unix.Renameat(dirfd, name, dirfd, "state.json"); err != nil {
		return empty, fmt.Errorf("status publication failed")
	}
	if err = unix.Fsync(dirfd); err != nil {
		return empty, fmt.Errorf("status publication durability uncertain")
	}
	return state, nil
}
