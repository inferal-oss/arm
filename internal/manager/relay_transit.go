package manager

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"time"

	"github.com/yrashk/arm/internal/model"
)

const relayTransitKind = "relay-transit"
const syncPreparationKind = "sync-preparation"

// RelayTransit is an exact current Manager-owned allocation. A caller creates
// joined before admission and closes it only after every original transport
// copy, authority watcher and cancellation worker has returned. Neither lease
// expiry, socket close nor a cancellation request is such a witness.
type RelayTransit struct {
	manager    *Manager
	id         string
	key        string
	ctx        context.Context
	cancel     context.CancelFunc
	joined     <-chan struct{}
	workerDone chan struct{}
	// Manager.mu protects result and settled.
	result         model.Lease
	settled        bool
	recoveryReason string
}

func (h *RelayTransit) Context() context.Context { return h.ctx }

// Lease returns the current durable record, without lending settlement rights.
func (h *RelayTransit) Lease() (model.Lease, error) {
	if h == nil || h.manager == nil {
		return model.Lease{}, model.Err("invalid_request", "exact relay transit owner required")
	}
	h.manager.mu.Lock()
	defer h.manager.mu.Unlock()
	return h.manager.get(h.id)
}

// AcquireRelayTransit admits a fresh in-process transport obligation using the
// same live CPU/memory and policy checks as ordinary requests. Wire callers
// cannot select its internal kind, attach an existing lease or replay a key.
func (m *Manager) AcquireRelayTransit(ctx context.Context, request model.Request, joined <-chan struct{}) (*RelayTransit, error) {
	return m.acquireOwnedTransit(ctx, request, joined, relayTransitKind, nil)
}

// Only package-owned preparation can select the disk-bearing kind.
func (m *Manager) acquireOwnedTransit(ctx context.Context, request model.Request, joined <-chan struct{}, kind string, grant *syncPreparationGrant) (*RelayTransit, error) {
	if kind != relayTransitKind && kind != syncPreparationKind || kind == relayTransitKind && request.Resources.Disk != 0 || kind == syncPreparationKind && request.Resources != syncPreparationResources {
		return nil, model.Err("invalid_request", "invalid owned resource preparation policy")
	}
	if ctx == nil || joined == nil {
		return nil, model.Err("invalid_request", "relay context and original join witness required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-joined:
		return nil, model.Err("invalid_request", "relay join witness is already closed")
	default:
	}
	if request.Key == "" || request.Kind != "" || request.Resources.CPU <= 0 || request.Resources.Memory <= 0 || request.Queue || request.StartDeadline != nil || request.Profile != "" || request.Agent != "" || request.StorageLease != "" || request.ServiceLease != "" || request.PortLease != "" || len(request.Ports) != 0 || len(request.Pools) != 0 || request.UsagePolicy != nil || grant == nil && (request.Peer != "" || request.PeerCertificate != "" || request.PeerGrantRevision != 0) || request.OriginKey != "" {
		return nil, model.Err("invalid_request", "relay requires a fresh keyed CPU/memory allocation without attachments")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-joined:
		return nil, model.Err("invalid_request", "relay join witness is already closed")
	default:
	}
	var existing string
	err := m.db.QueryRow(`SELECT id FROM leases WHERE request_key=?`, request.Key).Scan(&existing)
	if err == nil {
		return nil, model.Err("idempotency_conflict", "relay transit key is already recorded; an existing owner cannot be adopted")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if grant != nil {
		if kind != syncPreparationKind || request.Peer != grant.source || request.PeerCertificate != grant.certificate || request.PeerGrantRevision != grant.revision || !time.Now().Before(grant.validUntil) {
			return nil, model.Err("forbidden", "sync preparation grant changed")
		}
		leases, e := m.list()
		if e != nil {
			return nil, e
		}
		var used model.Resources
		for _, l := range leases {
			if l.Request.Peer == grant.source && consuming(l.State) {
				used = used.Add(l.Request.Resources)
			}
		}
		if !request.Resources.FitsRemaining(used, grant.limit) {
			return nil, model.Err("forbidden", "aggregate peer ceiling cannot cover sync preparation")
		}
	}
	request.Kind = kind
	lease, err := m.acquireWithRelay(request, nil, true)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	h := &RelayTransit{manager: m, id: lease.ID, key: request.Key, ctx: ctx, cancel: cancel, joined: joined, workerDone: make(chan struct{}), result: lease}
	if m.relayTransits == nil {
		m.relayTransits = map[string]*RelayTransit{}
	}
	m.relayTransits[lease.ID] = h
	// Registered under Manager.mu before admission returns; Close cannot miss
	// this owner or close the database while its original join is outstanding.
	m.wg.Go(func() {
		defer close(h.workerDone)
		<-joined
		_, _ = h.FinishAfterJoin()
	})
	return h, nil
}

func (m *Manager) endRelayTransit(l model.Lease, outcome string) (model.Lease, error) {
	h := m.relayTransits[l.ID]
	if h == nil || h.key != l.Request.Key || l.State == "needs_attention" {
		return l, model.Err("needs_attention", "original relay owner and positive join witness are unavailable; allocation remains charged")
	}
	// Cancellation never certifies reclamation. Even when recording ending
	// fails, stop the exact current owner and retain the prior charged row.
	// Otherwise Close could wait forever for an owner it had never cancelled.
	defer h.cancel()
	l.State, l.Outcome = "ending", outcome
	l.Revision++
	if err := m.put(l, "relay.ending"); err != nil {
		return l, err
	}
	return l, nil
}

// FinishAfterJoin persists resource settlement only for this exact live owner.
// Repeating it retries ledger finalization, never a transport or native effect.
func (h *RelayTransit) FinishAfterJoin() (model.Lease, error) {
	if h == nil || h.manager == nil {
		return model.Lease{}, model.Err("invalid_request", "exact relay owner required")
	}
	select {
	case <-h.joined:
	default:
		return model.Lease{}, model.Err("relay_not_joined", "original relay workers have not joined; resources remain charged")
	}
	m := h.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if h.settled {
		if h.recoveryReason != "" {
			return h.result, model.Err("needs_attention", h.recoveryReason)
		}
		return h.result, nil
	}
	if m.relayTransits[h.id] != h {
		return model.Lease{}, model.Err("relay_owner_changed", "relay owner differs; allocation remains charged")
	}
	l, err := m.get(h.id)
	if err != nil {
		return l, err
	}
	if l.Request.Kind != h.result.Request.Kind || l.Request.Key != h.key || !consuming(l.State) || l.Host != h.result.Host || l.Owner != h.result.Owner || l.PID != 0 || l.Process != nil || !reflect.DeepEqual(l.Request, h.result.Request) {
		return l, model.Err("relay_owner_changed", "durable relay allocation differs; no settlement performed")
	}
	if h.recoveryReason != "" {
		l.State, l.Ended = "needs_attention", nil
		l.Outcome, l.Failure = "interrupted", h.recoveryReason
		l.Revision++
		if err = m.put(l, "sync.preparation_recovery_required"); err != nil {
			return l, err
		}
		h.cancel()
		h.result, h.settled = l, true
		delete(m.relayTransits, h.id)
		return l, model.Err("needs_attention", h.recoveryReason)
	}
	now := time.Now().UTC()
	l.State, l.Ended = "terminal", &now
	if l.Outcome == "" {
		// Joining the transport proves resource closure, not successful remote
		// execution, file publication or delivery of the final receipt.
		l.Outcome = "relay_closed"
		if l.Request.Kind == syncPreparationKind {
			l.Outcome = "completed"
		}
	}
	l.Revision++
	joinedEvent := "relay.joined"
	if l.Request.Kind == syncPreparationKind {
		joinedEvent = "sync.preparation_joined"
	}
	if err = m.put(l, joinedEvent); err != nil {
		return l, err
	}
	h.cancel()
	h.result, h.settled = l, true
	delete(m.relayTransits, h.id)
	return l, nil
}

// Called after closing admission. No Manager mutex is held while waiting for
// external workers; final join acknowledgements still need the live database.
func (m *Manager) closeRelayTransits() error {
	m.mu.Lock()
	owners := make([]*RelayTransit, 0, len(m.relayTransits))
	var result error
	for id, h := range m.relayTransits {
		owners = append(owners, h)
		_, err := m.end(id, "cancelled")
		result = errors.Join(result, err)
	}
	m.mu.Unlock()
	for _, h := range owners {
		<-h.workerDone
		_, err := h.FinishAfterJoin()
		result = errors.Join(result, err)
	}
	return result
}
