package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharelease"
)

type sharePublicationChange struct {
	old                                   *sharelease.Publication
	next                                  sharelease.Publication
	raw, journal                          []byte
	capacity                              sharelease.JournalCapacity
	manifestRaw, manifestStored, evidence int64
	sequence                              int64
}

func sharePublicationBytes(p sharelease.Publication, raw []byte) int64 {
	if p.Retired == nil {
		return sharelease.MaxRecordBytes
	}
	return int64(len(raw))
}
func (m *Manager) planSharePublication(next sharelease.Publication, transition string) (*sharePublicationChange, error) {
	if err := m.sharePolicyError(); err != nil {
		return nil, err
	}
	if err := next.Validate(); err != nil {
		return nil, model.Err("share_evidence_unavailable", err.Error())
	}
	b := m.shares
	change := &sharePublicationChange{next: next, manifestRaw: b.state.ManifestRaw, manifestStored: b.state.ManifestStored, evidence: b.state.EvidenceReserved}
	raw, err := sharelease.Canonical(next)
	if err != nil {
		return nil, err
	}
	if transition == "reserved" || transition == "verify_reserved" {
		final := next
		final.Reason = strings.Repeat("x", 256)
		final.Failure = strings.Repeat("x", 3700)
		finalRaw, e := sharelease.Canonical(final)
		// Timestamps, revision digits and a maximum summary add under1KiB.
		// The diagnostic is bounded by encoded JSON bytes in shareDiagnostic.
		if e != nil || len(finalRaw)+1024 > sharelease.MaxRecordBytes {
			return nil, model.Err("share_capacity_exceeded", "publication intent lacks bounded diagnostic/finalization record headroom; no native work started")
		}
	}
	change.raw = raw
	old, found := b.state.Publications[next.ID]
	oldSlots := 0
	before := ""
	if found {
		if old.Retired != nil || next.Revision != old.Revision+1 || next.Authority != old.Authority || next.Owner != old.Owner || next.ID != old.ID || next.Generation != old.Generation || next.Request != old.Request || next.Lease != old.Lease || next.StorageIdentity != old.StorageIdentity || next.PreparationWorker != old.PreparationWorker || !next.Created.Equal(old.Created) || !next.Expires.Equal(old.Expires) || next.Updated.Before(old.Updated) {
			return nil, model.Err("revision_conflict", "publication immutable scope or revision changed")
		}
		if !reflect.DeepEqual(old.Publish, next.Publish) && (old.Publish != nil || transition != "verify_reserved") || (next.Worker != old.Worker || next.WorkerPhase != old.WorkerPhase) && transition != "verify_reserved" {
			return nil, model.Err("share_evidence_unavailable", "immutable publication intent or phase worker changed")
		}
		if err := sharePreparationTransition(old, next, transition); err != nil {
			return nil, err
		}
		sameTime := func(before, after *time.Time) bool { return before == nil || after != nil && before.Equal(*after) }
		if !sameTime(old.Prepared, next.Prepared) || !sameTime(old.Published, next.Published) || !sameTime(old.Retired, next.Retired) || old.Summary != nil && (next.Summary == nil || *old.Summary != *next.Summary) {
			return nil, model.Err("share_evidence_unavailable", "immutable preparation/publication receipt changed")
		}
		oldRaw, err := sharelease.Canonical(old)
		if err != nil {
			return nil, err
		}
		change.old = &old
		before = sshDigest(oldRaw)
		oldSlots = old.JournalSlots
		r, s, e := sharelease.ManifestReservation(old)
		if e != nil {
			return nil, e
		}
		change.manifestRaw -= r
		change.manifestStored -= s
		change.evidence -= sharePublicationBytes(old, oldRaw)
	} else if len(b.state.Publications) >= sharelease.MaxPublications || next.Revision != 1 || next.State != "preparing" || next.WorkerState != "reserved" || next.JournalSlots != sharelease.PublicationFinalSlots || transition != "reserved" {
		return nil, model.Err("share_capacity_exceeded", "retained publication history is full or initial admitted scope differs; no native work started")
	}
	r, s, err := sharelease.ManifestReservation(next)
	if err != nil {
		return nil, err
	}
	change.manifestRaw += r
	change.manifestStored += s
	change.evidence += sharePublicationBytes(next, raw)
	if change.manifestRaw > sharelease.MaxManifestRawBytes || change.evidence > sharelease.MaxEvidenceBytes {
		return nil, model.Err("share_capacity_exceeded", "complete retained manifest/evidence capacity is reserved; retirements keep audit manifests and no truncation/purge is automatic")
	}
	journal := sharelease.Journal{Schema: 1, Authority: m.host, Owner: m.owner, At: next.Updated, Actor: "uid:" + m.owner, Reason: next.Reason, Entity: "publication", ID: next.ID, Revision: next.Revision, Transition: transition, BeforeSHA256: before, AfterSHA256: sshDigest(raw)}
	change.journal, err = sharelease.EncodeJournal(journal)
	if err != nil {
		return nil, err
	}
	change.capacity, err = b.state.Capacity.Change(len(change.journal), oldSlots, next.JournalSlots)
	if err != nil {
		return nil, model.Err("share_capacity_exceeded", err.Error()+"; required finalization space must be reserved before work")
	}
	return change, nil
}
func (c *sharePublicationChange) write(ctx context.Context, tx *sql.Tx, initialize bool, parts []sharelease.ManifestPart) error {
	if initialize {
		if err := sharelease.Initialize(ctx, tx); err != nil {
			return err
		}
	}
	if c.old == nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO share_publications(id,data) VALUES(?,?)`, c.next.ID, string(c.raw)); err != nil {
			return err
		}
	} else {
		before, err := sharelease.Canonical(*c.old)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE share_publications SET data=? WHERE id=? AND data=?`, string(c.raw), c.next.ID, string(before))
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return model.Err("revision_conflict", "publication changed before exact transaction commit")
		}
	}
	if len(parts) > 0 {
		if c.next.Summary == nil || c.old == nil || c.old.Summary != nil || len(parts) != c.next.Summary.Parts {
			return model.Err("share_evidence_unavailable", "unexpected manifest publication scope")
		}
		stored := 0
		for i, p := range parts {
			if p.Share != c.next.ID || p.Generation != c.next.Generation || p.Index != i || len(p.Data) > sharelease.MaxManifestEncodedPartBytes {
				return model.Err("share_evidence_unavailable", "exact bounded manifest part roster differs")
			}
			stored += len(p.Data)
			if _, err := tx.ExecContext(ctx, `INSERT INTO share_manifest_parts(share,generation,part,data) VALUES(?,?,?,?)`, p.Share, p.Generation, p.Index, p.Data); err != nil {
				return err
			}
		}
		if stored != c.next.Summary.StoredBytes {
			return model.Err("share_evidence_unavailable", "manifest storage expansion differs")
		}
	} else if c.next.Summary != nil && (c.old == nil || c.old.Summary == nil) {
		return model.Err("share_evidence_unavailable", "prepared receipt missing its complete manifest")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO share_journal(data) VALUES(?)`, string(c.journal))
	if err != nil {
		return err
	}
	c.sequence, err = result.LastInsertId()
	return err
}
func (m *Manager) applySharePublication(c *sharePublicationChange) {
	b := m.shares
	b.state.Enabled = true
	b.state.Publications[c.next.ID] = c.next
	b.state.Capacity = c.capacity
	b.state.ManifestRaw = c.manifestRaw
	b.state.ManifestStored = c.manifestStored
	b.state.EvidenceReserved = c.evidence
	b.byLease[c.next.Lease] = c.next.ID
	var j sharelease.Journal
	_ = json.Unmarshal(c.journal, &j)
	b.state.Journal = append(b.state.Journal, sharelease.JournalRow{Sequence: c.sequence, Record: j, RawBytes: len(c.journal)})
}

// putSharePublication joins ordinary lease state/event + policy journal + exact
// manifest parts in one transaction. No native I/O, hashes or codec validation
// run here; preparation supplied the runtime-only off-lock verified result.
func (m *Manager) putSharePublication(p sharelease.Publication, l model.Lease, transition string, parts []sharelease.ManifestPart) error {
	change, err := m.planSharePublication(p, transition)
	if err != nil {
		return err
	}
	if change.old == nil {
		return fmt.Errorf("new publication must use ordinary atomic admission")
	}
	oldLease, err := m.get(l.ID)
	if err != nil {
		return err
	}
	if transition == "retired" && (oldLease.State != "ending" || !validShareEndOutcome(oldLease.Outcome) || l.Outcome != oldLease.Outcome) {
		return model.Err("share_evidence_unavailable", "retirement must preserve its exact committed ordinary end decision")
	}
	if oldLease.Revision+1 != l.Revision || oldLease.ID != p.Lease || !reflect.DeepEqual(oldLease.Request, l.Request) {
		return model.Err("revision_conflict", "ordinary share lease changed")
	}
	if p.Retired != nil && (l.State != "terminal" || l.Ended == nil || !l.Ended.Equal(*p.Retired)) || p.Retired == nil && (!consuming(l.State) || l.Ended != nil) {
		return model.Err("share_evidence_unavailable", "ordinary share obligation settlement differs")
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = change.write(context.Background(), tx, false, parts); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE leases SET data=? WHERE id=? AND json_extract(data,'$.revision')=?`, string(raw), l.ID, oldLease.Revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return model.Err("revision_conflict", "ordinary share lease revision changed")
	}
	if err = event(tx, "share."+transition, p.Lease, map[string]any{"share": p.ID, "generation": p.Generation, "state": p.State, "storage_lease": p.Request.Storage, "reason": p.Request.Reason, "revision": p.Revision}); err != nil {
		return err
	}
	if err = event(tx, "lease.share_"+transition, l.ID, l); err != nil {
		return err
	}
	if err = m.commitShareTransaction(tx); err != nil {
		return err
	}
	m.applySharePublication(change)
	m.traceShareLocked(transition, p.ID)
	return nil
}

// Every private preparation/verification transition binds exact worker phase.
// Consumer admission remains unavailable; generic row updates cannot grant it.
func sharePreparationTransition(old, next sharelease.Publication, transition string) error {
	ok := false
	switch transition {
	case "verify_reserved":
		ok = old.State == "prepared" && old.WorkerState == "joined" && old.Publish == nil && next.Publish != nil && next.Publish.Revision == old.Revision && next.State == "verifying" && next.WorkerState == "reserved" && next.WorkerPhase == "verification" && old.JournalSlots >= 6
	case "verify_started":
		ok = old.State == "verifying" && old.WorkerState == "reserved" && next.State == "verifying" && next.WorkerState == "running"
	case "published":
		ok = old.State == "verifying" && old.WorkerState == "running" && next.State == "published" && next.WorkerState == "joined" && next.Published != nil
	case "retiring":
		ok = old.State == "published" && old.WorkerState == "joined" && next.State == "retiring" && next.WorkerState == "joined"
	case "retired":
		ok = old.State == "retiring" && old.WorkerState == "joined" && next.State == "retired" && next.WorkerState == "joined" && next.Retired != nil
	case "worker_started":
		ok = old.State == "preparing" && old.WorkerState == "reserved" && next.State == "preparing" && next.WorkerState == "running"
	case "prepared":
		ok = old.State == "preparing" && old.WorkerState == "running" && next.State == "prepared" && next.WorkerState == "joined" && old.Summary == nil && next.Summary != nil
	case "ending":
		ok = old.Retired == nil && old.Published == nil && old.WorkerState != "joined" && old.State != "ending" && next.State == "ending" && next.WorkerState == old.WorkerState
	case "needs_attention":
		ok = old.Retired == nil && old.Published == nil && old.WorkerState != "joined" && next.State == "needs_attention" && next.WorkerState == "unknown"
	case "cancelled", "failed":
		ok = old.Retired == nil && old.Published == nil && next.State == transition && next.WorkerState == "joined" && next.Retired != nil
	}
	if !ok {
		return model.Err("share_evidence_unavailable", "unsupported private preparation transition; current worker and immutable phase evidence must match")
	}
	if next.Retired == nil && next.JournalSlots != old.JournalSlots-1 {
		return model.Err("share_evidence_unavailable", "preparation transition must consume its reserved finalization slot")
	}
	return nil
}
