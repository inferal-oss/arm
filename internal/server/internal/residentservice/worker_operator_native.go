package residentservice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

// Offline worker authorization owns its actual reads, image and native files.
// It has no Start, inherited child, Manager, charge, birth or release operation.
type WorkerOperatorPending struct {
	self                                                             *WorkerOperatorPending
	custody                                                          *workerCustody
	profilePath, evidencePath, key, reason                           string
	planTried, planComplete, applyTried, applyComplete, inspectTried bool
	mutationAttempted, namespaceSynced                               bool
	plan                                                             *WorkerOperatorPlan
	profile                                                          *WorkerMeasuredProfile
	evidence                                                         *WorkerEvidenceBundle
	reads                                                            []*operatorRead // bounded DATA read holders, not old OperatorPending
}

type WorkerOperatorPlan struct {
	Schema          uint32                    `json:"schema"`
	SHA256          string                    `json:"sha256"`
	Intent          WorkerOperatorIntent      `json:"intent"`
	ExistingGrantID string                    `json:"existing_grant_id,omitempty"`
	Measurements    []WorkerMeasurement       `json:"measurements"`
	Coverage        []MeasurementCoverage     `json:"core_coverage"`
	CostScopes      []MeasurementCostScope    `json:"core_cost_scopes"`
	Artifacts       []OperatorArtifactSummary `json:"artifacts"`
	Unknowns        []string                  `json:"unknowns"`
	Limit           string                    `json:"limit"`
}

// A stored-record observation never reconstructs the writer's original sync
// outcome or issues native service authority, closure or resource credit.
type WorkerAuthorizationObservation struct {
	Schema             uint32 `json:"schema"`
	Key                string `json:"key"`
	State              string `json:"state"`
	PlanSHA256         string `json:"plan_sha256,omitempty"`
	GrantID            string `json:"grant_id,omitempty"`
	Revision           uint64 `json:"revision,omitempty"`
	RecordSequence     uint32 `json:"record_sequence,omitempty"`
	OriginalCommit     string `json:"original_commit"`
	Existing           bool   `json:"existing"`
	ReadOnly           bool   `json:"read_only"`
	ExactRecordMatched bool   `json:"exact_record_matched"`
}

func NewWorkerOperatorPending(home, profilePath, evidencePath, key, reason string) (*WorkerOperatorPending, error) {
	if !validInputPath(home) || !validInputPath(profilePath) || !validInputPath(evidencePath) || profilePath == evidencePath || !boundedText(key, 128) || !boundedText(reason, 1024) {
		return nil, ErrInvalid
	}
	p := &WorkerOperatorPending{custody: newWorkerCustodyData(home), profilePath: profilePath, evidencePath: evidencePath, key: key, reason: reason}
	p.self = p
	p.custody.operator = p
	return p, nil
}

func (p *WorkerOperatorPending) beginOriginal(kind string) (*operation, error) {
	if p == nil || p.self != p || p.custody == nil || p.custody.self != p.custody || p.custody.operator != p || p.custody.parent != nil || p.custody.raw == nil {
		return nil, ErrInvalid
	}
	w := p.custody
	r := w.raw
	r.mu.Lock()
	defer r.mu.Unlock()
	if !w.legacyEmptyLocked() {
		return nil, ErrInvalid
	}
	if r.active != nil {
		return nil, ErrPending
	}
	if r.firstError != nil {
		return nil, r.firstError
	}
	switch kind {
	case "worker-operator-plan":
		if p.planTried || p.inspectTried || p.applyTried {
			return nil, ErrInvalid
		}
		p.planTried = true
	case "worker-operator-apply":
		if !p.planComplete || p.applyTried || p.inspectTried {
			return nil, ErrInvalid
		}
		p.applyTried = true
	case "worker-operator-inspect":
		if p.planTried || p.applyTried || p.inspectTried {
			return nil, ErrInvalid
		}
		p.inspectTried = true
	default:
		return nil, ErrInvalid
	}
	return w.operationLocked(kind)
}

func (p *WorkerOperatorPending) PlanOriginal(ctx context.Context) (plan WorkerOperatorPlan, resultErr error) {
	op, err := p.beginOriginal("worker-operator-plan")
	if err != nil {
		return plan, err
	}
	normal := false
	defer p.custody.raw.finish(op, &normal, &resultErr)
	resultErr = p.planBodyOriginal(ctx)
	if resultErr == nil {
		raw, err := json.Marshal(p.plan)
		if err != nil || len(raw) > MaxProfileBytes {
			resultErr = ErrInvalid
		} else {
			resultErr = json.Unmarshal(raw, &plan)
		}
	}
	if resultErr == nil {
		p.custody.raw.mu.Lock()
		p.planComplete = true
		p.custody.raw.mu.Unlock()
	}
	normal = true
	return plan, resultErr
}

func (p *WorkerOperatorPending) planBodyOriginal(ctx context.Context) error {
	if err := workerContextData(ctx); err != nil {
		return err
	}
	w := p.custody
	n := w.raw.files
	if err := n.openHome(w.path); err != nil {
		return err
	}
	n.progress("worker_operator_runtime_image_capture_pending")
	image, err := w.captureRuntimeImageOriginal(ctx)
	if err != nil {
		return err
	}
	n.progress("worker_operator_runtime_image_proof_pending")
	proof, err := image.proofOriginal(ctx)
	if err != nil {
		return err
	}
	build := Build{SHA256: proof.SHA256, Size: proof.Size, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoBuild: runtime.Version(), Protocol: 1}
	profileInput, profileRaw, err := p.readInputOriginal(ctx, p.profilePath, MaxProfileBytes)
	if err != nil {
		return err
	}
	evidenceInput, evidenceRaw, err := p.readInputOriginal(ctx, p.evidencePath, MaxEvidenceBytes)
	if err != nil {
		return err
	}
	p.profile = &WorkerMeasuredProfile{}
	if decodeOperatorDocument(profileRaw, MaxProfileBytes, p.profile) != nil || p.profile.Validate() != nil || p.profile.Core.Build != build || p.profile.EvidenceSHA256 != evidenceInput.SHA256 {
		return ErrInvalid
	}
	p.evidence = &WorkerEvidenceBundle{}
	if decodeOperatorDocument(evidenceRaw, MaxEvidenceBytes, p.evidence) != nil || p.evidence.Validate(*p.profile) != nil {
		return ErrInvalid
	}
	intent := WorkerOperatorIntent{Key: p.key, Home: w.path, HomeID: n.home.id, ExpectedRevision: 0, Profile: *p.profile, Reason: p.reason, Inputs: OperatorInputs{Profile: profileInput, Evidence: evidenceInput}}
	planSHA, err := intent.SHA256()
	if err != nil {
		return err
	}
	p.plan = &WorkerOperatorPlan{Schema: 3, SHA256: planSHA, Intent: intent, Measurements: p.evidence.ContractMeasurements, Coverage: p.evidence.Core.Coverage, CostScopes: p.evidence.Core.CostScopes, Unknowns: p.evidence.Core.Unknowns, Limit: "Bounded recorded evidence is not measurement truth, native funding, birth or release authority."}
	for _, a := range p.evidence.Core.Artifacts {
		p.plan.Artifacts = append(p.plan.Artifacts, OperatorArtifactSummary{Name: a.Name, Kind: a.Kind, SHA256: a.SHA256, Size: a.Size})
	}
	grant, err := p.inspectUnusedGrantOriginal(ctx)
	if err != nil {
		return err
	}
	if grant != nil {
		if grant.PlanSHA != planSHA {
			return ErrUnresolved
		}
		p.plan.ExistingGrantID = grant.ID
	}
	// Even the initial grant's entire encoded envelope must fit before Apply.
	if _, err = encodeWorkerJournalData([]WorkerRecordData{{Schema: 3, Sequence: 1, Kind: "worker-operator-authorized", Grant: workerGrantForPlanData(p.plan)}}); err != nil {
		return err
	}
	encoded, err := json.Marshal(p.plan)
	if err != nil || len(encoded) > MaxProfileBytes {
		return ErrInvalid
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return p.validateHomeOriginal()
}

func workerGrantForPlanData(plan *WorkerOperatorPlan) *WorkerGrantRecord {
	return &WorkerGrantRecord{ID: digest([]byte("resident-worker-operator-grant-v1:" + plan.SHA256)), Revision: 1, PlanSHA: plan.SHA256, Intent: plan.Intent}
}

func (p *WorkerOperatorPending) readInputOriginal(ctx context.Context, path string, limit int) (input OperatorFileInput, data []byte, resultErr error) {
	if err := workerContextData(ctx); err != nil {
		return input, nil, err
	}
	w := p.custody
	n := w.raw.files
	if len(p.reads) >= 4 {
		return input, nil, ErrInvalid
	}
	call := &operatorRead{path: path}
	p.reads = append(p.reads, call)
	fail := func(err error) (OperatorFileInput, []byte, error) {
		call.firstError = err
		return OperatorFileInput{}, nil, err
	}
	directory, err := n.walkHome(filepath.Dir(path))
	if err != nil {
		return fail(err)
	}
	file, err := n.openPrivateAt(directory, filepath.Base(path), unix.O_RDONLY)
	if err != nil {
		return fail(err)
	}
	n.progress("worker_operator_input_stat_pending")
	call.before, err = file.file.Stat()
	if err != nil {
		return fail(err)
	}
	if call.before.Size() <= 0 || call.before.Size() > int64(limit) {
		return fail(ErrInvalid)
	}
	call.data = make([]byte, int(call.before.Size()))
	for call.read < len(call.data) {
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		if call.calls >= 32 {
			return fail(ErrInvalid)
		}
		call.calls++
		n.progress("worker_operator_input_read_pending")
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
	current, err := n.walkHome(filepath.Dir(path))
	if err != nil {
		return fail(err)
	}
	if current.id != directory.id {
		return fail(ErrInvalid)
	}
	var named unix.Stat_t
	n.progress("worker_operator_input_name_check_pending")
	if err = unix.Fstatat(int(current.file.Fd()), filepath.Base(path), &named, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fail(err)
	}
	if nativeID(&named) != file.id || uint32(named.Mode) != uint32(unix.S_IFREG)|0600 || named.Nlink != 1 || named.Size != call.after.Size() {
		return fail(ErrInvalid)
	}
	input = OperatorFileInput{Path: path, ID: file.id, Size: int64(len(call.data)), SHA256: digest(call.data)}
	for _, f := range []*retainedFD{current, file, directory} {
		if err = n.closeAuxiliary(f); err != nil {
			return fail(err)
		}
	}
	call.finished = true
	return input, call.data, nil
}

func (p *WorkerOperatorPending) validateHomeOriginal() error {
	n := p.custody.raw.files
	if err := privateNative(n.home, true); err != nil {
		return err
	}
	current, err := n.walkHome(p.custody.path)
	if err != nil {
		return err
	}
	if current.id != n.home.id {
		return ErrInvalid
	}
	return n.closeAuxiliary(current)
}

func (p *WorkerOperatorPending) privateEntryOriginal(name string) (bool, error) {
	n := p.custody.raw.files
	var s unix.Stat_t
	n.progress("worker_operator_authority_entry_check_pending")
	err := unix.Fstatat(int(n.home.file.Fd()), name, &s, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if uint32(s.Mode) != uint32(unix.S_IFREG)|0600 || s.Uid != uint32(os.Geteuid()) || s.Nlink != 1 {
		return false, ErrInvalid
	}
	return true, nil
}

func (p *WorkerOperatorPending) inspectUnusedGrantOriginal(ctx context.Context) (*WorkerGrantRecord, error) {
	w := p.custody
	n := w.raw.files
	lockExists, err := p.privateEntryOriginal(lockName)
	if err != nil {
		return nil, err
	}
	journalExists, err := p.privateEntryOriginal(journalName)
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
	journal, err := w.readJournalOriginal(ctx)
	if err != nil {
		return nil, err
	}
	if len(journal.records) != 1 {
		return nil, ErrUnresolved
	}
	grant := journal.records[0].Grant
	if grant.Intent.Home != w.path || grant.Intent.HomeID != n.home.id {
		return nil, ErrInvalid
	}
	if err = n.closeAuxiliary(n.journal); err != nil {
		return nil, err
	}
	n.journal = nil
	return grant, nil
}

func (p *WorkerOperatorPending) ApplyOriginal(ctx context.Context, expect string) (result WorkerAuthorizationObservation, resultErr error) {
	if p == nil || p.self != p || p.custody == nil || p.custody.operator != p || p.custody.raw == nil || !validDigest(expect) {
		return result, ErrInvalid
	}
	p.custody.raw.mu.Lock()
	planned := p.planComplete
	p.custody.raw.mu.Unlock()
	if !planned {
		if _, err := p.PlanOriginal(ctx); err != nil {
			return result, err
		}
	}
	op, err := p.beginOriginal("worker-operator-apply")
	if err != nil {
		return result, err
	}
	normal := false
	defer p.custody.raw.finish(op, &normal, &resultErr)
	result, resultErr = p.applyBodyOriginal(ctx, expect)
	if resultErr == nil {
		p.custody.raw.mu.Lock()
		p.applyComplete = true
		p.custody.raw.mu.Unlock()
	}
	normal = true
	return result, resultErr
}

func (p *WorkerOperatorPending) applyBodyOriginal(ctx context.Context, expect string) (WorkerAuthorizationObservation, error) {
	var result WorkerAuthorizationObservation
	if err := workerContextData(ctx); err != nil {
		return result, err
	}
	w := p.custody
	n := w.raw.files
	if p.plan == nil || expect != p.plan.SHA256 || w.image == nil {
		return result, ErrInvalid
	}
	if err := p.validateHomeOriginal(); err != nil {
		return result, err
	}
	n.progress("worker_operator_preapply_image_proof_pending")
	proof, err := w.image.proofOriginal(ctx)
	if err != nil {
		return result, err
	}
	if proof.SHA256 != p.plan.Intent.Profile.Core.Build.SHA256 || proof.Size != p.plan.Intent.Profile.Core.Build.Size {
		return result, ErrInvalid
	}
	profile, _, err := p.readInputOriginal(ctx, p.profilePath, MaxProfileBytes)
	if err != nil {
		return result, err
	}
	evidence, _, err := p.readInputOriginal(ctx, p.evidencePath, MaxEvidenceBytes)
	if err != nil {
		return result, err
	}
	if (OperatorInputs{Profile: profile, Evidence: evidence}) != p.plan.Intent.Inputs {
		return result, ErrInvalid
	}
	grant := workerGrantForPlanData(p.plan)
	if _, err = encodeWorkerJournalData([]WorkerRecordData{{Schema: 3, Sequence: 1, Kind: "worker-operator-authorized", Grant: grant}}); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	createdLock, err := p.createAuthorityOriginal(lockName)
	if errors.Is(err, unix.EEXIST) {
		n.lock, err = n.openPrivateAt(n.home, lockName, unix.O_RDWR)
	} else if err == nil {
		n.lock = createdLock
	}
	if err != nil {
		return result, err
	}
	n.progress("worker_operator_authority_lock_pending")
	if err = unix.Flock(int(n.lock.file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return result, err
	}
	exists, err := p.privateEntryOriginal(journalName)
	if err != nil {
		return result, err
	}
	if exists {
		n.journal, err = n.openPrivateAt(n.home, journalName, unix.O_RDWR)
		if err != nil {
			return result, err
		}
		journal, err := w.readJournalOriginal(ctx)
		if err != nil {
			return result, err
		}
		if createdLock != nil || len(journal.records) != 1 || journal.records[0].Grant.PlanSHA != expect || journal.records[0].Grant.ID != grant.ID || journal.records[0].Grant.Intent.HomeID != n.home.id {
			return result, ErrUnresolved
		}
		if err = n.validateCurrent(w.path); err != nil {
			return result, err
		}
		w.raw.mu.Lock()
		w.journal, w.grant = journal, journal.records[0].Grant
		w.raw.mu.Unlock()
		return workerAuthorizationData(p.key, journal, true, false, "unknown_from_saved_record"), nil
	}
	if createdLock == nil || p.plan.ExistingGrantID != "" {
		return result, ErrUnresolved
	}
	n.journal, err = p.createAuthorityOriginal(journalName)
	if err != nil {
		return result, err
	}
	if err = n.validateCurrent(w.path); err != nil {
		return result, err
	}
	n.progress("worker_operator_empty_namespace_sync_pending")
	if err = n.lock.file.Sync(); err != nil {
		return result, err
	}
	if err = n.journal.file.Sync(); err != nil {
		return result, err
	}
	if err = n.home.file.Sync(); err != nil {
		return result, err
	}
	w.raw.mu.Lock()
	p.namespaceSynced = true
	w.raw.mu.Unlock()
	if err = w.appendJournalOriginal(ctx, WorkerRecordData{Schema: 3, Sequence: 1, Kind: "worker-operator-authorized", Grant: grant}); err != nil {
		return result, err
	}
	w.raw.mu.Lock()
	w.grant = grant
	w.raw.mu.Unlock()
	if err = n.validateCurrent(w.path); err != nil {
		return result, err
	}
	return workerAuthorizationData(p.key, w.journal, false, false, "sync_returned_this_original"), nil
}

func (p *WorkerOperatorPending) createAuthorityOriginal(name string) (*retainedFD, error) {
	w := p.custody
	n := w.raw.files
	if name != lockName && name != journalName {
		return nil, ErrInvalid
	}
	if err := n.capacity(1); err != nil {
		return nil, err
	}
	w.raw.mu.Lock()
	p.mutationAttempted = true
	w.raw.mu.Unlock()
	n.progress("worker_operator_authority_exclusive_create_pending")
	fd, err := unix.Openat(int(n.home.file.Fd()), name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	file, err := n.retain(fd, name)
	if err != nil {
		return file, err
	}
	n.progress("worker_operator_created_mode_pending")
	if err = unix.Fchmod(fd, 0600); err != nil {
		return file, err
	}
	return file, privateNative(file, false)
}

func workerAuthorizationData(key string, journal workerJournalData, existing, readOnly bool, commit string) WorkerAuthorizationObservation {
	g := journal.records[0].Grant
	return WorkerAuthorizationObservation{Schema: 3, Key: key, State: journal.records[len(journal.records)-1].Kind, PlanSHA256: g.PlanSHA, GrantID: g.ID, Revision: g.Revision, RecordSequence: uint32(len(journal.records)), OriginalCommit: commit, Existing: existing, ReadOnly: readOnly, ExactRecordMatched: true}
}

func (p *WorkerOperatorPending) InspectOriginal(ctx context.Context, key string) (result WorkerAuthorizationObservation, resultErr error) {
	if p == nil || p.self != p || !boundedText(key, 128) || key != p.key {
		return result, ErrInvalid
	}
	op, err := p.beginOriginal("worker-operator-inspect")
	if err != nil {
		return result, err
	}
	normal := false
	defer p.custody.raw.finish(op, &normal, &resultErr)
	result, resultErr = p.inspectBodyOriginal(ctx)
	normal = true
	return result, resultErr
}

func (p *WorkerOperatorPending) inspectBodyOriginal(ctx context.Context) (WorkerAuthorizationObservation, error) {
	result := WorkerAuthorizationObservation{Schema: 3, Key: p.key, State: "unknown", OriginalCommit: "unknown_from_readonly_inspection", ReadOnly: true}
	if err := workerContextData(ctx); err != nil {
		return result, err
	}
	w := p.custody
	n := w.raw.files
	if err := n.openHome(w.path); err != nil {
		if errors.Is(err, unix.ENOENT) {
			result.State = "home_not_observed"
			return result, nil
		}
		return result, err
	}
	lockExists, err := p.privateEntryOriginal(lockName)
	if err != nil {
		return result, err
	}
	journalExists, err := p.privateEntryOriginal(journalName)
	if err != nil {
		return result, err
	}
	if !lockExists || !journalExists {
		result.State = "partial_authority"
		if !lockExists && !journalExists {
			result.State = "authority_files_not_observed"
		}
		return result, p.validateHomeOriginal()
	}
	n.lock, err = n.openPrivateAt(n.home, lockName, unix.O_RDONLY)
	if err != nil {
		return result, err
	}
	n.journal, err = n.openPrivateAt(n.home, journalName, unix.O_RDONLY)
	if err != nil {
		return result, err
	}
	journal, err := w.readJournalOriginal(ctx)
	if err != nil {
		return result, err
	}
	g := journal.records[0].Grant
	if g.Intent.Key != p.key || g.Intent.Home != w.path || g.Intent.HomeID != n.home.id || g.Intent.Inputs.Profile.Path != p.profilePath || g.Intent.Inputs.Evidence.Path != p.evidencePath || g.Intent.Reason != p.reason {
		return result, ErrInvalid
	}
	if err = n.validateCurrent(w.path); err != nil {
		return result, err
	}
	return workerAuthorizationData(p.key, journal, true, true, "unknown_from_readonly_inspection"), nil
}

func (p *WorkerOperatorPending) ObserveOriginal() WorkerCustodyObservation {
	if p == nil || p.self != p || p.custody == nil || p.custody.operator != p {
		return WorkerCustodyObservation{FirstError: ErrInvalid, NativeOutcome: "unknown"}
	}
	return p.custody.observeData()
}
