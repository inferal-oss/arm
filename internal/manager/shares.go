package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharefs"
	"github.com/yrashk/arm/internal/sharelease"
)

// shareBoundary is authority-owned, never one per peer. Only Manager.mu protects
// ledger counters/maps. Native I/O and worker joins NEVER run under that lock.
type shareBoundary struct {
	state             sharelease.Snapshot
	engine            *sharefs.Engine
	failure           error
	commitAckFailure  error // private one-shot fault after real SQL commit; never wire/runtime authority
	workers           map[string]*shareWorker
	controls          map[string]context.CancelFunc
	beforeNativeBuild func(context.Context) // private native-I/O test seam; never wire authority
	byLease           map[string]string     // publication ordinary lease -> publication ID
}

func shareKind(kind string) bool {
	return kind == "share-preparation" || kind == "share-publication" || kind == "share-consumer"
}
func shareLeaseKey(host, key string) string { return mesh.Key(host, "share-prepare\x00"+key) }
func (m *Manager) initShares() {
	engine, err := sharefs.New(sharefs.DefaultLimits())
	b := &shareBoundary{engine: engine, workers: map[string]*shareWorker{}, controls: map[string]context.CancelFunc{}, byLease: map[string]string{}, failure: err}
	m.shares = b
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b.state, err = sharelease.Load(ctx, m.db, m.host, m.owner)
	b.failure = err
	// Partial row IDs are conservative extra holds only, NEVER release/grant proof.
	for _, p := range b.state.Publications {
		b.byLease[p.Lease] = p.ID
	}
	if err != nil {
		return
	}
	leases, err := m.list()
	if err != nil {
		b.failure = err
		return
	}
	seen := map[string]bool{}
	for _, p := range b.state.Publications {
		l, e := m.get(p.Lease)
		if e == nil {
			e = m.checkPublicationLease(p, l)
		}
		if e != nil {
			b.failure = e
			return
		}
		seen[l.ID] = true
	}
	for _, c := range b.state.Consumers {
		l, e := m.get(c.Admission.Lease)
		if e == nil {
			e = m.checkShareConsumerLease(c, l)
		}
		if e != nil {
			b.failure = e
			return
		}
		seen[l.ID] = true
	}
	for _, l := range leases {
		if shareKind(l.Request.Kind) && !seen[l.ID] {
			b.failure = fmt.Errorf("share lease is missing its exact durable operation")
			return
		}
	}
}
func (m *Manager) sharePolicyError() error {
	if m.shares == nil || m.shares.failure != nil {
		detail := "share boundary unavailable"
		if m.shares != nil && m.shares.failure != nil {
			detail = m.shares.failure.Error()
		}
		return model.Err("share_evidence_unavailable", detail+"; no share admission or ambiguous managed storage cleanup is permitted")
	}
	return nil
}
func (m *Manager) shareStorageBusy(storage string) (string, error) {
	if err := m.sharePolicyError(); err != nil {
		return "", err
	}
	for _, p := range m.shares.state.Publications {
		if p.Request.Storage == storage && p.Retired == nil {
			return p.ID, nil
		}
	}
	return "", nil
}
func (m *Manager) shareNonRuntimeLease(l model.Lease) bool {
	if shareKind(l.Request.Kind) {
		return true
	}
	if m.shares != nil {
		if _, ok := m.shares.byLease[l.ID]; ok {
			return true
		}
		for _, c := range m.shares.state.Consumers {
			if c.Admission.Lease == l.ID {
				return true
			}
		}
	}
	if l.Request.StorageLease != "" {
		var noRun bool
		if err := m.db.QueryRow(`SELECT run IS NULL FROM leases WHERE id=?`, l.ID).Scan(&noRun); err != nil {
			return true
		}
		return noRun
	}
	return false
}
func (m *Manager) checkPublicationLease(p sharelease.Publication, l model.Lease) error {
	r := l.Request
	if p.Validate() != nil || l.ID != p.Lease || l.Host != m.host || l.Owner != m.owner || r.Kind != "share-publication" || r.StorageLease != p.Request.Storage || r.Key != shareLeaseKey(m.host, p.Request.Key) || r.TTL != p.Request.TTL || r.Reason != p.Request.Reason || r.Resources != p.Request.Resources || r.Budget != p.Request.Budget || r.Peer != "" || r.OriginKey != "" || r.PeerCertificate != "" || r.PeerGrantRevision != 0 || r.Profile != "" || r.Queue || r.StartDeadline != nil || r.ServiceLease != "" || r.PortLease != "" || len(r.Ports) != 0 || len(r.Pools) != 0 || l.Cargo != nil || l.Workload != nil || l.Artifact != nil || l.PID != 0 || l.Expires == nil || !l.Expires.Equal(p.Expires) || l.Created.After(p.Created) || l.Started == nil || l.Started.After(p.Created) {
		return fmt.Errorf("publication ordinary lease binding differs")
	}
	var noRun bool
	if err := m.db.QueryRow(`SELECT run IS NULL FROM leases WHERE id=?`, l.ID).Scan(&noRun); err != nil || !noRun {
		return fmt.Errorf("share publication cannot carry process execution")
	}
	parent, err := m.get(p.Request.Storage)
	if err != nil || parent.Host != m.host || parent.Owner != m.owner || parent.Request.Kind != "storage" || parent.Artifact == nil || parent.Artifact.Identity == nil || *parent.Artifact.Identity != p.StorageIdentity || parent.Revision < p.Request.StorageRevision || parent.Expires == nil || parent.Expires.Before(p.Expires) {
		return fmt.Errorf("publication backing storage lease/identity differs")
	}
	if p.Retired == nil && (!consuming(parent.State) || parent.Artifact.Phase != "owned" || parent.State == "active" && parent.Revision != p.Request.StorageRevision) {
		return fmt.Errorf("unsettled share lacks its pinned managed storage obligation")
	}

	if p.Retired != nil {
		if l.State != "terminal" || l.Ended == nil || !l.Ended.Equal(*p.Retired) {
			return fmt.Errorf("publication final ordinary lease differs")
		}
	} else {
		expected := "active"
		if p.State == "ending" || p.State == "retiring" {
			expected = "ending"
		}
		if p.State == "needs_attention" {
			expected = "needs_attention"
		}
		if l.State != expected || l.Ended != nil || !consuming(l.State) {
			return fmt.Errorf("unsettled publication has no charged lease")
		}
	}
	return nil
}
func (m *Manager) checkShareConsumerLease(c sharelease.Consumer, l model.Lease) error {
	q := c.Admission.Control
	r := l.Request
	if c.Validate() != nil || l.ID != c.Admission.Lease || l.Host != m.host || l.Owner != m.owner || r.Kind != "share-consumer" || r.Key != mesh.Key(q.Source, "share-read\x00"+q.Key) || r.Peer != q.Source || r.OriginKey != q.Key || r.PeerCertificate != q.Certificate || int64(r.PeerGrantRevision) != q.Revision || r.Resources != q.Resources || r.TTL != q.TTL || r.Reason != q.Reason || r.Profile != "" || r.Queue || r.StartDeadline != nil || r.PortLease != "" || r.ServiceLease != "" || len(r.Ports) != 0 || len(r.Pools) != 0 || l.PID != 0 || l.Cargo != nil || l.Workload != nil || l.Artifact != nil || l.Expires == nil || !l.Expires.Equal(c.Admission.Expires) || l.Created.After(c.Admission.At) {
		return fmt.Errorf("consumer ordinary lease binding differs")
	}
	p, ok := m.shares.state.Publications[q.Share]
	if !ok || r.StorageLease != p.Request.Storage {
		return fmt.Errorf("consumer backing storage differs")
	}
	var noRun bool
	if err := m.db.QueryRow(`SELECT run IS NULL FROM leases WHERE id=?`, l.ID).Scan(&noRun); err != nil || !noRun {
		return fmt.Errorf("share consumer cannot carry a process command")
	}
	if c.State == "terminal" {
		if l.State != "terminal" || l.Ended == nil || !l.Ended.Equal(*c.Ended) || l.Outcome != c.Outcome {
			return fmt.Errorf("consumer terminal ordinary lease differs")
		}
	} else if !consuming(l.State) || l.Ended != nil {
		return fmt.Errorf("unsettled consumer lacks charged lease")
	}
	return nil
}
func (m *Manager) shareStorageSource(q sharelease.PrepareRequest) (model.Lease, error) {
	var empty model.Lease
	if err := m.sharePolicyError(); err != nil {
		return empty, err
	}
	if m.closing || m.draining {
		return empty, model.Err("backend_stopping", "share preparation is unavailable while backend stops/drains")
	}
	l, err := m.get(q.Storage)
	if err != nil {
		return l, err
	}
	if l.Request.Kind != "storage" || l.Owner != m.owner || l.Host != m.host || l.State != "active" || l.Revision != q.StorageRevision || l.Expires == nil || !time.Now().UTC().Add(q.TTL).Before(*l.Expires) || l.Artifact == nil || l.Artifact.Phase != "owned" || l.Artifact.Identity == nil || m.storage == nil || m.storageErr != nil {
		return l, model.Err("storage_unavailable", "preparation requires exact-revision active owned managed storage outlasting the full share TTL; renew storage before preparing")
	}
	if busy, err := m.shareStorageBusy(l.ID); err != nil {
		return l, err
	} else if busy != "" {
		return l, model.Err("storage_in_use", "retire share "+busy+" and join its consumers before preparing another generation")
	}
	if busy, err := m.storageExportBusy(l.ID); err != nil {
		return l, err
	} else if busy != "" {
		return l, model.Err("storage_in_use", "settle or explicitly hand off the existing export before preparing a share")
	}
	if m.storageJobs[l.ID] || m.storageJobs[exportJobKey(l.ID)] {
		return l, model.Err("operation_busy", "storage cleanup/export is running")
	}
	leases, err := m.list()
	if err != nil {
		return l, err
	}
	if len(storageConsumers(leases, l.ID)) != 0 {
		return l, model.Err("storage_in_use", "finish/cancel ALL queued and active storage writers before preparing a share")
	}
	return l, nil
}
func clonePublication(p sharelease.Publication) sharelease.Publication {
	raw, _ := json.Marshal(p)
	var out sharelease.Publication
	_ = json.Unmarshal(raw, &out)
	return out
}
func (m *Manager) shareRetry(q sharelease.PrepareRequest) (sharelease.Publication, bool, error) {
	if err := m.sharePolicyError(); err != nil {
		return sharelease.Publication{}, false, err
	}
	for _, p := range m.shares.state.Publications {
		if p.Request.Key == q.Key {
			if !reflect.DeepEqual(p.Request, q) {
				return p, true, model.Err("idempotency_conflict", "share preparation key already records a different exact request")
			}
			return clonePublication(p), true, nil
		}
	}
	return sharelease.Publication{}, false, nil
}
func shareMaxTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return a
	}
	return b
}
func shareSQLError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return model.Err("share_not_found", "share operation not found")
	}
	return err
}
