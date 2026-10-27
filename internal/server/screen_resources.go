package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/inferal-oss/arm/internal/model"
)

// A cooperative endpoint transport reservation, in addition to screen's wire
// limits. This covers neither browser decoding nor the native screen server,
// and is not a measurement or an OS CPU/RSS limit. Destination authorization
// still comes only from the original browser context or screen-view grant.
const screenTransportCPU = .05
const screenTransportMemory = 8 << 20

// reserveScreenTransport must precede any screen native dial. Its finish
// callback belongs to this original handler and must run after socket closes,
// copy workers, authority watchers and cancellation callbacks have joined.
// Expiry/release only cancels the returned context; it does not reclaim the
// allocation before that positive join witness.
func (s *Server) reserveScreenTransport(ctx context.Context) (context.Context, func(), error) {
	if s == nil || s.Manager == nil {
		return nil, nil, model.Err("resource_unavailable", "screen transport resource manager is unavailable")
	}
	if ctx == nil {
		return nil, nil, model.Err("invalid_request", "screen transport context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	ttl := screenLifetime
	if deadline, ok := ctx.Deadline(); ok {
		ttl = min(ttl, time.Until(deadline))
	}
	if ttl <= 0 {
		return nil, nil, context.DeadlineExceeded
	}
	key, err := uuid.NewRandom()
	if err != nil {
		return nil, nil, err
	}
	joined := make(chan struct{})
	hold, err := s.Manager.AcquireRelayTransit(ctx, model.Request{
		Key: "screen-transport:" + key.String(), Reason: "Standard Screen Sharing endpoint transport",
		Project: "screen-sharing", Resources: model.Resources{CPU: screenTransportCPU, Memory: screenTransportMemory}, TTL: ttl,
	}, joined)
	if err != nil {
		return nil, nil, err
	}
	frame := &screenTransportFrame{}
	var once sync.Once
	finish := func() {
		once.Do(func() {
			// Cleanup owns these exact original sockets. Close-once joins any
			// already-running native close, retaining its first result even if
			// a protocol adapter discarded that error or calls Close again.
			if !frame.closeOriginals() {
				if err := hold.MarkCleanupUncertain("screen transport native close result is unconfirmed"); err != nil {
					log.Printf("ARM screen: cannot confirm retained cleanup state: %v", err)
				}
			}
			close(joined)
			if _, err := hold.FinishAfterJoin(); err != nil {
				log.Printf("ARM screen: transport allocation remains unconfirmed after original join: %v", err)
			}
		})
	}
	lease, err := hold.Lease()
	if err == nil {
		err = hold.Context().Err()
	}
	if err != nil || lease.State != "active" {
		finish() // No native work has started on this refused path.
		if err == nil {
			err = model.Err("capacity_exceeded", "screen transport resources are not active")
		}
		return nil, nil, err
	}
	return context.WithValue(hold.Context(), screenTransportFrameKey{}, frame), finish, nil
}

// Each handler can own its target and one hijacked viewer. Upgrades that
// hand over no connection register no viewer socket.
type screenTransportFrameKey struct{}
type screenTransportFrame struct {
	mu      sync.Mutex
	sockets []*screenOriginalSocket
}

type screenOriginalSocket struct {
	net.Conn
	once        sync.Once
	closeNative func() error
	closeErr    error
}

func (s *screenOriginalSocket) Close() error {
	s.once.Do(func() {
		// A panic or other missing return is never a successful close result.
		s.closeErr = model.Err("screen_close_unconfirmed", "original native close did not return a confirmed result")
		s.closeErr = s.closeNative()
	})
	return s.closeErr
}

func screenOwnTransportConn(ctx context.Context, conn net.Conn) net.Conn {
	frame, _ := ctx.Value(screenTransportFrameKey{}).(*screenTransportFrame)
	if frame == nil {
		return conn
	}
	closeNative := conn.Close
	if secured, ok := conn.(*tls.Conn); ok {
		// CloseNow aborts the original TCP socket. Calling TLS.Close afterward
		// would issue a second native Close and could obscure its first result.
		// The caller joins TLS reads/writes; its remaining buffers are Go-owned.
		closeNative = secured.NetConn().Close
	}
	original := &screenOriginalSocket{Conn: conn, closeNative: closeNative}
	frame.mu.Lock()
	frame.sockets = append(frame.sockets, original)
	frame.mu.Unlock()
	return original
}

func (f *screenTransportFrame) closeOriginals() bool {
	f.mu.Lock()
	sockets := append([]*screenOriginalSocket(nil), f.sockets...)
	f.mu.Unlock()
	confirmed := true
	for _, s := range sockets {
		// A first already-closed error is not a captured successful original close.
		// The exact first result stays sticky; no second Close is a reclamation proof.
		if err := s.Close(); err != nil {
			confirmed = false
		}
	}
	return confirmed
}

type screenTransportWriter struct {
	http.ResponseWriter
	ctx context.Context
}

func (w screenTransportWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w screenTransportWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if conn != nil {
		conn = screenOwnTransportConn(w.ctx, conn)
	}
	return conn, rw, err
}
func screenOwnedUpgradeWriter(ctx context.Context, w http.ResponseWriter) http.ResponseWriter {
	if frame, _ := ctx.Value(screenTransportFrameKey{}).(*screenTransportFrame); frame != nil {
		return screenTransportWriter{ResponseWriter: w, ctx: ctx}
	}
	return w
}
