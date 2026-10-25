package nodestop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
)

// Decode accepts exactly the declared case-sensitive schema, including nested
// objects. encoding/json alone accepts duplicate keys and case aliases. Null
// does not stand for an absent/empty required array, object or scalar here.
func Decode(raw []byte, value any) error {
	t := reflect.TypeOf(value)
	if t == nil || t.Kind() != reflect.Pointer || reflect.ValueOf(value).IsNil() || len(raw) == 0 || len(raw) > MaxFrameBytes {
		return fmt.Errorf("invalid bounded node-stop frame")
	}
	if err := frame(raw, t.Elem()); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	fresh := reflect.New(t.Elem())
	if err := d.Decode(fresh.Interface()); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("one complete node-stop frame required")
	}
	reflect.ValueOf(value).Elem().Set(fresh.Elem())
	return nil
}

func frame(raw json.RawMessage, t reflect.Type) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("null node-stop field")
	}
	if t.Kind() == reflect.Pointer {
		return frame(raw, t.Elem())
	}
	if t == reflect.TypeFor[json.RawMessage]() {
		if !json.Valid(raw) {
			return fmt.Errorf("invalid raw node-stop payload")
		}
		return nil
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
			tag := strings.Split(f.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			if tag[0] == "" {
				return fmt.Errorf("node-stop schema field lacks exact tag")
			}
			fields[tag[0]] = f
			for _, item := range tag[1:] {
				optional[tag[0]] = optional[tag[0]] || item == "omitempty"
			}
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		token, err := d.Token()
		if err != nil || token != json.Delim('{') {
			return fmt.Errorf("node-stop object required")
		}
		seen := map[string]bool{}
		for d.More() {
			name, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := name.(string)
			f, known := fields[key]
			if !ok || !known || seen[key] {
				return fmt.Errorf("unknown, aliased or duplicate node-stop field %q", name)
			}
			seen[key] = true
			var part json.RawMessage
			if err := d.Decode(&part); err != nil {
				return err
			}
			if err := frame(part, f.Type); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim('}') || d.Decode(new(any)) != io.EOF {
			return fmt.Errorf("incomplete node-stop object")
		}
		for key := range fields {
			if !seen[key] && !optional[key] {
				return fmt.Errorf("missing node-stop field %q", key)
			}
		}
	case reflect.Slice:
		var parts []json.RawMessage
		if err := json.Unmarshal(raw, &parts); err != nil || parts == nil || len(parts) > MaxItems {
			return fmt.Errorf("bounded non-null node-stop array required")
		}
		for _, part := range parts {
			if err := frame(part, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int64, reflect.Uint32, reflect.Uint64:
		return json.Unmarshal(raw, reflect.New(t).Interface())
	default:
		return fmt.Errorf("unsupported node-stop schema type %s", t)
	}
	return nil
}
