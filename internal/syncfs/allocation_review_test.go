package syncfs

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

// Allocation is an observation per directory entry, not exclusive storage:
// hardlinks count twice, sparse files use native blocks, and link targets do
// not enter the result. This fixture has no Manager/admission claim.
func TestWorkspaceAllocationReviewReportsNativeBlocksOnly(t *testing.T) {
	source, selected, parent, w, reservation := testWorkspaceEvidence(t)
	data := filepath.Join(w.StagePath(), "data")
	allocationReviewWrite(t, data, make([]byte, 4<<20))
	if err := os.Link(data, filepath.Join(w.QuarantinePath(), "same-inode")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(source.Path(), "outside")
	allocationReviewWrite(t, outside, make([]byte, 8<<20))
	if err := os.Symlink(outside, filepath.Join(w.StagePath(), "outside-link")); err != nil {
		t.Fatal(err)
	}
	sparse, err := os.OpenFile(filepath.Join(w.StagePath(), "sparse"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := sparse.Truncate(16 << 20); err != nil {
		sparse.Close()
		t.Fatal(err)
	}
	if err := sparse.Sync(); err != nil {
		sparse.Close()
		t.Fatal(err)
	}
	if err := sparse.Close(); err != nil {
		t.Fatal(err)
	}
	var expected WorkspaceAllocation
	err = filepath.WalkDir(w.Path(), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		var st unix.Stat_t
		if err := unix.Lstat(path, &st); err != nil {
			return err
		}
		if st.Blocks < 0 || st.Blocks > math.MaxInt64/512 {
			t.Fatal("fixture has invalid native blocks")
		}
		expected.Entries++
		expected.AllocatedBytes += st.Blocks * 512
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before := allocationReviewSnapshot(t, w.Path())
	outsideBefore := allocationReviewSnapshot(t, source.Path())
	got, err := SurveyWorkspace(t.Context(), selected, parent, w.Path(), reservation)
	if err != nil || got != expected {
		t.Fatal("survey differs from independent native per-entry block sum", got, expected, err)
	}
	if got.Entries != 8 {
		t.Fatal("hardlink/sparse/link entries omitted", got.Entries)
	}
	if !reflect.DeepEqual(before, allocationReviewSnapshot(t, w.Path())) || !reflect.DeepEqual(outsideBefore, allocationReviewSnapshot(t, source.Path())) {
		t.Fatal("survey changed private or outside fixture evidence")
	}
}

func allocationReviewWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := f.Write(data); err != nil || n != len(data) {
		f.Close()
		t.Fatal("fixture write incomplete", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// Access time is excluded because native observational reads may update it.
// Names, ownership, modes, device/inode, sizes, links, blocks, modification/
// change times and payload/link-target evidence are retained independently.
type allocationReviewFile struct {
	Mode, UID, GID                                         uint32
	Size, Blocks, MtimeSec, MtimeNsec, CtimeSec, CtimeNsec int64
	Device, Inode, Links                                   uint64
	Data                                                   string
}

func allocationReviewSnapshot(t *testing.T, root string) map[string]allocationReviewFile {
	t.Helper()
	out := map[string]allocationReviewFile{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		var st unix.Stat_t
		if err := unix.Lstat(path, &st); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		mt, mn := statMtime(st)
		ct, cn := statCtime(st)
		r := allocationReviewFile{Mode: uint32(st.Mode), UID: st.Uid, GID: st.Gid, Size: st.Size, Blocks: st.Blocks, Device: uint64(st.Dev), Inode: st.Ino, Links: uint64(st.Nlink), MtimeSec: mt, MtimeNsec: mn, CtimeSec: ct, CtimeNsec: cn}
		if entry.Type()&os.ModeSymlink != 0 {
			r.Data, err = os.Readlink(path)
		} else if entry.Type().IsRegular() {
			var raw []byte
			raw, err = os.ReadFile(path)
			r.Data = digestWorkspaceTest(string(raw))
		}
		if err != nil {
			return err
		}
		out[rel] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWorkspaceAllocationReviewEntryBoundReturnsNoPartialResult(t *testing.T) {
	_, selected, parent, w, reservation := testWorkspaceEvidence(t)
	// Base, marker, stage and quarantine already account for four entries.
	for i := 0; i < maxWorkspaceSurveyEntries-4; i++ {
		f, err := os.OpenFile(filepath.Join(w.StagePath(), fmt.Sprintf("e-%06d", i)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Fsync(w.Stage.FD()); err != nil {
		t.Fatal(err)
	}
	got, err := SurveyWorkspace(t.Context(), selected, parent, w.Path(), reservation)
	if err != nil || got.Entries != maxWorkspaceSurveyEntries {
		t.Fatal("exact entry ceiling refused", got.Entries, err)
	}
	marker, err := os.ReadFile(filepath.Join(w.Path(), workspaceMarkerName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.StagePath(), "over-limit"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	got, err = SurveyWorkspace(t.Context(), selected, parent, w.Path(), reservation)
	if !errors.Is(err, ErrUnknown) || got != (WorkspaceAllocation{}) {
		t.Fatal("entry overflow returned a usable partial result", got, err)
	}
	after, err := os.ReadFile(filepath.Join(w.Path(), workspaceMarkerName))
	if err != nil || string(after) != string(marker) {
		t.Fatal("refusal changed marker", err)
	}
	entries, err := os.ReadDir(w.StagePath())
	if err != nil || len(entries) != maxWorkspaceSurveyEntries-3 {
		t.Fatal("survey changed bounded native entry set", len(entries), err)
	}
}

func TestWorkspaceAllocationReviewDepthAndArithmeticRefuse(t *testing.T) {
	t.Run("native-depth", func(t *testing.T) {
		_, selected, parent, w, reservation := testWorkspaceEvidence(t)
		path := w.StagePath()
		for i := 1; i < maxWorkspaceSurveyDepth; i++ {
			path = filepath.Join(path, "d")
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		}
		got, err := SurveyWorkspace(t.Context(), selected, parent, w.Path(), reservation)
		if err != nil || got.Entries != maxWorkspaceSurveyDepth+3 {
			t.Fatal("exact native depth refused", got, err)
		}
		if err := os.Mkdir(filepath.Join(path, "d"), 0700); err != nil {
			t.Fatal(err)
		}
		got, err = SurveyWorkspace(t.Context(), selected, parent, w.Path(), reservation)
		if !errors.Is(err, ErrUnknown) || got != (WorkspaceAllocation{}) {
			t.Fatal("depth overflow returned partial evidence", got, err)
		}
	})
	t.Run("invalid-and-overflow-blocks", func(t *testing.T) {
		for _, blocks := range []int64{-1, math.MaxInt64/512 + 1} {
			v := WorkspaceAllocation{}
			if err := v.add(unix.Stat_t{Blocks: blocks}); !errors.Is(err, ErrUnknown) || v != (WorkspaceAllocation{}) {
				t.Fatal("invalid native blocks entered sum", v, err)
			}
		}
		v := WorkspaceAllocation{}
		if err := v.add(unix.Stat_t{Blocks: math.MaxInt64 / 512}); err != nil {
			t.Fatal(err)
		}
		before := v
		if err := v.add(unix.Stat_t{Blocks: 1}); !errors.Is(err, ErrUnknown) || v != before {
			t.Fatal("allocation overflow changed accepted prefix", v, err)
		}
	})
}
