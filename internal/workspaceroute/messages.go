package workspaceroute

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"unicode"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/routing"
	"github.com/yrashk/arm/internal/workspace"
	"github.com/yrashk/arm/internal/workspacegraph"
)

const (
	MaxRequestBytes  = 60 << 10
	OperationMessage = "message_accept"
	OperationLookup  = "message_lookup"
	OperationRefresh = "directory_refresh"
)

// Request is the ENTIRE routed endpoint namespace. It has no generic params,
// method, URL, execution, direct-mesh, SSH or native administrative envelope.
// Outer relays cannot read it: only the pinned ORIGINAL and final TLS endpoints
// exchange these records. Exactly one request is accepted per routed stream.
type Request struct {
	Version    int                       `json:"schema_version"`
	Route      string                    `json:"route_id"`
	Workspace  string                    `json:"workspace"`
	Generation string                    `json:"generation"`
	Source     string                    `json:"source_host"`
	Target     string                    `json:"target_host"`
	Operation  string                    `json:"operation"`
	Envelope   *routing.Envelope         `json:"envelope,omitempty"`
	Refresh    *workspace.RefreshRequest `json:"refresh,omitempty"`
	Report     *workspacegraph.Report    `json:"graph_report,omitempty"`
}
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// LookupResult deliberately represents an absent receipt. Absence is a read
// observation, never proof that a prior uncertain message did not execute.
type LookupResult struct {
	Found   bool             `json:"found"`
	Receipt *routing.Receipt `json:"receipt,omitempty"`
}
type Reply struct {
	Version    int                  `json:"schema_version"`
	Route      string               `json:"route_id"`
	Workspace  string               `json:"workspace"`
	Generation string               `json:"generation"`
	Source     string               `json:"source_host"`
	Target     string               `json:"target_host"`
	Operation  string               `json:"operation"`
	Receipt    *routing.Receipt     `json:"receipt,omitempty"`
	Lookup     *LookupResult        `json:"lookup,omitempty"`
	Page       *workspace.Page      `json:"page,omitempty"`
	GraphPage  *workspacegraph.Page `json:"graph_page,omitempty"`
	Failure    *Failure             `json:"error,omitempty"`
}

func (q Request) Validate(o Open) error {
	if q.Version != Version || q.Route != o.Route || q.Workspace != o.Workspace || q.Generation != o.Generation || q.Source != o.Source || q.Target != o.Target {
		return errors.New("routed request differs from exact original/final endpoints")
	}
	switch q.Operation {
	case OperationMessage, OperationLookup:
		want := ClassMessage
		if q.Operation == OperationLookup {
			want = ClassLookup
		}
		e := q.Envelope
		if o.Class != want || e == nil || q.Refresh != nil || q.Report != nil || e.From.Kind != "host" || e.To.Kind != "host" || !e.Validate() || e.Workspace != q.Workspace || e.Generation != q.Generation || e.Origin != q.Source || e.Target != q.Target || e.OriginMembership != o.SourceGeneration || e.TargetMembership != o.TargetGeneration {
			return errors.New("message payload/class differs from admitted routed scope")
		}
	case OperationRefresh:
		p := q.Refresh
		if o.Class != ClassDirectory || p == nil || q.Envelope != nil || p.Source.Workspace != q.Workspace || p.Source.Generation != q.Generation || p.Source.Host != q.Source || p.Source.MembershipGeneration != o.SourceGeneration || !validHex(p.Source.Fingerprint, 32) || !validHex(p.Nonce, 32) {
			return errors.New("directory refresh differs from admitted original member")
		}
		if r := q.Report; r != nil {
			if r.ValidateStructure() != nil || r.Workspace != q.Workspace || r.Generation != q.Generation || r.Source != q.Source || r.SourceGeneration != p.Source.MembershipGeneration || r.SourcePin != p.Source.Fingerprint || r.Nonce != p.Nonce {
				return errors.New("graph report differs from exact refresh source/nonce")
			}
		}

	default:
		return errors.New("operation is outside the closed routed endpoint protocol")
	}
	return nil
}
func (q Request) Response() Reply {
	return Reply{Version: Version, Route: q.Route, Workspace: q.Workspace, Generation: q.Generation, Source: q.Target, Target: q.Source, Operation: q.Operation}
}
func (r Reply) Validate(q Request) error {
	if r.Version != Version || r.Route != q.Route || r.Workspace != q.Workspace || r.Generation != q.Generation || r.Source != q.Target || r.Target != q.Source || r.Operation != q.Operation {
		return errors.New("routed reply differs from pinned exact request")
	}
	count := 0
	for _, p := range []bool{r.Receipt != nil, r.Lookup != nil, r.Page != nil, r.GraphPage != nil, r.Failure != nil} {
		if p {
			count++
		}
	}
	if count != 1 {
		return errors.New("one routed reply payload required")
	}
	if r.Failure != nil {
		if !safeText(r.Failure.Code, 128) || !safeText(r.Failure.Message, 1024) {
			return errors.New("invalid bounded routed error")
		}
		return nil
	}
	switch q.Operation {
	case OperationMessage:
		if q.Envelope == nil || r.Receipt == nil || !r.Receipt.Matches(*q.Envelope) {
			return errors.New("message receipt differs from immutable envelope")
		}
	case OperationLookup:
		if q.Envelope == nil || r.Lookup == nil || r.Lookup.Found != (r.Lookup.Receipt != nil) || r.Lookup.Receipt != nil && !r.Lookup.Receipt.Matches(*q.Envelope) {
			return errors.New("lookup result differs from exact queried envelope")
		}
	case OperationRefresh:
		if q.Refresh == nil || r.Page == nil && r.GraphPage == nil {
			return errors.New("directory refresh page missing")
		}
		if g := r.GraphPage; g != nil {
			s := g.Snapshot
			if q.Report == nil || g.Validate() != nil || s.Workspace != q.Workspace || s.Generation != q.Generation || s.Directory != q.Target || s.Requester != q.Source || s.RequesterGeneration != q.Refresh.Source.MembershipGeneration || s.Nonce != q.Refresh.Nonce || !validHex(s.Hash, 32) {
				return errors.New("graph page differs from exact bounded routed refresh")
			}
			return nil
		}
		p := r.Page
		s := p.Snapshot
		raw, e := json.Marshal(p)
		if e != nil || len(raw) > workspace.MaxPageBytes || p.Total < 1 || p.Total > workspace.MaxMembers || p.Offset < 0 || len(s.Members) < 1 || p.Offset > p.Total || len(s.Members) > p.Total-p.Offset || s.Schema != workspace.Schema || s.Workspace != q.Workspace || s.Generation != q.Generation || s.Directory.Host != q.Target || s.Requester != q.Source || s.RequesterGeneration != q.Refresh.Source.MembershipGeneration || s.Nonce != q.Refresh.Nonce || !validHex(s.Hash, 32) {
			return errors.New("directory page differs from exact bounded routed refresh")
		}
	default:
		return errors.New("unsupported routed reply")
	}
	return nil
}
func safeText(v string, max int) bool {
	if len(v) == 0 || len(v) > max || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}
func readRecord(r io.Reader) ([]byte, error) {
	var size [4]byte
	if _, e := io.ReadFull(r, size[:]); e != nil {
		return nil, e
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > MaxRequestBytes {
		return nil, errors.New("routed record exceeds closed bound")
	}
	raw := make([]byte, int(n))
	_, e := io.ReadFull(r, raw)
	return raw, e
}
func writeRecord(w io.Writer, v any) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(raw) == 0 || len(raw) > MaxRequestBytes {
		return errors.New("routed record exceeds closed bound")
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(raw)))
	for _, p := range [][]byte{size[:], raw} {
		for len(p) > 0 {
			n, e := w.Write(p)
			if e != nil {
				return e
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			p = p[n:]
		}
	}
	return nil
}
func ReadRequest(r io.Reader, o Open) (Request, error) {
	raw, e := readRecord(r)
	if e != nil {
		return Request{}, e
	}
	var q Request
	if e = workspace.Decode(raw, &q); e != nil {
		return Request{}, e
	}
	if e = q.Validate(o); e != nil {
		return Request{}, e
	}
	return q, nil
}
func WriteRequest(w io.Writer, o Open, q Request) error {
	if e := q.Validate(o); e != nil {
		return e
	}
	return writeRecord(w, q)
}
func ReadReply(r io.Reader, q Request) (Reply, error) {
	raw, e := readRecord(r)
	if e != nil {
		return Reply{}, e
	}
	var p Reply
	if e = workspace.Decode(raw, &p); e != nil {
		return Reply{}, e
	}
	if e = p.Validate(q); e != nil {
		return Reply{}, e
	}
	return p, nil
}
func WriteReply(w io.Writer, q Request, r Reply) error {
	if e := r.Validate(q); e != nil {
		return e
	}
	return writeRecord(w, r)
}
