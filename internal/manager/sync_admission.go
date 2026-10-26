package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sshservice"
	"github.com/yrashk/arm/internal/syncfs"
	"github.com/yrashk/arm/internal/syncreconcile"
	"github.com/yrashk/arm/internal/syncservice"
	"golang.org/x/crypto/ssh"
)

// syncWorkerCommand is the only command admitted by this private adapter.
// There is no public dispatch caller until the fixed worker has been reviewed.
const syncWorkerCommand = syncservice.FixedWorkerCommand

// SyncEndpointID binds a selected endpoint to the Manager's stable host and
// effective account. The matching native root identity remains separate.
func SyncEndpointID(host, uid, account string) string {
	return fmt.Sprintf("arm-sync/v1/%s/%s/%s", host, uid, account)
}

type syncAdmission struct {
	inline      bool
	preparation *syncPreparation
	handedOff   model.Lease
	manager     *Manager
	ctx         context.Context
	intent      syncservice.Intent
	rows        []syncservice.ManifestRow
	observed    syncreconcile.Snapshot
	root        *syncfs.Root
	parent      *syncfs.Root
	volume      string
}

func (s *syncAdmission) reserveTx(tx *sql.Tx, l model.Lease) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := s.root.Check(); err != nil {
		return err
	}
	if err := s.parent.Check(); err != nil {
		return err
	}
	if volume, err := s.manager.syncVolume(s.root, s.parent); err != nil || volume != s.volume {
		return model.Err("volume_unavailable", "charged sync volume identity changed before admission")
	}
	if err := syncservice.ReserveTx(s.ctx, tx, s.intent, s.rows, s.observed); err != nil {
		return err
	}
	if _, err := tx.ExecContext(s.ctx, `INSERT INTO sync_lease_bindings(lease,key,side,volume,disk_bytes) VALUES(?,?,?,?,?)`, l.ID, s.intent.Key, s.intent.Side, s.volume, l.Request.Resources.Disk); err != nil {
		return err
	}
	if _, err := tx.ExecContext(s.ctx, `INSERT OR IGNORE INTO metadata(key,value) VALUES('sync_used','1')`); err != nil {
		return err
	}
	if err := s.handoffTx(tx, l); err != nil {
		return err
	}
	return event(tx, "sync.reserved", l.ID, s.intent)
}

// prepareSyncAdmission derives observation from a held native root. Both the
// selected root and private workspace parent must be on Manager's sampled
// state filesystem; a different volume has no authoritative disk budget yet.
func (m *Manager) prepareSyncAdmission(ctx context.Context, i syncservice.Intent, rows []syncservice.ManifestRow, root, parent *syncfs.Root, prep *syncPreparation) (*syncAdmission, error) {
	if ctx == nil || root == nil || parent == nil || ctx.Err() != nil {
		return nil, model.Err("invalid_request", "live sync context and held roots required")
	}
	if err := i.Validate(); err != nil {
		return nil, err
	}
	if i.Binding.SelectionID != "all" || i.Binding.MetadataID != "content" {
		return nil, model.Err("unsupported_policy", "only complete content-only selection is admitted")
	}
	volume, err := m.syncVolume(root, parent)
	if err != nil {
		return nil, err
	}
	actualID, err := root.NativeID()
	if err != nil {
		return nil, err
	}
	wantID := i.Binding.Local.RootID
	resources := i.LocalResources
	if i.Side == syncreconcile.Remote {
		wantID = i.Binding.Remote.RootID
		resources = i.RemoteResources
	}
	if actualID != wantID {
		return nil, syncfs.ErrChanged
	}
	if err := syncPhysicalPromise(i, rows, resources); err != nil {
		return nil, err
	}
	prep.scratchUncertain()
	names, err := syncfs.DetectNamePolicy(root, parent)
	if err != nil {
		return nil, err
	}
	prep.scratchClean()
	candidates := make([]string, 0, len(rows))
	for _, row := range rows {
		candidates = append(candidates, row.Path)
	}
	observed, err := root.Scan(ctx, candidates, i.Binding.Exclusions, names, syncfs.Metadata{}, syncfs.ScanLimits{Entries: syncreconcile.MaxEntries, Bytes: 1 << 40})
	if err != nil {
		return nil, err
	}
	if err := root.Check(); err != nil {
		return nil, err
	}
	if current, err := m.syncVolume(root, parent); err != nil || current != volume {
		return nil, model.Err("volume_unavailable", "sync volume changed during preparation")
	}
	return &syncAdmission{preparation: prep, manager: m, ctx: ctx, intent: i, rows: rows, observed: observed, root: root, parent: parent, volume: volume}, nil
}

// The footprint check is a conservative admission floor for one retained
// stage, one rsync temporary copy and one preimage, with block/inode slack.
// Runtime workspace preflight must still enforce the actual charged limit.
func syncPhysicalPromise(i syncservice.Intent, rows []syncservice.ManifestRow, resources model.Resources) error {
	if len(rows) > syncreconcile.MaxEntries*6 {
		return model.Err("invalid_request", "sync manifest row limit exceeded")
	}
	var bytes int64 = 1 << 20
	for _, a := range i.Actions {
		if a.Source == i.Side && a.After != nil {
			size := a.After.Size
			if a.After.Kind == syncreconcile.Directory {
				size = 0
			}
			if size < 0 || bytes > math.MaxInt64-65536 || size > math.MaxInt64-bytes-65536 {
				return model.Err("invalid_request", "outgoing source stage footprint overflows")
			}
			bytes += size + 65536
		}
		if a.Side != i.Side {
			continue
		}
		if bytes > math.MaxInt64-65536 {
			return model.Err("invalid_request", "receiver inode footprint overflows")
		}
		bytes += 65536
		for _, entry := range []*syncreconcile.Entry{a.Before, a.After} {
			if entry == nil || entry.Kind == syncreconcile.Directory {
				continue
			}
			if entry.Size < 0 || entry.Size > (math.MaxInt64-bytes)/4-65536 {
				return model.Err("invalid_request", "sync physical footprint overflows")
			}
			bytes += 4*entry.Size + 65536
		}
	}
	manifest, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	record, err := json.Marshal(i)
	if err != nil {
		return err
	}
	controlBytes := int64(len(manifest)) + int64(len(record))
	if len(manifest) > syncservice.MaxManifestBytes || len(record) > syncservice.MaxRecordBytes || resources.Memory < 4*controlBytes+128<<10 {
		return model.Err("insufficient_capacity", "sync control manifest exceeds reserved memory or byte limit")
	}
	if controlBytes > (math.MaxInt64-bytes)/4 {
		return model.Err("invalid_request", "sync control footprint overflows")
	}
	bytes += 4 * controlBytes
	if resources.Disk < bytes {
		return model.Err("insufficient_capacity", "sync disk promise does not cover staged, temporary and preimage allocation floor")
	}
	return nil
}

// ReserveSyncSource commits the source intention and resource lease together
// before any caller may open a network stream. It never replays an old key.
func (m *Manager) ReserveSyncSource(ctx context.Context, i syncservice.Intent, rows []syncservice.ManifestRow, root, parent *syncfs.Root, control model.SSHControlCommitment) (out model.Lease, err error) {
	var empty model.Lease
	if i.Side != syncreconcile.Local || control.Validate() != nil || control.Source != m.host || control.Target == m.host || control.Key != i.Key || control.CommandSHA256 != i.WorkerCommandSHA256 || control.Resources != i.RemoteResources || i.WorkerCommandSHA256 != sshDigest([]byte(syncWorkerCommand)) || i.Binding.Local.ID != SyncEndpointID(m.host, m.owner, m.ownerUsername) || i.Binding.Remote.ID != SyncEndpointID(control.Target, control.Owner, control.Account) {
		return empty, model.Err("invalid_request", "source sync identity or fixed worker differs from reviewed control")
	}
	digest, err := control.Digest()
	if err != nil || digest != i.SSHControlSHA256 {
		return empty, model.Err("invalid_request", "source sync control digest differs")
	}
	prep, err := m.beginSyncPreparation(ctx, i.Key, i.Side, control.TTL, nil)
	if err != nil {
		return empty, err
	}
	defer func() { err = errors.Join(err, prep.finish()) }()
	prepCtx := prep.owner.Context()
	admission, err := m.prepareSyncAdmission(prepCtx, i, rows, root, parent, prep)
	if err != nil {
		return empty, err
	}
	prep.nativeFinished()
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := (&syncservice.Ledger{DB: m.db, Key: i.Key, Side: i.Side}).Lookup(ctx); err == nil {
		return empty, syncservice.ErrKeyExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return empty, err
	}
	r := model.Request{Host: m.host, Key: mesh.Key(m.host, "sync-source\x00"+i.Key), Reason: i.Reason, Kind: "execution", TTL: control.TTL, Resources: i.LocalResources, Peer: control.Target, OriginKey: i.Key}
	var prior int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM leases WHERE request_key=?`, r.Key).Scan(&prior); err != nil {
		return empty, err
	}
	if prior != 0 {
		return empty, model.Err("sync_evidence_unavailable", "source lease exists without its original sync operation")
	}
	return m.acquireWithAdmission(r, nil, false, admission)
}

// AcquireSyncSSHCommitted is a private service adapter for an authenticated
// destination channel. It joins the exact fixed worker, SSH fence/operation,
// ordinary lease and sync reservation in the one admission transaction.
// No public handler calls it until the worker and recovery path are reviewed.
func (m *Manager) AcquireSyncSSHCommitted(ctx context.Context, i syncservice.Intent, rows []syncservice.ManifestRow, root, parent *syncfs.Root, q model.SSHExecutionRequest, e sshservice.Execution, ch ssh.Channel, limit model.Resources, maxTTL time.Duration, validUntil time.Time, control model.SSHControlCommitment) (out model.Lease, process sshservice.Process, err error) {
	var empty model.Lease
	digest, err := control.Digest()
	if err != nil || !control.Matches(q) || digest != i.SSHControlSHA256 || i.Side != syncreconcile.Remote || i.Key != q.Key || i.SSHKey != q.Key || i.Reason != q.Reason || i.RemoteResources != q.Resources || i.WorkerCommandSHA256 != q.CommandSHA256 || e.Kind != "exec" || e.Command != syncWorkerCommand || e.Terminal != nil || q.CWD != rootPath(root) || i.Binding.Remote.ID != SyncEndpointID(m.host, m.owner, m.ownerUsername) || !strings.HasPrefix(i.Binding.Local.ID, "arm-sync/v1/"+q.Source+"/") {
		return empty, nil, model.Err("invalid_request", "destination sync identity, resource or fixed worker commitment differs")
	}
	if control.Owner != m.owner || q.Target != m.host || q.Account != m.ownerUsername || q.TTL <= 0 || q.TTL > maxTTL || !q.Resources.Fits(limit) || validUntil.IsZero() || !time.Now().Before(validUntil) {
		return empty, nil, model.Err("forbidden", "sync destination grant is unavailable")
	}
	prep, err := m.beginSyncPreparation(ctx, i.Key, i.Side, control.TTL, &syncPreparationGrant{source: q.Source, certificate: q.Certificate, revision: q.Revision, limit: limit, validUntil: validUntil})
	if err != nil {
		return empty, nil, err
	}
	defer func() { err = errors.Join(err, prep.finish()) }()
	prepCtx := prep.owner.Context()
	admission, err := m.prepareSyncAdmission(prepCtx, i, rows, root, parent, prep)
	if err != nil {
		return empty, nil, err
	}
	prep.nativeFinished()
	return m.acquireSSH(ctx, q, e, ch, limit, maxTTL, validUntil, &control, admission)
}

// AdmitSyncWorker reserves the fixed in-process destination worker. It never
// launches a shell or reconstructs authority from the stored SSH descriptor.
// The caller must Start the returned hold with the reviewed syncworker runner
// before accepting the SSH exec request.
func (m *Manager) AdmitSyncWorker(ctx context.Context, i syncservice.Intent, rows []syncservice.ManifestRow, root, parent *syncfs.Root, q model.SSHExecutionRequest, e sshservice.Execution, ch ssh.Channel, limit model.Resources, maxTTL time.Duration, validUntil time.Time, control model.SSHControlCommitment) (out model.Lease, hold *SyncWorkerHold, err error) {
	var empty model.Lease
	if i.Side != syncreconcile.Remote || e.Kind != "exec" || e.Command != syncWorkerCommand || e.Terminal != nil || q.CWD != rootPath(root) {
		return empty, nil, model.Err("invalid_request", "fixed destination worker required")
	}
	digest, err := control.Digest()
	if err != nil || !control.Matches(q) || digest != i.SSHControlSHA256 || i.Key != q.Key || i.SSHKey != q.Key || i.Reason != q.Reason || i.RemoteResources != q.Resources || i.WorkerCommandSHA256 != q.CommandSHA256 || i.Binding.Remote.ID != SyncEndpointID(m.host, m.owner, m.ownerUsername) || !strings.HasPrefix(i.Binding.Local.ID, "arm-sync/v1/"+q.Source+"/") {
		return empty, nil, model.Err("invalid_request", "destination worker intent differs from authenticated SSH control")
	}
	if control.Owner != m.owner || q.Target != m.host || q.Account != m.ownerUsername || q.TTL <= 0 || q.TTL > maxTTL || !q.Resources.Fits(limit) || validUntil.IsZero() || !time.Now().Before(validUntil) {
		return empty, nil, model.Err("forbidden", "sync destination grant is unavailable")
	}
	prep, err := m.beginSyncPreparation(ctx, i.Key, i.Side, control.TTL, &syncPreparationGrant{source: q.Source, certificate: q.Certificate, revision: q.Revision, limit: limit, validUntil: validUntil})
	if err != nil {
		return empty, nil, err
	}
	defer func() {
		if finishErr := prep.finish(); finishErr != nil {
			if hold != nil {
				hold.JoinTransport()
				hold = nil
			}
			err = errors.Join(err, finishErr)
		}
	}()
	admission, err := m.prepareSyncAdmission(prep.owner.Context(), i, rows, root, parent, prep)
	if err != nil {
		return empty, nil, err
	}
	prep.nativeFinished()
	admission.inline = true
	l, process, err := m.acquireSSH(ctx, q, e, ch, limit, maxTTL, validUntil, &control, admission)
	if err != nil {
		return l, nil, err
	}
	hold, ok := process.(*SyncWorkerHold)
	if !ok || hold == nil {
		return l, nil, model.Err("sync_evidence_unavailable", "in-process worker admission lost its exact hold")
	}
	return l, hold, nil
}

func rootPath(root *syncfs.Root) string {
	if root == nil {
		return ""
	}
	return root.Path()
}

// syncLeaseBound is a retention fence. No generic End or process exit may
// release a sync promise until exact native cleanup has its own proof path.
func (m *Manager) syncLeaseBound(id string) (bool, error) {
	var n int
	err := m.db.QueryRow(`SELECT COUNT(*) FROM sync_lease_bindings WHERE lease=?`, id).Scan(&n)
	return n == 1, err
}

func (m *Manager) retainSyncLease(l *model.Lease, outcome string) error {
	l.State = "needs_attention"
	l.Outcome = outcome
	l.Ended = nil
	l.Failure = "Sync effects, workspace and receipt state require exact-key recovery before resources can be released"
	l.Revision++
	return m.put(*l, "sync.recovery_required")
}
