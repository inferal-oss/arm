package observe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

func storageLocation(ctx context.Context, r *model.StorageLocation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reading := DiskAvailability(ctx, r.Path)
	if reading.Volume == "" {
		return fmt.Errorf("filesystem identity unavailable: %s", reading.Detail)
	}
	r.Volume, r.FilesystemID, r.Filesystem = reading.Volume, reading.Volume, reading.Filesystem
	r.Backing = r.Volume
	r.Method = "statfs; bounded /proc/self/mountinfo; bounded /sys/dev/block"
	r.Detail = "Reported guest filesystem/source topology; device properties do not establish physical host capacity or enforcement."
	if reading.Shared {
		r.Backing = ""
	}
	if r.Filesystem == "btrfs" {
		before, e := captureLinuxStorageAssociation(ctx, r.Path)
		if e != nil {
			return unresolvedStorageLocation(r, e)
		}
		// Bind the initial availability identity to the opened native path snapshot.
		if before.Native.FilesystemID != r.FilesystemID || before.Native.Filesystem != r.Filesystem {
			return unresolvedStorageLocation(r, fmt.Errorf("native filesystem identity changed"))
		}
		devices := linuxStorageHardware(ctx, before.Source.SysfsPath)
		after, e := recaptureLinuxStorageAssociation(ctx, r.Path, before)
		if e != nil {
			return unresolvedStorageLocation(r, e)
		}
		association, e := confirmStorageBtrfs(before, after)
		if e != nil {
			return unresolvedStorageLocation(r, e)
		}
		r.Mount, r.Source = association.Mount.Mount, association.Mount.Source
		r.Backing, r.Volume = association.Backing, association.Backing
		r.Devices = devices
		r.Method += "; stable block rdev/sysfs object/Btrfs UUID membership"
		r.Detail = "Observed guest Btrfs filesystem association; aliases share full outstanding promises. Quotas and current contention may reduce usable capacity/performance. Guest device properties are not physical host-drive ratings or enforcement authority."
		return nil
	}
	// Other filesystems retain strict native-device matching; the synthetic
	// device exception is never a generic path-only source association.
	var st unix.Stat_t
	if e := unix.Stat(r.Path, &st); e != nil {
		return e
	}
	snapshot, e := linuxStorageMountSnapshot(ctx)
	if e != nil {
		return unresolvedStorageLocation(r, e)
	}
	mount, e := selectStorageMount(snapshot, storagePathIdentity{Path: r.Path, Device: fmt.Sprintf("%d:%d", unix.Major(uint64(st.Dev)), unix.Minor(uint64(st.Dev))), Filesystem: r.Filesystem, FilesystemID: r.FilesystemID, Object: linuxStorageObject(st), Directory: st.Mode&unix.S_IFMT == unix.S_IFDIR})
	if e != nil {
		return unresolvedStorageLocation(r, e)
	}
	r.Mount, r.Source = mount.Mount, mount.Source
	if r.Filesystem == "xfs" {
		r.Backing = r.Volume
	}
	block, e := linuxStorageBlock(ctx, r.Source, &storageSysfsBudget{})
	if e != nil {
		r.Detail += " Physical source metadata unavailable."
		return nil
	}
	if storageMapperPath(block.SysfsPath) || storageMapperPath(block.ResolvedSource) {
		return unresolvedStorageLocation(r, fmt.Errorf("device-mapper capacity sharing remains unresolved"))
	}
	r.Devices = linuxStorageHardware(ctx, block.SysfsPath)
	return nil
}
func unresolvedStorageLocation(r *model.StorageLocation, err error) error {
	r.Backing = ""
	r.Detail = "Storage backing association unavailable; named admission remains refused: " + err.Error()
	return nil
}
func linuxStorageHardware(ctx context.Context, base string) []model.StorageDevice {
	var devices []model.StorageDevice
	budget := &storageSysfsBudget{}
	if _, e := os.Stat(filepath.Join(base, "partition")); e == nil {
		base = filepath.Dir(base)
	}
	seen := map[string]bool{}
	var visit func(string, int)
	visit = func(base string, depth int) {
		if depth > 8 || len(seen) >= 16 || seen[base] || ctx.Err() != nil {
			return
		}
		seen[base] = true
		if storageMapperPath(base) {
			return
		}
		slaves, e := budget.directory(ctx, filepath.Join(base, "slaves"), 16)
		if e != nil && !os.IsNotExist(e) {
			return
		}
		if len(slaves) > 0 {
			for _, s := range slaves {
				if target, e := filepath.EvalSymlinks(filepath.Join(base, "slaves", s.Name())); e == nil {
					visit(target, depth+1)
				}
			}
			return
		}
		read := func(name string) string {
			data, e := budget.read(ctx, filepath.Join(base, name), 256)
			if e != nil {
				return ""
			}
			return strings.TrimSpace(string(data))
		}
		d := model.StorageDevice{Device: "/dev/" + filepath.Base(base), Model: read("device/model")}
		if value := read("queue/rotational"); value == "0" || value == "1" {
			solid := value == "0"
			d.SolidState = &solid
		}
		switch {
		case strings.Contains(base, "/usb"):
			d.Transport = "USB"
		case strings.Contains(base, "/nvme"):
			d.Transport = "NVMe"
		case strings.Contains(base, "/ata"):
			d.Transport = "ATA"
		case strings.Contains(base, "/virtio"):
			d.Transport = "virtio"
		}
		devices = append(devices, d)
	}
	visit(base, 0)
	return devices
}
