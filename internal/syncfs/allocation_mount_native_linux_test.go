package syncfs

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Explicit opt-in only, after the runner creates a fresh private mount
// namespace. Every mount source, covered target and sentinel is test-owned.
func TestWorkspaceAllocationNativeLinuxPrivateBindMountRefusal(t *testing.T) {
	if os.Getenv("ARM_TEST_SYNC_SURVEY_PRIVATE_NS") != "1" {
		t.Skip("requires explicit private mount-namespace runner")
	}
	if os.Geteuid() != 0 {
		t.Fatal("private bind fixture requires namespace-local root")
	}
	outer := os.Getenv("ARM_SYNC_SURVEY_OUTER_NS")
	current, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := os.Readlink("/proc/1/ns/mnt")
	if err != nil || outer == "" || current == outer || current == initial {
		t.Fatal("original/initial mount namespace was not isolated", err)
	}
	mountinfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(mountinfo)), "\n") {
		fields := strings.Fields(strings.SplitN(line, " - ", 2)[0])
		if len(fields) < 6 {
			t.Fatal("invalid namespace propagation proof")
		}
		for _, field := range fields[6:] {
			if strings.HasPrefix(field, "shared:") || strings.HasPrefix(field, "master:") {
				t.Fatal("mount propagation is not private")
			}
		}
	}
	t.Logf("private namespace=%s original=%s initial=%s; all propagation private", current, outer, initial)
	for _, kind := range []string{"nested-directory", "nested-file", "nested-symlink"} {
		t.Run(kind, func(t *testing.T) {
			source, selected, parent, w, reservation := testWorkspaceEvidence(t)
			external := filepath.Join(source.Path(), "external")
			target := filepath.Join(w.StagePath(), "nested")
			if kind == "nested-directory" {
				if err := os.Mkdir(external, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
				allocationReviewWrite(t, filepath.Join(external, "external-data"), []byte("private outside allocation"))
				allocationReviewWrite(t, filepath.Join(target, "covered-data"), []byte("covered allocation"))
			} else if kind == "nested-file" {
				allocationReviewWrite(t, external, []byte("private outside allocation"))
				allocationReviewWrite(t, target, []byte("covered allocation"))
			} else {
				a := filepath.Join(source.Path(), "external-target")
				b := filepath.Join(source.Path(), "covered-target")
				allocationReviewWrite(t, a, []byte("private external link payload"))
				allocationReviewWrite(t, b, []byte("private covered link payload"))
				if err := os.Symlink(a, external); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(b, target); err != nil {
					t.Fatal(err)
				}
			}
			allocationReviewWrite(t, filepath.Join(source.Path(), "sentinel"), []byte("retained outside sentinel"))
			// Both trees originate in the same disposable t.TempDir filesystem.
			fixture := filepath.Dir(source.Path())
			for _, path := range []string{external, target} {
				rel, err := filepath.Rel(fixture, path)
				if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, "../") {
					t.Fatal("mount escaped owned fixture")
				}
			}
			covered := allocationReviewSnapshot(t, w.Path())
			outside := allocationReviewSnapshot(t, source.Path())
			parentMount, err := surveyMountFD(parent.FD())
			if err != nil {
				t.Fatal(err)
			}
			var parentStat, sourceStat unix.Stat_t
			if err := unix.Fstat(parent.FD(), &parentStat); err != nil {
				t.Fatal(err)
			}
			if err := unix.Lstat(external, &sourceStat); err != nil || sourceStat.Dev != parentStat.Dev {
				t.Fatal("fixture is not same-device", err)
			}
			if kind == "nested-symlink" {
				// open_tree clones the link inode itself; move_mount defaults
				// to not following the target link. No linked payload is mounted.
				fd, err := unix.OpenTree(unix.AT_FDCWD, external, uint(unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC|unix.AT_SYMLINK_NOFOLLOW|unix.AT_NO_AUTOMOUNT))
				if err != nil {
					t.Fatal(err)
				}
				err = unix.MoveMount(fd, "", unix.AT_FDCWD, target, unix.MOVE_MOUNT_F_EMPTY_PATH)
				unix.Close(fd)
				if err != nil {
					t.Fatal(err)
				}
			} else if err := unix.Mount(external, target, "", unix.MS_BIND, ""); err != nil {
				t.Fatal(err)
			}
			mounted := true
			defer func() {
				if mounted {
					if err := unix.Unmount(target, unix.UMOUNT_NOFOLLOW); err != nil {
						t.Error("original private mount cleanup failed", err)
						return
					}
					mounted = false
				}
				if !reflect.DeepEqual(covered, allocationReviewSnapshot(t, w.Path())) || !reflect.DeepEqual(outside, allocationReviewSnapshot(t, source.Path())) {
					t.Error("deferred cleanup changed covered/outside native evidence")
					return
				}
				t.Log("original private mount removed before exit; covered/outside snapshots unchanged")
			}()
			fd, err := unix.Open(target, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			boundMount, mountErr := surveyMountFD(fd)
			var boundStat unix.Stat_t
			statErr := unix.Fstat(fd, &boundStat)
			unix.Close(fd)
			if mountErr != nil || statErr != nil || boundStat.Dev != parentStat.Dev || boundMount == parentMount {
				t.Fatal("same-device/distinct STATX_MNT_ID counterexample missing", mountErr, statErr)
			}
			// Mount exists before inspection. Nested targets leave all original
			// marked root identities valid, so only the walker boundary can refuse.
			inspected, err := InspectWorkspace(selected, parent, w.Path(), reservation)
			if err != nil {
				t.Fatal("marker/root failure masked mount boundary", err)
			}
			if err := inspected.Close(); err != nil {
				t.Fatal(err)
			}
			visible := allocationReviewSnapshot(t, w.Path())
			got, err := SurveyWorkspace(t.Context(), selected, parent, w.Path(), reservation)
			if !errors.Is(err, ErrChanged) || got != (WorkspaceAllocation{}) {
				t.Fatal("same-device bind mount returned allocation evidence", got, err)
			}
			if !reflect.DeepEqual(visible, allocationReviewSnapshot(t, w.Path())) || !reflect.DeepEqual(outside, allocationReviewSnapshot(t, source.Path())) {
				t.Fatal("refusal changed visible/outside native evidence")
			}
			if err := unix.Unmount(target, unix.UMOUNT_NOFOLLOW); err != nil {
				t.Fatal(err)
			}
			mounted = false
			if !reflect.DeepEqual(covered, allocationReviewSnapshot(t, w.Path())) || !reflect.DeepEqual(outside, allocationReviewSnapshot(t, source.Path())) {
				t.Fatal("cleanup changed covered/outside native evidence")
			}
			t.Log("same st_dev and distinct native mount ID verified; original mount removed before exit; covered/visible/outside snapshots unchanged")
		})
	}
}
