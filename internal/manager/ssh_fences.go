package manager

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

const sshFenceSchema = `CREATE TABLE ssh_key_fences (principal TEXT NOT NULL,key TEXT NOT NULL,control_sha256 TEXT NOT NULL,state TEXT NOT NULL,lease TEXT UNIQUE,data TEXT NOT NULL,PRIMARY KEY(principal,key))`
const maxSSHLegacyMigration = 4096

type sshFenceQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// sshFenceSchemaPresent is read-only. A partial used namespace is an error,
// never an invitation to recreate an empty idempotency domain.
func (m *Manager) sshFenceSchemaPresent(ctx context.Context) (bool, error) {
	var count int
	if e := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name='ssh_key_fences'`).Scan(&count); e != nil {
		return false, e
	}
	var marker string
	e := m.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='ssh_fence_schema'`).Scan(&marker)
	if count == 0 && errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if count != 1 || e != nil || marker != "1" {
		return false, model.Err("ssh_evidence_unavailable", "SSH key fence namespace is missing or unfamiliar")
	}
	var ddl string
	if e = m.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name='ssh_key_fences'`).Scan(&ddl); e != nil || ddl != sshFenceSchema {
		return false, model.Err("ssh_evidence_unavailable", "SSH key fence schema differs")
	}
	return true, nil
}

// ensureSSHKeyFences runs under Manager.mu before public SSH admission or seal.
// Existing private B1 operations retain immutable raw hashes, not invented
// historical control commitments. The migration and marker publish atomically.
func (m *Manager) ensureSSHKeyFences(ctx context.Context) error {
	present, e := m.sshFenceSchemaPresent(ctx)
	if e != nil || present {
		return e
	}
	if e = m.ensureSSHRecords(); e != nil {
		return e
	}
	rows, e := m.db.QueryContext(ctx, `SELECT principal,key,lease,data FROM ssh_operations ORDER BY principal,key LIMIT ?`, maxSSHLegacyMigration+1)
	if e != nil {
		return e
	}
	type old struct{ source, key, lease, raw string }
	records := []old{}
	total := 0
	for rows.Next() {
		var r old
		if e = rows.Scan(&r.source, &r.key, &r.lease, &r.raw); e != nil {
			rows.Close()
			return e
		}
		total += len(r.raw)
		if len(records) >= maxSSHLegacyMigration || len(r.raw) > 32<<10 || total > 16<<20 {
			rows.Close()
			return model.Err("ssh_evidence_unavailable", "legacy SSH migration exceeds complete bounded scope; no rows omitted")
		}
		records = append(records, r)
	}
	e = errors.Join(rows.Err(), rows.Close())
	if e != nil {
		return e
	}
	fences := make([]model.SSHKeyFence, 0, len(records))
	for _, r := range records {
		if e = ctx.Err(); e != nil {
			return e
		}
		lookup, e := m.lookupSSH(r.source, r.key)
		if e != nil {
			return e
		}
		if !lookup.Found || lookup.Operation == nil || lookup.Lease == nil || lookup.Lease.ID != r.lease {
			return model.Err("ssh_evidence_unavailable", "legacy SSH operation is not completely attributable")
		}
		f := model.SSHKeyFence{Schema: 1, Source: r.source, Target: m.host, Key: r.key, State: "legacy-admitted", OperationSHA256: sshDigest([]byte(r.raw)), Lease: r.lease, Actor: "local:" + m.owner, Reason: "Preserve immutable private SSH admission during explicit key-fence schema transition", At: time.Now().UTC()}
		if e = f.Validate(); e != nil {
			return e
		}
		fences = append(fences, f)
	}
	tx, e := m.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, sshFenceSchema); e != nil {
		return e
	}
	for _, f := range fences {
		if e = insertSSHKeyFence(ctx, tx, f); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('ssh_fence_schema','1')`); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	return tx.Commit()
}
func insertSSHKeyFence(ctx context.Context, tx sshFenceQuery, f model.SSHKeyFence) error {
	if e := f.Validate(); e != nil {
		return e
	}
	raw, e := json.Marshal(f)
	if e != nil {
		return e
	}
	if len(raw) > 32<<10 {
		return model.Err("invalid_request", "SSH fence exceeds32KiB")
	}
	var lease any
	if f.Lease != "" {
		lease = f.Lease
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO ssh_key_fences(principal,key,control_sha256,state,lease,data) VALUES(?,?,?,?,?,?)`, f.Source, f.Key, f.ControlSHA256, f.State, lease, string(raw))
	return e
}
func readSSHKeyFence(ctx context.Context, q sshFenceQuery, source, key, target string) (*model.SSHKeyFence, error) {
	var raw, digest, state string
	var lease sql.NullString
	e := q.QueryRowContext(ctx, `SELECT control_sha256,state,lease,data FROM ssh_key_fences WHERE principal=? AND key=?`, source, key).Scan(&digest, &state, &lease, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if len(raw) > 32<<10 {
		return nil, model.Err("ssh_evidence_unavailable", "SSH fence exceeds record bound")
	}
	var f model.SSHKeyFence
	if e = mesh.Decode([]byte(raw), &f); e != nil {
		return nil, e
	}
	canonical, e := json.Marshal(f)
	if e != nil || !bytes.Equal(canonical, []byte(raw)) || f.Validate() != nil || f.Source != source || f.Target != target || f.Key != key || f.ControlSHA256 != digest || f.State != state || f.Lease != lease.String || lease.Valid != (f.Lease != "") {
		return nil, model.Err("ssh_evidence_unavailable", "SSH fence row is not exact canonical evidence")
	}
	return &f, nil
}

// insertSSHAdmittedFence MUST be called inside the exact transaction that
// inserts the corresponding SSH operation+lease, before the owned process gate.
// Manager.mu is held by its caller. A committed seal cannot be overwritten.
func insertSSHAdmittedFence(ctx context.Context, tx *sql.Tx, c model.SSHControlCommitment, op model.SSHOperation, l model.Lease) error {
	digest, e := c.Digest()
	if e != nil {
		return e
	}
	if !c.Matches(op.Request) || op.Schema != 1 || op.Lease != l.ID || l.Host != c.Target || l.Owner != c.Owner || l.OwnerUsername != c.Account || l.Request.Key != sshLeaseKey(op.Request) || op.AdmittedAt.IsZero() {
		return model.Err("ssh_evidence_unavailable", "admission differs from exact control commitment")
	}
	existing, e := readSSHKeyFence(ctx, tx, c.Source, c.Key, c.Target)
	if e != nil {
		return e
	}
	if existing != nil {
		if existing.State == "sealed" {
			return model.Err("ssh_key_sealed", "this exact key is durably sealed against execution")
		}
		return model.Err("ssh_key_exists", "SSH key already admitted; inspection only")
	}
	raw, e := json.Marshal(op)
	if e != nil {
		return e
	}
	f := model.SSHKeyFence{Schema: 1, Source: c.Source, Target: c.Target, Key: c.Key, State: "admitted", Control: &c, ControlSHA256: digest, OperationSHA256: sshDigest(raw), Lease: l.ID, Actor: "peer:" + c.Source, Reason: c.Reason, At: op.AdmittedAt}
	return insertSSHKeyFence(ctx, tx, f)
}

func (m *Manager) lookupSSHKeyLocked(ctx context.Context, source, key string) (model.SSHKeyStatus, error) {
	out := model.SSHKeyStatus{Schema: 1, Source: source, Target: m.host, Key: key, State: "absent"}
	present, e := m.sshFenceSchemaPresent(ctx)
	if e != nil {
		return out, e
	}
	var tableCount, markerCount int
	if e = m.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='ssh_operations'),(SELECT COUNT(*) FROM metadata WHERE key='ssh_schema')`).Scan(&tableCount, &markerCount); e != nil {
		return out, e
	}
	if tableCount == 0 {
		if markerCount != 0 || present {
			return out, model.Err("ssh_evidence_unavailable", "SSH fence namespace lacks operation ledger")
		}
		return out, nil
	}
	if e = m.ensureSSHRecords(); e != nil {
		return out, e
	} // Existing table validation only.
	lookup, e := m.lookupSSH(source, key)
	if e != nil {
		return out, e
	}
	if !present {
		if lookup.Found {
			out.State = "legacy-admitted"
			out.Execution = &lookup
		}
		return out, nil
	}
	fence, e := readSSHKeyFence(ctx, m.db, source, key, m.host)
	if e != nil {
		return out, e
	}
	if fence == nil {
		if lookup.Found {
			return out, model.Err("ssh_evidence_unavailable", "SSH admission lacks its key fence")
		}
		return out, nil
	}
	out.Fence = fence
	out.State = fence.State
	if fence.State == "sealed" {
		if lookup.Found {
			return out, model.Err("ssh_evidence_unavailable", "sealed SSH key also has admitted work")
		}
		return out, nil
	}
	if !lookup.Found || lookup.Operation == nil || lookup.Lease == nil || lookup.Lease.ID != fence.Lease {
		return out, model.Err("ssh_evidence_unavailable", "SSH fence has no exact operation/lease")
	}
	raw, _ := json.Marshal(lookup.Operation)
	if sshDigest(raw) != fence.OperationSHA256 || (fence.Control != nil && !fence.Control.Matches(lookup.Operation.Request)) {
		return out, model.Err("ssh_evidence_unavailable", "SSH fenced operation changed")
	}
	out.Execution = &lookup
	return out, nil
}

// LookupSSHKey is private, principal-scoped, read-only and never initializes a
// table/key/listener or treats an absent snapshot as nonexecution proof.
func (m *Manager) LookupSSHKey(ctx context.Context, source, key string) (model.SSHKeyStatus, error) {
	if ctx == nil || !sshHex(source, 16) || !mesh.ValidKey(key) {
		return model.SSHKeyStatus{}, model.Err("invalid_request", "exact SSH principal/key required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lookupSSHKeyLocked(ctx, source, key)
}

// SealSSHKey is a private destination integration entry, not an exposed wire
// operation. The caller authenticates current direct-peer grants and derives
// Source from mTLS before invoking it. No child exit/timeout/absent lookup seals.
func (m *Manager) SealSSHKey(ctx context.Context, c model.SSHControlCommitment, reason string) (model.SSHKeyStatus, error) {
	return m.sealSSHKey(ctx, c, reason, "peer:"+c.Source)
}

// SealSSHKeyAsOperator uses native authenticated operator authority only. It
// never repairs trust or asserts remote revocation/cleanup.
func (m *Manager) SealSSHKeyAsOperator(ctx context.Context, c model.SSHControlCommitment, reason string) (model.SSHKeyStatus, error) {
	return m.sealSSHKey(ctx, c, reason, "local:"+m.owner)
}

func (m *Manager) sealSSHKey(ctx context.Context, c model.SSHControlCommitment, reason, actor string) (model.SSHKeyStatus, error) {
	if ctx == nil {
		return model.SSHKeyStatus{}, model.Err("invalid_request", "context required")
	}
	if _, e := c.Digest(); e != nil {
		return model.SSHKeyStatus{}, e
	}
	if len(reason) > 4096 || reason == "" {
		return model.SSHKeyStatus{}, model.Err("invalid_request", "seal reason required and at most4096bytes")
	}
	digest, _ := c.Digest()
	f := model.SSHKeyFence{Schema: 1, Source: c.Source, Target: c.Target, Key: c.Key, State: "sealed", Control: &c, ControlSHA256: digest, Actor: actor, Reason: reason, At: time.Now().UTC()}
	if e := f.Validate(); e != nil {
		return model.SSHKeyStatus{}, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return model.SSHKeyStatus{}, e
	}
	if m.closing {
		return model.SSHKeyStatus{}, model.Err("backend_stopping", "SSH seal unavailable after closing begins")
	}
	if c.Target != m.host || c.Owner != m.owner || c.Account != m.ownerUsername {
		return model.SSHKeyStatus{}, model.Err("forbidden", "SSH seal authority/account differs")
	}
	if e := m.ensureSSHKeyFences(ctx); e != nil {
		return model.SSHKeyStatus{}, e
	}
	current, e := m.lookupSSHKeyLocked(ctx, c.Source, c.Key)
	if e != nil {
		return current, e
	}
	if current.State != "absent" {
		if current.Fence != nil && current.Fence.Control != nil && !reflect.DeepEqual(*current.Fence.Control, c) {
			return current, model.Err("idempotency_conflict", "SSH key belongs to another exact control")
		}
		return current, nil
	}
	tx, e := m.db.BeginTx(ctx, nil)
	if e != nil {
		return current, e
	}
	defer tx.Rollback()
	// The same transaction/fence used by admission is decisive, including when
	// a stream already completed authentication but has not submitted exec.
	var n int
	if e = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM ssh_operations WHERE principal=? AND key=?)+(SELECT COUNT(*) FROM leases WHERE request_key=?)`, c.Source, c.Key, mesh.Key(c.Source, "ssh-exec\x00"+c.Key)).Scan(&n); e != nil {
		return current, e
	}
	if n != 0 {
		return current, model.Err("ssh_evidence_unavailable", "SSH work appeared before sealing")
	}
	if e = insertSSHKeyFence(ctx, tx, f); e != nil {
		return current, e
	}
	if e = event(tx, "ssh.key_sealed", c.Key, f); e != nil {
		return current, e
	}
	if e = ctx.Err(); e != nil {
		return current, e
	}
	if e = tx.Commit(); e != nil {
		return current, e
	}
	return model.SSHKeyStatus{Schema: 1, Source: c.Source, Target: c.Target, Key: c.Key, State: "sealed", Fence: &f}, nil
}
