// Package routing records addressed workspace messages and published endpoint
// scopes in the central SQLite authority. It grants no execution, direct mesh,
// filesystem or operator authentication. Every producer requires current
// workspace.RouteTransaction authorization on that SAME SQLite transaction.
package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"getarm.org/internal/published"
	"getarm.org/internal/workspace"
)

const (
	Schema           = 1
	MaxBodyBytes     = 16384
	MaxEnvelopeBytes = 56 << 10 // leaves a fixed bounded header inside the60KiB routed request
	MaxMessages      = 256      // retained inbox entries per local workspace, no eviction
	MaxOutbox        = 256
	MaxPending       = 8
	MaxRecordBytes   = 64 << 10
	MaxLedgerBytes   = 32 << 20
	MaxLifetime      = 24 * time.Hour
)

// Endpoint is workspace-scoped. Host endpoint generation is the exact decimal
// membership generation; agent generation is an opaque publication generation,
// never a contact/session/config path or a claim of independent agent auth.
type Endpoint struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Generation string `json:"generation"`
}

func HostEndpoint(generation int64) Endpoint {
	return Endpoint{Kind: "host", ID: "operator", Generation: strconv.FormatInt(generation, 10)}
}
func (e Endpoint) Validate() bool {
	if e.Kind == "host" {
		n, err := strconv.ParseInt(e.Generation, 10, 64)
		return err == nil && n > 0 && strconv.FormatInt(n, 10) == e.Generation && e.ID == "operator"
	}
	return e.Kind == "agent" && hexID(e.ID, 16) && hexID(e.Generation, 16)
}

type Reference struct {
	ID     string   `json:"message_id"`
	Digest string   `json:"message_sha256"`
	Origin string   `json:"origin_host"`
	From   Endpoint `json:"origin_endpoint"`
	Target string   `json:"target_host"`
	To     Endpoint `json:"target_endpoint"`
}
type Envelope struct {
	OriginPin        string     `json:"origin_pin"`
	TargetPin        string     `json:"target_pin"`
	Schema           int        `json:"schema_version"`
	Workspace        string     `json:"workspace"`
	Generation       string     `json:"generation"`
	ID               string     `json:"message_id"`
	Key              string     `json:"client_key"`
	Origin           string     `json:"origin_host"`
	From             Endpoint   `json:"origin_endpoint"`
	Target           string     `json:"target_host"`
	To               Endpoint   `json:"target_endpoint"`
	OriginMembership int64      `json:"origin_membership"`
	TargetMembership int64      `json:"target_membership"`
	Body             string     `json:"body"`
	Created          time.Time  `json:"created_at"`
	Expires          time.Time  `json:"expires_at"`
	ReplyTo          *Reference `json:"reply_to,omitempty"`
}

func hash(v any) string {
	raw, _ := json.Marshal(v)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (e Envelope) Digest() string { return hash(e) }
func (e Envelope) Reference() Reference {
	return Reference{ID: e.ID, Digest: e.Digest(), Origin: e.Origin, From: e.From, Target: e.Target, To: e.To}
}
func hexID(v string, n int) bool {
	raw, err := hex.DecodeString(v)
	return err == nil && len(raw) == n && strings.ToLower(v) == v
}
func text(v string, n int) bool {
	if len(v) < 1 || len(v) > n || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}
func validKey(v string) bool { return text(v, 128) }
func (e Envelope) Validate() bool {
	if e.Schema != Schema || !hexID(e.OriginPin, 32) || !hexID(e.TargetPin, 32) || !hexID(e.Workspace, 16) || !hexID(e.Generation, 16) || !hexID(e.ID, 16) || !validKey(e.Key) || !hexID(e.Origin, 16) || !hexID(e.Target, 16) || e.Origin == e.Target || !e.From.Validate() || !e.To.Validate() || e.OriginMembership < 1 || e.TargetMembership < 1 || e.Body == "" || len(e.Body) > MaxBodyBytes || !utf8.ValidString(e.Body) || strings.IndexByte(e.Body, 0) >= 0 || e.Created.IsZero() || !e.Expires.After(e.Created) || e.Expires.Sub(e.Created) > MaxLifetime {
		return false
	}
	if e.From.Kind == "host" && e.From != HostEndpoint(e.OriginMembership) || e.To.Kind == "host" && e.To != HostEndpoint(e.TargetMembership) {
		return false
	}
	if r := e.ReplyTo; r != nil {
		if !hexID(r.ID, 16) || !hexID(r.Digest, 32) || !hexID(r.Origin, 16) || !hexID(r.Target, 16) || r.Origin != e.Target || r.Target != e.Origin || r.From != e.To || r.To != e.From {
			return false
		}
	}
	raw, err := json.Marshal(e)
	return err == nil && len(raw) <= MaxEnvelopeBytes
}

// Intent is local-only input. It contains no asserted remote identity or final
// certificate. A current local source endpoint and remote endpoint are resolved
// under exact reviewed publication generations before Prepare is called.
type Intent struct {
	Workspace string        `json:"workspace"`
	Key       string        `json:"key"`
	Target    string        `json:"target_host"`
	From      Endpoint      `json:"origin_endpoint"`
	To        Endpoint      `json:"target_endpoint"`
	Body      string        `json:"body"`
	TTL       time.Duration `json:"ttl_ns"`
	ReplyTo   *Reference    `json:"reply_to,omitempty"`
}
type Receipt struct {
	Schema     int       `json:"schema_version"`
	Workspace  string    `json:"workspace"`
	Generation string    `json:"generation"`
	ID         string    `json:"message_id"`
	Digest     string    `json:"message_sha256"`
	Origin     string    `json:"origin_host"`
	From       Endpoint  `json:"origin_endpoint"`
	Target     string    `json:"target_host"`
	To         Endpoint  `json:"target_endpoint"`
	State      string    `json:"state"`
	Revision   int64     `json:"revision"`
	At         time.Time `json:"recorded_at"`
	Code       string    `json:"code,omitempty"`
}

func receipt(e Envelope, at time.Time, state, code string) Receipt {
	return Receipt{Schema: Schema, Workspace: e.Workspace, Generation: e.Generation, ID: e.ID, Digest: e.Digest(), Origin: e.Origin, From: e.From, Target: e.Target, To: e.To, State: state, Revision: 1, At: at, Code: code}
}
func (r Receipt) Matches(e Envelope) bool {
	return (r.Code == "" || text(r.Code, 128)) && r.Schema == Schema && r.Workspace == e.Workspace && r.Generation == e.Generation && r.ID == e.ID && r.Digest == e.Digest() && r.Origin == e.Origin && r.Target == e.Target && r.From == e.From && r.To == e.To && r.Revision > 0 && !r.At.IsZero() && (r.State == "accepted" || r.State == "delivery_pending" || r.State == "delivered" || r.State == "acknowledged" || r.State == "rejected" || r.State == "superseded" || r.State == "expired")
}

type Outbox struct {
	Schema     int                 `json:"schema_version"`
	Workspace  string              `json:"workspace"`
	Authority  workspace.Authority `json:"local_authority"`
	Intent     Intent              `json:"intent"`
	IntentHash string              `json:"intent_sha256"`
	Envelope   Envelope            `json:"envelope"`
	Phase      string              `json:"phase"`
	Revision   int64               `json:"revision"`
	Created    time.Time           `json:"created_at"`
	Updated    time.Time           `json:"updated_at"`
	Receipt    *Receipt            `json:"receipt,omitempty"`
}
type Inbox struct {
	Schema    int       `json:"schema_version"`
	Workspace string    `json:"workspace"`
	Envelope  Envelope  `json:"envelope"`
	Receipt   Receipt   `json:"receipt"`
	Received  time.Time `json:"received_at"`
}
type Root struct {
	Schema     int                 `json:"schema_version"`
	Workspace  string              `json:"workspace"`
	Generation string              `json:"generation"`
	Authority  workspace.Authority `json:"local_authority"`
	LocalPin   string              `json:"local_pin"`
	State      string              `json:"state"`
	Revision   int64               `json:"revision"`
	Created    time.Time           `json:"created_at"`
	Updated    time.Time           `json:"updated_at"`
}
type Change struct {
	Schema      int    `json:"schema_version"`
	Workspace   string `json:"workspace"`
	Kind        string `json:"kind"`
	Actor       string `json:"actor"`
	Key         string `json:"key"`
	Message     string `json:"message_id"`
	Digest      string `json:"message_sha256"`
	Phase       string `json:"phase"`
	ReceiptHash string `json:"receipt_sha256,omitempty"`
	Before      *Root  `json:"before,omitempty"`
	After       Root   `json:"after"`
}
type Records struct {
	Initialized bool
	Published   *published.Records
	Graph       *GraphRecords
	Roots       []Root
	Outbox      []Outbox
	Inbox       []Inbox
	Events      []Change
	rawBytes    int
}
