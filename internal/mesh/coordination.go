package mesh

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

// Coordination is durable operator workflow evidence, separate from peer trust.
// Worker credentials and private review data never belong in browser projections.
type Coordination struct {
	ResumeFrom      string               `json:"resume_from,omitempty"`
	ParentKey       string               `json:"parent_key,omitempty"`
	ResolvedBy      string               `json:"resolved_by,omitempty"`
	Schema          int                  `json:"schema"`
	Key             string               `json:"key"`
	Kind            string               `json:"kind"`
	Digest          string               `json:"request_sha256"`
	Request         json.RawMessage      `json:"request,omitempty"`
	Actor           local.Identity       `json:"actor"`
	Targets         []string             `json:"targets"`
	Revision        int64                `json:"revision"`
	Phase           string               `json:"phase"`
	Updated         time.Time            `json:"updated_at"`
	Worker          string               `json:"worker,omitempty"`
	WorkerExpires   time.Time            `json:"worker_expires_at,omitempty"`
	Review          json.RawMessage      `json:"review,omitempty"`
	ReviewRevision  int64                `json:"review_revision"`
	ReviewHash      string               `json:"review_sha256,omitempty"`
	ReviewExpires   time.Time            `json:"review_expires_at,omitempty"`
	CancelRequested bool                 `json:"cancel_requested"`
	Approved        bool                 `json:"approved"`
	Result          json.RawMessage      `json:"result,omitempty"`
	Handoff         *CoordinationHandoff `json:"handoff,omitempty"`
	Error           *model.Error         `json:"error,omitempty"`
}

type CoordinationGuard struct {
	mu                sync.Mutex
	home, key, worker string
	revision          int64
}

func coordinationPath(home, key string) (string, error) {
	p, e := LifecycleOperationPath(home, key)
	if e != nil {
		return "", e
	}
	return filepath.Join(filepath.Dir(p), "coordinator-"+filepath.Base(p)), nil
}
func ReadCoordination(home, key string) (Coordination, bool, error) {
	var op Coordination
	p, e := coordinationPath(home, key)
	if e != nil {
		return op, false, e
	}
	raw, e := privateFile(p)
	if os.IsNotExist(e) {
		return op, false, nil
	}
	if e != nil {
		return op, false, e
	}
	if e = Decode(raw, &op); e != nil {
		return op, false, e
	}
	if op.Schema != 1 || op.Key != key || op.Revision < 1 || len(op.Targets) > 4 {
		return op, false, fmt.Errorf("invalid operator coordination record")
	}
	return op, true, nil
}
func ListCoordinations(home string) ([]Coordination, error) {
	dir, e := directory(filepath.Join(home, "mesh-lifecycle"))
	if os.IsNotExist(e) {
		return []Coordination{}, nil
	}
	if e != nil {
		return nil, e
	}
	defer dir.Close()
	entries, e := dir.ReadDir(513)
	if e != nil && e != io.EOF {
		return nil, e
	}
	if len(entries) > 512 {
		return nil, fmt.Errorf("operator history exceeds bound")
	}
	out := []Coordination{}
	for _, entry := range entries {
		if len(entry.Name()) != len("coordinator-operation-")+64+5 || entry.Name()[:len("coordinator-operation-")] != "coordinator-operation-" {
			continue
		}
		raw, e := privateFile(filepath.Join(home, "mesh-lifecycle", entry.Name()))
		if e != nil {
			return nil, e
		}
		var op Coordination
		if e = Decode(raw, &op); e != nil {
			return nil, e
		}
		if op.Schema != 1 || !ValidKey(op.Key) || op.Revision < 1 || len(op.Targets) > 4 {
			return nil, fmt.Errorf("invalid operator coordination record")
		}
		expected, _ := coordinationPath(home, op.Key)
		if filepath.Base(expected) != entry.Name() {
			return nil, fmt.Errorf("operator coordination identity mismatch")
		}
		out = append(out, op)
		if len(out) > 128 {
			return nil, fmt.Errorf("operator coordination history exceeds bounded roster")
		}
	}
	return out, nil
}
func coordinationActive(op Coordination) bool {
	return op.ResolvedBy == "" && op.Phase != "completed" && op.Phase != "declined" && op.Phase != "cancelled" && op.Phase != "rejected" && op.Phase != "handed_off"
}
func targetOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
func saveCoordination(home string, op Coordination) error {
	// Lifecycle writer creates/verifies this same private directory and bounds it.
	p, e := coordinationPath(home, op.Key)
	if e != nil {
		return e
	}
	parent, e := directory(home)
	if e != nil {
		return e
	}
	defer parent.Close()
	if e = unix.Mkdirat(int(parent.Fd()), "mesh-lifecycle", 0700); e != nil && e != unix.EEXIST {
		return e
	}
	dir, e := directory(filepath.Dir(p))
	if e != nil {
		return e
	}
	dir.Close()
	return save(p, op)
}

// ClaimCoordination serializes across processes using a short durable CAS lock.
// Expired workers are deliberately unresolved: their remote mutation may still
// have committed. A new worker never silently steals their target or retries it.
func ClaimCoordination(home, key, kind, digest, target string) (*CoordinationGuard, error) {
	decoded, decodeErr := hex.DecodeString(digest)
	if !ValidKey(key) || decodeErr != nil || len(decoded) != 32 || (kind != "connect" && kind != "reconnect" && kind != "disconnect" && kind != "provision") || len(digest) != 64 || target == "" || len(target) > 512 {
		return nil, model.Err("invalid_request", "invalid operator coordination intent")
	}
	unlock, e := coordinationLock(home)
	if e != nil {
		return nil, e
	}
	defer unlock()
	previous, exists, e := ReadCoordination(home, key)
	if e != nil {
		return nil, e
	}
	if exists {
		if previous.Kind != kind || previous.Digest != digest {
			return nil, model.Err("idempotency_conflict", "operator key belongs to another request")
		}
		return nil, model.Err("terminal_or_active_key", "operation key is already recorded; inspect its receipt or use explicit recovery, never replace it")
	}
	ops, e := ListCoordinations(home)
	if e != nil {
		return nil, e
	}
	if !exists && len(ops) >= 128 {
		return nil, model.Err("history_full", "operator history is full; no record was removed")
	}
	live := 0
	for _, op := range ops {
		if op.Worker != "" && time.Now().Before(op.WorkerExpires) {
			live++
		}
	}
	if live >= 2 {
		return nil, model.Err("busy", "at most two durable operator workers may run across CLI and backend")
	}
	for _, op := range ops {
		if op.Key != key && coordinationActive(op) && targetOverlap(op.Targets, []string{target}) {
			return nil, model.Err("mesh_target_busy", "another active or unresolved operation owns this target")
		}
	}
	worker := local.ID() + local.ID()
	op := Coordination{Schema: 1, Key: key, Kind: kind, Digest: digest, Targets: []string{target}, Revision: 1, Phase: "preparing", Worker: worker, Actor: local.CurrentIdentity(), Updated: time.Now().UTC()}

	op.WorkerExpires = op.Updated.Add(time.Minute)
	if e = saveCoordination(home, op); e != nil {
		return nil, e
	}
	return &CoordinationGuard{home: home, key: key, worker: worker, revision: op.Revision}, nil
}

// Change is a worker-only CAS transition. No external I/O or human wait belongs
// in fn. A crash after an external mutation remains active/unresolved on disk.
func (g *CoordinationGuard) Change(fn func(*Coordination) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	unlock, e := coordinationLock(g.home)
	if e != nil {
		return e
	}
	defer unlock()
	op, exists, e := ReadCoordination(g.home, g.key)
	if e != nil {
		return e
	}
	if !exists || op.Worker != g.worker || op.Revision != g.revision || !time.Now().Before(op.WorkerExpires) {
		return model.Err("mesh_operation_changed", "operator worker lost its durable revision or deadline")
	}
	if e = fn(&op); e != nil {
		return e
	}
	op.Revision++
	op.Updated = time.Now().UTC()
	if op.Worker != "" {
		op.WorkerExpires = op.Updated.Add(time.Minute)
	} else {
		op.WorkerExpires = time.Time{}
	}
	if e = saveCoordination(g.home, op); e != nil {
		return e
	}
	g.revision = op.Revision
	return nil
}
func (g *CoordinationGuard) BindTarget(target string) error {
	return g.Change(func(op *Coordination) error {
		if target == "" || len(target) > 512 {
			return model.Err("invalid_request", "invalid target")
		}
		for _, x := range op.Targets {
			if x == target {
				return nil
			}
		}
		all, e := ListCoordinations(g.home)
		if e != nil {
			return e
		}
		for _, other := range all {
			if other.Key != op.Key && coordinationActive(other) && targetOverlap(other.Targets, []string{target}) {
				return model.Err("mesh_target_busy", "another active or unresolved operation owns this stable target")
			}
		}
		if len(op.Targets) >= 4 {
			return model.Err("invalid_request", "too many operator target bindings")
		}
		op.Targets = append(op.Targets, target)
		return nil
	})
}
func (g *CoordinationGuard) Heartbeat() error {
	return g.Change(func(*Coordination) error { return nil })
}
func (g *CoordinationGuard) SaveLifecycle(op LifecycleOperation) error {
	return g.Change(func(*Coordination) error { return SaveLifecycleOperation(g.home, op) })
}

// Read refreshes a worker after a separately authenticated review decision.
// Only the same live worker may adopt that durable decision revision.
func (g *CoordinationGuard) Read() (Coordination, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	unlock, e := coordinationLock(g.home)
	if e != nil {
		return Coordination{}, e
	}
	defer unlock()
	op, exists, e := ReadCoordination(g.home, g.key)
	if e != nil {
		return op, e
	}
	if !exists || op.Worker != g.worker || !time.Now().Before(op.WorkerExpires) {
		return op, model.Err("mesh_operation_changed", "operator worker is no longer live")
	}
	g.revision = op.Revision
	return op, nil
}

// DecideCoordination is the only non-worker mutation. Its immutable review
// revision/hash authorizes exactly one plan; it cannot edit endpoints or grants.
func DecideCoordination(home, key string, revision int64, hash string, approve bool) (Coordination, error) {
	unlock, e := coordinationLock(home)
	if e != nil {
		return Coordination{}, e
	}
	defer unlock()
	op, exists, e := ReadCoordination(home, key)
	if e != nil {
		return op, e
	}
	if !exists {
		return op, model.Err("not_found", "operator operation not found")
	}
	if op.ReviewHash != hash || op.ReviewRevision != revision {
		return op, model.Err("review_changed", "review identity changed")
	}
	if op.Phase == "review_approved" && approve {
		return op, nil
	}
	if op.Phase == "review_declined" && !approve {
		return op, nil
	}
	if op.CancelRequested || op.Phase != "awaiting_review" || !time.Now().Before(op.ReviewExpires) || !time.Now().Before(op.WorkerExpires) {
		return op, model.Err("review_expired", "review no longer has live exact reservations")
	}
	op.Approved = approve
	op.Phase = "review_declined"
	if approve {
		op.Phase = "review_approved"
	}
	op.Revision++
	op.Updated = time.Now().UTC()
	if e = saveCoordination(home, op); e != nil {
		return op, e
	}
	return op, nil
}

func CancelCoordination(home, key string, revision int64) (Coordination, error) {
	unlock, e := coordinationLock(home)
	if e != nil {
		return Coordination{}, e
	}
	defer unlock()
	op, ok, e := ReadCoordination(home, key)
	if e != nil {
		return op, e
	}
	if !ok {
		return op, model.Err("not_found", "operator key not found")
	}
	if op.Revision != revision {
		return op, model.Err("review_changed", "operator revision changed; refresh first")
	}
	if !coordinationActive(op) {
		return op, nil
	}
	op.CancelRequested = true
	op.Revision++
	op.Updated = time.Now().UTC()
	if e = saveCoordination(home, op); e != nil {
		return op, e
	}
	return op, nil
}

// Bound contention on short journal transitions, never wait for human/SSH work.
func coordinationLock(home string) (func(), error) {
	if e := local.CheckStateFence(home); e != nil {
		return nil, e
	}
	until := time.Now().Add(250 * time.Millisecond)
	for {
		unlock, e := LockLifecycle(home)
		if e == nil {
			if fence := local.CheckStateFence(home); fence != nil {
				unlock()
				return nil, fence
			}
			return unlock, nil
		}
		if !errors.Is(e, unix.EWOULDBLOCK) || !time.Now().Before(until) {
			return nil, e
		}
		time.Sleep(2 * time.Millisecond)
	}
}

const CoordinationHandoffEffect = "Stop protecting this operator workflow target only. Do not revoke trust, close listeners, release leases, erase receipts or claim cleanup; inspect current nodes and use a fresh reviewed disconnect/connect key."

func CoordinationRecoveryHash(op Coordination) (string, error) {
	return ReviewDigest(struct {
		Key      string
		Revision int64
		Targets  []string
		Review   string
		Effect   string
	}{op.Key, op.Revision, op.Targets, op.ReviewHash, CoordinationHandoffEffect})
}

// HandoffCoordination is explicit reviewed local recovery, never automatic
// retry/rollback. It preserves original intent/review/result/provenance.
func HandoffCoordination(home, key string, revision int64, hash, reason string) (Coordination, error) {
	if strings.TrimSpace(reason) == "" || len(reason) > 4096 {
		return Coordination{}, model.Err("invalid_request", "bounded handoff reason required")
	}
	unlock, e := coordinationLock(home)
	if e != nil {
		return Coordination{}, e
	}
	defer unlock()
	op, ok, e := ReadCoordination(home, key)
	if e != nil {
		return op, e
	}
	if !ok {
		return op, model.Err("not_found", "operator key not found")
	}
	expected, e := CoordinationRecoveryHash(op)
	if e != nil {
		return op, e
	}
	if op.Revision != revision || hash != expected {
		return op, model.Err("review_changed", "handoff review changed")
	}
	if !coordinationActive(op) || op.Worker != "" && time.Now().Before(op.WorkerExpires) {
		return op, model.Err("worker_active", "only an inactive or expired unresolved worker can be handed off")
	}
	op.Phase = "handed_off"
	op.Worker = ""
	op.WorkerExpires = time.Time{}
	op.Revision++
	op.Updated = time.Now().UTC()
	op.Handoff = &CoordinationHandoff{Actor: local.CurrentIdentity(), At: op.Updated, Reason: reason, Effect: CoordinationHandoffEffect}
	if e = saveCoordination(home, op); e != nil {
		return op, e
	}
	return op, nil
}

type CoordinationHandoff struct {
	Actor  local.Identity `json:"actor"`
	At     time.Time      `json:"at"`
	Reason string         `json:"reason"`
	Effect string         `json:"effect"`
}

// ClaimCoordinationAttempt retains the original receipt and creates bounded
// separate evidence for explicit recovery of that same immutable intent.
func ClaimCoordinationAttempt(home, parent, target string) (*CoordinationGuard, error) {
	unlock, e := coordinationLock(home)
	if e != nil {
		return nil, e
	}
	defer unlock()
	original, ok, e := ReadCoordination(home, parent)
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, model.Err("not_found", "original operation not found")
	}
	requestedParent := parent
	seen := map[string]bool{parent: true}
	for original.ParentKey != "" {
		if len(seen) >= 16 || seen[original.ParentKey] {
			return nil, model.Err("invalid_request", "invalid recovery ancestry")
		}
		seen[original.ParentKey] = true
		ancestor, exists, err := ReadCoordination(home, original.ParentKey)
		if err != nil {
			return nil, err
		}
		if !exists || ancestor.Kind != original.Kind || ancestor.Digest != original.Digest {
			return nil, model.Err("idempotency_conflict", "recovery ancestry does not match immutable intent")
		}
		parent = ancestor.Key
		original = ancestor
	}
	if original.Worker != "" || original.ResolvedBy != "" || (original.Phase != "completed" && original.Phase != "incomplete") {
		return nil, model.Err("terminal_key", "this receipt cannot authorize another attempt")
	}
	all, e := ListCoordinations(home)
	if e != nil {
		return nil, e
	}
	targets := append([]string{}, original.Targets...)
	if !targetOverlap(targets, []string{target}) {
		targets = append(targets, target)
	}
	if target == "" || len(target) > 512 || len(targets) > 4 {
		return nil, model.Err("invalid_request", "invalid attempt target")
	}
	count, live := 0, 0
	for _, op := range all {
		if op.ParentKey == parent {
			count++
		}
		if op.Worker != "" && time.Now().Before(op.WorkerExpires) {
			live++
		}
		if op.Key != parent && !(op.ParentKey == parent && op.Phase == "incomplete" && op.Worker == "") && coordinationActive(op) && targetOverlap(op.Targets, targets) {
			return nil, model.Err("mesh_target_busy", "another unresolved operation owns target")
		}
	}
	if live >= 2 {
		return nil, model.Err("busy", "at most two durable operator workers may run across CLI and backend")
	}
	if count >= 16 || len(all) >= 128 {
		return nil, model.Err("history_full", "bounded recovery attempt history is full")
	}
	key := "attempt-" + local.ID()
	worker := local.ID() + local.ID()
	op := Coordination{Schema: 1, Key: key, ParentKey: parent, ResumeFrom: requestedParent, Kind: original.Kind, Digest: original.Digest, Request: original.Request, Actor: local.CurrentIdentity(), Targets: targets, Revision: 1, Phase: "preparing", Worker: worker, Updated: time.Now().UTC()}
	op.WorkerExpires = op.Updated.Add(time.Minute)
	if e = saveCoordination(home, op); e != nil {
		return nil, e
	}
	return &CoordinationGuard{home: home, key: key, worker: worker, revision: 1}, nil
}
func ResolveCoordinationParent(home, child string) error {
	unlock, e := coordinationLock(home)
	if e != nil {
		return e
	}
	defer unlock()
	attempt, ok, e := ReadCoordination(home, child)
	if e != nil {
		return e
	}
	if !ok || attempt.ParentKey == "" || attempt.Phase != "completed" {
		return nil
	}
	parent, ok, e := ReadCoordination(home, attempt.ParentKey)
	if e != nil {
		return e
	}
	if !ok || parent.Kind != attempt.Kind || parent.Digest != attempt.Digest {
		return fmt.Errorf("recovery parent identity mismatch")
	}
	if parent.Phase != "incomplete" {
		return nil
	}
	if parent.Worker != "" {
		return model.Err("worker_active", "parent worker is still unresolved")
	}
	if parent.ResolvedBy != "" {
		if parent.ResolvedBy == child {
			return nil
		}
		return model.Err("review_changed", "parent already resolved by another attempt")
	}
	all, e := ListCoordinations(home)
	if e != nil {
		return e
	}
	for _, prior := range all {
		if prior.ParentKey == parent.Key && prior.Key != child && prior.Phase == "incomplete" && prior.Worker == "" && prior.ResolvedBy == "" {
			prior.ResolvedBy = child
			prior.Revision++
			prior.Updated = time.Now().UTC()
			if e = saveCoordination(home, prior); e != nil {
				return e
			}
		}
	}
	parent.ResolvedBy = child
	parent.Revision++
	parent.Updated = time.Now().UTC()
	return saveCoordination(home, parent)
}

func (g *CoordinationGuard) Key() string { return g.key }
