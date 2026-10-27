package syncservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/inferal-oss/arm/internal/syncfs"
	"github.com/inferal-oss/arm/internal/syncreconcile"
)

const WorkspacesSchema = `CREATE TABLE sync_workspaces (key TEXT NOT NULL,side TEXT NOT NULL,role TEXT NOT NULL,path TEXT NOT NULL,root_id TEXT NOT NULL,parent_id TEXT NOT NULL,base_id TEXT NOT NULL,disk_bytes INTEGER NOT NULL,PRIMARY KEY(key,side,role),UNIQUE(path))`

// WorkspaceRecord is a durable locator, not authority to delete files or
// release a disk promise. Native roots must be reopened and checked separately.
type WorkspaceRecord struct {
	Role, Path, RootID, ParentID, BaseID string
	DiskBytes                            int64
}

func validWorkspaceRole(side syncreconcile.Side, role string) bool {
	if role == "receiver" {
		return true
	}
	return role == "source-"+string(side)+"-to-local" || role == "source-"+string(side)+"-to-remote"
}

func validWorkspaceID(id string) bool {
	if !strings.HasPrefix(id, "arm-sync-root-v2:") || len(id) != len("arm-sync-root-v2:")+64 {
		return false
	}
	for _, c := range strings.TrimPrefix(id, "arm-sync-root-v2:") {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}

func validWorkspaceRecord(side syncreconcile.Side, r WorkspaceRecord) bool {
	name := filepath.Base(r.Path)
	if !validWorkspaceRole(side, r.Role) || len(r.Path) > syncreconcile.MaxPathBytes || !utf8.ValidString(r.Path) || strings.ContainsAny(r.Path, "\x00\r\n\\") || !filepath.IsAbs(r.Path) || filepath.Clean(r.Path) != r.Path ||
		!strings.HasPrefix(name, ".arm-sync-") || len(name) != len(".arm-sync-")+24 ||
		!validWorkspaceID(r.RootID) || !validWorkspaceID(r.ParentID) || !validWorkspaceID(r.BaseID) ||
		r.DiskBytes <= 0 || r.DiskBytes > 1<<40 {
		return false
	}
	for _, c := range strings.TrimPrefix(name, ".arm-sync-") {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}

// RecordWorkspace binds a newly created marked workspace to the original
// operation before staging. Failure retains the charged obligation and native
// workspace; no mutation or cleanup may follow an uncertain commit.
func (l *Ledger) RecordWorkspace(ctx context.Context, role string, selected, parent *syncfs.Root, work *syncfs.Workspace) error {
	if err := l.valid(); err != nil {
		return err
	}
	if ctx == nil || ctx.Err() != nil || selected == nil || parent == nil || work == nil ||
		!validWorkspaceRole(l.Side, role) || work.Parent != parent {
		return ErrUnknown
	}
	lookup, err := l.Lookup(ctx)
	if err != nil {
		return err
	}
	intent := lookup.Intent
	wantID, disk := intent.Binding.Local.RootID, intent.LocalResources.Disk
	if l.Side == syncreconcile.Remote {
		wantID, disk = intent.Binding.Remote.RootID, intent.RemoteResources.Disk
	}
	if lookup.State != "reserved" || work.Reservation.Key != l.Key ||
		work.Reservation.Side != l.Side || work.Reservation.PlanDigest != intent.PlanDigest ||
		work.Reservation.Generation != intent.Generation || work.Reservation.DiskBytes > disk {
		return ErrUnknown
	}
	for _, root := range []*syncfs.Root{work.Base, work.Stage, work.Quarantine} {
		if root == nil || root.Check() != nil {
			return syncfs.ErrChanged
		}
	}
	verified, err := syncfs.InspectWorkspace(selected, parent, work.Path(), work.Reservation)
	if err != nil {
		return err
	}
	defer verified.Close()
	rootID, err := selected.NativeID()
	if err != nil || rootID != wantID {
		return ErrUnknown
	}
	parentID, err := parent.NativeID()
	if err != nil {
		return err
	}
	baseID, err := verified.Base.NativeID()
	if err != nil {
		return err
	}
	for _, pair := range [][2]*syncfs.Root{{work.Base, verified.Base}, {work.Stage, verified.Stage}, {work.Quarantine, verified.Quarantine}} {
		originalID, e := pair[0].NativeID()
		if e != nil {
			return e
		}
		verifiedID, e := pair[1].NativeID()
		if e != nil || originalID != verifiedID {
			return syncfs.ErrChanged
		}
	}
	record := WorkspaceRecord{Role: role, Path: work.Path(), RootID: rootID, ParentID: parentID, BaseID: baseID, DiskBytes: work.Reservation.DiskBytes}
	if !validWorkspaceRecord(l.Side, record) {
		return ErrUnknown
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := durableTx(ctx, tx); err != nil {
		return err
	}
	var digest, state string
	if err := tx.QueryRowContext(ctx, `SELECT record_sha256,state FROM sync_operations WHERE key=? AND side=?`, l.Key, l.Side).Scan(&digest, &state); err != nil {
		return err
	}
	recordRaw, err := json.Marshal(intent)
	if err != nil || digest != sha(recordRaw) || state != "reserved" {
		return ErrUnknown
	}
	var collision int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM sync_workspaces WHERE path=? LIMIT 1`, record.Path).Scan(&collision)
	if err == nil {
		return ErrUnknown
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_workspaces(key,side,role,path,root_id,parent_id,base_id,disk_bytes) VALUES(?,?,?,?,?,?,?,?)`,
		l.Key, l.Side, record.Role, record.Path, record.RootID, record.ParentID, record.BaseID, record.DiskBytes); err != nil {
		return ErrUnknown
	}
	return tx.Commit()
}

// Workspaces lists only bounded, shaped durable locators. The caller must
// reopen native roots to inspect them; rows alone never grant cleanup.
func (l *Ledger) Workspaces(ctx context.Context) ([]WorkspaceRecord, error) {
	if err := l.valid(); err != nil {
		return nil, err
	}
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrUnknown
	}
	lookup, err := l.Lookup(ctx)
	if err != nil {
		return nil, err
	}
	// SQLite affinities do not enforce stored types. Bound bytes and reject
	// unexpected types inside SQL, before Scan can allocate a private BLOB or
	// include its contents in a numeric-conversion error.
	rows, err := l.DB.QueryContext(ctx, `SELECT
		CASE WHEN typeof(role)='text' THEN substr(CAST(role AS BLOB),1,129) ELSE '' END,
		CASE WHEN typeof(path)='text' THEN substr(CAST(path AS BLOB),1,4097) ELSE '' END,
		CASE WHEN typeof(root_id)='text' THEN substr(CAST(root_id AS BLOB),1,82) ELSE '' END,
		CASE WHEN typeof(parent_id)='text' THEN substr(CAST(parent_id AS BLOB),1,82) ELSE '' END,
		CASE WHEN typeof(base_id)='text' THEN substr(CAST(base_id AS BLOB),1,82) ELSE '' END,
		CASE WHEN typeof(disk_bytes)='integer' THEN disk_bytes ELSE -1 END
		FROM sync_workspaces WHERE key=? AND side=? ORDER BY role LIMIT 4`, l.Key, l.Side)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkspaceRecord
	for rows.Next() {
		var r WorkspaceRecord
		if err := rows.Scan(&r.Role, &r.Path, &r.RootID, &r.ParentID, &r.BaseID, &r.DiskBytes); err != nil {
			return nil, err
		}
		wantID, disk := lookup.Intent.Binding.Local.RootID, lookup.Intent.LocalResources.Disk
		if l.Side == syncreconcile.Remote {
			wantID, disk = lookup.Intent.Binding.Remote.RootID, lookup.Intent.RemoteResources.Disk
		}
		if !validWorkspaceRecord(l.Side, r) || r.RootID != wantID || r.DiskBytes > disk || len(out) == 3 {
			return nil, ErrUnknown
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	receiver := false
	for _, record := range out {
		if record.Role == "receiver" {
			receiver = true
			break
		}
	}
	if !receiver && (len(out) > 0 || lookup.State != "reserved" && lookup.State != "sealed") {
		return nil, ErrUnknown
	}
	return out, nil
}
