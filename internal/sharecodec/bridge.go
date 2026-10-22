// Package sharecodec bounds the maintained SFTP codec before packet allocation.
// It is a read-only file codec, not authentication, a listener or a mount.
package sharecodec

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	Credits     = 8
	MaxIncoming = 64 << 10
	// v1.13.11's receiver caps the complete SFTP message at256KiB even
	// with MaxPacketUnchecked. Leave room for type/ID/length/metadata.
	MaxData     = (256 << 10) - 9 // DATA: type1 + requestID4 + string length4.
	MaxOutgoing = 256 << 10
	MaxRequests = 1 << 20
)

var ErrProtocol = errors.New("bounded read-only SFTP protocol refused")

type ticket struct {
	kind       byte
	responding bool
}
type BridgeStats struct{ Accepted, HighWater, Outstanding int }

// Bridge bounds all request classes, including INIT and failing requests.
// abort MUST close the exact retained underlying socket without acquiring any
// codec, Bridge, TLS-write or application mutex. RequestServer.Close alone can
// deadlock behind upstream's sendPacket mutex while a writer is stalled.
type Bridge struct {
	ctx                 context.Context
	cancel              context.CancelFunc
	conn                io.ReadWriteCloser
	abort               func()
	once                sync.Once
	stop                func() bool
	mu                  sync.Mutex
	live                map[uint64]*ticket
	initialized         bool
	accepted, highWater int
	credits             chan struct{}
	readMu              sync.Mutex
	rhead               [9]byte
	rpos, rremaining    int
	writeMu             sync.Mutex
	whead               [9]byte
	wused, wremaining   int
	wkey                uint64
	wticket             *ticket
}

func NewBridge(ctx context.Context, c io.ReadWriteCloser, abort func()) (*Bridge, error) {
	if ctx == nil || c == nil || abort == nil {
		return nil, fmt.Errorf("live context, stream and independent socket abort required")
	}
	ctx, cancel := context.WithCancel(ctx)
	b := &Bridge{ctx: ctx, cancel: cancel, conn: c, abort: abort, live: map[uint64]*ticket{}, credits: make(chan struct{}, Credits), rpos: 9}
	b.stop = context.AfterFunc(ctx, func() { b.Close() })
	return b, nil
}
func (b *Bridge) Stats() BridgeStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BridgeStats{b.accepted, b.highWater, len(b.live)}
}
func (b *Bridge) Close() error {
	b.once.Do(func() { b.cancel(); b.abort(); _ = b.conn.Close() })
	return nil
}
func (b *Bridge) fail() error { b.Close(); return ErrProtocol }
func allowed(k byte) bool {
	switch k {
	case 1, 3, 4, 5, 7, 8, 11, 12, 16, 17:
		return true
	}
	return false
}
func (b *Bridge) beginRequest() error {
	if _, err := io.ReadFull(b.conn, b.rhead[:4]); err != nil {
		if err == io.EOF {
			return err
		}
		return b.fail()
	}
	n := binary.BigEndian.Uint32(b.rhead[:4])
	if n < 5 || n > MaxIncoming {
		return b.fail()
	}
	if _, err := io.ReadFull(b.conn, b.rhead[4:]); err != nil {
		return b.fail()
	}
	k := b.rhead[4]
	v := binary.BigEndian.Uint32(b.rhead[5:])
	if !allowed(k) {
		return b.fail()
	}
	key := uint64(v)
	if k == 1 {
		if v != 3 {
			return b.fail()
		}
		key = 1 << 32
	}
	select {
	case b.credits <- struct{}{}:
	case <-b.ctx.Done():
		return b.ctx.Err()
	}
	b.mu.Lock()
	if _, exists := b.live[key]; exists || b.accepted >= MaxRequests || k == 1 && b.initialized || k != 1 && !b.initialized {
		b.mu.Unlock()
		<-b.credits
		return b.fail()
	}
	if k == 1 {
		b.initialized = true
	}
	b.accepted++
	b.live[key] = &ticket{kind: k}
	b.highWater = max(b.highWater, len(b.live))
	b.mu.Unlock()
	b.rpos = 0
	b.rremaining = int(n) - 5
	return nil
}
func (b *Bridge) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b.readMu.Lock()
	defer b.readMu.Unlock()
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if b.rpos == len(b.rhead) && b.rremaining == 0 {
		if err := b.beginRequest(); err != nil {
			return 0, err
		}
	}
	if b.rpos < len(b.rhead) {
		n := copy(p, b.rhead[b.rpos:])
		b.rpos += n
		return n, nil
	}
	limit := min(len(p), b.rremaining)
	n, err := b.conn.Read(p[:limit])
	if n < 0 || n > limit {
		return 0, b.fail()
	}
	b.rremaining -= n
	if err != nil {
		if err == io.EOF && b.rremaining == 0 && n > 0 {
			return n, nil
		}
		return n, b.fail()
	}
	if n == 0 {
		return 0, b.fail()
	}
	return n, nil
}
func matches(request, response byte) bool {
	if request == 1 {
		return response == 2
	}
	if response == 101 {
		return true
	}
	switch request {
	case 3, 11:
		return response == 102
	case 5:
		return response == 103
	case 12, 16:
		return response == 104
	case 7, 8, 17:
		return response == 105
	}
	return false
}
func (b *Bridge) beginResponse() error {
	n := binary.BigEndian.Uint32(b.whead[:4])
	if n < 5 || n > MaxOutgoing {
		return b.fail()
	}
	k := b.whead[4]
	v := binary.BigEndian.Uint32(b.whead[5:])
	key := uint64(v)
	if k == 2 {
		if v != 3 {
			return b.fail()
		}
		key = 1 << 32
	}
	b.mu.Lock()
	t, ok := b.live[key]
	if !ok || t.responding || !matches(t.kind, k) {
		b.mu.Unlock()
		return b.fail()
	}
	t.responding = true
	b.mu.Unlock()
	b.wkey, b.wticket, b.wremaining = key, t, int(n)-5
	return nil
}
func (b *Bridge) completeResponse() error {
	b.mu.Lock()
	t, ok := b.live[b.wkey]
	if !ok || t != b.wticket || !t.responding {
		b.mu.Unlock()
		return b.fail()
	}
	delete(b.live, b.wkey)
	b.mu.Unlock()
	<-b.credits
	b.wused = 0
	b.wticket = nil
	return nil
}
func writeFull(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if n < 0 || n > len(p) {
			return ErrProtocol
		}
		p = p[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
func (b *Bridge) Write(p []byte) (int, error) {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	consumed := 0
	for len(p) > 0 {
		if err := b.ctx.Err(); err != nil {
			return consumed, err
		}
		if b.wused < 9 {
			n := copy(b.whead[b.wused:], p)
			b.wused += n
			consumed += n
			p = p[n:]
			if b.wused >= 4 {
				size := binary.BigEndian.Uint32(b.whead[:4])
				if size < 5 || size > MaxOutgoing {
					return consumed, b.fail()
				}
			}
			if b.wused < 9 {
				continue
			}
			if err := b.beginResponse(); err != nil {
				return consumed, err
			}
			if err := writeFull(b.conn, b.whead[:]); err != nil {
				b.Close()
				return consumed, err
			}
			if b.wremaining == 0 {
				if err := b.completeResponse(); err != nil {
					return consumed, err
				}
			}
			continue
		}
		n := min(len(p), b.wremaining)
		if err := writeFull(b.conn, p[:n]); err != nil {
			b.Close()
			return consumed, err
		}
		consumed += n
		p = p[n:]
		b.wremaining -= n
		if b.wremaining == 0 {
			if err := b.completeResponse(); err != nil {
				return consumed, err
			}
		}
	}
	return consumed, nil
}
