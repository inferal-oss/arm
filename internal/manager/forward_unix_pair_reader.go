package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
	"getarm.org/internal/storage"
)

type forwardUnixPlannedRow struct {
	data      forwardUnixPairData
	encoded   string
	sourceRaw string
	proof     storage.EmptyDirectoryProof
}

func forwardUnixPairUnavailable() error {
	return model.Err("forward_unix_pair_unavailable", "forward Unix pair requires exact no-bind proof or compatible native recovery")
}

func validForwardUnixSocketName(name string) bool {
	prefix := "forward-"
	if strings.HasPrefix(name, "f-") {
		prefix = "f-"
	}
	return len(name) == len(prefix)+32+len(".sock") && strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".sock") && canonicalForwardHex(strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".sock"), 16)
}

func validForwardUnixPairPhase(data forwardUnixPairData) bool {
	validIntent := data.BindIntent != nil && data.BindIntent.Directory == data.StorageArtifact && data.BindIntent.Allocated >= 0 && data.BindIntent.Allocated <= data.DiskBytes && !data.BindIntent.At.IsZero() && data.BindIntent.At.Location() == time.UTC
	validBound := data.Bound != nil && data.Bound.Directory == data.StorageArtifact && data.Bound.Socket.Device == data.StorageArtifact.Device && data.Bound.Socket.Inode != 0 && data.Bound.Socket.Owner == data.StorageArtifact.Owner && data.Bound.Socket.BirthKnown && data.Bound.Allocated >= 0 && data.Bound.Allocated <= data.DiskBytes && !data.Bound.At.IsZero() && data.Bound.At.Location() == time.UTC && validIntent && !data.Bound.At.Before(data.BindIntent.At)
	switch data.Phase {
	case "planned":
		return data.Revision == 1 && data.NoBind == nil && data.BindIntent == nil && data.Bound == nil && data.NativeTerminal == nil
	case "bind_intent":
		return data.Revision == 2 && data.NoBind == nil && validIntent && data.Bound == nil && data.NativeTerminal == nil
	case "bound":
		return data.Revision == 3 && data.NoBind == nil && validIntent && validBound && data.NativeTerminal == nil
	case "terminal":
		if data.Revision == 2 && data.NoBind != nil && data.BindIntent == nil && data.Bound == nil && data.NativeTerminal == nil {
			return data.NoBind.Directory == data.StorageArtifact && data.NoBind.Allocated >= 0 && data.NoBind.Allocated <= data.DiskBytes && !data.NoBind.At.IsZero()
		}
		return data.Revision == 4 && data.NoBind == nil && validIntent && validBound && data.NativeTerminal != nil && data.NativeTerminal.Socket == data.Bound.Socket && data.NativeTerminal.Directory == data.StorageArtifact && data.NativeTerminal.Allocated >= 0 && data.NativeTerminal.Allocated <= data.DiskBytes && !data.NativeTerminal.At.IsZero() && data.NativeTerminal.At.Location() == time.UTC && !data.NativeTerminal.At.Before(data.Bound.At)
	}
	return false
}

func validForwardUnixPairClock(d *leaseclock.Deadline, max time.Duration) bool {
	return d != nil && d.Version == 1 && d.Boot != "" && len(d.Boot) <= 256 && d.Anchor >= 0 && d.End > d.Anchor && d.End-d.Anchor <= int64(max) && !d.Wall.IsZero()
}

// Forwarding Unix pairs own the default storage root only. A persisted lease
// cannot acquire named-root placement through a same-revision data change.
func forwardUnixDefaultRootLease(l model.Lease) bool {
	return l.Request.StoragePool == "" && l.StorageBinding == nil
}

func readForwardUnixPairLease(ctx context.Context, tx *sql.Tx, id string) (model.Lease, string, string, error) {
	var l model.Lease
	var raw, key string
	var runNull int
	err := tx.QueryRowContext(ctx, `SELECT
		CASE WHEN typeof(data)='text' AND length(CAST(data AS BLOB))<=65536 THEN data ELSE '' END,
		CASE WHEN typeof(request_key)='text' AND length(CAST(request_key AS BLOB))<=128 THEN request_key ELSE '' END,
		CASE WHEN run IS NULL THEN 1 ELSE 0 END
		FROM leases WHERE id=?`, id).Scan(&raw, &key, &runNull)
	if err != nil || runNull != 1 || len(raw) > 65536 || len(key) > 128 || json.Unmarshal([]byte(raw), &l) != nil || l.ID != id || l.Request.Key != key || !forwardUnixDefaultRootLease(l) {
		return l, "", "", forwardUnixPairUnavailable()
	}
	canonical, err := json.Marshal(l)
	if err != nil || string(canonical) != raw {
		return l, "", "", forwardUnixPairUnavailable()
	}
	return l, raw, key, nil
}

// readForwardUnixPairRows bounds every projected field before Go allocates it.
// Bind intent and later native phases retain both charges until a separate
// reviewed original-FD cleanup reader exists.
func readForwardUnixPairRows(ctx context.Context, tx *sql.Tx) ([]forwardUnixPlannedRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT
		CASE WHEN typeof(source_id)='text' AND length(CAST(source_id AS BLOB))<=32 THEN source_id ELSE '' END,
		CASE WHEN typeof(storage_id)='text' AND length(CAST(storage_id AS BLOB))<=32 THEN storage_id ELSE '' END,
		CASE WHEN typeof(phase)='text' AND length(CAST(phase AS BLOB))<=32 THEN phase ELSE '' END,
		CASE WHEN typeof(source_key)='text' AND length(CAST(source_key AS BLOB))<=128 THEN source_key ELSE '' END,
		CASE WHEN typeof(storage_key)='text' AND length(CAST(storage_key AS BLOB))<=128 THEN storage_key ELSE '' END,
		CASE WHEN typeof(socket_name)='text' AND length(CAST(socket_name AS BLOB))<=128 THEN socket_name ELSE '' END,
		CASE WHEN typeof(disk_bytes)='integer' THEN disk_bytes ELSE -1 END,
		CASE WHEN typeof(data)='text' AND length(CAST(data AS BLOB))<=4096 THEN data ELSE '' END
		FROM forward_unix_pairs ORDER BY source_id LIMIT 257`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	planned := make([]forwardUnixPlannedRow, 0)
	for rows.Next() {
		if len(planned) == 256 {
			return nil, forwardUnixPairUnavailable()
		}
		var sourceID, storageID, phase, sourceKey, storageKey, name, raw string
		var diskBytes int64
		if err := rows.Scan(&sourceID, &storageID, &phase, &sourceKey, &storageKey, &name, &diskBytes, &raw); err != nil {
			return nil, forwardUnixPairUnavailable()
		}
		if !canonicalForwardHex(sourceID, 16) || !canonicalForwardHex(storageID, 16) || sourceID == storageID || phase != "planned" && phase != "terminal" && phase != "bind_intent" && phase != "bound" || !strings.HasPrefix(sourceKey, "forward-listener:") || !canonicalForwardHex(strings.TrimPrefix(sourceKey, "forward-listener:"), 16) || storageKey != "forward-unix-storage:"+sourceID || len(sourceKey) > 128 || len(storageKey) > 128 || !validForwardUnixSocketName(name) || diskBytes != forwardUnixDiskPromise || len(raw) > 4096 {
			return nil, forwardUnixPairUnavailable()
		}
		var data forwardUnixPairData
		if json.Unmarshal([]byte(raw), &data) != nil {
			return nil, forwardUnixPairUnavailable()
		}
		canonical, err := json.Marshal(data)
		if err != nil || string(canonical) != raw || data.Schema != 1 || data.Phase != phase || data.SourceID != sourceID || data.StorageID != storageID || data.SourceKey != sourceKey || data.StorageKey != storageKey || data.SocketName != name || data.DiskBytes != diskBytes || data.SourceRevision < 1 || data.StorageRevision < 1 || data.StorageArtifact.Device == 0 || data.StorageArtifact.Inode == 0 || data.StorageRoot.Device == 0 || data.StorageRoot.Inode == 0 || !validForwardUnixPairPhase(data) {
			return nil, forwardUnixPairUnavailable()
		}
		planned = append(planned, forwardUnixPlannedRow{data: data, encoded: raw})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return planned, nil
}

// validateForwardUnixPairStartup accepts only exact completed terminal
// tombstones. A planned row requires a separately fenced native absence
// survey; bind intent and bound states still retain both charges.
func validateForwardUnixPairStartup(ctx context.Context, tx *sql.Tx, allowPlanned bool) error {
	rows, err := readForwardUnixPairRows(ctx, tx)
	if err != nil || len(rows) == 0 {
		return err
	}
	var rootRaw string
	if err = tx.QueryRowContext(ctx, `SELECT CASE WHEN typeof(value)='text' AND length(CAST(value AS BLOB))<=4096 THEN value ELSE '' END FROM metadata WHERE key='storage_root'`).Scan(&rootRaw); err != nil || len(rootRaw) > 4096 {
		return forwardUnixPairUnavailable()
	}
	var root model.FileIdentity
	if json.Unmarshal([]byte(rootRaw), &root) != nil || root.Device == 0 || root.Inode == 0 {
		return forwardUnixPairUnavailable()
	}
	canonicalRoot, err := json.Marshal(root)
	if err != nil || string(canonicalRoot) != rootRaw {
		return forwardUnixPairUnavailable()
	}
	for _, row := range rows {
		if row.data.Phase == "planned" && allowPlanned && row.data.StorageRoot == root {
			continue
		}
		if err := validateForwardUnixTerminalRow(ctx, tx, row, root); err != nil {
			return err
		}
	}
	return nil
}

func validateForwardUnixTerminalRow(ctx context.Context, tx *sql.Tx, row forwardUnixPlannedRow, root model.FileIdentity) error {
	if row.data.Phase != "terminal" || row.data.StorageRoot != root {
		return forwardUnixPairUnavailable()
	}
	source, _, sourceKey, err := readForwardUnixPairLease(ctx, tx, row.data.SourceID)
	if err != nil {
		return err
	}
	storage, _, storageKey, err := readForwardUnixPairLease(ctx, tx, row.data.StorageID)
	if err != nil {
		return err
	}
	if sourceKey != row.data.SourceKey || source.Request.Kind != forwardListenerKind || source.Request.ForwardSource == nil || !reflect.DeepEqual(*source.Request.ForwardSource, row.data.SourceBinding) || source.Request.StorageLease != "" || source.Revision <= row.data.SourceRevision || !validForwardUnixPairClock(source.Clock, 15*time.Minute) || *source.Clock != row.data.SourceClock || source.Host == "" || source.Owner == "" || source.PID != 0 || source.Process != nil || storageKey != row.data.StorageKey || storage.Request.Kind != "storage" || storage.Request.Project != "mesh-forward" || storage.Request.Resources != (model.Resources{Disk: row.data.DiskBytes}) || storage.Request.Reason != "Forward Unix source directory for "+source.ID || !validForwardUnixPairClock(storage.Clock, 7*24*time.Hour) || *storage.Clock != row.data.StorageClock || storage.Host != source.Host || storage.Owner != source.Owner || storage.Revision < row.data.StorageRevision || storage.Artifact == nil || storage.Artifact.Identity == nil || *storage.Artifact.Identity != row.data.StorageArtifact {
		return forwardUnixPairUnavailable()
	}
	return nil
}

// abortForwardUnixPlannedPairs validates every pair before changing any row.
// It runs before generic startup recovery while the daemon lifetime lock is
// held. Native bind intent and bound states are never interpreted as no-bind.
func abortForwardUnixPlannedPairs(ctx context.Context, tx *sql.Tx, home string) error {
	planned, err := readForwardUnixPairRows(ctx, tx)
	if err != nil || len(planned) == 0 {
		return err
	}
	needsProof := false
	for _, row := range planned {
		if row.data.Phase != "planned" && row.data.Phase != "terminal" {
			return forwardUnixPairUnavailable()
		}
		needsProof = needsProof || row.data.Phase == "planned"
	}
	var rootRaw string
	if err = tx.QueryRowContext(ctx, `SELECT CASE WHEN typeof(value)='text' AND length(CAST(value AS BLOB))<=4096 THEN value ELSE '' END FROM metadata WHERE key='storage_root'`).Scan(&rootRaw); err != nil || len(rootRaw) > 4096 {
		return forwardUnixPairUnavailable()
	}
	var root model.FileIdentity
	if json.Unmarshal([]byte(rootRaw), &root) != nil || root.Device == 0 || root.Inode == 0 {
		return forwardUnixPairUnavailable()
	}
	canonicalRoot, err := json.Marshal(root)
	if err != nil || string(canonicalRoot) != rootRaw {
		return forwardUnixPairUnavailable()
	}
	for _, row := range planned {
		if row.data.Phase == "terminal" {
			if err := validateForwardUnixTerminalRow(ctx, tx, row, root); err != nil {
				return err
			}
		}
	}
	if !needsProof {
		return nil
	}
	provider, err := storage.Open(filepath.Join(home, "storage"), &root)
	if err != nil {
		return forwardUnixPairUnavailable()
	}
	providerClosed := false
	defer func() {
		if !providerClosed {
			_ = provider.Close()
		}
	}()
	for i := range planned {
		row := &planned[i]
		if row.data.Phase == "terminal" {
			continue
		}
		source, sourceRaw, sourceKey, err := readForwardUnixPairLease(ctx, tx, row.data.SourceID)
		if err != nil {
			return err
		}
		storage, _, storageKey, err := readForwardUnixPairLease(ctx, tx, row.data.StorageID)
		if err != nil {
			return err
		}
		if sourceKey != row.data.SourceKey || source.Request.Kind != forwardListenerKind || source.Request.ForwardSource == nil || !reflect.DeepEqual(*source.Request.ForwardSource, row.data.SourceBinding) || source.Request.Resources.CPU <= 0 || source.Request.Resources.Memory < 1<<20 || source.Request.Resources.Disk != 0 || len(source.Request.Ports) != 1 || source.Request.StorageLease != storage.ID || source.Revision < row.data.SourceRevision || !validForwardUnixPairClock(source.Clock, 15*time.Minute) || *source.Clock != row.data.SourceClock || source.Expires == nil || !source.Expires.Equal(source.Clock.Wall) || source.PID != 0 || source.Process != nil || !consuming(source.State) || source.Host == "" || source.Owner == "" || storageKey != row.data.StorageKey || storage.Request.Kind != "storage" || storage.Request.Project != "mesh-forward" || storage.Request.Reason != "Forward Unix source directory for "+source.ID || storage.Request.Resources != (model.Resources{Disk: row.data.DiskBytes}) || storage.Revision < row.data.StorageRevision || !validForwardUnixPairClock(storage.Clock, 7*24*time.Hour) || *storage.Clock != row.data.StorageClock || storage.Clock.Boot != source.Clock.Boot || storage.Clock.End-source.Clock.End < int64(forwardUnixCleanupWindow/2) || storage.Host != source.Host || storage.Owner != source.Owner || storage.Artifact == nil || storage.Artifact.Identity == nil || *storage.Artifact.Identity != row.data.StorageArtifact || storage.Artifact.Name != storage.ID || storage.Artifact.Path != storage.ManagedDir || storage.Artifact.Phase != "owned" || storage.State != "active" && storage.State != "needs_attention" || root != row.data.StorageRoot {
			return forwardUnixPairUnavailable()
		}
		proof, err := provider.ProveEmptyDirectory(ctx, *storage.Artifact, row.data.SocketName)
		if err != nil || proof.Identity != row.data.StorageArtifact || proof.Allocated < 0 || proof.Allocated > row.data.DiskBytes {
			return forwardUnixPairUnavailable()
		}
		row.sourceRaw = sourceRaw
		row.proof = proof
	}
	if err = provider.Close(); err != nil {
		return forwardUnixPairUnavailable()
	}
	providerClosed = true
	// Every row is proven planned and internally coherent. The transaction is
	// all-or-nothing: an unknown commit result refuses startup.
	for _, row := range planned {
		if row.data.Phase == "terminal" {
			continue
		}
		terminal := row.data
		terminal.Phase, terminal.Revision = "terminal", 2
		terminal.NoBind = &forwardUnixNoBindProof{Directory: row.proof.Identity, Allocated: row.proof.Allocated, At: time.Now().UTC()}
		terminalRaw, err := json.Marshal(terminal)
		if err != nil || len(terminalRaw) > 4096 {
			return forwardUnixPairUnavailable()
		}
		updatedPair, err := tx.ExecContext(ctx, `UPDATE forward_unix_pairs SET phase='terminal',data=? WHERE source_id=? AND storage_id=? AND phase='planned' AND data=?`, string(terminalRaw), row.data.SourceID, row.data.StorageID, row.encoded)
		if err != nil {
			return err
		}
		n, err := updatedPair.RowsAffected()
		if err != nil || n != 1 {
			return forwardUnixPairUnavailable()
		}
		var source model.Lease
		if json.Unmarshal([]byte(row.sourceRaw), &source) != nil {
			return forwardUnixPairUnavailable()
		}
		source.Request.StorageLease = ""
		source.Revision++
		updatedRaw, err := json.Marshal(source)
		if err != nil {
			return err
		}
		updated, err := tx.ExecContext(ctx, `UPDATE leases SET data=? WHERE id=? AND request_key=? AND data=?`, string(updatedRaw), row.data.SourceID, row.data.SourceKey, row.sourceRaw)
		if err != nil {
			return err
		}
		n, err = updated.RowsAffected()
		if err != nil || n != 1 {
			return forwardUnixPairUnavailable()
		}
		if err = event(tx, "forward.unix_pair_nobind_aborted", row.data.SourceID, terminal); err != nil {
			return err
		}
	}
	return nil
}
