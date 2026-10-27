// Package meshstream provides the closed, bounded transport for admitted mesh
// SSH streams. It does not grant execution or launch processes.
package meshstream

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"getarm.org/internal/model"
	"golang.org/x/crypto/ssh"
)

const Path = "/v1/mesh/ssh"
const Protocol = "arm.mesh.ssh.v1"
const MaxControlBytes = 16 << 10

// MaxFrameBytes is the stream reader resource bound, including the upstream
// library's one-byte lookahead. WebSocket limits apply to whole messages, not
// individual fragments. Legitimate payloads reserve that probe byte.
const MaxFrameBytes = 64 << 10
const MaxPayloadBytes = MaxFrameBytes - 1

// Scope comes from current authenticated destination authorization, never from
// the peer's control record. Certificate is the pinned client's fingerprint.
type Scope struct {
	Source, Target, Pair, Account, Certificate string
	Revision                                   int
	MaxTTL                                     time.Duration
	Limits                                     model.Resources
	ValidUntil                                 time.Time
	Owner, HostKeySHA256                       string
	ServiceRevision                            uint64
}

type Control struct {
	Version       int             `json:"version"`
	Source        string          `json:"source"`
	Target        string          `json:"target"`
	Pair          string          `json:"pair"`
	Revision      int             `json:"revision"`
	Account       string          `json:"account"`
	Key           string          `json:"key"`
	PublicKey     string          `json:"public_key"`
	Reason        string          `json:"reason"`
	TTL           time.Duration   `json:"ttl_ns"`
	Resources     model.Resources `json:"resources"`
	CWD           string          `json:"cwd"`
	TargetOwner   string          `json:"target_owner"`
	HostKeySHA256 string          `json:"host_key_sha256"`
	Kind          string          `json:"kind"`
	CommandSHA256 string          `json:"command_sha256"`
	PTY           bool            `json:"pty"`
}
type ready struct {
	Version       int    `json:"version"`
	Source        string `json:"source"`
	Target        string `json:"target"`
	Pair          string `json:"pair"`
	Revision      int    `json:"revision"`
	Account       string `json:"account"`
	ControlSHA256 string `json:"control_sha256"`
}

func (c Control) Ready() ready {
	raw, _ := json.Marshal(c)
	h := sha256.Sum256(raw)
	return ready{1, c.Source, c.Target, c.Pair, c.Revision, c.Account, hex.EncodeToString(h[:])}
}

func (c Control) commitment(certificate string) model.SSHControlCommitment {
	key, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(c.PublicKey))
	publicHash := ""
	if key != nil {
		h := sha256.Sum256(key.Marshal())
		publicHash = hex.EncodeToString(h[:])
	}
	return model.SSHControlCommitment{Schema: 1, Source: c.Source, Target: c.Target, Pair: c.Pair, Revision: c.Revision, Certificate: certificate, Owner: c.TargetOwner, Account: c.Account, HostKeySHA256: c.HostKeySHA256, Key: c.Key, PublicKeySHA256: publicHash, Reason: c.Reason, TTL: c.TTL, Resources: c.Resources, CWD: c.CWD, Kind: c.Kind, CommandSHA256: c.CommandSHA256, PTY: c.PTY}
}
func (c Control) Commitment(certificate string) (model.SSHControlCommitment, error) {
	q := c.commitment(certificate)
	if e := c.Validate(); e != nil {
		return q, e
	}
	return q, q.Validate()
}
func (c Control) Validate() error {
	if c.Version != 1 || !hexID(c.Source, 32) || !hexID(c.Target, 32) || c.Source == c.Target || !hexID(c.Pair, 64) || c.Revision < 1 || !safeText(c.Account, 256) || !safeText(c.Key, 128) || !safeText(c.Reason, 4096) || !safeText(c.PublicKey, 1024) || !strings.HasPrefix(c.PublicKey, "ssh-ed25519 ") || !filepath.IsAbs(c.CWD) || !safeText(c.CWD, 4096) {
		return fmt.Errorf("invalid SSH stream identity or bounded purpose")
	}
	key, _, _, rest, keyErr := ssh.ParseAuthorizedKey([]byte(c.PublicKey))
	if keyErr != nil || key.Type() != ssh.KeyAlgoED25519 || len(rest) != 0 || strings.TrimSpace(c.PublicKey) != strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) {
		return fmt.Errorf("canonical Ed25519 public key required")
	}
	if c.TTL <= 0 || c.TTL > 7*24*time.Hour || math.IsNaN(c.Resources.CPU) || math.IsInf(c.Resources.CPU, 0) || c.Resources.CPU < 0 || c.Resources.CPU > 65536 || c.Resources.Memory < 0 || c.Resources.Disk < 0 || c.Resources == (model.Resources{}) {
		return fmt.Errorf("positive SSH budget and bounded TTL required")
	}
	return c.commitment(strings.Repeat("0", 64)).Validate()
}
func (c Control) Authorize(s Scope) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Source != s.Source || c.Target != s.Target || c.Pair != s.Pair || c.Revision != s.Revision || c.Account != s.Account || c.TargetOwner != s.Owner || c.HostKeySHA256 != s.HostKeySHA256 || s.Certificate == "" || !time.Now().Before(s.ValidUntil) || s.MaxTTL <= 0 || c.TTL > s.MaxTTL || !c.Resources.Fits(s.Limits) {
		return fmt.Errorf("SSH stream authority or grant changed")
	}
	return nil
}
func hexID(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func safeText(s string, n int) bool {
	if strings.TrimSpace(s) == "" || len(s) > n || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}

// decodeControl rejects duplicate and case-alias keys before struct decoding.
func decodeControl(raw []byte) (Control, error) {
	var c Control
	top, err := closedObject(raw, []string{"version", "source", "target", "pair", "revision", "account", "key", "public_key", "reason", "ttl_ns", "resources", "cwd", "target_owner", "host_key_sha256", "kind", "command_sha256", "pty"})
	if err != nil {
		return c, err
	}
	if _, err = closedObject(top["resources"], []string{"cpu", "memory_bytes", "disk_bytes"}); err != nil {
		return c, err
	}
	err = json.Unmarshal(raw, &c)
	if err == nil {
		err = c.Validate()
	}
	return c, err
}
func closedObject(raw []byte, required []string) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("one closed object required")
	}
	fields := map[string]json.RawMessage{}
	allowed := map[string]bool{}
	for _, name := range required {
		allowed[name] = true
	}
	for d.More() {
		t, e := d.Token()
		if e != nil {
			return nil, e
		}
		name, ok := t.(string)
		if !ok || !allowed[name] || fields[name] != nil {
			return nil, fmt.Errorf("unknown, alias or duplicate field")
		}
		var v json.RawMessage
		if e = d.Decode(&v); e != nil {
			return nil, e
		}
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, fmt.Errorf("null field")
		}
		fields[name] = v
	}
	if _, err = d.Token(); err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	if len(fields) != len(required) {
		return nil, fmt.Errorf("required field missing")
	}
	return fields, nil
}
