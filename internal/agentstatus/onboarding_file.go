package agentstatus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"getarm.org/internal/local"
	"golang.org/x/sys/unix"
)

func (s OnboardingStore) directory(create bool) (int, os.FileInfo, error) {
	if !filepath.IsAbs(s.Home) {
		return -1, nil, fmt.Errorf("status setup requires an absolute selected ARM home")
	}
	dir := filepath.Join(s.Home, "status-onboarding-v1")
	if create {
		if e := os.Mkdir(dir, 0700); e != nil && !os.IsExist(e) {
			return -1, nil, fmt.Errorf("status setup directory unavailable")
		}
	}
	info, e := os.Lstat(dir)
	if os.IsNotExist(e) && !create {
		return -1, nil, nil
	}
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return -1, nil, fmt.Errorf("status setup directory must be owned and private; evidence preserved")
	}
	fd, e := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return -1, nil, fmt.Errorf("status setup directory unavailable")
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Ino != info.Sys().(*syscall.Stat_t).Ino || uint64(st.Dev) != uint64(info.Sys().(*syscall.Stat_t).Dev) {
		unix.Close(fd)
		return -1, nil, fmt.Errorf("status setup directory identity changed")
	}
	return fd, info, nil
}
func onboardingReadFD(fd int) (OnboardingState, error) {
	state := onboardingEmpty()
	copy, e := duplicatePrivateFD(fd)
	if e != nil {
		return state, fmt.Errorf("status setup inspection unavailable")
	}
	directory := os.NewFile(uintptr(copy), "status setup directory")
	names, readErr := directory.Readdirnames(4)
	directory.Close()
	if readErr != nil && readErr != io.EOF {
		return state, fmt.Errorf("status setup inspection unavailable")
	}
	for _, name := range names {
		if name != "lock" && name != "state.json" {
			return state, fmt.Errorf("status setup has active, interrupted or unrecognized publication evidence; preserved")
		}
	}
	input, e := unix.Openat(fd, "state.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e == unix.ENOENT {
		return state, nil
	}
	if e != nil {
		return state, fmt.Errorf("status setup snapshot unavailable")
	}
	f, e := checkedFile(input)
	if e != nil {
		return state, e
	}
	raw, e := io.ReadAll(io.LimitReader(f, OnboardingMaxBytes+1))
	f.Close()
	if e != nil || len(raw) > OnboardingMaxBytes {
		return state, fmt.Errorf("status setup snapshot exceeds its bound or cannot be read")
	}
	state = OnboardingState{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF || state.validate() != nil {
		return state, fmt.Errorf("unsupported or malformed status setup snapshot; preserved")
	}
	reserved := 0
	for _, record := range state.Operations {
		reserved += record.ReservedBytes
	}
	if len(raw)+reserved > OnboardingMaxBytes {
		return state, fmt.Errorf("status setup finalization reserve exceeds its document bound; preserved")
	}
	return state, nil
}
func (s OnboardingStore) read() (OnboardingState, error) {
	fd, _, e := s.directory(false)
	if e != nil {
		return onboardingEmpty(), e
	}
	if fd < 0 {
		return onboardingEmpty(), nil
	}
	defer unix.Close(fd)
	return onboardingReadFD(fd)
}

// update returns whether an original publication attempt began. After that
// boundary callers preserve the key and an outcome-unknown error on failure.
func (s OnboardingStore) update(change func(*OnboardingState) error) (bool, error) {
	fd, identity, e := s.directory(true)
	if e != nil {
		return false, e
	}
	defer unix.Close(fd)
	lockFD, e := unix.Openat(fd, "lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if e != nil {
		return false, fmt.Errorf("status setup lock unavailable")
	}
	lock, e := checkedFile(lockFD)
	if e != nil {
		return false, e
	}
	defer lock.Close()
	if unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return false, fmt.Errorf("status setup is busy; retain the original request and key")
	}
	defer unix.Flock(lockFD, unix.LOCK_UN)
	state, e := onboardingReadFD(fd)
	if e != nil {
		return false, e
	}
	before, _ := json.Marshal(state)
	if e = change(&state); e != nil {
		return false, e
	}
	if e = state.validate(); e != nil {
		return false, e
	}
	raw, e := json.Marshal(state)
	reserved := 0
	for _, record := range state.Operations {
		reserved += record.ReservedBytes
	}
	if e != nil || len(raw)+reserved > OnboardingMaxBytes {
		return false, fmt.Errorf("status setup document capacity reached; no retained outcome discarded")
	}
	if bytes.Equal(before, raw) {
		return false, nil
	}
	name := ".state-" + local.ID()
	output, e := unix.Openat(fd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return true, fmt.Errorf("status setup publication could not be established")
	}
	file := os.NewFile(uintptr(output), "status setup publication")
	e = file.Chmod(0600)
	if e == nil {
		_, e = file.Write(raw)
	}
	if e == nil {
		e = file.Sync()
	}
	closed := file.Close()
	if e == nil {
		e = closed
	}
	if e != nil {
		return true, fmt.Errorf("status setup publication failed; original evidence retained")
	}
	current, e := os.Lstat(filepath.Join(s.Home, "status-onboarding-v1"))
	if e != nil || !os.SameFile(identity, current) || current.Mode().Perm() != 0700 || current.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return true, fmt.Errorf("status setup directory changed before publication")
	}
	var held, named unix.Stat_t
	if unix.Fstat(lockFD, &held) != nil || unix.Fstatat(fd, "lock", &named, unix.AT_SYMLINK_NOFOLLOW) != nil || held.Ino != named.Ino || held.Dev != named.Dev || named.Mode&unix.S_IFMT != unix.S_IFREG || named.Mode&0777 != 0600 || named.Uid != uint32(os.Getuid()) || named.Nlink != 1 {
		return true, fmt.Errorf("status setup lock identity changed")
	}
	if unix.Renameat(fd, name, fd, "state.json") != nil {
		return true, fmt.Errorf("status setup publication outcome uncertain; evidence retained")
	}
	if unix.Fsync(fd) != nil {
		return true, fmt.Errorf("status setup publication durability uncertain")
	}
	return true, nil
}
