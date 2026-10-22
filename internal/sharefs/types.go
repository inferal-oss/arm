// Package sharefs implements the confined immutable read-only origin boundary.
// It supplies no network listener, publication policy, mount or cleanup authority.
package sharefs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yrashk/arm/internal/model"
)

const (
	ChunkBytes       = 1 << 20
	MaxEntries       = 8192
	MaxChunks        = 32768
	MaxManifestBytes = 8 << 20
	MaxDepth         = 32
)

var (
	ErrChanged = errors.New("published share generation changed")
	ErrLimit   = errors.New("share bound exceeded")
	ErrDenied  = errors.New("share access denied")
	ErrClosed  = errors.New("share handle closed")
)

// Limits may reduce, but never silently expand, this capability's bounds.
type Limits struct{ Entries, Chunks, ManifestBytes, Handles, Reads, Sessions int }

func DefaultLimits() Limits { return Limits{MaxEntries, MaxChunks, MaxManifestBytes, 64, 8, 8} }
func (l Limits) validate() error {
	if l.Entries < 1 || l.Entries > MaxEntries || l.Chunks < 1 || l.Chunks > MaxChunks || l.ManifestBytes < 1 || l.ManifestBytes > MaxManifestBytes || l.Handles < 1 || l.Handles > 64 || l.Reads < 1 || l.Reads > 8 || l.Sessions < 1 || l.Sessions > 8 {
		return fmt.Errorf("invalid share bounds")
	}
	return nil
}

type Binding struct {
	Authority       string `json:"authority"`
	Owner           uint32 `json:"owner"`
	Storage         string `json:"storage"`
	StorageRevision int64  `json:"storage_revision"`
	Share           string `json:"share"`
	Generation      string `json:"generation"`
}

func (b Binding) validate() error {
	if !id(b.Authority) || !id(b.Storage) || !id(b.Share) || !id(b.Generation) || b.StorageRevision < 1 {
		return fmt.Errorf("exact share authority, storage revision and generation required")
	}
	return nil
}

// Scope is a session capability, not evidence that authorization remains valid.
// The required runtime Guard consults current enrollment/grant/lease authority.
type Scope struct {
	Source             string
	EnrollmentRevision int64
	Consumer           string
	Session            string
}

func (s Scope) validate() error {
	if !id(s.Source) || !id(s.Consumer) || !id(s.Session) || s.EnrollmentRevision < 1 {
		return ErrDenied
	}
	return nil
}

type Access struct {
	Binding         Binding
	Scope           Scope
	Operation, Path string
}
type Guard func(context.Context, Access) error

type Version struct {
	Identity        model.FileIdentity `json:"identity"`
	Mount           string             `json:"mount"`
	Size            int64              `json:"size"`
	Mode            uint32             `json:"mode"`
	Links           uint64             `json:"links"`
	ModifiedSeconds int64              `json:"modified_seconds"`
	ModifiedNanos   int64              `json:"modified_nanos"`
	ChangedSeconds  int64              `json:"changed_seconds"`
	ChangedNanos    int64              `json:"changed_nanos"`
}
type Entry struct {
	Path      string   `json:"path"`
	Directory bool     `json:"directory"`
	Version   Version  `json:"version"`
	Chunks    []string `json:"chunks"`
}
type Manifest struct {
	Schema     int     `json:"schema"`
	Binding    Binding `json:"binding"`
	ChunkBytes int     `json:"chunk_bytes"`
	Entries    []Entry `json:"entries"`
	SHA256     string  `json:"sha256"`
}

// Info exposes origin metadata deliberately mapped to read-only/no-exec access.
// Numeric origin ownership is descriptive; it never authenticates a consumer.
type Info struct {
	Path                           string
	Directory                      bool
	Size                           int64
	Mode                           uint32
	ModifiedSeconds, ModifiedNanos int64
}

func info(e Entry) Info {
	mode := uint32(0400)
	size := e.Version.Size
	if e.Directory {
		mode = 0500
		size = 0
	}
	return Info{e.Path, e.Directory, size, mode, e.Version.ModifiedSeconds, e.Version.ModifiedNanos}
}

func id(s string) bool {
	if len(s) != 32 {
		return false
	}
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 16 && strings.ToLower(s) == s
}
func digest(s string) bool {
	if len(s) != 64 {
		return false
	}
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && strings.ToLower(s) == s
}
func manifestHash(m Manifest) (string, int, error) {
	m.SHA256 = ""
	b, err := json.Marshal(m)
	if err != nil {
		return "", 0, err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), len(b) + sha256.Size*2, nil
}
