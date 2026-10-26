package manager

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sshservice"
	"github.com/yrashk/arm/internal/syncservice"
	"golang.org/x/crypto/ssh"
)

const sshSchema = `CREATE TABLE ssh_operations (principal TEXT NOT NULL,key TEXT NOT NULL,lease TEXT NOT NULL UNIQUE,data TEXT NOT NULL,PRIMARY KEY(principal,key))`
const sshRedactedCommand = "[arm-ssh:redacted]"

// ensureSSHRecords is deliberately lazy during this private integration slice.
// Ordinary existing installations do not acquire an unused schema. Missing
// previously used evidence must never become an empty idempotency namespace.
func (m *Manager) ensureSSHRecords() error {
	var n int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='ssh_operations'`).Scan(&n); err != nil {
		return err
	}
	var marker string
	err := m.db.QueryRow(`SELECT value FROM metadata WHERE key='ssh_schema'`).Scan(&marker)
	if n == 1 {
		if err != nil || marker != "1" {
			return model.Err("ssh_evidence_unavailable", "SSH schema marker missing or unfamiliar")
		}
		var schema string
		if err = m.db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name='ssh_operations'`).Scan(&schema); err != nil {
			return err
		}
		if schema != sshSchema {
			return model.Err("ssh_evidence_unavailable", "SSH operation schema changed")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.Err("ssh_evidence_unavailable", "SSH operation table missing from an established namespace")
	}
	if err = m.db.QueryRow(`SELECT COUNT(*) FROM leases WHERE run IS NOT NULL AND json_type(run,'$.ssh') IS NOT NULL`).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return model.Err("ssh_evidence_unavailable", "SSH leases exist without their operation ledger")
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(sshSchema); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO metadata(key,value) VALUES('ssh_schema','1')`); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Manager) insertSSHOperation(tx *sql.Tx, l model.Lease, r *RunRequest) error {
	if r == nil || r.sshRuntime == nil {
		return nil
	}
	op := model.SSHOperation{Schema: 1, Request: r.sshRuntime.request, Lease: l.ID, AdmittedAt: time.Now().UTC()}
	raw, err := json.Marshal(op)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO ssh_operations(principal,key,lease,data) VALUES(?,?,?,?)`, op.Request.Source, op.Request.Key, l.ID, string(raw)); err != nil {
		return err
	}
	if r.sshRuntime.commitment != nil {
		if err = insertSSHAdmittedFence(r.sshRuntime.ctx, tx, *r.sshRuntime.commitment, op, l); err != nil {
			return err
		}
	}
	return event(tx, "ssh.admitted", l.ID, op)
}

func sshDigest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func sshHex(s string, n int) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == n && s == strings.ToLower(s)
}
func sshLeaseKey(q model.SSHExecutionRequest) string { return mesh.Key(q.Source, "ssh-exec\x00"+q.Key) }
func sshDescriptor(q model.SSHExecutionRequest) *model.SSHRunDescriptor {
	return &model.SSHRunDescriptor{Schema: 1, Source: q.Source, Key: q.Key, Kind: q.Kind, CommandSHA256: q.CommandSHA256, TerminalSHA256: q.TerminalSHA256}
}

func (m *Manager) lookupSSH(source, key string) (model.SSHLookup, error) {
	var raw string
	err := m.db.QueryRow(`SELECT data FROM ssh_operations WHERE principal=? AND key=?`, source, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		var n int
		if e := m.db.QueryRow(`SELECT COUNT(*) FROM leases WHERE request_key=?`, mesh.Key(source, "ssh-exec\x00"+key)).Scan(&n); e != nil {
			return model.SSHLookup{}, e
		}
		if n != 0 {
			return model.SSHLookup{}, model.Err("ssh_evidence_unavailable", "SSH lease exists without its keyed intent")
		}
		return model.SSHLookup{}, nil
	}
	if err != nil {
		return model.SSHLookup{}, err
	}
	if len(raw) > 32<<10 {
		return model.SSHLookup{}, model.Err("ssh_evidence_unavailable", "SSH record exceeds bound")
	}
	var op model.SSHOperation
	if err = mesh.Decode([]byte(raw), &op); err != nil {
		return model.SSHLookup{}, err
	}
	canonical, _ := json.Marshal(op)
	if string(canonical) != raw {
		return model.SSHLookup{}, model.Err("ssh_evidence_unavailable", "SSH record is not exact canonical evidence")
	}
	if op.Schema != 1 || op.Request.Source != source || op.Request.Key != key || op.Request.Target != m.host || op.Lease == "" || op.AdmittedAt.IsZero() {
		return model.SSHLookup{}, model.Err("ssh_evidence_unavailable", "SSH operation identity is inconsistent")
	}
	l, err := m.get(op.Lease)
	if err != nil {
		return model.SSHLookup{}, err
	}
	var runRaw string
	if err = m.db.QueryRow(`SELECT run FROM leases WHERE id=?`, l.ID).Scan(&runRaw); err != nil {
		return model.SSHLookup{}, err
	}
	var run RunRequest
	if err = mesh.Decode([]byte(runRaw), &run); err != nil {
		return model.SSHLookup{}, err
	}
	canonical, _ = json.Marshal(run)
	if string(canonical) != runRaw {
		return model.SSHLookup{}, model.Err("ssh_evidence_unavailable", "SSH run record is not exact canonical evidence")
	}
	if l.Request.Key != sshLeaseKey(op.Request) || l.Request.Peer != source || l.Request.OriginKey != key || !reflect.DeepEqual(run.SSH, sshDescriptor(op.Request)) || !reflect.DeepEqual(run.Command, []string{sshRedactedCommand}) || !reflect.DeepEqual(run.Request, l.Request) || op.Request.Account != l.OwnerUsername || op.Request.Reason != l.Request.Reason || op.Request.TTL != l.Request.TTL || op.Request.Resources != l.Request.Resources || op.Request.CWD != run.CWD || op.Request.Certificate != l.Request.PeerCertificate || op.Request.Revision != l.Request.PeerGrantRevision {
		return model.SSHLookup{}, model.Err("ssh_evidence_unavailable", "SSH lease and operation do not match")
	}
	return model.SSHLookup{Found: true, Operation: &op, Lease: &l}, nil
}

// LookupSSH is read-only and principal-scoped. It never reconnects or launches.
func (m *Manager) LookupSSH(source, key string) (model.SSHLookup, error) {
	if !sshHex(source, 16) || !mesh.ValidKey(key) {
		return model.SSHLookup{}, model.Err("invalid_request", "source and operation key required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='ssh_operations'`).Scan(&n); err != nil {
		return model.SSHLookup{}, err
	}
	if n == 0 {
		var evidence int
		if err := m.db.QueryRow(`SELECT (SELECT COUNT(*) FROM metadata WHERE key='ssh_schema')+(SELECT COUNT(*) FROM leases WHERE run IS NOT NULL AND json_type(run,'$.ssh') IS NOT NULL)`).Scan(&evidence); err != nil {
			return model.SSHLookup{}, err
		}
		if evidence != 0 {
			return model.SSHLookup{}, model.Err("ssh_evidence_unavailable", "SSH ledger is missing despite retained intent evidence")
		}
		return model.SSHLookup{}, nil
	}
	if err := m.ensureSSHRecords(); err != nil {
		return model.SSHLookup{}, err
	}
	return m.lookupSSH(source, key)
}

// AcquireSSH is a private integration entry, not a wire operation. Authentication
// and the current directional grant are destination-owned inputs. There is no
// existing-key replay: callers receive inspection with a nil process handle.
func (m *Manager) AcquireSSH(ctx context.Context, q model.SSHExecutionRequest, e sshservice.Execution, ch ssh.Channel, limit model.Resources, maxTTL time.Duration, validUntil time.Time) (model.Lease, sshservice.Process, error) {
	return m.acquireSSH(ctx, q, e, ch, limit, maxTTL, validUntil, nil, nil)
}

// AcquireSSHCommitted is the public-service adapter: its exact pre-exec
// commitment joins operation+lease in one transaction before the process gate.
// Actual command/channel remain runtime-only, never wire-decodable capability.
func (m *Manager) AcquireSSHCommitted(ctx context.Context, q model.SSHExecutionRequest, e sshservice.Execution, ch ssh.Channel, limit model.Resources, maxTTL time.Duration, validUntil time.Time, commitment model.SSHControlCommitment) (model.Lease, sshservice.Process, error) {
	if commitment.Validate() != nil || !commitment.Matches(q) {
		return model.Lease{}, nil, model.Err("invalid_request", "exact SSH control differs from execution")
	}
	return m.acquireSSH(ctx, q, e, ch, limit, maxTTL, validUntil, &commitment, nil)
}

func (m *Manager) acquireSSH(ctx context.Context, q model.SSHExecutionRequest, e sshservice.Execution, ch ssh.Channel, limit model.Resources, maxTTL time.Duration, validUntil time.Time, commitment *model.SSHControlCommitment, sync *syncAdmission) (model.Lease, sshservice.Process, error) {
	empty := model.Lease{}
	if ctx == nil || ch == nil {
		return empty, nil, model.Err("invalid_request", "live SSH context/channel required")
	}
	if err := ctx.Err(); err != nil {
		return empty, nil, err
	}
	if !sshHex(q.Source, 16) || !sshHex(q.Target, 16) || !sshHex(q.Pair, 32) || q.Revision < 1 || !sshHex(q.Certificate, 32) || !sshHex(q.PublicKeySHA256, 32) || !mesh.ValidKey(q.Key) || len(strings.TrimSpace(q.Reason)) == 0 || len(q.Reason) > 4096 || !filepath.IsAbs(q.CWD) || len(q.CWD) > 4096 || q.Kind != e.Kind || (e.Kind != "shell" && e.Kind != "exec") || len(e.Command) > 32768 || strings.ContainsRune(e.Command, 0) || e.Kind == "shell" && e.Command != "" || e.Kind == "exec" && e.Command == "" {
		return empty, nil, model.Err("invalid_request", "invalid SSH execution admission")
	}
	terminalRaw, _ := json.Marshal(e.Terminal)
	terminalHash := ""
	if e.Terminal != nil {
		terminalHash = sshDigest(terminalRaw)
	}
	if q.CommandSHA256 != sshDigest([]byte(e.Command)) || q.TerminalSHA256 != terminalHash {
		return empty, nil, model.Err("invalid_request", "SSH command/terminal commitment differs")
	}
	if math.IsNaN(q.Resources.CPU) || math.IsInf(q.Resources.CPU, 0) || q.Resources.CPU < 0 || q.Resources.Memory < 0 || q.Resources.Disk < 0 || q.Resources == (model.Resources{}) || q.TTL <= 0 || q.TTL > maxTTL || q.TTL > 7*24*time.Hour || !q.Resources.Fits(limit) || validUntil.IsZero() || !time.Now().Before(validUntil) {
		return empty, nil, model.Err("forbidden", "SSH resource/TTL grant is unavailable")
	}
	account, err := resolveSSHAccount(ctx)
	if err != nil {
		return empty, nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if q.Target != m.host || q.Account != m.ownerUsername || q.Account != account.Username || account.Uid != m.owner {
		return empty, nil, model.Err("forbidden", "SSH account/authority differs from destination owner")
	}
	if err = m.ensureSSHRecords(); err != nil {
		return empty, nil, err
	}
	if commitment != nil {
		if commitment.Owner != m.owner {
			return empty, nil, model.Err("forbidden", "SSH destination UID differs from committed control")
		}
		if err = m.ensureSSHKeyFences(ctx); err != nil {
			return empty, nil, err
		}
	}
	prior, err := m.lookupSSH(q.Source, q.Key)
	if err != nil {
		return empty, nil, err
	}
	if prior.Found {
		if sync != nil {
			return empty, nil, syncservice.ErrKeyExists
		}
		if !reflect.DeepEqual(prior.Operation.Request, q) {
			return empty, nil, model.Err("idempotency_conflict", "SSH key belongs to a different execution")
		}
		return *prior.Lease, nil, nil
	}
	present, fenceErr := m.sshFenceSchemaPresent(ctx)
	if fenceErr != nil {
		return empty, nil, fenceErr
	}
	if present {
		fence, fenceErr := readSSHKeyFence(ctx, m.db, q.Source, q.Key, m.host)
		if fenceErr != nil {
			return empty, nil, fenceErr
		}
		if fence != nil {
			if fence.State == "sealed" {
				return empty, nil, model.Err("ssh_key_sealed", "this key is durably sealed against execution")
			}
			return empty, nil, model.Err("ssh_evidence_unavailable", "SSH key fence has no matching admitted operation")
		}
		if commitment == nil {
			return empty, nil, model.Err("invalid_request", "committed SSH admission required for established key-fence namespace")
		}
	}
	leases, err := m.list()
	if err != nil {
		return empty, nil, err
	}
	if sync != nil {
		leases, err = sync.withoutPreparation(leases)
		if err != nil {
			return empty, nil, err
		}
	}
	var used model.Resources
	for _, l := range leases {
		if l.Request.Peer == q.Source && consuming(l.State) {
			used = used.Add(l.Request.Resources)
		}
	}
	if !q.Resources.FitsRemaining(used, limit) {
		return empty, nil, model.Err("forbidden", "aggregate peer ceiling exceeded")
	}
	runtime, err := newSSHRuntime(ctx, q, e, ch, account, validUntil)
	if err != nil {
		return empty, nil, err
	}
	if commitment != nil {
		c := *commitment
		runtime.commitment = &c
	}
	r := model.Request{Host: m.host, Reason: q.Reason, TTL: q.TTL, Resources: q.Resources, Key: sshLeaseKey(q), Kind: "", Peer: q.Source, OriginKey: q.Key, PeerCertificate: q.Certificate, PeerGrantRevision: q.Revision}
	run := &RunRequest{Request: r, Command: []string{sshRedactedCommand}, CWD: q.CWD, SSH: sshDescriptor(q), sshRuntime: runtime}
	l, err := m.acquireWithAdmission(r, run, false, sync)
	if err != nil || l.State != "active" {
		if sync != nil && sync.inline {
			runtime.closeAll()
			<-runtime.closeDone
			return l, nil, err
		}
		if err == nil {
			err = model.Err("ssh_launch_failed", fmt.Sprintf("execution did not become active (%s)", l.Outcome))
		}
		return l, &failedSSHTransport{runtime: runtime}, err
	}
	if sync != nil && sync.inline {
		hold := newSyncWorkerHold(m, l, sync, runtime)
		m.syncWorkers[l.ID] = hold
		return l, hold, nil
	}
	if m.jobs[l.ID] == nil {
		return l, &failedSSHTransport{runtime: runtime}, model.Err("ssh_evidence_unavailable", "admission did not return a newly owned process")
	}
	process := &sshProcess{manager: m, lease: l.ID, runtime: runtime, job: m.jobs[l.ID]}
	m.wg.Go(func() {
		select {
		case <-runtime.ctx.Done():
			m.mu.Lock()
			if m.jobs[l.ID] == process.job {
				outcome := "interrupted"
				if errors.Is(runtime.ctx.Err(), context.DeadlineExceeded) {
					outcome = "expired"
				}
				_, _ = m.end(l.ID, outcome)
			}
			m.mu.Unlock()
		case <-process.job.done:
		}
	})
	return l, process, nil
}
