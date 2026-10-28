package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"getarm.org/internal/kubeprovision"
	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
)

type KubernetesSourceResult struct {
	SourceLeaseID   string `json:"source_lease_id"`
	StorageLeaseID  string `json:"storage_lease_id,omitempty"`
	SourceClosed    bool   `json:"source_closed"`
	SourceSettled   bool   `json:"source_settled"`
	StorageState    string `json:"storage_state,omitempty"`
	StorageRetained bool   `json:"storage_retained"`
	PromisedDisk    int64  `json:"promised_disk_bytes"`
}

// KubernetesSourcePreparation is an unused original native pair capability.
// No row/ID/path/FD or browser request can reconstruct it after admission.
type KubernetesSourcePreparation struct {
	lostDebtAck         bool // private fixture: committed observation with lost acknowledgment
	manager             *Manager
	scope               kubesource.Scope
	transit             *RelayTransit
	parent              model.Lease // Manager.mu protects the original expected parent tuple
	ctx                 context.Context
	cancel              context.CancelFunc
	callerJoined        <-chan struct{}
	privateJoined       chan struct{}
	done                chan struct{}
	watchDone           chan struct{}
	files               *kubernetesSourceFiles
	mu                  sync.Mutex
	published, finished bool
	result              KubernetesSourceResult
	err                 error
}

func (k *KubernetesSourcePreparation) Context() context.Context {
	if k == nil {
		return context.Background()
	}
	return k.ctx
}
func (k *KubernetesSourcePreparation) Cancel() {
	if k != nil && k.cancel != nil {
		k.cancel()
	}
}
func (k *KubernetesSourcePreparation) Files() kubeprovision.SourceFiles {
	if k == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.published || k.finished {
		return nil
	}
	return k.files
}
func (k *KubernetesSourcePreparation) CheckActive(ctx context.Context) error {
	if k == nil || k.manager == nil || ctx == nil || ctx.Err() != nil || k.ctx.Err() != nil {
		return model.Err("lease_inactive", "original source pair unavailable")
	}
	m := k.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	return k.checkPairLocked()
}
func (k *KubernetesSourcePreparation) checkPairLocked() error {
	m := k.manager
	h := k.transit
	source, err := m.get(h.id)
	if err != nil {
		return err
	}
	parent, err := m.get(k.parent.ID)
	if err != nil {
		return err
	}
	if k.ctx.Err() != nil || m.closing || m.draining || m.relayTransits[h.id] != h || h.settled || h.recoveryReason != "" || source.State != "active" || source.Revision != h.result.Revision || !reflect.DeepEqual(source.Request, h.result.Request) || !reflect.DeepEqual(source.Clock, h.result.Clock) || !reflect.DeepEqual(source.Expires, h.result.Expires) || source.Host != h.result.Host || source.Owner != h.result.Owner || parent.State != "active" || parent.Revision != k.parent.Revision || !reflect.DeepEqual(parent.Request, k.parent.Request) || !reflect.DeepEqual(parent.Clock, k.parent.Clock) || !reflect.DeepEqual(parent.Expires, k.parent.Expires) || !reflect.DeepEqual(parent.Artifact, k.parent.Artifact) || !m.leaseLive(source) || !m.leaseLive(parent) || parent.Artifact == nil || parent.Artifact.Phase != "owned" {
		return model.Err("lease_inactive", "exact current source pair unavailable")
	}
	if err = kubesource.ValidatePair(source, parent); err != nil {
		return err
	}
	remaining, err := m.leaseRemaining(source)
	if err != nil {
		return err
	}
	if !m.leaseCovers(parent, remaining) {
		return model.Err("lease_inactive", "parent does not cover source entitlement")
	}
	return nil
}

// PrepareKubernetesSource admits only the reviewed fixed native pair. It is not
// called from Provider, CLI, server, browser or connector. The generic admission
// methods retain their existing no-attachment rules.
func (m *Manager) PrepareKubernetesSource(ctx context.Context, scope kubesource.Scope, callerJoined <-chan struct{}) (result *KubernetesSourcePreparation, resultErr error) {
	if ctx == nil || callerJoined == nil || scope.Validate() != nil {
		return nil, model.Err("invalid_request", "closed native source scope and original caller required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-callerJoined:
		return nil, model.Err("invalid_request", "caller already joined")
	default:
	}
	m.mu.Lock()
	if m.closing || m.draining {
		m.mu.Unlock()
		return nil, model.Err("backend_stopping", "source admission closed")
	}
	if m.kubernetesSourceAdmissions == nil {
		m.kubernetesSourceAdmissions = map[string]bool{}
	}
	if m.kubernetesSourceAdmissions[scope.Operation] {
		m.mu.Unlock()
		return nil, model.Err("busy", "source operation admission already in progress")
	}
	m.kubernetesSourceAdmissions[scope.Operation] = true
	defer func() { m.mu.Lock(); delete(m.kubernetesSourceAdmissions, scope.Operation); m.mu.Unlock() }()
	budget := m.canonicalBudgetSelector("")
	// Both keys, including partial/terminal rows, are lookup-only. No existing
	// admission can be adopted as an original runtime owner.
	var count int
	err := m.db.QueryRow(`SELECT count(*) FROM leases WHERE request_key IN (?,?)`, scope.Key(false), scope.Key(true)).Scan(&count)
	if err == nil && count != 0 {
		err = model.Err("idempotency_conflict", "source attempt already recorded; inspect original tuple")
	}
	if err == nil {
		err = excludeSourceOperation(m.db, scope)
	}
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	joined := make(chan struct{})
	if m.sourceBeforeAdmission != nil {
		m.sourceBeforeAdmission()
	}
	transit, err := m.acquireKubernetesSourceTransit(ctx, scope, budget, joined)
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(transit.Context())
	k := &KubernetesSourcePreparation{manager: m, scope: scope, transit: transit, ctx: life, cancel: cancel, callerJoined: callerJoined, privateJoined: joined, done: make(chan struct{})}
	// The registered transit owns construction itself; Close cannot miss it.
	defer func() {
		if problem := recover(); problem != nil {
			_ = transit.MarkCleanupUncertain("source constructor panicked")
			k.finish(false)
			panic(problem)
		}
		if resultErr != nil {
			k.finish(false)
		}
	}()
	m.mu.Lock()
	if m.kubernetesSources == nil {
		m.kubernetesSources = map[string]*KubernetesSourcePreparation{}
	}
	m.kubernetesSources[transit.id] = k
	m.mu.Unlock()
	if err = transit.CheckActive(life); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if life.Err() != nil || m.closing || m.draining {
		err = model.Err("backend_stopping", "source construction cancelled")
	} else {
		request := scope.Request(m.host, budget, true)
		k.parent, err = m.acquireWithSourcePermit(request, nil, false, nil, &kubernetesSourcePermit{manager: m, scope: scope, parent: true, request: request})
	}
	if k.parent.ID != "" {
		m.kubernetesSources[k.parent.ID] = k
	}
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if k.parent.State != "active" || k.parent.Artifact == nil {
		return nil, model.Err("resource_unavailable", "source storage not active; original obligation retained")
	}
	if err = transit.CheckActive(life); err != nil {
		return nil, err
	}
	// Atomic one-time attachment; no I/O or human wait belongs under this lock.
	m.mu.Lock()
	source, e := m.get(transit.id)
	if e == nil {
		var parent model.Lease
		parent, e = m.get(k.parent.ID)
		if e == nil && (source.Revision != transit.result.Revision || !reflect.DeepEqual(source.Request, transit.result.Request) || source.State != "active" || source.Request.StorageLease != "" || parent.Revision != k.parent.Revision || !reflect.DeepEqual(parent, k.parent) || life.Err() != nil || m.closing || m.draining || !m.leaseLive(source) || !m.leaseLive(parent)) {
			e = model.Err("lease_inactive", "source pair changed before attachment")
		}
		if e == nil {
			source.Request.StorageLease = parent.ID
			e = kubesource.ValidatePair(source, parent)
		}
		if e == nil {
			source.Revision++
			e = m.put(source, "kubernetes.source_attached")
			if e == nil {
				transit.result = source
			}
		}
	}
	m.mu.Unlock()
	if e != nil {
		_ = transit.MarkCleanupUncertain("source attachment acknowledgement unresolved")
		return nil, e
	}
	if err = k.CheckActive(life); err != nil {
		return nil, err
	}
	directory, err := m.storage.HoldSourceDirectory(*k.parent.Artifact)
	if err != nil {
		_ = transit.MarkCleanupUncertain("original source directory custody unresolved")
		return nil, err
	}
	k.files = newKubernetesSourceFiles(k, directory)
	if err = k.CheckActive(life); err != nil {
		return nil, err
	}
	select {
	case <-callerJoined:
		return nil, model.Err("lease_inactive", "caller joined before source publication")
	default:
	}
	k.mu.Lock()
	k.published = true
	k.mu.Unlock()
	k.watchDone = make(chan struct{})
	m.wg.Go(func() {
		defer close(k.watchDone)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-life.Done():
				return
			case <-tick.C:
				if k.CheckActive(life) != nil {
					k.Cancel()
					return
				}
			}
		}
	})
	m.wg.Go(func() {
		select {
		case <-callerJoined:
		case <-life.Done():
			<-callerJoined
		}
		k.finish(true)
	})
	return k, nil
}

// captureDebt records actual observed namespace allocation before the first
// producer unlink. Unknown ACK never authorizes unlink/write/exposure.
func (k *KubernetesSourcePreparation) captureDebt(ctx context.Context) error {
	m := k.manager
	m.mu.Lock()
	parent := k.parent
	m.mu.Unlock()
	if parent.Artifact.AllocatedBefore != nil {
		if !parent.Artifact.ContentObserved {
			return fmt.Errorf("inconsistent source debt observation")
		}
		return nil
	}
	observed, err := m.storage.Survey(ctx, *parent.Artifact)
	if err != nil {
		return err
	}
	if observed.Entries < 1 || observed.Allocated < 0 {
		return fmt.Errorf("original linked source entry not observed")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = k.checkPairLocked(); err != nil {
		return err
	}
	a := *k.parent.Artifact
	if a.AllocatedBefore != nil {
		// A sibling original file creation may have recorded and received the
		// first observation ACK while this Survey was in flight. Only this
		// original owner's ACK updates k.parent, so the paired cached state is
		// sufficient; no later file can replace or erase that first observation.
		if a.ContentObserved {
			return nil
		}
		return fmt.Errorf("source debt observation changed inconsistently")
	}
	v := observed.Allocated
	a.AllocatedBefore = &v
	a.ContentObserved = true
	a.Policy += "; source allocation measured before first source unlink; later unlinked writes not measured"
	a.Updated = time.Now().UTC()
	updated := k.parent
	updated.Artifact = &a
	updated.Revision++
	if err = m.put(updated, "storage.source_first_unlink_observed"); err != nil {
		return err
	}
	if k.lostDebtAck {
		return fmt.Errorf("source observation acknowledgement unavailable")
	}
	k.parent = updated
	return nil
}
func (k *KubernetesSourcePreparation) FinishAfterJoin() (KubernetesSourceResult, error) {
	if k == nil || k.manager == nil {
		return KubernetesSourceResult{}, model.Err("invalid_request", "original source owner required")
	}
	select {
	case <-k.callerJoined:
	default:
		return KubernetesSourceResult{}, model.Err("source_not_joined", "original caller has not joined")
	}
	<-k.done
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.result, k.err
}
func (k *KubernetesSourcePreparation) finish(published bool) {
	k.Cancel()
	var cleanupErr error
	if k.files != nil {
		cleanupErr = k.files.closeOriginal()
	}
	if k.watchDone != nil {
		<-k.watchDone
	}
	if cleanupErr != nil {
		cleanupErr = errors.Join(cleanupErr, k.transit.MarkCleanupUncertain("original source file/helper custody unresolved"))
	}
	nativeClosed := cleanupErr == nil
	close(k.privateJoined)
	source, err := k.transit.FinishAfterJoin()
	cleanupErr = errors.Join(cleanupErr, err)
	r := KubernetesSourceResult{SourceLeaseID: k.transit.id, StorageLeaseID: k.parent.ID, SourceClosed: nativeClosed, SourceSettled: err == nil && source.State == "terminal", PromisedDisk: k.scope.DiskBytes()}
	if k.parent.ID != "" {
		parent, endErr := k.manager.End(k.parent.ID, "released")
		if endErr != nil {
			cleanupErr = errors.Join(cleanupErr, endErr)
		}
		r.StorageState = parent.State
		r.StorageRetained = parent.State != "terminal"
	}
	k.manager.mu.Lock()
	delete(k.manager.kubernetesSources, k.transit.id)
	if k.parent.ID != "" {
		delete(k.manager.kubernetesSources, k.parent.ID)
	}
	k.manager.mu.Unlock()
	k.mu.Lock()
	k.finished = true
	k.result = r
	k.err = cleanupErr
	k.mu.Unlock()
	close(k.done)
}

// This permit has no public constructor or wire representation. Generic entry
// points pass nil; only this source constructor builds its exact request.
type kubernetesSourcePermit struct {
	manager *Manager
	scope   kubesource.Scope
	parent  bool
	request model.Request
}

func validateSourcePermit(m *Manager, r model.Request, p *kubernetesSourcePermit) error {
	if !kubesource.IsReserved(r) {
		if p != nil {
			return fmt.Errorf("source permit on unrelated request")
		}
		return nil
	}
	want := model.Request{}
	if p != nil {
		want = p.scope.Request(m.host, r.Budget, p.parent)
		if !p.parent {
			want.Kind = relayTransitKind
		}
	}
	if p == nil || p.manager != m || p.scope.Validate() != nil || !reflect.DeepEqual(r, p.request) || !reflect.DeepEqual(r, want) || r.Key != p.scope.Key(p.parent) {
		return model.Err("invalid_request", "reserved source request requires exact native admission permit")
	}
	return nil
}
func (m *Manager) acquireKubernetesSourceTransit(ctx context.Context, scope kubesource.Scope, budget string, joined <-chan struct{}) (*RelayTransit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	request := scope.Request(m.host, budget, false)
	request.Kind = relayTransitKind
	lease, err := m.acquireWithSourcePermit(request, nil, true, nil, &kubernetesSourcePermit{manager: m, scope: scope, request: request})
	if err != nil {
		return nil, err
	}
	return m.registerOwnedTransitLocked(ctx, lease, request.Key, joined), nil
}

// Recheck inside the insertion transaction, so even distinct Manager objects
// cannot both reserve a live original source for one operation.
type sourceOperationQuery interface {
	Query(string, ...any) (*sql.Rows, error)
}

func excludeSourceOperationTx(tx *sql.Tx, scope kubesource.Scope) error {
	return excludeSourceOperation(tx, scope)
}
func excludeSourceOperation(q sourceOperationQuery, scope kubesource.Scope) (resultErr error) {
	rows, err := q.Query(`SELECT typeof(request_key),length(CAST(request_key AS BLOB)),substr(CAST(request_key AS BLOB),1,129),typeof(data),length(CAST(data AS BLOB)),substr(CAST(data AS BLOB),1,1048577) FROM leases WHERE request_key LIKE ? LIMIT 129`, "ks1:"+scope.Operation+":%")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	n := 0
	for rows.Next() {
		n++
		if n > 128 {
			return model.Err("busy", "source operation history bound exceeded")
		}
		var keyType, storageType string
		var keySize, size int64
		var keyRaw, raw []byte
		if err = rows.Scan(&keyType, &keySize, &keyRaw, &storageType, &size, &raw); err != nil {
			return err
		}
		if keyType != "text" || keySize < 1 || keySize > 128 || int64(len(keyRaw)) != keySize || storageType != "text" || size < 1 || size > 1048576 || int64(len(raw)) != size {
			return fmt.Errorf("source operation record exceeds canonical bound")
		}
		var l model.Lease
		if err = json.Unmarshal(raw, &l); err != nil {
			return err
		}
		prior, parent, e := kubesource.ValidateRole(l)
		if e != nil || parent || prior.Operation != scope.Operation || string(keyRaw) != l.Request.Key {
			return fmt.Errorf("source operation tuple corrupt")
		}
		if l.State != "active" && l.State != "ending" && l.State != "needs_attention" && l.State != "terminal" || l.State == "terminal" && (l.Ended == nil || l.Ended.IsZero() || l.Outcome == "") {
			return fmt.Errorf("source operation lifecycle corrupt")
		}
		if consuming(l.State) || l.State == "queued" {
			return model.Err("needs_attention", "original source operation remains unresolved")
		}
	}
	return rows.Err()
}
