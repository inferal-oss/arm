package agentsessions

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SavedSessionRef addresses display metadata in one client home. It is not an
// ARM identity, native generation, delivery capability or enrollment record.
type SavedSessionRef struct {
	Adapter string `json:"adapter"`
	Home    string `json:"client_home"`
	ID      string `json:"native_session"`
}

// Valid checks lexical supported metadata only. Producers must establish actual
// canonical paths/membership, and activation must verify its own current facts.
func (r SavedSessionRef) Valid() bool {
	id, err := uuid.Parse(r.ID)
	return err == nil && id.String() == r.ID && (r.Adapter == "codex" || r.Adapter == "claude") && savedPath(r.Home)
}

func savedPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 4096 && !strings.ContainsFunc(path, func(r rune) bool { return r < 32 || r == 127 })
}

type SavedSession struct {
	Ref       SavedSessionRef `json:"native"`
	Directory string          `json:"directory"`
	Name      string          `json:"name,omitempty"`
	Updated   time.Time       `json:"metadata_updated_at"`
}

func (s SavedSession) Valid() bool { return s.Ref.Valid() && savedPath(s.Directory) }

type SavedSessionResult struct {
	Sessions  []SavedSession `json:"sessions"`
	Warnings  []string       `json:"warnings"`
	Truncated bool           `json:"truncated,omitempty"`
}

// SavedSessionSource is bounded, read-only display discovery. Ordinary native
// Session/Source discovery and its identity/delivery contract remain separate.
type SavedSessionSource interface {
	DiscoverSavedSessions(context.Context, []string) SavedSessionResult
}
