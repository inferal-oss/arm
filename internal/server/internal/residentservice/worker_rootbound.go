package residentservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
)

type workerChildRootIO struct {
	offset                    int64
	requested, returned       int
	attempted, normalReturned bool
	err                       error
}
type workerChildRootBoundCall struct {
	child                       *WorkerChildPending
	op                          *operation
	ctx                         context.Context
	prepared                    *workerPreparedServiceCall
	custody                     *residentrefs.ServiceOriginal
	runtime                     *mesh.WorkerRuntimeView
	charge                      *WorkerManagerCharge
	record                      WorkerRootBoundRecord
	frame                       []byte
	append                      *workerNativeAppend
	current                     residentrefs.ServiceIdentities
	before                      os.FileInfo
	writes                      [32]workerChildRootIO
	syncAttempted, syncReturned bool
	syncError                   error
	data                        WorkerRootBoundData
	normalReturned              bool
	resultError                 error
}

// DATA encoding only. Complete original prepared owners still gate real writes.
func EncodeWorkerRootBoundFrameData(record WorkerRootBoundRecord, previous string) ([]byte, error) {
	if !validDigest(previous) || !validDigest(record.DebitID) || !boundedText(record.LaunchKey, 128) || !validWorkerGenerationData(record.Generation) || !validDigest(record.CommitmentSHA256) || !validDigest(record.ContractSHA256) {
		return nil, ErrInvalid
	}
	b, err := hex.DecodeString(previous)
	if err != nil {
		return nil, ErrInvalid
	}
	var prior [sha256.Size]byte
	copy(prior[:], b)
	return encodeWorkerRecordFrameData(WorkerRecordData{Schema: 3, Sequence: 3, Kind: "worker-root-bound", Root: &record}, prior)
}
func WorkerRootBoundJournalBytesData(birth int64, frame int) (int64, error) {
	if birth <= 0 || birth > workerJournalMaxBytes || frame <= 0 || frame > workerJournalFrameWireBytes || int64(frame) > workerJournalMaxBytes-birth {
		return 0, ErrInvalid
	}
	return birth + int64(frame), nil
}

func (p *WorkerChildPending) CommitWorkerRootBoundOriginal(ctx context.Context, custody *residentrefs.ServiceOriginal, image *mesh.WorkerRuntimeView, charge *WorkerManagerCharge, generation, commitment string) (resultErr error) {
	if p == nil || p.self != p || p.custody == nil || p.custody.raw == nil || !sameWorkerContextData(ctx, p.rootContext) {
		return ErrInvalid
	}
	op, err := p.beginOriginal("worker-child-root-bound")
	if err != nil {
		return err
	}
	w := p.custody
	c := &workerChildRootBoundCall{child: p, op: op, ctx: ctx, custody: custody, runtime: image, charge: charge}
	w.raw.mu.Lock()
	p.rootBound = c
	c.prepared = p.preparedValidation
	if w.debit != nil && w.debit.record != nil {
		c.record = WorkerRootBoundRecord{DebitID: w.debit.record.ID, LaunchKey: w.debit.record.LaunchKey, Generation: generation, CommitmentSHA256: commitment, ContractSHA256: w.debit.record.ContractSHA256}
	}
	valid := p.preparedOriginalLocked(c.prepared) && c.prepared.op.normalFinished && c.prepared.normalReturned && c.prepared.resultError == nil && c.prepared.custody == custody && c.prepared.runtime == image && c.prepared.charge == charge
	w.raw.mu.Unlock()
	normal := false
	defer func() {
		w.raw.mu.Lock()
		c.normalReturned = normal && op.normalFinished && !op.abnormal
		c.resultError = resultErr
		w.raw.mu.Unlock()
	}()
	defer w.raw.finish(op, &normal, &resultErr)
	if !valid {
		resultErr = ErrInvalid
	} else {
		resultErr = p.commitWorkerRootBoundBody(c)
	}
	normal = true
	return resultErr
}

func (p *WorkerChildPending) commitWorkerRootBoundBody(c *workerChildRootBoundCall) error {
	w := p.custody
	var err error
	c.frame, err = EncodeWorkerRootBoundFrameData(c.record, c.prepared.data.BirthJournalSHA256)
	if err != nil {
		return err
	}
	bytes, err := WorkerRootBoundJournalBytesData(c.prepared.data.BirthJournalBytes, len(c.frame))
	if err != nil {
		return err
	}
	candidate := append(append([]WorkerRecordData(nil), w.journal.records...), WorkerRecordData{Schema: 3, Sequence: 3, Kind: "worker-root-bound", Root: &c.record})
	if ValidateWorkerLedgerData(candidate) != nil {
		return ErrInvalid
	}
	c.append = &workerNativeAppend{owner: w, frame: c.frame, offset: c.prepared.data.BirthJournalBytes}
	if err = p.currentBodyOriginal(c.ctx, true); err != nil {
		return err
	}
	c.current, err = c.custody.ValidateOriginal(c.ctx)
	if err != nil {
		return err
	}
	if c.current != c.prepared.data.Identities || c.current != p.identities {
		return ErrInvalid
	}
	c.before, err = p.journal.StatOriginal(c.ctx)
	if err != nil {
		return err
	}
	before := c.prepared.read.after
	if !os.SameFile(before, c.before) || before.Size() != c.before.Size() || !before.ModTime().Equal(c.before.ModTime()) || c.before.Size() != c.append.offset {
		return ErrUnresolved
	}
	a := c.append
	for a.written < len(a.frame) {
		if err = c.ctx.Err(); err != nil {
			a.firstError = err
			return err
		}
		if a.calls >= len(c.writes) {
			a.firstError = io.ErrShortWrite
			return a.firstError
		}
		call := &c.writes[a.calls]
		a.calls++
		call.offset, call.requested = a.offset+int64(a.written), len(a.frame)-a.written
		call.attempted = true
		call.returned, call.err = p.journal.WriteAtOriginal(c.ctx, a.frame[a.written:], call.offset)
		call.normalReturned = true
		if call.returned < 0 || call.returned > call.requested {
			a.firstError = ErrInvalid
			return ErrInvalid
		}
		a.written += call.returned
		if call.err != nil {
			a.firstError = call.err
			return call.err
		}
		if call.returned == 0 {
			a.firstError = io.ErrNoProgress
			return a.firstError
		}
	}
	a.syncTried, c.syncAttempted = true, true
	c.syncError = p.journal.SyncOriginal(c.ctx)
	c.syncReturned = true
	if c.syncError != nil {
		a.firstError = c.syncError
		return c.syncError
	}
	a.committed = true
	h := sha256.Sum256(c.frame)
	c.data = WorkerRootBoundData{Record: c.record, ProfileSHA256: c.prepared.data.ProfileSHA256, ImageSHA256: c.prepared.data.Build.SHA256, InitialPackageSHA256: c.prepared.data.InitialPackageSHA256, InitialManifestSHA256: c.prepared.data.InitialManifestSHA256, JournalBytes: bytes, JournalSHA256: hex.EncodeToString(h[:]), HomeID: c.prepared.data.HomeID}
	// Keep immutable w.journal/read1 birth2 unchanged; this separate original
	// append and later current read2 retain the actual service3 byte lineage.
	return c.ctx.Err()
}

func (p *WorkerChildPending) completedWorkerRootBoundLocked() bool {
	if p == nil || p.self != p || p.custody == nil || p.custody.raw == nil {
		return false
	}
	w := p.custody
	c := p.rootBound
	return c != nil && c.child == p && c.op != nil && c.op.owner == w.raw && c.op.kind == "worker-child-root-bound" && c.op.normalFinished && !c.op.abnormal && c.normalReturned && c.resultError == nil && p.preparedOriginalLocked(c.prepared) && c.prepared.normalReturned && c.prepared.resultError == nil && c.prepared.op.normalFinished && c.custody == p.source && c.runtime == p.runtime && c.charge == p.charge && c.append != nil && c.append.owner == w && c.append.committed && c.append.syncTried && c.append.firstError == nil && c.append.written == len(c.append.frame) && c.syncAttempted && c.syncReturned && c.syncError == nil
}
func (p *WorkerChildPending) WorkerRootBoundOriginalData() (WorkerRootBoundData, error) {
	if p == nil || p.self != p || p.custody == nil || p.custody.raw == nil {
		return WorkerRootBoundData{}, ErrInvalid
	}
	w := p.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if w.raw.active != nil || w.raw.firstError != nil || !p.completedWorkerRootBoundLocked() {
		return WorkerRootBoundData{}, ErrInvalid
	}
	return p.rootBound.data, nil
}
