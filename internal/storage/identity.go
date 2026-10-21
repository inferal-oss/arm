package storage

import "github.com/yrashk/arm/internal/model"

// DirectoryIdentity reads the native creation-time identity of an already-open
// directory. It grants no adoption/deletion or process-control authority. The
// caller must independently validate descriptor type, ownership and modes.
func DirectoryIdentity(fd int) (model.FileIdentity, error) {
	return durableIdentity(fd, "")
}
