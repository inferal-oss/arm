// Package customnames validates optional, local presentation metadata.
// Names never establish resource existence, ownership, visibility or authority.
package customnames

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"unicode/utf8"

	"getarm.org/internal/readablename"
)

const MetadataKey = "custom_names_v1"
const MaxBytes = 512 << 10
const MaxEntries = 1024

type Document struct {
	Version  int     `json:"version"`
	Owner    string  `json:"owner"`
	Host     string  `json:"host"`
	Revision int64   `json:"revision"`
	Entries  []Entry `json:"entries"`
}

// BindingLease distinguishes a leased port incarnation from its reusable
// socket locator. Callers must independently verify the reservation owner.
type Entry struct {
	Kind          string `json:"kind"`
	AuthorityHost string `json:"authority_host"`
	ID            string `json:"id"`
	BindingLease  string `json:"binding_lease"`
	Name          string `json:"name"`
	Active        bool   `json:"active"`
}

var (
	ErrInvalid          = errors.New("invalid custom name metadata")
	ErrRevisionConflict = errors.New("custom name revision changed")
	ErrNameConflict     = errors.New("custom name is reserved")
	ErrNotFound         = errors.New("exact custom name binding does not exist")
	ErrLimit            = errors.New("custom name metadata bound reached; explicitly clear unused reservations")
)

func invalid(detail string) error { return fmt.Errorf("%w: %s", ErrInvalid, detail) }

func exactID(id string) bool {
	if len(id) != 32 || strings.ToLower(id) != id {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func validOwner(owner string) bool {
	n, err := strconv.ParseUint(owner, 10, 32)
	return err == nil && strconv.FormatUint(n, 10) == owner
}

func asciiAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func ValidateName(name string) error {
	if len(name) < 1 || len(name) > 128 || !asciiAlnum(name[0]) {
		return invalid("name must start with an ASCII letter/digit and contain 1..128 bytes")
	}
	for i := 1; i < len(name); i++ {
		if !asciiAlnum(name[i]) && name[i] != '.' && name[i] != '_' && name[i] != '-' {
			return invalid("name contains unsupported characters")
		}
	}
	lower := strings.ToLower(name)
	if lower == "localhost" || exactID(lower) || strings.HasPrefix(lower, "pool_") && exactID(strings.TrimPrefix(lower, "pool_")) || readablename.LooksGeneratedLabel(lower) {
		return invalid("name is reserved for an ID or generated label")
	}
	return nil
}

func ValidateBinding(e Entry) error {
	if !exactID(e.AuthorityHost) {
		return invalid("invalid authority host")
	}
	switch e.Kind {
	case "host":
		if e.ID != e.AuthorityHost || e.BindingLease != "" {
			return invalid("host binding differs")
		}
	case "lease":
		if !exactID(e.ID) || e.BindingLease != "" {
			return invalid("invalid lease binding")
		}
	case "pool":
		if !strings.HasPrefix(e.ID, "pool_") || !exactID(strings.TrimPrefix(e.ID, "pool_")) || e.BindingLease != "" {
			return invalid("invalid pool binding")
		}
	case "resource":
		if len(e.ID) > 256 {
			return invalid("resource ID exceeds bound")
		}
		for _, kind := range []string{"cpu", "memory", "disk"} {
			if e.ID == e.AuthorityHost+"/"+kind {
				if e.BindingLease != "" {
					return invalid("persistent resource has a lease incarnation")
				}
				return nil
			}
		}
		prefix := e.AuthorityHost + "/ports/"
		if !strings.HasPrefix(e.ID, prefix) || !exactID(e.BindingLease) {
			return invalid("port resource needs its exact reservation lease")
		}
		parts := strings.SplitN(strings.TrimPrefix(e.ID, prefix), "/", 3)
		if len(parts) != 3 || len(parts[0]) == 0 || len(parts[0]) > 128 || !oneOf(parts[1], "tcp4", "tcp6", "udp4", "udp6") {
			return invalid("invalid port resource locator")
		}
		for _, c := range parts[0] {
			if c < 33 || c > 126 {
				return invalid("invalid port namespace")
			}
		}
		address, port, err := net.SplitHostPort(parts[2])
		ip := net.ParseIP(address)
		p, pErr := strconv.Atoi(port)
		if err != nil || ip == nil || pErr != nil || p < 1 || p > 65535 || strconv.Itoa(p) != port || ip.String() != address || net.JoinHostPort(address, port) != parts[2] || strings.HasSuffix(parts[1], "4") != (ip.To4() != nil) {
			return invalid("invalid port address or number")
		}
	default:
		return invalid("unsupported resource kind")
	}
	return nil
}

func oneOf(s string, choices ...string) bool {
	for _, choice := range choices {
		if s == choice {
			return true
		}
	}
	return false
}

// Host selectors span the whole source-local registry, so host names share
// one namespace. Other resource names belong to a selected host authority.
func nameNamespace(e Entry) string {
	if e.Kind == "host" {
		return "host\x00"
	}
	return e.Kind + "\x00" + e.AuthorityHost + "\x00"
}

func SameBinding(a, b Entry) bool {
	return a.Kind == b.Kind && a.AuthorityHost == b.AuthorityHost && a.ID == b.ID && a.BindingLease == b.BindingLease
}
func ActiveName(doc Document, binding Entry) string {
	for _, e := range doc.Entries {
		if e.Active && SameBinding(e, binding) {
			return e.Name
		}
	}
	return ""
}

func validateDocument(doc Document, owner, host string, virtual bool) error {
	if !validOwner(owner) || !exactID(host) || doc.Version != 1 || doc.Owner != owner || doc.Host != host || doc.Revision < 1 && !(virtual && doc.Revision == 0 && len(doc.Entries) == 0) || doc.Entries == nil {
		return invalid("invalid document authority, version, revision or entries")
	}
	if len(doc.Entries) > MaxEntries {
		return ErrLimit
	}
	names, active := map[string]bool{}, map[string]bool{}
	for _, e := range doc.Entries {
		if err := ValidateBinding(e); err != nil {
			return err
		}
		if err := ValidateName(e.Name); err != nil {
			return err
		}
		nameKey := nameNamespace(e) + strings.ToLower(e.Name)
		bindingKey := e.Kind + "\x00" + e.AuthorityHost + "\x00" + e.ID + "\x00" + e.BindingLease
		if names[nameKey] || e.Active && active[bindingKey] {
			return invalid("duplicate name reservation or active binding")
		}
		names[nameKey] = true
		if e.Active {
			active[bindingKey] = true
		}
	}
	raw, err := json.Marshal(doc)
	if err != nil || len(raw) > MaxBytes {
		return ErrLimit
	}
	return nil
}

// Decode accepts absence as a virtual empty revision; it never repairs bytes.
// SQL callers must bound the raw TEXT byte length before materializing it.
func Decode(raw []byte, owner, host string) (Document, error) {
	zero := Document{}
	if !validOwner(owner) || !exactID(host) {
		return zero, invalid("invalid expected authority")
	}
	if raw == nil {
		return Document{Version: 1, Owner: owner, Host: host, Entries: []Entry{}}, nil
	}
	if len(raw) > MaxBytes {
		return zero, ErrLimit
	}
	if !utf8.Valid(raw) {
		return zero, invalid("invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := uniqueJSON(d, 0); err != nil {
		return zero, err
	}
	if _, err := d.Token(); err != io.EOF {
		return zero, invalid("trailing JSON")
	}
	object, err := exactObject(raw, "version", "owner", "host", "revision", "entries")
	if err != nil {
		return zero, err
	}
	var entries []json.RawMessage
	if err = json.Unmarshal(object["entries"], &entries); err != nil || entries == nil {
		return zero, invalid("entries must be an array")
	}
	if len(entries) > MaxEntries {
		return zero, ErrLimit
	}
	for _, entry := range entries {
		if _, err = exactObject(entry, "kind", "authority_host", "id", "binding_lease", "name", "active"); err != nil {
			return zero, err
		}
	}
	var doc Document
	if err = json.Unmarshal(raw, &doc); err != nil {
		return zero, invalid("unsupported field type")
	}
	if err = validateDocument(doc, owner, host, false); err != nil {
		return zero, err
	}
	return doc, nil
}

func exactObject(raw []byte, fields ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil || len(object) != len(fields) {
		return nil, invalid("object has unknown or missing fields")
	}
	for _, field := range fields {
		if v, ok := object[field]; !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, invalid("required canonical field is missing/null")
		}
	}
	return object, nil
}

func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 8 {
		return invalid("excessive JSON depth")
	}
	token, err := d.Token()
	if err != nil {
		return invalid("malformed JSON")
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return invalid("malformed object")
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return invalid("duplicate object key")
			}
			seen[s] = true
			if len(seen) > 7 {
				return invalid("object exceeds closed field bound")
			}
			if err = uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		count := 0
		for d.More() {
			if count >= MaxEntries {
				return ErrLimit
			}
			count++
			if err = uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return invalid("unexpected delimiter")
	}
	_, err = d.Token()
	if err != nil {
		return invalid("malformed container")
	}
	return nil
}

// Mutate changes one complete validated document in memory. A storage owner
// must compare its original bytes and commit this result plus audit atomically.
func Mutate(doc Document, operation string, target Entry, expectedRevision int64) (Document, bool, error) {
	if err := validateDocument(doc, doc.Owner, doc.Host, true); err != nil {
		return Document{}, false, err
	}
	if doc.Revision != expectedRevision {
		return Document{}, false, ErrRevisionConflict
	}
	if err := ValidateBinding(target); err != nil {
		return Document{}, false, err
	}
	if err := ValidateName(target.Name); err != nil {
		return Document{}, false, err
	}
	if operation != "set" && operation != "clear" {
		return Document{}, false, invalid("unsupported name operation")
	}
	result := doc
	result.Entries = append([]Entry{}, doc.Entries...)
	match := -1
	for i, e := range result.Entries {
		if nameNamespace(e) == nameNamespace(target) && strings.EqualFold(e.Name, target.Name) {
			if !SameBinding(e, target) || e.Name != target.Name {
				return Document{}, false, ErrNameConflict
			}
			match = i
		}
	}
	if operation == "set" && match >= 0 && result.Entries[match].Active {
		return doc, false, nil
	}
	if doc.Revision == math.MaxInt64 {
		return Document{}, false, ErrLimit
	}
	if operation == "clear" {
		if match < 0 {
			return Document{}, false, ErrNotFound
		}
		result.Entries = append(result.Entries[:match], result.Entries[match+1:]...)
	} else {
		for i, e := range result.Entries {
			if SameBinding(e, target) {
				result.Entries[i].Active = false
			}
		}
		if match >= 0 {
			result.Entries[match].Active = true
		} else {
			if len(result.Entries) >= MaxEntries {
				return Document{}, false, ErrLimit
			}
			target.Active = true
			result.Entries = append(result.Entries, target)
		}
	}
	result.Revision++
	raw, err := json.Marshal(result)
	if err != nil {
		return Document{}, false, err
	}
	if len(raw) > MaxBytes {
		return Document{}, false, ErrLimit
	}
	return result, true, nil
}
