package observe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"syscall"

	"github.com/inferal-oss/arm/internal/model"
)

// Scan walks a bounded tree using rooted operations. Symlinks and child mounts
// are not followed. Its sums are observational, not a deletion authorization.
func Scan(ctx context.Context, path string) (model.StorageMeasurement, error) {
	var result model.StorageMeasurement
	abs, err := filepath.Abs(path)
	if err != nil {
		return result, err
	}
	result.Path = abs
	root, err := os.OpenRoot(abs)
	if err != nil {
		return result, err
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		return result, err
	}
	base, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return result, fmt.Errorf("filesystem identity unavailable")
	}
	result.Filesystem = Filesystem(ctx, abs)
	seen := map[[2]uint64]bool{}
	logical, allocated := int64(0), int64(0)
	var visit func(string, int) error
	visit = func(path string, depth int) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if depth > 128 {
			return fmt.Errorf("scan directory depth limit reached (128)")
		}
		if result.Entries >= 100000 {
			return fmt.Errorf("scan entry limit reached (100000)")
		}
		result.Entries++
		info, e := root.Lstat(path)
		if e != nil {
			return e
		}
		s, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("file identity unavailable")
		}
		if s.Dev != base.Dev {
			result.MountsSkipped++
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			result.SymlinksSkipped++
			return nil
		}
		if info.IsDir() {
			// Read in small batches rather than collecting a large directory in
			// memory before the entry limit can be checked. Reject a swapped
			// symlink and verify the opened directory's identity and device.
			dir, e := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
			if e != nil {
				return e
			}
			defer dir.Close()
			opened, e := dir.Stat()
			if e != nil {
				return e
			}
			if !os.SameFile(info, opened) {
				return fmt.Errorf("directory changed during scan")
			}
			for {
				entries, e := dir.ReadDir(256)
				if e != nil && !errors.Is(e, io.EOF) {
					return e
				}
				for _, entry := range entries {
					if e := visit(filepath.Join(path, entry.Name()), depth+1); e != nil {
						return e
					}
				}
				if errors.Is(e, io.EOF) {
					return nil
				}
			}
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		id := [2]uint64{uint64(s.Dev), s.Ino}
		if seen[id] {
			result.HardLinksSkipped++
			return nil
		}
		seen[id] = true
		if info.Size() < 0 || s.Blocks < 0 || s.Blocks > math.MaxInt64/512 || logical > math.MaxInt64-info.Size() || allocated > math.MaxInt64-s.Blocks*512 {
			return fmt.Errorf("file counters overflow")
		}
		logical += info.Size()
		allocated += s.Blocks * 512
		return nil
	}
	err = visit(".", 0)
	result.Logical = known(float64(logical), "bytes", "unique-regular-file-inodes:"+abs, "rooted-walk:st_size", "reported")
	result.Allocated = known(float64(allocated), "bytes", "unique-regular-file-inodes:"+abs, "rooted-walk:st_blocks*512", "reported")
	for _, measurement := range []*model.Measurement{&result.Logical, &result.Allocated} {
		measurement.Detail = "Non-atomic walk; regular files only; hard links deduplicated within this walk; directory metadata and skipped symlinks/mounts excluded"
		if err != nil {
			measurement.Complete = false
			measurement.Quality = "partial"
			measurement.Detail += "; stopped: " + err.Error()
		}
	}
	result.Allocated.Detail += "; clone/shared extents can still be counted more than once"
	result.Exclusive = unknown("bytes", abs, "none", "Exclusive extents and external hard links have not been resolved")
	result.Reclaimable = unknown("bytes", abs, "none", "Snapshots, clones, open files, external hard links and filesystem policies can prevent or defer space reclamation; measurement grants no deletion authority")
	// Partial observations are useful results with explicit completeness; opening
	// the root or establishing identity, by contrast, fails the entire operation.
	if errors.Is(err, context.Canceled) {
		return result, err
	}
	return result, nil
}
