package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/syncfs"
	"getarm.org/internal/syncreconcile"
	"getarm.org/internal/syncscope"
	"getarm.org/internal/syncservice"
)

// A private fixed preparation policy covers bounded intent/manifest decoding,
// reconciliation maps and one streaming fingerprint buffer. This is a
// cooperative promise, not a kernel limit or a measured process RSS claim.
var syncPreparationResources = model.Resources{CPU: 1, Memory: 768 << 20, Disk: 1 << 20}

type syncPreparationGrant struct {
	source, certificate string
	revision            int
	limit               model.Resources
	validUntil          time.Time
}

type syncPreparation struct {
	owner           *RelayTransit
	joined          chan struct{}
	cancel          context.CancelFunc
	nativeJoined    chan struct{}
	chargedScope    *syncDedicatedScope
	chargedOriginal *syncscope.Original
}

// syncOriginalCurrentLocked is the in-memory part of original authority.
// The caller holds Manager.mu; the bound SQL reader checks the physical row.
func (m *Manager) syncOriginalCurrentLocked(owner *RelayTransit) bool {
	return owner != nil && owner.manager == m && owner.ctx != nil && owner.ctx.Err() == nil &&
		!m.closing && !m.draining && m.relayTransits[owner.id] == owner &&
		!owner.settled && owner.recoveryReason == ""
}

// CheckActive and MemoryBytes make only this current, charged preparation
// owner available to the bounded first-frame decoder. The wire frame cannot
// supply or enlarge the memory promise used to parse itself.
func (p *syncPreparation) CheckActive(ctx context.Context) error {
	if p == nil || p.owner == nil || ctx == nil || ctx.Err() != nil || p.owner.ctx.Err() != nil {
		return model.Err("sync_preparation_unavailable", "live charged preparation required before frame decode")
	}
	m := p.owner.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.syncOriginalCurrentLocked(p.owner) {
		return model.Err("sync_preparation_unavailable", "original preparation owner changed")
	}
	l, err := m.get(p.owner.id)
	if err != nil || l.State != "active" || l.Expires == nil || !m.leaseLive(l) || !reflect.DeepEqual(l, p.owner.result) {
		return model.Err("sync_preparation_unavailable", "charged preparation lease changed")
	}
	return nil
}

func (p *syncPreparation) MemoryBytes() int64 {
	if p == nil || p.owner == nil {
		return 0
	}
	return p.owner.result.Request.Resources.Memory
}

func (m *Manager) beginSyncPreparation(ctx context.Context, key string, side syncreconcile.Side, ttl time.Duration, grant *syncPreparationGrant) (*syncPreparation, error) {
	if ctx == nil || ctx.Err() != nil || len(key) < 16 || len(key) > 128 || (side != syncreconcile.Local && side != syncreconcile.Remote) || ttl <= 0 || ttl > 7*24*time.Hour {
		return nil, model.Err("invalid_request", "bounded live sync preparation identity and lifetime required")
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return nil, model.Err("invalid_request", "invalid sync preparation key")
		}
	}
	var scope *syncScopeReservation
	var writer *syncScopeWriter
	if m.syncSchemaVersion == "3" {
		dedicated := m.syncDedicated
		if dedicated == nil || dedicated.gate == nil || dedicated.project == nil || dedicated.selected == nil || dedicated.home == nil {
			return nil, model.Err("sync_evidence_unavailable", "pre-enrolled dedicated native scope is unavailable")
		}
		// Start is delayed in dedicated mode. Collect ordinary live readings
		// now; acquireWithAdmission still applies its normal capacity checks.
		m.sampleCPU()
		m.sampleMemory()
		m.sampleDisk()
		var err error
		writer, err = dedicated.gate.beginWrite(ctx)
		if err != nil {
			return nil, err
		}
		defer writer.release()
		for _, root := range []*syncfs.Root{dedicated.project, dedicated.selected, dedicated.home} {
			if err := root.Check(); err != nil {
				return nil, syncfs.ErrChanged
			}
		}
		if dedicated.home.Path() != m.home || dedicated.chargedHard <= 0 || dedicated.initialUsed < 0 || dedicated.initialUsed > dedicated.chargedHard {
			return nil, model.Err("sync_evidence_unavailable", "dedicated scope binding or hard ceiling differs")
		}
		scope = &syncScopeReservation{ctx: ctx, key: key, side: side, host: m.host, owner: m.owner,
			scopeID: dedicated.scopeID, mountID: dedicated.mountID, projectID: dedicated.projectID,
			projectRootID: dedicated.projectRootID, selectedRootID: dedicated.selectedRootID, homeRootID: dedicated.homeRootID,
			ownerEpoch: dedicated.ownerEpoch, enrollmentDigest: dedicated.enrollmentDigest, ownershipDigest: dedicated.ownershipDigest,
			chargedHard: dedicated.chargedHard, initialUsed: dedicated.initialUsed}
	}
	// Old keys are observations only. Even the scratch probe must not precede
	// this lookup. A prior preparation key cannot be adopted as a new owner.
	m.mu.Lock()
	var n int
	err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_operations WHERE key=? AND side=?`, key, side).Scan(&n)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if n != 0 {
		return nil, syncservice.ErrKeyExists
	}
	if scope != nil {
		m.mu.Lock()
		err = m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_charged_scopes WHERE key=? AND side=?`, key, side).Scan(&n)
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if n != 0 {
			return nil, syncservice.ErrKeyExists
		}
	}
	bounded, cancel := context.WithTimeout(ctx, ttl)
	if grant != nil {
		parentCancel := cancel
		deadlineCtx, deadlineCancel := context.WithDeadline(bounded, grant.validUntil)
		bounded = deadlineCtx
		cancel = func() { deadlineCancel(); parentCancel() }
	}
	if scope != nil {
		scope.ctx = bounded
	}
	joined := make(chan struct{})
	request := model.Request{Host: m.host, Key: mesh.Key(m.host, "sync-preparation\x00"+string(side)+"\x00"+key), Reason: "Prepare reviewed sync " + string(side) + " " + key, TTL: ttl, Resources: syncPreparationResources}
	if scope != nil {
		request.Resources.Disk = scope.chargedHard
	}
	if grant != nil {
		request.Peer, request.PeerCertificate, request.PeerGrantRevision = grant.source, grant.certificate, grant.revision
	}
	owner, err := m.acquireOwnedTransitScope(bounded, request, joined, syncPreparationKind, grant, scope)
	if err != nil {
		cancel()
		return nil, err
	}
	var charged *syncDedicatedScope
	if scope != nil {
		charged = m.syncDedicated
	}
	p := &syncPreparation{owner: owner, joined: joined, cancel: cancel, nativeJoined: make(chan struct{}), chargedScope: charged}
	if scope != nil {
		original, bindErr := syncscope.BindOriginal(bounded, m.db, &m.mu, charged.gate.core, owner.ctx, m.readLeaseClock,
			syncscope.Identity{Key: key, Side: string(side), LeaseID: owner.id, Host: m.host, Owner: m.owner})
		if bindErr == nil {
			m.mu.Lock()
			if !m.syncOriginalCurrentLocked(owner) {
				original.Revoke()
				bindErr = syncscope.ErrOriginal
			} else {
				owner.chargedOriginal = original
			}
			m.mu.Unlock()
		}
		if bindErr != nil {
			m.mu.Lock()
			owner.recoveryReason = "Charged original binding failed after lease commit; preserve the exact scope for review"
			m.mu.Unlock()
			cancel()
			close(joined)
			<-owner.workerDone
			return nil, model.Err("sync_evidence_unavailable", "original charged scope binding failed after commit")
		}
		p.chargedOriginal = original
	}
	return p, nil
}

// verifyChargedOriginal is the concrete native bridge prerequisite. It can
// inspect only the exact in-process original preparation; callers must release
// their writer before invoking it. Public admission remains fenced until the
// dedicated pre-enrollment and complete writer inventory are accepted.
func (p *syncPreparation) verifyChargedOriginal(ctx context.Context, key string, side syncreconcile.Side) (syncfs.CompleteScopeProof, error) {
	if p == nil || p.chargedOriginal == nil || p.chargedScope == nil || ctx == nil || ctx.Err() != nil {
		return syncfs.CompleteScopeProof{}, model.Err("sync_evidence_unavailable", "charged original owner unavailable")
	}
	if err := p.CheckActive(ctx); err != nil {
		return syncfs.CompleteScopeProof{}, err
	}
	r := p.chargedOriginal.Record()
	if r.Key != key || r.Side != string(side) || r.LeaseID != p.owner.id {
		return syncfs.CompleteScopeProof{}, model.Err("sync_evidence_unavailable", "charged original identity differs")
	}
	claim, err := syncfs.NewScopeClaim(syncfs.ScopeClaimFields{
		Key: r.Key, Side: r.Side, PreparationLease: r.LeaseID,
		Provider: r.Provider, ScopeID: r.ScopeID, MountID: r.MountID,
		ProjectRootID: r.ProjectRootID, SelectedRootID: r.SelectedRootID, ParentRootID: r.HomeRootID,
		OwnershipDigest: r.OwnershipDigest, ProjectID: uint32(r.ProjectID),
		OwnerEpoch: uint64(r.OwnerEpoch), ChargedHardBytes: r.ChargedHard,
	})
	if err != nil {
		return syncfs.CompleteScopeProof{}, err
	}
	owner, err := syncfs.NewBoundOriginalScopeOwner(ctx, claim, p.chargedOriginal,
		p.chargedScope.project, p.chargedScope.selected, p.chargedScope.home)
	if err != nil {
		return syncfs.CompleteScopeProof{}, err
	}
	return syncfs.VerifyCompleteScope(ctx, claim, owner,
		p.chargedScope.project, p.chargedScope.selected, p.chargedScope.home)
}

// beginChargedEffect is the writer-release -> verify -> fresh-writer handoff.
// It is a private prerequisite; no public workflow calls it yet. The returned
// hold must cover the native effect and be closed after that effect joins.
func (p *syncPreparation) beginChargedEffect(ctx context.Context, proof syncfs.CompleteScopeProof) (*syncChargedEffect, error) {
	if p == nil || p.chargedOriginal == nil || p.chargedScope == nil || ctx == nil || ctx.Err() != nil {
		return nil, model.Err("sync_evidence_unavailable", "fresh charged writer requires exact prior verification")
	}
	if err := p.CheckActive(ctx); err != nil {
		return nil, err
	}
	if proof.VerificationToken() == nil {
		return nil, model.Err("sync_evidence_unavailable", "fresh charged writer requires exact prior verification")
	}
	r := p.chargedOriginal.Record()
	claim, err := syncfs.NewScopeClaim(syncfs.ScopeClaimFields{
		Key: r.Key, Side: r.Side, PreparationLease: r.LeaseID,
		Provider: r.Provider, ScopeID: r.ScopeID, MountID: r.MountID,
		ProjectRootID: r.ProjectRootID, SelectedRootID: r.SelectedRootID, ParentRootID: r.HomeRootID,
		OwnershipDigest: r.OwnershipDigest, ProjectID: uint32(r.ProjectID),
		OwnerEpoch: uint64(r.OwnerEpoch), ChargedHardBytes: r.ChargedHard,
	})
	if err != nil {
		return nil, err
	}
	binding, err := proof.BindingFor(ctx, claim)
	if err != nil || binding.HardBytes() != uint64(r.ChargedHard) {
		return nil, model.Err("sync_evidence_unavailable", "scope proof differs from charged original")
	}
	writer, err := p.chargedScope.gate.beginWrite(ctx)
	if err != nil {
		return nil, err
	}
	if !writer.core.Follows(proof.VerificationToken()) {
		writer.release()
		return nil, model.Err("sync_evidence_unavailable", "another native writer followed the scope proof")
	}
	hold, err := p.chargedOriginal.HoldWriter(ctx, writer.core)
	if err != nil {
		writer.release()
		return nil, err
	}
	closeWithError := func(err error) (*syncChargedEffect, error) {
		hold.Release()
		writer.release()
		return nil, err
	}
	for _, root := range []*syncfs.Root{p.chargedScope.project, p.chargedScope.selected, p.chargedScope.home} {
		if root == nil || root.Check() != nil {
			return closeWithError(syncfs.ErrChanged)
		}
	}
	projectID, err := p.chargedScope.project.NativeID()
	if err != nil || projectID != r.ProjectRootID {
		return closeWithError(syncfs.ErrChanged)
	}
	selectedID, err := p.chargedScope.selected.NativeID()
	if err != nil || selectedID != r.SelectedRootID {
		return closeWithError(syncfs.ErrChanged)
	}
	homeID, err := p.chargedScope.home.NativeID()
	if err != nil || homeID != r.HomeRootID {
		return closeWithError(syncfs.ErrChanged)
	}
	quota, err := syncfs.ProbeNativeQuota(ctx, p.chargedScope.selected, p.chargedScope.home)
	if err != nil || quota.Provider() != r.Provider || quota.Scope() != r.ScopeID || quota.Mount() != r.MountID ||
		quota.HardBytes() != uint64(r.ChargedHard) || quota.UsedBytes() > quota.HardBytes() {
		return closeWithError(model.Err("sync_evidence_unavailable", "native project quota changed before effect"))
	}
	if err := hold.Check(ctx); err != nil {
		return closeWithError(err)
	}
	return &syncChargedEffect{writer: writer, hold: hold}, nil
}

type syncChargedEffect struct {
	writer *syncScopeWriter
	hold   *syncscope.WriterHold
}

func (e *syncChargedEffect) close() {
	if e != nil {
		if e.hold != nil {
			e.hold.Release()
		}
		if e.writer != nil {
			e.writer.release()
		}
	}
}

func (p *syncPreparation) scratchUncertain() {
	if p.chargedOriginal != nil {
		p.chargedOriginal.Revoke()
	}
	p.owner.manager.mu.Lock()
	p.owner.recoveryReason = "Sync preparation scratch cleanup was not positively verified; preserve the charged preparation for exact recovery"
	p.owner.manager.mu.Unlock()
}

func (p *syncPreparation) scratchClean() {
	p.owner.manager.mu.Lock()
	p.owner.recoveryReason = ""
	p.owner.manager.mu.Unlock()
}

// All preparation work is synchronous on the original caller. Closing joined
// happens only after its final native call has returned, including errors.
// Cancellation, expiry and End merely cancel; they cannot release the hold.
func (p *syncPreparation) finish() error {
	p.cancel()
	if p.chargedOriginal != nil {
		p.chargedOriginal.Revoke()
	}
	if p.chargedScope != nil {
		m := p.owner.manager
		m.mu.Lock()
		if !p.owner.settled && p.owner.recoveryReason == "" {
			p.owner.recoveryReason = "Charged original native scope is unresolved until exact provider and final-lease settlement"
		}
		m.mu.Unlock()
	}
	close(p.joined)
	_, err := p.owner.FinishAfterJoin()
	return err
}

// Native volume evidence is checked before and after the scan and once more
// in the final admission transaction. The reported pool label (including an
// APFS container when available) is bound to that same state filesystem.
func (m *Manager) syncVolume(root, parent *syncfs.Root) (string, error) {
	native, err := syncNativeVolume(m.home, root, parent)
	if err != nil {
		return "", err
	}
	m.observationMu.RLock()
	reading := m.diskAvailability
	m.observationMu.RUnlock()
	if reading.Volume == "" || reading.Path != m.home || !strings.HasSuffix(native, "/"+reading.Volume) {
		return "", model.Err("volume_unavailable", "disk observation is not bound to the held state filesystem")
	}
	return fmt.Sprintf("%s/pool:%s", native, reading.Pool), nil
}

func syncNativeVolume(home string, root, parent *syncfs.Root) (string, error) {
	if root == nil || parent == nil {
		return "", syncfs.ErrChanged
	}
	if err := root.Check(); err != nil {
		return "", err
	}
	if err := parent.Check(); err != nil {
		return "", err
	}
	if parent.Path() != home || root.Path() == home || strings.HasPrefix(home, root.Path()+string(os.PathSeparator)) {
		return "", model.Err("forbidden", "sync workspace parent must be Manager's private home outside the selected root")
	}
	var selected, work, state unix.Stat_t
	if err := unix.Fstat(root.FD(), &selected); err != nil {
		return "", err
	}
	if err := unix.Fstat(parent.FD(), &work); err != nil {
		return "", err
	}
	if err := unix.Stat(home, &state); err != nil {
		return "", err
	}
	if selected.Dev != work.Dev || work.Dev != state.Dev || work.Ino != state.Ino || selected.Ino == work.Ino || selected.Uid != uint32(os.Geteuid()) || work.Uid != uint32(os.Geteuid()) || work.Mode&0077 != 0 {
		return "", model.Err("volume_unavailable", "selected root and private workspace are not on the charged filesystem")
	}
	var selectedFS, workFS unix.Statfs_t
	if err := unix.Fstatfs(root.FD(), &selectedFS); err != nil {
		return "", err
	}
	if err := unix.Fstatfs(parent.FD(), &workFS); err != nil {
		return "", err
	}
	if !reflect.DeepEqual(selectedFS.Fsid, workFS.Fsid) {
		return "", model.Err("volume_unavailable", "selected root and workspace filesystem identities differ")
	}

	return fmt.Sprintf("dev:%d/fsid:%x", selected.Dev, selectedFS.Fsid.Val), nil
}

// The original synchronous native phase has returned. The small retained
// intent/manifest working set must fit the final promise before handoff.
func (p *syncPreparation) nativeFinished() { close(p.nativeJoined) }

// Manager.mu is held. Only the exact still-active original owner can lend its
// accounting slot to the single atomic final transaction. No durable resource
// is released here, and ordinary callers cannot supply this participant.
func (s *syncAdmission) withoutPreparation(leases []model.Lease) ([]model.Lease, error) {
	p := s.preparation
	if p == nil || p.owner.manager != s.manager {
		return nil, model.Err("sync_preparation_unavailable", "exact preparation owner required")
	}
	select {
	case <-p.nativeJoined:
	default:
		return nil, model.Err("sync_preparation_unjoined", "native preparation has not returned")
	}
	h := p.owner
	if h.ctx.Err() != nil || h.recoveryReason != "" || h.settled || s.manager.relayTransits[h.id] != h {
		return nil, model.Err("sync_preparation_unavailable", "preparation cancelled, changed or unverified")
	}
	out := make([]model.Lease, 0, len(leases))
	found := false
	for _, l := range leases {
		if l.ID == h.id {
			if l.State != "active" || !s.manager.leaseLive(l) || l.Revision != h.result.Revision || !reflect.DeepEqual(l.Request, h.result.Request) || l.Owner != h.result.Owner || l.Host != h.result.Host {
				return nil, model.Err("sync_preparation_unavailable", "preparation allocation changed")
			}
			found = true
			continue
		}
		out = append(out, l)
	}
	if !found {
		return nil, model.Err("sync_preparation_unavailable", "preparation allocation missing")
	}
	return out, nil
}

func (s *syncAdmission) handoffTx(tx *sql.Tx, final model.Lease) error {
	h := s.preparation.owner
	var raw string
	if err := tx.QueryRowContext(s.ctx, `SELECT data FROM leases WHERE id=?`, h.id).Scan(&raw); err != nil {
		return err
	}
	var previous model.Lease
	if err := json.Unmarshal([]byte(raw), &previous); err != nil {
		return err
	}
	if !reflect.DeepEqual(previous, h.result) || previous.State != "active" || h.ctx.Err() != nil || h.recoveryReason != "" {
		return model.Err("sync_preparation_unavailable", "exact preparation predecessor changed before handoff")
	}
	sample, err := s.manager.readLeaseClock()
	if err != nil {
		return err
	}
	for _, lease := range []model.Lease{previous, final} {
		remaining, err := lease.Clock.Remaining(sample, lease.Expires)
		if err != nil || remaining <= 0 {
			return model.Err("sync_preparation_unavailable", "elapsed handoff entitlement unavailable")
		}
	}
	joined := previous
	now := time.Now().UTC()
	joined.State, joined.Outcome, joined.Ended = "terminal", "completed", &now
	joined.Revision++
	encoded, err := json.Marshal(joined)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(s.ctx, `UPDATE leases SET data=? WHERE id=? AND data=?`, string(encoded), h.id, raw)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return model.Err("sync_preparation_unavailable", "preparation handoff outcome changed")
	}
	if err = event(tx, "sync.preparation_handoff", h.id, struct{ Preparation, Lease string }{h.id, final.ID}); err != nil {
		return err
	}
	s.handedOff = joined
	return nil
}

// Called only after the complete final transaction is durably acknowledged.
// A failed/unknown commit never promotes the cached preparation state.
func (s *syncAdmission) acknowledgeHandoff() {
	h := s.preparation.owner
	h.result, h.settled = s.handedOff, true
	delete(s.manager.relayTransits, h.id)
}
