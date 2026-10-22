package database

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// Cargo omitempty fields are absent in producer evidence, never explicit null.
// Required nullable slices (notably an audit with no regulated settings) retain
// their declared semantics; semantic validators bound consumed collections.
func cargoNoOptionalNull(raw json.RawMessage, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil
		}
		t = t.Elem()
	}
	if t == reflect.TypeOf(time.Time{}) {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return fmt.Errorf("invalid Cargo evidence object")
		}
		schema := map[string]evidenceStructField{}
		if e := evidenceStructFields(t, schema); e != nil {
			return e
		}
		for name, value := range fields {
			field, ok := schema[name]
			if !ok {
				return fmt.Errorf("unknown Cargo evidence field")
			}
			if field.optional && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return fmt.Errorf("explicit null Cargo optional evidence is unsupported")
			}
			if e := cargoNoOptionalNull(value, field.typ); e != nil {
				return e
			}
		}
	case reflect.Array, reflect.Slice:
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil
		}
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return fmt.Errorf("invalid Cargo evidence collection")
		}
		for _, v := range values {
			if e := cargoNoOptionalNull(v, t.Elem()); e != nil {
				return e
			}
		}
	}
	return nil
}
