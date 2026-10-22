package sshservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func readSourceRecovery(ctx context.Context, home string, m SourceHistoryMarker) (*SourceHistoryRecovery, *stateFile, error) {
	meshDir, e := privateDirectory(filepath.Join(home, "mesh"))
	if e != nil {
		return nil, nil, e
	}
	defer meshDir.Close()
	raw, id, e := readStateFile(ctx, meshDir, SourceHistoryRecoveryName, SourceHistoryRecoveryMaxBytes)
	if errors.Is(e, unix.ENOENT) {
		return nil, nil, nil
	}
	if e != nil {
		return nil, nil, e
	}
	var v SourceHistoryRecovery
	if e = decodeSourceRecovery(raw, &v); e != nil || v.Marker != m {
		return nil, nil, fmt.Errorf("source recovery fence identity/schema differs")
	}
	return &v, &id, nil
}
func checkSourceRecoveryFence(ctx context.Context, home string, m SourceHistoryMarker, allowPending bool) error {
	v, _, e := readSourceRecovery(ctx, home, m)
	if e != nil {
		return e
	}
	if v != nil && v.Pending() && !allowPending {
		return fmt.Errorf("pending exact catalog recovery; use its typed inspection/reconciliation: %w", ErrHistoryUnavailable)
	}
	return nil
}

// InspectSourceHistoryRecovery bypasses ONLY the pending fence's admission
// refusal, not typed schema, authority or namespace identity validation. It
// opens no SQLite connection and cannot infer a new outcome from a gone journal.
func InspectSourceHistoryRecovery(ctx context.Context, home string, b Binding, key string) (SourceHistoryRecoveryAttempt, error) {
	if !stateText(key, 128) {
		return SourceHistoryRecoveryAttempt{}, fmt.Errorf("exact recovery key required")
	}
	s, e := readSourceState(ctx, home, b, false)
	if e != nil {
		return SourceHistoryRecoveryAttempt{}, e
	}
	if s.historyMarker == nil {
		return SourceHistoryRecoveryAttempt{}, os.ErrNotExist
	}
	v, _, e := readSourceRecovery(ctx, home, *s.historyMarker)
	if e != nil {
		return SourceHistoryRecoveryAttempt{}, e
	}
	if v == nil {
		return SourceHistoryRecoveryAttempt{}, os.ErrNotExist
	}
	for _, a := range v.Attempts {
		if a.Key == key {
			return a, nil
		}
	}
	return SourceHistoryRecoveryAttempt{}, os.ErrNotExist
}
