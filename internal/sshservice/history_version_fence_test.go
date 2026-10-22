package sshservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func corruptHistoryFileSameIdentity(t *testing.T, path string, restoreModified bool) {
	t.Helper()
	f, e := os.OpenFile(path, os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	info, e := f.Stat()
	if e != nil {
		f.Close()
		t.Fatal(e)
	}
	var one [1]byte
	if _, e = f.ReadAt(one[:], 0); e != nil {
		f.Close()
		t.Fatal(e)
	}
	one[0] ^= 255
	if _, e = f.WriteAt(one[:], 0); e != nil {
		f.Close()
		t.Fatal(e)
	}
	if e = f.Sync(); e != nil {
		f.Close()
		t.Fatal(e)
	}
	f.Close()
	if restoreModified {
		if e = os.Chtimes(path, info.ModTime(), info.ModTime()); e != nil {
			t.Fatal(e)
		}
	}
}
func onePublishedHistoryRecord(t *testing.T, home string) string {
	t.Helper()
	root := filepath.Join(home, SourceHistoryName, "packs")
	names, e := os.ReadDir(root)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range names {
		if strings.HasPrefix(name.Name(), "pack-") {
			return filepath.Join(root, name.Name(), "records.bin")
		}
	}
	t.Fatal("actual published pack not found")
	return ""
}
func TestSourceHistoryPackVersionLossBeforeCommitOrUnlinkPreservesHot(t *testing.T) {
	for _, stage := range []string{"before_catalog_commit", "before_unlink"} {
		for _, file := range []string{"records.bin", "manifest.json"} {
			t.Run(stage+"/"+file, func(t *testing.T) {
				home, b, items := settledSourceHistoryFixture(t, 1)
				q := sourceArchiveRequest(t, home, b, "changed-cold-pack")
				fired := false
				_, e := archiveSourceHistory(t.Context(), home, b, q, func(point string) error {
					if point == stage && !fired {
						fired = true
						path := filepath.Join(filepath.Dir(onePublishedHistoryRecord(t, home)), file)
						corruptHistoryFileSameIdentity(t, path, true)
					}
					return nil
				})
				if !fired {
					t.Fatal("actual destructive boundary not reached")
				}
				if e == nil {
					t.Fatal("changed same-inode published bytes passed destructive fence")
				}
				if stage == "before_catalog_commit" {
					op, inspectErr := InspectSourceHistoryOperation(t.Context(), home, b, q.Key)
					if inspectErr != nil || op.Phase != "published" {
						t.Fatal("mutated pack committed a permanent fence", inspectErr)
					}
					state, inspectErr := ReadState(t.Context(), home, b)
					if inspectErr != nil || state.History.Archived != 0 {
						t.Fatal("mutated pack created cold key references", inspectErr)
					}
				}
				if _, e = os.Stat(filepath.Join(home, "mesh", completionName(items[0]))); e != nil {
					t.Fatal("unverified cold pack caused original hot receipt loss", e)
				}
			})
		}
	}
}
func TestSourceHistoryRecoveryPackOrCatalogVersionLossStaysPending(t *testing.T) {
	for _, stage := range []string{"after_recovery_validation", "before_recovery_completed"} {
		for _, which := range []string{"records.bin", "manifest.json", "catalog-restored-mtime"} {
			t.Run(stage+"/"+which, func(t *testing.T) {
				home, b, _ := settledSourceHistoryFixture(t, 1)
				if _, e := ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "before-recovery")); e != nil {
					t.Fatal(e)
				}
				crashSourceHistoryCatalog(t, home)
				q, e := PlanSourceHistoryRecovery(t.Context(), home, b, "changed-validated-history", "Preserve exact pending fence if any validated evidence changes")
				if e != nil {
					t.Fatal(e)
				}
				fired := false
				_, e = recoverSourceHistoryCatalog(t.Context(), home, b, q, func(point string) error {
					if point == stage && !fired {
						fired = true
						path := filepath.Join(filepath.Dir(onePublishedHistoryRecord(t, home)), which)
						if which == "catalog-restored-mtime" {
							path = filepath.Join(home, SourceHistoryName, SourceHistoryCatalogName)
						}
						corruptHistoryFileSameIdentity(t, path, true)
					}
					return nil
				})
				if !fired {
					t.Fatal("real postvalidation boundary not reached")
				}
				if e == nil {
					t.Fatal("changed verified history published completed recovery")
				}
				got, e := InspectSourceHistoryRecovery(t.Context(), home, b, q.Key)
				if e != nil || got.Completed != nil {
					t.Fatal("changed history lost pending original fence", e)
				}
			})
		}
	}
}
