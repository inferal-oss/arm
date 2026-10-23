//go:build linux

package providerexec

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

// The initial Linux selection scope is known POSIX-mode filesystems with absent
// extended access/default ACLs. NFSv4/rich ACLs and unknown filesystem authority
// models are not inferred safe from POSIX mode. Breadth remains explicit work.
func imageACL(f *os.File) (string, error) {
	var fs unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &fs); err != nil {
		return "", err
	}
	switch uint64(fs.Type) {
	case 0xef53, 0x58465342, 0x9123683e, 0x01021994, 0x794c7630: // ext, XFS, Btrfs, tmpfs, overlay
	default:
		return "", fmt.Errorf("image filesystem ACL authorization model unsupported")
	}
	var raw [4096]byte
	for _, name := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
		n, err := unix.Fgetxattr(int(f.Fd()), name, raw[:])
		if err == unix.ENODATA {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("native image ACL inspection unknown: %w", err)
		}
		return "", fmt.Errorf("nonempty or unrecognized extended image ACL is unsupported (bytes=%d)", n)
	}
	return hash([]byte("linux:known-posix-filesystem:access-and-default-acl-absent")), nil
}
