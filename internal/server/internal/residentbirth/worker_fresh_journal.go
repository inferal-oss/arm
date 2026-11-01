package residentbirth

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"reflect"
	"syscall"

	"encoding/json"
	"getarm.org/internal/server/internal/residentservice"
	"golang.org/x/sys/unix"
)

// WorkerLaunchJournal is a nonclosing original view. Only the fence registry owns its
// FD. No File, Close, Cmd, Start, caller-recorded birth or boolean ACK is exposed.
type WorkerLaunchJournal struct {
	self     *WorkerLaunchJournal
	fence    *WorkerFreshFencePending
	prepared *WorkerPreparedFence
}
type workerFenceWrite struct {
	offset              int64
	requested, returned int
	err                 error
	returnedKnown       bool
}

func (h *WorkerFreshFencePending) JournalForOriginal(ctx context.Context, prepared *WorkerPreparedFence) (journal *WorkerLaunchJournal, resultErr error) {
	op, err := h.begin(ctx, "journal", prepared)
	if err != nil {
		return nil, err
	}
	o := h.owner
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.state.before("original_nonclosing_journal_claim")
	if resultErr == nil {
		journal = &WorkerLaunchJournal{fence: h, prepared: prepared}
		journal.self = journal
		o.mu.Lock()
		o.journal = journal
		o.mu.Unlock()
	}
	normal = true
	return journal, resultErr
}

// AppendOriginal writes exactly one caller-owned bounded encoded intent, from an
// owned copy retained before the first native write. Partial/error/abnormal
// outcomes are retained with the original operation; they are never retried.
func (j *WorkerLaunchJournal) AppendOriginal(ctx context.Context, encoded []byte) (resultErr error) {
	if j == nil || j.self != j || j.fence == nil || j.fence.owner == nil {
		return errOriginalWorkerFence
	}
	o := j.fence.owner
	o.mu.Lock()
	original := o.journal == j
	o.mu.Unlock()
	if !original {
		return errOriginalWorkerFence
	}
	op, err := j.fence.begin(ctx, "write", j.prepared)
	if err != nil {
		return err
	}
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = j.appendBody(encoded)
	normal = true
	return resultErr
}
func (j *WorkerLaunchJournal) appendBody(encoded []byte) error {
	o := j.fence.owner
	n := o.state
	if len(encoded) == 0 || len(encoded) > maxWorkerIntentBytes {
		return errOriginalWorkerFence
	}
	var intent residentservice.WorkerBirthIntentData
	if err := json.Unmarshal(encoded, &intent); err != nil || intent.Validate() != nil {
		return errOriginalWorkerFence
	}
	canonical, err := json.Marshal(intent)
	if err != nil || !bytes.Equal(encoded, canonical) {
		return errOriginalWorkerFence
	}
	expected, image, err := j.fence.preparedBindingsData(j.prepared, true)
	if err != nil {
		return err
	}
	service, err := o.group.BootstrapOriginalData()
	if err != nil {
		return err
	}
	if intent.ControlID != service.ChannelID {
		return errOriginalWorkerFence
	}
	if intent.Home != o.path || intent.HomeID != o.service.HomeID || intent.ServiceDebitID != o.service.DebitID || intent.LaunchKey != o.service.LaunchKey || intent.ProfileSHA256 != o.service.ProfileSHA256 || intent.ContractSHA256 != o.service.ContractSHA256 || intent.Build != o.service.Build || intent.Fence != expected.Identities || intent.Image != image || intent.InitialPackageSHA256 != o.service.Contract.InitialPackageSHA256 || intent.InitialManifestSHA256 != o.service.Contract.InitialManifestSHA256 || intent.InitialManifestBytes != o.service.Contract.InitialManifestBytes {
		return errOriginalWorkerFence
	}
	if err := n.before("original_bounded_intent_retention"); err != nil {
		return err
	}
	o.mu.Lock()
	o.intent = append([]byte(nil), encoded...)
	o.mu.Unlock()
	var st unix.Stat_t
	if err := n.before("original_empty_intent_stat"); err != nil {
		return err
	}
	if err := unix.Fstat(n.journal.number, &st); err != nil {
		return err
	}
	if st.Size != 0 {
		return errOriginalWorkerFence
	}
	offset := 0
	for offset < len(o.intent) {
		if err := n.before("original_intent_write_pending"); err != nil {
			return err
		}
		call := &workerFenceWrite{offset: int64(offset), requested: len(o.intent) - offset}
		o.mu.Lock()
		o.writes = append(o.writes, call)
		o.mu.Unlock()
		written, err := n.journal.file.WriteAt(o.intent[offset:], int64(offset))
		o.mu.Lock()
		call.returned, call.err, call.returnedKnown = written, err, true
		o.written += written
		o.mu.Unlock()
		if written < 0 || written > call.requested {
			return errOriginalWorkerFence
		}
		offset += written
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrNoProgress
		}
	}
	if err := n.before("original_intent_sync_pending"); err != nil {
		return err
	}
	if err := n.journal.file.Sync(); err != nil {
		return err
	}
	if err := n.before("original_intent_sync_returned"); err != nil {
		return err
	}
	o.mu.Lock()
	o.intentCommitted = true
	o.mu.Unlock()
	return nil
}

// Withdrawal is permanent before checking cancellation. No bootstrap byte may
// be sent until the root has ALSO withdrawn the actual service writer.
func (h *WorkerFreshFencePending) WithdrawOriginalWriter(ctx context.Context, prepared *WorkerPreparedFence) (resultErr error) {
	op, err := h.begin(ctx, "withdraw", prepared)
	if err != nil {
		return err
	}
	o := h.owner
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.state.before("original_launch_writer_permanently_withdrawn")
	normal = true
	return resultErr
}
func (n *workerFenceNative) validateIntent() error {
	o := n.owner
	if !o.intentCommitted || !o.writerWithdrawn || len(o.intent) == 0 || len(o.intent) > maxWorkerIntentBytes {
		return errOriginalWorkerFence
	}
	if err := n.before("original_committed_intent_stat"); err != nil {
		return err
	}
	var st unix.Stat_t
	if err := unix.Fstat(n.journal.number, &st); err != nil {
		return err
	}
	if st.Size != int64(len(o.intent)) {
		return errOriginalWorkerFence
	}
	o.verification = make([]byte, len(o.intent))
	if err := n.before("original_committed_intent_read"); err != nil {
		return err
	}
	read, err := n.journal.file.ReadAt(o.verification, 0)
	if err != nil {
		return err
	}
	if read != len(o.intent) || !bytes.Equal(o.intent, o.verification) {
		return errOriginalWorkerFence
	}
	return n.before("original_committed_intent_validated")
}
func (h *WorkerFreshFencePending) commandSelectors(bound bool) error {
	o := h.owner
	c := o.command
	if o.group.CheckOriginalCommand(c) != nil || c == nil || c.Process != nil || c.ProcessState != nil || c.Cancel != nil || c.WaitDelay != 0 || c.Stdin != nil || c.Dir != "/" || !reflect.DeepEqual(c.SysProcAttr, &syscall.SysProcAttr{Setsid: true}) {
		return errOriginalWorkerFence
	}
	if c.Path != o.image.Path || len(c.Args) != 2 || c.Args[0] != o.image.Path || c.Args[1] != workerMeshCoreRole || len(c.ExtraFiles) != 13 || len(o.extra) != 13 || &c.ExtraFiles[0] != &o.extra[0] {
		return errOriginalWorkerFence
	}
	for _, writer := range []any{c.Stdout, c.Stderr} {
		if writer != nil {
			if _, ok := writer.(*os.File); !ok {
				return errOriginalWorkerFence
			}
		}
	}
	expected := map[int]*os.File{4: o.state.launch.file, 5: o.state.journal.file, 9: o.state.home.file, 10: o.state.daemon.file, 11: o.state.directory.file, 12: o.state.ledger.file}
	for i, file := range c.ExtraFiles {
		if own, ok := expected[i]; ok {
			if bound && file != own || !bound && file != nil {
				return errOriginalWorkerFence
			}
		} else if bound || i < 4 {
			if file == nil {
				return errOriginalWorkerFence
			}
		} else if file != nil {
			return errOriginalWorkerFence
		}
	}
	return nil
}
func (h *WorkerFreshFencePending) BindOriginal(ctx context.Context, prepared *WorkerPreparedFence) (resultErr error) {
	op, err := h.begin(ctx, "bind", prepared)
	if err != nil {
		return err
	}
	o := h.owner
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.state.validate()
	if resultErr == nil {
		resultErr = h.commandSelectors(false)
	}
	if resultErr == nil {
		resultErr = o.state.before("original_nonclosing_six_reference_binding")
	}
	if resultErr == nil {
		// SAME original objects; command aliases never obtain reference-close authority.
		for slot, file := range map[int]*os.File{4: o.state.launch.file, 5: o.state.journal.file, 9: o.state.home.file, 10: o.state.daemon.file, 11: o.state.directory.file, 12: o.state.ledger.file} {
			o.command.ExtraFiles[slot] = file
		}
		o.mu.Lock()
		o.bound = true
		o.mu.Unlock()
	}
	normal = true
	return resultErr
}
func (h *WorkerFreshFencePending) ValidateOriginalBeforeStart(ctx context.Context, prepared *WorkerPreparedFence, command *exec.Cmd) (resultErr error) {
	if h == nil || h.owner == nil || h.owner.self != h || command != h.owner.command {
		return errOriginalWorkerFence
	}
	op, err := h.begin(ctx, "validate_before_start", prepared)
	if err != nil {
		return err
	}
	o := h.owner
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.state.validate()
	if resultErr == nil {
		resultErr = o.state.validateIntent()
	}
	if resultErr == nil {
		resultErr = h.commandSelectors(true)
	}
	normal = true
	return resultErr
}
