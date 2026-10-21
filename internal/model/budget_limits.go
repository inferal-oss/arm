package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// A count is an exact integer: JSON null must not accidentally become the Go
// zero value and silently change an inherited pool ceiling into a zero ceiling.
// Nullable native fields deliberately retain their documented clear semantics.
func (limits *BudgetLimits) UnmarshalJSON(raw []byte) error {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("budget limits must be an object")
	}
	type plain BudgetLimits
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("budget limits must contain exactly one object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if pools := fields["pools"]; len(pools) > 0 && !bytes.Equal(bytes.TrimSpace(pools), []byte("null")) {
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(pools, &entries); err != nil {
			return err
		}
		for _, value := range entries {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return fmt.Errorf("pool ceiling counts must be int64 integers; omit an ID to clear its ceiling")
			}
		}
	}
	*limits = BudgetLimits(decoded)
	return nil
}
