package observe

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
)

// diskutil emits XML property lists. This small decoder accepts the container,
// string, integer, and boolean values we need and skips unsupported scalars.
// Input is bounded at the command boundary; nesting is bounded here too.
func plist(data []byte) (map[string]any, error) {
	v, err := propertyList(data)
	if err != nil {
		return nil, err
	}
	d, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected property list dictionary")
	}
	return d, nil
}
func propertyList(data []byte) (any, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		if s, ok := t.(xml.StartElement); ok && (s.Name.Local == "dict" || s.Name.Local == "array") {
			v, err := plistValue(d, s, 0)
			if err != nil {
				return nil, err
			}
			return v, nil
		}
	}
}
func plistValue(d *xml.Decoder, start xml.StartElement, depth int) (any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("property list nesting exceeds limit")
	}
	if start.Name.Local == "dict" || start.Name.Local == "array" {
		dict := map[string]any{}
		array := []any{}
		key := ""
		hasKey := false
		for {
			t, err := d.Token()
			if err != nil {
				return nil, err
			}
			switch v := t.(type) {
			case xml.EndElement:
				if v.Name == start.Name {
					if hasKey {
						return nil, fmt.Errorf("property list key without value")
					}
					if start.Name.Local == "dict" {
						return dict, nil
					}
					return array, nil
				}
			case xml.StartElement:
				if start.Name.Local == "dict" && !hasKey {
					if v.Name.Local != "key" {
						return nil, fmt.Errorf("expected property list key")
					}
					if err := d.DecodeElement(&key, &v); err != nil {
						return nil, err
					}
					hasKey = true
					continue
				}
				x, err := plistValue(d, v, depth+1)
				if err != nil {
					return nil, err
				}
				if start.Name.Local == "dict" {
					dict[key] = x
					hasKey = false
				} else {
					array = append(array, x)
				}
			}
		}
	}
	var value string
	if err := d.DecodeElement(&value, &start); err != nil {
		return nil, err
	}
	switch start.Name.Local {
	case "string":
		return value, nil
	case "integer":
		n, err := strconv.ParseUint(value, 10, 64)
		return n, err
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return nil, nil
	}
}

// Avoid accepting partial, failed, or unrelated diskutil records as a pool.
func apfsPool(data []byte, reference string) (map[string]any, error) {
	d, err := plist(data)
	if err != nil {
		return nil, err
	}
	containers, _ := d["Containers"].([]any)
	for _, v := range containers {
		c, ok := v.(map[string]any)
		if ok && c["ContainerReference"] == reference {
			if uuid, _ := c["APFSContainerUUID"].(string); uuid != "" {
				return c, nil
			}
		}
	}
	return nil, fmt.Errorf("APFS container identity is unavailable")
}
