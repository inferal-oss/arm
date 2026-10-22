//go:build armcarrier

package companions

import (
	"embed"
	"io/fs"
)

// Generated ONLY inside the carrier builder's private source tree. Thin
// payload builds never enable armcarrier, so payloads cannot recursively embed.
//
//go:embed generated/*
var embedded embed.FS

func embeddedFiles() fs.FS { return embedded }
