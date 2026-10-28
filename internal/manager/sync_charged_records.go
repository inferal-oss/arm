package manager

import (
	"context"
	"encoding/hex"

	"getarm.org/internal/model"
)

// verifyChargedSyncRecords is startup evidence review for the private fresh
// version-3 namespace. It does not restore an original in-process owner or
// authorize native work. At most one project/operation is supported here.
func (m *Manager) verifyChargedSyncRecords(ctx context.Context, used bool) error {
	var operations, scopes int
	if err := m.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM sync_operations),(SELECT COUNT(*) FROM sync_charged_scopes)`).Scan(&operations, &scopes); err != nil {
		return err
	}
	if operations < 0 || operations > 1 || scopes < 0 || scopes > 1 || used != (operations > 0 || scopes > 0) {
		return model.Err("sync_evidence_unavailable", "charged sync count or use marker differs")
	}
	// Version-2 operation/binding history retains its complete independent
	// checks. Preparing-only history has no operation or final binding yet.
	if err := m.verifySyncRecords(ctx, operations > 0); err != nil {
		return err
	}
	if scopes == 0 {
		if operations != 0 {
			return model.Err("sync_evidence_unavailable", "admitted sync has no original charged scope")
		}
		return nil
	}
	key, side, err := m.readOnlyChargedKey(ctx)
	if err != nil {
		return err
	}
	r, err := m.readChargedScope(ctx, key, side)
	if err != nil {
		return err
	}
	if r.state == "preparing" || r.state == "unresolved" {
		if operations != 0 || r.final.Valid ||
			(r.current.State != "active" && r.current.State != "needs_attention") ||
			(r.current.State == "active" && r.currentRaw != r.prepData) {
			return model.Err("sync_evidence_unavailable", "preparing scope lost its original charged lease")
		}
	} else if operations != 1 || !r.final.Valid || r.final.String == "" || r.current.State != "terminal" {
		return model.Err("sync_evidence_unavailable", "admitted scope lacks final handoff")
	} else {
		var finalLease string
		query := "SELECT " + chargedText("lease", 256) + " FROM sync_lease_bindings WHERE key=? AND side=?"
		if err := m.db.QueryRowContext(ctx, query, r.key, r.side).Scan(&finalLease); err != nil || finalLease != r.final.String {
			return model.Err("sync_evidence_unavailable", "charged final lease binding differs")
		}
	}
	return nil
}

func lowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	b, err := hex.DecodeString(s)
	return err == nil && hex.EncodeToString(b) == s
}
