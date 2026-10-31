package meshforward

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
)

// AdmittedRoute owns an original inner TLS connection only after the exact
// destination ACK has been read on a pinned forwarding channel. Its fields are
// private so an arbitrary opener cannot return a raw connection as admission.
type AdmittedRoute struct {
	conn    *tls.Conn
	request Request
}

func (a *AdmittedRoute) Conn() *tls.Conn {
	if a == nil {
		return nil
	}
	return a.conn
}

func (a *AdmittedRoute) Request() Request {
	if a == nil {
		return Request{}
	}
	return a.request
}

// ReadAdmittedRoute is called only after WriteRequest on the same original
// inner TLS connection. The caller owns conn on every failure.
func ReadAdmittedRoute(conn *tls.Conn, q Request) (*AdmittedRoute, error) {
	if conn == nil || q.Validate() != nil {
		return nil, errors.New("exact forwarding TLS request required")
	}
	state := conn.ConnectionState()
	if !state.HandshakeComplete || state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != "arm.workspace.forward.v1" || len(state.PeerCertificates) == 0 {
		return nil, errors.New("pinned forwarding TLS state unavailable")
	}
	digest := sha256.Sum256(state.PeerCertificates[0].Raw)
	if hex.EncodeToString(digest[:]) != q.TargetPin {
		return nil, errors.New("forward destination certificate changed")
	}
	if err := ReadAccepted(conn, q); err != nil {
		return nil, err
	}
	return &AdmittedRoute{conn: conn, request: q}, nil
}
