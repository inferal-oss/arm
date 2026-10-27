package meshsshrelay

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/workspace"
)

const maxBindingBytes = 512

type bindingRecord struct {
	Version int    `json:"schema_version"`
	Route   string `json:"route_id"`
	Digest  string `json:"binding_sha256"`
}

func bindingFor(now time.Time, o Open) (bindingRecord, error) {
	d, err := o.BindingDigest(now)
	if err != nil {
		return bindingRecord{}, err
	}
	return bindingRecord{Version: Version, Route: o.Route, Digest: d}, nil
}

func writeBinding(w io.Writer, b bindingRecord) error {
	raw, err := json.Marshal(b)
	if err != nil || len(raw) > maxBindingBytes {
		return errors.New("SSH relay binding exceeds bound")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(raw)))
	for _, part := range [][]byte{header[:], raw} {
		for len(part) > 0 {
			n, err := w.Write(part)
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}

func readBinding(r io.Reader) (bindingRecord, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return bindingRecord{}, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > maxBindingBytes {
		return bindingRecord{}, errors.New("invalid SSH relay binding length")
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(r, raw); err != nil {
		return bindingRecord{}, err
	}
	var b bindingRecord
	if err := workspace.Decode(raw, &b); err != nil {
		return bindingRecord{}, err
	}
	if b.Version != Version || !hexBytes(b.Route, 16) || !hexBytes(b.Digest, 32) {
		return bindingRecord{}, errors.New("invalid SSH relay binding")
	}
	return b, nil
}

// AcceptBinding is called only after the original source's pinned inner TLS
// handshake. It refuses relay changes to immutable route fields before any
// destination identity, lookup, seal or execution request is handled.
func AcceptBinding(c *tls.Conn, o Open) error {
	if c == nil {
		return errors.New("missing authenticated SSH relay endpoint")
	}
	if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	defer c.SetDeadline(time.Time{})
	want, err := bindingFor(time.Now(), o)
	if err != nil {
		return err
	}
	got, err := readBinding(c)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New("SSH relay route differs from original endpoint binding")
	}
	return writeBinding(c, want)
}

// Dial establishes the opaque outer carrier, exact pinned inner TLS and route
// binding. It returns only the original endpoint's authenticated byte stream.
func Dial(ctx context.Context, address string, outer, inner *tls.Config, o Open, revalidate func(context.Context) error) (*tls.Conn, error) {
	protocol := InnerProtocol
	if o.Class == ClassForward {
		protocol = ForwardInnerProtocol
	}
	if inner == nil || inner.InsecureSkipVerify || inner.MinVersion != tls.VersionTLS13 || inner.MaxVersion != tls.VersionTLS13 ||
		inner.RootCAs == nil || inner.VerifyConnection == nil || inner.ServerName != mesh.SAN(o.Target) ||
		len(inner.NextProtos) != 1 || inner.NextProtos[0] != protocol || revalidate == nil {
		return nil, errors.New("exact pinned SSH relay endpoint TLS required")
	}
	raw, err := DialCarrier(ctx, address, outer, o, revalidate)
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, inner)
	closeOnError := func(e error) (*tls.Conn, error) { _ = raw.Close(); return nil, e }
	handshake, stop := context.WithTimeout(ctx, 5*time.Second)
	err = conn.HandshakeContext(handshake)
	stop()
	if err != nil {
		return closeOnError(err)
	}
	if err = revalidate(ctx); err != nil {
		return closeOnError(err)
	}
	if err = conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return closeOnError(err)
	}
	defer conn.SetDeadline(time.Time{})
	want, err := bindingFor(time.Now(), o)
	if err != nil {
		return closeOnError(err)
	}
	if err = writeBinding(conn, want); err != nil {
		return closeOnError(err)
	}
	got, err := readBinding(conn)
	if err != nil {
		return closeOnError(err)
	}
	if got != want {
		return closeOnError(errors.New("SSH relay destination binding differs"))
	}
	return conn, nil
}
