package syncfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWorkspaceAllocationDarwinMountIdentitySeparatesReportedMountpoints(t *testing.T) {
	first := unix.Statfs_t{}
	first.Fsid.Val = [2]int32{11, 22}
	copy(first.Mntonname[:], "/fixture-one")
	second := first
	clear(second.Mntonname[:])
	copy(second.Mntonname[:], "/fixture-two")
	if fmt.Sprintf("%x", first.Fsid.Val) != fmt.Sprintf("%x", second.Fsid.Val) {
		t.Fatal("counterexample did not hold filesystem ID equal")
	}
	a, err := surveyDarwinMountIdentity(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := surveyDarwinMountIdentity(second)
	if err != nil || a == b {
		t.Fatal("same-filesystem reported mounts were conflated", err)
	}
	missing := first
	clear(missing.Mntonname[:])
	if id, err := surveyDarwinMountIdentity(missing); id != "" || !errors.Is(err, ErrUnknown) {
		t.Fatal("missing mountpoint became mount evidence", err)
	}
}

func TestWorkspaceAllocationDarwinSymlinkDescriptorNamesOriginalInode(t *testing.T) {
	_, _, _, w, _ := testWorkspaceEvidence(t)
	name := "mount-identity-link"
	path := filepath.Join(w.StagePath(), name)
	if err := os.Symlink("/", path); err != nil {
		t.Fatal(err)
	}
	var named unix.Stat_t
	if err := unix.Fstatat(w.Stage.FD(), name, &named, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		t.Fatal(err)
	}
	fd, err := surveyDarwinOpenMountEntry(w.Stage.FD(), name)
	if err != nil {
		t.Fatal("symlink metadata descriptor refused", err)
	}
	defer unix.Close(fd)
	var held unix.Stat_t
	if err := unix.Fstat(fd, &held); err != nil || held.Mode&unix.S_IFMT != unix.S_IFLNK || !sameObject(named, held) {
		t.Fatal("metadata descriptor followed or replaced original symlink", err)
	}
	mount, err := surveyMountFD(fd)
	if err != nil {
		t.Fatal("symlink descriptor mount identity unavailable", err)
	}
	parent, err := surveyMountFD(w.Stage.FD())
	if err != nil || mount != parent {
		t.Fatal("symlink descriptor does not report containing mount", err)
	}
	viaName, err := surveyMountAt(w.Stage.FD(), name)
	if err != nil || viaName != mount {
		t.Fatal("child mount lookup changed symlink identity", err)
	}
}
