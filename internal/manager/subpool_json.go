package manager

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

// Immutable identity evidence rejects aliases/missing scalar fields instead of
// allowing encoding/json to turn them into zero/default or replacement values.
func decodePoolRecord(raw []byte, out any) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("invalid UTF8 pool evidence")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := poolUniqueJSON(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing pool JSON")
	}
	if err := poolJSONFields(raw, reflect.TypeOf(out).Elem()); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(out)
}
func poolUniqueJSON(d *json.Decoder, depth int) error {
	if depth > 40 {
		return fmt.Errorf("pool JSON nesting exceeds bound")
	}
	tok, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	keys := map[string]bool{}
	for d.More() {
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || keys[s] {
				return fmt.Errorf("duplicate pool JSON key")
			}
			keys[s] = true
		}
		if err := poolUniqueJSON(d, depth+1); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}
func poolJSONFields(raw []byte, t reflect.Type) error {
	if t.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil
		}
		return poolJSONFields(raw, t.Elem())
	}
	if t == reflect.TypeOf(time.Time{}) {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("null pool timestamp")
		}
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
			return nil
		}
		return fmt.Errorf("null pool evidence")
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			return fmt.Errorf("invalid pool object")
		}
		allowed := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			name := tag[0]
			if name == "" {
				name = f.Name
			}
			allowed[name] = true
			v, ok := fields[name]
			optional := false
			for _, s := range tag[1:] {
				optional = optional || s == "omitempty"
			}
			if !ok {
				if optional {
					continue
				}
				return fmt.Errorf("missing canonical pool field")
			}
			if err := poolJSONFields(v, f.Type); err != nil {
				return err
			}
		}
		for key := range fields {
			if !allowed[key] {
				return fmt.Errorf("unknown pool field")
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, v := range values {
			if err := poolJSONFields(v, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
