package agentstatus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"getarm.org/internal/agents"
)

const MaxStateBytes = 8 << 20
const MaxIdentities = 256
const MaxAttempts = 128

type Identity struct {
	Host    string         `json:"host"`
	Contact agents.Contact `json:"contact"`
}

func (i Identity) Key() string {
	b, _ := json.Marshal([]string{i.Host, i.Contact.ID, i.Contact.Session, i.Contact.Registration})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}

func RequestDigest(entry string, contact agents.Contact) string {
	raw, _ := json.Marshal(struct {
		ID      string
		Contact agents.Contact
	}{entry, contact})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

type Entry struct {
	ID            string    `json:"id"`
	Identity      Identity  `json:"identity"`
	FirstObserved time.Time `json:"first_observed_at"`
	LastObserved  time.Time `json:"last_observed_at"`
}
type Config struct {
	Revision            int     `json:"revision"`
	Enabled             bool    `json:"enabled"`
	Provider            string  `json:"provider"`
	Model               string  `json:"model"`
	CredentialEnv       string  `json:"credential_env"`
	MaxDailyRequests    int     `json:"max_daily_requests"`
	EstimatedAttemptUSD float64 `json:"estimated_attempt_usd"`
}

func DefaultConfig() Config {
	return Config{Provider: "openai", Model: CodexModel, MaxDailyRequests: 20, EstimatedAttemptUSD: .05}
}

var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

func (c Config) Validate() error {
	if c.Provider != "openai" && c.Provider != "anthropic" {
		return fmt.Errorf("unsupported summary provider")
	}
	if c.Provider == "openai" && c.Model != CodexModel || c.Provider == "anthropic" && c.Model != ClaudeModel {
		return fmt.Errorf("model must match the explicitly selected provider")
	}
	if c.Enabled && !envName.MatchString(c.CredentialEnv) || c.CredentialEnv != "" && !envName.MatchString(c.CredentialEnv) {
		return fmt.Errorf("explicit credential environment variable name required")
	}
	if c.MaxDailyRequests < 1 || c.MaxDailyRequests > 100 || !(c.EstimatedAttemptUSD >= .001 && c.EstimatedAttemptUSD <= 1) {
		return fmt.Errorf("daily limit requires 1–100 requests and estimated attempt budget .001–1 USD")
	}
	return nil
}

type Usage struct {
	Input        int     `json:"input_tokens,omitempty"`
	Output       int     `json:"output_tokens,omitempty"`
	EstimatedUSD float64 `json:"estimated_usd,omitempty"`
	Billing      string  `json:"billing"`
}
type Attempt struct {
	ID            string     `json:"id"`
	Key           string     `json:"key"`
	Digest        string     `json:"request_digest"`
	EntryID       string     `json:"entry_id"`
	Identity      Identity   `json:"identity"`
	Generation    int        `json:"generation"`
	Provider      string     `json:"provider"`
	Model         string     `json:"model"`
	State         string     `json:"state"`
	Created       time.Time  `json:"created_at"`
	Deadline      time.Time  `json:"reply_deadline"`
	MessageID     string     `json:"message_id,omitempty"`
	ReplyID       string     `json:"reply_id,omitempty"`
	ReplyAt       *time.Time `json:"reply_observed_at,omitempty"`
	Account       *Account   `json:"account,omitempty"`
	Generated     *time.Time `json:"generated_at,omitempty"`
	CallStarted   *time.Time `json:"call_started_at,omitempty"`
	ReservedBytes int        `json:"reserved_report_bytes,omitempty"`
	HTML          string     `json:"html,omitempty"`
	Usage         *Usage     `json:"usage,omitempty"`
	Error         string     `json:"error,omitempty"`
}
type State struct {
	Version    int       `json:"version"`
	Config     Config    `json:"config"`
	Entries    []Entry   `json:"entries"`
	Attempts   []Attempt `json:"attempts"`
	Day        string    `json:"day"`
	DailyCalls int       `json:"daily_calls"`
}

func NewState() State {
	return State{Version: Version, Config: DefaultConfig(), Entries: []Entry{}, Attempts: []Attempt{}}
}
func (s *State) Observe(host string, contacts []agents.Contact, now time.Time) (bool, error) {
	full := false
	for _, c := range contacts {
		if c.Registration == "" || c.Session == "" {
			continue
		}
		identity := Identity{host, c}
		id := identity.Key()
		found := false
		for j := range s.Entries {
			if s.Entries[j].ID == id {
				s.Entries[j].Identity = identity
				s.Entries[j].LastObserved = now
				found = true
				break
			}
		}
		if !found {
			if len(s.Entries) >= MaxIdentities {
				full = true
				continue
			}
			s.Entries = append(s.Entries, Entry{id, identity, now, now})
		}
	}
	return full, nil
}
func (s *State) Attempt(id string) *Attempt {
	for i := range s.Attempts {
		if s.Attempts[i].ID == id {
			return &s.Attempts[i]
		}
	}
	return nil
}
func (s *State) Latest(entry string) *Attempt {
	for i := len(s.Attempts) - 1; i >= 0; i-- {
		if s.Attempts[i].EntryID == entry {
			return &s.Attempts[i]
		}
	}
	return nil
}
func (s *State) Report(entry string) *Attempt {
	for i := len(s.Attempts) - 1; i >= 0; i-- {
		if s.Attempts[i].EntryID == entry && s.Attempts[i].HTML != "" {
			return &s.Attempts[i]
		}
	}
	return nil
}
func Pending(state string) bool {
	return state == "pending_delivery" || state == "awaiting_reply" || state == "generating" || state == "unknown"
}
func (s *State) Prune(now time.Time) {
	latest := map[string]string{}
	for _, a := range s.Attempts {
		if a.HTML != "" {
			latest[a.EntryID] = a.ID
		}
	}
	kept := make([]Attempt, 0, len(s.Attempts))
	for _, a := range s.Attempts {
		if Pending(a.State) || latest[a.EntryID] == a.ID || now.Sub(a.Created) < 7*24*time.Hour {
			kept = append(kept, a)
		}
	}
	s.Attempts = kept
}
