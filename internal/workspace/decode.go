package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

// Decode validates canonical keys/required fields before encoding/json can
// collapse duplicate/case aliases, null primitives or missing values to zero.
func Decode(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > MaxRecordBytes || !utf8.Valid(raw) {
		return fmt.Errorf("invalid bounded UTF-8 workspace record")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if e := unique(d, 0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("trailing workspace JSON")
	}
	t := reflect.TypeOf(out)
	if t == nil || t.Kind() != reflect.Pointer || t.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("workspace decoder requires struct pointer")
	}
	if e := fields(raw, t.Elem()); e != nil {
		return e
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	return d.Decode(out)
}
func unique(d *json.Decoder, depth int) error {
	if depth > 32 {
		return fmt.Errorf("workspace JSON depth exceeded")
	}
	tok, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return fmt.Errorf("duplicate workspace field")
			}
			seen[s] = true
			if e = unique(d, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e = unique(d, depth+1); e != nil {
				return e
			}
		}
	default:
		return fmt.Errorf("invalid workspace delimiter")
	}
	_, e = d.Token()
	return e
}
func fields(raw []byte, t reflect.Type) error {
	null := bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	if null {
		return fmt.Errorf("workspace null evidence is unsupported; omit absent optional fields")
	}
	if t.Kind() == reflect.Pointer {
		return fields(raw, t.Elem())
	}
	if t == reflect.TypeFor[time.Time]() || t == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if e := json.Unmarshal(raw, &object); e != nil || object == nil {
			return fmt.Errorf("workspace object required")
		}
		known := map[string]reflect.StructField{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if f.PkgPath != "" || name == "-" {
				continue
			}
			if name == "" {
				return fmt.Errorf("explicit workspace tags required")
			}
			known[name] = f
		}
		for k, v := range object {
			f, ok := known[k]
			if !ok {
				return fmt.Errorf("unknown/noncanonical workspace field %q", k)
			}
			if e := fields(v, f.Type); e != nil {
				return fmt.Errorf("%s: %w", k, e)
			}
		}
		for k, f := range known {
			if _, ok := object[k]; !ok && !strings.Contains(f.Tag.Get("json"), ",omitempty") {
				return fmt.Errorf("missing workspace field %q", k)
			}
		}
	case reflect.Slice, reflect.Array:
		var list []json.RawMessage
		if e := json.Unmarshal(raw, &list); e != nil {
			return e
		}
		if len(list) > MaxMembers {
			return fmt.Errorf("workspace collection too large")
		}
		for _, v := range list {
			if e := fields(v, t.Elem()); e != nil {
				return e
			}
		}
	case reflect.Map:
		return fmt.Errorf("free-form workspace maps unsupported")
	}
	return nil
}
