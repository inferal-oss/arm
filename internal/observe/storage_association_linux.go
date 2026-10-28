package observe

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// This is a finite observation, not an atomic kernel transaction. Comparing two
// complete snapshots refuses changes seen during the probe; it cannot promise
// a mount/device will remain unchanged after the probe returns.
type storageSysfsBudget struct{ entries, bytes int }

func (b *storageSysfsBudget) charge(path string, n int) error {
	b.bytes += len(path) + n
	if b.bytes > storageSysfsMaxBytes {
		return fmt.Errorf("sysfs snapshot exceeds total byte bound")
	}
	return nil
}
func (b *storageSysfsBudget) read(ctx context.Context, path string, max int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.charge(path, 0); err != nil {
		return nil, err
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if e != nil {
		return nil, e
	}
	if len(data) > max {
		return nil, fmt.Errorf("sysfs attribute exceeds byte bound")
	}
	if e = b.charge("", len(data)); e != nil {
		return nil, e
	}
	return data, ctx.Err()
}
func (b *storageSysfsBudget) directory(ctx context.Context, path string, max int) ([]os.DirEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.charge(path, 0); err != nil {
		return nil, err
	}
	remaining := storageSysfsMaxEntries - b.entries
	if remaining < max {
		max = remaining
	}
	if max < 0 {
		return nil, fmt.Errorf("sysfs snapshot exceeds entry bound")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	entries, e := f.ReadDir(max + 1)
	if e != nil && e != io.EOF {
		return nil, e
	}
	if len(entries) > max {
		return nil, fmt.Errorf("sysfs snapshot exceeds entry bound")
	}
	b.entries += len(entries)
	for _, entry := range entries {
		if e = b.charge("", len(entry.Name())); e != nil {
			return nil, e
		}
	}
	return entries, ctx.Err()
}
func linuxStorageObject(st unix.Stat_t) storageObject {
	return storageObject{Device: uint64(st.Dev), Inode: st.Ino, ChangeSec: int64(st.Ctim.Sec), ChangeNS: int64(st.Ctim.Nsec)}
}
func linuxStoragePath(path string) (storagePathIdentity, error) {
	var result storagePathIdentity
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if e != nil {
		return result, e
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	var fs unix.Statfs_t
	if e = unix.Fstat(fd, &st); e != nil {
		return result, e
	}
	if e = unix.Fstatfs(fd, &fs); e != nil {
		return result, e
	}
	filesystem := fmt.Sprintf("linux-magic:0x%x", uint64(fs.Type))
	if fs.Type == unix.BTRFS_SUPER_MAGIC {
		filesystem = "btrfs"
	}
	return storagePathIdentity{Path: path, Device: fmt.Sprintf("%d:%d", unix.Major(uint64(st.Dev)), unix.Minor(uint64(st.Dev))), Filesystem: filesystem, FilesystemID: fmt.Sprintf("fsid:%x", fs.Fsid.Val), Object: linuxStorageObject(st), Mode: st.Mode, Owner: st.Uid, Directory: st.Mode&unix.S_IFMT == unix.S_IFDIR}, nil
}
func linuxStorageMountSnapshot(ctx context.Context) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	f, e := os.Open("/proc/self/mountinfo")
	if e != nil {
		return nil, e
	}
	defer f.Close()
	var fs unix.Statfs_t
	if e = unix.Fstatfs(int(f.Fd()), &fs); e != nil {
		return nil, e
	}
	if fs.Type != unix.PROC_SUPER_MAGIC {
		return nil, fmt.Errorf("mount snapshot is not procfs")
	}
	data, e := readStorageMountSnapshot(f)
	if e != nil {
		return nil, e
	}
	return data, ctx.Err()
}
func linuxStorageSysfsObject(path string) (storageObject, error) {
	var st unix.Stat_t
	var fs unix.Statfs_t
	if !canonicalStoragePath(path) || !strings.HasPrefix(path, "/sys/devices/") {
		return storageObject{}, fmt.Errorf("resolved block object is outside sysfs devices")
	}
	if e := unix.Stat(path, &st); e != nil {
		return storageObject{}, e
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return storageObject{}, fmt.Errorf("sysfs block object is not a directory")
	}
	if e := unix.Statfs(path, &fs); e != nil {
		return storageObject{}, e
	}
	if fs.Type != unix.SYSFS_MAGIC {
		return storageObject{}, fmt.Errorf("block object is not sysfs")
	}
	return linuxStorageObject(st), nil
}
func linuxStorageBlock(ctx context.Context, source string, budget *storageSysfsBudget) (storageBlockIdentity, error) {
	var result storageBlockIdentity
	if !canonicalStoragePath(source) || !strings.HasPrefix(source, "/dev/") {
		return result, fmt.Errorf("accessible block mount source unavailable")
	}
	if e := ctx.Err(); e != nil {
		return result, e
	}
	resolved, e := filepath.EvalSymlinks(source)
	if e != nil {
		return result, e
	}
	var node, other unix.Stat_t
	if e = unix.Stat(source, &node); e != nil {
		return result, e
	}
	if e = unix.Stat(resolved, &other); e != nil {
		return result, e
	}
	if node.Mode&unix.S_IFMT != unix.S_IFBLK || other.Mode&unix.S_IFMT != unix.S_IFBLK || linuxStorageObject(node) != linuxStorageObject(other) || node.Rdev != other.Rdev {
		return result, fmt.Errorf("mount source is nonblock or changed")
	}
	device := fmt.Sprintf("%d:%d", unix.Major(uint64(node.Rdev)), unix.Minor(uint64(node.Rdev)))
	sysfs, e := filepath.EvalSymlinks("/sys/dev/block/" + device)
	if e != nil {
		return result, e
	}
	object, e := linuxStorageSysfsObject(sysfs)
	if e != nil {
		return result, e
	}
	data, e := budget.read(ctx, filepath.Join(sysfs, "dev"), 64)
	if e != nil {
		return result, e
	}
	return storageBlockIdentity{Source: source, ResolvedSource: resolved, Device: device, SysfsDevice: strings.TrimSpace(string(data)), SysfsPath: sysfs, Node: linuxStorageObject(node), SysfsObject: object, Block: true, SysfsDirectory: true}, nil
}
func linuxStorageBtrfsMembers(ctx context.Context, budget *storageSysfsBudget) ([]storageBtrfsMember, error) {
	entries, e := budget.directory(ctx, "/sys/fs/btrfs", storageBtrfsMaxRoots)
	if e != nil {
		return nil, e
	}
	var members []storageBtrfsMember
	for _, entry := range entries {
		name := entry.Name()
		if name == "features" {
			continue
		}
		if !storageUUID(name) {
			return nil, fmt.Errorf("unexpected Btrfs UUID entry")
		}
		devices, e := budget.directory(ctx, filepath.Join("/sys/fs/btrfs", name, "devices"), storageBtrfsMaxMembers-len(members))
		if e != nil {
			return nil, e
		}
		for _, device := range devices {
			link := filepath.Join("/sys/fs/btrfs", name, "devices", device.Name())
			if e = budget.charge(link, 0); e != nil {
				return nil, e
			}
			resolved, e := filepath.EvalSymlinks(link)
			if e != nil {
				return nil, e
			}
			object, e := linuxStorageSysfsObject(resolved)
			if e != nil {
				return nil, e
			}
			data, e := budget.read(ctx, filepath.Join(resolved, "dev"), 64)
			if e != nil {
				return nil, e
			}
			members = append(members, storageBtrfsMember{UUID: name, Name: device.Name(), Device: strings.TrimSpace(string(data)), SysfsPath: resolved, Object: object})
		}
	}
	return canonicalStorageMembers(members)
}
func captureLinuxStorageAssociation(ctx context.Context, path string) (storageAssociationSnapshot, error) {
	var s storageAssociationSnapshot
	var e error
	if e = ctx.Err(); e != nil {
		return s, e
	}
	if s.Native, e = linuxStoragePath(path); e != nil {
		return s, e
	}
	if s.MountInfo, e = linuxStorageMountSnapshot(ctx); e != nil {
		return s, e
	}
	mount, e := selectStorageMount(s.MountInfo, s.Native)
	if e != nil {
		return s, e
	}
	budget := &storageSysfsBudget{}
	if s.Source, e = linuxStorageBlock(ctx, mount.Source, budget); e != nil {
		return s, e
	}
	if s.Members, e = linuxStorageBtrfsMembers(ctx, budget); e != nil {
		return s, e
	}
	return s, ctx.Err()
}

// Reverse-order recapture brackets the entire association with native path and
// mount/source facts. Membership is collected nearest the middle of the probe.
func recaptureLinuxStorageAssociation(ctx context.Context, path string, before storageAssociationSnapshot) (storageAssociationSnapshot, error) {
	var s storageAssociationSnapshot
	var err error
	budget := &storageSysfsBudget{}
	if s.Members, err = linuxStorageBtrfsMembers(ctx, budget); err != nil {
		return s, err
	}
	if s.Source, err = linuxStorageBlock(ctx, before.Source.Source, budget); err != nil {
		return s, err
	}
	if s.MountInfo, err = linuxStorageMountSnapshot(ctx); err != nil {
		return s, err
	}
	if s.Native, err = linuxStoragePath(path); err != nil {
		return s, err
	}
	return s, ctx.Err()
}
