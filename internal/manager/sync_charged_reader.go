package manager

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/syncreconcile"
)

// chargedScopeRead is a bounded, typed snapshot of one exact original. SQL
// CASE expressions refuse oversized/BLOB values before they reach Go's scan.
// The row and physical lease are read in one SQLite read transaction.
type chargedScopeRead struct {
	key, side, prepID, prepData, host, owner, provider, scope, mount       string
	projectRoot, selectedRoot, homeRoot, enrollmentDigest, ownershipDigest string
	prepRevision, projectID, ownerEpoch, hard, initialUsed, rowRevision    int64
	state                                                                  string
	final                                                                  sql.NullString
	original, current                                                      model.Lease
	currentRaw                                                             string
}

func chargedText(column string, max int) string {
	return fmt.Sprintf("CASE WHEN typeof(%s)='text' AND length(CAST(%s AS BLOB)) BETWEEN 1 AND %d THEN %s END", column, column, max, column)
}

func chargedInt(column string) string {
	return fmt.Sprintf("CASE WHEN typeof(%s)='integer' THEN %s END", column, column)
}

func chargedOptionalText(column string, max int) string {
	return fmt.Sprintf("CASE WHEN %s IS NULL THEN 1 WHEN typeof(%s)='text' AND length(CAST(%s AS BLOB)) BETWEEN 1 AND %d THEN 1 ELSE 0 END", column, column, column, max)
}

func chargedNullableText(column string, max int) string {
	return fmt.Sprintf("CASE WHEN %s IS NULL THEN NULL WHEN typeof(%s)='text' AND length(CAST(%s AS BLOB)) BETWEEN 1 AND %d THEN %s END", column, column, column, max, column)
}

func chargedOriginalLeaseQuery() string {
	return "SELECT " + chargedText("data", 65536) + "," + chargedText("request_key", 512) + ",CASE WHEN run IS NULL THEN 1 ELSE 0 END FROM leases WHERE id=?"
}

func (m *Manager) readChargedScope(ctx context.Context, key string, side syncreconcile.Side) (chargedScopeRead, error) {
	var zero chargedScopeRead
	if ctx == nil || ctx.Err() != nil || len(key) < 16 || len(key) > 128 || (side != syncreconcile.Local && side != syncreconcile.Remote) {
		return zero, model.Err("sync_evidence_unavailable", "charged key is invalid")
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return zero, model.Err("sync_evidence_unavailable", "charged key contains an invalid character")
		}
	}
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	var operations, scopes int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM sync_operations),(SELECT COUNT(*) FROM sync_charged_scopes)`).Scan(&operations, &scopes); err != nil ||
		operations < 0 || operations > 1 || scopes != 1 {
		return zero, model.Err("sync_evidence_unavailable", "charged scope count exceeds one original")
	}
	columns := []string{
		chargedText("key", 128), chargedText("side", 6), chargedText("preparation_lease", 256),
		chargedInt("preparation_revision"), chargedText("preparation_data", 65536),
		chargedText("host", 256), chargedText("owner", 256), chargedText("provider", 32),
		chargedText("scope_id", 512), chargedText("mount_id", 512), chargedInt("project_id"),
		chargedText("project_root_id", 512), chargedText("selected_root_id", 512), chargedText("home_root_id", 512),
		chargedInt("owner_epoch"), chargedText("enrollment_digest", 64), chargedText("ownership_digest", 64),
		chargedInt("charged_hard_bytes"), chargedInt("initial_used_bytes"), chargedText("state", 32),
		chargedInt("row_revision"), chargedNullableText("final_lease", 256), chargedOptionalText("final_lease", 256),
	}
	query := "SELECT " + strings.Join(columns, ",") + " FROM sync_charged_scopes WHERE key=? AND side=?"
	var r chargedScopeRead
	var finalValid int
	err = tx.QueryRowContext(ctx, query, key, string(side)).Scan(
		&r.key, &r.side, &r.prepID, &r.prepRevision, &r.prepData,
		&r.host, &r.owner, &r.provider, &r.scope, &r.mount, &r.projectID,
		&r.projectRoot, &r.selectedRoot, &r.homeRoot, &r.ownerEpoch,
		&r.enrollmentDigest, &r.ownershipDigest, &r.hard, &r.initialUsed,
		&r.state, &r.rowRevision, &r.final, &finalValid)
	if err != nil || finalValid != 1 {
		return zero, model.Err("sync_evidence_unavailable", "charged scope row is missing, malformed or exceeds bound")
	}
	// A preparing or unresolved original has not handed off to an operation.
	// Keep this phase relation in the same snapshot as its row and lease.
	if (r.state == "preparing" || r.state == "unresolved") && (operations != 0 || r.final.Valid) {
		return zero, model.Err("sync_evidence_unavailable", "preparing charged scope has operation or final lease")
	}
	if r.key != key || r.side != string(side) || r.prepID == "" || r.host != m.host || r.owner != m.owner ||
		r.provider != "xfs-project-quota" || r.scope == "" || r.mount == "" ||
		r.projectID <= 0 || r.projectID > math.MaxUint32 ||
		r.projectRoot == "" || r.selectedRoot == "" || r.homeRoot == "" ||
		r.ownerEpoch <= 0 || r.prepRevision <= 0 || r.rowRevision <= 0 ||
		r.hard <= 0 || r.initialUsed < 0 || r.initialUsed > r.hard ||
		!lowerHex64(r.enrollmentDigest) || !lowerHex64(r.ownershipDigest) {
		return zero, model.Err("sync_evidence_unavailable", "charged scope row identity or bound differs")
	}
	if json.Unmarshal([]byte(r.prepData), &r.original) != nil {
		return zero, model.Err("sync_evidence_unavailable", "charged original lease cannot decode")
	}
	canonical, err := json.Marshal(r.original)
	if err != nil || !bytes.Equal(canonical, []byte(r.prepData)) ||
		r.original.ID != r.prepID || r.original.Revision != int(r.prepRevision) ||
		r.original.Host != r.host || r.original.Owner != r.owner || r.original.State != "active" ||
		r.original.PID != 0 || r.original.Process != nil || r.original.Clock == nil || r.original.Expires == nil ||
		r.original.Request.Kind != syncPreparationKind || r.original.Request.Host != r.host ||
		r.original.Request.Key != mesh.Key(r.host, "sync-preparation\x00"+r.side+"\x00"+r.key) ||
		r.original.Request.Resources.Disk != r.hard {
		return zero, model.Err("sync_evidence_unavailable", "charged original lease differs")
	}
	var physicalKey string
	var runNull int
	if err := tx.QueryRowContext(ctx, chargedOriginalLeaseQuery(), r.prepID).Scan(&r.currentRaw, &physicalKey, &runNull); err != nil {
		return zero, model.Err("sync_evidence_unavailable", "charged physical lease is missing or malformed")
	}
	if runNull != 1 || physicalKey != r.original.Request.Key || json.Unmarshal([]byte(r.currentRaw), &r.current) != nil {
		return zero, model.Err("sync_evidence_unavailable", "charged physical run, request key or lease differs")
	}
	canonical, err = json.Marshal(r.current)
	if err != nil || !bytes.Equal(canonical, []byte(r.currentRaw)) ||
		r.current.ID != r.original.ID || r.current.Host != r.original.Host || r.current.Owner != r.original.Owner ||
		r.current.OwnerUsername != r.original.OwnerUsername || r.current.Created != r.original.Created ||
		!reflect.DeepEqual(r.current.Started, r.original.Started) ||
		!reflect.DeepEqual(r.current.Clock, r.original.Clock) || !reflect.DeepEqual(r.current.Expires, r.original.Expires) ||
		r.current.PID != 0 || r.current.Process != nil || (r.current.State != "terminal" && r.current.Ended != nil) ||
		r.current.Request.Key != physicalKey || !reflect.DeepEqual(r.current.Request, r.original.Request) ||
		r.current.Revision < r.original.Revision {
		return zero, model.Err("sync_evidence_unavailable", "charged current original lease changed")
	}
	if err := tx.Commit(); err != nil {
		return zero, err
	}
	return r, nil
}

// readOnlyChargedKey obtains only bounded TEXT cells before the exact reader.
// A malformed first row cannot be adopted as a new key or skipped.
func (m *Manager) readOnlyChargedKey(ctx context.Context) (string, syncreconcile.Side, error) {
	var key, side string
	q := "SELECT " + chargedText("key", 128) + "," + chargedText("side", 6) + " FROM sync_charged_scopes LIMIT 2"
	if err := m.db.QueryRowContext(ctx, q).Scan(&key, &side); err != nil {
		return "", "", model.Err("sync_evidence_unavailable", "charged scope row identity is missing or malformed")
	}
	return key, syncreconcile.Side(side), nil
}
