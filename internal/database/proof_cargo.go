package database

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"getarm.org/internal/cargoprofile"
	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

const cargoResetSchema = `CREATE TABLE cargo_workloads (lease TEXT PRIMARY KEY,data TEXT NOT NULL)`

// Independent private-record mirror. Unknown producer fields remain refused.
type proofCargoRecord struct {
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

func cargoProofDigest(value any) string {
	raw, _ := json.Marshal(value)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (p *proofReader) readCargo() error {
	if e := checkCargoProofTypes(); e != nil {
		return e
	}
	_, exists := p.catalog["cargo_workloads"]
	if exists != p.cargoSchema {
		return fmt.Errorf("Cargo optional table/schema marker do not match")
	}
	seen := map[string]bool{}
	if e := p.walk("cargo_workloads", func(row proofRow) error {
		raw, e := row.text("data")
		if e != nil || len(raw) > 256<<10 {
			return fmt.Errorf("Cargo record exceeds known persistence bound")
		}
		if e = cargoNoOptionalNull(json.RawMessage(raw), reflect.TypeOf(proofCargoRecord{})); e != nil {
			return e
		}
		rec, e := rowJSON[proofCargoRecord](row, "data", "births")
		if e != nil {
			return e
		}
		id, e := row.text("lease")
		if e != nil {
			return e
		}
		lease, ok := p.leases[id]
		store, hasStore := p.leases[rec.Storage]
		run, hasRun := p.cargoRuns[id]
		if !ok || !hasStore || !hasRun || rec.Schema != 1 || rec.Lease != id || !sshProofHex(id, 16) || !sshProofHex(rec.Nonce, 32) || !sshProofHex(rec.RunSHA256, 32) || !sshProofHex(rec.CommandSHA256, 32) || rec.Host != p.result.Host || rec.Owner != p.result.Owner || rec.Revision < 1 || rec.Created.IsZero() || !rec.Created.Equal(lease.Created) || lease.Cargo == nil || lease.Request.Profile != "rust-build" || lease.Request.StorageLease != store.ID || store.Request.Kind != "storage" || store.Artifact == nil || store.Artifact.Identity == nil || run.SSH != nil || run.Request.Peer != "" || run.Request.ServiceLease != "" || run.Request.PortLease != "" || len(run.Request.Ports) > 0 || lease.Workload != nil || !reflect.DeepEqual(run.Request, lease.Request) {
			return fmt.Errorf("Cargo admission/lease/run/storage identity differs")
		}
		if cargoProofDigest(run) != rec.RunSHA256 || cargoProofDigest(struct {
			Command []string `json:"command"`
			CWD     string   `json:"cwd"`
		}{run.Command, run.CWD}) != rec.CommandSHA256 {
			return fmt.Errorf("Cargo original request/command digest differs")
		}
		command, e := cargoprofile.Parse(run.Command, lease.Request.Resources.CPU)
		if e != nil {
			return fmt.Errorf("Cargo original command is unsupported")
		}
		if !cargoProofBinding(lease, rec) {
			return fmt.Errorf("Cargo public binding differs from durable evidence")
		}
		seen[id] = true
		if rec.Worker != "" && !sshProofHex(rec.Worker, 16) {
			return fmt.Errorf("Cargo worker identity malformed")
		}
		if !oneOf(rec.Phase, "admitted", "spawn_intent", "helper_bound", "creating", "resolving", "prepared", "compilation_authorized", "compiling", "finished", "needs_attention") {
			return fmt.Errorf("unknown Cargo lifecycle phase")
		}
		if rec.Runtime != nil && (!sshProofHex(rec.Runtime.SHA256, 32) || rec.Runtime.Size < 1 || rec.Runtime.Version == "" || rec.Runtime.Protocol != cargoprofile.HelperProtocol) {
			return fmt.Errorf("Cargo helper startup runtime evidence invalid")
		}
		if rec.Process != nil {
			v := rec.Process
			if rec.Runtime == nil || v.PID <= 0 || v.PGID != v.PID || v.UID != p.result.Owner || v.Start == "" || v.Namespace == "" || lease.Process == nil || *lease.Process != *v || lease.PID != v.PID {
				return fmt.Errorf("Cargo helper native process binding differs")
			}
		}
		if e = p.validateCargoBirths(rec, store); e != nil {
			return e
		}
		if rec.Prepared != nil {
			if e = validateCargoPrepared(rec, command, *store.Artifact.Identity); e != nil {
				return e
			}
		}
		if rec.Started != nil && (rec.Process == nil || rec.AuthorizedAt == nil || rec.Started.At.IsZero() || rec.Started.At.Before(*rec.AuthorizedAt) || rec.Started.Process.PID <= 0 || rec.Started.Process.PID == rec.Process.PID || rec.Started.Process.PGID != rec.Process.PGID || rec.Started.Process.UID != p.result.Owner || rec.Started.Process.Start == "" || rec.Started.Process.Namespace != rec.Process.Namespace) {
			return fmt.Errorf("Cargo actual start lacks authorized current-group evidence")
		}
		if rec.AuthorizedAt != nil && (rec.Prepared == nil || rec.AuthorizedAt.IsZero() || rec.AuthorizedAt.Before(rec.Prepared.At)) {
			return fmt.Errorf("Cargo compile authorization lacks immutable preparation")
		}
		if rec.PreflightAt != nil && (rec.Process == nil || rec.PreflightAt.IsZero() || rec.PreflightAt.Before(rec.Created) || rec.Prepared != nil && rec.Prepared.At.Before(*rec.PreflightAt)) {
			return fmt.Errorf("Cargo preflight timing lacks native responsibility/preparation linkage")
		}
		if rec.PreflightAt == nil && !rec.NoPreflight && rec.Phase == "finished" {
			return fmt.Errorf("Cargo finished record lacks known preflight history")
		}
		if rec.Finished != nil {
			f := rec.Finished
			if f.Code < 0 || f.Code > 255 || f.At.IsZero() || f.At.Before(rec.Created) || rec.Started != nil && !f.CommandStarted || f.CommandStarted && rec.AuthorizedAt == nil || f.Code == 0 && (!f.CommandStarted || rec.Started == nil) {
				return fmt.Errorf("Cargo final receipt contradicts authorized/start evidence")
			}
		}
		if rec.Phase != "finished" || rec.Uncertain || rec.Worker != "" || !rec.GroupAbsent || lease.State != "terminal" {
			return p.block("cargo", id, "Cargo preparation/process responsibility or exact final outcome has not settled")
		}
		if rec.FinalAt == nil || rec.FinalAt.IsZero() || rec.FinalAt.Before(rec.Created) || rec.Started != nil && rec.FinalAt.Before(rec.Started.At) || rec.Finished != nil && rec.FinalAt.Before(rec.Finished.At) || lease.Ended == nil || !rec.FinalAt.Equal(*lease.Ended) {
			return fmt.Errorf("Cargo final group-absence/time evidence missing")
		}
		if rec.NoSpawn {
			if !rec.NoPreflight || rec.Process != nil || rec.PreflightAt != nil || len(rec.Births) != 0 || rec.PolicyIntent || rec.Policy != nil || rec.Prepared != nil || rec.AuthorizedAt != nil || rec.Started != nil || rec.Finished != nil {
				return fmt.Errorf("Cargo no-spawn record contains execution/mutation evidence")
			}
		} else if rec.Process == nil {
			return fmt.Errorf("Cargo settled spawn lacks verified native group")
		}
		if rec.NoPreflight && (rec.PreflightAt != nil || len(rec.Births) != 0 || rec.PolicyIntent || rec.Policy != nil || rec.Prepared != nil || rec.AuthorizedAt != nil || rec.Started != nil) {
			return fmt.Errorf("Cargo no-preflight record contains granted work")
		}
		if lease.Outcome == "completed" && (rec.Finished == nil || rec.Finished.Code != 0 || !rec.Finished.CommandStarted || rec.Started == nil) {
			return fmt.Errorf("Cargo success is not proved by actual compilation completion")
		}
		if rec.Finished == nil && !rec.NoPreflight && !oneOf(lease.Outcome, "released", "cancelled", "expired", "revoked", "rejected", "reconciled") {
			return fmt.Errorf("Cargo missing receipt must not become ordinary known completion")
		}
		return nil
	}); e != nil {
		return e
	}
	for id, l := range p.leases {
		if l.Cargo != nil && !seen[id] {
			return fmt.Errorf("Cargo lease lacks its complete optional ledger")
		}
	}
	return nil
}
func cargoProofBinding(l model.Lease, r proofCargoRecord) bool {
	b := l.Cargo
	if b == nil {
		return false
	}
	state := r.Phase
	if l.State == "queued" {
		state = "queued"
	}
	if b.Schema != 1 || b.Adapter != "rust-build" || b.State != state || b.CommandSHA256 != r.CommandSHA256 || b.PreparedSHA256 != r.PreparedSHA256 || b.Detail == "" || !reflect.DeepEqual(b.AuthorizedAt, r.AuthorizedAt) {
		return false
	}
	if !reflect.DeepEqual(b.PreparingAt, l.Started) {
		return false
	}
	var startedAt *time.Time
	if r.Started != nil {
		at := r.Started.At
		startedAt = &at
	}
	if !reflect.DeepEqual(b.StartedAt, startedAt) {
		return false
	}
	if r.Prepared == nil {
		return b.Jobs == 0 && b.Workspace == "" && b.Target == "" && b.Build == "" && b.Control == "" && b.CargoVersion == "" && b.RustcVersion == "" && b.RustdocVersion == "" && b.Toolchain == ""
	}
	q := r.Prepared
	return b.Jobs == q.Plan.Jobs && b.Workspace == q.Source.Workspace && b.Target == q.Owned.Target.Path && b.Build == q.Owned.Build.Path && b.Control == q.Owned.Control.Path && b.CargoVersion == q.Tools.CargoVersion && b.RustcVersion == q.Tools.RustcVersion && b.RustdocVersion == q.Tools.RustdocVersion && b.Toolchain == q.Tools.Toolchain
}
func (p *proofReader) validateCargoBirths(r proofCargoRecord, store model.Lease) error {
	if len(r.Births) > 8 {
		return fmt.Errorf("Cargo birth count exceeds private roots")
	}
	nativeRoot, e := filepath.EvalSymlinks(filepath.Join(p.home, "storage"))
	if e != nil {
		return fmt.Errorf("Cargo managed storage root cannot be resolved for history linkage")
	}
	root := filepath.Join(nativeRoot, store.ID, "cargo-"+r.Lease)
	for i, b := range r.Births {
		path := root
		if i/2 > 0 {
			path = filepath.Join(root, []string{"target", "build", "control"}[i/2-1])
		}
		if b.Step != i/2+1 || b.Path != path || (i%2 == 0) != (b.Born == nil) {
			return fmt.Errorf("Cargo birth evidence differs from exact owned intention")
		}
		if b.Born != nil && (b.Born.Inode == 0 || !b.Born.BirthKnown || b.Born.Owner != uint32Owner(p.result.Owner) || b.Born.Device != store.Artifact.Identity.Device) {
			return fmt.Errorf("Cargo native birth is outside owned parent filesystem/account")
		}
	}
	if r.Policy != nil && (!r.PolicyIntent || len(r.Births) != 8 || r.Policy.Path != filepath.Join(root, "control", "policy.toml") || !r.Policy.Present || r.Policy.Ignored || r.Policy.Inode == 0 || r.Policy.Owner != uint32Owner(p.result.Owner) || r.Policy.Mode != unix.S_IFREG|0600 || r.Policy.Size < 1 || r.Policy.Size > cargoprofile.MaxPolicyBytes || !sshProofHex(r.Policy.SHA256, 32)) {
		return fmt.Errorf("Cargo private policy birth/evidence differs")
	}
	return nil
}
func validateCargoPrepared(r proofCargoRecord, command cargoprofile.Command, parent model.FileIdentity) error {
	q := r.Prepared
	if q.Schema != 1 || len(r.Births) != 8 || r.Policy == nil || !reflect.DeepEqual(*r.Policy, q.Policy) || q.At.IsZero() || q.At.Before(r.Created) || !reflect.DeepEqual(q.Command, command) || q.Owned.AllocationIdentity != parent || q.Owned.Root.Path != r.Births[0].Path || q.Plan.Jobs != command.Jobs || q.Plan.Roots != q.Owned.Roots() {
		return fmt.Errorf("Cargo prepared command/policy/parent differs")
	}
	digest, e := cargoprofile.PreparedDigest(*q)
	if e != nil || digest != r.PreparedSHA256 {
		return fmt.Errorf("Cargo immutable prepared digest differs")
	}
	for i, d := range []cargoprofile.OwnedDirectory{q.Owned.Root, q.Owned.Target, q.Owned.Build, q.Owned.Control} {
		b := r.Births[2*i+1]
		if b.Born == nil || d.Path != b.Path || d.Identity != *b.Born || d.Name != filepath.Base(b.Path) {
			return fmt.Errorf("Cargo prepared roots differ from exact acknowledged births")
		}
	}
	if q.Owned.AllocationPath != filepath.Dir(q.Owned.Root.Path) || q.Plan.PolicySHA256 != q.Policy.SHA256 {
		return fmt.Errorf("Cargo prepared allocation/policy digest differs")
	}
	args := append(append([]string{}, command.Arguments...), "--jobs", strconv.Itoa(command.Jobs), "--target-dir", q.Owned.Target.Path, "--config", q.Policy.Path)
	metadata := append(command.MetadataArguments(), "--config", q.Policy.Path)
	if !reflect.DeepEqual(args, q.Plan.Arguments) || !reflect.DeepEqual(metadata, q.Plan.Metadata) {
		return fmt.Errorf("Cargo final argv lost authoritative jobs/output/private-last-policy")
	}
	if !filepath.IsAbs(q.Audit.CWD) || filepath.Clean(q.Audit.CWD) != q.Audit.CWD || !filepath.IsAbs(q.Audit.CargoHome) || q.Audit.CWDIdentity.Inode == 0 || q.Source.WorkspaceIdentity.Inode == 0 || len(q.Source.Files) < 2 || len(q.Source.Files) > cargoprofile.MaxSourceManifests+2 || len(q.Source.Members) < 1 || len(q.Source.Members) > cargoprofile.MaxSourceManifests {
		return fmt.Errorf("Cargo preparation config/source identity bounds invalid")
	}
	if e := validateCargoAudit(q.Audit); e != nil {
		return e
	}
	if q.Tools.Selector != command.Executable || q.Tools.Schema != 1 || !sshProofHex(q.Tools.Cargo.SHA256, 32) || len(q.Tools.BuiltinTargets) < 1 || len(q.Tools.BuiltinTargets) > 4096 || len(q.Tools.CheckedTargets) > 192 || q.Tools.CargoVersion == "" || q.Tools.CargoVersion != q.Tools.RustcVersion || q.Tools.CargoVersion != q.Tools.RustdocVersion || q.Tools.SysrootIdentity.Inode == 0 || !filepath.IsAbs(q.Tools.Sysroot) || filepath.Clean(q.Tools.Sysroot) != q.Tools.Sysroot || q.Tools.SysrootIdentity.Mode&unix.S_IFMT != unix.S_IFDIR || q.Tools.Host == "" || q.Tools.Detail == "" || !sort.StringsAreSorted(q.Tools.BuiltinTargets) {
		return fmt.Errorf("Cargo toolchain evidence is unsupported")
	}
	seen := map[string]bool{}
	for _, target := range q.Tools.BuiltinTargets {
		if target == "" || seen[target] {
			return fmt.Errorf("Cargo builtin-target evidence is ambiguous")
		}
		seen[target] = true
	}
	for _, target := range q.Tools.CheckedTargets {
		if !seen[target.Selector] || target.Origin == "" {
			return fmt.Errorf("Cargo target lacks concrete compiler builtin evidence")
		}
	}
	executables := []cargoprofile.ExecutableEvidence{q.Tools.Selected, q.Tools.Cargo, q.Tools.Rustc, q.Tools.Rustdoc}
	if q.Tools.Rustup != nil {
		executables = append(executables, *q.Tools.Rustup)
	}
	for _, f := range executables {
		if !filepath.IsAbs(f.Path) || filepath.Clean(f.Path) != f.Path || f.Inode == 0 || f.Mode&unix.S_IFMT != unix.S_IFREG || f.Mode&07022 != 0 || f.Mode&0111 == 0 || f.Size < 1 || f.Size > cargoprofile.MaxToolBytes || !sshProofHex(f.SHA256, 32) || f.Modified.IsZero() || f.Owner != 0 && f.Owner != parent.Owner {
			return fmt.Errorf("Cargo executable evidence is unsupported")
		}
	}
	if q.Tools.Rustup != nil {
		f := q.Tools.Rustup
		if q.Tools.Selection != "installed-rustup-query" || q.Tools.Toolchain == "" || f.Device != q.Tools.Selected.Device || f.Inode != q.Tools.Selected.Inode || f.SHA256 != q.Tools.Selected.SHA256 {
			return fmt.Errorf("Cargo Rustup selection evidence differs")
		}
	} else if q.Tools.Selection != "concrete-installed-siblings" || q.Tools.Selected.Device != q.Tools.Cargo.Device || q.Tools.Selected.Inode != q.Tools.Cargo.Inode || q.Tools.Toolchain != "" {
		return fmt.Errorf("Cargo concrete installed selection differs")
	}
	expectedTargets, e := q.Audit.EffectiveTargets()
	if e != nil {
		return fmt.Errorf("Cargo configured target precedence evidence invalid")
	}
	explicit, e := command.ExplicitTargets()
	if e != nil {
		return e
	}
	expectedTargets = append(expectedTargets, explicit...)

	if !reflect.DeepEqual(expectedTargets, q.Tools.CheckedTargets) && !(len(expectedTargets) == 0 && len(q.Tools.CheckedTargets) == 0) {
		return fmt.Errorf("Cargo configured/explicit targets were not all verified")
	}
	if !filepath.IsAbs(q.Source.Workspace) || filepath.Clean(q.Source.Workspace) != q.Source.Workspace {
		return fmt.Errorf("Cargo source workspace is noncanonical")
	}
	sources := map[string]cargoprofile.FileEvidence{}
	var sourceBytes int64
	for _, f := range q.Source.Files {
		rel, e := filepath.Rel(q.Source.Workspace, f.Path)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !filepath.IsAbs(f.Path) || filepath.Clean(f.Path) != f.Path || sources[f.Path].Path != "" || f.Ignored || f.Size < 0 {
			return fmt.Errorf("Cargo source file evidence is outside its workspace/ambiguous")
		}
		if f.Present {
			if f.Inode == 0 || f.Mode&unix.S_IFMT != unix.S_IFREG || !sshProofHex(f.SHA256, 32) || f.Size > cargoprofile.MaxConfigBytes {
				return fmt.Errorf("Cargo source file identity/digest invalid")
			}
		} else if filepath.Base(f.Path) != "Cargo.lock" || f.Inode != 0 || f.SHA256 != "" || f.Mode != 0 || f.Size != 0 || f.Owner != 0 || f.Device != 0 {
			return fmt.Errorf("Cargo missing source evidence is not an absent lock")
		}
		sourceBytes += f.Size
		sources[f.Path] = f
	}
	if sourceBytes > cargoprofile.MaxSourceBytes || !sources[filepath.Join(q.Source.Workspace, "Cargo.toml")].Present || sources[filepath.Join(q.Source.Workspace, "Cargo.lock")].Path == "" {
		return fmt.Errorf("Cargo root manifest/lock evidence missing/bounded out")
	}
	members := map[string]bool{}
	for _, path := range q.Source.Members {
		if members[path] || filepath.Base(path) != "Cargo.toml" || !sources[path].Present {
			return fmt.Errorf("Cargo source member has no exact manifest evidence")
		}
		members[path] = true
	}

	expectedFiles := map[string]bool{filepath.Join(q.Source.Workspace, "Cargo.toml"): true, filepath.Join(q.Source.Workspace, "Cargo.lock"): true}
	for path := range members {
		expectedFiles[path] = true
	}
	if len(expectedFiles) != len(sources) || !sort.StringsAreSorted(q.Source.Members) {
		return fmt.Errorf("Cargo manifest/lock roster differs from exact sorted source coverage")
	}
	for path := range sources {
		if !expectedFiles[path] {
			return fmt.Errorf("Cargo unexpected source evidence")
		}
	}

	return nil
}

func validateCargoAudit(a cargoprofile.Audit) error {
	dirs := []string{a.CargoHome}
	var ancestors []string
	for path := a.CWD; ; path = filepath.Dir(path) {
		ancestors = append(ancestors, filepath.Join(path, ".cargo"))
		if len(ancestors) > 128 {
			return fmt.Errorf("Cargo config ancestor bound exceeded")
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		dirs = append(dirs, ancestors[i])
	}
	expected := []string{}
	seen := map[string]bool{}
	for _, dir := range dirs {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		expected = append(expected, filepath.Join(dir, "config"), filepath.Join(dir, "config.toml"))
	}
	if len(a.Files) != len(expected) || len(a.Files) > 260 || len(a.Settings) > 4096 {
		return fmt.Errorf("Cargo config audit coverage exceeds or differs from physical precedence")
	}
	files := map[string]cargoprofile.FileEvidence{}
	var total int64
	for i, f := range a.Files {
		if f.Path != expected[i] || f.Size < 0 || f.Size > cargoprofile.MaxConfigBytes {
			return fmt.Errorf("Cargo config file coverage/size differs")
		}
		if f.Present {
			if f.Inode == 0 || f.Mode&unix.S_IFMT != unix.S_IFREG || !sshProofHex(f.SHA256, 32) {
				return fmt.Errorf("Cargo config native evidence invalid")
			}
		} else if f.Ignored || f.Device != 0 || f.Inode != 0 || f.Owner != 0 || f.Mode != 0 || f.Size != 0 || f.SHA256 != "" {
			return fmt.Errorf("Cargo absent config contains file evidence")
		}
		if i%2 == 0 && f.Ignored || i%2 == 1 && f.Ignored != (f.Present && a.Files[i-1].Present) {
			return fmt.Errorf("Cargo legacy config precedence differs")
		}
		total += f.Size
		files[f.Path] = f
	}
	if total > cargoprofile.MaxTotalConfigBytes {
		return fmt.Errorf("Cargo total config evidence exceeds bound")
	}
	for _, v := range a.Settings {
		f := files[v.Origin]
		if !f.Present || f.Ignored {
			return fmt.Errorf("Cargo regulated setting has no effective config origin")
		}
		switch v.Key {
		case "build.jobs":
			if v.Jobs == 0 || v.Jobs < -65536 || v.Jobs > 65536 || v.Path != "" || len(v.Targets) > 0 || v.TargetArray {
				return fmt.Errorf("Cargo jobs provenance invalid")
			}
		case "build.target-dir", "build.build-dir":
			if v.Jobs != 0 || !filepath.IsAbs(v.Path) || filepath.Clean(v.Path) != v.Path || len(v.Targets) > 0 || v.TargetArray {
				return fmt.Errorf("Cargo directory provenance invalid")
			}
		case "build.target":
			if v.Jobs != 0 || v.Path != "" {
				return fmt.Errorf("Cargo target provenance invalid")
			}
		default:
			if !strings.HasPrefix(v.Key, "env.") || len(v.Key) <= 4 || v.Jobs != 0 || v.Path != "" || len(v.Targets) > 0 || v.TargetArray {
				return fmt.Errorf("Cargo setting provenance unsupported")
			}
		}
	}
	return nil
}
