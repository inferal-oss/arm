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
// Active buffer space is at most2MiB (one result plus one verified scratch),
// bounded globally by Engine.Reads, never multiplied by the number of sessions.
func (o *Origin) Read(ctx context.Context, token string, offset int64, length int) ([]byte, error) {
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
	// Buffer allocation is admitted by the global gate. At most two chunks are
	// needed for an unaligned one-MiB read; only one chunk scratch is allocated.
	out := make([]byte, int(n))
	scratch := make([]byte, ChunkBytes)
	for copied := int64(0); copied < n; {
		if err = o.authorize(ctx, "read", r.path); err != nil {
			return nil, err
		}
		position := offset + copied
		chunk := position / ChunkBytes
		begin := chunk * ChunkBytes
		chunkN := min(int64(ChunkBytes), size-begin)
		if _, err = f.ReadAt(scratch[:chunkN], begin); err != nil {
			return nil, err
		}
		h := sha256.Sum256(scratch[:chunkN])
		if hex.EncodeToString(h[:]) != x.Chunks[chunk] {
			return nil, ErrChanged
		}
		take := min(n-copied, chunkN-(position-begin))
		copy(out[copied:copied+take], scratch[position-begin:position-begin+take])
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
