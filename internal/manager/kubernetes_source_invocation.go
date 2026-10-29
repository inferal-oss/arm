package manager

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"getarm.org/internal/kubesource"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

const kubernetesInvocationRowBytes = 1 << 20
const kubernetesInvocationTupleBytes = 3 * (kubernetesInvocationRowBytes + 32 + 128)

// KubernetesSourceInvocationOutcome is observational data, never a source,
// cleanup capability, admission credit or a recovered native owner. Closed
// originals and ordinary retained scratch debt are independent facts.
type KubernetesSourceInvocationOutcome struct {
	Disposition string
	Binding     kubesource.Binding
	Rows        []KubernetesSourceInvocationRow
	Result      KubernetesSourceResult
}

type KubernetesSourceInvocationRow struct {
	Role                  kubesource.Role
	ID, RequestKey, State string
	Admitted              bool
}

type kubernetesInvocationCandidate struct {
	role                                                 kubesource.Role
	id, key                                              string
	request                                              model.Request
	initial                                              []byte // complete exact pre-INSERT lease; bounded, never a status view
	attempted, commitAttempted, acknowledged, rolledBack bool
	lostAdmissionACK                                     bool // private original-instance fault fixture, never a caller field
}

// KubernetesSourceInvocation retains only this process's original call. Copies,
// saved IDs/rows and zero values cannot reconstruct it. Mutable fields are
// protected by Manager.mu; returned closes only after constructor cleanup.
type KubernetesSourceInvocation struct {
	original        *KubernetesSourceInvocation
	manager         *Manager
	scope           kubesource.Scope
	ctx             context.Context
	callerJoined    <-chan struct{}
	returned        chan struct{}
	candidates      []kubernetesInvocationCandidate
	preparation     *KubernetesSourcePreparation // includes a failed, unpublished original
	success         *KubernetesSourcePreparation
	constructionErr error
	panicked        bool
}

// PrepareKubernetesSourceInvocation leaves existing constructors unchanged. A
// syntactically valid call returns its original invocation even on refusal.
func (m *Manager) PrepareKubernetesSourceInvocation(ctx context.Context, scope kubesource.Scope, joined <-chan struct{}) (*KubernetesSourceInvocation, error) {
	if scope.Stage == kubesource.Forward {
		return nil, model.Err("invalid_request", "forward source requires the original three-row invocation")
	}
	return m.prepareKubernetesInvocation(ctx, scope, joined)
}
func (m *Manager) PrepareKubernetesForwardSourceInvocation(ctx context.Context, scope kubesource.Scope, joined <-chan struct{}) (*KubernetesSourceInvocation, error) {
	if scope.Stage != kubesource.Forward {
		return nil, model.Err("invalid_request", "exact forward invocation required")
	}
	return m.prepareKubernetesInvocation(ctx, scope, joined)
}
func (m *Manager) newKubernetesInvocation(ctx context.Context, scope kubesource.Scope, joined <-chan struct{}) (*KubernetesSourceInvocation, error) {
	if ctx == nil || joined == nil || scope.Validate() != nil {
		return nil, model.Err("invalid_request", "closed source scope and original caller required")
	}
	i := &KubernetesSourceInvocation{manager: m, scope: scope, ctx: ctx, callerJoined: joined, returned: make(chan struct{})}
	i.original = i
	// Generate proposed identities before bookkeeping capture; capture itself
	// performs no native I/O, callback, close or join.
	ids := [3]string{local.ID(), local.ID(), local.ID()}
	m.mu.Lock()
	defer m.mu.Unlock()
	budget := m.canonicalBudgetSelector("")
	for n, role := range []kubesource.Role{kubesource.SourceRole, kubesource.StorageRole, kubesource.PortRole} {
		if role == kubesource.PortRole && scope.Stage != kubesource.Forward {
			continue
		}
		request := scope.Request(m.host, budget, role == kubesource.StorageRole)
		if role == kubesource.SourceRole {
			request.Kind = relayTransitKind
		}
		if role == kubesource.PortRole {
			request = scope.PortRequest(m.host, budget, m.portNamespace)
		}
		i.candidates = append(i.candidates, kubernetesInvocationCandidate{role: role, id: ids[n], key: request.Key, request: request})
	}
	return i, nil
}
func (m *Manager) prepareKubernetesInvocation(ctx context.Context, scope kubesource.Scope, joined <-chan struct{}) (*KubernetesSourceInvocation, error) {
	i, err := m.newKubernetesInvocation(ctx, scope, joined)
	if err != nil {
		return nil, err
	}
	err = i.construct()
	return i, err
}
func (i *KubernetesSourceInvocation) valid() bool {
	return i != nil && i.original == i && i.manager != nil && i.ctx != nil && i.callerJoined != nil && i.returned != nil
}
func (i *KubernetesSourceInvocation) construct() (resultErr error) {
	defer func() {
		problem := recover()
		i.manager.mu.Lock()
		i.constructionErr = resultErr
		i.panicked = problem != nil
		i.manager.mu.Unlock()
		close(i.returned)
		if problem != nil {
			panic(problem)
		}
	}()
	k, err := i.manager.prepareKubernetesSourceRecorded(i.ctx, i.scope, i.callerJoined, i)
	i.manager.mu.Lock()
	i.success = k
	i.manager.mu.Unlock()
	return err
}

// Preparation returns only the successfully published original; a constructor
// error never exposes its failed partial owner as a usable factory.
func (i *KubernetesSourceInvocation) Preparation() (*KubernetesSourcePreparation, error) {
	if !i.valid() {
		return nil, model.Err("source_outcome_unknown", "original invocation required")
	}
	select {
	case <-i.returned:
	default:
		return nil, model.Err("source_outcome_unknown", "constructor has not returned")
	}
	i.manager.mu.Lock()
	defer i.manager.mu.Unlock()
	if i.panicked || i.success == nil {
		return nil, errors.Join(i.constructionErr, model.Err("source_outcome_unknown", "constructor did not publish a source"))
	}
	return i.success, nil
}
func (i *KubernetesSourceInvocation) candidate(parent, port bool) *kubernetesInvocationCandidate {
	role := kubesource.SourceRole
	if parent {
		role = kubesource.StorageRole
	}
	if port {
		role = kubesource.PortRole
	}
	for n := range i.candidates {
		if i.candidates[n].role == role {
			return &i.candidates[n]
		}
	}
	return nil
}
func (p *kubernetesSourcePermit) captureCandidate(m *Manager, lease model.Lease, raw []byte, run any) error {
	if p.invocation == nil {
		return nil
	}
	i := p.invocation
	c := i.candidate(p.parent, p.port)
	if !i.valid() || i.manager != m || i.scope != p.scope || c == nil || c.attempted || len(c.initial) != 0 || lease.ID != c.id || lease.Request.Key != c.key || !reflect.DeepEqual(lease.Request, c.request) || run != nil || len(raw) == 0 || len(raw) > kubernetesInvocationRowBytes {
		return model.Err("source_outcome_unknown", "original admission recorder differs")
	}
	c.initial = bytes.Clone(raw)
	return nil
}

func unknownKubernetesInvocation() error {
	return model.Err("source_outcome_unknown", "original constructor census or cleanup is unresolved; retained allocations remain charged")
}

// OutcomeAfterJoin performs no cancellation, settlement, retry or native work.
// It refuses lock contention instead of creating an unbounded bookkeeping wait;
// every SQL operation shares a <=2s caller-bounded read-only snapshot.
func (i *KubernetesSourceInvocation) OutcomeAfterJoin(ctx context.Context) (KubernetesSourceInvocationOutcome, error) {
	out := KubernetesSourceInvocationOutcome{Disposition: "unknown"}
	if !i.valid() || ctx == nil || ctx.Err() != nil {
		return out, unknownKubernetesInvocation()
	}
	select {
	case <-i.returned:
	default:
		return out, unknownKubernetesInvocation()
	}
	select {
	case <-i.callerJoined:
	default:
		return out, model.Err("source_not_joined", "original caller has not joined")
	}
	m := i.manager
	if !m.mu.TryLock() {
		return out, unknownKubernetesInvocation()
	}
	defer m.mu.Unlock()
	if m.closing || i.panicked {
		return out, unknownKubernetesInvocation()
	}
	expected := 2
	if i.scope.Stage == kubesource.Forward {
		expected = 3
	}
	if len(i.candidates) != expected {
		return out, unknownKubernetesInvocation()
	}
	seenRoles, seenIDs, seenKeys := map[kubesource.Role]bool{}, map[string]bool{}, map[string]bool{}
	for _, c := range i.candidates {
		key := i.scope.Key(false)
		switch c.role {
		case kubesource.SourceRole:
		case kubesource.StorageRole:
			key = i.scope.Key(true)
		case kubesource.PortRole:
			if i.scope.Stage != kubesource.Forward {
				return out, unknownKubernetesInvocation()
			}
			key = i.scope.PortKey()
		default:
			return out, unknownKubernetesInvocation()
		}
		if seenRoles[c.role] || seenIDs[c.id] || seenKeys[c.key] || len(c.id) != 32 || c.key != key || c.request.Key != key {
			return out, unknownKubernetesInvocation()
		}
		seenRoles[c.role] = true
		seenIDs[c.id] = true
		seenKeys[c.key] = true
	}
	k := i.preparation
	if k != nil {
		select {
		case <-k.done:
		default:
			return out, model.Err("source_not_joined", "original native cleanup has not joined")
		}
		select {
		case <-k.transit.workerDone:
		default:
			return out, model.Err("source_not_joined", "original transit worker has not joined")
		}
		k.mu.Lock()
		out.Result = k.result
		cleanupErr := k.err
		k.mu.Unlock()
		if cleanupErr != nil || !out.Result.SourceClosed || !out.Result.SourceSettled || k.portID != "" && (!out.Result.PortClosed || !out.Result.PortSettled) {
			return out, unknownKubernetesInvocation()
		}
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, err := m.db.BeginTx(bounded, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, unknownKubernetesInvocation()
	}
	defer tx.Rollback()
	rows, err := i.census(bounded, tx)
	if err != nil {
		return out, errors.Join(unknownKubernetesInvocation(), err)
	}
	admitted := 0
	retained := false
	for _, c := range i.candidates {
		l, present := rows[c.role]
		row := KubernetesSourceInvocationRow{Role: c.role, ID: c.id, RequestKey: c.key}
		if c.commitAttempted && !c.acknowledged || c.attempted && len(c.initial) == 0 {
			return out, unknownKubernetesInvocation()
		}
		if present {
			if !c.acknowledged || k == nil {
				return out, unknownKubernetesInvocation()
			}
			if err = i.validateCensusLease(c, l, k); err != nil {
				return out, errors.Join(unknownKubernetesInvocation(), err)
			}
			row.Admitted = true
			row.State = l.State
			admitted++
			if c.role == kubesource.StorageRole {
				retained = l.State != "terminal"
			}
		} else if c.acknowledged || c.attempted && !c.rolledBack { // a failed rollback cannot prove original absence
			return out, unknownKubernetesInvocation()
		}
		out.Rows = append(out.Rows, row)
	}
	if err = tx.Commit(); err != nil {
		return out, unknownKubernetesInvocation()
	}
	if admitted == 0 {
		out.Disposition = "no_admission"
		return out, nil
	}
	out.Binding = kubesource.Binding{Operation: i.scope.Operation, Attempt: i.scope.Attempt, Stage: string(i.scope.Stage), BinaryBytes: i.scope.B}
	for _, r := range out.Rows {
		if !r.Admitted {
			continue
		}
		switch r.Role {
		case kubesource.SourceRole:
			out.Binding.SourceLeaseID = r.ID
		case kubesource.StorageRole:
			out.Binding.StorageLeaseID = r.ID
		case kubesource.PortRole:
			out.Binding.PortLeaseID = r.ID
		}
	}
	out.Disposition = "closed_originals"
	if retained {
		out.Disposition = "closed_originals_storage_retained"
	}
	return out, nil
}

func (i *KubernetesSourceInvocation) census(ctx context.Context, tx *sql.Tx) (result map[kubesource.Role]model.Lease, resultErr error) {
	// CASE guards storage class and byte length BEFORE projecting/allocating body.
	// Guard each comparison operand before TEXT conversion, including rows not
	// returned by the census. Bounded BLOB aliases are still selected and refused
	// by their physical storage class; either exact arm can expose the other
	// malformed or oversized physical column.
	// LIMIT4 bounds even hostile duplicate output to four1MiB bodies before
	// Scan; at most three are retained. Nonempty run bytes are never projected.
	query := `SELECT typeof(id),length(CAST(id AS BLOB)),CASE WHEN typeof(id)='text' AND length(CAST(id AS BLOB))=32 THEN CAST(id AS BLOB) END,typeof(request_key),length(CAST(request_key AS BLOB)),CASE WHEN typeof(request_key)='text' AND length(CAST(request_key AS BLOB)) BETWEEN 1 AND 128 THEN CAST(request_key AS BLOB) END,typeof(run),length(CAST(run AS BLOB)),CASE WHEN typeof(run)='text' AND length(CAST(run AS BLOB))=0 THEN X'' END,typeof(data),length(CAST(data AS BLOB)),CASE WHEN typeof(data)='text' AND length(CAST(data AS BLOB)) BETWEEN 1 AND 1048576 THEN CAST(data AS BLOB) END FROM leases WHERE `
	args := []any{}
	for n, c := range i.candidates {
		if n > 0 {
			query += ` OR `
		}
		query += `CASE WHEN typeof(id) IN ('text','blob') AND length(CAST(id AS BLOB))=32 THEN CAST(id AS TEXT) END=? OR CASE WHEN typeof(request_key) IN ('text','blob') AND length(CAST(request_key AS BLOB)) BETWEEN 1 AND 128 THEN CAST(request_key AS TEXT) END=?`
		args = append(args, c.id, c.key)
	}
	query += ` LIMIT 4`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	result = map[kubesource.Role]model.Lease{}
	total := 0
	for rows.Next() {
		var idType, keyType, runType, dataType string
		var idSize, keySize, dataSize int64
		var runSize sql.NullInt64
		var id, key, run, raw []byte
		if err = rows.Scan(&idType, &idSize, &id, &keyType, &keySize, &key, &runType, &runSize, &run, &dataType, &dataSize, &raw); err != nil {
			return nil, err
		}
		total += len(id) + len(key) + len(run) + len(raw)
		if idType != "text" || idSize != 32 || len(id) != 32 || keyType != "text" || keySize < 1 || keySize > 128 || int64(len(key)) != keySize || runType != "null" || runSize.Valid || run != nil || dataType != "text" || dataSize < 1 || dataSize > kubernetesInvocationRowBytes || int64(len(raw)) != dataSize || total > kubernetesInvocationTupleBytes {
			return nil, fmt.Errorf("noncanonical source census storage")
		}
		var c *kubernetesInvocationCandidate
		for n := range i.candidates {
			v := &i.candidates[n]
			if v.id == string(id) && v.key == string(key) {
				c = v
				break
			}
		}
		if c == nil {
			return nil, fmt.Errorf("source census physical identity alias")
		}
		if _, ok := result[c.role]; ok {
			return nil, fmt.Errorf("duplicate source census role")
		}
		var l model.Lease
		if err = json.Unmarshal(raw, &l); err != nil {
			return nil, err
		}
		canonical, e := json.Marshal(l)
		if e != nil || !bytes.Equal(canonical, raw) || l.ID != c.id || l.Request.Key != c.key {
			return nil, fmt.Errorf("noncanonical source census body")
		}
		result[c.role] = l
	}
	return result, rows.Err()
}
func (i *KubernetesSourceInvocation) validateCensusLease(c kubernetesInvocationCandidate, l model.Lease, k *KubernetesSourcePreparation) error {
	var original model.Lease
	if len(c.initial) == 0 || json.Unmarshal(c.initial, &original) != nil {
		return fmt.Errorf("missing original admission binding")
	}
	if c.role == kubesource.StorageRole {
		// Admission is recorded before provider.Create. Only an acknowledged
		// original provisioned parent may add the provider's exact pair of
		// capabilities; no other role or changed cached list is accepted.
		provisioned := append(append([]string{}, original.Capabilities...), "managed-storage", "explicit-storage-consumers")
		if k.parent.ID != c.id {
			return fmt.Errorf("original storage provisioning acknowledgment differs")
		}
		if k.parent.Artifact != nil && k.parent.Artifact.Identity != nil && k.parent.Artifact.Phase == "owned" {
			original.Capabilities = provisioned
		}
		if !reflect.DeepEqual(k.parent.Capabilities, original.Capabilities) {
			return fmt.Errorf("original storage capabilities differ")
		}
	}
	scope, role, err := kubesource.ValidateRoleKind(l)
	immutable := l
	immutable.State = original.State
	immutable.Outcome = original.Outcome
	immutable.Failure = original.Failure
	immutable.RecoveryReason = original.RecoveryReason
	immutable.Revision = original.Revision
	immutable.Ended = original.Ended
	immutable.Artifact = original.Artifact
	immutable.ManagedDir = original.ManagedDir
	immutable.Ports = original.Ports
	immutable.Request.StorageLease = original.Request.StorageLease
	immutable.Request.PortLease = original.Request.PortLease
	if !reflect.DeepEqual(immutable, original) {
		return fmt.Errorf("original immutable admission fields differ")
	}
	if err != nil || scope != i.scope || role != c.role || l.Host != original.Host || l.Owner != original.Owner || l.OwnerUsername != original.OwnerUsername || !l.Created.Equal(original.Created) || !reflect.DeepEqual(l.Started, original.Started) || !reflect.DeepEqual(l.Clock, original.Clock) || !reflect.DeepEqual(l.Expires, original.Expires) || l.PID != 0 || l.Process != nil || l.Usage != nil || l.Revision < original.Revision {
		return fmt.Errorf("original source admission identity differs")
	}
	switch c.role {
	case kubesource.SourceRole:
		if !reflect.DeepEqual(l, k.transit.result) || l.State != "terminal" || l.Ended == nil || !k.transit.settled {
			return fmt.Errorf("source original terminal acknowledgment differs")
		}
	case kubesource.PortRole:
		if !reflect.DeepEqual(l, k.port) || l.State != "terminal" || l.Ended == nil {
			return fmt.Errorf("port original terminal acknowledgment differs")
		}
	case kubesource.StorageRole:
		parent := k.parent
		if l.ID != parent.ID || !reflect.DeepEqual(l.Request, parent.Request) || l.ManagedDir != parent.ManagedDir || l.Artifact == nil || parent.Artifact == nil || l.Artifact.Name != parent.Artifact.Name || l.Artifact.Path != parent.Artifact.Path || !reflect.DeepEqual(l.Artifact.Identity, parent.Artifact.Identity) || parent.Artifact.ContentObserved && (!l.Artifact.ContentObserved || !reflect.DeepEqual(l.Artifact.AllocatedBefore, parent.Artifact.AllocatedBefore)) {
			return fmt.Errorf("original scratch identity differs")
		}
		if l.State != "ending" && l.State != "needs_attention" && l.State != "terminal" || l.State == "terminal" && (l.Ended == nil || l.Artifact.Phase != "removed" || !l.Artifact.NamespaceRemoved) || l.State != "terminal" && l.Ended != nil {
			return fmt.Errorf("original scratch lifecycle unknown")
		}
	}
	return nil
}
