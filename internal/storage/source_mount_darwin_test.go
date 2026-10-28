//go:build darwin

package storage

import (
	"golang.org/x/sys/unix"
	"testing"
)

func TestSourceMountUsesReportedMountpoint(t *testing.T) {
	var a, b unix.Statfs_t
	copy(a.Mntonname[:], "/one")
	copy(b.Mntonname[:], "/two")
	one, e := sourceDarwinMountIdentity(a)
	if e != nil {
		t.Fatal(e)
	}
	two, e := sourceDarwinMountIdentity(b)
	if e != nil || one == two {
		t.Fatal("same Fsid collapsed distinct mounts")
	}
	var empty unix.Statfs_t
	if _, e = sourceDarwinMountIdentity(empty); e == nil {
		t.Fatal("unknown mountpoint accepted")
	}
}
