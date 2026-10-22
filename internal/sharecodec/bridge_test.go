package sharecodec

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

func frame(k byte, id uint32, body ...[]byte) []byte {
	n := 5
	for _, b := range body {
		n += len(b)
	}
	out := make([]byte, 4, n+4)
	binary.BigEndian.PutUint32(out, uint32(n))
	out = append(out, k)
	out = binary.BigEndian.AppendUint32(out, id)
	for _, b := range body {
		out = append(out, b...)
	}
	return out
}
func str(s string) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(len(s)))
	return append(b, s...)
}
func rawPacket(t testing.TB, r io.Reader) []byte {
	t.Helper()
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		t.Fatal(err)
	}
	n := binary.BigEndian.Uint32(h[:])
	if n > MaxOutgoing || n < 5 {
		t.Fatalf("bad test reply length%d", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		t.Fatal(err)
	}
	return b
}
func pipeBridge(t *testing.T) (*Bridge, net.Conn) {
	t.Helper()
	server, peer := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	b, err := NewBridge(ctx, server, func() { server.Close() })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close(); b.Close() })
	return b, peer
}
func incoming(t *testing.T, b *Bridge, p net.Conn, packet []byte) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- writeFull(p, packet) }()
	got := make([]byte, len(packet))
	if _, err := io.ReadFull(b, got); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(packet, got) {
		t.Fatal("framing changed input")
	}
}
func outgoing(t *testing.T, b *Bridge, p net.Conn, packet []byte) {
	t.Helper()
	done := make(chan error, 1)
	go func() { _, err := b.Write(packet); done <- err }()
	got := make([]byte, len(packet))
	if _, err := io.ReadFull(p, got); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(packet, got) {
		t.Fatal("framing changed reply")
	}
}
func initBridge(t *testing.T, b *Bridge, p net.Conn) {
	incoming(t, b, p, frame(1, 3))
	outgoing(t, b, p, frame(2, 3))
}

func TestBridgeFragmentedAndMulticallFrames(t *testing.T) {
	b, p := pipeBridge(t)
	raw := frame(1, 3)
	done := make(chan error, 1)
	go func() {
		for _, v := range raw {
			if _, err := p.Write([]byte{v}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	got := make([]byte, len(raw))
	if _, err := io.ReadFull(b, got); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	go func() {
		reply := frame(2, 3)
		for _, v := range reply {
			if _, err := b.Write([]byte{v}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	if _, err := io.ReadFull(p, got); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	incoming(t, b, p, frame(5, 9))
	incoming(t, b, p, frame(5, 10))
	combined := append(frame(103, 9, []byte("first payload")), frame(101, 10, []byte("status payload"))...)
	go func() {
		for _, cut := range []int{2, 1, 7, 3, 11, len(combined)} {
			n := min(cut, len(combined))
			if n == 0 {
				break
			}
			if _, err := b.Write(combined[:n]); err != nil {
				done <- err
				return
			}
			combined = combined[n:]
		}
		done <- nil
	}()
	if v := rawPacket(t, p); v[0] != 103 {
		t.Fatal("first reply")
	}
	if v := rawPacket(t, p); v[0] != 101 {
		t.Fatal("second reply")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s := b.Stats(); s.Outstanding != 0 || s.HighWater > Credits {
		t.Fatalf("credit leak %#v", s)
	}
}

func TestBridgeHostileHeadersAndResponseCorrelation(t *testing.T) {
	for _, kind := range []string{"huge", "short", "truncated", "unknown", "before-init", "duplicate-init", "duplicate-id", "wrong-response-id", "wrong-response-type", "huge-response"} {
		t.Run(kind, func(t *testing.T) {
			b, p := pipeBridge(t)
			if kind == "wrong-response-id" || kind == "wrong-response-type" || kind == "huge-response" {
				initBridge(t, b, p)
				incoming(t, b, p, frame(5, 77))
				reply := frame(103, 77)
				if kind == "wrong-response-id" {
					reply = frame(103, 78)
				}
				if kind == "wrong-response-type" {
					reply = frame(105, 77)
				}
				if kind == "huge-response" {
					binary.BigEndian.PutUint32(reply, MaxOutgoing+1)
				}
				if _, err := b.Write(reply); err == nil {
					t.Fatal("hostile response accepted")
				}
				return
			}
			var raw []byte
			switch kind {
			case "huge":
				raw = binary.BigEndian.AppendUint32(nil, math.MaxUint32)
			case "short":
				raw = binary.BigEndian.AppendUint32(nil, 4)
			case "truncated":
				raw = []byte{0, 0}
			case "unknown":
				raw = frame(255, 1)
			case "before-init":
				raw = frame(5, 1)
			case "duplicate-init":
				incoming(t, b, p, frame(1, 3))
				raw = frame(1, 3)
			case "duplicate-id":
				initBridge(t, b, p)
				incoming(t, b, p, frame(5, 1))
				raw = frame(5, 1)
			}
			go func() {
				_ = writeFull(p, raw)
				if kind == "truncated" {
					p.Close()
				}
			}()
			got := make([]byte, 1024)
			_, err := b.Read(got)
			if err == nil {
				t.Fatal("hostile frame accepted")
			}
		})
	}
}

func TestCreditReleasedOnlyAfterFullReplyWritesAndAbort(t *testing.T) {
	b, p := pipeBridge(t)
	initBridge(t, b, p)
	for i := uint32(1); i <= Credits; i++ {
		incoming(t, b, p, frame(5, i))
	}
	done := make(chan error, 1)
	go func() { _, err := b.Write(frame(103, 1, bytes.Repeat([]byte("x"), 65536))); done <- err }()
	var header [9]byte
	if _, err := io.ReadFull(p, header[:]); err != nil {
		t.Fatal(err)
	}
	if s := b.Stats(); s.Outstanding != Credits {
		t.Fatal("slot released before payload", s)
	}
	closed := make(chan struct{})
	go func() { b.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("abort waited for write mutex")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked writer falsely succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("writer remained blocked")
	}
}

type lengthReader struct {
	first   chan struct{}
	release <-chan struct{}
	seen    atomic.Int64
	once    sync.Once
}

func (r *lengthReader) ReadAt(p []byte, off int64) (int, error) {
	r.seen.Store(int64(len(p)))
	if off == 0 && r.first != nil {
		r.once.Do(func() { close(r.first); <-r.release })
	}
	if len(p) > 0 {
		p[0] = 'x'
		return 1, io.EOF
	}
	return 0, io.EOF
}

type readerHandler struct{ r io.ReaderAt }

func (h readerHandler) Fileread(*sftp.Request) (io.ReaderAt, error) { return h.r, nil }

func rawCodec(t *testing.T, r io.ReaderAt) (net.Conn, *Bridge, <-chan error) {
	t.Helper()
	server, peer := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	b, err := NewBridge(ctx, server, func() { server.Close() })
	if err != nil {
		t.Fatal(err)
	}
	rs := sftp.NewRequestServer(b, sftp.Handlers{FileGet: readerHandler{r}}, sftp.WithRSMaxTxPacket(MaxData))
	done := make(chan error, 1)
	go func() { done <- rs.Serve() }()
	t.Cleanup(func() { peer.Close(); b.Close() })
	if err = writeFull(peer, frame(1, 3)); err != nil {
		t.Fatal(err)
	}
	if got := rawPacket(t, peer); got[0] != 2 {
		t.Fatal("version")
	}
	open := append(str("/file"), binary.BigEndian.AppendUint32(nil, 1)...)
	open = binary.BigEndian.AppendUint32(open, 0)
	if err = writeFull(peer, frame(3, 1, open)); err != nil {
		t.Fatal(err)
	}
	reply := rawPacket(t, peer)
	if reply[0] != 102 {
		t.Fatal("open", reply)
	}
	return peer, b, done
}
func readRequest(id uint32, off uint64, length uint32) []byte {
	b := str("1")
	b = binary.BigEndian.AppendUint64(b, off)
	b = binary.BigEndian.AppendUint32(b, length)
	return frame(5, id, b)
}

func TestActualCodecUint32MaxReadClampedBeforeAllocation(t *testing.T) {
	r := &lengthReader{}
	p, b, done := rawCodec(t, r)
	if err := writeFull(p, readRequest(2, 1, math.MaxUint32)); err != nil {
		t.Fatal(err)
	}
	if got := rawPacket(t, p); got[0] != 103 {
		t.Fatal("DATA expected")
	}
	if got := r.seen.Load(); got != MaxData {
		t.Fatalf("upstream allocated unclamped read buffer%d", got)
	}
	b.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("workers not joined")
	}
}

func TestActualCodecHeadOfLineCannotGrowBeyondCredits(t *testing.T) {
	first, release := make(chan struct{}), make(chan struct{})
	r := &lengthReader{first: first, release: release}
	p, b, done := rawCodec(t, r)
	writerDone := make(chan error, 1)
	go func() {
		for id := uint32(2); id < 1002; id++ {
			if err := writeFull(p, readRequest(id, uint64(id-2), MaxData)); err != nil {
				writerDone <- err
				return
			}
		}
		writerDone <- nil
	}()
	<-first
	deadline := time.Now().Add(time.Second)
	for b.Stats().Outstanding < Credits && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	s := b.Stats()
	if s.Outstanding != Credits || s.HighWater > Credits || s.Accepted > Credits+2 {
		t.Fatalf("unbounded ordered queue %#v", s)
	}
	select {
	case err := <-writerDone:
		t.Fatalf("1000requests passed stalled creditwindow: %v", err)
	default:
	}
	// Independent socket abort must unblock the writer even while upstream's
	// sendPacket mutex is owned. Native ReaderAt joins only after release.
	start := time.Now()
	b.Close()
	if time.Since(start) > time.Second {
		t.Fatal("abort blocked")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("codec workers did not join")
	}
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("request writer did not join")
	}
}

func TestCancellationInterruptsActualCodecSlowReceiver(t *testing.T) {
	r := &lengthReader{}
	p, b, done := rawCodec(t, r)
	if err := writeFull(p, readRequest(2, 1, MaxData)); err != nil {
		t.Fatal(err)
	}
	// Do not read DATA. sendPacket holds its own mutex writing to this pipe.
	deadline := time.Now().Add(time.Second)
	for b.Stats().Outstanding != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	b.cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel failed to abort writer independently of codec Close")
	}
}

func TestNoImplicitAuthorityFromNilTransport(t *testing.T) {
	if _, err := NewBridge(context.Background(), nil, func() {}); err == nil {
		t.Fatal("nilstream")
	}
	if !errors.Is(ErrProtocol, ErrProtocol) {
		t.Fatal("sentinel")
	}
}

func TestPinnedClientRejectsHugeExtendedAttributeCount(t *testing.T) {
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()
	deadline := time.Now().Add(2 * time.Second)
	server.SetDeadline(deadline)
	peer.SetDeadline(deadline)
	done := make(chan error, 1)
	go func() {
		var h [4]byte
		if _, err := io.ReadFull(server, h[:]); err != nil {
			done <- err
			return
		}
		n := binary.BigEndian.Uint32(h[:])
		if n > MaxIncoming {
			done <- ErrProtocol
			return
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(server, body); err != nil {
			done <- err
			return
		}
		if err := writeFull(server, frame(2, 3)); err != nil {
			done <- err
			return
		}
		if _, err := io.ReadFull(server, h[:]); err != nil {
			done <- err
			return
		}
		n = binary.BigEndian.Uint32(h[:])
		if n > MaxIncoming || n < 5 {
			done <- ErrProtocol
			return
		}
		body = make([]byte, n)
		if _, err := io.ReadFull(server, body); err != nil {
			done <- err
			return
		}
		id := binary.BigEndian.Uint32(body[1:5])
		attrs := []byte{0x80, 0, 0, 0, 0xff, 0xff, 0xff, 0xff}
		done <- writeFull(server, frame(105, id, attrs))
	}()
	c, err := sftp.NewClientPipe(peer, peer)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.Stat("/file"); err == nil {
		t.Fatal("unbounded impossible attribute count accepted")
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRequestCounterRefusesBeforeInternalUint32Wrap(t *testing.T) {
	b, p := pipeBridge(t)
	initBridge(t, b, p)
	b.mu.Lock()
	b.accepted = MaxRequests
	b.mu.Unlock()
	go func() { _ = writeFull(p, frame(5, 1)) }()
	var h [9]byte
	if _, err := io.ReadFull(b, h[:]); err == nil {
		t.Fatal("request-count bound bypassed")
	}
}
