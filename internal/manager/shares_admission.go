package manager

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharelease"
	"github.com/yrashk/arm/internal/storage"
)

// No exported/wire value can construct this in-process admission. It augments
// EXACTLY the existing ordinary acquire transaction, not a second scheduler.
// Consumer admission is deliberately not exposed by this private checkpoint.
type shareAdmission struct{ preparation *sharePrepareAdmission }
type sharePrepareAdmission struct {
	ctx      context.Context
	request  sharelease.PrepareRequest
	source   model.Lease
	provider *storage.Provider
	worker   sharelease.Worker
	change   *sharePublicationChange
}

func (a shareAdmission) validate(r model.Request, run *RunRequest) error {
	p := a.preparation
	if p == nil || p.ctx == nil || p.provider == nil || p.worker.Validate(p.source.Owner) != nil || p.request.Validate() != nil || run != nil || r.Kind != "share-publication" || r.Key != shareLeaseKey(p.source.Host, p.request.Key) || r.StorageLease != p.request.Storage || r.Resources != p.request.Resources || r.Reason != p.request.Reason || r.TTL != p.request.TTL || r.Budget != p.request.Budget || r.Queue || r.StartDeadline != nil || r.Profile != "" || r.Peer != "" || r.OriginKey != "" || r.PeerCertificate != "" || r.PeerGrantRevision != 0 || r.PortLease != "" || r.ServiceLease != "" || len(r.Ports) != 0 || len(r.Pools) != 0 {
		return model.Err("invalid_request", "share kinds require exact typed in-process admission; wire descriptors cannot authorize filesystem work")
	}
	return p.ctx.Err()
}
func (m *Manager) prepareShareAdmission(a shareAdmission, l *model.Lease) error {
	p := a.preparation
	if p == nil || p.ctx.Err() != nil {
		return model.Err("request_cancelled", "share preparation was cancelled before admission")
	}
	if l.State != "active" || l.Started == nil || l.Expires == nil {
		return model.Err("share_unavailable", "preparation requires immediate ordinary admission; no queued or rejected publication starts native work")
	}
	source, err := m.shareStorageSource(p.request)
	if err != nil {
		return err
	}
	if source.Revision != p.source.Revision || !reflect.DeepEqual(source.Artifact, p.source.Artifact) || p.provider != m.storage {
		return model.Err("revision_conflict", "managed storage changed during off-lock native verification; inspect and review again")
	}
	if time.Now().UTC().After(*l.Expires) || l.Expires.After(*source.Expires) {
		return model.Err("lease_inactive", "share/storage lifetime ended before atomic admission")
	}
	at := time.Now().UTC()
	publication := sharelease.Publication{Schema: 2, Authority: m.host, Owner: m.owner, ID: local.ID(), Generation: local.ID(), Request: p.request, Lease: l.ID, StorageIdentity: *source.Artifact.Identity, PreparationWorker: p.worker, WorkerPhase: "preparation", Worker: p.worker, WorkerState: "reserved", State: "preparing", Revision: 1, Created: at, Updated: at, Expires: *l.Expires, Reason: p.request.Reason, JournalSlots: sharelease.PublicationFinalSlots}
	p.change, err = m.planSharePublication(publication, "reserved")
	if err != nil {
		return err
	}
	l.Capabilities = append(append([]string{}, l.Capabilities...), "share-publication-lifecycle", "fixed-share-lifetime")
	return nil
}
func (m *Manager) commitShareAdmission(a shareAdmission, tx *sql.Tx) error {
	p := a.preparation
	if p == nil || p.change == nil {
		return fmt.Errorf("missing runtime share admission plan")
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if err := p.change.write(p.ctx, tx, !m.shares.state.Enabled, nil); err != nil {
		return err
	}
	return event(tx, "share.reserved", p.change.next.Lease, map[string]any{"share": p.change.next.ID, "generation": p.change.next.Generation, "state": p.change.next.State, "storage_lease": p.request.Storage, "reason": p.request.Reason})
}
func (m *Manager) finishShareAdmission(a shareAdmission) {
	m.applySharePublication(a.preparation.change)
}
