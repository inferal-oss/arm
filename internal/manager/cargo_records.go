package manager

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/cargoprofile"
	"github.com/inferal-oss/arm/internal/leaseclock"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

const cargoRecordLimit = 256 << 10
const cargoRecordCountLimit = 4096

// Private durability record. No environment, complete TOML, credential bodies
// or caller-supplied helper/runtime authority is persisted.
type cargoRecord struct {
	Runtime        *cargoprofile.HelperRuntime  `json:"runtime,omitempty"`
	PreflightAt    *time.Time                   `json:"preflight_at,omitempty"`
	NoPreflight    bool                         `json:"no_preflight"`
	Schema         int                          `json:"schema"`
	Lease          string                       `json:"lease"`
	Host           string                       `json:"host"`
	Owner          string                       `json:"owner"`
	Storage        string                       `json:"storage"`
	RunSHA256      string                       `json:"run_sha256"`
	CommandSHA256  string                       `json:"command_sha256"`
	Nonce          string                       `json:"nonce"`
	Revision       int                          `json:"revision"`
	Phase          string                       `json:"phase"`
	Worker         string                       `json:"worker,omitempty"`
	Created        time.Time                    `json:"created"`
	Process        *model.ProcessIdentity       `json:"process,omitempty"`
	Births         []cargoprofile.BirthStep     `json:"births"`
	PolicyIntent   bool                         `json:"policy_intent"`
	Policy         *cargoprofile.FileEvidence   `json:"policy,omitempty"`
	Prepared       *cargoprofile.Prepared       `json:"prepared,omitempty"`
	PreparedSHA256 string                       `json:"prepared_sha256,omitempty"`
	AuthorizedAt   *time.Time                   `json:"authorized_at,omitempty"`
	Started        *cargoprofile.HelperStarted  `json:"started,omitempty"`
	Finished       *cargoprofile.HelperFinished `json:"finished,omitempty"`
	FinalAt        *time.Time                   `json:"final_at,omitempty"`
	GroupAbsent    bool                         `json:"group_absent"`
	NoSpawn        bool                         `json:"no_spawn"`
	Uncertain      bool                         `json:"uncertain"`
	Error          string                       `json:"error,omitempty"`
}

func cargoDigest(v any) string {
	raw, _ := json.Marshal(v)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func cargoCommandDigest(run *RunRequest) string {
	return cargoDigest(struct {
		Command []string `json:"command"`
		CWD     string   `json:"cwd"`
	}{run.Command, run.CWD})
}
func cargoNewRecord(l model.Lease, run *RunRequest) cargoRecord {
	rec := cargoRecord{Schema: 1, Lease: l.ID, Host: l.Host, Owner: l.Owner, Storage: l.Request.StorageLease, RunSHA256: cargoDigest(run), CommandSHA256: cargoCommandDigest(run), Nonce: local.ID() + local.ID(), Revision: 1, Phase: "admitted", Created: l.Created, Births: []cargoprofile.BirthStep{}}
	if l.State == "terminal" {
		now := time.Now().UTC()
		if l.Ended != nil {
			now = *l.Ended
		}
		rec.Phase = "finished"
		rec.GroupAbsent, rec.NoSpawn, rec.NoPreflight = true, true, true
		rec.FinalAt = &now
	}
	return rec
}
func projectCargo(l *model.Lease, rec cargoRecord) {
	binding := model.CargoBinding{Schema: 1, Adapter: "rust-build", State: rec.Phase, CommandSHA256: rec.CommandSHA256, PreparedSHA256: rec.PreparedSHA256, Detail: "Cargo jobs and inherited jobserver are cooperative. Build scripts/linkers can exceed budgets; shared Cargo caches/network are external and never cleanup authority."}
	if l.State == "queued" {
		binding.State = "queued"
	}
	if l.Started != nil {
		at := *l.Started
		binding.PreparingAt = &at
	}
	binding.AuthorizedAt = rec.AuthorizedAt
	if rec.Started != nil {
		at := rec.Started.At
		binding.StartedAt = &at
	}
	if rec.Prepared != nil {
		p := rec.Prepared
		binding.Jobs = p.Plan.Jobs
		binding.Workspace = p.Source.Workspace
		binding.Target = p.Owned.Target.Path
		binding.Build = p.Owned.Build.Path
		binding.Control = p.Owned.Control.Path
		binding.CargoVersion = p.Tools.CargoVersion
		binding.RustcVersion = p.Tools.RustcVersion
		binding.RustdocVersion = p.Tools.RustdocVersion
		binding.Toolchain = p.Tools.Toolchain
	}
	l.Cargo = &binding
}
func (m *Manager) initCargoRecords() error {
	var marker string
	e := m.db.QueryRow(`SELECT value FROM metadata WHERE key='cargo_schema'`).Scan(&marker)
	var count int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='cargo_workloads'`).Scan(&count); err != nil {
		return err
	}
	if errors.Is(e, sql.ErrNoRows) {
		if count != 0 {
			return model.Err("cargo_state_corrupt", "Cargo table is missing its explicit schema marker")
		}
		return nil
	}
	if e != nil {
		return e
	}
	if marker != "1" || count != 1 {
		return model.Err("cargo_state_corrupt", "unsupported/partial Cargo record schema")
	}
	rows, e := m.db.Query(`SELECT lease,substr(data,1,262145) FROM cargo_workloads LIMIT 4097`)
	if e != nil {
		return e
	}
	var records []cargoRecord
	for rows.Next() {
		var id, raw string
		if e = rows.Scan(&id, &raw); e != nil {
			rows.Close()
			return e
		}
		rec, e := decodeCargoRecord(raw)
		if e != nil || rec.Lease != id {
			rows.Close()
			return model.Err("cargo_state_corrupt", "Cargo record identity/encoding differs")
		}
		records = append(records, rec)
		if len(records) > cargoRecordCountLimit {
			rows.Close()
			return model.Err("cargo_state_corrupt", "Cargo record count exceeds bound")
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, rec := range records {
		l, e := m.get(rec.Lease)
		if e != nil || l.Request.Profile != "rust-build" || l.Request.StorageLease != rec.Storage || l.Host != rec.Host || l.Owner != rec.Owner || l.Cargo == nil {
			return model.Err("cargo_state_corrupt", "Cargo record/lease attachment differs")
		}
		expected := l
		projectCargo(&expected, rec)
		if !reflect.DeepEqual(expected.Cargo, l.Cargo) {
			return model.Err("cargo_state_corrupt", "Cargo public projection differs from durable record")
		}
		var raw string
		if e = m.db.QueryRow(`SELECT run FROM leases WHERE id=?`, l.ID).Scan(&raw); e != nil {
			return e
		}
		var run RunRequest
		if e = json.Unmarshal([]byte(raw), &run); e != nil || cargoDigest(&run) != rec.RunSHA256 || cargoCommandDigest(&run) != rec.CommandSHA256 {
			return model.Err("cargo_state_corrupt", "Cargo original run digest differs")
		}
	}
	return nil
}
func decodeCargoRecord(raw string) (cargoRecord, error) {
	var rec cargoRecord
	if len(raw) > cargoRecordLimit || cargoprofile.DecodeCanonical([]byte(raw), &rec) != nil {
		return rec, model.Err("cargo_state_corrupt", "Cargo record encoding exceeds known schema/bounds")
	}
	if rec.Schema != 1 || !cargoHex(rec.Lease, 32) || !cargoHex(rec.Nonce, 64) || rec.Revision < 1 || len(rec.RunSHA256) != 64 || len(rec.CommandSHA256) != 64 {
		return rec, model.Err("cargo_state_corrupt", "Cargo record identity is invalid")
	}
	switch rec.Phase {
	case "admitted", "spawn_intent", "helper_bound", "creating", "resolving", "prepared", "compilation_authorized", "compiling", "finished", "needs_attention":
	default:
		return rec, model.Err("cargo_state_corrupt", "unknown Cargo record phase")
	}
	return rec, nil
}
func (m *Manager) getCargo(id string) (cargoRecord, error) {
	var raw string
	e := m.db.QueryRow(`SELECT substr(data,1,262145) FROM cargo_workloads WHERE lease=?`, id).Scan(&raw)
	if e != nil {
		return cargoRecord{}, e
	}
	rec, e := decodeCargoRecord(raw)
	if e != nil {
		return rec, e
	}
	if rec.Lease != id {
		return rec, model.Err("cargo_state_corrupt", "Cargo record identity differs")
	}
	return rec, nil
}
func insertCargoRecord(tx *sql.Tx, l model.Lease, run *RunRequest) error {
	if l.Cargo == nil {
		return nil
	}
	if _, e := tx.Exec(`CREATE TABLE IF NOT EXISTS cargo_workloads (lease TEXT PRIMARY KEY,data TEXT NOT NULL)`); e != nil {
		return e
	}
	var count int
	if e := tx.QueryRow(`SELECT COUNT(*) FROM cargo_workloads`).Scan(&count); e != nil {
		return e
	}
	if count >= cargoRecordCountLimit {
		return model.Err("cargo_capacity_exhausted", "Retained Cargo history reached its complete startup bound; no new lease or execution was admitted. Inspect and settle history before an explicitly reviewed ledger reset.")
	}
	if _, e := tx.Exec(`INSERT INTO metadata(key,value) VALUES('cargo_schema','1') ON CONFLICT(key) DO NOTHING`); e != nil {
		return e
	}
	rec := cargoNewRecord(l, run)
	raw, e := json.Marshal(rec)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO cargo_workloads(lease,data) VALUES(?,?)`, l.ID, string(raw))
	return e
}
func (m *Manager) putCargo(l model.Lease, rec cargoRecord, eventKind string) error {
	projectCargo(&l, rec)
	raw, e := json.Marshal(rec)
	if e != nil || len(raw) > cargoRecordLimit {
		return model.Err("cargo_state_corrupt", "Cargo durable record exceeds bound")
	}
	leaseRaw, e := json.Marshal(l)
	if e != nil {
		return e
	}
	tx, e := m.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	result, e := tx.Exec(`UPDATE cargo_workloads SET data=? WHERE lease=?`, string(raw), l.ID)
	if e != nil {
		return e
	}
	changed, e := result.RowsAffected()
	if e != nil || changed != 1 {
		return model.Err("cargo_state_corrupt", "Cargo record disappeared")
	}
	if _, e = tx.Exec(`UPDATE leases SET data=? WHERE id=?`, string(leaseRaw), l.ID); e != nil {
		return e
	}
	if e = event(tx, eventKind, l.ID, l); e != nil {
		return e
	}
	return tx.Commit()
}

// DB-only guard under Manager.mu. Native birth/namespace/provider checks belong
// in the admitted helper, never in the global admission critical section.
func (m *Manager) cargoStorageParent(r model.Request, expires *leaseclock.Deadline) (model.Lease, error) {
	parent, e := m.get(r.StorageLease)
	if e != nil {
		return parent, e
	}
	covered := m.leaseCovers(parent, r.TTL)
	if expires != nil {
		covered = m.leaseLive(parent) && parent.Clock != nil && expires.Boot == parent.Clock.Boot && expires.End <= parent.Clock.End
	}
	if parent.Request.Kind != "storage" || parent.Owner != m.owner || parent.Host != m.host || parent.State != "active" || parent.Expires == nil || !covered || parent.Artifact == nil || parent.Artifact.Phase != "owned" || parent.Artifact.Identity == nil || !parent.Artifact.Identity.BirthKnown || parent.Artifact.Identity.Inode == 0 || !filepath.IsAbs(parent.ManagedDir) {
		return parent, model.Err("storage_unavailable", "managed Cargo storage must be active/owned and outlast the command's TTL")
	}
	if m.storage == nil || m.storageErr != nil {
		return parent, model.Err("storage_unavailable", "managed storage provider is unavailable")
	}
	if export, e := m.storageExportBusy(parent.ID); e != nil {
		return parent, e
	} else if export != "" {
		return parent, model.Err("operation_busy", "managed storage has an unresolved export")
	}
	return parent, nil
}
func (m *Manager) validateCargoRequest(r model.Request, run *RunRequest) error {
	if r.Profile != "rust-build" {
		return nil
	}
	if run == nil || run.SSH != nil || run.sshRuntime != nil || r.Peer != "" || len(r.Ports) > 0 || r.PortLease != "" || r.ServiceLease != "" {
		return model.Err("profile_conflict", "managed rust-build requires a local supervised Cargo command without ports, SSH, remote peer or service attachment")
	}
	if r.StorageLease == "" {
		return model.Err("storage_required", "rust-build needs managed storage; create a short-lived storage lease then pass --storage ID")
	}
	if _, e := cargoprofile.Parse(run.Command, r.Resources.CPU); e != nil {
		return model.Err("profile_conflict", e.Error())
	}
	if m.cargoHelper == nil {
		return model.Err("cargo_helper_unavailable", "startup-verified Cargo helper is unavailable; restart a supported ARM backend")
	}
	_, e := m.cargoStorageParent(r, nil)
	return e
}
func cargoFailure(code, message string) error {
	return model.Err(code, fmt.Sprintf("Cargo: %s", message))
}

func (m *Manager) storageParentForProfile(r model.Request, expires *leaseclock.Deadline) (model.Lease, error) {
	if r.Profile == "rust-build" {
		return m.cargoStorageParent(r, expires)
	}
	return m.storageForRun(r)
}

func cargoHex(value string, size int) bool {
	return len(value) == size && strings.Trim(value, "0123456789abcdef") == ""
}
