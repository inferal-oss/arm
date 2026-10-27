package meshforward

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"
)

type localBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c localBufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

type localBufferedTCPConn struct {
	*net.TCPConn
	reader *bufio.Reader
}

func (c localBufferedTCPConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// ServeLocalCompatibleTLS keeps already-reviewed plaintext local modes usable
// while a fenced rollout migrates them. A TLS ClientHello is never retried as
// plaintext, so the updated CLI cannot downgrade after authentication fails.
// Plaintext callers still carry their existing bearer risk on port handoff;
// they must not be presented as source-authenticated clients.
func ServeLocalCompatibleTLS(ctx context.Context, local net.Conn, serverTLS *tls.Config, digest string, open func(context.Context) (net.Conn, error)) error {
	if local == nil {
		return errors.New("owned local TCP forward connection required")
	}
	defer local.Close()
	if ctx == nil || serverTLS == nil || open == nil {
		return errors.New("owned local TCP forward connection required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(local, 4096)
	if err := local.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	first, err := reader.Peek(1)
	if err != nil {
		return err
	}
	if err = local.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	var replay net.Conn = localBufferedConn{Conn: local, reader: reader}
	if tcp, ok := local.(*net.TCPConn); ok {
		replay = localBufferedTCPConn{TCPConn: tcp, reader: reader}
	}
	if first[0] == 0x16 { // TLS handshake record; never fall back after this point.
		return ServeLocalTLS(ctx, replay, serverTLS, digest, open)
	}
	return ServeLocal(ctx, replay, digest, open)
}

// ServeLocalTLS presents the existing local mesh identity before reading a
// bearer or application byte. The client pins that identity; the destination
// still performs its normal grant admission.
func ServeLocalTLS(ctx context.Context, local net.Conn, serverTLS *tls.Config, digest string, open func(context.Context) (net.Conn, error)) error {
	if local == nil {
		return errors.New("owned local TLS forward connection required")
	}
	defer local.Close()
	if ctx == nil || serverTLS == nil || open == nil {
		return errors.New("owned local TLS forward connection required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	secure := tls.Server(local, serverTLS)
	defer secure.Close()
	if err := secure.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if err := secure.HandshakeContext(ctx); err != nil {
		return err
	}
	state := secure.ConnectionState()
	if state.Version != tls.VersionTLS13 || state.DidResume || len(serverTLS.NextProtos) != 1 || state.NegotiatedProtocol != serverTLS.NextProtos[0] {
		return errors.New("local forward TLS protocol differs")
	}
	if err := secure.SetDeadline(time.Time{}); err != nil {
		return err
	}
	return ServeLocal(ctx, secure, digest, open)
}
