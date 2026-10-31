package syncfs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEnrollmentOwnerLockExcludesSecondOpenerAndReplacement(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	path := filepath.Join(base, EnrollmentOwnerLockName)
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	first, stat, err := acquireEnrollmentOwnerLock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, _, err := acquireEnrollmentOwnerLock(root); !errors.Is(err, ErrPreOpenEnrollment) {
		if second != nil {
			second.Close()
		}
		t.Fatalf("second owner acquired: %v", err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkEnrollmentOwnerLock(root, first, stat); !errors.Is(err, ErrPreOpenEnrollment) {
		t.Fatalf("replacement owner name accepted: %v", err)
	}
}

func TestPreOpenCandidateRefusesOrdinaryFilesystemAndReleasesOwner(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(base, "project")
	selectedPath := filepath.Join(projectPath, "selected")
	homePath := filepath.Join(projectPath, "home")
	for _, path := range []string{projectPath, selectedPath, homePath} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	backing, err := OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer backing.Close()
	project, err := OpenRoot(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	defer project.Close()
	selected, err := OpenRoot(selectedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Close()
	home, err := OpenRoot(homePath)
	if err != nil {
		t.Fatal(err)
	}
	defer home.Close()
	record := testEnrollmentRecord()
	for _, pair := range []struct {
		root   *Root
		assign *string
	}{
		{backing, &record.BackingRootID}, {project, &record.ProjectRootID},
		{selected, &record.SelectedRootID}, {home, &record.HomeRootID},
	} {
		*pair.assign, err = pair.root.NativeID()
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, EnrollmentRecordName), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, EnrollmentOwnerLockName), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if candidate, err := OpenPreOpenEnrollmentCandidate(context.Background(), base, projectPath, selectedPath, homePath); !errors.Is(err, ErrPreOpenEnrollment) {
		if candidate != nil {
			candidate.Close()
		}
		t.Fatalf("ordinary filesystem accepted: %v", err)
	}
	owner, _, err := acquireEnrollmentOwnerLock(backing)
	if err != nil {
		t.Fatalf("refused candidate retained owner lock: %v", err)
	}
	owner.Close()
}

// These callback values exercise Check's ordering predicate only. They are
// not native XFS evidence or production admission results.
func TestPreOpenCheckRechecksRootsAfterLaterProbe(t *testing.T) {
	for _, changed := range []string{"selected", "home", "owner-lock"} {
		t.Run(changed, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			projectPath := filepath.Join(base, "project")
			selectedPath := filepath.Join(projectPath, "selected")
			homePath := filepath.Join(projectPath, "home")
			for _, path := range []string{projectPath, selectedPath, homePath} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			c := &PreOpenEnrollmentCandidate{}
			t.Cleanup(func() { c.Close() })
			for _, pair := range []struct {
				path string
				root **Root
			}{
				{base, &c.backing}, {projectPath, &c.project},
				{selectedPath, &c.selected}, {homePath, &c.home},
			} {
				*pair.root, err = OpenRoot(pair.path)
				if err != nil {
					t.Fatal(err)
				}
			}
			record := testEnrollmentRecord()
			for _, pair := range []struct {
				root   *Root
				assign *string
			}{
				{c.backing, &record.BackingRootID}, {c.project, &record.ProjectRootID},
				{c.selected, &record.SelectedRootID}, {c.home, &record.HomeRootID},
			} {
				*pair.assign, err = pair.root.NativeID()
				if err != nil {
					t.Fatal(err)
				}
			}
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(base, EnrollmentRecordName), raw, 0600); err != nil {
				t.Fatal(err)
			}
			lockPath := filepath.Join(base, EnrollmentOwnerLockName)
			if err := os.WriteFile(lockPath, nil, 0600); err != nil {
				t.Fatal(err)
			}
			c.ownerLock, c.lockStat, err = acquireEnrollmentOwnerLock(c.backing)
			if err != nil {
				t.Fatal(err)
			}
			c.enrollment, err = ReadEnrollmentRecord(c.backing)
			if err != nil {
				t.Fatal(err)
			}
			quota := NativeQuotaEvidence{
				provider: record.Provider, scope: record.ScopeID, mount: record.MountID,
				selectedID: record.SelectedRootID, parentID: record.HomeRootID,
				hardBytes: uint64(record.ChargedHardBytes),
			}
			headroom := BackingHeadroom{mount: record.MountID, rootID: record.BackingRootID,
				total: 1 << 30, available: 1 << 29}
			quotaProbe := func(context.Context, *Root, *Root) (NativeQuotaEvidence, error) {
				return quota, nil
			}
			stableProbe := func(context.Context, *Root, *Root, *RecordedEnrollment) (BackingHeadroom, error) {
				return headroom, nil
			}
			gotQuota, gotHeadroom, err := c.checkWith(context.Background(), quotaProbe, stableProbe)
			if err != nil || gotQuota != quota || gotHeadroom != headroom {
				t.Fatalf("stable ordering control refused: quota=%+v headroom=%+v err=%v", gotQuota, gotHeadroom, err)
			}
			laterProbe := func(context.Context, *Root, *Root, *RecordedEnrollment) (BackingHeadroom, error) {
				path := selectedPath
				if changed == "home" {
					path = homePath
				} else if changed == "owner-lock" {
					path = lockPath
				}
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if changed == "owner-lock" {
					if err := os.WriteFile(path, nil, 0600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				return headroom, nil
			}
			gotQuota, gotHeadroom, err = c.checkWith(context.Background(), quotaProbe, laterProbe)
			if !errors.Is(err, ErrPreOpenEnrollment) || gotQuota != (NativeQuotaEvidence{}) || gotHeadroom != (BackingHeadroom{}) {
				t.Fatalf("replacement during later probe accepted: quota=%+v headroom=%+v err=%v", gotQuota, gotHeadroom, err)
			}
		})
	}
}
