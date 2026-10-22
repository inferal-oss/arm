package routeproto

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type readResult struct {
	n   int64
	err error
}

// These mechanics tests intentionally use a loopback plaintext WebSocket to
// isolate maintained NetConn behavior. Separate three-node tests cover both
// outer pinned TLS links and the inner mutually authenticated TLS stream.
func sink(t *testing.T, b streamBounds, parents ...context.Context) (*wire, <-chan readResult) {
	t.Helper()
	results := make(chan readResult, 1)
	parent := context.Background()
	if len(parents) > 0 {
		parent = parents[0]
	}
	ctx, cancel := context.WithCancel(parent)
	t.Cleanup(cancel)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, e := acceptWire(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if e != nil {
			return
		}
		c := bounded(ctx, ws, b)
		defer c.Close()
		n, e := io.CopyBuffer(io.Discard, c, make([]byte, 4096))
		results <- readResult{n, e}
	}))
	t.Cleanup(s.Close)
	ws, _, e := websocket.Dial(ctx, "ws"+s.URL[len("http"):], &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return &wire{Conn: ws}, results
}
func result(t *testing.T, ch <-chan readResult) readResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("bounded stream failed to terminate")
		return readResult{}
	}
}

func TestReadLimitRestoredAfterNetConnForWholeAndFragmentedMessages(t *testing.T) {
	for _, fragmented := range []bool{false, true} {
		t.Run(map[bool]string{false: "whole", true: "fragmented"}[fragmented], func(t *testing.T) {
			ws, ch := sink(t, defaultBounds())
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			body := make([]byte, maxFrame+100)
			if fragmented {
				w, e := ws.Writer(ctx, websocket.MessageBinary)
				if e != nil {
					t.Fatal(e)
				}
				for len(body) > 0 {
					n := min(len(body), 4096)
					if _, e = w.Write(body[:n]); e != nil {
						break
					}
					body = body[n:]
				}
				_ = w.Close()
			} else {
				_ = ws.Write(ctx, websocket.MessageBinary, body)
			}
			r := result(t, ch)
			if r.err == nil || r.n > maxFrame {
				t.Fatal("aggregate WebSocket message exceeded reserved lookahead bound", r.n, r.err)
			}
			// SetReadLimit(65535) delivers65536 including coder's single-byte
			// overflow probe, then closes. Reordering before NetConn permits65736.
			if r.n != maxFrame {
				t.Fatal("test did not reach real message limit/lookahead", r.n, r.err)
			}
		})
	}
}
func TestByteQuotaSpansMultipleValidMessagesAndRejectsText(t *testing.T) {
	t.Run("quota", func(t *testing.T) {
		b := defaultBounds()
		b.bytes = 9000
		ws, ch := sink(t, b)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		for i := 0; i < 4; i++ {
			if e := ws.Write(ctx, websocket.MessageBinary, make([]byte, 4000)); e != nil {
				break
			}
		}
		r := result(t, ch)
		if r.err == nil || r.n != 9000 {
			t.Fatal("cumulative quota lost", r.n, r.err)
		}
	})
	t.Run("text", func(t *testing.T) {
		ws, ch := sink(t, defaultBounds())
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = ws.Write(ctx, websocket.MessageText, []byte("not binary"))
		r := result(t, ch)
		if r.err == nil || r.n != 0 {
			t.Fatal("text accepted", r.n, r.err)
		}
	})
}
func TestIdleLifetimeCancellationAndReadDeadline(t *testing.T) {
	for _, name := range []string{"idle", "lifetime", "cancel", "read-deadline"} {
		t.Run(name, func(t *testing.T) {
			b := defaultBounds()
			if name == "idle" {
				b.idle = 40 * time.Millisecond
			}
			if name == "lifetime" {
				b.lifetime = 40 * time.Millisecond
			}
			ws, ch := sink(t, b)
			if name == "cancel" || name == "read-deadline" {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				client := bounded(ctx, ws, defaultBounds())
				defer client.Close()
				if name == "cancel" {
					cancel()
				} else {
					_ = client.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
				}
				var buf [1]byte
				if n, e := client.Read(buf[:]); e == nil || n != 0 {
					t.Fatal("blocked read survived cancellation/deadline", n, e)
				}
			}
			r := result(t, ch)
			if r.err == nil || r.n != 0 {
				t.Fatal("idle/lifetime stream did not stop", r.n, r.err)
			}
		})
	}
}
func TestOutboundChunksValidMessagesAndEnforcesQuota(t *testing.T) {
	b := defaultBounds()
	b.bytes = maxFrame + 100
	ws, ch := sink(t, defaultBounds())
	client := bounded(context.Background(), ws, b)
	defer client.Close()
	n, e := client.Write(make([]byte, maxFrame+200))
	if e == nil || n != maxFrame+100 {
		t.Fatal("outgoing quota/chunk count", n, e)
	}
	r := result(t, ch)
	if r.n != int64(n) {
		t.Fatal("chunked valid messages were rejected", r.n, r.err)
	}
}

func TestTextFrameGracefulCloseCannotOutliveForcedCancellation(t *testing.T) {
	for _, name := range []string{"short-lifetime", "cancel-during-close"} {
		t.Run(name, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			b := defaultBounds()
			if name == "short-lifetime" {
				b.lifetime = 80 * time.Millisecond
			}
			ws, ch := sink(t, b, parent)
			ctx, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			_ = ws.Write(ctx, websocket.MessageText, []byte("malicious text; peer does not read or acknowledge close"))
			if name == "cancel-during-close" {
				// Let the server enter NetConn's synchronous unsupported-type
				// close path; the peer deliberately performs no reads/close ACK.
				time.Sleep(30 * time.Millisecond)
				cancel()
			}
			select {
			case r := <-ch:
				if r.n != 0 || r.err == nil {
					t.Fatal(r)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("graceful close escaped hard socket cancellation")
			}
		})
	}
}

func TestNestedTLSHandshakeDeadlineAgainstNonrespondingEndpoint(t *testing.T) {
	ws, ch := sink(t, defaultBounds())
	stream := bounded(context.Background(), ws, defaultBounds())
	defer stream.Close()
	now := time.Now()
	client := newIdentity(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", now.Add(-time.Minute), now.Add(time.Hour))
	target := newIdentity(t, "cccccccccccccccccccccccccccccccc", now.Add(-time.Minute), now.Add(time.Hour))
	conn := tls.Client(stream, policy(client, target, false, innerProtocol))
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if e := conn.HandshakeContext(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("stalled TLS handshake escaped deadline", e)
	}
	_ = stream.Close()
	_ = result(t, ch)
}
