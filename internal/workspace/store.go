package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/inferal-oss/arm/internal/local"
)

type Clock interface {
	Wall() time.Time
	Elapsed() time.Duration
}
type realClock struct{ start time.Time }

func (c realClock) Wall() time.Time        { return time.Now().UTC() }
func (c realClock) Elapsed() time.Duration { return time.Since(c.start) }

type Store struct {
	db              *sql.DB
	authority       Authority
	self            Identity
	clock           Clock
	boot            string
	mu              sync.Mutex
	wallStart       time.Time
	elapsedStart    time.Duration
	clockInvalid    string // first failure observation; retained under mu for this process
	directoryGraphs map[string]directoryGraphWitness
	// Optional deterministic failure injection for in-package transactional
	// tests. Never a public RPC/CLI option; no callback spans network I/O.
	fault func(string) error
}

func Open(ctx context.Context, db *sql.DB, authority Authority, self Identity, clock Clock) (*Store, error) {
	if db == nil || !validAuthority(authority) || authority.UID != strconv.Itoa(os.Geteuid()) || self.Host != authority.Host {
		return nil, fmt.Errorf("authenticated workspace authority required")
	}
	if _, e := ValidateIdentity(self); e != nil {
		return nil, e
	}
	if clock == nil {
		clock = realClock{time.Now()}
	}
	s := &Store{db: db, authority: authority, self: self, clock: clock, boot: local.ID(), wallStart: clock.Wall(), elapsedStart: clock.Elapsed()}
	if _, e := s.Read(ctx); e != nil {
		return nil, e
	}
	return s, nil
}
func (s *Store) checkClock(floor time.Time) error {
	if s.clockInvalid != "" {
		return problem("workspace_clock_unavailable", s.clockInvalid)
	}
	wall := s.clock.Wall()
	elapsed := s.clock.Elapsed() - s.elapsedStart
	delta := wall.Sub(s.wallStart) - elapsed
	var reason string
	switch {
	case elapsed < 0:
		reason = fmt.Sprintf("elapsed clock moved backwards (relative elapsed %s)", elapsed)
	case delta > MaxClockSkew || delta < -MaxClockSkew:
		reason = fmt.Sprintf("wall and elapsed time differ by %s", delta)
	case !floor.IsZero() && wall.Before(floor):
		reason = fmt.Sprintf("wall time is before the recorded workspace floor by %s", floor.Sub(wall))
	default:
		return nil
	}
	// Preserve only the first bounded observation, never an inferred cause such
	// as suspend/NTP. Return a fresh error so callers cannot mutate the latch.
	s.clockInvalid = fmt.Sprintf("workspace clock invalidated at %s: %s; workspace authorization remains disabled for this process", wall.UTC().Format(time.RFC3339Nano), reason)
	return problem("workspace_clock_unavailable", s.clockInvalid)
}
func (s *Store) now() time.Time { return s.clock.Wall().UTC() }
func (s *Store) fail(stage string) error {
	if s.fault != nil {
		return s.fault(stage)
	}
	return nil
}

// Records is a bounded complete ledger view used by pure relationship/proof
// validators. It includes private operation evidence and is NOT a public DTO.
type Records struct {
	rawBytes        int64
	RecoveryEnabled bool
	Initialized     bool
	Instances       []Instance
	Members         []Member
	Trusted         []TrustedMember
	Observations    []Observation
	Operations      []Operation
	Changes         []Change
	Denies          []Deny
	Clocks          []ClockRecord
}

func emptyRecords() Records {
	return Records{Instances: []Instance{}, Members: []Member{}, Trusted: []TrustedMember{}, Observations: []Observation{}, Operations: []Operation{}, Changes: []Change{}, Denies: []Deny{}, Clocks: []ClockRecord{}}
}
func (s *Store) Read(ctx context.Context) (result Records, err error) {
	err = s.readPublished(ctx, func(v Records) error { result = v; return nil })
	return
}

// readPublished observes SQL and process-only witnesses under the same Store
// lock. Callbacks may briefly acquire Runtime.mu; they never do external I/O.
func (s *Store) readPublished(ctx context.Context, observe func(Records) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	v, e := readRecords(ctx, tx, s.authority)
	if e != nil {
		return stateProblem(e)
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	return observe(v)
}
func (s *Store) transaction(ctx context.Context, initialize bool, fn func(*sql.Tx, *Records) error) error {
	return s.transactionChecked(ctx, initialize, fn, nil)
}

// transactionChecked adds an internal final authority check immediately before
// commit, AFTER structural validation and fault hooks. It must be pure local
// ledger/clock work: no network, manager lock or external authorization I/O.
func (s *Store) transactionChecked(ctx context.Context, initialize bool, fn func(*sql.Tx, *Records) error, final func(*Records) error) error {
	return s.transactionPublished(ctx, initialize, fn, final, nil)
}

// transactionPublished serializes a committed ledger and its ephemeral witness.
// publish runs under Store.mu after commit/ACK validation; success=false means
// SQL may have committed but its new authority MUST remain display-only. No
// Runtime.mu holder may wait for this lock. publish performs local memory work
// only, with no SQL, network, native delivery, or Manager.mu acquisition.
func (s *Store) transactionPublished(ctx context.Context, initialize bool, fn func(*sql.Tx, *Records) error, final func(*Records) error, publish func(bool) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	v, e := readRecords(ctx, tx, s.authority)
	if e != nil {
		return stateProblem(e)
	}
	if !v.Initialized {
		if !initialize {
			return problem("workspace_uninitialized", "no workspace ledger exists")
		}
		if e = install(ctx, tx); e != nil {
			return e
		}
		v.Initialized = true
		v.RecoveryEnabled = true
	}
	if initialize && !v.RecoveryEnabled {
		if e = enableRecoverySchema(ctx, tx, &v); e != nil {
			return e
		}
	}
	if e = fn(tx, &v); e != nil {
		return e
	}
	if e = s.fail("before_validate"); e != nil {
		return e
	}
	validated, e := readRecords(ctx, tx, s.authority)
	if e != nil {
		var capacity *capacityProblem
		if errors.As(e, &capacity) {
			return problem("history_full", capacity.Error())
		}
		return stateProblem(e)
	}
	if e = s.fail("before_commit"); e != nil {
		return e
	}
	if final != nil {
		if e = final(&validated); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		if publish != nil {
			_ = publish(false)
		}
		return problem("workspace_outcome_unknown", "workspace commit outcome unknown; inspect exact key before another mutation")
	}
	if e = s.fail("after_commit"); e != nil {
		if publish != nil {
			_ = publish(false)
		}
		return problem("workspace_outcome_unknown", "workspace reply lost after commit; inspect exact key")
	}
	if publish != nil {
		return publish(true)
	}
	return nil
}
func encode(v any) (string, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	if len(raw) > MaxRecordBytes {
		return "", fmt.Errorf("workspace record exceeds limit")
	}
	return string(raw), nil
}
func put(ctx context.Context, tx *sql.Tx, query string, v any, args ...any) error {
	raw, e := encode(v)
	if e != nil {
		return e
	}
	args = append(args, raw)
	_, e = tx.ExecContext(ctx, query, args...)
	return e
}
func writeInstance(ctx context.Context, tx *sql.Tx, v Instance) error {
	return put(ctx, tx, `INSERT INTO workspace_instances(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, v, v.ID)
}
func writeMember(ctx context.Context, tx *sql.Tx, v Member) error {
	return put(ctx, tx, `INSERT INTO workspace_directory_members(workspace,host,data) VALUES(?,?,?) ON CONFLICT(workspace,host) DO UPDATE SET data=excluded.data`, v, v.Workspace, v.Identity.Host)
}
func writeTrusted(ctx context.Context, tx *sql.Tx, v TrustedMember) error {
	return put(ctx, tx, `INSERT INTO workspace_trusted_members(workspace,host,data) VALUES(?,?,?) ON CONFLICT(workspace,host) DO UPDATE SET data=excluded.data`, v, v.Workspace, v.Identity.Host)
}
func writeClock(ctx context.Context, tx *sql.Tx, v ClockRecord) error {
	return put(ctx, tx, `INSERT INTO workspace_clocks(workspace,data) VALUES(?,?) ON CONFLICT(workspace) DO UPDATE SET data=excluded.data`, v, v.Workspace)
}
func writeOperation(ctx context.Context, tx *sql.Tx, v Operation) error {
	raw, e := encode(v)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO workspace_operations(workspace,principal,key,request,data) VALUES(?,?,?,?,?) ON CONFLICT(workspace,principal,key) DO UPDATE SET request=excluded.request,data=excluded.data`, v.Workspace, v.Principal, v.Key, string(v.Request), raw)
	return e
}
func writeChange(ctx context.Context, tx *sql.Tx, v Change) error {
	return put(ctx, tx, `INSERT INTO workspace_changes(workspace,data) VALUES(?,?)`, v, v.Workspace)
}
func rootOf(v *Records, id string) (Instance, error) {
	for _, x := range v.Instances {
		if x.ID == id {
			return x, nil
		}
	}
	return Instance{}, problem("not_found", "workspace not found")
}
func memberOf(v *Records, id, host string) (Member, bool) {
	for _, x := range v.Members {
		if x.Workspace == id && x.Identity.Host == host {
			return x, true
		}
	}
	return Member{}, false
}
func clockOf(v *Records, id string) (ClockRecord, error) {
	for _, x := range v.Clocks {
		if x.Workspace == id {
			return x, nil
		}
	}
	return ClockRecord{}, problem("workspace_state_invalid", "workspace clock floor missing")
}
func operationOf(v *Records, id, principal, key string) (Operation, bool) {
	for _, x := range v.Operations {
		if x.Workspace == id && x.Principal == principal && x.Key == key {
			return x, true
		}
	}
	return Operation{}, false
}
func denied(v *Records, id, host string) bool {
	for _, x := range v.Denies {
		if x.Workspace == id && x.Host == host {
			return true
		}
	}
	return false
}
func counts(v *Records, id string) (members, operations, changes int) {
	for _, x := range v.Members {
		if x.Workspace == id && x.State != "revoked" {
			members++
		}
	}
	for _, x := range v.Operations {
		if x.Workspace == id {
			operations++
		}
	}
	for _, x := range v.Changes {
		if x.Workspace == id {
			changes++
		}
	}
	return
}
func reserved(o Operation) int {
	switch o.Phase {
	case "awaiting_review":
		if o.Kind == "join" {
			return 2
		}
		return 1
	case "approved":
		if o.Kind == "admit" {
			return 2
		}
		return 1
	case "proving", "unknown", "interrupted":
		return 1
	}
	return 0
}

// Reserve enough journal slots to finish/cancel each admitted intent. A full
// operation roster must not prevent settlement of its already recorded rows.
func room(v *Records, id string, after Operation) error {
	_, ops, changes := counts(v, id)
	found := false
	pending := 0
	for _, o := range v.Operations {
		if o.Workspace != id {
			continue
		}
		if o.Principal == after.Principal && o.Key == after.Key {
			found = true
			continue
		}
		pending += reserved(o)
	}
	retirement := 1
	if after.Kind == "retire" || known(after.Kind, "create", "join") && known(after.Phase, "declined", "cancelled") {
		retirement = 0
	}
	operationLimit := MaxOperations - 1 // Reserve a receipt for explicit local retirement.
	if after.Kind == "retire" {
		operationLimit = MaxOperations
	}
	if !found && ops >= operationLimit || changes+1+pending+reserved(after)+retirement > MaxChanges {
		return problem("history_full", "workspace audit/operation capacity exhausted; no evidence was deleted")
	}
	return nil
}
func (s *Store) authorize(v *Records, root Instance, p Principal, introducer bool) (Member, error) {
	if root.Role != "directory" || root.State != "active" || root.Self != s.self || p.Workspace != root.ID || p.Generation != root.Generation || !validPrincipal(p) || denied(v, root.ID, p.Host) {
		return Member{}, problem("forbidden", "workspace principal is not currently admitted")
	}
	m, ok := memberOf(v, root.ID, p.Host)
	if !ok || m.State != "active" || m.MembershipGeneration != p.MembershipGeneration || m.Identity.Fingerprint != p.Fingerprint || introducer && !m.Introducer {
		return Member{}, problem("forbidden", "workspace generation/pin/delegation changed")
	}
	c, e := clockOf(v, root.ID)
	if e != nil {
		return Member{}, e
	}
	if e = s.checkClock(c.WallFloor); e != nil {
		return Member{}, e
	}
	if e = identityCurrent(root.Self, s.now()); e != nil {
		return Member{}, e
	}
	if e = identityCurrent(m.Identity, s.now()); e != nil {
		return Member{}, e
	}
	return m, nil
}
