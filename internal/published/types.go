// Package published stores explicit workspace-scoped poll publications. It
// contains no native delivery or routing/manager callbacks.
package published

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/workspace"
)

const (
	MaxActive             = 32
	MaxRetained           = 256
	MaxEvents             = 512
	MaxLinks              = 256
	MaxContactRecord      = 8 << 10
	MaxRecord             = 12 << 10
	MaxInitialPublication = 6 << 10
	MaxChangeRecord       = 2*MaxRecord + 4096
	MaxBytes              = 16 << 20
	FinalizationReserve   = MaxChangeRecord + MaxRecord
)

type Address struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Generation string `json:"generation"`
}
type Request struct {
	Workspace    string `json:"workspace"`
	ID           string `json:"id"`
	Session      string `json:"session"`
	Registration string `json:"registration"`
	Label        string `json:"label"`
	Delivery     string `json:"delivery"`
	Key          string `json:"key"`
	Reason       string `json:"reason"`
}
type Withdrawal struct {
	Key      string `json:"key"`
	Reason   string `json:"reason"`
	Revision int64  `json:"revision"`
}
type Scope struct {
	Workspace         string              `json:"workspace"`
	Generation        string              `json:"generation"`
	Authority         workspace.Authority `json:"authority"`
	Pin               string              `json:"pin"`
	Membership        int64               `json:"membership"`
	WorkspaceRevision int64               `json:"workspace_revision"`
}
type Plan struct {
	Request    Request `json:"request"`
	Scope      Scope   `json:"scope"`
	ContactSHA string  `json:"contact_sha256"`
	ReviewSHA  string  `json:"review_sha256"`
}
type Actor struct {
	UID      string `json:"uid"`
	Username string `json:"username"`
}
type Publication struct {
	Schema     int            `json:"schema_version"`
	Scope      Scope          `json:"scope"`
	Request    Request        `json:"request"`
	Endpoint   Address        `json:"endpoint"`
	Contact    agents.Contact `json:"contact"`
	ContactSHA string         `json:"contact_sha256"`
	ReviewSHA  string         `json:"review_sha256"`
	Actor      Actor          `json:"actor"`
	State      string         `json:"state"`
	Revision   int64          `json:"revision"`
	Created    time.Time      `json:"created_at"`
	Updated    time.Time      `json:"updated_at"`
	Withdrawal *Withdrawal    `json:"withdrawal,omitempty"`
}
type Change struct {
	Workspace string       `json:"workspace"`
	Sequence  int64        `json:"sequence"`
	Before    *Publication `json:"before,omitempty"`
	After     Publication  `json:"after"`
}
type Context struct {
	Workspace  string  `json:"workspace"`
	Generation string  `json:"generation"`
	Message    string  `json:"message_id"`
	Digest     string  `json:"message_sha256"`
	Origin     string  `json:"origin_host"`
	Target     string  `json:"target_host"`
	From       Address `json:"origin_endpoint"`
	To         Address `json:"target_endpoint"`
}
type Link struct {
	Schema       int       `json:"schema_version"`
	Context      Context   `json:"context"`
	LocalMessage string    `json:"local_message"`
	Recipient    string    `json:"recipient"`
	Registration string    `json:"registration"`
	ContactSHA   string    `json:"contact_sha256"`
	Created      time.Time `json:"created_at"`
}
type Card struct {
	Endpoint Address `json:"endpoint"`
	Label    string  `json:"label"`
	Delivery string  `json:"delivery"`
	Revision int64   `json:"revision"`
}
type Query struct {
	Workspace string `json:"workspace"`
	Offset    int    `json:"offset"`
	Limit     int    `json:"limit"`
	Revision  int64  `json:"revision"`
	Nonce     string `json:"nonce"`
}
type Page struct {
	Workspace  string    `json:"workspace"`
	Generation string    `json:"generation"`
	Host       string    `json:"host"`
	Revision   int64     `json:"revision"`
	Nonce      string    `json:"nonce"`
	Expires    time.Time `json:"expires_at"`
	Offset     int       `json:"offset"`
	Next       int       `json:"next"`
	Total      int       `json:"total"`
	Cards      []Card    `json:"cards"`
}
type Records struct {
	Initialized   bool
	Publications  []Publication
	Changes       []Change
	Links         []Link
	RawBytes      int
	ReservedBytes int
}

func Hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func Hex(s string, n int) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == n && strings.ToLower(s) == s
}
func Text(s string, n int) bool {
	if !utf8.ValidString(s) || len(s) > n || strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}
func Invalid(s string) error { return model.Err("publication_invalid", s) }
func ContactValid(c agents.Contact) bool {
	u, e := uuid.Parse(c.Session)
	return Text(c.ID, 128) && c.ID != "operator" && c.ID != "broadcast" && e == nil && u.String() == c.Session && Hex(c.Registration, 16) && Text(c.CWD, 2048) && filepath.IsAbs(c.CWD) && (c.ConfigDir == "" || Text(c.ConfigDir, 2048) && filepath.IsAbs(c.ConfigDir)) && (c.Adapter == "codex" || c.Adapter == "claude" || c.Adapter == "generic") && (c.Delivery == "poll" || c.Delivery == "native") && (c.Remote == "" || c.Adapter == "codex" && Text(c.Remote, 2048) && strings.HasPrefix(c.Remote, "unix://"))
}
func (q Request) Validate() error {
	if !Hex(q.Workspace, 16) || !Text(q.ID, 128) || !Text(q.Session, 64) || !Hex(q.Registration, 16) || !Text(q.Label, 128) || !Text(q.Key, 128) || !Text(q.Reason, 512) {
		return Invalid("exact contact/workspace, bounded label, key and reason required")
	}
	if q.Delivery != "poll" {
		return model.Err("unsupported_delivery", "published agents currently support poll delivery only; native delivery is not enabled")
	}
	return nil
}
func (q Query) Validate() error {
	if !Hex(q.Workspace, 16) || !Hex(q.Nonce, 16) || q.Offset < 0 || q.Limit < 1 || q.Limit > 16 || q.Revision < 0 {
		return Invalid("bounded endpoint query required")
	}
	return nil
}
func (p Page) Validate() error {
	if !Hex(p.Workspace, 16) || !Hex(p.Generation, 16) || !Hex(p.Host, 16) || !Hex(p.Nonce, 16) || p.Revision < 0 || p.Expires.IsZero() || p.Offset < 0 || p.Total < 0 || p.Total > MaxActive || p.Next != p.Offset+len(p.Cards) || p.Next > p.Total || len(p.Cards) > 16 {
		return Invalid("invalid bounded endpoint page")
	}
	last := ""
	for _, c := range p.Cards {
		if c.Endpoint.Kind != "agent" || !Hex(c.Endpoint.ID, 16) || !Hex(c.Endpoint.Generation, 16) || !Text(c.Label, 128) || c.Delivery != "poll" || c.Revision < 1 || c.Endpoint.ID <= last {
			return Invalid("invalid public endpoint card")
		}
		last = c.Endpoint.ID
	}
	return nil
}
func (p Publication) Card() Card { return Card{p.Endpoint, p.Request.Label, "poll", p.Revision} }
func (p Plan) Digest() string    { p.ReviewSHA = ""; return Hash(p) }
