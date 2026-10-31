package residentservice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"getarm.org/internal/mesh"
	"golang.org/x/sys/unix"
)

// This owner is reachable only through the offline native operator command.
// It creates a durable operator grant, never a Parent/Child/ManagerCharge or
// live-process capability. The command adapter must enforce exact --expect and
// explicit operator confirmation; ordinary start cannot call apply implicitly.
type OperatorPending struct {
	self                                          *OperatorPending
	owner                                         *owner
	profilePath, evidencePath, key, reason        string
	planTried, applyTried, closeTried             bool
	planComplete, applyComplete, referencesClosed bool
	plan                                          *OperatorPlan
	profile                                       *MeasuredProfile
	evidence                                      *EvidenceBundle
	reads                                         []*operatorRead
	inspectTried, inspectionComplete              bool
	authorityMutationAttempted, namespaceSynced   bool
	existingGrantRead                             bool
	imageCloseTried                               bool
	imageCloseError                               error
}
type OperatorPlan struct {
	Schema              uint32                    `json:"schema"`
	SHA256              string                    `json:"sha256"`
	Intent              OperatorIntent            `json:"intent"`
	ExistingGrantID     string                    `json:"existing_grant_id,omitempty"`
	Fixture             string                    `json:"fixture"`
	FixtureSourceSHA256 string                    `json:"fixture_source_sha256"`
	Coverage            []MeasurementCoverage     `json:"coverage"`
	CostScopes          []MeasurementCostScope    `json:"cost_scopes"`
	ResourceUnits       map[string]string         `json:"resource_units"`
	Artifacts           []OperatorArtifactSummary `json:"artifacts"`
	EvidenceUnknowns    []string                  `json:"evidence_unknowns"`
	Limits              []string                  `json:"limits"`
}
type OperatorArtifactSummary struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type OperatorReceipt struct {
	GrantID    string `json:"grant_id"`
	Revision   uint64 `json:"revision"`
	PlanSHA256 string `json:"plan_sha256"`
	Existing   bool   `json:"existing"`
	// A receipt is DATA, never service/native authority or release evidence.
}
type operatorRead struct {
	path          string
	data          []byte
	before, after os.FileInfo
	read, calls   int
	firstError    error
	finished      bool
}
type OperatorInspection struct {
	Original                                                        Inspection
	PlanAttempted, PlanCompleted, ApplyAttempted, ApplyCompleted    bool
	ReferenceCloseAttempted, ReferencesClosed                       bool // process-local references only
	AuthorityMutationAttempted, GrantCommitKnown, ExistingGrantRead bool
	InspectionAttempted, InspectionCompleted                        bool
	KnownGrantID                                                    string
}

func NewOperatorPending(home, profilePath, evidencePath, key, reason string) (*OperatorPending, error) {
	if !validInputPath(home) || !validInputPath(profilePath) || !validInputPath(evidencePath) || profilePath == evidencePath || !boundedText(key, 128) || !boundedText(reason, 1024) {
		return nil, ErrInvalid
	}
	p := &OperatorPending{profilePath: profilePath, evidencePath: evidencePath, key: key, reason: reason}
	p.self = p
	o := &owner{operator: p, path: home}
	p.owner = o
	o.files = &nativeFiles{owner: o}
	return p, nil // pure allocation before native, input or runtime-image effects
}
func (p *OperatorPending) begin(kind string) (*operation, error) {
	if p == nil || p.self != p || p.owner == nil || p.owner.operator != p {
		return nil, ErrInvalid
	}
	o := p.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil {
		return nil, ErrPending
	}
	if o.firstError != nil {
		return nil, o.firstError
	}
	switch kind {
	case "operator_plan":
		if p.planTried || p.inspectTried {
			return nil, ErrInvalid
		}
		p.planTried = true
	case "operator_inspect":
		if p.planTried || p.applyTried || p.inspectTried || p.closeTried {
			return nil, ErrInvalid
		}
		p.inspectTried = true
	case "operator_apply":
		if !p.planComplete || p.applyTried || p.closeTried {
			return nil, ErrInvalid
		}
		p.applyTried = true
	case "operator_reference_close":
		if (!p.planComplete && !p.inspectionComplete) || p.applyTried && !p.applyComplete || p.closeTried {
			return nil, ErrInvalid
		}
		p.closeTried = true
	default:
		return nil, ErrInvalid
	}
	op := &operation{owner: o, kind: kind, phase: "registered_before_effect"}
	o.active = op
	return op, nil
}

func (p *OperatorPending) PlanOriginal(ctx context.Context) (plan OperatorPlan, resultErr error) {
	op, err := p.begin("operator_plan")
	if err != nil {
		return plan, err
	}
	normal := false
	defer p.owner.finish(op, &normal, &resultErr)
	resultErr = p.planBody(ctx)
	if resultErr == nil {
		// Return a separate deep DATA copy, never mutable aliases into the owner.
		data, err := json.Marshal(p.plan)
		if err != nil || len(data) > MaxPayloadBytes {
			resultErr = ErrInvalid
		} else {
			resultErr = json.Unmarshal(data, &plan)
		}
	}
	if resultErr == nil {
		p.owner.mu.Lock()
		p.planComplete = true
		p.owner.mu.Unlock()
	}
	normal = true
	return plan, resultErr
}
func (p *OperatorPending) planBody(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	o := p.owner
	if err := o.files.openHome(o.path); err != nil {
		return err
	}
	// Capture/opaque runtime work is retained by this original pending owner.
	// Its hidden internals are not represented as exhaustively enumerated FDs.
	o.files.progress("original_operator_runtime_image_pending")
	image, err := mesh.CaptureRuntimeExecutable()
	o.mu.Lock()
	o.image = image
	o.mu.Unlock()
	if err != nil {
		return err
	}
	proof, err := image.Proof()
	if err != nil {
		return err
	}
	o.build = Build{SHA256: proof.SHA256, Size: proof.Size, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoBuild: runtime.Version(), Protocol: 1}
	profileInput, profileRaw, err := p.readInput(ctx, p.profilePath, MaxProfileBytes)
	if err != nil {
		return err
	}
	evidenceInput, evidenceRaw, err := p.readInput(ctx, p.evidencePath, MaxEvidenceBytes)
	if err != nil {
		return err
	}
	profile := &MeasuredProfile{}
	p.profile = profile // retained before decoding into it
	if err = decodeOperatorDocument(profileRaw, MaxProfileBytes, profile); err != nil {
		return err
	}
	if err = profile.Validate(); err != nil || profile.Build != o.build || profile.EvidenceSHA256 != evidenceInput.SHA256 {
		return ErrInvalid
	}
	evidence := &EvidenceBundle{}
	p.evidence = evidence
	if err = decodeOperatorDocument(evidenceRaw, MaxEvidenceBytes, evidence); err != nil {
		return err
	}
	if err = evidence.Validate(*profile); err != nil {
		return err
	}
	intent := OperatorIntent{Key: p.key, Home: o.path, HomeID: o.files.home.id, ExpectedRevision: 0, Profile: cloneMeasuredProfile(*profile), Reason: p.reason, Inputs: OperatorInputs{Profile: profileInput, Evidence: evidenceInput}}
	planSHA, err := intent.SHA256()
	if err != nil {
		return err
	}
	candidate := &OperatorPlan{Schema: 1, SHA256: planSHA, Intent: intent, Fixture: evidence.Fixture, FixtureSourceSHA256: evidence.FixtureSourceSHA256, Coverage: evidence.Coverage, CostScopes: evidence.CostScopes, ResourceUnits: map[string]string{"cpu": "logical-core-equivalents", "memory": "bytes", "disk": "bytes"}, EvidenceUnknowns: evidence.Unknowns, Limits: append([]string(nil), standingMeasurementLimits[:]...)}
	for _, a := range evidence.Artifacts {
		candidate.Artifacts = append(candidate.Artifacts, OperatorArtifactSummary{a.Name, a.Kind, a.SHA256, a.Size})
	}
	p.plan = candidate
	grant, err := p.inspectGrant(ctx)
	if err != nil {
		return err
	}
	if grant != nil {
		if grant.PlanSHA != planSHA {
			return ErrUnresolved
		}
		candidate.ExistingGrantID = grant.ID
	}
	// Refuse an unencodable authorization BEFORE creating any authority object.
	initial := Record{Schema: 1, Sequence: 1, Kind: "operator-authorized", Grant: grantForPlan(candidate)}
	if _, err = encodeFrame(initial, o.journal.last); err != nil {
		return err
	}
	data, err := json.Marshal(candidate)
	if err != nil || len(data) > MaxPayloadBytes {
		return ErrInvalid
	}
	return p.validateHome()
}

func (p *OperatorPending) readInput(ctx context.Context, path string, limit int) (input OperatorFileInput, data []byte, resultErr error) {
	n := p.owner.files
	if len(p.reads) >= 4 {
		return input, nil, ErrInvalid
	}
	call := &operatorRead{path: path}
	p.reads = append(p.reads, call) // actual read/buffer holder before native effects
	fail := func(err error) (OperatorFileInput, []byte, error) {
		call.firstError = err
		return OperatorFileInput{}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	directory, err := n.walkHome(filepath.Dir(path))
	if err != nil {
		return fail(err)
	}
	file, err := n.openPrivateAt(directory, filepath.Base(path), unix.O_RDONLY)
	if err != nil {
		return fail(err)
	}
	n.progress("original_operator_input_stat_pending")
	call.before, err = file.file.Stat()
	if err != nil {
		return fail(err)
	}
	if call.before.Size() <= 0 || call.before.Size() > int64(limit) {
		return fail(ErrInvalid)
	}
	call.data = make([]byte, int(call.before.Size())) // retained before ReadAt
	for call.read < len(call.data) {
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		if call.calls >= 32 {
			return fail(ErrInvalid)
		}
		call.calls++
		n.progress("original_operator_input_read_pending")
		count, readErr := file.file.ReadAt(call.data[call.read:], int64(call.read))
		call.read += count
		if readErr != nil && !(errors.Is(readErr, io.EOF) && call.read == len(call.data)) {
			return fail(readErr)
		}
		if count == 0 {
			return fail(io.ErrNoProgress)
		}
	}
	call.after, err = file.file.Stat()
	if err != nil {
		return fail(err)
	}
	if !os.SameFile(call.before, call.after) || call.before.Size() != call.after.Size() || !call.before.ModTime().Equal(call.after.ModTime()) {
		return fail(ErrInvalid)
	}
	if err = privateNative(file, false); err != nil {
		return fail(err)
	}
	// Validate the current canonical name using independently retained auxiliaries.
	current, err := n.walkHome(filepath.Dir(path))
	if err != nil {
		return fail(err)
	}
	if current.id != directory.id {
		return fail(ErrInvalid)
	}
	var named unix.Stat_t
	n.progress("original_operator_input_name_check_pending")
	if err = unix.Fstatat(int(current.file.Fd()), filepath.Base(path), &named, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fail(err)
	}
	if nativeID(&named) != file.id || uint32(named.Mode) != uint32(unix.S_IFREG)|0600 || named.Nlink != 1 || named.Size != call.after.Size() {
		return fail(ErrInvalid)
	}
	input = OperatorFileInput{Path: path, ID: file.id, Size: int64(len(call.data)), SHA256: digest(call.data)}
	if err = n.closeAuxiliary(current); err != nil {
		return fail(err)
	}
	if err = n.closeAuxiliary(file); err != nil {
		return fail(err)
	}
	if err = n.closeAuxiliary(directory); err != nil {
		return fail(err)
	}
	call.finished = true
	return input, call.data, nil
}
func (p *OperatorPending) validateHome() error {
	n := p.owner.files
	if err := privateNative(n.home, true); err != nil {
		return err
	}
	current, err := n.walkHome(p.owner.path)
	if err != nil {
		return err
	}
	if current.id != n.home.id {
		return ErrInvalid
	}
	return n.closeAuxiliary(current)
}
func (p *OperatorPending) privateEntry(name string) (bool, error) {
	n := p.owner.files
	var stat unix.Stat_t
	n.progress("original_operator_authority_entry_check_pending")
	err := unix.Fstatat(int(n.home.file.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if uint32(stat.Mode) != uint32(unix.S_IFREG)|0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return false, ErrInvalid
	}
	return true, nil
}
func (p *OperatorPending) inspectGrant(ctx context.Context) (*GrantRecord, error) {
	o, n := p.owner, p.owner.files
	lockExists, err := p.privateEntry(lockName)
	if err != nil {
		return nil, err
	}
	journalExists, err := p.privateEntry(journalName)
	if err != nil {
		return nil, err
	}
	if !lockExists && !journalExists {
		return nil, nil
	}
	if !lockExists || !journalExists {
		return nil, ErrUnresolved
	}
	n.journal, err = n.openPrivateAt(n.home, journalName, unix.O_RDONLY)
	if err != nil {
		return nil, err
	}
	journal, err := o.readOriginalJournal(ctx)
	if err != nil {
		return nil, err
	}
	if len(journal.records) != 1 {
		return nil, ErrUnresolved
	}
	grant := journal.records[0].Grant
	if err = n.closeAuxiliary(n.journal); err != nil {
		return nil, err
	}
	n.journal = nil
	return grant, nil
}
func grantForPlan(plan *OperatorPlan) *GrantRecord {
	intent := plan.Intent
	intent.Profile = cloneMeasuredProfile(intent.Profile)
	return &GrantRecord{ID: digest([]byte("resident-operator-grant-v1:" + plan.SHA256)), Revision: 1, PlanSHA: plan.SHA256, Intent: intent}
}

func (p *OperatorPending) ApplyOriginal(ctx context.Context, expect string) (receipt OperatorReceipt, resultErr error) {
	if p == nil || p.self != p || p.owner == nil || p.owner.operator != p || !validDigest(expect) {
		return receipt, ErrInvalid
	}
	// A fresh offline apply process recomputes a real readonly plan before mutation.
	p.owner.mu.Lock()
	planned := p.planComplete
	p.owner.mu.Unlock()
	if !planned {
		if _, err := p.PlanOriginal(ctx); err != nil {
			return receipt, err
		}
	}
	op, err := p.begin("operator_apply")
	if err != nil {
		return receipt, err
	}
	normal := false
	defer p.owner.finish(op, &normal, &resultErr)
	receipt, resultErr = p.applyBody(ctx, expect)
	if resultErr == nil {
		p.owner.mu.Lock()
		p.applyComplete = true
		p.owner.mu.Unlock()
	}
	normal = true
	return receipt, resultErr
}
func (p *OperatorPending) applyBody(ctx context.Context, expect string) (OperatorReceipt, error) {
	var receipt OperatorReceipt
	if ctx == nil || p.plan == nil || expect != p.plan.SHA256 {
		return receipt, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	o, n := p.owner, p.owner.files
	if err := p.validateHome(); err != nil {
		return receipt, err
	}
	if _, err := o.image.Proof(); err != nil {
		return receipt, err
	}
	profile, _, err := p.readInput(ctx, p.profilePath, MaxProfileBytes)
	if err != nil {
		return receipt, err
	}
	evidence, _, err := p.readInput(ctx, p.evidencePath, MaxEvidenceBytes)
	if err != nil {
		return receipt, err
	}
	if (OperatorInputs{Profile: profile, Evidence: evidence}) != p.plan.Intent.Inputs {
		return receipt, ErrInvalid
	}
	// Retain actual lock before original flock. Existing lock is never enough to
	// adopt a partial attempt; only a complete identical unused grant may replay.
	createdLock, err := p.createAuthorityFile(lockName)
	if errors.Is(err, unix.EEXIST) {
		n.lock, err = n.openPrivateAt(n.home, lockName, unix.O_RDWR)
	} else if err == nil {
		n.lock = createdLock
	}
	if err != nil {
		return receipt, err
	}
	n.progress("original_operator_authority_lock_pending")
	if err = unix.Flock(int(n.lock.file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return receipt, err
	}
	journalExists, err := p.privateEntry(journalName)
	if err != nil {
		return receipt, err
	}
	if journalExists {
		n.journal, err = n.openPrivateAt(n.home, journalName, unix.O_RDWR)
		if err != nil {
			return receipt, err
		}
		journal, err := o.readOriginalJournal(ctx)
		if err != nil {
			return receipt, err
		}
		if createdLock != nil || len(journal.records) != 1 || journal.records[0].Grant.PlanSHA != expect {
			return receipt, ErrUnresolved
		}
		if err = n.validateCurrent(o.path); err != nil {
			return receipt, err
		}
		g := journal.records[0].Grant
		o.mu.Lock()
		p.existingGrantRead = true
		o.mu.Unlock()
		return OperatorReceipt{GrantID: g.ID, Revision: g.Revision, PlanSHA256: g.PlanSHA, Existing: true}, nil
	}
	if createdLock == nil || p.plan.ExistingGrantID != "" {
		return receipt, ErrUnresolved
	}
	n.journal, err = p.createAuthorityFile(journalName)
	if err != nil {
		return receipt, err
	}
	if err = n.validateCurrent(o.path); err != nil {
		return receipt, err
	}
	// Persist the empty namespace FIRST. No valid grant frame is written if
	// directory persistence fails. Partial/unknown objects are never repaired.
	n.progress("original_operator_authority_namespace_sync_pending")
	if err = n.lock.file.Sync(); err != nil {
		return receipt, err
	}
	if err = n.journal.file.Sync(); err != nil {
		return receipt, err
	}
	if err = n.home.file.Sync(); err != nil {
		return receipt, err
	}
	o.mu.Lock()
	p.namespaceSynced = true
	o.mu.Unlock()
	g := grantForPlan(p.plan)
	if err = o.appendOriginal(ctx, Record{Schema: 1, Sequence: 1, Kind: "operator-authorized", Grant: g}); err != nil {
		return receipt, err
	}
	if err = n.validateCurrent(o.path); err != nil {
		return receipt, err
	}
	return OperatorReceipt{GrantID: g.ID, Revision: g.Revision, PlanSHA256: g.PlanSHA}, nil
}
func (p *OperatorPending) createAuthorityFile(name string) (*retainedFD, error) {
	n := p.owner.files
	if err := n.capacity(1); err != nil {
		return nil, err
	}
	p.owner.mu.Lock()
	p.authorityMutationAttempted = true
	p.owner.mu.Unlock()
	n.progress("original_operator_authority_exclusive_create_pending")
	fd, err := unix.Openat(int(n.home.file.Fd()), name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	file, err := n.retain(fd, name) // raw retained before wrapping/stat/chmod
	if err != nil {
		return file, err
	}
	n.progress("original_operator_created_file_mode_pending")
	if err = unix.Fchmod(fd, 0600); err != nil {
		return file, err
	}
	return file, privateNative(file, false)
}

// CloseCompletedReferences closes only this completed offline command's own
// actual references. It never deletes files, cancels a debit, credits resources
// or attests native/root/service Closed. Errors/partial operations retain their
// exact owner and cannot use this as a finite-abort path.
func (p *OperatorPending) CloseCompletedReferences() (resultErr error) {
	op, err := p.begin("operator_reference_close")
	if err != nil {
		return err
	}
	normal := false
	defer p.owner.finish(op, &normal, &resultErr)
	o, n := p.owner, p.owner.files
	for _, original := range n.slots {
		if original != nil {
			if resultErr = n.closeAuxiliary(original); resultErr != nil {
				normal = true
				return resultErr
			}
		}
	}
	o.mu.Lock()
	p.imageCloseTried = true
	o.mu.Unlock()
	n.progress("original_operator_runtime_reference_close_pending")
	if o.image != nil {
		resultErr = o.image.Close()
	}
	o.mu.Lock()
	p.imageCloseError = resultErr
	p.referencesClosed = resultErr == nil
	o.mu.Unlock()
	normal = true
	return resultErr
}
func (p *OperatorPending) InspectOriginal() OperatorInspection {
	if p == nil || p.self != p || p.owner == nil || p.owner.operator != p {
		return OperatorInspection{Original: Inspection{FirstError: ErrInvalid, NativeOutcome: "unknown"}}
	}
	seen := OperatorInspection{Original: p.owner.inspectOriginal()}
	p.owner.mu.Lock()
	seen.PlanAttempted, seen.PlanCompleted = p.planTried, p.planComplete
	seen.ApplyAttempted, seen.ApplyCompleted = p.applyTried, p.applyComplete
	seen.ReferenceCloseAttempted, seen.ReferencesClosed = p.closeTried, p.referencesClosed
	seen.AuthorityMutationAttempted = p.authorityMutationAttempted
	seen.ExistingGrantRead = p.existingGrantRead
	seen.InspectionAttempted, seen.InspectionCompleted = p.inspectTried, p.inspectionComplete
	seen.GrantCommitKnown = p.namespaceSynced && p.owner.append != nil && p.owner.append.committed
	if seen.GrantCommitKnown && len(p.owner.journal.records) > 0 && p.owner.journal.records[0].Grant != nil {
		seen.KnownGrantID = p.owner.journal.records[0].Grant.ID
	}
	p.owner.mu.Unlock()
	return seen
}
