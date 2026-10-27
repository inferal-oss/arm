package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"math"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/published"
)

const MaxProofBytes int64 = 64 << 20
const MaxProofBlockers = 100

// SettledProof is read-only evidence, never permission to delete. An apply
// operation must reacquire every authority/namespace lock and revalidate the
// complete snapshot. Complete is false on any unsupported/corrupt/bounded-out
// scope; an error must never be interpreted as an empty safe ledger.
type SettledProof struct {
	Schema         string              `json:"schema"`
	Host           string              `json:"host"`
	Owner          string              `json:"owner"`
	Observed       time.Time           `json:"observed_at"`
	Complete       bool                `json:"complete"`
	Settled        bool                `json:"settled"`
	Tables         map[string]int64    `json:"table_counts"`
	Blockers       []ProofBlocker      `json:"blockers"`
	EvidenceBytes  int64               `json:"evidence_bytes"`
	SHA256         string              `json:"evidence_sha256"`
	StorageRoot    *model.FileIdentity `json:"storage_root,omitempty"`
	Scope          string              `json:"scope"`
	removedStorage []string
}
type ProofBlocker struct {
	Kind   string `json:"kind"`
	Entity string `json:"entity"`
	Detail string `json:"detail"`
}
type proofRow map[string]any

func (r proofRow) text(key string) (string, error) {
	v, ok := r[key].(string)
	if !ok {
		return "", fmt.Errorf("%s must be SQLite TEXT, not missing/null or another type", key)
	}
	return v, nil
}
func (r proofRow) integer(key string) (int64, error) {
	v, ok := r[key].(int64)
	if !ok {
		return 0, fmt.Errorf("%s must be SQLite INTEGER", key)
	}
	return v, nil
}
func rowJSON[T any](r proofRow, key string, required ...string) (T, error) {
	var out T
	raw, err := r.text(key)
	if err == nil {
		err = strictEvidence(raw, &out, required...)
	}
	return out, err
}

type proofReader struct {
	leaseReviewSchema        bool
	home                     string
	ctx                      context.Context
	tx                       *sql.Tx
	catalog                  map[string][]string
	result                   SettledProof
	rows                     int
	digest                   hash.Hash
	leases                   map[string]model.Lease
	pools                    map[string]model.Pool
	budgets                  map[string]model.Budget
	bindings                 map[string]model.BudgetBinding
	messages                 map[string]proofMessage
	incidents                map[string]model.UsageIncident
	poolChanges              map[int64]model.PoolChange
	budgetChanges            map[int64]model.BudgetChange
	gradle                   map[string]proofGradle
	bulkIDs                  []string
	maxSequence              map[string]int64
	recordedSequences        map[string]int64
	subpoolSchema            bool
	workspaceSchema          bool
	routingSchema            bool
	publishedSchema          bool
	publishedRecords         *published.Records
	publishedLinks           map[string]proofPublishedLink
	routingGraphSchema       bool
	sshSchema                bool
	syncSchema               bool
	syncWorkspaceSchema      bool
	syncUsed                 bool
	cargoSchema              bool
	cargoRuns                map[string]proofSSHRun
	sshRuns                  map[string]proofSSHRun
	poolPaths                map[string][]string
	poolParentRevisions      map[int64]int64
	resetMetadata            *resetApplied
	poolOperationSequences   map[int64]bool
	budgetOperationSequences map[int64]bool
}

// ProveDatabaseSettled checks all current writer tables and relationships from
// a coherent stopped copy. External mesh/pending/storage namespaces are checked
// separately by ReviewSettled; this narrower result explicitly cannot authorize
// removal. Owner must be the authenticated local UID, not read from the ledger.
func ProveDatabaseSettled(ctx context.Context, s *Source, owner string) (SettledProof, error) {
	ctx, cancel := context.WithTimeout(ctx, FullTimeout)
	defer cancel()
	result := SettledProof{Schema: "arm-local-ledger-2026-10-v1", Owner: owner, Observed: SnapshotTime(), Tables: map[string]int64{}, Blockers: []ProofBlocker{}, Scope: "Database evidence only. External pending operations, mesh lifecycle/configuration and storage infrastructure must also be verified; this is not removal authority."}
	if s.live {
		return result, fmt.Errorf("settled proof requires stopped-exclusive authority")
	}
	if owner != strconv.Itoa(os.Geteuid()) || len(owner) > 32 {
		return result, fmt.Errorf("authenticated local owner required")
	}
	if _, err := strconv.ParseUint(owner, 10, 32); err != nil {
		return result, fmt.Errorf("authenticated numeric local UID required")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = RefuseRetiredMountState(ctx, tx); err != nil {
		return result, err
	}
	catalog, err := verifyResetSchema(ctx, tx)
	if err != nil {
		return result, err
	}
	p := &proofReader{ctx: ctx, home: s.Directory.Path, tx: tx, catalog: catalog, result: result, digest: sha256.New(), leases: map[string]model.Lease{}, pools: map[string]model.Pool{}, budgets: map[string]model.Budget{}, bindings: map[string]model.BudgetBinding{}, messages: map[string]proofMessage{}, incidents: map[string]model.UsageIncident{}, poolChanges: map[int64]model.PoolChange{}, budgetChanges: map[int64]model.BudgetChange{}, gradle: map[string]proofGradle{}, poolOperationSequences: map[int64]bool{}, budgetOperationSequences: map[int64]bool{}, maxSequence: map[string]int64{}, recordedSequences: map[string]int64{}}
	for _, step := range []func() error{p.metadata, p.readResetMetadata, p.readLeases, p.readSSH, p.readSync, p.readWorkspaces, p.readPublished, p.readRouting, p.readPools, p.readSubpools, p.readBudgets, p.readMessages, p.readLeaseReviews, p.readIncidents, p.readExports, p.readGradle, p.readCargo, p.readPoolJournal, p.readBudgetJournal, p.readOperations, p.readOther, p.relationships} {
		if err = step(); err != nil {
			return p.result, err
		}
	}
	// Structural corruption is not settled evidence, even if individual parsed
	// rows happened to look valid. This uses the same readonly transaction.
	var quick string
	if err = tx.QueryRowContext(ctx, `PRAGMA quick_check(1)`).Scan(&quick); err != nil {
		return p.result, err
	}
	if quick != "ok" {
		return p.result, fmt.Errorf("SQLite structural check did not certify reset evidence")
	}
	if err = tx.Commit(); err != nil {
		return p.result, err
	}
	if err = s.Validate(ctx, true); err != nil {
		return p.result, err
	}
	p.result.SHA256 = hex.EncodeToString(p.digest.Sum(nil))
	p.result.Complete = true
	p.result.Settled = len(p.result.Blockers) == 0
	return p.result, nil
}
func (p *proofReader) block(kind, id, detail string) error {
	if len(p.result.Blockers) >= MaxProofBlockers {
		return fmt.Errorf("settled proof exceeds 100 blockers; nothing was truncated or declared safe")
	}
	if len(id) > 128 {
		return fmt.Errorf("evidence identity exceeds 128 bytes")
	}
	p.result.Blockers = append(p.result.Blockers, ProofBlocker{kind, id, detail})
	return nil
}
func (p *proofReader) walk(table string, visit func(proofRow) error) error {
	columns, exists := p.catalog[table]
	if !exists {
		return nil
	}
	parts := make([]string, 0, len(columns)*3)
	for _, name := range columns {
		q := `"` + name + `"`
		parts = append(parts, `typeof(`+q+`)`, `length(CAST(`+q+` AS BLOB))`, `CASE WHEN length(CAST(`+q+` AS BLOB))<=1048576 THEN `+q+` END`)
	}
	rows, err := p.tx.QueryContext(p.ctx, `SELECT `+strings.Join(parts, ",")+` FROM "`+table+`" ORDER BY rowid`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err = p.ctx.Err(); err != nil {
			return err
		}
		p.rows++
		if p.rows > MaxEvidenceRows {
			return fmt.Errorf("settled proof exceeds 1000000 evidence rows")
		}
		values := make([]any, len(parts))
		targets := make([]any, len(parts))
		for i := range values {
			targets[i] = &values[i]
		}
		if err = rows.Scan(targets...); err != nil {
			return err
		}
		r := proofRow{}
		for i, name := range columns {
			kind, ok := values[3*i].(string)
			if !ok {
				return fmt.Errorf("invalid SQLite evidence type")
			}
			var length int64
			if v := values[3*i+1]; v != nil {
				length, ok = v.(int64)
				if !ok || length < 0 || length > MaxRecordBytes {
					return fmt.Errorf("%s.%s exceeds record bound", table, name)
				}
			}
			p.result.EvidenceBytes += length
			if p.result.EvidenceBytes > MaxProofBytes {
				return fmt.Errorf("settled proof exceeds 64 MiB evidence bound")
			}
			value := values[3*i+2]
			if kind != "text" && kind != "integer" && kind != "null" {
				return fmt.Errorf("unsupported SQLite evidence type for %s.%s", table, name)
			}
			r[name] = value
		}
		p.result.Tables[table]++
		// Publication sequences are explicitly scoped composite keys, not SQLite
		// AUTOINCREMENT counters; their complete sequence is proved separately.
		if seq, ok := r["sequence"].(int64); ok && table != "published_agent_changes" && seq > p.maxSequence[table] {
			p.maxSequence[table] = seq
		}
		raw, _ := json.Marshal([]any{table, r})
		p.digest.Write(raw)
		p.digest.Write([]byte{'\n'})
		if err = visit(r); err != nil {
			return fmt.Errorf("invalid %s reset evidence: %w", table, err)
		}
	}
	if _, ok := p.result.Tables[table]; !ok {
		p.result.Tables[table] = 0
	}
	return rows.Err()
}
func (p *proofReader) metadata() error {
	return p.walk("metadata", func(r proofRow) error {
		key, err := r.text("key")
		if err != nil {
			return err
		}
		if len(key) > 256 {
			return fmt.Errorf("metadata key exceeds proof bound")
		}
		raw, err := r.text("value")
		if err != nil {
			return err
		}
		switch key {
		case "host":
			if !validHost(raw) {
				return fmt.Errorf("invalid host authority")
			}
			p.result.Host = raw
		case "storage_root":
			var id model.FileIdentity
			if err = strictEvidence(raw, &id, "device", "inode", "owner", "birth_known"); err != nil {
				return err
			}
			if id.Inode == 0 || id.Owner != uint32Owner(p.result.Owner) {
				return fmt.Errorf("invalid recorded storage infrastructure identity")
			}
			p.result.StorageRoot = &id
		case "budget_bindings_v1":
			if raw != "1" {
				return fmt.Errorf("unknown budget binding schema")
			}
		case "routed_agents_schema":
			if raw != "1" {
				return fmt.Errorf("unknown published agent schema")
			}
			p.publishedSchema = true
		case "routing_schema":
			if raw != "1" && raw != "2" {
				return fmt.Errorf("unknown routing schema")
			}
			p.routingSchema = true
			p.routingGraphSchema = raw == "2"
		case "workspace_schema":
			if raw != "1" && raw != "2" {
				return fmt.Errorf("unknown workspace schema")
			}
			p.workspaceSchema = true
		case "lease_review_schema":
			if raw != "1" {
				return fmt.Errorf("unknown lease review schema")
			}
			p.leaseReviewSchema = true
		case "cargo_schema":
			if raw != "1" {
				return fmt.Errorf("unknown Cargo workload schema")
			}
			p.cargoSchema = true
		case "ssh_schema":
			if raw != "1" {
				return fmt.Errorf("unknown SSH execution schema")
			}
			p.sshSchema = true
		case "sync_schema":
			if raw != "1" && raw != "2" {
				return fmt.Errorf("unknown sync schema")
			}
			p.syncSchema = true
			p.syncWorkspaceSchema = raw == "2"
		case "sync_used":
			if raw != "1" {
				return fmt.Errorf("unknown sync use marker")
			}
			p.syncUsed = true
		case "ssh_fence_schema":
			if raw != "1" {
				return fmt.Errorf("unknown SSH key fence schema")
			}
		case "subpool_schema":
			if raw != "1" {
				return fmt.Errorf("unknown subpool schema")
			}
			p.subpoolSchema = true
		case resetAppliedKey:
			var applied resetApplied
			if err = strictEvidence(raw, &applied); err != nil {
				return err
			}
			p.resetMetadata = &applied
		case "scheduler_clock":
			at, e := time.Parse(time.RFC3339Nano, raw)
			if e != nil || at.IsZero() {
				return fmt.Errorf("invalid scheduler clock")
			}
		case "shutdown":
			var report model.ShutdownReport
			if err = strictEvidence(raw, &report, "id", "actor", "phase", "started_at", "remaining"); err != nil {
				return err
			}
			if report.Actor != p.result.Owner || report.ID == "" || report.Started.IsZero() {
				return fmt.Errorf("invalid shutdown actor/identity")
			}
			if report.Phase != "ready" && report.Phase != "completed" {
				return p.block("shutdown", report.ID, "Shutdown is not durably settled")
			}
			if len(report.Remaining) > 0 {
				return fmt.Errorf("settled shutdown still records remaining obligations")
			}
		default:
			if !strings.HasPrefix(key, "bulk:") {
				return fmt.Errorf("unknown metadata key %q", key)
			}
			var b struct {
				Request  model.BulkEndRequest `json:"request"`
				Actor    string               `json:"actor"`
				Username string               `json:"actor_username,omitempty"`
				At       time.Time            `json:"started_at"`
				IDs      []string             `json:"ids"`
			}
			if err = strictEvidence(raw, &b, "request", "actor", "started_at", "ids"); err != nil {
				return err
			}
			if b.Actor != p.result.Owner || key != "bulk:"+b.Request.Key || !oneOf(b.Request.Action, "release", "cancel") || b.At.IsZero() {
				return fmt.Errorf("invalid bulk intent")
			}
			// Store scope as bounded references; every referenced lease must exist
			// and be settled after the full lease scan (not just state counts).
			for _, id := range b.IDs {
				p.bulkIDs = append(p.bulkIDs, id)
			}
		}
		return nil
	})
}
func uint32Owner(owner string) uint32 { v, _ := strconv.ParseUint(owner, 10, 32); return uint32(v) }
func oneOf(value string, choices ...string) bool {
	for _, c := range choices {
		if value == c {
			return true
		}
	}
	return false
}
func validResources(r model.Resources) bool {
	return !math.IsNaN(r.CPU) && !math.IsInf(r.CPU, 0) && r.CPU >= 0 && r.Memory >= 0 && r.Disk >= 0
}
func evidenceID(s string) bool {
	return s != "" && len(s) <= 128 && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}
func (p *proofReader) readLeases() error {
	if !validHost(p.result.Host) {
		return fmt.Errorf("metadata host authority is missing")
	}
	return p.walk("leases", func(r proofRow) error {
		l, err := rowJSON[model.Lease](r, "data", "id", "host", "owner", "request", "state", "created_at", "revision")
		if err != nil {
			return err
		}

		leaseRaw, _ := r.text("data")
		var leaseFields map[string]json.RawMessage
		_ = json.Unmarshal([]byte(leaseRaw), &leaseFields)
		if cargoRaw, ok := leaseFields["cargo"]; ok {
			if string(cargoRaw) == "null" {
				return fmt.Errorf("explicit null Cargo binding is unsupported")
			}
			if e := cargoNoOptionalNull(cargoRaw, reflect.TypeOf(model.CargoBinding{})); e != nil {
				return e
			}
		}
		id, err := r.text("id")
		if err != nil {
			return err
		}
		key := ""
		if r["request_key"] != nil {
			key, err = r.text("request_key")
			if err != nil {
				return err
			}
		}
		if l.ID != id || !evidenceID(id) || l.Host != p.result.Host || l.Owner != p.result.Owner || !oneOf(l.Request.Host, "", "localhost", l.Host) || l.Request.Key != key || key != "" && !evidenceID(key) || l.Revision < 1 || l.Created.IsZero() || !validResources(l.Request.Resources) || l.Request.TTL <= 0 || l.Request.TTL > 7*24*time.Hour || strings.TrimSpace(l.Request.Reason) == "" || len(l.Request.Reason) > 4096 {
			return fmt.Errorf("lease identity/authority/request evidence differs")
		}
		if !oneOf(l.State, "queued", "provisioning", "active", "ending", "needs_attention", "reclaiming", "terminal") {
			return fmt.Errorf("unknown lease lifecycle")
		}
		if !oneOf(l.Request.Kind, "", "storage", "ports", "relay-transit", "sync-preparation", "forward-listener") {
			return fmt.Errorf("unknown lease provider kind")
		}
		if l.Request.ForwardSource != nil && l.Request.Kind != "forward-listener" {
			return fmt.Errorf("forward source binding on unrelated lease")
		}
		if l.Request.Kind == "forward-listener" {
			b := l.Request.ForwardSource
			if b == nil || r["run"] != nil || l.PID != 0 || l.Process != nil || l.Request.Resources.CPU <= 0 || l.Request.Resources.Memory < 1<<20 || l.Request.Resources.Disk != 0 || l.Request.Queue || l.Request.StartDeadline != nil || l.Request.Profile != "" || l.Request.Agent != "" || l.Request.StorageLease != "" || l.Request.ServiceLease != "" || l.Request.PortLease != "" || len(l.Request.Ports) != 1 || len(l.Ports) != 1 || len(l.Request.Pools) != 0 || l.Request.UsagePolicy != nil || l.Request.Peer != "" || l.Request.PeerCertificate != "" || l.Request.PeerGrantRevision != 0 || l.Request.OriginKey != "" || !strings.HasPrefix(l.Request.Key, "forward-listener:") || !validHost(b.Target) || !validDigest(b.TokenSHA256) || b.Workspace == "" || b.Service == "" || b.GrantRevision < 1 || !oneOf(l.State, "active", "ending", "needs_attention", "terminal") {
				return fmt.Errorf("invalid in-process forward listener obligation")
			}
			host, _, splitErr := net.SplitHostPort(b.Bind)
			ip := net.ParseIP(host)
			if splitErr != nil || ip == nil || !ip.IsLoopback() || l.Request.Ports[0].Mode != "hold" || l.Ports[0].Mode != "hold" {
				return fmt.Errorf("invalid exact forward source port authority")
			}
			if l.State == "terminal" && l.Outcome == "reconciled" && strings.TrimSpace(l.RecoveryReason) == "" {
				return fmt.Errorf("recovered source listener lacks reviewed reason")
			}
		}
		if l.Request.Kind == "sync-preparation" && (r["run"] != nil || l.PID != 0 || l.Process != nil || l.Request.Resources != (model.Resources{CPU: 1, Memory: 768 << 20, Disk: 1 << 20}) || l.Request.Queue || l.Request.StartDeadline != nil || l.Request.Profile != "" || l.Request.Agent != "" || l.Request.StorageLease != "" || l.Request.ServiceLease != "" || l.Request.PortLease != "" || len(l.Request.Ports) != 0 || len(l.Request.Pools) != 0 || l.Request.UsagePolicy != nil || (l.Request.Peer == "" && (l.Request.PeerCertificate != "" || l.Request.PeerGrantRevision != 0) || l.Request.Peer != "" && (!validHost(l.Request.Peer) || !validDigest(l.Request.PeerCertificate) || l.Request.PeerGrantRevision < 1)) || l.Request.OriginKey != "" || !oneOf(l.State, "active", "ending", "needs_attention", "terminal")) {
			return fmt.Errorf("invalid in-process sync preparation obligation")
		}
		if l.Request.Kind == "relay-transit" && (r["run"] != nil || l.PID != 0 || l.Process != nil || l.Request.Resources.CPU <= 0 || l.Request.Resources.Memory <= 0 || l.Request.Resources.Disk != 0 || l.Request.Queue || l.Request.StartDeadline != nil || l.Request.Profile != "" || l.Request.Agent != "" || l.Request.StorageLease != "" || l.Request.ServiceLease != "" || l.Request.PortLease != "" || len(l.Request.Ports) != 0 || len(l.Request.Pools) != 0 || l.Request.UsagePolicy != nil || l.Request.Peer != "" || l.Request.PeerCertificate != "" || l.Request.PeerGrantRevision != 0 || l.Request.OriginKey != "") {
			return fmt.Errorf("invalid in-process relay transit obligation")
		}
		if l.Request.Kind == "relay-transit" && !oneOf(l.State, "active", "ending", "needs_attention", "terminal") {
			return fmt.Errorf("invalid in-process relay lifecycle")
		}
		if l.State != "terminal" {
			if err = p.block("lease", id, "Lease still has queued or unsettled resource obligations"); err != nil {
				return err
			}
		} else if l.Ended == nil || l.Ended.IsZero() || l.Ended.Before(l.Created) || !(oneOf(l.Outcome, "released", "cancelled", "expired", "revoked", "completed", "failed", "launch_failed", "admission_failed", "interrupted", "reconciled", "handed_off", "rejected") || l.Request.Kind == "relay-transit" && oneOf(l.Outcome, "relay_closed", "forward_closed") || l.Request.Kind == "forward-listener" && oneOf(l.Outcome, "stopped", "listener_closed", "mesh_closed", "forward_listener_closed", "listener_admission_failed", "start_failed")) {
			return fmt.Errorf("terminal lease lacks a known settled outcome/time")
		}
		if l.Request.Kind == "storage" {
			if a := l.Artifact; a != nil {
				if p.result.StorageRoot == nil || a.Name != id || l.ManagedDir != filepath.Join(p.home, "storage", id) || !oneOf(a.Path, l.ManagedDir, filepath.Join(p.home, "storage", ".reclaim-"+id)) || a.Updated.IsZero() {
					return fmt.Errorf("invalid storage artifact identity")
				}
				if a.Phase == "handed_off" {
					if l.Outcome != "handed_off" {
						return fmt.Errorf("artifact handoff differs from lease outcome")
					}
				} else if a.Phase != "removed" || !a.NamespaceRemoved || a.Identity == nil || a.AllocatedBefore == nil || *a.AllocatedBefore != 0 || a.ContentObserved {
					if err = p.block("storage", id, "Storage cleanup/reclamation has not settled or been explicitly handed off"); err != nil {
						return err
					}
				}
				if a.Phase == "removed" {
					p.result.removedStorage = append(p.result.removedStorage, id)
				}
			} else if l.ManagedDir != "" || !oneOf(l.Outcome, "cancelled", "rejected") {
				return fmt.Errorf("storage lease lacks settled artifact evidence")
			}
		} else if l.Artifact != nil || l.ManagedDir != "" {
			return fmt.Errorf("non-storage lease carries artifact cleanup evidence")
		}
		for _, b := range l.Ports {
			if b.State != "observed_available" {
				if err = p.block("port", id, "A recorded port binding is not reconciled as available"); err != nil {
					return err
				}
			}
		}
		if run := r["run"]; run != nil {
			raw, ok := run.(string)
			if !ok {
				return fmt.Errorf("invalid run record")
			}
			var q proofSSHRun
			if err = strictEvidence(raw, &q, "command", "cwd"); err != nil {
				return err
			}
			if l.Cargo != nil {
				if p.cargoRuns == nil {
					p.cargoRuns = map[string]proofSSHRun{}
				}
				p.cargoRuns[id] = q
			}
			if q.SSH != nil {
				if p.sshRuns == nil {
					p.sshRuns = map[string]proofSSHRun{}
				}
				p.sshRuns[id] = q
			}
			if len(q.Command) == 0 || q.CWD == "" {
				return fmt.Errorf("missing supervised command evidence")
			}
		}
		p.leases[id] = l
		return nil
	})
}
func (p *proofReader) relationships() error {
	for table, seq := range p.maxSequence {
		if p.recordedSequences[table] < seq {
			return fmt.Errorf("audit sequence counter is missing or behind its journal")
		}
	}
	if err := p.policyJournalRelationships(); err != nil {
		return err
	}
	for _, msg := range p.messages {
		if msg.ReplyTo != "" {
			if _, ok := p.messages[msg.ReplyTo]; !ok {
				return fmt.Errorf("message reply references missing original")
			}
		}
		for _, id := range msg.NativeReplies {
			if _, ok := p.messages[id]; !ok {
				return fmt.Errorf("native reply mapping references missing message")
			}
		}
	}
	for _, id := range p.bulkIDs {
		if _, ok := p.leases[id]; !ok {
			return fmt.Errorf("bulk intent references a missing lease")
		}
	}
	for _, l := range p.leases {
		for _, target := range []string{l.Request.StorageLease, l.Request.PortLease, l.Request.ServiceLease} {
			if target != "" {
				if _, ok := p.leases[target]; !ok {
					return fmt.Errorf("lease attachment references a missing provider")
				}
			}
		}
		for _, claim := range l.Request.Pools {
			pool, ok := p.pools[claim.Pool]
			if !ok || claim.Quantity <= 0 || claim.Authority != pool.Authority || claim.Scope != pool.Scope || claim.Host != pool.Host {
				return fmt.Errorf("lease pool claim has invalid definition/authority")
			}
		}
		binding, exists := p.bindings[l.ID]
		if exists {
			if l.Budget != nil && !equalEvidence(*l.Budget, binding) {
				return fmt.Errorf("lease embedded budget evidence differs")
			}
		} else if l.Budget != nil || l.Request.Budget != "" {
			return fmt.Errorf("lease policy binding is missing")
		}
		if l.Workload != nil || oneOf(l.Request.Profile, "gradle-build", "gradle-service") {
			if _, ok := p.gradle[l.ID]; !ok {
				return fmt.Errorf("Gradle lease has no workload evidence")
			}
		}
	}
	for _, binding := range p.bindings {
		if err := p.validBinding(binding); err != nil {
			return err
		}
	}
	return nil
}
func equalEvidence(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
