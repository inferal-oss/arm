package observe

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

const storageFixtureUUID = "12345678-1234-4abc-8def-1234567890ab"

func storageAssociationFixture() storageAssociationSnapshot {
	return storageAssociationSnapshot{
		Native:    storagePathIdentity{Path: "/home/agent/scratch", Device: "0:53", Filesystem: "btrfs", FilesystemID: "fsid:fixture", Object: storageObject{Device: 53, Inode: 400}, Directory: true},
		MountInfo: []byte("125 66 0:38 /fixture-root / rw,noatime shared:24 - btrfs /dev/fixture0p1 rw,subvolid=258\n"),
		Source:    storageBlockIdentity{Source: "/dev/fixture0p1", ResolvedSource: "/dev/fixture0p1", Device: "254:17", SysfsDevice: "254:17", SysfsPath: "/sys/devices/fixture/block/fixture0/fixture0p1", Node: storageObject{Device: 6, Inode: 50}, SysfsObject: storageObject{Device: 7, Inode: 80}, Block: true, SysfsDirectory: true},
		Members:   []storageBtrfsMember{{UUID: storageFixtureUUID, Name: "fixture0p1", Device: "254:17", SysfsPath: "/sys/devices/fixture/block/fixture0/fixture0p1", Object: storageObject{Device: 7, Inode: 80}}},
	}
}
func cloneStorageAssociation(s storageAssociationSnapshot) storageAssociationSnapshot {
	s.MountInfo = append([]byte(nil), s.MountInfo...)
	s.Members = append([]storageBtrfsMember(nil), s.Members...)
	return s
}
func TestStorageAssociationObservedSyntheticDevice(t *testing.T) {
	s := storageAssociationFixture()
	got, err := confirmStorageBtrfs(s, cloneStorageAssociation(s))
	if err != nil || got.Backing != "btrfs:"+storageFixtureUUID || got.Mount.Source != "/dev/fixture0p1" || got.Mount.Mount != "/" {
		t.Fatalf("observed accessible guest association: %+v %v", got, err)
	}
	if s.Native.FilesystemID != "fsid:fixture" {
		t.Fatal("native FilesystemID changed")
	}
}
func TestStorageAssociationLongestEscapedBoundaries(t *testing.T) {
	for _, tc := range []struct{ path, mount string }{
		{"/home/agent/scratch", "/home/agent"}, {"/home/agent-other/scratch", "/"}, {"/home/a b/x", "/home/a b"}, {"/home/a\tb/x", "/home/a\tb"}, {"/home/a\nb/x", "/home/a\nb"}, {"/home/a\\b/x", "/home/a\\b"},
	} {
		t.Run(fmt.Sprintf("%q", tc.path), func(t *testing.T) {
			s := storageAssociationFixture()
			s.Native.Path = tc.path
			s.MountInfo = append(s.MountInfo, []byte("126 125 0:54 /subvol /home/agent rw - btrfs /dev/fixture0p1 rw\n127 125 0:55 /subvol /home/a\\040b rw - btrfs /dev/fixture0p1 rw\n128 125 0:56 /subvol /home/a\\011b rw - btrfs /dev/fixture0p1 rw\n129 125 0:57 /subvol /home/a\\012b rw - btrfs /dev/fixture0p1 rw\n130 125 0:58 /subvol /home/a\\134b rw - btrfs /dev/fixture0p1 rw\n")...)
			got, e := confirmStorageBtrfs(s, cloneStorageAssociation(s))
			if e != nil || got.Mount.Mount != tc.mount {
				t.Fatal(got, e)
			}
		})
	}
}
func TestStorageAssociationRefusals(t *testing.T) {
	cases := map[string]func(*storageAssociationSnapshot){
		"native-type": func(s *storageAssociationSnapshot) { s.Native.Filesystem = "ext" },
		"mount-type": func(s *storageAssociationSnapshot) {
			s.MountInfo = bytes.ReplaceAll(s.MountInfo, []byte("btrfs"), []byte("ext4"))
		},
		"not-directory":      func(s *storageAssociationSnapshot) { s.Native.Directory = false },
		"noncanonical-path":  func(s *storageAssociationSnapshot) { s.Native.Path = "/home/agent/../scratch" },
		"missing-source":     func(s *storageAssociationSnapshot) { s.Source = storageBlockIdentity{} },
		"nonblock-source":    func(s *storageAssociationSnapshot) { s.Source.Block = false },
		"source-path":        func(s *storageAssociationSnapshot) { s.Source.Source = "/dev/other" },
		"source-rdev":        func(s *storageAssociationSnapshot) { s.Source.Device = "254:18" },
		"sysfs-rdev":         func(s *storageAssociationSnapshot) { s.Source.SysfsDevice = "254:18" },
		"sysfs-object":       func(s *storageAssociationSnapshot) { s.Source.SysfsObject.Inode++ },
		"sysfs-nondirectory": func(s *storageAssociationSnapshot) { s.Source.SysfsDirectory = false },
		"sysfs-outside":      func(s *storageAssociationSnapshot) { s.Source.SysfsPath = "/private/vdb1" },
		"no-membership":      func(s *storageAssociationSnapshot) { s.Members = nil },
		"membership-device":  func(s *storageAssociationSnapshot) { s.Members[0].Device = "254:18" },
		"membership-object":  func(s *storageAssociationSnapshot) { s.Members[0].Object.Inode++ },
		"membership-path":    func(s *storageAssociationSnapshot) { s.Members[0].SysfsPath = "/sys/devices/other" },
		"bad-uuid": func(s *storageAssociationSnapshot) {
			s.Members[0].UUID = strings.ReplaceAll(storageFixtureUUID, "-", "") + "----"
		},
		"duplicate-member": func(s *storageAssociationSnapshot) { s.Members = append(s.Members, s.Members[0]) },
		"same-object-different-device": func(s *storageAssociationSnapshot) {
			m := s.Members[0]
			m.Name = "other"
			m.Device = "8:1"
			s.Members = append(s.Members, m)
		},
		"multiple-uuid": func(s *storageAssociationSnapshot) {
			m := s.Members[0]
			m.UUID = "87654321-4321-4abc-8def-ba0987654321"
			s.Members = append(s.Members, m)
		},
		"source-device-mapper": func(s *storageAssociationSnapshot) { s.Source.ResolvedSource = "/dev/dm-0" },
		"sysfs-device-mapper":  func(s *storageAssociationSnapshot) { s.Source.SysfsPath = "/sys/devices/virtual/block/dm-0" },
		"other-member-device-mapper": func(s *storageAssociationSnapshot) {
			m := s.Members[0]
			m.Name = "dm-0"
			m.Device = "253:0"
			m.SysfsPath = "/sys/devices/virtual/block/dm-0"
			s.Members = append(s.Members, m)
		},
		"ambiguous-cover": func(s *storageAssociationSnapshot) {
			s.MountInfo = append(s.MountInfo, []byte("126 66 0:38 /other / rw - btrfs /dev/fixture0p1 rw\n")...)
		},
		"nested-mismatch-no-parent-fallback": func(s *storageAssociationSnapshot) {
			s.MountInfo = append(s.MountInfo, []byte("126 125 8:1 / /home/agent rw - ext4 /dev/other-fixture1 rw\n")...)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s := storageAssociationFixture()
			change(&s)
			if _, e := confirmStorageBtrfs(s, cloneStorageAssociation(s)); e == nil {
				t.Fatal("unresolved topology earned backing")
			}
		})
	}
}
func TestStorageAssociationSnapshotChanges(t *testing.T) {
	for name, change := range map[string]func(*storageAssociationSnapshot){
		"path-inode":  func(s *storageAssociationSnapshot) { s.Native.Object.Inode++ },
		"path-ctime":  func(s *storageAssociationSnapshot) { s.Native.Object.ChangeNS++ },
		"path-device": func(s *storageAssociationSnapshot) { s.Native.Device = "0:54" },
		"native-fsid": func(s *storageAssociationSnapshot) { s.Native.FilesystemID = "different" },
		"mount-options": func(s *storageAssociationSnapshot) {
			s.MountInfo = bytes.ReplaceAll(s.MountInfo, []byte("rw,noatime"), []byte("rw,relatime"))
		},
		"source-inode": func(s *storageAssociationSnapshot) { s.Source.Node.Inode++ },
		"source-link":  func(s *storageAssociationSnapshot) { s.Source.ResolvedSource = "/dev/alias" },
		"new-unrelated-member": func(s *storageAssociationSnapshot) {
			m := s.Members[0]
			m.UUID = "87654321-4321-4abc-8def-ba0987654321"
			m.Name = "fixture1p1"
			m.Device = "254:33"
			m.SysfsPath = "/sys/devices/fixture/block/fixture1/fixture1p1"
			s.Members = append(s.Members, m)
		},
	} {
		t.Run(name, func(t *testing.T) {
			before := storageAssociationFixture()
			after := cloneStorageAssociation(before)
			change(&after)
			if _, e := confirmStorageBtrfs(before, after); e == nil {
				t.Fatal("changed association accepted")
			}
		})
	}
}
func TestStorageAssociationNormalDeviceMatching(t *testing.T) {
	s := storageAssociationFixture()
	s.Native.Filesystem = "ext"
	s.MountInfo = bytes.ReplaceAll(s.MountInfo, []byte("btrfs"), []byte("ext4"))
	if _, e := selectStorageMount(s.MountInfo, s.Native); e == nil {
		t.Fatal("non-Btrfs synthetic device exception")
	}
	s.Native.Device = "0:38"
	if _, e := selectStorageMount(s.MountInfo, s.Native); e != nil {
		t.Fatal(e)
	}
}
func TestStorageAssociationMountBoundsAndMalformed(t *testing.T) {
	good := string(storageAssociationFixture().MountInfo)
	for _, data := range []string{"", strings.TrimSuffix(good, "\n"), strings.Replace(good, "0:38", "0:038", 1), strings.Replace(good, "125 66", "0 66", 1), strings.Replace(good, "/fixture-root", "/a/../b", 1), strings.Replace(good, "/fixture-root", `/bad\041path`, 1), strings.Replace(good, " - ", " - - ", 1), good + good, strings.Repeat("x", storageMountMaxLine) + "\n", strings.Repeat(good, storageMountMaxEntries+1)} {
		if _, e := parseStorageMountSnapshot([]byte(data)); e == nil {
			t.Fatalf("bad snapshot accepted: len=%d", len(data))
		}
	}
	var all strings.Builder
	for i := 1; i <= storageMountMaxEntries; i++ {
		fmt.Fprintf(&all, "%d %d 0:1 / /m%d rw - ext4 /dev/fixture0p1 rw\n", i, i+storageMountMaxEntries, i)
	}
	if _, e := readStorageMountSnapshot(strings.NewReader(all.String())); e != nil {
		t.Fatal(e)
	}
	overEntries := all.String() + fmt.Sprintf("%d %d 0:1 / /extra rw - ext4 /dev/fixture0p1 rw\n", storageMountMaxEntries+1, storageMountMaxEntries*2+1)
	if _, err := parseStorageMountSnapshot([]byte(overEntries)); err == nil {
		t.Fatal("individually valid mount entries exceeded total bound")
	}

	reader := &storageCountingReader{Reader: strings.NewReader(strings.Repeat("x", storageMountMaxBytes+10))}
	if _, e := readStorageMountSnapshot(reader); e == nil || reader.n != storageMountMaxBytes+1 {
		t.Fatal("snapshot read not byte bounded", e, reader.n)
	}
}

type storageCountingReader struct {
	io.Reader
	n int
}

func (r *storageCountingReader) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	r.n += n
	return n, e
}

func TestStorageAssociationMembershipBounds(t *testing.T) {
	for name, mutate := range map[string]func(*storageAssociationSnapshot){
		"member-count": func(s *storageAssociationSnapshot) {
			for i := 1; i <= storageBtrfsMaxMembers; i++ {
				m := s.Members[0]
				m.Name = fmt.Sprintf("disk%d", i)
				m.Device = fmt.Sprintf("8:%d", i)
				m.SysfsPath = "/sys/devices/" + m.Name
				m.Object.Inode += uint64(i)
				s.Members = append(s.Members, m)
			}
		},
		"uuid-count": func(s *storageAssociationSnapshot) {
			for i := 1; i <= storageBtrfsMaxRoots; i++ {
				m := s.Members[0]
				m.UUID = fmt.Sprintf("%08x%s", i, storageFixtureUUID[8:])
				m.Name = fmt.Sprintf("disk%d", i)
				m.Device = fmt.Sprintf("8:%d", i)
				m.SysfsPath = "/sys/devices/" + m.Name
				m.Object.Inode += uint64(i)
				s.Members = append(s.Members, m)
			}
		},
		"total-bytes": func(s *storageAssociationSnapshot) {
			for i := 1; i < storageBtrfsMaxMembers; i++ {
				m := s.Members[0]
				m.Name = fmt.Sprintf("disk%d", i)
				m.Device = fmt.Sprintf("8:%d", i)
				m.SysfsPath = "/sys/devices/" + strings.Repeat("long", 200) + m.Name
				s.Members = append(s.Members, m)
			}
		},
		"long-name":                  func(s *storageAssociationSnapshot) { s.Members[0].Name = strings.Repeat("x", 129) },
		"member-traversal":           func(s *storageAssociationSnapshot) { s.Members[0].Name = "../vdb1" },
		"noncanonical-member-device": func(s *storageAssociationSnapshot) { s.Members[0].Device = "254:017" },
		"duplicate-object-alias": func(s *storageAssociationSnapshot) {
			m := s.Members[0]
			m.Name = "alias"
			s.Members = append(s.Members, m)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := storageAssociationFixture()
			mutate(&s)
			if _, err := confirmStorageBtrfs(s, cloneStorageAssociation(s)); err == nil {
				t.Fatal("invalid/bounded membership accepted")
			}
		})
	}
}
func TestStorageAssociationCanonicalMemberOrderAndRootParent(t *testing.T) {
	s := storageAssociationFixture()
	s.MountInfo = bytes.ReplaceAll(s.MountInfo, []byte("125 66"), []byte("125 125")) // a namespace root may parent itself
	other := s.Members[0]
	other.Name = "fixture1p1"
	other.Device = "254:33"
	other.SysfsPath = "/sys/devices/fixture/block/fixture1/fixture1p1"
	other.Object.Inode++
	s.Members = append(s.Members, other)
	after := cloneStorageAssociation(s)
	after.Members[0], after.Members[1] = after.Members[1], after.Members[0]
	if _, err := confirmStorageBtrfs(s, after); err != nil {
		t.Fatal("stable membership enumeration order rejected", err)
	}
}
func TestStorageAssociationEachStableProofDimensionIsRechecked(t *testing.T) {
	for name, change := range map[string]func(*storageAssociationSnapshot){
		"mode":         func(s *storageAssociationSnapshot) { s.Native.Mode++ },
		"owner":        func(s *storageAssociationSnapshot) { s.Native.Owner++ },
		"source-ctime": func(s *storageAssociationSnapshot) { s.Source.Node.ChangeNS++ },
		"sysfs-inode-and-membership": func(s *storageAssociationSnapshot) {
			s.Source.SysfsObject.Inode++
			s.Members[0].Object = s.Source.SysfsObject
		},
		"mount-and-source-alias": func(s *storageAssociationSnapshot) {
			s.MountInfo = bytes.ReplaceAll(s.MountInfo, []byte("/dev/fixture0p1"), []byte("/dev/alias"))
			s.Source.Source = "/dev/alias"
			s.Source.ResolvedSource = "/dev/fixture0p1"
		},
	} {
		t.Run(name, func(t *testing.T) {
			before := storageAssociationFixture()
			after := cloneStorageAssociation(before)
			change(&after)
			if _, err := associateStorageBtrfs(after); err != nil {
				t.Fatal("after fixture must independently validate", err)
			}
			if _, err := confirmStorageBtrfs(before, after); err == nil {
				t.Fatal("changed valid proof accepted")
			}
		})
	}
}
func TestStorageAssociationRejectsOversizedAndControlMountFields(t *testing.T) {
	line := "125 66 0:38 / / rw - btrfs /dev/fixture0p1 " + strings.Repeat("a", storageMountMaxLine) + "\n"
	if _, err := parseStorageMountSnapshot([]byte(line)); err == nil {
		t.Fatal("valid-shape oversized line accepted")
	}
	good := string(storageAssociationFixture().MountInfo)
	for _, bad := range []string{strings.Replace(good, "rw,noatime", "rw,\x01bad", 1), strings.Replace(good, "rw,noatime", "rw,\x7fbad", 1)} {
		if _, err := parseStorageMountSnapshot([]byte(bad)); err == nil {
			t.Fatal("control-bearing mount field accepted")
		}
	}
}
