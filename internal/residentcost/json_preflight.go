package residentcost

import (
	"bytes"
	"encoding/json"
	"io"
)

// These fixed structural DATA limits run BEFORE a typed decoder can allocate
// slices for malformed/duplicate arrays. Valid post-decode cardinality checks
// remain mandatory. Token strings may transiently use up to the bounded input
// size before their individual length is checked; that decoder/token backing
// is explicitly funded, not asserted to be bounded by valid field lengths.
func PreflightEvidenceJSONData(raw []byte) error {
	return jsonPreflightData(raw, 1<<20, 4096, 256, 256, 128<<10)
}

func PreflightLedgerJSONData(raw []byte) error {
	return jsonPreflightData(raw, MaxLedgerBytes, 2048, 64, 64, 4096)
}

func jsonPreflightData(raw []byte, maxBytes, maxTokens, maxObjects, maxArrays, maxString int) error {
	if len(raw) == 0 || len(raw) > maxBytes {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber() // oversized numeric text never becomes an unbounded float parse
	depth, tokens, objects, arrays := 0, 0, 0, 0
	var frames [16]struct {
		kind     json.Delim
		elements int
	}
	rootStarted, rootCompleted := false, false
	for {
		token, err := d.Token()
		if err == io.EOF {
			if rootStarted && rootCompleted && depth == 0 {
				return nil
			}
			return ErrInvalid
		}
		if err != nil || rootCompleted {
			return ErrInvalid
		}
		tokens++
		if tokens > maxTokens {
			return ErrInvalid
		}
		if !rootStarted {
			if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
				return ErrInvalid
			}
			rootStarted = true
		}
		// Count each array element at its START, including null values that
		// would otherwise allocate zero-valued typed structs without an object
		// delimiter. Object keys and nested members are not extra elements.
		closing := token == json.Delim('}') || token == json.Delim(']')
		if depth > 0 && frames[depth-1].kind == '[' && !closing {
			frames[depth-1].elements++
			if frames[depth-1].elements > 64 {
				return ErrInvalid
			}
		}
		switch value := token.(type) {
		case json.Delim:
			switch value {
			case '{':
				objects++
				if depth >= len(frames) {
					return ErrInvalid
				}
				frames[depth].kind, frames[depth].elements = value, 0
				depth++
			case '[':
				arrays++
				if depth >= len(frames) {
					return ErrInvalid
				}
				frames[depth].kind, frames[depth].elements = value, 0
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					rootCompleted = true
				}
			default:
				return ErrInvalid
			}
			if depth < 0 || depth > 16 || objects > maxObjects || arrays > maxArrays {
				return ErrInvalid
			}
		case string:
			if len(value) > maxString {
				return ErrInvalid
			}
		case json.Number:
			if len(value) > 32 {
				return ErrInvalid
			}
		case bool, nil:
		default:
			return ErrInvalid
		}
	}
}
