package residentservice

import (
	"encoding/hex"
	"getarm.org/internal/residentprotocol"
	"os/exec"
)

type WorkerRootBoundData struct {
	Record                                                                  WorkerRootBoundRecord
	ProfileSHA256, ImageSHA256, InitialPackageSHA256, InitialManifestSHA256 string
	JournalBytes                                                            int64
	JournalSHA256                                                           string
	HomeID                                                                  NativeID
}
type WorkerParentCurrentServiceData struct {
	Context   residentprotocol.WorkerContextData
	HomeID    NativeID
	RootBound WorkerRootBoundData
}

func (a *WorkerParentAckAuthentication) ValidateOriginalGroup(group *WorkerServiceGroup, command *exec.Cmd) (WorkerParentCurrentServiceData, error) {
	var result WorkerParentCurrentServiceData
	if a == nil || a.self != a || group == nil || a.group != group || group.parent == nil || group.parent.custody == nil || group.parent.custody.raw == nil {
		return result, ErrInvalid
	}
	w := group.parent.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if group.self != group || group.parent.group != group || !w.originalWorkerDebitLocked(group.debit) || !group.ackCompletedLocked() || group.ackAuthentication != a || group.retirement != nil || w.raw.active != nil || w.raw.firstError != nil || command == nil || command != group.command || command.Process == nil || command.Env == nil || !equalEnvironmentEntries(command.Env, group.environment) || !group.bound || !group.shortSealed || !group.writerWithdrawn || len(group.extra) != 13 || len(command.ExtraFiles) != 13 || &group.extra[0] != &command.ExtraFiles[0] || len(w.reads) != 4 || len(a.journal.records) != 3 || a.journal.records[2].Root == nil {
		return result, ErrInvalid
	}
	readKinds := [...]string{"worker-parent-open", "worker-parent-debit", "worker-group-before-start", "worker-parent-ack-auth"}
	for i, call := range w.reads {
		if call == nil || call.owner != w || !call.finished || call.firstError != nil || call.before == nil || call.after == nil || len(call.data) == 0 {
			return result, ErrInvalid
		}
		if call.kind != readKinds[i] {
			return result, ErrInvalid
		}
	}
	for i, entry := range []*retainedFD{w.raw.files.childChannel, w.raw.files.home, w.raw.files.lock, w.raw.files.journal} {
		if entry == nil || entry.file == nil || entry.closeTried || group.extra[i] != entry.file {
			return result, ErrInvalid
		}
	}
	for i := 0; i < 2; i++ {
		if !group.parentControlCompleteLocked(group.controlPhases[i], i) {
			return result, ErrInvalid
		}
	}
	result.Context = a.context
	result.HomeID = w.grant.Intent.HomeID
	result.RootBound = WorkerRootBoundData{Record: *a.journal.records[2].Root, ProfileSHA256: group.debit.record.ProfileSHA256, ImageSHA256: w.grant.Intent.Profile.Core.Build.SHA256, InitialPackageSHA256: group.debit.record.InitialPackageSHA256, InitialManifestSHA256: group.debit.record.InitialManifestSHA256, JournalBytes: a.journal.bytes, JournalSHA256: hex.EncodeToString(a.journal.last[:]), HomeID: result.HomeID}
	return result, nil
}

// This validates an actual original completed proof; it issues no closer.
func (proof *WorkerParentProofAuthentication) ValidateOriginalGroup(group *WorkerServiceGroup, command *exec.Cmd, ack *WorkerParentAckAuthentication) error {
	if proof == nil || proof.self != proof || group == nil || group.self != group || proof.group != group || group.parent == nil || group.parent.custody == nil || group.parent.custody.raw == nil {
		return ErrInvalid
	}
	w := group.parent.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if group.parent.group != group || !w.originalWorkerDebitLocked(group.debit) || group.proofAuthentication != proof || !group.proofCompletedLocked() || ack == nil || ack != group.ackAuthentication || group.challenge.ack != ack || command == nil || command != group.command || command.Process == nil || group.retirement != nil || w.raw.active != nil || w.raw.firstError != nil {
		return ErrInvalid
	}
	return nil
}
