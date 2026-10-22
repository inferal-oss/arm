package sharefs

import (
	"context"
	"sync"
)

// These limits include reserved fills AND pinned readers. Each cached chunk,
// including a short final chunk, reserves one full MiB: both byte and metadata
// cardinality stay bounded. One authority must use one Engine, never one/peer.
const VerifiedCacheOriginBytes = 8 * ChunkBytes
const VerifiedCacheAuthorityBytes = 32 * ChunkBytes

type chunkKey struct {
	origin  *Origin // an exact session; never reused across Close/reopen/restart
	binding Binding
	path    string
	version Version
	digest  string
	index   int64
}
type chunkEntry struct {
	key     chunkKey
	ready   chan struct{}
	data    []byte // immutable; never exposed outside Read's verified copy
	err     error
	refs    int
	filling bool
	drop    bool
	touched uint64
}
type chunkCache struct {
	mu      sync.Mutex // bookkeeping only, never held over I/O, guard or waiting
	entries map[chunkKey]*chunkEntry
	origins map[*Origin]int
	changed chan struct{}
	clock   uint64
}

func newChunkCache() *chunkCache {
	return &chunkCache{entries: map[chunkKey]*chunkEntry{}, origins: map[*Origin]int{}, changed: make(chan struct{})}
}
func (c *chunkCache) wake() { close(c.changed); c.changed = make(chan struct{}) }
func (c *chunkCache) remove(v *chunkEntry) {
	if c.entries[v.key] != v {
		return
	}
	delete(c.entries, v.key)
	c.origins[v.key.origin]--
	if c.origins[v.key.origin] == 0 {
		delete(c.origins, v.key.origin)
	}
	v.data = nil
	c.wake()
}
func (c *chunkCache) evict(o *Origin) bool {
	var oldest *chunkEntry
	originFull := c.origins[o] >= VerifiedCacheOriginBytes/ChunkBytes
	for _, v := range c.entries {
		if v.filling || v.refs != 0 || originFull && v.key.origin != o {
			continue
		}
		if oldest == nil || v.touched < oldest.touched {
			oldest = v
		}
	}
	if oldest == nil {
		return false
	}
	c.remove(oldest)
	return true
}
func (c *chunkCache) release(v *chunkEntry) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			v.refs--
			if v.refs == 0 && v.drop {
				c.remove(v)
			}
			c.wake()
		})
	}
}

// acquire coalesces only exact chunk fills, not authorization. The caller MUST
// already hold the Engine operation gate, resolve the current native path and
// authorize this operation; it MUST recheck native identity/root and current
// authorization before returning any bytes. Neither result caches permission.
func (c *chunkCache) acquire(ctx context.Context, k chunkKey, fill func() ([]byte, error)) ([]byte, func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		c.mu.Lock()
		if k.origin.cacheClosed.Load() {
			c.mu.Unlock()
			return nil, nil, ErrClosed
		}
		if v := c.entries[k]; v != nil {
			if v.drop {
				changed := c.changed
				c.mu.Unlock()
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				case <-k.origin.cacheDone:
					return nil, nil, ErrClosed
				case <-changed:
				}
				continue
			}
			if v.filling {
				c.mu.Unlock()
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				case <-k.origin.cacheDone:
					return nil, nil, ErrClosed
				case <-v.ready:
				}
				if v.err != nil {
					return nil, nil, v.err
				}
				continue
			}
			v.refs++
			c.clock++
			v.touched = c.clock
			data := v.data
			c.mu.Unlock()
			return data, c.release(v), nil
		}
		if len(c.entries) >= VerifiedCacheAuthorityBytes/ChunkBytes || c.origins[k.origin] >= VerifiedCacheOriginBytes/ChunkBytes {
			if c.evict(k.origin) {
				c.mu.Unlock()
				continue
			}
			changed := c.changed
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-k.origin.cacheDone:
				return nil, nil, ErrClosed
			case <-changed:
			}
			continue
		}
		c.clock++
		v := &chunkEntry{key: k, ready: make(chan struct{}), refs: 1, filling: true, touched: c.clock}
		c.entries[k] = v
		c.origins[k.origin]++
		c.mu.Unlock()
		// The reserved cache slot covers this allocation before the fill starts.
		data, err := fill()
		if err == nil {
			err = ctx.Err()
		}
		c.mu.Lock()
		if err == nil && k.origin.cacheClosed.Load() {
			err = ErrClosed
		}
		if err == nil && (len(data) < 1 || len(data) > ChunkBytes) {
			err = ErrLimit
		}
		v.filling = false
		v.err = err
		if err != nil {
			c.remove(v)
		} else {
			v.data = data
		}
		close(v.ready)
		c.wake()
		c.mu.Unlock()
		if err != nil {
			return nil, nil, err
		}
		return data, c.release(v), nil
	}
}
func (c *chunkCache) closeOrigin(o *Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, v := range c.entries {
		if v.key.origin == o {
			v.drop = true
			if !v.filling && v.refs == 0 {
				c.remove(v)
			}
		}
	}
	c.wake()
}

// waitDrained checks the actual reserved cache roster, including fills and pins,
// independently of operation count. Removing idle entries happens in Close.
func (c *chunkCache) waitDrained(ctx context.Context, o *Origin) error {
	for {
		c.mu.Lock()
		remaining := c.origins[o]
		changed := c.changed
		c.mu.Unlock()
		if remaining == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}
