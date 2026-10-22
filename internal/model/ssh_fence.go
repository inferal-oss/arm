package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// SSHControlCommitment binds the source's pre-execution intention to the
// destination-authenticated identity. Actual PTY modes arrive later; only PTY
// expectation is committed here. No executable command or private key is stored.
type SSHControlCommitment struct {
	Schema          int           `json:"schema"`
	Source          string        `json:"source"`
	Target          string        `json:"target"`
	Pair            string        `json:"pair"`
	Revision        int           `json:"revision"`
	Certificate     string        `json:"certificate"`
	Owner           string        `json:"owner"`
	Account         string        `json:"account"`
	HostKeySHA256   string        `json:"host_key_sha256"`
	Key             string        `json:"key"`
	PublicKeySHA256 string        `json:"public_key_sha256"`
	Reason          string        `json:"reason"`
	TTL             time.Duration `json:"ttl_ns"`
	Resources       Resources     `json:"resources"`
	CWD             string        `json:"cwd"`
	Kind            string        `json:"kind"`
	CommandSHA256   string        `json:"command_sha256"`
	PTY             bool          `json:"pty"`
}

func (c SSHControlCommitment) Validate() error {
	uid, e := strconv.ParseUint(c.Owner, 10, 32)
	if c.Schema != 1 || !sshFenceHex(c.Source, 32) || !sshFenceHex(c.Target, 32) || c.Source == c.Target || !sshFenceHex(c.Pair, 64) || c.Revision < 1 || !sshFenceHex(c.Certificate, 64) || e != nil || strconv.FormatUint(uid, 10) != c.Owner || !sshFenceText(c.Account, 256) || !sshFenceHex(c.HostKeySHA256, 64) || !sshFenceText(c.Key, 128) || !sshFenceHex(c.PublicKeySHA256, 64) || !sshFenceText(c.Reason, 4096) || c.TTL <= 0 || c.TTL > 7*24*time.Hour || math.IsNaN(c.Resources.CPU) || math.IsInf(c.Resources.CPU, 0) || c.Resources.CPU < 0 || c.Resources.CPU > 65536 || c.Resources.Memory < 0 || c.Resources.Disk < 0 || c.Resources == (Resources{}) || !filepath.IsAbs(c.CWD) || !sshFenceText(c.CWD, 4096) || (c.Kind != "exec" && c.Kind != "shell") || !sshFenceHex(c.CommandSHA256, 64) {
		return fmt.Errorf("invalid exact SSH control commitment")
	}
	if c.Kind == "shell" {
		h := sha256.Sum256(nil)
		if c.CommandSHA256 != hex.EncodeToString(h[:]) {
			return fmt.Errorf("shell commitment must have empty command hash")
		}
	}
	return nil
}
func (c SSHControlCommitment) Digest() (string, error) {
	if e := c.Validate(); e != nil {
		return "", e
	}
	raw, e := json.Marshal(c)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}
func (c SSHControlCommitment) Matches(q SSHExecutionRequest) bool {
	return c.Validate() == nil && c.Source == q.Source && c.Target == q.Target && c.Pair == q.Pair && c.Revision == q.Revision && c.Certificate == q.Certificate && c.Account == q.Account && c.Key == q.Key && c.PublicKeySHA256 == q.PublicKeySHA256 && c.Reason == q.Reason && c.TTL == q.TTL && c.Resources == q.Resources && c.CWD == q.CWD && c.Kind == q.Kind && c.CommandSHA256 == q.CommandSHA256 && ((c.PTY && sshFenceHex(q.TerminalSHA256, 64)) || (!c.PTY && q.TerminalSHA256 == ""))
}

// SSHKeyFence is immutable destination evidence. A sealed key has no lease or
// operation and can never execute. Legacy rows retain an exact operation hash;
// no historical control commitment is invented during private B1 migration.
type SSHKeyFence struct {
	Schema          int                   `json:"schema"`
	Source          string                `json:"source"`
	Target          string                `json:"target"`
	Key             string                `json:"key"`
	State           string                `json:"state"`
	Control         *SSHControlCommitment `json:"control"`
	ControlSHA256   string                `json:"control_sha256"`
	OperationSHA256 string                `json:"operation_sha256"`
	Lease           string                `json:"lease"`
	Actor           string                `json:"actor"`
	Reason          string                `json:"reason"`
	At              time.Time             `json:"at"`
}

func (f SSHKeyFence) Validate() error {
	if f.Schema != 1 || !sshFenceHex(f.Source, 32) || !sshFenceHex(f.Target, 32) || f.Source == f.Target || !sshFenceText(f.Key, 128) || !sshFenceText(f.Actor, 512) || !sshFenceText(f.Reason, 4096) || f.At.IsZero() {
		return fmt.Errorf("invalid SSH key fence")
	}
	switch f.State {
	case "sealed", "admitted":
		if f.Control == nil || f.Control.Source != f.Source || f.Control.Target != f.Target || f.Control.Key != f.Key {
			return fmt.Errorf("SSH fence has no exact control")
		}
		digest, e := f.Control.Digest()
		if e != nil || digest != f.ControlSHA256 {
			return fmt.Errorf("SSH fence commitment differs")
		}
		if f.State == "sealed" {
			if f.Lease != "" || f.OperationSHA256 != "" {
				return fmt.Errorf("nonexecution fence cannot have admitted work")
			}
		} else if !sshFenceHex(f.Lease, 32) || !sshFenceHex(f.OperationSHA256, 64) {
			return fmt.Errorf("admission fence lacks exact operation")
		}
	case "legacy-admitted":
		if f.Control != nil || f.ControlSHA256 != "" || !sshFenceHex(f.Lease, 32) || !sshFenceHex(f.OperationSHA256, 64) {
			return fmt.Errorf("legacy admission cannot invent a control commitment")
		}
	default:
		return fmt.Errorf("unknown SSH fence state")
	}
	return nil
}

type SSHKeyStatus struct {
	Schema    int          `json:"schema"`
	Source    string       `json:"source"`
	Target    string       `json:"target"`
	Key       string       `json:"key"`
	State     string       `json:"state"`
	Fence     *SSHKeyFence `json:"fence"`
	Execution *SSHLookup   `json:"execution"`
}

func sshFenceHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
func sshFenceText(s string, n int) bool {
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
