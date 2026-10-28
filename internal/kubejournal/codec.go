package kubejournal

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

const MaxRecordBytes = 512 << 10

// Decode accepts one bounded, exact tagged schema. It rejects aliases,
// duplicate/unknown members, raw interfaces and excessive nesting/counts.
func Decode(raw []byte, out any) error {
	t := reflect.TypeOf(out)
	if t == nil || t.Kind() != reflect.Pointer || reflect.ValueOf(out).IsNil() || len(raw) == 0 || len(raw) > MaxRecordBytes || !utf8.Valid(raw) {
		return fmt.Errorf("invalid bounded Kubernetes journal")
	}
	if err := schemaFrame(raw, t.Elem(), 0); err != nil {
		return err
	}
	fresh := reflect.New(t.Elem())
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(fresh.Interface()); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("one Kubernetes journal object required")
	}
	reflect.ValueOf(out).Elem().Set(fresh.Elem())
	return nil
}
func schemaFrame(raw json.RawMessage, t reflect.Type, depth int) error {
	if depth > 32 {
		return fmt.Errorf("Kubernetes journal nesting exceeds bound")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		// Inherited Plan maps/slices have native nil representations. The journal
		// validator separately requires its own stage/attempt arrays to be non-null.
		if t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
			return nil
		}
		return fmt.Errorf("null Kubernetes journal field")
	}
	if t.Kind() == reflect.Pointer {
		return schemaFrame(raw, t.Elem(), depth+1)
	}
	if t == reflect.TypeFor[time.Time]() {
		var at time.Time
		return json.Unmarshal(raw, &at)
	}
	switch t.Kind() {
	case reflect.Struct:
		fields := map[string]reflect.StructField{}
		optional := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tags := strings.Split(f.Tag.Get("json"), ",")
			if tags[0] == "-" {
				continue
			}
			if tags[0] == "" {
				tags[0] = f.Name // exact native JSON name; aliases are still refused
			}
			fields[tags[0]] = f
			for _, tag := range tags[1:] {
				optional[tags[0]] = optional[tags[0]] || tag == "omitempty"
			}
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		first, err := d.Token()
		if err != nil || first != json.Delim('{') {
			return fmt.Errorf("journal object required")
		}
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			f, known := fields[name]
			if !ok || !known || seen[name] {
				return fmt.Errorf("unknown, aliased or duplicate journal field")
			}
			seen[name] = true
			var part json.RawMessage
			if err = d.Decode(&part); err != nil {
				return err
			}
			if err = schemaFrame(part, f.Type, depth+1); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim('}') || d.Decode(new(any)) != io.EOF {
			return fmt.Errorf("incomplete journal object")
		}
		for name := range fields {
			if !seen[name] && !optional[name] {
				return fmt.Errorf("missing journal field %s", name)
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil || len(values) > 128 {
			return fmt.Errorf("journal array exceeds bound")
		}
		for _, v := range values {
			if err := schemaFrame(v, t.Elem(), depth+1); err != nil {
				return err
			}
		}
	case reflect.Map:
		if t.Key().Kind() != reflect.String || t.Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported journal map schema")
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		first, err := d.Token()
		if err != nil || first != json.Delim('{') {
			return fmt.Errorf("journal map required")
		}
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || len(name) > 253 || seen[name] || len(seen) >= 64 {
				return fmt.Errorf("invalid or duplicate journal map member")
			}
			seen[name] = true
			var value string
			if err = d.Decode(&value); err != nil || len(value) > 65536 {
				return fmt.Errorf("invalid journal map string")
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim('}') || d.Decode(new(any)) != io.EOF {
			return fmt.Errorf("incomplete journal map")
		}
	case reflect.String:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || len(value) > 65536 {
			return fmt.Errorf("invalid bounded journal string")
		}
	case reflect.Bool, reflect.Int, reflect.Int64, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float64:
		return json.Unmarshal(raw, reflect.New(t).Interface())
	default:
		return fmt.Errorf("unsupported journal schema type %s", t)
	}
	return nil
}
