package sharefs

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The root is the empty string. Adapters must translate their single root
// convention explicitly; this boundary never cleans or repeatedly decodes paths.
func validPath(p string) error {
	if len(p) > 4096 || !utf8.ValidString(p) {
		return fmt.Errorf("invalid share path")
	}
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	if len(parts) > MaxDepth {
		return ErrLimit
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 255 {
			return fmt.Errorf("noncanonical share path")
		}
		for _, r := range part {
			if r == '\\' || unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
				return fmt.Errorf("unsupported share path character")
			}
		}
	}
	return nil
}
func parent(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return ""
	}
	return p[:i]
}
