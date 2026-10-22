package sshservice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Bound new history arrays BEFORE typed decoding. The only new collections
// are<=64 reviewed members/cleanup names/recovery attempts; pack manifests have
// their separately reviewed historypack streaming decoder.
func preflightSourceHistory(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return fmt.Errorf("history JSON depth exceeds bound")
		}
		token, e := d.Token()
		if e != nil {
			return e
		}
		switch value := token.(type) {
		case json.Delim:
			switch value {
			case '{':
				n := 0
				for d.More() {
					n++
					if n > 64 {
						return fmt.Errorf("history object field bound exceeded")
					}
					key, e := d.Token()
					if e != nil {
						return e
					}
					if text, ok := key.(string); !ok || len(text) > 128 {
						return fmt.Errorf("history field-name bound exceeded")
					}
					if e = walk(depth + 1); e != nil {
						return e
					}
				}
				end, e := d.Token()
				if e != nil || end != json.Delim('}') {
					return fmt.Errorf("history object incomplete")
				}
			case '[':
				n := 0
				for d.More() {
					n++
					if n > 64 {
						return fmt.Errorf("history array count exceeds64 before typed allocation")
					}
					if e = walk(depth + 1); e != nil {
						return e
					}
				}
				end, e := d.Token()
				if e != nil || end != json.Delim(']') {
					return fmt.Errorf("history array incomplete")
				}
			default:
				return fmt.Errorf("unexpected history delimiter")
			}
		case string:
			if len(value) > 8192 {
				return fmt.Errorf("history string exceeds bound")
			}
		case json.Number:
			if len(value) > 20 {
				return fmt.Errorf("history number exceeds bound")
			}
		case nil:
			return fmt.Errorf("null history fields unsupported")
		}
		return nil
	}
	if e := walk(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("trailing history JSON")
	}
	return nil
}
