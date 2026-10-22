package database

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Complete existing lifecycle rosters are <=512; the new nested source roster
// is <=256. Bound every collection before reflected/typed maintenance decoding.
// Nullability and exact canonical fields remain the stricter typed validators'
// responsibility, preserving older valid maintenance receipt representations.
func preflightResetCollections(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("maintenance JSON depth exceeds bound")
		}
		t, e := d.Token()
		if e != nil {
			return e
		}
		v, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		if v != '{' && v != '[' {
			return fmt.Errorf("invalid maintenance delimiter")
		}
		n := 0
		for d.More() {
			n++
			if n > 1024 {
				return fmt.Errorf("maintenance collection exceeds1024 before typed allocation")
			}
			if v == '{' {
				if _, e = d.Token(); e != nil {
					return e
				}
			}
			if e = walk(depth + 1); e != nil {
				return e
			}
		}
		end, e := d.Token()
		want := json.Delim(']')
		if v == '{' {
			want = '}'
		}
		if e != nil || end != want {
			return fmt.Errorf("maintenance collection incomplete")
		}
		return nil
	}
	if e := walk(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("trailing maintenance JSON")
	}
	return nil
}
