package residentbirth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"

	"getarm.org/internal/mesh"
	"getarm.org/internal/server/internal/residentservice"
	"golang.org/x/sys/unix"
)

type workerParentPostStartHeld struct {
	entry                                                    *workerFenceFD
	stat                                                     unix.Stat_t
	statAttempted, statReturned                              bool
	statError                                                error
	fdFlags, accessFlags                                     int
	fdAttempted, fdReturned, accessAttempted, accessReturned bool
	fdError, accessError                                     error
}

type workerParentPostStartName struct {
	parent, target      *workerFenceFD
	name                string
	stat                unix.Stat_t
	attempted, returned bool
	err                 error
}

type workerParentPostStartCall struct {
	owner                                                          *workerFenceOwner
	op                                                             *workerFenceOperation
	prepared                                                       *WorkerPreparedFence
	group                                                          *residentservice.WorkerServiceGroup
	command                                                        *exec.Cmd
	state                                                          *workerFenceNative
	service                                                        *residentservice.WorkerParentAckAuthentication
	serviceBefore, serviceAfter                                    residentservice.WorkerParentCurrentServiceData
	serviceBeforeAttempted, serviceBeforeReturned                  bool
	serviceAfterAttempted, serviceAfterReturned                    bool
	serviceBeforeError, serviceAfterError                          error
	process                                                        *os.Process // consistency only; actual normal Start belongs to Root
	processObserved                                                bool
	uidAttempted, uidReturned                                      bool
	uid                                                            int
	held                                                           [6]workerParentPostStartHeld
	names                                                          [5]workerParentPostStartName
	before, after                                                  os.FileInfo
	beforeAttempted, beforeReturned, afterAttempted, afterReturned bool
	beforeError, afterError                                        error
	data                                                           []byte // separate actual current observation; original intent stays immutable
	reads                                                          [32]workerFooterIO
	readCalls, readBytes                                           int
	verified                                                       bool
	result                                                         WorkerParentPostStartData
	normalReturned                                                 bool
	resultError                                                    error
}

// Bounded DATA, not normal Cmd.Start, an issuer, native closure or readiness.
type WorkerParentPostStartData struct {
	Service residentservice.WorkerParentCurrentServiceData
	Launch  WorkerRootBoundFooterResultData
}

func completedWorkerParentFenceOperationData(op *workerFenceOperation, owner *workerFenceOwner, kind string) bool {
	return op != nil && op.owner == owner && op.kind == kind && op.ctx != nil && op.normalFinished && !op.abnormal && op.finalError == nil
}

// LOCAL publication metadata only. Old holders without the actual original
// operations refuse; no operation is reconstructed from flags or DTOs.
func (o *workerFenceOwner) postStartPreparedLocked(prepared *WorkerPreparedFence) bool {
	if o.self == nil || o.self.owner != o || prepared == nil || prepared != o.prepared || prepared.owner != o || o.firstError != nil || o.group == nil || o.command == nil || o.state == nil || o.state.owner != o || !o.bound || !o.intentCommitted || !o.writerWithdrawn || !o.shortSealTried || !o.beforeStartTried || o.withdrawOriginal == o.shortSealOriginal || !completedWorkerParentFenceOperationData(o.withdrawOriginal, o, "withdraw") || !completedWorkerParentFenceOperationData(o.shortSealOriginal, o, "short-seal") || len(o.intent) == 0 || len(o.intent) > maxWorkerIntentBytes || o.written != len(o.intent) || !bytes.Equal(o.intent, o.verification) {
		return false
	}
	if len(o.extra) != 13 || len(o.command.ExtraFiles) != 13 || &o.extra[0] != &o.command.ExtraFiles[0] {
		return false
	}
	for _, e := range []struct {
		index int
		entry *workerFenceFD
	}{{4, o.state.launch}, {5, o.state.journal}, {9, o.state.home}, {10, o.state.daemon}, {11, o.state.directory}, {12, o.state.ledger}} {
		if e.entry == nil || e.entry.file == nil || e.entry.closeTried || e.entry.closed || o.extra[e.index] != e.entry.file {
			return false
		}
	}
	return true
}

func (o *workerFenceOwner) postStartCallLocked(c *workerParentPostStartCall) bool {
	return c != nil && c == o.postStart && c.owner == o && c.op != nil && c.op.owner == o && c.op.kind == "worker-post-start" && !c.op.abnormal && o.active == c.op && c.group == o.group && c.command == o.command && c.state == o.state && c.service != nil && o.postStartPreparedLocked(c.prepared) && (!c.processObserved || c.process != nil && c.command.Process == c.process)
}

func (o *workerFenceOwner) postStartGate(c *workerParentPostStartCall) error {
	o.mu.Lock()
	valid := o.postStartCallLocked(c)
	o.mu.Unlock()
	if !valid {
		return errOriginalWorkerFence
	}
	return c.op.ctx.Err()
}

func (o *workerFenceOwner) publishPostStart(c *workerParentPostStartCall, normal *bool, resultErr *error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if c == nil || c != o.postStart || c.owner != o || c.op == nil || c.op.owner != o || c.op.kind != "worker-post-start" {
		panic("original parent postStart fence identity lost")
	}
	c.normalReturned = *normal && !c.op.abnormal && c.op.normalFinished
	c.resultError = *resultErr
}

// The future PRIVATE Root must gate this call with its OWN still-active
// launch-and-transfer op, actual normal Cmd.Start/exact returned Process and
// completed original bootstrap. Process!=nil below is consistency only. Old
// preStart/ShortSeal/validate/validateIntent methods must not be rerun here.
func (h *WorkerFreshFencePending) ValidateWorkerPostStartOriginal(ctx context.Context, prepared *WorkerPreparedFence, command *exec.Cmd, service *residentservice.WorkerParentAckAuthentication) (data WorkerParentPostStartData, resultErr error) {
	if h == nil || h.owner == nil || h.owner.self != h || ctx == nil || command == nil || command != h.owner.command || service == nil {
		return data, errOriginalWorkerFence
	}
	if err := ctx.Err(); err != nil {
		return data, err
	}
	op, err := h.begin(ctx, "worker-post-start", prepared)
	if err != nil {
		return data, err
	}
	o := h.owner
	c := &workerParentPostStartCall{owner: o, op: op, prepared: prepared, group: o.group, command: command, state: o.state, service: service}
	o.mu.Lock()
	o.postStart = c // actual call retained BEFORE service/native/IO
	o.mu.Unlock()
	normal := false
	defer o.publishPostStart(c, &normal, &resultErr)
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.validatePostStartBody(c)
	if resultErr == nil {
		data = c.result
	}
	normal = true
	return data, resultErr
}

func (o *workerFenceOwner) validatePostStartBody(c *workerParentPostStartCall) error {
	if err := o.postStartGate(c); err != nil {
		return err
	}
	c.serviceBeforeAttempted = true
	c.serviceBefore, c.serviceBeforeError = c.service.ValidateOriginalGroup(c.group, c.command)
	c.serviceBeforeReturned = true
	if c.serviceBeforeError != nil {
		return c.serviceBeforeError
	}
	if err := o.postStartGate(c); err != nil {
		return err
	}
	c.process, c.processObserved = c.command.Process, true
	if c.process == nil {
		return errOriginalWorkerFence
	}
	c.uidAttempted = true
	c.uid = os.Geteuid()
	c.uidReturned = true
	if uint32(c.uid) != o.uid {
		return errOriginalWorkerFence
	}
	for i, e := range []struct {
		entry     *workerFenceFD
		directory bool
	}{{c.state.launch, false}, {c.state.journal, false}, {c.state.home, true}, {c.state.daemon, false}, {c.state.directory, true}, {c.state.ledger, false}} {
		h := &c.held[i]
		h.entry = e.entry
		if e.entry == nil || e.entry.file == nil || e.entry.closeTried || e.entry.closed {
			return errOriginalWorkerFence
		}
		if err := c.op.ctx.Err(); err != nil {
			return err
		}
		h.statAttempted = true
		h.statError = unix.Fstat(e.entry.number, &h.stat)
		h.statReturned = true
		if h.statError != nil {
			return h.statError
		}
		mode, access := uint32(unix.S_IFREG)|0600, unix.O_RDWR
		if e.directory {
			mode, access = uint32(unix.S_IFDIR)|0700, unix.O_RDONLY
		}
		if fenceID(&h.stat) != e.entry.id || h.stat.Uid != o.uid || uint32(h.stat.Mode) != mode || h.stat.Nlink == 0 || !e.directory && h.stat.Nlink != 1 {
			return errOriginalWorkerFence
		}
		if err := c.op.ctx.Err(); err != nil {
			return err
		}
		h.fdAttempted = true
		h.fdFlags, h.fdError = unix.FcntlInt(uintptr(e.entry.number), unix.F_GETFD, 0)
		h.fdReturned = true
		if h.fdError != nil {
			return h.fdError
		}
		if h.fdFlags&unix.FD_CLOEXEC == 0 {
			return errOriginalWorkerFence
		}
		if err := c.op.ctx.Err(); err != nil {
			return err
		}
		h.accessAttempted = true
		h.accessFlags, h.accessError = unix.FcntlInt(uintptr(e.entry.number), unix.F_GETFL, 0)
		h.accessReturned = true
		if h.accessError != nil {
			return h.accessError
		}
		if h.accessFlags&unix.O_ACCMODE != access {
			return errOriginalWorkerFence
		}
	}
	for i, e := range []struct {
		parent, target *workerFenceFD
		name           string
	}{{c.state.home, c.state.launch, "resident-launch.lock"}, {c.state.home, c.state.daemon, "daemon.lock"}, {c.state.home, c.state.journal, "resident-birth.json"}, {c.state.home, c.state.directory, "state.db"}, {c.state.directory, c.state.ledger, "ledger.sqlite"}} {
		n := &c.names[i]
		n.parent, n.target, n.name = e.parent, e.target, e.name
		if err := c.op.ctx.Err(); err != nil {
			return err
		}
		n.attempted = true
		n.err = unix.Fstatat(e.parent.number, e.name, &n.stat, unix.AT_SYMLINK_NOFOLLOW)
		n.returned = true
		if n.err != nil {
			return n.err
		}
		if fenceID(&n.stat) != e.target.id || n.stat.Nlink == 0 || uint32(n.stat.Mode)&unix.S_IFMT == unix.S_IFREG && n.stat.Nlink != 1 {
			return errOriginalWorkerFence
		}
	}
	if err := o.postStartGate(c); err != nil {
		return err
	}
	if err := o.readPostStartJournal(c); err != nil {
		return err
	}
	c.serviceAfterAttempted = true
	c.serviceAfter, c.serviceAfterError = c.service.ValidateOriginalGroup(c.group, c.command)
	c.serviceAfterReturned = true
	if c.serviceAfterError != nil {
		return c.serviceAfterError
	}
	if c.serviceAfter != c.serviceBefore {
		return errOriginalWorkerFence
	}
	c.result.Service = c.serviceAfter
	return o.postStartGate(c)
}

func (o *workerFenceOwner) readPostStartJournal(c *workerParentPostStartCall) error {
	if err := c.op.ctx.Err(); err != nil {
		return err
	}
	c.beforeAttempted = true
	c.before, c.beforeError = c.state.journal.file.Stat()
	c.beforeReturned = true
	if c.beforeError != nil {
		return c.beforeError
	}
	if c.before == nil || c.before.Size() <= 0 || c.before.Size() > MaxWorkerLaunchJournalBytes || c.before.Size() != c.serviceBefore.Context.LaunchJournalBytes {
		return errOriginalWorkerFence
	}
	c.data = make([]byte, int(c.before.Size())) // own distinct buffer BEFORE IO
	for c.readBytes < len(c.data) {
		if err := c.op.ctx.Err(); err != nil {
			return err
		}
		if c.readCalls >= len(c.reads) {
			return io.ErrNoProgress
		}
		r := &c.reads[c.readCalls]
		r.offset, r.requested = int64(c.readBytes), len(c.data)-c.readBytes
		c.readCalls++
		n, err := c.state.journal.file.ReadAt(c.data[c.readBytes:], r.offset)
		r.returned, r.err, r.returnedKnown = n, err, true
		if n < 0 || n > r.requested {
			return errOriginalWorkerFence
		}
		c.readBytes += n
		if err != nil && !(errors.Is(err, io.EOF) && c.readBytes == len(c.data)) {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	if err := c.op.ctx.Err(); err != nil {
		return err
	}
	c.afterAttempted = true
	c.after, c.afterError = c.state.journal.file.Stat()
	c.afterReturned = true
	if c.afterError != nil {
		return c.afterError
	}
	if c.after == nil || !os.SameFile(c.before, c.after) || c.before.Size() != c.after.Size() || !c.before.ModTime().Equal(c.after.ModTime()) {
		return errOriginalWorkerFence
	}
	var err error
	c.result.Launch, err = validateWorkerParentPostStartJournalData(c.data, o.intent, c.serviceBefore, o.service, o.image)
	if err != nil {
		return err
	}
	c.verified = true
	return c.op.ctx.Err()
}

// Closed DATA comparison only. Settings match footer/authenticated Context here;
// actual settings snapshot, typed intent/control layout and fresh cross-component
// canonical commitment remain PRIVATE Root structural prerequisites. No duplicate
// server-private intent parser or supplied settings DTO certifies those checks.
func validateWorkerParentPostStartJournalData(current, intent []byte, service residentservice.WorkerParentCurrentServiceData, own residentservice.WorkerServiceSelectorsData, image mesh.ResidentImageInventory) (WorkerRootBoundFooterResultData, error) {
	var empty WorkerRootBoundFooterResultData
	c := service.Context
	if c.Validate() != nil || len(intent) == 0 || len(intent) > maxWorkerIntentBytes || len(current) <= len(intent) || len(current) > MaxWorkerLaunchJournalBytes || int64(len(current)) != c.LaunchJournalBytes || !bytes.Equal(current[:len(intent)], intent) {
		return empty, errOriginalWorkerFence
	}
	footer := current[len(intent):] // alias actual read buffer, no extra footer copy
	if int64(len(footer)) != c.FooterBytes {
		return empty, errOriginalWorkerFence
	}
	decoded, err := DecodeWorkerRootBoundFooterData(footer)
	if err != nil {
		return empty, err
	}
	ih, fh, jh := sha256.Sum256(intent), sha256.Sum256(footer), sha256.Sum256(current)
	intentSHA, footerSHA, journalSHA := hex.EncodeToString(ih[:]), hex.EncodeToString(fh[:]), hex.EncodeToString(jh[:])
	expected, err := WorkerFooterForServiceData(service.RootBound, intentSHA, c.SettingsSHA256)
	if err != nil {
		return empty, err
	}
	key, err := WorkerLaunchKeyCommitmentData(own.LaunchKey)
	if err != nil {
		return empty, err
	}
	if decoded != expected || c.ContractSHA256 != own.ContractSHA256 || c.InitialPackageSHA256 != own.Contract.InitialPackageSHA256 || c.InitialManifestSHA256 != own.Contract.InitialManifestSHA256 || decoded.ContractSHA256 != c.ContractSHA256 || decoded.InitialPackageSHA256 != c.InitialPackageSHA256 || decoded.InitialManifestSHA256 != c.InitialManifestSHA256 || c.Generation != decoded.Generation || c.DebitID != decoded.DebitID || c.LaunchKeySHA256 != decoded.LaunchKeySHA256 || c.PreparedCommitmentSHA256 != decoded.PreparedCommitmentSHA256 || c.IntentSHA256 != intentSHA || c.ProfileSHA256 != decoded.ProfileSHA256 || c.ImageSHA256 != decoded.ImageSHA256 || c.ServiceJournalBytes != decoded.ServiceJournalBytes || c.ServiceJournalSHA256 != decoded.ServiceJournalSHA256 || c.FooterSHA256 != footerSHA || c.LaunchJournalSHA256 != journalSHA || c.DebitID != own.DebitID || service.RootBound.Record.LaunchKey != own.LaunchKey || c.LaunchKeySHA256 != key || c.ProfileSHA256 != own.ProfileSHA256 || c.ImageSHA256 != own.Build.SHA256 || c.ImageBytes != own.Build.Size || c.ImageSHA256 != image.SHA256 || c.ImageBytes != image.Size || image.DebitID != own.DebitID || image.Home != own.Home || service.HomeID != own.HomeID || service.RootBound.HomeID != own.HomeID || image.HomeID.Device != own.HomeID.Device || image.HomeID.Inode != own.HomeID.Inode || image.HomeID.UID != own.HomeID.UID {
		return empty, errOriginalWorkerFence
	}
	return WorkerRootBoundFooterResultData{Footer: decoded, FooterBytes: len(footer), JournalBytes: len(current), FooterSHA256: footerSHA, JournalSHA256: journalSHA}, nil
}

func (o *workerFenceOwner) completedPostStartLocked(c *workerParentPostStartCall, prepared *WorkerPreparedFence, command *exec.Cmd, service *residentservice.WorkerParentAckAuthentication) bool {
	if c == nil || c != o.postStart || c.owner != o || c.op == nil || !completedWorkerParentFenceOperationData(c.op, o, "worker-post-start") || !c.normalReturned || c.resultError != nil || o.active != nil || !o.postStartPreparedLocked(prepared) || c.prepared != prepared || command != o.command || c.command != command || c.group != o.group || c.state != o.state || c.service != service || service == nil || !c.processObserved || c.process == nil || c.command.Process != c.process || !c.uidAttempted || !c.uidReturned || !c.verified || !c.serviceBeforeAttempted || !c.serviceBeforeReturned || c.serviceBeforeError != nil || !c.serviceAfterAttempted || !c.serviceAfterReturned || c.serviceAfterError != nil || c.serviceBefore != c.serviceAfter || c.result.Service != c.serviceAfter || !c.beforeAttempted || !c.beforeReturned || c.beforeError != nil || !c.afterAttempted || !c.afterReturned || c.afterError != nil || c.before == nil || c.after == nil || c.readCalls <= 0 || c.readCalls > len(c.reads) || c.readBytes != len(c.data) {
		return false
	}
	for _, h := range c.held {
		if !h.statAttempted || !h.statReturned || h.statError != nil || !h.fdAttempted || !h.fdReturned || h.fdError != nil || !h.accessAttempted || !h.accessReturned || h.accessError != nil {
			return false
		}
	}
	for _, n := range c.names {
		if !n.attempted || !n.returned || n.err != nil {
			return false
		}
	}
	return true
}

// Exact completed original call selector; foreign service validation is outside
// the fence publication lock and rechecked against the same completed record.
func (h *WorkerFreshFencePending) WorkerPostStartOriginalData(prepared *WorkerPreparedFence, command *exec.Cmd, service *residentservice.WorkerParentAckAuthentication) (WorkerParentPostStartData, error) {
	if h == nil || h.owner == nil || h.owner.self != h {
		return WorkerParentPostStartData{}, errOriginalWorkerFence
	}
	o := h.owner
	o.mu.Lock()
	c := o.postStart
	valid := o.completedPostStartLocked(c, prepared, command, service)
	o.mu.Unlock()
	if !valid {
		return WorkerParentPostStartData{}, errOriginalWorkerFence
	}
	data, err := service.ValidateOriginalGroup(c.group, c.command)
	if err != nil {
		return WorkerParentPostStartData{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.completedPostStartLocked(c, prepared, command, service) || data != c.serviceAfter {
		return WorkerParentPostStartData{}, errOriginalWorkerFence
	}
	return c.result, nil
}
