//go:build !armcarrier

package companions

import "io/fs"

// Ordinary development builds contain no distribution payloads.
func embeddedFiles() fs.FS { return nil }
