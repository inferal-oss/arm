package manager

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/providerpreparation"
)

// C1 values are validated observations, not a registry publisher, lease or
// entitlement. No production entrypoint calls this unused decoder.
type providerPairLeaseWire struct {
	Ref          providerpreparation.LeaseRef `json:"provider_preparation"`
	Clock        leaseclock.Deadline          `json:"elapsed_deadline"`
	Budget       providerPairBudgetWire       `json:"budget_binding"`
	Policies     []providerPairPolicyWire     `json:"budget_policy"`
	ID           string                       `json:"id"`
	Host         string                       `json:"host"`
	Owner        string                       `json:"owner"`
	Username     string                       `json:"owner_username,omitempty"`
	Request      providerPairRequestWire      `json:"request"`
	State        string                       `json:"state"`
	Created      time.Time                    `json:"created_at"`
	Expires      time.Time                    `json:"expires_at"`
	Revision     int                          `json:"revision"`
	Capabilities []string                     `json:"capabilities"`
}

// Independent closed wire shapes prevent a later model-field addition from
// silently widening this inert reader. Conversion to a model validation value
// is explicit and confers no admission/accounting authority.
type providerPairResourcesWire struct {
	CPU    float64 `json:"cpu"`
	Memory int64   `json:"memory_bytes"`
	Disk   int64   `json:"disk_bytes"`
}
type providerPairBudgetWire struct {
	Authority string    `json:"authority"`
	Owner     string    `json:"owner"`
	Selected  string    `json:"selected"`
	Path      []string  `json:"path"`
	Bound     time.Time `json:"bound_at"`
}
type providerPairPolicyWire struct {
	ID       string                 `json:"id"`
	Revision int64                  `json:"revision"`
	Limits   providerPairLimitsWire `json:"limits"`
}
type providerPairLimitsWire struct {
	CPU    *float64         `json:"cpu,omitempty"`
	Memory *int64           `json:"memory_bytes,omitempty"`
	Disk   *int64           `json:"disk_bytes,omitempty"`
	Pools  map[string]int64 `json:"pools,omitempty"`
}

type providerPairRequestWire struct {
	Budget    string                    `json:"budget,omitempty"`
	Host      string                    `json:"host"`
	Reason    string                    `json:"reason"`
	Project   string                    `json:"project,omitempty"`
	Agent     string                    `json:"agent,omitempty"`
	Resources providerPairResourcesWire `json:"resources"`
	TTL       time.Duration             `json:"ttl_ns"`
	Key       string                    `json:"idempotency_key"`
	Queue     bool                      `json:"queue"`
	Kind      string                    `json:"kind"`
}

func providerPairDecimal(s string, positive bool) (int64, error) {
	if s == "" || len(s) > 19 || (len(s) > 1 && s[0] == '0') {
		return 0, fmt.Errorf("noncanonical decimal")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("nondecimal scalar")
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || positive && n == 0 {
		return 0, fmt.Errorf("decimal range")
	}
	return n, nil
}
func providerPairUID(s string) bool {
	n, e := providerPairDecimal(s, false)
	return e == nil && n <= 1<<32-1 && len(s) <= 10
}
func providerPairDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func providerPairStamp(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	t, e := time.Parse(time.RFC3339Nano, s)
	return e == nil && !t.IsZero() && t.Year() >= 0 && t.Year() <= 9999 && t.UTC().Format(time.RFC3339Nano) == s
}

// Walk the bounded byte input before typed decoding. In particular, encoding/json
// must not silently truncate fixed arrays or fold aliased/duplicate field names.
func providerPairTokens(d *json.Decoder, raw []byte, depth, maxDepth int) error {
	if depth > maxDepth {
		return fmt.Errorf("pair JSON depth bound")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' && delim != '[' {
		return fmt.Errorf("unexpected pair delimiter")
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			before := d.InputOffset()
			key, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return fmt.Errorf("duplicate pair key")
			}
			seen[s] = true
			spelling := bytes.TrimSpace(raw[before:d.InputOffset()])
			spelling = bytes.TrimSpace(bytes.TrimPrefix(spelling, []byte(",")))
			canonical, _ := json.Marshal(s)
			if !bytes.Equal(spelling, canonical) {
				return fmt.Errorf("noncanonical pair key spelling")
			}
		}
		if err := providerPairTokens(d, raw, depth+1, maxDepth); err != nil {
			return err
		}
	}
	end, err := d.Token()
	if err != nil {
		return err
	}
	if end != json.Delim(map[json.Delim]rune{'{': '}', '[': ']'}[delim]) {
		return fmt.Errorf("pair delimiter mismatch")
	}
	return nil
}

func providerPairArray(raw []byte, limit int, exact bool) ([]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, e := d.Token()
	if e != nil || token != json.Delim('[') {
		return nil, fmt.Errorf("pair array required")
	}
	values := make([]json.RawMessage, 0, limit)
	for d.More() {
		if len(values) == limit {
			return nil, fmt.Errorf("pair array exceeds bound")
		}
		var v json.RawMessage
		if e = d.Decode(&v); e != nil {
			return nil, e
		}
		values = append(values, v)
	}
	if _, e = d.Token(); e != nil {
		return nil, e
	}
	if exact && len(values) != limit {
		return nil, fmt.Errorf("pair fixed array differs")
	}
	return values, nil
}

func providerPairShape(raw []byte, t reflect.Type, path string) error {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		if path == "$.parents[]" && t == reflect.TypeOf((*providerpreparation.ParentCharge)(nil)) {
			return nil
		}
		return fmt.Errorf("null pair field %s", path)
	}
	if t.Kind() == reflect.Pointer {
		return providerPairShape(raw, t.Elem(), path)
	}
	if t == reflect.TypeOf(time.Time{}) {
		var s string
		if json.Unmarshal(raw, &s) != nil || !providerPairStamp(s) {
			return fmt.Errorf("pair timestamp %s", path)
		}
		return nil
	}
	// This fixed profile has only integer CPU literals 0/1. Validate the raw
	// spelling before float64 decoding can round a different decimal to 1.
	if path == "$.request.resources.cpu" && !bytes.Equal(raw, []byte("0")) && !bytes.Equal(raw, []byte("1")) {
		return fmt.Errorf("noncanonical inert CPU quantity")
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return fmt.Errorf("pair object required")
		}
		allowed := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			name := tag[0]
			if name == "" || name == "-" || allowed[name] {
				return fmt.Errorf("invalid private pair schema")
			}
			allowed[name] = true
			v, ok := fields[name]
			if !ok {
				if len(tag) > 1 && tag[1] == "omitempty" {
					continue
				}
				return fmt.Errorf("missing pair field %s.%s", path, name)
			}
			if e := providerPairShape(v, f.Type, path+"."+name); e != nil {
				return e
			}
		}
		for name := range fields {
			if !allowed[name] {
				return fmt.Errorf("unknown pair field at %s", path)
			}
		}
	case reflect.Array:
		values, e := providerPairArray(raw, t.Len(), true)
		if e != nil {
			return e
		}
		for _, v := range values {
			if e = providerPairShape(v, t.Elem(), path+"[]"); e != nil {
				return e
			}
		}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			var s string
			if json.Unmarshal(raw, &s) != nil {
				return fmt.Errorf("pair byte string required")
			}
			limit := providerpreparation.MaxLeaseBytes
			if strings.HasSuffix(path, ".budget_bytes") {
				limit = providerpreparation.MaxBudgetBytes
			}
			if len(s) > base64.StdEncoding.EncodedLen(limit) || len(s)%4 != 0 {
				return fmt.Errorf("pair base64 size")
			}
			decodedBytes := len(s) / 4 * 3
			if strings.HasSuffix(s, "=") {
				decodedBytes--
			}
			if strings.HasSuffix(s, "==") {
				decodedBytes--
			}
			if decodedBytes > limit {
				return fmt.Errorf("pair decoded base64 size")
			}
			b, e := base64.StdEncoding.Strict().DecodeString(s)
			if e != nil || len(b) > limit || base64.StdEncoding.EncodeToString(b) != s {
				return fmt.Errorf("noncanonical pair base64")
			}
			encoded, _ := json.Marshal(s)
			if !bytes.Equal(raw, encoded) {
				return fmt.Errorf("escaped pair base64")
			}
			return nil
		}
		limit := providerpreparation.MaxParentCharges
		if path == "$.capabilities" {
			limit = 0
		}
		values, e := providerPairArray(raw, limit, false)
		if e != nil {
			return e
		}
		for _, v := range values {
			if e = providerPairShape(v, t.Elem(), path+"[]"); e != nil {
				return e
			}
		}
	case reflect.Map:
		// Inert profile has no custom pool claims or limits. A present empty map is
		// still data; arbitrary keys would require a separately reviewed profile.
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || len(fields) != 0 {
			return fmt.Errorf("nonempty pair policy map")
		}
	}
	return nil
}

// JSON escaped UTF-16 must denote Unicode scalars, rather than relying on
// encoding/json's replacement of unmatched surrogate escapes.
func providerPairUnicode(raw []byte) error {
	quoted := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return fmt.Errorf("unfinished pair string escape")
		}
		if raw[i] != 'u' {
			continue
		}
		if i+5 > len(raw) {
			return fmt.Errorf("short pair Unicode escape")
		}
		n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return e
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+11 > len(raw) || raw[i+5] != '\\' || raw[i+6] != 'u' {
				return fmt.Errorf("unpaired high surrogate")
			}
			low, e := strconv.ParseUint(string(raw[i+7:i+11]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return fmt.Errorf("unpaired high surrogate")
			}
			i += 10
		} else {
			if n >= 0xdc00 && n <= 0xdfff {
				return fmt.Errorf("unpaired low surrogate")
			}
			i += 4
		}
	}
	return nil
}

func decodeProviderPairJSON(raw []byte, out any, maxBytes, maxDepth int, canonical bool) error {
	if len(raw) == 0 || len(raw) > maxBytes || !utf8.Valid(raw) {
		return fmt.Errorf("pair JSON byte/UTF8 bound")
	}
	if e := providerPairUnicode(raw); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := providerPairTokens(d, raw, 0, maxDepth); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("trailing pair JSON")
	}
	t := reflect.TypeOf(out)
	if t == nil || t.Kind() != reflect.Pointer || reflect.ValueOf(out).IsNil() {
		return fmt.Errorf("private pair decode target required")
	}
	if e := providerPairShape(raw, t.Elem(), "$"); e != nil {
		return e
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	if canonical {
		encoded, e := json.Marshal(out)
		if e != nil || !bytes.Equal(encoded, raw) {
			return fmt.Errorf("noncanonical pair envelope encoding")
		}
	}
	return nil
}
