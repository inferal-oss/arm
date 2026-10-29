// Package resident is a private, unconnected prototype of a resident dispatch
// boundary. It owns no Manager, native job or public startup path. In particular,
// closing a worker channel cannot close a Manager or cancel a resident job.
package resident

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"
	"unicode"
)

const (
	Protocol       = 1
	MaxRequest     = 64 << 10
	MaxResponse    = 256 << 10
	MaxReceipts    = 128
	MaxGenerations = 2 // Including sealed owners; no native exit is inferred.
)

var hexID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var digestID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var versionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

type Identity struct {
	Host       string `json:"host"`
	Owner      string `json:"owner"`
	Generation string `json:"generation"`
}

type WorkerIdentity struct {
	Generation string `json:"generation"`
	SHA256     string `json:"sha256"`
	Version    string `json:"version"`
}

// The prototype admits only an explicitly provisioned local fixture principal.
// It does not translate browser/mesh claims into native authority.
type Principal struct {
	Kind  string `json:"kind"`
	Owner string `json:"owner"`
}

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Failure) Error() string   { return e.Code + ": " + e.Message }
func failure(code string) *Failure { return &Failure{Code: code, Message: code} }

type Request struct {
	Protocol   int             `json:"protocol"`
	Resident   Identity        `json:"resident"`
	Worker     WorkerIdentity  `json:"worker"`
	Capability string          `json:"capability"`
	ID         uint64          `json:"id"`
	Operation  string          `json:"operation"`
	Key        string          `json:"key,omitempty"`
	Params     json.RawMessage `json:"params"`
}

type Reply struct {
	Protocol int             `json:"protocol"`
	Resident Identity        `json:"resident"`
	Worker   WorkerIdentity  `json:"worker"`
	ID       uint64          `json:"id"`
	Key      string          `json:"key,omitempty"`
	Outcome  string          `json:"outcome"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    *Failure        `json:"error,omitempty"`
}

type bootstrap struct {
	Protocol    int            `json:"protocol"`
	Resident    Identity       `json:"resident"`
	Worker      WorkerIdentity `json:"worker"`
	Capability  string         `json:"capability"`
	ClientToken string         `json:"client_token"`
}

type hello struct {
	Protocol   int            `json:"protocol"`
	Resident   Identity       `json:"resident"`
	Worker     WorkerIdentity `json:"worker"`
	Capability string         `json:"capability"`
}

func secret() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func decode(raw []byte, value any) error {
	shape := json.NewDecoder(bytes.NewReader(raw))
	if err := jsonShape(shape, 0, reflect.TypeOf(value)); err != nil {
		return err
	}
	if _, err := shape.Token(); err != io.EOF {
		return fmt.Errorf("trailing frame data")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing frame data")
	}
	return nil
}

// Reject duplicate/ambiguous keys and bound nesting before typed decoding.
func jsonShape(d *json.Decoder, depth int, target reflect.Type) error {
	if depth > 32 {
		return failure("json_depth")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	target = jsonTarget(target)
	switch delim {
	case '{':
		var fields map[string]reflect.Type
		var element reflect.Type
		if target != nil {
			switch target.Kind() {
			case reflect.Struct:
				fields = jsonFields(target)
			case reflect.Map:
				element = target.Elem()
			}
		}
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return failure("json_duplicate_key")
			}
			seen[name] = true
			child := element
			if fields != nil {
				var present bool
				child, present = fields[name]
				// encoding/json accepts case-folded aliases for typed fields.
				// Require their actual wire name before its matching can occur.
				if !present {
					return failure("json_field_name")
				}
			}
			if err = jsonShape(d, depth+1, child); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return failure("json_invalid")
		}
	case '[':
		var element reflect.Type
		if target != nil && (target.Kind() == reflect.Slice || target.Kind() == reflect.Array) {
			element = target.Elem()
		}
		for d.More() {
			if err := jsonShape(d, depth+1, element); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return failure("json_invalid")
		}
	default:
		return failure("json_invalid")
	}
	return nil
}

var jsonUnmarshaller = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

// RawMessage, interfaces and custom JSON representations are opaque. Map keys
// remain case-sensitive; only their typed values get a struct-field schema.
func jsonTarget(t reflect.Type) reflect.Type {
	for t != nil {
		if t.Implements(jsonUnmarshaller) || (t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(jsonUnmarshaller)) {
			return nil
		}
		if t.Kind() == reflect.Interface {
			return nil
		}
		if t.Kind() != reflect.Pointer {
			return t
		}
		t = t.Elem()
	}
	return nil
}

// Resolve exported/promoted wire fields by depth and explicit-tag dominance,
// following encoding/json's struct selection. Ambiguous fields are absent and
// thus refused, as by DisallowUnknownFields. No payload keys are case-folded.
func jsonFields(t reflect.Type) map[string]reflect.Type {
	type candidate struct {
		kind   reflect.Type
		depth  int
		tagged bool
	}
	choices := map[string][]candidate{}
	path := map[reflect.Type]bool{}
	var visit func(reflect.Type, int)
	visit = func(current reflect.Type, depth int) {
		if path[current] {
			return
		}
		path[current] = true
		defer delete(path, current)
		for i := 0; i < current.NumField(); i++ {
			f := current.Field(i)
			base := f.Type
			if base.Kind() == reflect.Pointer {
				base = base.Elem()
			}
			if !f.IsExported() && (!f.Anonymous || base.Kind() != reflect.Struct) {
				continue
			}
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, _, _ := strings.Cut(tag, ",")
			if !jsonFieldName(name) {
				name = ""
			}
			if name == "" && f.Anonymous && base.Kind() == reflect.Struct {
				visit(base, depth+1)
				continue
			}
			tagged := name != ""
			if name == "" {
				name = f.Name
			}
			c := candidate{kind: f.Type, depth: depth, tagged: tagged}
			old := choices[name]
			if len(old) == 0 || depth < old[0].depth {
				choices[name] = []candidate{c}
			} else if depth == old[0].depth {
				choices[name] = append(old, c)
			}
		}
	}
	visit(t, 0)
	fields := map[string]reflect.Type{}
	for name, list := range choices {
		if len(list) == 1 {
			fields[name] = list[0].kind
			continue
		}
		var tagged []candidate
		for _, c := range list {
			if c.tagged {
				tagged = append(tagged, c)
			}
		}
		if len(tagged) == 1 {
			fields[name] = tagged[0].kind
		}
	}
	return fields
}

func jsonFieldName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", r) {
			continue
		}
		return false
	}
	return true
}

func readFrame(r io.Reader, maximum int, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > uint32(maximum) {
		return failure("frame_limit")
	}
	raw := make([]byte, int(n))
	if _, err := io.ReadFull(r, raw); err != nil {
		return err
	}
	return decode(raw, value)
}

func writeFrame(w io.Writer, maximum int, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(raw) == 0 || len(raw) > maximum {
		return failure("frame_limit")
	}
	frame := make([]byte, 4+len(raw))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(raw)))
	copy(frame[4:], raw)
	n, err := w.Write(frame)
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	return err
}

// ExecutableSHA256 is integrity evidence, not release authentication. The
// launcher must additionally retain/recheck its original immutable file.
func ExecutableSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0022 != 0 || before.Size() < 1 || before.Size() > 128<<20 {
		return "", failure("executable_invalid")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, (128<<20)+1))
	if err != nil || n != before.Size() {
		return "", failure("executable_changed")
	}
	after, err := f.Stat()
	named, namedErr := os.Stat(path)
	if err != nil || namedErr != nil || !os.SameFile(before, after) || !os.SameFile(before, named) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || before.Size() != named.Size() || before.ModTime() != named.ModTime() {
		return "", failure("executable_changed")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
