package database

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sshservice"
	"golang.org/x/crypto/ssh"
)

// Deliberately closed producer mirrors. Adding a writer field is not by itself
// approval to retire that evidence under the destructive proof.
type proofSSHBinding struct {
	Host    string `json:"host"`
	Owner   string `json:"owner"`
	Account string `json:"account"`
}
type proofSSHService struct {
	Schema    int             `json:"schema"`
	Binding   proofSSHBinding `json:"binding"`
	PublicKey string          `json:"public_key"`
	Enabled   bool            `json:"enabled"`
	Revision  uint64          `json:"revision"`
	Reason    string          `json:"reason"`
	Created   time.Time       `json:"created_at"`
	Updated   time.Time       `json:"updated_at"`
}
type proofSSHPin struct {
	Schema         int       `json:"schema"`
	Source         string    `json:"source"`
	Target         string    `json:"target"`
	Pair           string    `json:"pair"`
	Revision       int       `json:"revision"`
	TLSFingerprint string    `json:"tls_fingerprint"`
	TLSSPKI        string    `json:"tls_spki"`
	Account        string    `json:"account"`
	PublicKey      string    `json:"public_key"`
	At             time.Time `json:"at"`
}
type proofSSHSourceRequest struct {
	Key               string          `json:"key"`
	Pin               proofSSHPin     `json:"pin"`
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
type proofSSHDirectory struct {
	Path   string `json:"path"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	Owner  string `json:"owner"`
}
type proofSSHSource struct {
	Schema    int                   `json:"schema"`
	Source    string                `json:"source"`
	Owner     string                `json:"owner"`
	Request   proofSSHSourceRequest `json:"request"`
	Worker    model.ProcessIdentity `json:"worker"`
	Directory proofSSHDirectory     `json:"directory"`
	Created   time.Time             `json:"created_at"`
}
type proofSSHCompletion struct {
	Schema  int             `json:"schema"`
	Intent  proofSSHSource  `json:"intent"`
	Receipt model.SSHLookup `json:"receipt"`
	At      time.Time       `json:"settled_at"`
}

func strictSSHProducer(raw []byte, out any) error {
	if e := checkSSHProofTypeShapes(); e != nil {
		return e
	}
	if len(raw) > sshservice.MaxIntentBytes {
		return fmt.Errorf("SSH state record exceeds32KiB")
	}
	if e := strictEvidence(string(raw), out); e != nil {
		return e
	}
	canonical, e := json.Marshal(out)
	if e != nil || !bytes.Equal(canonical, raw) {
		return fmt.Errorf("SSH namespace record differs from exact canonical producer")
	}
	return nil
}
func proveSSHNamespace(ctx context.Context, contents map[string][]byte, host, owner string) ([]ProofBlocker, error) {
	blockers := []ProofBlocker{}
	pins, pending, completed := 0, 0, 0
	public := ""
	var service *proofSSHService
	intentRecords := map[string]sshservice.SourceIntent{}
	completionRecords := map[string]sshservice.SourceCompletion{}
	sealRecords := map[string]sshservice.SourceSealCompletion{}
	total := 0
	for _, name := range sortedKeys(contents) {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		raw := contents[name]
		total += len(raw)
		if total > 4<<20 {
			return nil, fmt.Errorf("complete SSH namespace exceeds4MiB")
		}
		switch {
		case name == "config.json", name == "node-key.pem":
		case name == "ssh-history.json", name == "ssh-history-recovery.json":
			// Closed schema, full catalog/pack closure and worker exclusion are
			// checked by reviewSSHSourceHistory; never ignored as generic metadata.
		case name == sshservice.StateLockName:
			if len(raw) != 0 {
				return nil, fmt.Errorf("SSH namespace lock must be empty")
			}
		case name == "ssh-host-key.json":
			var r struct {
				Schema  int    `json:"schema"`
				Host    string `json:"host"`
				Private string `json:"private_pkcs8"`
			}
			if e := strictSSHProducer(raw, &r); e != nil || r.Schema != 1 || r.Host != host {
				return nil, fmt.Errorf("invalid stopped SSH host key authority")
			}
			private, e := base64.StdEncoding.Strict().DecodeString(r.Private)
			if e != nil || base64.StdEncoding.EncodeToString(private) != r.Private {
				return nil, fmt.Errorf("invalid stopped SSH private key encoding")
			}
			key, e := x509.ParsePKCS8PrivateKey(private)
			if e != nil {
				return nil, fmt.Errorf("invalid stopped SSH host private key")
			}
			ed, ok := key.(ed25519.PrivateKey)
			if !ok || len(ed) != ed25519.PrivateKeySize {
				return nil, fmt.Errorf("stopped SSH key is not Ed25519")
			}
			k, e := ssh.NewPublicKey(ed.Public())
			if e != nil {
				return nil, e
			}
			public = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
		case name == sshservice.ServiceFile:
			var r proofSSHService
			if e := strictSSHProducer(raw, &r); e != nil {
				return nil, e
			}
			var typed sshservice.ServiceRecord
			if e := json.Unmarshal(raw, &typed); e != nil || typed.Validate() != nil || r.Binding.Host != host || r.Binding.Owner != owner {
				return nil, fmt.Errorf("stopped SSH service binding is invalid")
			}
			service = &r
		case strings.HasPrefix(name, "ssh-pin-") && strings.HasSuffix(name, ".json"):
			var r proofSSHPin
			if e := strictSSHProducer(raw, &r); e != nil {
				return nil, e
			}
			var typed sshservice.Pin
			if e := json.Unmarshal(raw, &typed); e != nil || typed.Validate() != nil || r.Source != host || name != "ssh-pin-"+r.Target+".json" {
				return nil, fmt.Errorf("invalid stopped SSH pin")
			}
			pins++
		case strings.HasPrefix(name, "ssh-client-") && strings.HasSuffix(name, ".json"):
			var r proofSSHSource
			if e := strictSSHProducer(raw, &r); e != nil {
				return nil, e
			}
			var typed sshservice.SourceIntent
			if e := json.Unmarshal(raw, &typed); e != nil || typed.Validate() != nil || r.Source != host || r.Owner != owner || name != sshIntentProofName(typed) {
				return nil, fmt.Errorf("invalid stopped SSH source intention")
			}
			intentRecords[name] = typed
			pending++
			blockers = append(blockers, ProofBlocker{"ssh_client_intent", r.Request.Key, "An SSH source intention remains pending/unknown; only an exact destination terminal receipt and verified local cleanup can settle it"})
		case strings.HasPrefix(name, "ssh-complete-") && strings.HasSuffix(name, ".json"):
			var r proofSSHCompletion
			if e := strictSSHProducer(raw, &r); e != nil {
				return nil, e
			}
			var typed sshservice.SourceCompletion
			if e := json.Unmarshal(raw, &typed); e != nil || typed.Validate() != nil || r.Intent.Source != host || r.Intent.Owner != owner || name != strings.Replace(sshIntentProofName(typed.Intent), "ssh-client-", "ssh-complete-", 1) {
				return nil, fmt.Errorf("invalid stopped SSH completion receipt")
			}
			completionRecords[strings.Replace(name, "ssh-complete-", "ssh-client-", 1)] = typed
			completed++
			if _, e := os.Lstat(r.Intent.Directory.Path); !os.IsNotExist(e) {
				blockers = append(blockers, ProofBlocker{"ssh_client_artifacts", r.Intent.Request.Key, "SSH invocation artifacts remain or cannot be proved removed; no cleanup inferred"})
			}
		case strings.HasPrefix(name, "ssh-sealed-") && strings.HasSuffix(name, ".json"):
			var r proofSSHSourceSeal
			if e := strictSSHProducer(raw, &r); e != nil {
				return nil, e
			}
			var typed sshservice.SourceSealCompletion
			if e := json.Unmarshal(raw, &typed); e != nil || typed.Validate() != nil || r.Intent.Source != host || r.Intent.Owner != owner || name != strings.Replace(sshIntentProofName(typed.Intent), "ssh-client-", "ssh-sealed-", 1) {
				return nil, fmt.Errorf("invalid stopped SSH nonexecution receipt")
			}
			sealRecords[strings.Replace(name, "ssh-sealed-", "ssh-client-", 1)] = typed
			completed++
			if _, e := os.Lstat(r.Intent.Directory.Path); !os.IsNotExist(e) {
				blockers = append(blockers, ProofBlocker{"ssh_client_artifacts", r.Intent.Request.Key, "SSH invocation artifacts remain or cannot be proved removed; no cleanup inferred"})
			}
		default:
			return nil, fmt.Errorf("mesh namespace contains unfamiliar/partial SSH evidence %q", name)
		}
	}
	if pins > sshservice.MaxPins || pending > sshservice.MaxSourceIntents || completed > sshservice.MaxCompletions {
		return nil, fmt.Errorf("SSH namespace complete roster exceeds supported bounds")
	}
	if service != nil && (public == "" || public != service.PublicKey) {
		return nil, fmt.Errorf("SSH host key and stopped service public identity differ")
	}
	for name := range sealRecords {
		if _, ok := completionRecords[name]; ok {
			return nil, fmt.Errorf("SSH key has contradictory admitted/nonexecution receipts")
		}
	}
	for name, r := range intentRecords {
		if c, ok := sealRecords[name]; ok {
			a, _ := json.Marshal(r)
			b, _ := json.Marshal(c.Intent)
			if !bytes.Equal(a, b) {
				return nil, fmt.Errorf("paired SSH pending/nonexecution records differ")
			}
		}
		if c, ok := completionRecords[name]; ok {
			a, _ := json.Marshal(r)
			b, _ := json.Marshal(c.Intent)
			if !bytes.Equal(a, b) {
				return nil, fmt.Errorf("paired SSH pending/completion records differ")
			}
		}
	}
	return blockers, nil
}
func sshIntentProofName(r sshservice.SourceIntent) string {
	return "ssh-client-" + proofDigest([]byte(r.Source+"\x00"+r.Request.Pin.Target+"\x00"+r.Request.Key)) + ".json"
}

func proofDigest(raw []byte) string { v := sha256.Sum256(raw); return hex.EncodeToString(v[:]) }
