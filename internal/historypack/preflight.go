package historypack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

// preflightManifest bounds the typed entry allocation BEFORE json.Unmarshal.
// It consumes a closed streaming shape with bounded strings/numbers and stops
// on entry4097; a hostile4MiB array cannot allocate hundreds of thousands of
// typed entries. Canonical encoding and semantic offsets are checked afterwards.
func preflightManifest(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	err := shapeObject(d, map[string]func() error{
		"schema_version": func() error { return shapeUint(d) },
		"pack_id":        func() error { return shapeString(d, 32) },
		"authority":      func() error { return shapeString(d, 32) },
		"records_bytes":  func() error { return shapeUint(d) },
		"records_sha256": func() error { return shapeString(d, 64) },
		"entries": func() error {
			if e := shapeDelim(d, '['); e != nil {
				return e
			}
			count := 0
			for d.More() {
				if count >= MaxRecords {
					return fmt.Errorf("manifest entry count exceeds4096 before typed allocation")
				}
				count++
				if e := shapeObject(d, map[string]func() error{
					"key": func() error {
						return shapeObject(d, map[string]func() error{
							"domain":    func() error { return shapeString(d, MaxDomainBytes) },
							"principal": func() error { return shapeString(d, MaxPrincipalBytes) },
							"key":       func() error { return shapeString(d, MaxKeyBytes) },
						})
					},
					"frame_offset":  func() error { return shapeUint(d) },
					"payload_bytes": func() error { return shapeUint(d) },
					"sha256":        func() error { return shapeString(d, 64) },
				}); e != nil {
					return e
				}
			}
			if count == 0 {
				return fmt.Errorf("manifest entries must be nonempty")
			}
			return shapeDelim(d, ']')
		},
	})
	if err != nil {
		return err
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("manifest has trailing data")
	}
	return nil
}
func shapeObject(d *json.Decoder, fields map[string]func() error) error {
	if e := shapeDelim(d, '{'); e != nil {
		return e
	}
	seen := map[string]bool{}
	for d.More() {
		t, e := d.Token()
		if e != nil {
			return e
		}
		key, ok := t.(string)
		if !ok {
			return fmt.Errorf("object field required")
		}
		fn, ok := fields[key]
		if !ok || seen[key] {
			return fmt.Errorf("unknown/duplicate exact manifest field")
		}
		seen[key] = true
		if e = fn(); e != nil {
			return e
		}
	}
	if len(seen) != len(fields) {
		return fmt.Errorf("required manifest field missing")
	}
	return shapeDelim(d, '}')
}
func shapeDelim(d *json.Decoder, want json.Delim) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	if t != want {
		return fmt.Errorf("manifest shape differs")
	}
	return nil
}
func shapeString(d *json.Decoder, limit int) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	s, ok := t.(string)
	if !ok || len(s) > limit {
		return fmt.Errorf("manifest string type/size differs")
	}
	return nil
}
func shapeUint(d *json.Decoder) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	n, ok := t.(json.Number)
	if !ok || len(n) > 20 {
		return fmt.Errorf("bounded unsigned number required")
	}
	_, e = strconv.ParseUint(string(n), 10, 64)
	return e
}
