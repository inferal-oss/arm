package syncfs

import (
	"errors"
	"fmt"
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
