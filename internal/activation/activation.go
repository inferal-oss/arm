// Package activation records explicit native activation-message delivery. These
// records confer no ARM enrollment, messaging contact or resource authority.
package activation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"getarm.org/internal/agentsessions"
	"getarm.org/internal/local"
	"github.com/google/uuid"
)

type Plan struct {
	Review         string                `json:"review_sha256"`
	Project        string                `json:"project"`
	PolicyRevision int                   `json:"policy_revision"`
	Session        agentsessions.Session `json:"recipient"`
	Message        string                `json:"message"`
	Delivery       string                `json:"delivery"`
	Binary         string                `json:"native_binary,omitempty"`
	BinarySHA      string                `json:"native_binary_sha256,omitempty"`
}

func Seal(p Plan, policy any) Plan {
	p.Review = ""
	stable := p
	stable.Session.Updated = time.Time{}
	stable.Session.Activity = ""
	stable.Session.Evidence = ""
	b, _ := json.Marshal(struct {
		Plan   Plan
		Policy any
	}{stable, policy})
	h := sha256.Sum256(b)
	p.Review = hex.EncodeToString(h[:])
	return p
}
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func Message(prefix, project string, revision int, s agentsessions.Session) string {
	selectors := " --project " + Quote(project) + " --cwd " + Quote(s.CWD) + " --adapter " + Quote(s.Adapter) + " --session " + Quote(s.ID)
	start := "The original session start is unknown. Omit --started-at unless you can establish the original start; never substitute the current time."
	identifySelectors := selectors
	if !s.Started.IsZero() {
		start = "The identify command includes the native reported original start. Confirm it belongs to this existing session before using it; otherwise omit --started-at. Never substitute the current time."
		identifySelectors += " --started-at " + Quote(s.Started.Format(time.RFC3339Nano))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Activate ARM coordination for this existing session only.\nAdapter: %s\nSession UUID: %s\nWorking directory: %s\nReviewed ARM project: %s; policy revision: %d.\n\n", s.Adapter, s.ID, s.CWD, project, revision)
	fmt.Fprintf(&b, "Read the installed ARM instructions and run:\n%s rollout check%s --json\n\n", prefix, selectors)
	fmt.Fprintln(&b, "Stop and report if the project/revision differs, discovery is disabled, or the session identity/directory does not match. Do not change rollout policy, invite yourself, create or fork a session, or bypass pause/revocation/new-session eligibility. Honor any task-supplied native client-project ID consistently; never guess one.")
	fmt.Fprintf(&b, "\nIf discovery is enabled, report your actual bounded resource needs (describe idle or unknown needs honestly). Replace the needs placeholder below with your own description.\n%s\n%s rollout identify%s --needs '<describe your actual resource needs>' --json\n\n", start, prefix, identifySelectors)
	fmt.Fprintf(&b, "Identification alone does not enroll you. Only if eligible under the reviewed policy, use the returned identity and current policy revision to acknowledge:\n%s rollout ack%s --identity '<returned identity>' --policy-revision %d --json\n\n", prefix, selectors, revision)
	fmt.Fprint(&b, "Proceed with the installed operating protocol only when coordinate is true. Do not register contacts, start a backend or reserve resources merely because this message arrived. Preserve all existing jobs, leases, ownership and cleanup/accounting obligations. Report whether you identified, enrolled, or were blocked and why.")
	return b.String()
}

func BinaryHash(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	fi, e := f.Stat()
	if e != nil || !fi.Mode().IsRegular() || fi.Mode()&0111 == 0 || fi.Size() > 256<<20 {
		return "", fmt.Errorf("native queue executable is unsupported")
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, (256<<20)+1))
	if e != nil || n > 256<<20 {
		return "", fmt.Errorf("native executable hash limit exceeded")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type Record struct {
	Key            string    `json:"key"`
	Review         string    `json:"review_sha256"`
	Project        string    `json:"project"`
	Adapter        string    `json:"adapter"`
	Session        string    `json:"session"`
	State          string    `json:"state"`
	At             time.Time `json:"at"`
	Message        string    `json:"message"`
	CWD            string    `json:"cwd"`
	Generation     string    `json:"generation"`
	PolicyRevision int       `json:"policy_revision"`
}
type ledger struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}
type Store struct{ Home string }

func digest64(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, e := hex.DecodeString(s)
	return e == nil
}

func (s Store) save(l ledger) error {
	b, e := json.MarshalIndent(l, "", "  ")
	if e != nil {
		return e
	}
	if len(b)+1 > 1<<20 {
		return fmt.Errorf("activation history byte capacity reached; existing receipts were retained")
	}
	if e := local.AtomicJSON(filepath.Join(s.Home, "rollout-activations.json"), l); e != nil {
		return e
	}
	d, e := os.Open(s.Home)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func validKey(s string) bool {
	if len(s) < 1 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func (s Store) read() (ledger, error) {
	out := ledger{Version: 1, Records: []Record{}}
	r, e := os.OpenRoot(s.Home)
	if os.IsNotExist(e) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	defer r.Close()
	f, e := r.OpenFile("rollout-activations.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(e) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	defer f.Close()
	fi, e := f.Stat()
	if e != nil || !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
		return out, fmt.Errorf("activation history is not a bounded regular ledger")
	}
	d := json.NewDecoder(io.LimitReader(f, (1<<20)+1))
	d.DisallowUnknownFields()
	if e = d.Decode(&out); e != nil {
		return out, e
	}
	if d.Decode(new(any)) != io.EOF || out.Version != 1 || len(out.Records) > 256 {
		return out, fmt.Errorf("unsupported activation history")
	}
	seen := map[string]bool{}
	for _, r := range out.Records {
		_, idErr := uuid.Parse(r.Session)
		if !validKey(r.Key) || seen[r.Key] || !digest64(r.Review) || !digest64(r.Generation) || idErr != nil || !filepath.IsAbs(r.CWD) || r.Message == "" || len(r.Message) > 65536 || r.PolicyRevision < 1 || r.At.IsZero() || r.Adapter != "codex" {
			return out, fmt.Errorf("invalid activation history")
		}
		switch r.State {
		case "pending", "queued", "failed", "unknown":
		default:
			return out, fmt.Errorf("invalid activation outcome")
		}
		seen[r.Key] = true
	}
	return out, nil
}
func (s Store) Lookup(key string) (Record, error) {
	if !validKey(key) {
		return Record{}, fmt.Errorf("key must be 1..128 letters, digits, hyphens or underscores")
	}
	l, e := s.read()
	if e != nil {
		return Record{}, e
	}
	for _, r := range l.Records {
		if r.Key == key {
			return r, nil
		}
	}
	return Record{}, fmt.Errorf("activation key is not recorded")
}

// Send durably records a key before native start. Existing keys never execute
// again, including pending/unknown results after interruption or process loss.
func (s Store) Send(ctx context.Context, p Plan, key string, revalidate func() (Plan, error)) (Record, error) {
	if !validKey(key) || p.Delivery != "queue" || p.Session.Adapter != "codex" || !digest64(p.Review) || !digest64(p.BinarySHA) || !digest64(p.Session.Generation) || p.Message == "" || len(p.Message) > 65536 || !filepath.IsAbs(p.Binary) || revalidate == nil {
		return Record{}, fmt.Errorf("reviewed exact Codex queue delivery and a bounded key are required")
	}
	if e := ctx.Err(); e != nil {
		return Record{}, e
	}
	lock, e := local.Lock(s.Home, "rollout-activation.lock")
	if e != nil {
		return Record{}, e
	}
	defer lock.Close()
	l, e := s.read()
	if e != nil {
		return Record{}, e
	}
	for _, r := range l.Records {
		if r.Key == key {
			if r.Review != p.Review {
				return r, fmt.Errorf("activation key is bound to a different reviewed request")
			}
			return r, nil
		}
	}
	if len(l.Records) >= 256 {
		return Record{}, fmt.Errorf("activation history capacity reached; no receipts were discarded")
	}
	policyLock, e := local.Lock(s.Home, "rollout.lock")
	if e != nil {
		return Record{}, e
	}
	defer policyLock.Close()
	fresh, e := revalidate()
	if e != nil {
		return Record{}, e
	}
	if fresh.Review != p.Review {
		return Record{}, fmt.Errorf("recipient, policy, instructions, message or delivery executable changed; preview again")
	}
	currentHash, e := BinaryHash(p.Binary)
	if e != nil || currentHash != p.BinarySHA {
		return Record{}, fmt.Errorf("native queue executable changed; preview again")
	}
	if e = ctx.Err(); e != nil {
		return Record{}, e
	}
	r := Record{Key: key, Review: p.Review, Project: p.Project, Adapter: p.Session.Adapter, Session: p.Session.ID, State: "pending", At: time.Now().UTC(), Message: p.Message, CWD: p.Session.CWD, Generation: p.Session.Generation, PolicyRevision: p.PolicyRevision}
	l.Records = append(l.Records, r)
	if e = s.save(l); e != nil {
		return r, e
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.Binary, "queue", "--thread", p.Session.ID, "--message", p.Message)
	cmd.Dir = p.Session.CWD
	cmd.Env = agentsessions.Environment("CODEX_HOME", p.Session.Home)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		r.State = "failed"
	} else if e = cmd.Wait(); e != nil {
		r.State = "unknown"
	} else {
		r.State = "queued"
	}
	l.Records[len(l.Records)-1] = r
	if saveErr := s.save(l); saveErr != nil {
		r.State = "unknown"
		return r, fmt.Errorf("activation receipt persistence is uncertain; inspect the original key")
	}
	if e != nil {
		return r, fmt.Errorf("native delivery ended with %s outcome; inspect the original activation key", r.State)
	}
	return r, nil
}
