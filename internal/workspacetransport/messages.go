package workspacetransport

import (
	"encoding/json"
	"errors"
	"io"
	"unicode"
	"unicode/utf8"

	"getarm.org/internal/workspace"
)

const (
	ClassAdmissionStatus = "admission_status"
	OperationProof       = "join_proof"
	OperationRefresh     = "directory_refresh"
	OperationStatus      = "admission_status"
)

// Request is the complete inner RPC namespace. It is deliberately not the
// native or direct-mesh request type, and contains no generic method/params.
type Request struct {
	Version    int                               `json:"schema_version"`
	Route      string                            `json:"route_id"`
	Workspace  string                            `json:"workspace"`
	Generation string                            `json:"generation"`
	Source     string                            `json:"source_host"`
	Target     string                            `json:"target_host"`
	Operation  string                            `json:"operation"`
	Challenge  *workspace.Challenge              `json:"challenge,omitempty"`
	Refresh    *workspace.RefreshRequest         `json:"refresh,omitempty"`
	Status     *workspace.AdmissionStatusRequest `json:"admission_status,omitempty"`
}
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Reply struct {
	Version    int                        `json:"schema_version"`
	Route      string                     `json:"route_id"`
	Workspace  string                     `json:"workspace"`
	Generation string                     `json:"generation"`
	Source     string                     `json:"source_host"`
	Target     string                     `json:"target_host"`
	Operation  string                     `json:"operation"`
	Proof      *workspace.Proof           `json:"proof,omitempty"`
	Page       *workspace.Page            `json:"page,omitempty"`
	Status     *workspace.AdmissionStatus `json:"admission_status,omitempty"`
	Failure    *Failure                   `json:"error,omitempty"`
}

func (q Request) Validate(o Open) error {
	if q.Version != Version || q.Route != o.Route || q.Workspace != o.Workspace || q.Generation != o.Generation || q.Source != o.Source || q.Target != o.Target {
		return errors.New("workspace inner request differs from admitted stream")
	}
	count := 0
	for _, present := range []bool{q.Challenge != nil, q.Refresh != nil, q.Status != nil} {
		if present {
			count++
		}
	}
	if count != 1 {
		return errors.New("one closed workspace request payload required")
	}
	switch q.Operation {
	case OperationProof:
		if o.Class != ClassJoinProof || q.Challenge == nil {
			return errors.New("candidate proof is unavailable on this stream class")
		}
		c := q.Challenge
		if _, e := workspace.ValidateIdentity(c.Directory); e != nil {
			return e
		}
		if _, e := workspace.ValidateIdentity(c.Candidate); e != nil {
			return e
		}
		if c.Recovery != nil && (!wireText(c.Recovery.OriginalKey, 128) || !wireText(c.Recovery.AttemptKey, 128) || !hexValue(c.Recovery.CandidateAuthorization, 32)) {
			return errors.New("invalid recovery proof binding")
		}
		if c.Workspace != o.Workspace || c.Generation != o.Generation || c.Directory.Host != o.Source || c.Candidate.Host != o.Target || c.ConsentHash != o.Consent || !hexValue(c.Nonce, 32) {
			return errors.New("candidate challenge differs from exact stream intent")
		}
	case OperationRefresh:
		if o.Class != ClassDirectory || q.Refresh == nil {
			return errors.New("directory refresh is unavailable on this stream class")
		}
		r := q.Refresh
		if r.Source.Workspace != o.Workspace || r.Source.Generation != o.Generation || r.Source.Host != o.Source || r.Source.MembershipGeneration < 1 || !hexValue(r.Source.Fingerprint, 32) || !hexValue(r.Nonce, 32) {
			return errors.New("directory refresh differs from exact stream source")
		}
	case OperationStatus:
		if o.Class != ClassAdmissionStatus || q.Status == nil {
			return errors.New("own admission status is unavailable on this stream class")
		}
		s := q.Status
		if s.Workspace != o.Workspace || s.Generation != o.Generation || s.Source != o.Source || s.Consent != o.Consent || !hexValue(s.Nonce, 32) {
			return errors.New("own admission status differs from exact stream source")
		}
	default:
		return errors.New("operation is outside the closed workspace protocol")
	}
	return nil
}
func (q Request) Response() Reply {
	return Reply{Version: Version, Route: q.Route, Workspace: q.Workspace, Generation: q.Generation, Source: q.Target, Target: q.Source, Operation: q.Operation}
}
func (r Reply) Validate(q Request) error {
	if r.Version != Version || r.Route != q.Route || r.Workspace != q.Workspace || r.Generation != q.Generation || r.Source != q.Target || r.Target != q.Source || r.Operation != q.Operation {
		return errors.New("workspace reply differs from exact request endpoints")
	}
	count := 0
	for _, present := range []bool{r.Proof != nil, r.Page != nil, r.Status != nil, r.Failure != nil} {
		if present {
			count++
		}
	}
	if count != 1 {
		return errors.New("one closed workspace reply payload required")
	}
	if r.Failure != nil {
		if !wireText(r.Failure.Code, 128) || !wireText(r.Failure.Message, 1024) {
			return errors.New("invalid bounded workspace failure")
		}
		return nil
	}
	switch q.Operation {
	case OperationProof:
		if r.Proof == nil || q.Challenge == nil {
			return errors.New("proof reply payload differs")
		}
		p, c := r.Proof, q.Challenge
		if p.Workspace != q.Workspace || p.Generation != q.Generation || p.Source != q.Target || p.Target != q.Source || p.SourcePin != c.Candidate.Fingerprint || p.TargetPin != c.Directory.Fingerprint || p.Nonce != c.Nonce || p.ConsentHash != c.ConsentHash {
			return errors.New("proof reply did not bind the exact candidate challenge")
		}
		actual, _ := json.Marshal(p.Recovery)
		expected, _ := json.Marshal(c.Recovery)
		if string(actual) != string(expected) {
			return errors.New("proof reply recovery binding differs")
		}
	case OperationRefresh:
		if r.Page == nil || q.Refresh == nil {
			return errors.New("directory page reply payload differs")
		}
		p := r.Page
		s := p.Snapshot
		raw, e := json.Marshal(p)
		if e != nil || len(raw) > workspace.MaxPageBytes || p.Total < 1 || p.Total > workspace.MaxMembers || p.Offset < 0 || len(s.Members) < 1 || p.Offset+len(s.Members) > p.Total || s.Schema != workspace.Schema || s.Workspace != q.Workspace || s.Generation != q.Generation || s.Directory.Host != q.Target || s.Requester != q.Source || s.RequesterGeneration != q.Refresh.Source.MembershipGeneration || s.Nonce != q.Refresh.Nonce || !hexValue(s.Hash, 32) {
			return errors.New("directory page differs from bounded exact refresh")
		}
	case OperationStatus:
		if r.Status == nil || q.Status == nil {
			return errors.New("own admission status reply payload differs")
		}
		s := r.Status
		if s.Schema != workspace.Schema || s.Workspace != q.Workspace || s.Generation != q.Generation || s.Source != q.Target || s.Target != q.Source || s.Nonce != q.Status.Nonce || s.Consent != q.Status.Consent {
			return errors.New("own admission status reply source/binding differs")
		}
	default:
		return errors.New("unsupported workspace reply operation")
	}
	return nil
}
func ReadRequest(r io.Reader, o Open) (Request, error) {
	var q Request
	raw, e := readRecord(r, MaxRequestBytes)
	if e != nil {
		return q, e
	}
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
	raw, e := json.Marshal(q)
	if e != nil {
		return e
	}
	return writeRecord(w, raw, MaxRequestBytes)
}
func ReadReply(r io.Reader, q Request) (Reply, error) {
	var result Reply
	raw, e := readRecord(r, MaxRequestBytes)
	if e != nil {
		return result, e
	}
	if e = workspace.Decode(raw, &result); e != nil {
		return Reply{}, e
	}
	if e = result.Validate(q); e != nil {
		return Reply{}, e
	}
	return result, nil
}
func WriteReply(w io.Writer, q Request, r Reply) error {
	if e := r.Validate(q); e != nil {
		return e
	}
	raw, e := json.Marshal(r)
	if e != nil {
		return e
	}
	return writeRecord(w, raw, MaxRequestBytes)
}

func wireText(v string, max int) bool {
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
