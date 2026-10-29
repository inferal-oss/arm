// Package resident is a private, unconnected prototype of a resident dispatch
// boundary. It owns no Manager, native job or public startup path. In particular,
// closing a worker channel cannot close a Manager or cancel a resident job.
package resident

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
)

const (
	Protocol       = 1
	MaxRequest     = 64 << 10
	MaxResponse    = 256 << 10
	MaxReceipts    = 128
	MaxGenerations = 2 // Including sealed owners; no native exit is inferred.
)

var hexID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var digestID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var versionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

type Identity struct {
	Host       string `json:"host"`
	Owner      string `json:"owner"`
	Generation string `json:"generation"`
}

type WorkerIdentity struct {
	Generation string `json:"generation"`
	SHA256     string `json:"sha256"`
	Version    string `json:"version"`
}

// The prototype admits only an explicitly provisioned local fixture principal.
// It does not translate browser/mesh claims into native authority.
type Principal struct {
	Kind  string `json:"kind"`
	Owner string `json:"owner"`
}

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Failure) Error() string   { return e.Code + ": " + e.Message }
func failure(code string) *Failure { return &Failure{Code: code, Message: code} }

type Request struct {
	Protocol   int             `json:"protocol"`
	Resident   Identity        `json:"resident"`
	Worker     WorkerIdentity  `json:"worker"`
	Capability string          `json:"capability"`
	ID         uint64          `json:"id"`
	Operation  string          `json:"operation"`
	Key        string          `json:"key,omitempty"`
	Params     json.RawMessage `json:"params"`
}

type Reply struct {
	Protocol int             `json:"protocol"`
	Resident Identity        `json:"resident"`
	Worker   WorkerIdentity  `json:"worker"`
	ID       uint64          `json:"id"`
	Key      string          `json:"key,omitempty"`
	Outcome  string          `json:"outcome"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    *Failure        `json:"error,omitempty"`
}

type bootstrap struct {
	Protocol    int            `json:"protocol"`
	Resident    Identity       `json:"resident"`
	Worker      WorkerIdentity `json:"worker"`
	Capability  string         `json:"capability"`
	ClientToken string         `json:"client_token"`
}

type hello struct {
	Protocol   int            `json:"protocol"`
	Resident   Identity       `json:"resident"`
	Worker     WorkerIdentity `json:"worker"`
	Capability string         `json:"capability"`
}

func secret() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func decode(raw []byte, value any) error {
	shape := json.NewDecoder(bytes.NewReader(raw))
	if err := jsonShape(shape, 0); err != nil {
		return err
	}
	if _, err := shape.Token(); err != io.EOF {
		return fmt.Errorf("trailing frame data")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing frame data")
	}
	return nil
}

// Reject duplicate/ambiguous keys and bound nesting before typed decoding.
func jsonShape(d *json.Decoder, depth int) error {
	if depth > 32 {
		return failure("json_depth")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return failure("json_duplicate_key")
			}
			seen[name] = true
			if err = jsonShape(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return failure("json_invalid")
		}
	case '[':
		for d.More() {
			if err := jsonShape(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return failure("json_invalid")
		}
	default:
		return failure("json_invalid")
	}
	return nil
}

func readFrame(r io.Reader, maximum int, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > uint32(maximum) {
		return failure("frame_limit")
	}
	raw := make([]byte, int(n))
	if _, err := io.ReadFull(r, raw); err != nil {
		return err
	}
	return decode(raw, value)
}

func writeFrame(w io.Writer, maximum int, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(raw) == 0 || len(raw) > maximum {
		return failure("frame_limit")
	}
	frame := make([]byte, 4+len(raw))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(raw)))
	copy(frame[4:], raw)
	n, err := w.Write(frame)
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	return err
}

// ExecutableSHA256 is integrity evidence, not release authentication. The
// launcher must additionally retain/recheck its original immutable file.
func ExecutableSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0022 != 0 || before.Size() < 1 || before.Size() > 128<<20 {
		return "", failure("executable_invalid")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, (128<<20)+1))
	if err != nil || n != before.Size() {
		return "", failure("executable_changed")
	}
	after, err := f.Stat()
	named, namedErr := os.Stat(path)
	if err != nil || namedErr != nil || !os.SameFile(before, after) || !os.SameFile(before, named) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || before.Size() != named.Size() || before.ModTime() != named.ModTime() {
		return "", failure("executable_changed")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
