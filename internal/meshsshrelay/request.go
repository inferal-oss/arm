package meshsshrelay

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/inferal-oss/arm/internal/meshstream"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
	"github.com/inferal-oss/arm/internal/workspace"
)

const MaxRequestBytes = 16 << 10
const MaxResponseBytes = 64 << 10

const (
	OperationIdentity = "identity"
	OperationExecute  = "execute"
	OperationLookup   = "lookup"
	OperationSeal     = "seal"
)

type Request struct {
	Version    int                         `json:"schema_version"`
	Operation  string                      `json:"operation"`
	Key        string                      `json:"key,omitempty"`
	Control    *meshstream.Control         `json:"control,omitempty"`
	Commitment *model.SSHControlCommitment `json:"commitment,omitempty"`
	Reason     string                      `json:"reason,omitempty"`
}

func requestText(s string, max int) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}

func (q Request) Validate() error {
	if q.Version != Version {
		return errors.New("invalid routed SSH request version")
	}
	switch q.Operation {
	case OperationIdentity:
		if q.Key != "" || q.Control != nil || q.Commitment != nil || q.Reason != "" {
			return errors.New("identity has extra authority fields")
		}
	case OperationExecute:
		if q.Key != "" || q.Control == nil || q.Control.Validate() != nil || q.Commitment != nil || q.Reason != "" {
			return errors.New("invalid exact routed SSH execution control")
		}
	case OperationLookup:
		if !requestText(q.Key, 128) || q.Control != nil || q.Commitment != nil || q.Reason != "" {
			return errors.New("invalid read-only routed SSH lookup")
		}
	case OperationSeal:
		if !requestText(q.Key, 128) || q.Control != nil || q.Commitment == nil || q.Commitment.Validate() != nil || q.Commitment.Key != q.Key || !requestText(q.Reason, 4096) {
			return errors.New("invalid exact routed SSH nonexecution seal")
		}
	default:
		return errors.New("unsupported routed SSH operation")
	}
	return nil
}

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Response struct {
	Version       int                           `json:"schema_version"`
	Operation     string                        `json:"operation"`
	RequestSHA256 string                        `json:"request_sha256"`
	Identity      *model.SSHDestinationIdentity `json:"identity,omitempty"`
	Status        *model.SSHKeyStatus           `json:"status,omitempty"`
	Accepted      bool                          `json:"accepted,omitempty"`
	Failure       *Failure                      `json:"failure,omitempty"`
}

func (q Request) Digest() string {
	raw, _ := json.Marshal(q)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (q Request) Reply() Response {
	return Response{Version: Version, Operation: q.Operation, RequestSHA256: q.Digest()}
}

func (r Response) Validate(q Request) error {
	if r.Version != Version || r.Operation != q.Operation || r.RequestSHA256 != q.Digest() {
		return errors.New("routed SSH response differs from exact request")
	}
	count := 0
	if r.Identity != nil {
		count++
	}
	if r.Status != nil {
		count++
	}
	if r.Accepted {
		count++
	}
	if r.Failure != nil {
		count++
	}
	if count != 1 {
		return errors.New("routed SSH response must have one exact outcome")
	}
	if r.Failure != nil {
		if !requestText(r.Failure.Code, 128) || !requestText(r.Failure.Message, 4096) {
			return errors.New("invalid routed SSH failure")
		}
		return nil
	}
	if q.Operation == OperationIdentity && r.Identity == nil ||
		(q.Operation == OperationLookup || q.Operation == OperationSeal) && r.Status == nil ||
		q.Operation == OperationExecute && !r.Accepted {
		return errors.New("routed SSH response outcome type differs")
	}
	return nil
}

func readRecord(r io.Reader, limit uint32) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > limit {
		return nil, errors.New("invalid bounded routed SSH record length")
	}
	raw := make([]byte, n)
	_, err := io.ReadFull(r, raw)
	return raw, err
}
func writeRecord(w io.Writer, raw []byte, limit uint32) error {
	if len(raw) == 0 || uint64(len(raw)) > uint64(limit) {
		return errors.New("routed SSH record exceeds bound")
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(raw)))
	for _, part := range [][]byte{h[:], raw} {
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

func ReadRequest(r io.Reader) (Request, error) {
	raw, err := readRecord(r, MaxRequestBytes)
	if err != nil {
		return Request{}, err
	}
	var q Request
	if err = workspace.Decode(raw, &q); err != nil {
		return Request{}, err
	}
	return q, q.Validate()
}
func WriteRequest(w io.Writer, q Request) error {
	if err := q.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(q)
	if err != nil {
		return err
	}
	return writeRecord(w, raw, MaxRequestBytes)
}
func ReadResponse(r io.Reader, q Request) (Response, error) {
	raw, err := readRecord(r, MaxResponseBytes)
	if err != nil {
		return Response{}, err
	}
	// SSHKeyStatus has canonical optional null evidence from the existing
	// destination producer. Decode that one field with its own strict status
	// decoder while keeping the routed envelope closed and key-bound.
	var wire struct {
		Version       int                           `json:"schema_version"`
		Operation     string                        `json:"operation"`
		RequestSHA256 string                        `json:"request_sha256"`
		Identity      *model.SSHDestinationIdentity `json:"identity,omitempty"`
		Status        json.RawMessage               `json:"status,omitempty"`
		Accepted      bool                          `json:"accepted,omitempty"`
		Failure       *Failure                      `json:"failure,omitempty"`
	}
	if err = workspace.Decode(raw, &wire); err != nil {
		return Response{}, err
	}
	out := Response{Version: wire.Version, Operation: wire.Operation, RequestSHA256: wire.RequestSHA256,
		Identity: wire.Identity, Accepted: wire.Accepted, Failure: wire.Failure}
	if len(wire.Status) != 0 {
		var status model.SSHKeyStatus
		if err = sshservice.DecodeKeyStatus(wire.Status, &status); err != nil {
			return Response{}, err
		}
		out.Status = &status
	}
	return out, out.Validate(q)
}
func WriteResponse(w io.Writer, q Request, out Response) error {
	if err := out.Validate(q); err != nil {
		return err
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return writeRecord(w, raw, MaxResponseBytes)
}

// RequestDeadline bounds the closed control exchange; SSH stream bytes after
// accepted execution use the separately bounded carrier lifetime and idle cap.
const RequestDeadline = 5 * time.Second
