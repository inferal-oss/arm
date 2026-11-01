package agentstatus

import (
	"golang.org/x/sys/unix"
	"testing"
)

func TestPrivateDirectoryDuplicateIsCloseOnExec(t *testing.T) {
	fd, err := unix.Open(t.TempDir(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	copy, err := duplicatePrivateFD(fd)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(copy)
	flags, err := unix.FcntlInt(uintptr(copy), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("private directory duplicate lacks close-on-exec", flags, err)
	}
	var original, duplicate unix.Stat_t
	if unix.Fstat(fd, &original) != nil || unix.Fstat(copy, &duplicate) != nil || original.Dev != duplicate.Dev || original.Ino != duplicate.Ino {
		t.Fatal("duplicate changed directory identity")
	}
}
