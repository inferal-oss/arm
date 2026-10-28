// Package nodeavailability holds owner-declared advisory availability. A profile
// never changes trust, resource admission, expiry, execution or settlement.
package nodeavailability

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"getarm.org/internal/mesh"
)

const MetadataKey = "node_availability_v1"
const Capability = "node-availability-v1"
const MaxDocumentBytes = 8 << 10

type Update struct {
	Key              string `json:"key"`
	ExpectedRevision uint64 `json:"expected_revision"`
	Profile          string `json:"profile"`
	Reason           string `json:"reason"`
}
type Document struct {
	Schema     int       `json:"schema"`
	Host       string    `json:"host"`
	Owner      string    `json:"owner"`
	Revision   uint64    `json:"revision"`
	Profile    string    `json:"profile"`
	Reason     string    `json:"reason"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastUpdate Update    `json:"last_update"`
}
type Declaration struct {
	Profile   string     `json:"profile"`
	Revision  uint64     `json:"revision"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}
type Report struct {
	Schema        int         `json:"schema"`
	Host          string      `json:"host"`
	OwnerDeclared Declaration `json:"owner_declared"`
	ObservedAt    time.Time   `json:"observed_at"`
}

func ValidProfile(profile string) bool {
	return profile == "unspecified" || profile == "stable" || profile == "intermittent"
}
func (u Update) Validate() error {
	if !mesh.ValidKey(u.Key) || !ValidProfile(u.Profile) || u.Reason == "" || len(u.Reason) > 1024 || !utf8.ValidString(u.Reason) || strings.TrimSpace(u.Reason) != u.Reason || strings.ContainsRune(u.Reason, 0) {
		return fmt.Errorf("availability update needs bounded key, exact profile and trimmed reason of1–1024bytes")
	}
	return nil
}

// Exact top-level spelling and duplicate rejection complement encoding/json's
// type checks; DisallowUnknownFields alone accepts case aliases and duplicates.
func decodeObject(raw []byte, out any, keys ...string) error {
	if len(raw) > MaxDocumentBytes || !utf8.Valid(raw) {
		return fmt.Errorf("availability JSON exceeds bounds or is not UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("availability object required")
	}
	allowed, seen := map[string]bool{}, map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || !allowed[key] || seen[key] {
			return fmt.Errorf("unknown, aliased or duplicate availability field")
		}
		seen[key] = true
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("null availability field")
		}
	}
	if _, err := d.Token(); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("one availability object required")
	}
	if len(seen) != len(allowed) {
		return fmt.Errorf("required availability field absent")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}

func DecodeUpdate(raw []byte) (Update, error) {
	var u Update
	if err := decodeObject(raw, &u, "key", "expected_revision", "profile", "reason"); err != nil {
		return u, err
	}
	return u, u.Validate()
}

func DecodeHost(raw []byte) (string, error) {
	var request struct {
		Host string `json:"host"`
	}
	if err := decodeObject(raw, &request, "host"); err != nil {
		return "", err
	}
	id, err := hex.DecodeString(request.Host)
	if err != nil || len(id) != 16 || strings.ToLower(request.Host) != request.Host {
		return "", fmt.Errorf("exact stable host ID required")
	}
	return request.Host, nil
}

// Targets use the existing native registry resolver, never a private alias map.
// Browser node contexts still pass their exact selected canonical ID.
func DecodeReadTarget(raw []byte) (string, error) {
	var request struct {
		Host string `json:"host"`
	}
	if err := decodeObject(raw, &request, "host"); err != nil {
		return "", err
	}
	if request.Host == "" || len(request.Host) > 128 || strings.TrimSpace(request.Host) != request.Host || strings.ContainsRune(request.Host, 0) {
		return "", fmt.Errorf("bounded registered host target required")
	}
	return request.Host, nil
}

// DecodeDocument accepts only the exact canonical private metadata producer.
// Missing metadata is handled separately and never creates a startup write.
func DecodeDocument(raw []byte, host, owner string) (Document, error) {
	var d Document
	if err := decodeObject(raw, &d, "schema", "host", "owner", "revision", "profile", "reason", "updated_at", "last_update"); err != nil {
		return d, err
	}
	canonical, err := json.Marshal(d)
	if err != nil || !bytes.Equal(raw, canonical) {
		return d, fmt.Errorf("availability metadata is not the exact canonical document")
	}
	id, err := hex.DecodeString(host)
	if err != nil || len(id) != 16 || strings.ToLower(host) != host || owner == "" || d.Schema != 1 || d.Host != host || d.Owner != owner || d.Revision < 1 || d.UpdatedAt.IsZero() || d.UpdatedAt.Location() != time.UTC || !ValidProfile(d.Profile) || d.LastUpdate.ExpectedRevision != d.Revision-1 || d.LastUpdate.Profile != d.Profile || d.LastUpdate.Reason != d.Reason {
		return d, fmt.Errorf("availability metadata differs from local authority or schema")
	}
	if err := d.LastUpdate.Validate(); err != nil {
		return d, err
	}
	return d, nil
}

func (d Document) Report(host string) Report {
	report := Report{Schema: 1, Host: host, OwnerDeclared: Declaration{Profile: "unspecified"}, ObservedAt: time.Now().UTC()}
	if d.Revision > 0 {
		stamp := d.UpdatedAt
		report.OwnerDeclared = Declaration{Profile: d.Profile, Revision: d.Revision, UpdatedAt: &stamp}
	}
	return report
}

func DecodeReport(raw []byte, host string) (Report, error) {
	var report Report
	if err := decodeObject(raw, &report, "schema", "host", "owner_declared", "observed_at"); err != nil {
		return report, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return report, err
	}
	keys := []string{"profile", "revision"}
	if report.OwnerDeclared.UpdatedAt != nil {
		keys = append(keys, "updated_at")
	}
	var declaration Declaration
	if err := decodeObject(fields["owner_declared"], &declaration, keys...); err != nil {
		return report, err
	}
	if report.Schema != 1 || report.Host != host || report.ObservedAt.IsZero() || !ValidProfile(declaration.Profile) || (declaration.Revision == 0 && (declaration.Profile != "unspecified" || declaration.UpdatedAt != nil)) || (declaration.Revision > 0 && (declaration.UpdatedAt == nil || declaration.UpdatedAt.IsZero())) {
		return report, fmt.Errorf("availability report differs from selected host or supported schema")
	}
	return report, nil
}
