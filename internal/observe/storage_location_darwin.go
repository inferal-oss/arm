package observe

import (
	"context"
	"fmt"
	"strings"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

func storageLocation(ctx context.Context, r *model.StorageLocation) error {
	var st unix.Statfs_t
	if err := unix.Statfs(r.Path, &st); err != nil {
		return err
	}
	r.Volume = fmt.Sprintf("fsid:%x", st.Fsid.Val)
	r.FilesystemID = r.Volume
	r.Mount = unix.ByteSliceToString(st.Mntonname[:])
	r.Source = unix.ByteSliceToString(st.Mntfromname[:])
	r.Filesystem = unix.ByteSliceToString(st.Fstypename[:])
	r.Backing = r.Volume
	r.Method = "statfs; diskutil info -plist; diskutil apfs list -plist"
	r.Detail = "Reported topology; shared backing capacity must not be summed. Hardware properties are optional observations."
	data, err := command(ctx, "/usr/sbin/diskutil", "info", "-plist", r.Source)
	if err != nil {
		r.Detail += " Device metadata unavailable."
		if r.Filesystem == "apfs" {
			r.Backing = ""
		}
		return nil
	}
	info, err := plist(data)
	if err != nil {
		return err
	}
	if uuid, _ := info["VolumeUUID"].(string); uuid != "" {
		r.Volume = "volume-uuid:" + strings.ToLower(uuid)
	}
	if r.Filesystem == "apfs" {
		r.Backing = ""
		if ref, _ := info["APFSContainerReference"].(string); ref != "" {
			if data, e := command(ctx, "/usr/sbin/diskutil", "apfs", "list", "-plist", ref); e == nil {
				if pool, e := apfsPool(data, ref); e == nil {
					r.Backing = "apfs-container:" + strings.ToLower(pool["APFSContainerUUID"].(string))
				}
			}
		}
	}
	stores, _ := info["APFSPhysicalStores"].([]any)
	if len(stores) > 16 {
		return fmt.Errorf("too many physical stores")
	}
	if len(stores) == 0 {
		r.Devices = append(r.Devices, darwinStorageDevice(info))
		return nil
	}
	for _, entry := range stores {
		store, _ := entry.(map[string]any)
		name, _ := store["APFSPhysicalStore"].(string)
		if name == "" || len(name) > 64 || strings.ContainsAny(name, "/\x00") {
			continue
		}
		d := model.StorageDevice{Device: "/dev/" + name}
		if data, e := command(ctx, "/usr/sbin/diskutil", "info", "-plist", name); e == nil {
			if properties, e := plist(data); e == nil {
				if whole, _ := properties["ParentWholeDisk"].(string); whole != "" && len(whole) < 64 && !strings.ContainsAny(whole, "/\x00") {
					if data, e := command(ctx, "/usr/sbin/diskutil", "info", "-plist", whole); e == nil {
						if w, e := plist(data); e == nil {
							properties = w
						}
					}
				}
				d = darwinStorageDevice(properties)
			}
		}
		r.Devices = append(r.Devices, d)
	}
	return nil
}
func darwinStorageDevice(info map[string]any) model.StorageDevice {
	text := func(key string) string {
		s, _ := info[key].(string)
		if len(s) > 256 {
			s = s[:256]
		}
		return s
	}
	d := model.StorageDevice{Device: text("DeviceNode"), Model: text("MediaName"), Transport: text("BusProtocol")}
	if d.Model == "" {
		d.Model = text("IORegistryEntryName")
	}
	if v, ok := info["SolidState"].(bool); ok {
		d.SolidState = &v
	}
	if v, ok := info["Internal"].(bool); ok {
		d.Internal = &v
	}
	return d
}
