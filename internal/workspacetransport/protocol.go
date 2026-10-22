// Package workspacetransport implements the closed messaging-workspace wire
// protocol. It does not admit direct mesh peers or confer resource, execution,
// SSH, filesystem, browser or native operator authority.
//
// This protocol foundation has no registered listener or public dispatcher.
package workspacetransport

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/workspace"
)

const (
	Path            = "/v1/mesh/workspace"
	Protocol        = "arm.mesh.workspace.v1"
	InnerProtocol   = "arm.workspace.directory.v1"
	Version         = 1
	MaxControlBytes = 16 << 10
	MaxRequestBytes = 60 << 10
	MaxFrameBytes   = 64 << 10
	MaxPayloadBytes = MaxFrameBytes - 1 // Reserve coder/websocket's lookahead byte.
	MaxStreamBytes  = 1 << 20
	MaxLifetime     = 30 * time.Second
	MaxIdle         = 10 * time.Second
	ClassJoinProof  = "join_proof"
	ClassDirectory  = "directory"
)

// Open selects a closed operation over an already authenticated direct pair.
// Its fields are assertions to compare with destination-owned authority, never
// authentication. This checkpoint permits one hop only; no dial address is
// accepted and no graph path can be forwarded by this type's validator.
type Open struct {
	Version    int       `json:"schema_version"`
	Workspace  string    `json:"workspace"`
	Generation string    `json:"generation"`
	Route      string    `json:"route_id"`
	Source     string    `json:"source_host"`
	Target     string    `json:"target_host"`
	Path       []string  `json:"path"`
	Class      string    `json:"class"`
	Expires    time.Time `json:"expires_at"`
	Pair       string    `json:"pair"`
	Revision   int       `json:"enrollment_revision"`
	Consent    string    `json:"consent_sha256,omitempty"`
}

func hexValue(v string, bytes int) bool {
	decoded, err := hex.DecodeString(v)
	return err == nil && len(decoded) == bytes && strings.ToLower(v) == v
}

// Validate bounds the envelope and time assertions only. A transport must also
// authenticate the current exact pair/identity and the class-specific recorded
// workspace intent, then cap lifetime from its own monotonic request start.
func (o Open) Validate(now time.Time) error {
	if o.Version != Version || !hexValue(o.Workspace, 16) || !hexValue(o.Generation, 16) || !hexValue(o.Route, 16) || !hexValue(o.Source, 16) || !hexValue(o.Target, 16) || o.Source == o.Target || !hexValue(o.Pair, 32) || o.Revision < 1 || now.IsZero() || !o.Expires.After(now) || o.Expires.After(now.Add(MaxLifetime+workspace.MaxClockSkew)) {
		return errors.New("invalid bounded workspace stream identity or deadline")
	}
	if len(o.Path) != 2 || o.Path[0] != o.Source || o.Path[1] != o.Target {
		return errors.New("workspace transport currently permits an exact direct hop only")
	}
	switch o.Class {
	case ClassJoinProof:
		if !hexValue(o.Consent, 32) {
			return errors.New("candidate proof requires exact approved consent")
		}
	case ClassDirectory:
		if o.Consent != "" {
			return errors.New("directory stream cannot carry candidate consent")
		}
	default:
		return errors.New("unsupported workspace stream class")
	}
	return nil
}

func DecodeOpen(raw []byte, now time.Time) (Open, error) {
	var o Open
	if len(raw) == 0 || len(raw) > MaxControlBytes {
		return o, errors.New("workspace stream control exceeds byte bound")
	}
	if err := workspace.Decode(raw, &o); err != nil {
		return Open{}, err
	}
	// An optional field must actually be omitted for the other class; an empty
	// string is not a second encoding of an absent authority field.
	if o.Class == ClassDirectory {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return Open{}, err
		}
		if _, exists := fields["consent_sha256"]; exists {
			return Open{}, errors.New("directory stream consent field must be omitted")
		}
	}
	if err := o.Validate(now); err != nil {
		return Open{}, err
	}
	return o, nil
}

// ReadOpen reads exactly one bounded length-prefixed control record. The caller
// owns cancellation/deadlines on r; this function does not start a goroutine or
// allocate according to an unchecked peer-advertised length.
func ReadOpen(r io.Reader, now time.Time) (Open, error) {
	raw, err := readRecord(r, MaxControlBytes)
	if err != nil {
		return Open{}, err
	}
	return DecodeOpen(raw, now)
}

func WriteOpen(w io.Writer, o Open, now time.Time) error {
	if err := o.Validate(now); err != nil {
		return err
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return writeRecord(w, raw, MaxControlBytes)
}

func readRecord(r io.Reader, limit uint32) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > limit {
		return nil, errors.New("invalid workspace record length")
	}
	raw := make([]byte, int(size))
	_, err := io.ReadFull(r, raw)
	return raw, err
}

func writeRecord(w io.Writer, raw []byte, limit uint32) error {
	if len(raw) == 0 || uint64(len(raw)) > uint64(limit) {
		return errors.New("workspace record exceeds byte bound")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(raw)))
	// io.Copy handles short writes and reports a writer that fails to make
	// progress; the caller must not interpret a partial write as an outcome.
	if _, err := io.Copy(w, strings.NewReader(string(header[:])+string(raw))); err != nil {
		return err
	}
	return nil
}
