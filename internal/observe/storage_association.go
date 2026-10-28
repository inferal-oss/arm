package observe

// Pure, bounded kernel-topology parsing and association. These helpers run on
// every development platform; only collecting native facts is Linux-specific.
import (
	"bytes"
	"fmt"
	"io"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const (
	storageMountMaxBytes   = 1 << 20
	storageMountMaxEntries = 4096
	storageMountMaxLine    = 16 << 10
	storageBtrfsMaxRoots   = 128
	storageBtrfsMaxMembers = 512
	storageSysfsMaxEntries = 1024
	storageSysfsMaxBytes   = 256 << 10
)

type storageMount struct {
	ID                                      uint64
	Parent                                  uint64
	Device, Root, Mount, Filesystem, Source string
}
type storageObject struct {
	Device, Inode       uint64
	ChangeSec, ChangeNS int64
}
type storagePathIdentity struct {
	Path, Device, Filesystem, FilesystemID string
	Object                                 storageObject
	Mode, Owner                            uint32
	Directory                              bool
}
type storageBlockIdentity struct {
	Source, ResolvedSource, Device, SysfsDevice, SysfsPath string
	Node, SysfsObject                                      storageObject
	Block, SysfsDirectory                                  bool
}
type storageBtrfsMember struct {
	UUID, Name, Device, SysfsPath string
	Object                        storageObject
}
type storageAssociationSnapshot struct {
	Native    storagePathIdentity
	MountInfo []byte
	Source    storageBlockIdentity
	Members   []storageBtrfsMember
}
type storageAssociation struct {
	Mount              storageMount
	Backing, SysfsPath string
}

func readStorageMountSnapshot(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, storageMountMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > storageMountMaxBytes {
		return nil, fmt.Errorf("mount snapshot exceeds byte bound")
	}
	if _, err := parseStorageMountSnapshot(data); err != nil {
		return nil, err
	}
	return data, nil
}
func storageDevice(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil || strconv.FormatUint(n, 10) != part {
			return false
		}
	}
	return true
}
func storageMountPath(value string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			if value[i] < 32 || value[i] == 127 {
				return "", fmt.Errorf("unescaped mount control")
			}
			out.WriteByte(value[i])
			continue
		}
		if i+3 >= len(value) {
			return "", fmt.Errorf("incomplete mount escape")
		}
		decoded, ok := map[string]byte{"040": ' ', "011": '\t', "012": '\n', "134": '\\'}[value[i+1:i+4]]
		if !ok {
			return "", fmt.Errorf("unsupported mount escape")
		}
		out.WriteByte(decoded)
		i += 3
	}
	return out.String(), nil
}
func canonicalStoragePath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.ContainsRune(value, 0) && path.Clean(value) == value
}
func storagePathCovers(mount, target string) bool {
	return target == mount || strings.HasPrefix(target, strings.TrimSuffix(mount, "/")+"/")
}
func parseStorageMountSnapshot(data []byte) ([]storageMount, error) {
	if len(data) == 0 || len(data) > storageMountMaxBytes || data[len(data)-1] != '\n' {
		return nil, fmt.Errorf("missing or oversized mount snapshot")
	}
	if bytes.Count(data, []byte{'\n'}) > storageMountMaxEntries {
		return nil, fmt.Errorf("mount snapshot exceeds entry bound")
	}
	lines := bytes.Split(data[:len(data)-1], []byte{'\n'})
	mounts := make([]storageMount, 0, len(lines))
	ids := map[uint64]bool{}
	for _, line := range lines {
		if len(line) == 0 || len(line) > storageMountMaxLine {
			return nil, fmt.Errorf("invalid mount line bound")
		}
		fields := strings.Split(string(line), " ")
		for _, f := range fields {
			if f == "" || strings.IndexFunc(f, func(c rune) bool { return c < 32 || c == 127 }) >= 0 {
				return nil, fmt.Errorf("invalid mount field")
			}
		}
		sep := -1
		for i, f := range fields {
			if f == "-" {
				if sep != -1 {
					return nil, fmt.Errorf("ambiguous mount separator")
				}
				sep = i
			}
		}
		if sep < 6 || len(fields) != sep+4 {
			return nil, fmt.Errorf("invalid mount shape")
		}
		id, e := strconv.ParseUint(fields[0], 10, 64)
		if e != nil || id == 0 || strconv.FormatUint(id, 10) != fields[0] || ids[id] {
			return nil, fmt.Errorf("invalid mount identity")
		}
		parent, e := strconv.ParseUint(fields[1], 10, 64)
		if e != nil || parent == 0 || strconv.FormatUint(parent, 10) != fields[1] || !storageDevice(fields[2]) {
			return nil, fmt.Errorf("invalid mount parent/device")
		}
		root, e := storageMountPath(fields[3])
		if e != nil || !canonicalStoragePath(root) {
			return nil, fmt.Errorf("invalid mount root")
		}
		mount, e := storageMountPath(fields[4])
		if e != nil || !canonicalStoragePath(mount) {
			return nil, fmt.Errorf("invalid mount path")
		}
		source, e := storageMountPath(fields[sep+2])
		if e != nil || source == "" {
			return nil, fmt.Errorf("invalid mount source")
		}
		ids[id] = true
		mounts = append(mounts, storageMount{ID: id, Parent: parent, Device: fields[2], Root: root, Mount: mount, Filesystem: fields[sep+1], Source: source})
	}
	return mounts, nil
}
func selectStorageMount(data []byte, native storagePathIdentity) (storageMount, error) {
	var selected storageMount
	if !canonicalStoragePath(native.Path) || !storageDevice(native.Device) || native.FilesystemID == "" || !native.Directory || native.Object.Inode == 0 {
		return selected, fmt.Errorf("native path identity unavailable")
	}
	mounts, err := parseStorageMountSnapshot(data)
	if err != nil {
		return selected, err
	}
	ambiguous := false
	for _, m := range mounts {
		if !storagePathCovers(m.Mount, native.Path) {
			continue
		}
		if selected.Mount == "" || len(m.Mount) > len(selected.Mount) {
			selected = m
			ambiguous = false
		} else if m.Mount == selected.Mount {
			ambiguous = true
		}
	}
	if selected.Mount == "" || ambiguous {
		return storageMount{}, fmt.Errorf("missing or ambiguous covering mount")
	}
	// Btrfs subvolumes may have a synthetic st_dev distinct from their
	// covering mount. Only native/mount Btrfs agreement permits that exception;
	// shared backing still requires the separate exact block/membership proof.
	if selected.Device != native.Device && !(native.Filesystem == "btrfs" && selected.Filesystem == "btrfs") {
		return storageMount{}, fmt.Errorf("covering mount device differs from native path")
	}
	if (native.Filesystem == "btrfs" || selected.Filesystem == "btrfs") && (native.Filesystem != "btrfs" || selected.Filesystem != "btrfs") {
		return storageMount{}, fmt.Errorf("native/mount filesystem type differs")
	}
	return selected, nil
}
func storageUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
func storageMapperPath(value string) bool {
	for _, part := range strings.Split(value, "/") {
		if strings.HasPrefix(part, "dm-") {
			return true
		}
	}
	return false
}
func canonicalStorageMembers(members []storageBtrfsMember) ([]storageBtrfsMember, error) {
	if len(members) == 0 || len(members) > storageBtrfsMaxMembers {
		return nil, fmt.Errorf("Btrfs membership unavailable or oversized")
	}
	result := append([]storageBtrfsMember(nil), members...)
	roots := map[string]bool{}
	keys := map[string]bool{}
	devices := map[string]bool{}
	objects := map[string]bool{}
	bytes := 0
	for i := range result {
		m := &result[i]
		m.UUID = strings.ToLower(m.UUID)
		bytes += len(m.UUID) + len(m.Name) + len(m.Device) + len(m.SysfsPath)
		if bytes > storageSysfsMaxBytes || !storageUUID(m.UUID) || m.Name == "" || len(m.Name) > 128 || path.Base(m.Name) != m.Name || m.Name == "." || m.Name == ".." || strings.ContainsAny(m.Name, "\x00\r\n\t") || !storageDevice(m.Device) || !canonicalStoragePath(m.SysfsPath) || !strings.HasPrefix(m.SysfsPath, "/sys/devices/") || m.Object.Inode == 0 {
			return nil, fmt.Errorf("invalid Btrfs membership identity")
		}
		key := m.UUID + "/" + m.Name
		if keys[key] || devices[m.Device] || objects[m.SysfsPath] {
			return nil, fmt.Errorf("duplicate or ambiguous Btrfs block membership")
		}
		keys[key] = true
		devices[m.Device] = true
		objects[m.SysfsPath] = true
		roots[m.UUID] = true
	}
	if len(roots) > storageBtrfsMaxRoots {
		return nil, fmt.Errorf("Btrfs UUID count exceeds bound")
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.UUID != b.UUID {
			return a.UUID < b.UUID
		}
		return a.Name < b.Name
	})
	return result, nil
}
func associateStorageBtrfs(snapshot storageAssociationSnapshot) (storageAssociation, error) {
	var result storageAssociation
	mount, err := selectStorageMount(snapshot.MountInfo, snapshot.Native)
	if err != nil {
		return result, err
	}
	if snapshot.Native.Filesystem != "btrfs" || mount.Filesystem != "btrfs" {
		return result, fmt.Errorf("not a Btrfs association")
	}
	s := snapshot.Source
	if s.Source != mount.Source || !canonicalStoragePath(s.Source) || !strings.HasPrefix(s.Source, "/dev/") || !canonicalStoragePath(s.ResolvedSource) || !s.Block || s.Node.Inode == 0 || !storageDevice(s.Device) || s.Device != s.SysfsDevice || !s.SysfsDirectory || s.SysfsObject.Inode == 0 || !canonicalStoragePath(s.SysfsPath) || !strings.HasPrefix(s.SysfsPath, "/sys/devices/") || storageMapperPath(s.SysfsPath) || storageMapperPath(s.ResolvedSource) {
		return result, fmt.Errorf("block source/sysfs identity unavailable or unsupported")
	}
	members, err := canonicalStorageMembers(snapshot.Members)
	if err != nil {
		return result, err
	}
	matches := 0
	uuid := ""
	for _, m := range members {
		if m.Device != s.Device {
			continue
		}
		if m.SysfsPath != s.SysfsPath || m.Object != s.SysfsObject {
			return result, fmt.Errorf("block source/membership object mismatch")
		}
		matches++
		uuid = m.UUID
	}
	if matches != 1 {
		return result, fmt.Errorf("Btrfs source must match exactly one UUID membership")
	}
	for _, m := range members {
		if m.UUID == uuid && storageMapperPath(m.SysfsPath) {
			return result, fmt.Errorf("device-mapper Btrfs membership remains unresolved")
		}
	}
	return storageAssociation{Mount: mount, Backing: "btrfs:" + uuid, SysfsPath: s.SysfsPath}, nil
}
func confirmStorageBtrfs(before, after storageAssociationSnapshot) (storageAssociation, error) {
	first, err := associateStorageBtrfs(before)
	if err != nil {
		return storageAssociation{}, err
	}
	second, err := associateStorageBtrfs(after)
	if err != nil {
		return storageAssociation{}, err
	}
	a, err := canonicalStorageMembers(before.Members)
	if err != nil {
		return storageAssociation{}, err
	}
	b, err := canonicalStorageMembers(after.Members)
	if err != nil {
		return storageAssociation{}, err
	}
	if before.Native != after.Native || !bytes.Equal(before.MountInfo, after.MountInfo) || before.Source != after.Source || !reflect.DeepEqual(a, b) || first != second {
		return storageAssociation{}, fmt.Errorf("storage association changed during observation")
	}
	return first, nil
}
