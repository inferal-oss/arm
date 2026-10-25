package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// Injected errors are
// explicitly controls after real Close, not evidence of an OS Close failure.
func TestSnapshotFDRealOpenFailureAndConfinedWalkCloseReceipts(t *testing.T) {
	p, a := fixture(t)
	if err := os.Mkdir(filepath.Join(a.Path, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Path, "dir", "file"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	owners := newSnapshotFDSet()
	t.Cleanup(func() { _ = owners.closeAll() })
	provider, err := p.snapshotRetain(owners)
	if err != nil {
		t.Fatal(err)
	}
	root, err := provider.snapshotOpenArtifact(deadline(t), a, owners, "test-root")
	if err != nil {
		t.Fatal(err)
	}
	file, err := owners.openFile(root, "dir/file", "test-file")
	if err != nil {
		t.Fatal(err)
	}
	if err = owners.close(file); err != nil {
		t.Fatal(err)
	}
	if _, err = owners.openFile(root, "dir/missing", "missing-file"); !errors.Is(err, unix.ENOENT) {
		t.Fatal("actual ENOENT missing", err)
	}
	if err = owners.closeAll(); err != nil {
		t.Fatal(err)
	}
	receipt := owners.receipt()
	if !receipt.Closed || receipt.Unknown || receipt.HighWater > 8 || receipt.validateClosed() != nil {
		t.Fatal("actual descriptor closure incomplete", receipt)
	}
	failed := false
	walked := false
	for _, d := range receipt.Descriptors {
		if d.Role == "missing-file" && d.State == "not-opened" && d.Error != "" {
			failed = true
		}
		if d.Role == "walk-directory" && d.State == "closed" {
			walked = true
		}
	}
	if !failed || !walked {
		t.Fatal("real open failure/intermediate walk not captured", receipt)
	}
}

func TestSnapshotFDUnknownCloseIsStickyAndNeverClosesLaterFile(t *testing.T) {
	for _, scenario := range []string{"reported-error-after-real-close", "real-already-closed-file"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "original")
			owners := newSnapshotFDSet()
			file, err := owners.acquire("original", func() (int, error) { return unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, 0600) })
			if err != nil {
				t.Fatal(err)
			}
			attempts := 0
			failure := errors.New("explicit close-uncertainty control")
			if scenario == "reported-error-after-real-close" {
				owners.closeFile = func(f *os.File) error { attempts++; return errors.Join(f.Close(), failure) }
			} else {
				if err = file.Close(); err != nil {
					t.Fatal(err)
				}
				owners.closeFile = func(f *os.File) error { attempts++; return f.Close() }
			}
			first := owners.close(file)
			if !errors.Is(first, errSnapshotClose) {
				t.Fatal("real/injected close refusal erased", first)
			}
			if scenario == "reported-error-after-real-close" && !errors.Is(first, failure) {
				t.Fatal("first failure identity changed", first)
			}
			// A real new File is opened after the original close. Regardless of
			// whether the kernel chooses the same integer, old receipt cleanup
			// must never act on this new object's descriptor.
			replacement, err := os.OpenFile(filepath.Join(t.TempDir(), "replacement"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer replacement.Close()
			for range 3 {
				if err = owners.close(file); !errors.Is(err, errSnapshotClose) {
					t.Fatal(err)
				}
				if err = owners.closeAll(); !errors.Is(err, errSnapshotClose) {
					t.Fatal(err)
				}
			}
			if attempts != 1 {
				t.Fatal("unknown original Close was retried", attempts)
			}
			if _, err = replacement.Write([]byte("still-owned-by-replacement")); err != nil {
				t.Fatal("unrelated live object was closed", err)
			}
			receipt := owners.receipt()
			if receipt.Closed || !receipt.Unknown || owners.owned != 1 || len(receipt.Descriptors) != 1 || receipt.Descriptors[0].State != "unknown" || receipt.Descriptors[0].CloseError == "" {
				t.Fatal("unknown close duty/slot erased", receipt)
			}
		})
	}
}

func TestSnapshotFDBudgetRefusesBeforeOpeningAndClosesEveryOriginalOnce(t *testing.T) {
	owners := newSnapshotFDSet()
	defer owners.closeAll()
	for i := 0; i < 8; i++ {
		if _, err := owners.acquire("quota", func() (int, error) { return unix.Open(t.TempDir(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0) }); err != nil {
			t.Fatal(err)
		}
	}
	opened := false
	if _, err := owners.acquire("ninth", func() (int, error) { opened = true; return -1, errors.New("must not open") }); err == nil || opened {
		t.Fatal("over-budget native open dispatched", opened, err)
	}
	if err := owners.closeAll(); err != nil {
		t.Fatal(err)
	}
	if !owners.receipt().Closed || owners.high != 8 || owners.owned != 0 {
		t.Fatal("actual quota originals not positively closed", owners.receipt())
	}
}
