package storage

import (
	"fmt"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

// readOnlyNamedObservation is one no-follow named observation, consumed by the
// same read-only resolution. It is not an opened-FD version or cached authority.
type readOnlyNamedObservation struct {
	Identity model.FileIdentity
	Mode     uint32
}

func (p *Provider) validateReadOnlyNamed() error {
	observed, err := readOnlyNamedAt(unix.AT_FDCWD, p.path)
	if err != nil {
		return err
	}
	if observed.Mode&unix.S_IFMT != unix.S_IFDIR || observed.Identity != p.identity || observed.Mode&0077 != 0 {
		return fmt.Errorf("managed root was replaced or its permissions changed")
	}
	return nil
}
