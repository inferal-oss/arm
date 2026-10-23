package providerexec

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/model"
)

const maxControlFrame = 512 << 10

type packet struct {
	Version  int             `json:"version"`
	Nonce    string          `json:"nonce"`
	Sequence int             `json:"sequence"`
	Kind     string          `json:"kind"`
	Data     json.RawMessage `json:"data"`
}
type imagePacket struct {
	Path      string             `json:"path"`
	SHA256    string             `json:"sha256"`
	Size      int64              `json:"size"`
	Identity  model.FileIdentity `json:"identity"`
	Ancestors []directoryWitness `json:"ancestors"`
	ACLSHA256 string             `json:"acl_sha256"`
	Stamp     fileStamp          `json:"stamp"`
}

func (i Image) packet() imagePacket {
	return imagePacket{Path: i.path, SHA256: i.sha, Size: i.size, Identity: i.identity, Ancestors: append([]directoryWitness(nil), i.ancestors...), ACLSHA256: i.aclSHA, Stamp: i.stamp}
}
func (i imagePacket) image() Image {
	return Image{path: i.Path, sha: i.SHA256, size: i.Size, identity: i.Identity, stamp: i.Stamp, ancestors: append([]directoryWitness(nil), i.Ancestors...), aclSHA: i.ACLSHA256, valid: true}
}

type initPacket struct {
	Runtime       imagePacket        `json:"runtime"`
	Provider      imagePacket        `json:"provider"`
	TempPath      string             `json:"temp_path"`
	TempIdentity  model.FileIdentity `json:"temp_identity"`
	Request       json.RawMessage    `json:"request"`
	RequestSHA256 string             `json:"request_sha256"`
}
type helloPacket struct {
	Process       model.ProcessIdentity `json:"process"`
	RuntimeSHA256 string                `json:"runtime_sha256"`
	RequestSHA256 string                `json:"request_sha256"`
}
type grantPacket struct {
	RequestSHA256 string `json:"request_sha256"`
}
type resultPacket struct {
	Process model.ProcessIdentity `json:"process"`
	Exit    Exit                  `json:"exit"`
	Started bool                  `json:"started"`
}

func writePacket(w io.Writer, nonce string, sequence int, kind string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	body, err := json.Marshal(packet{1, nonce, sequence, kind, data})
	if err != nil {
		return err
	}
	if len(body) == 0 || len(body) > maxControlFrame {
		return fmt.Errorf("control frame exceeds bound")
	}
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(body)))
	for _, part := range [][]byte{head[:], body} {
		for len(part) > 0 {
			n, e := w.Write(part)
			if n < 0 || n > len(part) {
				return fmt.Errorf("invalid control write count")
			}
			part = part[n:]
			if e != nil {
				return e
			}
			if n == 0 {
				return io.ErrNoProgress
			}
		}
	}
	return nil
}
func readPacket(r io.Reader, nonce string, sequence int, kind string, out any) error {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(head[:])
	if size == 0 || size > maxControlFrame {
		return fmt.Errorf("invalid bounded control frame")
	}
	body := make([]byte, int(size))
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	var p packet
	if err := strictJSON(body, &p); err != nil {
		return err
	}
	if p.Version != 1 || len(p.Nonce) != 32 || nonce != "" && p.Nonce != nonce || p.Sequence != sequence || p.Kind != kind {
		return fmt.Errorf("foreign control packet")
	}
	return strictJSON(p.Data, out)
}
func strictJSON(b []byte, out any) error {
	if len(b) == 0 || len(b) > maxControlFrame || !utf8.Valid(b) {
		return fmt.Errorf("invalid control JSON bytes")
	}
	if err := escapedUnicode(b); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	nodes := 0
	var walk func(int) error
	walk = func(depth int) error {
		nodes++
		if depth > 24 || nodes > 8192 {
			return fmt.Errorf("control JSON shape limit")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		if s, ok := t.(string); ok {
			if len(s) > 4096 || strings.ContainsRune(s, 0) {
				return fmt.Errorf("control string limit")
			}
			return nil
		}
		v, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch v {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return e
				}
				s, ok := k.(string)
				if !ok || len(s) > 4096 || strings.ContainsRune(s, 0) || keys[s] || len(keys) >= 64 {
					return fmt.Errorf("duplicate/excess control field")
				}
				keys[s] = true
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
			_, err = d.Token()
			return err
		case '[':
			n := 0
			for d.More() {
				n++
				if n > 256 {
					return fmt.Errorf("control array limit")
				}
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		}
		return fmt.Errorf("invalid control delimiter")
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing control JSON")
	}
	if err := requiredShape(b, reflect.TypeOf(out).Elem()); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(out)
}

var rawType = reflect.TypeFor[json.RawMessage]()

func requiredShape(b []byte, t reflect.Type) error {
	if t == rawType {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		if t.Kind() == reflect.Pointer {
			return nil
		}
		return fmt.Errorf("null required control value")
	}
	if t.Kind() == reflect.Pointer {
		return requiredShape(b, t.Elem())
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		var items []json.RawMessage
		if err := json.Unmarshal(b, &items); err != nil || items == nil || len(items) > 256 {
			return fmt.Errorf("bounded required control array")
		}
		if t.Kind() == reflect.Array && len(items) != t.Len() {
			return fmt.Errorf("exact fixed control array length required")
		}
		for _, item := range items {
			if err := requiredShape(item, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(b, &fields); err != nil || fields == nil {
			return fmt.Errorf("required control object")
		}
		if len(fields) != t.NumField() {
			return fmt.Errorf("closed required control fields")
		}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" {
				name = field.Name
			}
			v, ok := fields[name]
			if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) && field.Type.Kind() != reflect.Pointer && field.Type != rawType {
				return fmt.Errorf("missing/null control field")
			}
			if err := requiredShape(v, field.Type); err != nil {
				return err
			}
		}
	}
	return nil
}

func readControlBody(r io.Reader) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > maxControlFrame {
		return nil, fmt.Errorf("control frame limit")
	}
	b := make([]byte, int(n))
	_, err := io.ReadFull(r, b)
	return b, err
}

// encoding/json replaces unpaired escaped UTF16 surrogates. Reject them before
// decoding so private control identity is never silently rewritten.
func escapedUnicode(b []byte) error {
	hex4 := func(v []byte) (uint16, bool) {
		var n uint16
		for _, c := range v {
			n <<= 4
			switch {
			case c >= '0' && c <= '9':
				n += uint16(c - '0')
			case c >= 'a' && c <= 'f':
				n += uint16(c - 'a' + 10)
			case c >= 'A' && c <= 'F':
				n += uint16(c - 'A' + 10)
			default:
				return 0, false
			}
		}
		return n, true
	}
	for i := 0; i < len(b); i++ {
		if b[i] != '"' {
			continue
		}
		i++
		for i < len(b) && b[i] != '"' {
			if b[i] == '\\' {
				i++
				if i >= len(b) {
					return fmt.Errorf("truncated control escape")
				}
				if b[i] == 'u' {
					if i+4 >= len(b) {
						return fmt.Errorf("truncated Unicode escape")
					}
					v, ok := hex4(b[i+1 : i+5])
					if !ok {
						return fmt.Errorf("invalid Unicode escape")
					}
					i += 4
					if v >= 0xd800 && v <= 0xdbff {
						if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
							return fmt.Errorf("unpaired control Unicode surrogate")
						}
						w, ok := hex4(b[i+3 : i+7])
						if !ok || w < 0xdc00 || w > 0xdfff {
							return fmt.Errorf("unpaired control Unicode surrogate")
						}
						i += 6
					} else if v >= 0xdc00 && v <= 0xdfff {
						return fmt.Errorf("unpaired control Unicode surrogate")
					}
				}
			}
			i++
		}
	}
	return nil
}
