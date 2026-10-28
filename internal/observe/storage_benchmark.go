package observe

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/storage"
)

// BenchmarkStorage writes only one exclusive private scratch file. Each phase
// has a bounded byte/operation count and checks context between native I/O calls.
// Native I/O itself cannot be forcibly cancelled; callers retain admission until
// this function joins. No global cache eviction or raw-device access is used.
func BenchmarkStorage(ctx context.Context, path, volume string, size int64) (model.StoragePerformance, error) {
	return benchmarkStorage(ctx, path, volume, size, nil)
}

// BenchmarkStorageOwned checks the exact already-recorded managed directory on
// the opened root before creating scratch. Later path/mount replacement cannot
// retarget the rooted file operations onto another disk or unrelated directory.
func BenchmarkStorageOwned(ctx context.Context, path, volume string, size int64, expected model.FileIdentity) (model.StoragePerformance, error) {
	return benchmarkStorage(ctx, path, volume, size, &expected)
}
func benchmarkStorage(ctx context.Context, path, volume string, size int64, expected *model.FileIdentity) (result model.StoragePerformance, err error) {
	if size < 8<<20 || size > 1<<30 || size%(1<<20) != 0 {
		return result, fmt.Errorf("benchmark size must be 8MiB..1GiB in whole MiB")
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return result, err
	}
	defer root.Close()
	if expected != nil {
		directory, e := root.Open(".")
		if e != nil {
			return result, e
		}
		actual, identityErr := storage.DirectoryIdentity(int(directory.Fd()))
		info, statErr := directory.Stat()
		closeErr := directory.Close()
		if identityErr != nil || statErr != nil || closeErr != nil || !expected.BirthKnown || actual != *expected || info == nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || actual.Owner != uint32(os.Geteuid()) {
			return result, fmt.Errorf("managed benchmark directory identity, type or ownership changed")
		}
	}
	name := ".arm-benchmark-" + local.ID()
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, f.Close(), root.Remove(name)) }()
	method, err := benchmarkCacheMode(f)
	if err != nil {
		return result, err
	}
	buffer := make([]byte, 1<<20)
	if _, err = rand.Read(buffer); err != nil {
		return result, err
	}
	result = model.StoragePerformance{At: time.Now().UTC(), Volume: volume, Bytes: size, BlockBytes: len(buffer), Method: method, Detail: "Sequential 1MiB write including fsync, sequential 1MiB read, random 4KiB reads at queue depth 1. Incompressible scratch content; read cache policy is platform-specific. Filesystem/path measurements under current contention; not raw-drive ratings, guaranteed bandwidth or power-loss durability."}
	started := time.Now()
	writeStart := time.Now()
	for offset := int64(0); offset < size; offset += int64(len(buffer)) {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		// Random content is prepared before timing. Give every 4KiB block a
		// distinct header without timing the entropy generator as drive writes.
		for block := 0; block < len(buffer); block += 4096 {
			binary.LittleEndian.PutUint64(buffer[block:block+8], uint64(offset)+uint64(block))
		}
		if _, err = f.Write(buffer); err != nil {
			return result, err
		}
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = f.Sync(); err != nil {
		return result, err
	}
	result.WriteBytesPerSecond = float64(size) / time.Since(writeStart).Seconds()
	if err = benchmarkDropCache(f, size); err != nil {
		return result, err
	}
	readStart := time.Now()
	for offset := int64(0); offset < size; offset += int64(len(buffer)) {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if _, err = f.ReadAt(buffer, offset); err != nil {
			return result, err
		}
	}
	result.ReadBytesPerSecond = float64(size) / time.Since(readStart).Seconds()
	if err = benchmarkDropCache(f, size); err != nil {
		return result, err
	}
	offsets := make([]int64, 512)
	for i := range offsets {
		n, e := rand.Int(rand.Reader, big.NewInt(size/4096))
		if e != nil {
			return result, e
		}
		offsets[i] = n.Int64() * 4096
	}
	randomStart := time.Now()
	for _, offset := range offsets {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if _, err = f.ReadAt(buffer[:4096], offset); err != nil {
			return result, err
		}
	}
	elapsed := time.Since(randomStart)
	result.RandomReadIOPS = float64(len(offsets)) / elapsed.Seconds()
	result.RandomReadLatencyNS = int64(elapsed) / int64(len(offsets))
	result.Duration = time.Since(started)
	return result, ctx.Err()
}
