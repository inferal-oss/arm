package storage

import (
	"context"
	"fmt"
	"os"

	"github.com/yrashk/arm/internal/model"
)

// OpenArtifactReadOnly pins only an existing, verified ARM-created allocation.
// This grants neither publication permission nor cleanup/adoption authority.
// Callers must retain the ordinary storage obligation and recheck before use.
func (p *Provider) OpenArtifactReadOnly(ctx context.Context, a model.Artifact) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil || p.root == nil {
		return nil, fmt.Errorf("storage provider unavailable")
	}
	if a.Phase != "owned" || a.Path != p.Path(a.Name) {
		return nil, fmt.Errorf("share backing must be an owned managed allocation")
	}
	// Keep the provider descriptor alive across locate, including concurrent
	// Provider.Close; a recycled integer descriptor must never anchor a share.
	raw, err := p.root.SyscallConn()
	if err != nil {
		return nil, err
	}
	var name string
	var f *os.File
	var locateErr error
	if err = raw.Control(func(uintptr) { name, f, locateErr = p.locate(a) }); err != nil {
		if f != nil {
			f.Close()
		}
		return nil, err
	}
	err = locateErr
	if err == nil && name != a.Name {
		f.Close()
		return nil, fmt.Errorf("quarantined storage cannot be shared")
	}
	if err == nil {
		if err = ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, err
}
