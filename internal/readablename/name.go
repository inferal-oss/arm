// Package readablename provides stable display aliases. Aliases are neither
// identities nor capabilities; callers must resolve against a complete,
// authorized inventory and refuse ambiguity before using the original ID.
package readablename

import (
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"errors"
	"strings"
)

const (
	Host     = "host"
	Lease    = "lease"
	Pool     = "pool"
	Resource = "resource"
)

//go:embed eff_large_wordlist.txt
var vocabulary string

// Keep the complete source order fixed: changes would rename existing objects.
var words = func() []string {
	fields := strings.Fields(vocabulary)
	out := make([]string, len(fields)/2)
	for i := range out {
		out[i] = fields[2*i+1]
	}
	return out
}()

// FromID v1 maps a domain and the exact UTF-8 ID bytes to four EFF words. The
// length-delimited domain prevents separator injection across domains. Four
// independent 64-bit digest chunks select 7,776 words each (~51.7 bits total).
// This deliberately short display alias can collide; never guess on a match.
// Empty IDs have no alias. There is no randomness, state, locale or hostname.
func FromID(domain, id string) string {
	if id == "" {
		return ""
	}
	h := sha256.New()
	h.Write([]byte("arm-readable-name-v1\x00"))
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(domain)))
	h.Write(size[:])
	h.Write([]byte(domain))
	h.Write([]byte(id))
	digest := h.Sum(nil)
	var parts [4]string
	for i := range parts {
		parts[i] = words[binary.BigEndian.Uint64(digest[i*8:])%uint64(len(words))]
	}
	return strings.Join(parts[:], "-")
}

type Candidate struct{ ID, Name string }

var (
	ErrNotFound   = errors.New("name is not in the authorized inventory; use its stable ID")
	ErrAmbiguous  = errors.New("name is ambiguous; use its stable ID")
	ErrIncomplete = errors.New("complete authorized inventory is unavailable; use a stable ID")
)

// Resolve prefers an exact original ID, then requires one distinct ID matching
// either the supplied name or generated alias. A partial inventory proves no
// uniqueness. Include tombstones when the domain can retain old obligations.
func Resolve(domain, target string, candidates []Candidate, complete bool) (string, error) {
	if !complete {
		return "", ErrIncomplete
	}
	for _, c := range candidates {
		if c.ID != "" && c.ID == target {
			return c.ID, nil
		}
	}
	found := ""
	for _, c := range candidates {
		if c.ID == "" || target == "" || (target != c.Name && target != FromID(domain, c.ID)) {
			continue
		}
		if found != "" && found != c.ID {
			return "", ErrAmbiguous
		}
		found = c.ID
	}
	if found == "" {
		return "", ErrNotFound
	}
	return found, nil
}
