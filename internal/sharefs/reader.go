package sharefs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math"
)

// Reader is deliberately inaccessible by path or caller-constructed JSON.
// An Origin resolves its opaque handle only within its own exact session.
type Reader struct {
	origin *Origin
	path   string
	closed bool
}

// Read returns at most one MiB. No caller buffer is populated before EVERY
// covered chunk is verified and final current authorization/identity succeeds.
// Each result is at most1MiB, globally bounded by Engine.Reads. Verified chunk
// bytes (including in-flight fills/pinned readers) separately reserve <=8MiB per
// Origin and <=32MiB per Engine. Authorization and native identity are NOT cached.
func (o *Origin) Read(ctx context.Context, token string, offset int64, length int) ([]byte, error) {
	if err := o.beginOperation(); err != nil {
		return nil, err
	}
	defer o.endOperation()
	if offset < 0 || length < 0 || length > ChunkBytes || offset > math.MaxInt64-int64(length) {
		return nil, ErrLimit
	}
	o.mu.Lock()
	r, ok := o.handles[token]
	closed := !ok || r.closed || o.closed
	o.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	if err := o.authorize(ctx, "read", r.path); err != nil {
		return nil, err
	}
	if err := o.engine.enter(ctx); err != nil {
		return nil, err
	}
	defer o.engine.leave()
	f, err := o.resolve(ctx, r.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	x := o.entries[r.path]
	size := x.Version.Size
	n := min(int64(length), max(int64(0), size-offset))
	// Result allocation is admitted by the global gate. A read pins only one
	// cached chunk at a time, including an unaligned read spanning two chunks.
	out := make([]byte, int(n))
	for copied := int64(0); copied < n; {
		if err = o.authorize(ctx, "read", r.path); err != nil {
			return nil, err
		}
		position := offset + copied
		chunk := position / ChunkBytes
		begin := chunk * ChunkBytes
		chunkN := min(int64(ChunkBytes), size-begin)
		key := chunkKey{o, o.binding, r.path, x.Version, x.Chunks[chunk], chunk}
		verified, release, err := o.engine.cache.acquire(ctx, key, func() ([]byte, error) {
			scratch := make([]byte, int(chunkN))
			if _, err := f.ReadAt(scratch, begin); err != nil {
				return nil, err
			}
			h := sha256.Sum256(scratch)
			if hex.EncodeToString(h[:]) != x.Chunks[chunk] {
				return nil, ErrChanged
			}
			after, err := version(int(f.Fd()))
			if err != nil {
				return nil, err
			}
			if after != x.Version {
				return nil, ErrChanged
			}
			if err = o.finish(ctx, "read", r.path); err != nil {
				return nil, err
			}
			return scratch, nil
		})
		if err != nil {
			return nil, err
		}
		take := min(n-copied, chunkN-(position-begin))
		copy(out[copied:copied+take], verified[position-begin:position-begin+take])
		release()
		copied += take
	}
	after, err := version(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	if after != x.Version {
		return nil, ErrChanged
	}
	if err = o.finish(ctx, "read", r.path); err != nil {
		return nil, err
	}
	o.mu.Lock()
	closed = r.closed || o.closed || o.handles[token] != r
	o.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	if n < int64(length) {
		return out, io.EOF
	}
	return out, nil
}
