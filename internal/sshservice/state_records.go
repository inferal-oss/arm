package sshservice

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/ssh"
	"math"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/model"
)

const StateLockName = "ssh-pins.lock"
const ServiceFile = "ssh-service.json"
const MaxSourceIntents = 64
const MaxPins = 64
const MaxCompletions = 64
const MaxStateMembers = 256
const MaxIntentBytes = 32 << 10

// Binding is supplied by the authenticated local ledger owner. It never
// selects a remote OS account or changes the host authority.
type Binding struct {
	Host    string `json:"host"`
	Owner   string `json:"owner"`
	Account string `json:"account"`
}
type ServiceRecord struct {
	Schema    int       `json:"schema"`
	Binding   Binding   `json:"binding"`
	PublicKey string    `json:"public_key"`
	Enabled   bool      `json:"enabled"`
	Revision  uint64    `json:"revision"`
	Reason    string    `json:"reason"`
	Created   time.Time `json:"created_at"`
	Updated   time.Time `json:"updated_at"`
}

// SourceRequest is the exact pre-stream intention. Actual terminal modes are
// chosen by OpenSSH later and are hashed by the destination, never guessed here.
type SourceRequest struct {
	Key               string          `json:"key"`
	Pin               Pin             `json:"pin"`
	TargetOwner       string          `json:"target_owner"`
	SourceCertificate string          `json:"source_certificate"`
	PublicKey         string          `json:"public_key"`
	Reason            string          `json:"reason"`
	TTL               time.Duration   `json:"ttl_ns"`
	Resources         model.Resources `json:"resources"`
	CWD               string          `json:"cwd"`
	Kind              string          `json:"kind"`
	CommandSHA256     string          `json:"command_sha256"`
	PTY               bool            `json:"pty"`
}
type InvocationDirectory struct {
	Path   string `json:"path"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	Owner  string `json:"owner"`
}
type SourceIntent struct {
	Schema    int                   `json:"schema"`
	Source    string                `json:"source"`
	Owner     string                `json:"owner"`
	Request   SourceRequest         `json:"request"`
	Worker    model.ProcessIdentity `json:"worker"`
	Directory InvocationDirectory   `json:"directory"`
	Created   time.Time             `json:"created_at"`
}

func (b Binding) Validate() error {
	if !validID(b.Host) || !validOwner(b.Owner) || !validAccount(b.Account) {
		return fmt.Errorf("invalid SSH authority/account binding")
	}
	return nil
}
func validOwner(owner string) bool {
	v, e := strconv.ParseUint(owner, 10, 32)
	return e == nil && strconv.FormatUint(v, 10) == owner
}
func canonicalPublic(s string) bool {
	p := Pin{Schema: 1, Source: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Pair: strings.Repeat("c", 64), Revision: 1, TLSFingerprint: strings.Repeat("d", 64), TLSSPKI: strings.Repeat("e", 64), Account: "account", PublicKey: s, At: time.Unix(1, 0).UTC()}
	return p.Validate() == nil
}
func stateText(s string, n int) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) == "" || len(s) > n {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}
func (r ServiceRecord) Validate() error {
	if r.Schema != 1 || r.Binding.Validate() != nil || !canonicalPublic(r.PublicKey) || r.Revision < 1 || r.Revision > 1000000 || !stateText(r.Reason, 4096) || r.Created.IsZero() || r.Updated.Before(r.Created) {
		return fmt.Errorf("invalid SSH service record")
	}
	return nil
}
func (q SourceRequest) Validate() error {
	if !stateText(q.Key, 128) || q.Pin.Validate() != nil || !validOwner(q.TargetOwner) || !hexString(q.SourceCertificate, 64) || !canonicalPublic(q.PublicKey) || !stateText(q.Reason, 4096) || q.TTL <= 0 || q.TTL > 7*24*time.Hour || !filepath.IsAbs(q.CWD) || !stateText(q.CWD, 4096) || (q.Kind != "exec" && q.Kind != "shell") || !hexString(q.CommandSHA256, 64) || q.Kind == "shell" && q.CommandSHA256 != stateHash(nil) || math.IsNaN(q.Resources.CPU) || math.IsInf(q.Resources.CPU, 0) || q.Resources.CPU < 0 || q.Resources.CPU > 65536 || q.Resources.Memory < 0 || q.Resources.Disk < 0 || q.Resources == (model.Resources{}) {
		return fmt.Errorf("invalid exact SSH source request")
	}
	return nil
}
func (r SourceIntent) Validate() error {
	// Linux may report PGID0 when the source's inherited process group is
	// outside its PID namespace. This is a fingerprint only: no source group
	// signal/adoption is ever authorized by a source-intent record.
	if r.Schema != 1 || !validID(r.Source) || !validOwner(r.Owner) || r.Request.Validate() != nil || r.Request.Pin.Source != r.Source || r.Worker.PID <= 0 || r.Worker.PID > math.MaxInt32 || r.Worker.PGID < 0 || r.Worker.PGID > math.MaxInt32 || r.Worker.UID != r.Owner || !stateText(r.Worker.Start, 256) || !stateText(r.Worker.Namespace, 256) || !validInvocationPath(r.Directory.Path) || !safePath(r.Directory.Path) || filepath.Clean(r.Directory.Path) != r.Directory.Path || r.Directory.Device == 0 || r.Directory.Inode == 0 || r.Directory.Owner != r.Owner || r.Created.IsZero() {
		return fmt.Errorf("invalid SSH source intent")
	}
	return nil
}

// DecodeStateRecord accepts only exact canonical producer JSON: recursively
// rejects aliases, missing/null fields, duplicates, unknown fields and trailing
// bytes without encoding/json's permissive primitive-null/case coercion.
func DecodeStateRecord(raw []byte, out any) error {
	if len(raw) > MaxIntentBytes {
		return fmt.Errorf("SSH state record exceeds32KiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	encoded, err := json.Marshal(out)
	if err != nil || !bytes.Equal(encoded, raw) {
		return fmt.Errorf("SSH record is not exact canonical producer evidence")
	}
	return nil
}

func MatchTerminalReceipt(intent SourceIntent, receipt model.SSHLookup) error {
	return matchSSHAdmission(intent, receipt, true)
}

// matchSSHAdmission validates the original admission without asserting that
// nonterminal work has settled. Both branches retain the full request relation.
func matchSSHAdmission(intent SourceIntent, receipt model.SSHLookup, terminal bool) error {
	if intent.Validate() != nil || !receipt.Found || receipt.Operation == nil || receipt.Lease == nil {
		return fmt.Errorf("matching durable destination receipt required")
	}
	op, l := receipt.Operation, receipt.Lease
	q := op.Request
	r := intent.Request
	expected := model.Request{Host: q.Target, Peer: intent.Source, OriginKey: r.Key, Key: "mesh:" + stateHash([]byte("arm-mesh-key-v1\x00"+intent.Source+"\x00ssh-exec\x00"+r.Key)), Reason: r.Reason, TTL: r.TTL, Resources: r.Resources, PeerCertificate: r.SourceCertificate, PeerGrantRevision: r.Pin.Revision}
	if l.Request.Budget == DefaultSSHPolicyID(q.Target, r.TargetOwner) {
		expected.Budget = l.Request.Budget
	}
	if !validID(l.ID) || l.Host != q.Target || l.Created.IsZero() || !reflect.DeepEqual(l.Request, expected) || ValidateSSHLeaseAttachments(*l) != nil {
		return fmt.Errorf("destination receipt has inconsistent authority, admission or unsupported attached obligations")
	}
	if op.Schema != 1 || op.Lease == "" || op.Lease != l.ID || op.AdmittedAt.IsZero() || l.Ended != nil && l.Ended.Before(op.AdmittedAt) || op.AdmittedAt.Before(l.Created) || q.Source != intent.Source || q.Target != r.Pin.Target || q.Pair != r.Pin.Pair || q.Revision != r.Pin.Revision || q.Account != r.Pin.Account || q.Key != r.Key || q.Certificate != r.SourceCertificate || q.PublicKeySHA256 != publicKeyHash(r.PublicKey) || q.Reason != r.Reason || q.TTL != r.TTL || !reflect.DeepEqual(q.Resources, r.Resources) || q.CWD != r.CWD || q.Kind != r.Kind || q.CommandSHA256 != r.CommandSHA256 || (r.PTY && !hexString(q.TerminalSHA256, 64)) || (!r.PTY && q.TerminalSHA256 != "") || l.Request.Host != q.Target || l.Request.Peer != intent.Source || l.Request.OriginKey != r.Key || l.OwnerUsername != q.Account || l.Owner != r.TargetOwner || l.Request.Key != "mesh:"+stateHash([]byte("arm-mesh-key-v1\x00"+intent.Source+"\x00ssh-exec\x00"+r.Key)) || l.Request.Reason != q.Reason || l.Request.TTL != q.TTL || l.Request.Resources != q.Resources || l.Request.PeerCertificate != q.Certificate || l.Request.PeerGrantRevision != q.Revision {
		return fmt.Errorf("destination receipt differs from exact source intention or is not terminal")
	}
	switch l.State {
	case "provisioning", "active", "ending", "reclaiming", "needs_attention", "terminal":
	default:
		return fmt.Errorf("unsupported SSH lease lifecycle state")
	}
	if terminal && (l.State != "terminal" || l.Ended == nil || l.Outcome == "") {
		return fmt.Errorf("destination receipt is not terminal")
	}
	if l.State == "terminal" && (l.Ended == nil || l.Outcome == "") {
		return fmt.Errorf("terminal SSH admission lacks final outcome")
	}
	return nil
}

func stateHash(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func publicKeyHash(raw string) string {
	k, _, _, _, e := ssh.ParseAuthorizedKey([]byte(raw))
	if e != nil {
		return ""
	}
	return publicDigest(k)
}

// SourceCompletion preserves the matching terminal receipt before pending-file
// removal; interrupted settlement never erases all recovery evidence.
type SourceCompletion struct {
	Schema  int             `json:"schema"`
	Intent  SourceIntent    `json:"intent"`
	Receipt model.SSHLookup `json:"receipt"`
	At      time.Time       `json:"settled_at"`
}

func (r SourceCompletion) Validate() error {
	// Intent/settlement use the source clock. Receipt chronology is validated
	// within the destination authority; the two wall clocks need not agree.
	if r.Schema != 1 || r.At.IsZero() || r.At.Before(r.Intent.Created) || r.Receipt.Lease == nil || r.Receipt.Lease.Ended == nil {
		return fmt.Errorf("invalid SSH source completion")
	}
	return MatchTerminalReceipt(r.Intent, r.Receipt)
}
