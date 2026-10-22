package database

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

func strictEvidence(raw string, out any, required ...string) error {
	if !utf8.ValidString(raw) {
		return fmt.Errorf("evidence is not valid UTF-8")
	}
	if len(raw) > MaxRecordBytes {
		return fmt.Errorf("record exceeds 1 MiB proof bound")
	}
	d := json.NewDecoder(bytes.NewBufferString(raw))
	d.UseNumber()
	if err := uniqueJSON(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON evidence")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil || object == nil {
		return fmt.Errorf("evidence must be an object")
	}
	for _, key := range required {
		v, ok := object[key]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fmt.Errorf("required evidence field %q is missing/null", key)
		}
	}
	if err := validateEvidenceFields(json.RawMessage(raw), reflect.TypeOf(out).Elem()); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewBufferString(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	if err := d.Decode(out); err != nil {
		return fmt.Errorf("unsupported evidence schema or value")
	}
	return nil
}
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON evidence exceeds 64 nesting levels")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return fmt.Errorf("duplicate/invalid JSON evidence key")
			}
			seen[s] = true
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	_, err = d.Token()
	return err
}

// encoding/json accepts null into primitive Go fields as their zero value.
// Destructive proof must not turn malformed counts/flags into safe zero/false,
// or absent current-schema required fields into defaults. Optional pointer/map/
// slice fields retain their declared nullable semantics.
func validateEvidenceFields(raw json.RawMessage, t reflect.Type) error {
	if t == nil {
		return nil
	}
	if t.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil
		}
		return validateEvidenceFields(raw, t.Elem())
	}
	if t == reflect.TypeOf(json.RawMessage{}) || t.Kind() == reflect.Interface {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if t.Kind() == reflect.Map || t.Kind() == reflect.Slice {
			return nil
		}
		return fmt.Errorf("null is not valid scalar evidence")
	}
	if t == reflect.TypeOf(time.Time{}) {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			return fmt.Errorf("structured evidence must be an object")
		}
		return validateEvidenceStruct(fields, t)
	case reflect.Slice, reflect.Array:
		var entries []json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			return err
		}
		for _, v := range entries {
			if err := validateEvidenceFields(v, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Map:
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			return err
		}
		for _, v := range entries {
			if err := validateEvidenceFields(v, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

type evidenceStructField struct {
	typ      reflect.Type
	optional bool
}

func evidenceStructFields(t reflect.Type, out map[string]evidenceStructField) error {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() && !f.Anonymous {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		name := parts[0]
		if f.Anonymous && name == "" {
			base := f.Type
			if base.Kind() == reflect.Pointer {
				base = base.Elem()
			}
			if base.Kind() == reflect.Struct {
				if err := evidenceStructFields(base, out); err != nil {
					return err
				}
				continue
			}
		}
		if name == "" {
			name = f.Name
		}
		optional := false
		for _, part := range parts[1:] {
			if part == "omitempty" || part == "omitzero" {
				optional = true
			}
		}
		if _, present := out[name]; present {
			return fmt.Errorf("ambiguous canonical evidence struct schema")
		}
		out[name] = evidenceStructField{typ: f.Type, optional: optional}
	}
	return nil
}
func validateEvidenceStruct(fields map[string]json.RawMessage, t reflect.Type) error {
	schema := map[string]evidenceStructField{}
	if err := evidenceStructFields(t, schema); err != nil {
		return err
	}
	// encoding/json accepts case-insensitive aliases even with
	// DisallowUnknownFields. Validate exact canonical names before it can choose
	// a conflicting alias. Only struct keys are constrained; free-form map keys
	// remain data and each typed map value is validated recursively.
	for name := range fields {
		if _, allowed := schema[name]; !allowed {
			return fmt.Errorf("unknown or noncanonical evidence field")
		}
	}
	for name, field := range schema {
		value, present := fields[name]
		if !present {
			if field.optional {
				continue
			}
			return fmt.Errorf("required current-schema field %q is absent", name)
		}
		if err := validateEvidenceFields(value, field.typ); err != nil {
			return fmt.Errorf("invalid evidence field %s: %w", name, err)
		}
	}
	return nil
}
