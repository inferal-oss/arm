//go:build linux

package database

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// This opt-in fixture requires the runner's already-private mount namespace.
// Every mount source and target belongs to this test's disposable directories.
func TestForgetNativeLinuxBindMountRefusalBeforePreview(t *testing.T) {
	if os.Getenv("ARM_TEST_FORGET_MOUNT_NS") != "1" {
		t.Skip("requires explicit isolated Linux mount-namespace validation")
	}
	if os.Geteuid() != 0 {
		t.Fatal("isolated native mount fixture requires root")
	}
	outer := os.Getenv("ARM_FORGET_PARENT_MNT_NS")
	current, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := os.Readlink("/proc/1/ns/mnt")
	if err != nil || outer == "" || outer != initial || current == outer || current == initial {
		t.Fatal("private mount namespace differs from neither original nor PID1 namespace", outer, current, initial, err)
	}
	fixtureRoot := os.Getenv("ARM_FORGET_FIXTURE_ROOT")
	if filepath.Clean(fixtureRoot) != fixtureRoot || !strings.HasPrefix(fixtureRoot, "/tmp/arm-forget-linux.") {
		t.Fatal("exact disposable native fixture root required")
	}
	within := func(t *testing.T, path string) {
		t.Helper()
		rel, err := filepath.Rel(fixtureRoot, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
			t.Fatal("mount fixture escaped original disposable root", path, err)
		}
	}
	for _, name := range []string{"state.db", "ssh-source-history", "ssh-source-history/packs/pack-private", "ssh-source-history/catalog.db"} {
		t.Run(name, func(t *testing.T) {
			home := forgetFixture(t)
			attachForgetBookkeeping(t, home)
			within(t, home)
			target := filepath.Join(home, filepath.FromSlash(name))
			outside := t.TempDir()
			within(t, outside)
			external := filepath.Join(outside, "external")
			info, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			if info.IsDir() {
				err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					rel, err := filepath.Rel(target, path)
					if err != nil {
						return err
					}
					copyPath := filepath.Join(external, rel)
					if entry.IsDir() {
						return os.Mkdir(copyPath, 0700)
					}
					raw, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					return os.WriteFile(copyPath, raw, 0600)
				})
			} else {
				err = os.WriteFile(external, []byte("outside private fixture payload"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(outside, "outside-sentinel.txt")
			if info.IsDir() {
				sentinel = filepath.Join(external, "outside-sentinel.txt")
			}
			if err := os.WriteFile(sentinel, []byte("outside sentinel must survive mount refusal"), 0600); err != nil {
				t.Fatal(err)
			}
			coveredBefore := forgetSnapshot(t, home)
			outsideBefore := forgetSnapshot(t, outside)
			parent, err := OpenDirectory(home)
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Close()
			parentMount, err := forgetMountIdentity(parent.FD)
			if err != nil {
				t.Fatal(err)
			}
			var sourceStat, parentStat unix.Stat_t
			if err := unix.Stat(external, &sourceStat); err != nil {
				t.Fatal(err)
			}
			if err := unix.Fstat(int(parent.FD.Fd()), &parentStat); err != nil {
				t.Fatal(err)
			}
			if sourceStat.Dev != parentStat.Dev {
				t.Fatal("fixture does not exercise same-device mount boundary")
			}
			if err := unix.Mount(external, target, "", unix.MS_BIND, ""); err != nil {
				t.Fatal(err)
			}
			mounted := true
			defer func() {
				if mounted {
					if err := unix.Unmount(target, 0); err != nil {
						t.Error("original private bind mount did not unmount", err)
					}
				}
			}()
			bound, err := os.Open(target)
			if err != nil {
				t.Fatal(err)
			}
			boundMount, mountErr := forgetMountIdentity(bound)
			var boundStat unix.Stat_t
			statErr := unix.Fstat(int(bound.Fd()), &boundStat)
			bound.Close()
			if mountErr != nil || statErr != nil || boundStat.Dev != parentStat.Dev || boundMount == parentMount {
				t.Fatal("native bind fixture lacks same device and distinct STATX_MNT_ID", parentMount, boundMount, mountErr, statErr)
			}
			t.Logf("namespace=%s original=%s device=%d parent_mount=%s bound_mount=%s", current, outer, parentStat.Dev, parentMount, boundMount)
			visibleBefore := forgetSnapshot(t, home)
			review, err := ReviewForget(t.Context(), home, "native disposable bind-mount refusal", true)
			if review != nil {
				review.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "mount") {
				t.Fatal("real same-device mount was not refused by mount identity", err)
			}
			if !reflect.DeepEqual(visibleBefore, forgetSnapshot(t, home)) || !reflect.DeepEqual(outsideBefore, forgetSnapshot(t, outside)) {
				t.Fatal("mount refusal changed source, outside payload, sentinel, or intent namespace")
			}
			if err := unix.Unmount(target, 0); err != nil {
				t.Fatal(err)
			}
			mounted = false
			if !reflect.DeepEqual(coveredBefore, forgetSnapshot(t, home)) || !reflect.DeepEqual(outsideBefore, forgetSnapshot(t, outside)) {
				t.Fatal("private mount teardown changed original covered source or outside sentinel")
			}
			t.Log("original private bind mount removed; visible and covered source plus outside sentinel unchanged")
		})
	}
}
