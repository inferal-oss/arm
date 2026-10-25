package snapshotlease

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/yrashk/arm/internal/model"
)

// SourceReader permits the same fixed bounded read before preflight and inside
// admission's transaction. It grants no native access or allocation authority.
type SourceReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// ReadSourceLease binds the decoded body to the selected physical SQL row and
// ordinary request/execution columns. A body ID must never retarget a control,
// native source path or transaction read to another allocation.
func ReadSourceLease(ctx context.Context, reader SourceReader, id, authority, owner, home string) (l model.Lease, err error) {
	if !ID(id) {
		return l, fmt.Errorf("exact snapshot source SQL identity required")
	}
	var selected, keyType, runType string
	var key sql.NullString
	var raw []byte
	err = reader.QueryRowContext(ctx, `SELECT CASE WHEN typeof(id)='text' AND length(CAST(id AS BLOB))=32 THEN id END,typeof(request_key),CASE WHEN typeof(request_key)='text' AND length(CAST(request_key AS BLOB)) BETWEEN 1 AND 128 THEN request_key END,CASE WHEN typeof(data)='text' AND length(CAST(data AS BLOB)) BETWEEN 1 AND 1048576 THEN data END,typeof(run) FROM leases WHERE id=?`, id).Scan(&selected, &keyType, &key, &raw, &runType)
	if err != nil {
		return l, err
	}
	if len(raw) == 0 {
		return l, fmt.Errorf("snapshot source SQL byte/type bound differs")
	}
	if err = json.Unmarshal(raw, &l); err != nil {
		return l, err
	}
	if selected != id || l.ID != id || l.Host != authority || l.Owner != owner || runType != "null" || l.PID != 0 || l.Cargo != nil || l.Workload != nil {
		return l, fmt.Errorf("snapshot source SQL/body/authority/execution binding differs")
	}
	if l.Request.Key == "" {
		if keyType != "null" || key.Valid {
			return l, fmt.Errorf("snapshot source empty request key must be SQL NULL")
		}
	} else if !text(l.Request.Key, 128) || keyType != "text" || !key.Valid || key.String != l.Request.Key {
		return l, fmt.Errorf("snapshot source immutable request key differs from SQL")
	}
	want := filepath.Join(home, "storage", id)
	if l.Request.Kind != "storage" || l.Artifact == nil || l.Artifact.Name != id || l.Artifact.Path != want || l.ManagedDir != want {
		return l, fmt.Errorf("snapshot source managed allocation layout differs")
	}
	return l, nil
}
