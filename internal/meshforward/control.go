package meshforward

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"

	"github.com/inferal-oss/arm/internal/workspace"
)

const maxControl = 2048

func readControl(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > maxControl {
		return nil, errors.New("TCP forward control exceeds bound")
	}
	raw := make([]byte, n)
	_, err := io.ReadFull(r, raw)
	return raw, err
}

func writeControl(w io.Writer, value any) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) == 0 || len(raw) > maxControl {
		return errors.New("TCP forward control exceeds bound")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(raw)))
	for _, part := range [][]byte{header[:], raw} {
		for len(part) > 0 {
			n, e := w.Write(part)
			if e != nil {
				return e
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}

func ReadRequest(r io.Reader) (Request, error) {
	raw, err := readControl(r)
	if err != nil {
		return Request{}, err
	}
	var q Request
	if err = workspace.Decode(raw, &q); err != nil {
		return Request{}, err
	}
	if err = q.Validate(); err != nil {
		return Request{}, err
	}
	return q, nil
}

func (q Request) Validate() error {
	if q.Version != 1 || !hexID(q.Workspace, 16) || !hexID(q.Generation, 16) || !hexID(q.Source, 16) || !hexID(q.Target, 16) || !hexID(q.SourcePin, 32) || !hexID(q.TargetPin, 32) ||
		!hexID(q.ConnectionID, 16) || !hexID(q.RouteBindingHash, 32) || q.GrantRevision < 1 || q.TTL <= 0 || q.TTL > MaxLifetime ||
		(Endpoint{Name: q.Service, Network: "tcp", Address: "127.0.0.1:1"}).Validate() != nil {
		return errors.New("invalid exact TCP forward request")
	}
	return nil
}

func WriteRequest(w io.Writer, q Request) error {
	if err := q.Validate(); err != nil {
		return err
	}
	// Round-trip through the same decoder so callers cannot write extra or
	// malformed authority fields that destinations would interpret differently.
	raw, err := json.Marshal(q)
	if err != nil {
		return err
	}
	var decoded Request
	if err = workspace.Decode(raw, &decoded); err != nil {
		return err
	}
	if decoded != q {
		return errors.New("noncanonical TCP forward request")
	}
	return writeControl(w, q)
}

type accepted struct {
	Version      int    `json:"schema_version"`
	ConnectionID string `json:"connection_id"`
	Accepted     bool   `json:"accepted"`
}

func WriteAccepted(w io.Writer, q Request) error {
	return writeControl(w, accepted{1, q.ConnectionID, true})
}
func ReadAccepted(r io.Reader, q Request) error {
	raw, err := readControl(r)
	if err != nil {
		return err
	}
	var a accepted
	if err = workspace.Decode(raw, &a); err != nil {
		return err
	}
	if a != (accepted{1, q.ConnectionID, true}) {
		return errors.New("TCP forward destination acknowledgement differs")
	}
	return nil
}
