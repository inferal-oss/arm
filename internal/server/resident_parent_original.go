package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"
)

var errResidentParentOriginal = errors.New("original composed resident parent required; retain original pending custody")

// The sole parent launch owner. No command, File/FD, Start, child Ready or Close
// capability is exposed to another component. Components bind nonclosing slots
// on this ONE retained command. No error path destroys/abandons the real debit,
// resets a writer, retires references, cancels the core or issues another key.
type residentParentPending struct {
	mu                                  *sync.Mutex
	self                                *residentParentPending
	home, launchKey                     string
	settings                            daemonconfig.Settings
	settingsSHA                         string
	command                             *exec.Cmd
	extra                               []*os.File
	bound                               [13]*os.File
	environment                         []string
	service                             *residentservice.ParentPending
	debit                               *residentservice.OriginalDebit
	group                               *residentservice.P1ServiceGroup
	selectors                           residentservice.P1ServiceSelectors
	pin                                 *mesh.ResidentImagePinPending
	image                               *mesh.ResidentImageP1Pending
	fence                               *residentbirth.FreshFence
	preparedFence                       *residentbirth.PreparedFence
	journal                             *residentbirth.LaunchJournal
	bootstrap                           p1RootBootstrap
	intent, bootstrapRaw, frame         []byte
	preflightFrameBytes                 int
	active                              *residentParentOperation
	prepareTried, prepared, launchTried bool
	startAttempted, startReturned       bool
	startError                          error
	process                             *os.Process
	transferWritten                     bool // DATA only; never Ready/ACK/native/worker success
	firstError                          error
}
type residentParentOperation struct {
	ctx         context.Context
	kind, phase string
	abnormal    bool
	cause       any
}

// Pure parent allocation/settings copy. Expected executable/env come only from
// the real UNUSED grant opened by prepareOriginalBirth; global image hashing
// happens only later, after the actual committed debit and group exist.
func newResidentParentPending(home, key string, settings daemonconfig.Settings) (root *residentParentPending, resultErr error) {
	root = &residentParentPending{mu: &sync.Mutex{}}
	root.self = root
	defer func() {
		if resultErr != nil {
			root.mu.Lock()
			root.firstError = resultErr
			root.mu.Unlock()
		}
	}()
	if !filepath.IsAbs(home) || filepath.Clean(home) != home || home == "/" || len(home) > 4096 || strings.ContainsRune(home, 0) || key == "" || len(key) > 128 || !utf8.ValidString(key) || strings.ContainsAny(key, "\x00\r\n") {
		return root, errResidentParentOriginal
	}
	root.home, root.launchKey = home, key
	root.settingsSHA, resultErr = daemonconfig.SnapshotSHA256(settings)
	if resultErr != nil {
		return root, resultErr
	}
	raw, err := json.Marshal(settings)
	if err != nil || len(raw) > daemonconfig.MaxBytes {
		return root, errResidentParentOriginal
	}
	if resultErr = json.Unmarshal(raw, &root.settings); resultErr != nil {
		return root, resultErr
	}
	root.command = &exec.Cmd{Dir: "/", ExtraFiles: make([]*os.File, 13), SysProcAttr: &syscall.SysProcAttr{Setsid: true}}
	root.extra = root.command.ExtraFiles
	root.service, resultErr = residentservice.NewP1ExistingGrantParentPending(home)
	return root, resultErr
}
func (r *residentParentPending) beginOriginal(ctx context.Context, kind string) (*residentParentOperation, error) {
	if r == nil || r.self != r || r.mu == nil {
		return nil, errResidentParentOriginal
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil || r.firstError != nil {
		return nil, errResidentParentOriginal
	}
	switch kind {
	case "prepare":
		if r.prepareTried {
			return nil, errResidentParentOriginal
		}
		r.prepareTried = true
	case "launch-and-transfer":
		if !r.prepared || r.launchTried {
			return nil, errResidentParentOriginal
		}
		r.launchTried = true
	default:
		return nil, errResidentParentOriginal
	}
	op := &residentParentOperation{ctx: ctx, kind: kind, phase: "registered_before_original_effect"}
	r.active = op
	return op, nil
}
func (r *residentParentPending) phaseOriginal(phase string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		panic("original parent operation missing")
	}
	r.active.phase = phase
}
func (r *residentParentPending) finishOriginal(op *residentParentOperation, normal *bool, resultErr *error) {
	cause := recover()
	if *normal && *resultErr == nil {
		if op.ctx == nil {
			*resultErr = errResidentParentOriginal
		} else {
			*resultErr = op.ctx.Err()
		}
	}
	r.mu.Lock()
	if r.active != op {
		r.mu.Unlock()
		panic("original composed parent operation identity lost")
	}
	if !*normal {
		op.abnormal = true
		op.cause = cause
		if r.firstError == nil {
			r.firstError = &residentServerAbnormal{cause: cause}
		}
	} else {
		if *resultErr != nil && r.firstError == nil {
			r.firstError = *resultErr
		}
		if *resultErr == nil && op.kind == "prepare" {
			r.prepared = true
		}
		r.active = nil
	}
	r.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}
func (r *residentParentPending) prepareOriginalBirth(ctx context.Context) (resultErr error) {
	op, err := r.beginOriginal(ctx, "prepare")
	if err != nil {
		return err
	}
	normal := false
	defer r.finishOriginal(op, &normal, &resultErr)
	resultErr = r.prepareOriginalBody(ctx)
	normal = true
	return resultErr
}
func (r *residentParentPending) prepareOriginalBody(ctx context.Context) (err error) {
	if ctx == nil {
		return errResidentParentOriginal
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	r.phaseOriginal("actual_unused_original_grant_open")
	if err = r.service.OpenExistingAuthorizedOriginal(ctx); err != nil {
		return err
	}
	inputs, err := r.service.P1LaunchInputsOriginalData()
	if err != nil {
		return err
	}
	if inputs.Home != r.home || inputs.Build.GOOS != runtime.GOOS || inputs.Build.GOARCH != runtime.GOARCH || inputs.Build.GoBuild != runtime.Version() {
		return errResidentParentOriginal
	}
	r.environment = append([]string(nil), inputs.Environment...)
	r.command.Env = append([]string(nil), r.environment...)
	r.phaseOriginal("bounded_actual_unused_grant_capacity_before_debit_image_fence")
	r.preflightFrameBytes, err = p1BootstrapCapacityData(inputs, r.settings, r.launchKey)
	if err != nil {
		return err
	}
	r.phaseOriginal("actual_original_debit_before_any_image_fence_effect")
	r.debit, err = r.service.PrepareOriginalDebit(ctx, r.launchKey)
	if err != nil {
		return err
	}
	r.group, err = residentservice.NewP1ServiceGroup(r.service, r.debit, r.command)
	if err != nil {
		return err
	}
	if err = r.group.ValidateFundingOriginal(ctx); err != nil {
		return err
	}
	r.selectors, err = r.group.SelectorsOriginal()
	if err != nil {
		return err
	}
	if r.selectors.Home != inputs.Home || r.selectors.Build != inputs.Build || r.selectors.ProfileSHA256 != inputs.ProfileSHA256 || r.selectors.LaunchKey != r.launchKey {
		return errResidentParentOriginal
	}
	path := filepath.Join(r.home, "resident-images", "pin-"+r.selectors.DebitID, "arm")
	r.command.Path = path
	r.command.Args = []string{path, mesh.ResidentP1CoreRole}
	r.phaseOriginal("funded_actual_current_A_capture_and_exclusive_readonly_pin")
	r.pin, err = r.group.NewOriginalImagePinPending()
	if err != nil {
		return err
	}
	if err = r.pin.PrepareOriginal(ctx); err != nil {
		return err
	}
	if err = r.group.PrepareOriginalChannel(ctx); err != nil {
		return err
	}
	if err = r.group.BindOriginal(ctx); err != nil {
		return err
	}
	r.phaseOriginal("actual_exclusive_fresh_fence_and_six_reference_binding")
	r.fence, err = residentbirth.NewFreshFencePending(r.group, r.pin, r.command)
	if err != nil {
		return err
	}
	r.preparedFence, err = r.fence.PrepareOriginal(ctx)
	if err != nil {
		return err
	}
	if err = r.fence.BindOriginal(ctx, r.preparedFence); err != nil {
		return err
	}
	r.phaseOriginal("actual_three_image_reference_binding_and_all13_snapshot")
	r.image, err = mesh.NewResidentImageP1Pending(r.pin, r.command)
	if err != nil {
		return err
	}
	if err = r.image.BindOriginal(ctx); err != nil {
		return err
	}
	copy(r.bound[:], r.command.ExtraFiles)
	r.bootstrap = p1RootBootstrap{Schema: 2, Settings: r.settings, SettingsSHA256: r.settingsSHA}
	r.bootstrap.Service, err = r.group.BootstrapOriginalData()
	if err != nil {
		return err
	}
	r.bootstrap.Fence, r.bootstrap.Image, err = r.fence.PreparedBindingsOriginalData(r.preparedFence)
	if err != nil {
		return err
	}
	r.intent, err = encodeP1IntentData(r.bootstrap)
	if err != nil {
		return err
	}
	r.phaseOriginal("actual_original_bounded_intent_append_and_sync")
	r.journal, err = r.fence.JournalForOriginal(ctx, r.preparedFence)
	if err != nil {
		return err
	}
	if err = r.journal.AppendOriginal(ctx, r.intent); err != nil {
		return err
	}
	r.bootstrap.Fence, err = r.fence.BootstrapOriginalData(r.preparedFence)
	if err != nil {
		return err
	}
	currentIntent, err := encodeP1IntentData(r.bootstrap)
	if err != nil || !bytes.Equal(r.intent, currentIntent) {
		return errResidentParentOriginal
	}
	r.bootstrapRaw, err = json.Marshal(r.bootstrap)
	if err != nil {
		return err
	}
	var decoded p1RootBootstrap
	if err = decodeP1BootstrapData(r.bootstrapRaw, &decoded); err != nil {
		return err
	}
	r.frame, err = residentrefs.EncodeFrameData(r.bootstrapRaw)
	if err != nil {
		return err
	}
	if len(r.frame) > r.preflightFrameBytes {
		return errResidentParentOriginal
	}
	return ctx.Err()
}

// This method observes the actual one Cmd.Start itself. It does not call the
// old4FD helper or accept caller-recorded birth/success. A full transfer still
// is not ACK/current-authority/Ready or permission to retire a parent reference.
// No public callsite is enabled until the actual core/worker/ACK cut exists.
func (r *residentParentPending) launchAndTransferOriginal(ctx context.Context) (resultErr error) {
	op, err := r.beginOriginal(ctx, "launch-and-transfer")
	if err != nil {
		return err
	}
	normal := false
	defer r.finishOriginal(op, &normal, &resultErr)
	resultErr = r.launchAndTransferBody(ctx)
	normal = true
	return resultErr
}
func (r *residentParentPending) launchAndTransferBody(ctx context.Context) error {
	if ctx == nil {
		return errResidentParentOriginal
	}
	if _, ok := ctx.Deadline(); !ok {
		return errResidentParentOriginal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// The actual root operation above exists BEFORE either permanent withdrawal.
	r.phaseOriginal("both_actual_writers_withdraw_before_any_bootstrap_byte")
	if err := r.group.WithdrawOriginalWriter(ctx); err != nil {
		return err
	}
	if err := r.fence.WithdrawOriginalWriter(ctx, r.preparedFence); err != nil {
		return err
	}
	r.phaseOriginal("all_long_original_image_fence_service_validations")
	proof, err := r.image.ValidateOriginal(ctx, r.command)
	if err != nil {
		return err
	}
	if proof.SHA256 != r.selectors.Build.SHA256 || proof.Size != r.selectors.Build.Size || proof.Protocol != int(r.selectors.Build.Protocol) {
		return errResidentParentOriginal
	}
	if err = r.fence.ValidateOriginalBeforeStart(ctx, r.preparedFence, r.command); err != nil {
		return err
	}
	if err = r.group.ValidateOriginalBeforeStart(ctx, r.command); err != nil {
		return err
	}
	r.phaseOriginal("all13_original_short_native_seals_after_long_IO")
	if err = r.image.ShortSealOriginal(ctx, r.command); err != nil {
		return err
	}
	if err = r.fence.ShortSealOriginal(ctx, r.preparedFence, r.command); err != nil {
		return err
	}
	if err = r.group.ShortSealOriginal(ctx, r.command); err != nil {
		return err
	}
	if err = r.validateFinalCommandOriginalData(ctx); err != nil {
		return err
	}
	r.phaseOriginal("ONE_actual_original_Cmd_Start_pending")
	r.mu.Lock()
	r.startAttempted = true
	r.mu.Unlock()
	startError := r.command.Start() // THE SOLE ACTUAL COMPOSED PARENT START
	r.mu.Lock()
	r.startReturned = true
	r.startError = startError
	r.process = r.command.Process
	r.mu.Unlock()
	if startError != nil {
		return startError
	}
	if r.process == nil {
		return errResidentParentOriginal
	}
	r.phaseOriginal("actual_one_bounded_original_frame_transfer_no_ACK_claim")
	transferError := r.group.SendOriginalP1Frame(ctx, r.command, r.frame)
	transfer, inspectError := r.group.TransferOriginalP1Data()
	r.mu.Lock()
	r.transferWritten = transfer.FullWrite
	r.mu.Unlock()
	if transferError != nil {
		return transferError
	}
	if inspectError != nil {
		return inspectError
	}
	if !transfer.FullWrite {
		return errResidentParentOriginal
	}
	// Keep process and every actual parent original until a future reviewed real
	// ACK/current-authority exchange and owned reference-retirement cut. No wait,
	// Release, close, daemon marker, ordinary admission or Ready occurs here.
	return ctx.Err()
}
func (r *residentParentPending) validateFinalCommandOriginalData(ctx context.Context) error {
	if r == nil || r.self != r || ctx == nil {
		return errResidentParentOriginal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c := r.command
	expected := filepath.Join(r.home, "resident-images", "pin-"+r.selectors.DebitID, "arm")
	if c == nil || c.Process != nil || c.ProcessState != nil || c.Err != nil || c.Cancel != nil || c.WaitDelay != 0 || c.Stdin != nil || c.Stdout != nil || c.Stderr != nil || c.Dir != "/" || !reflect.DeepEqual(c.SysProcAttr, &syscall.SysProcAttr{Setsid: true}) || c.Path != expected || len(c.Args) != 2 || c.Args[0] != expected || c.Args[1] != mesh.ResidentP1CoreRole {
		return errResidentParentOriginal
	}
	if c.Env == nil || !reflect.DeepEqual(c.Env, r.environment) || len(c.ExtraFiles) != 13 || len(r.extra) != 13 || &c.ExtraFiles[0] != &r.extra[0] {
		return errResidentParentOriginal
	}
	seen := make(map[*os.File]struct{}, 13)
	for i, file := range c.ExtraFiles {
		if file == nil || file != r.bound[i] {
			return errResidentParentOriginal
		}
		if _, duplicate := seen[file]; duplicate {
			return errResidentParentOriginal
		}
		seen[file] = struct{}{}
	}
	if err := r.group.ValidateExecutionEnvironmentOriginal(c); err != nil {
		return err
	}
	return ctx.Err()
}
