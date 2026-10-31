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

// DATA from this actual completed original operation, never writer/Root authority.
type P1RootBoundData struct {
	Record                     RootBoundRecord
	ProfileSHA256, ImageSHA256 string
	JournalBytes               int64
	JournalSHA256              string
	HomeID                     NativeID
}
type p1RootBoundIO struct {
	offset              int64
	requested, returned int
	returnedKnown       bool
	err                 error
}
type p1RootBoundCall struct {
	owner                               *owner
	op                                  *operation
	prepared                            *p1PreparedServiceCall
	custody                             *residentrefs.ServiceOriginal
	image                               *mesh.RuntimeExecutable
	charge                              *ManagerCharge
	record                              RootBoundRecord
	before                              os.FileInfo
	current                             residentrefs.ServiceIdentities
	identityAttempted, identityReturned bool
	identityError                       error
	statAttempted, statReturned         bool
	statError                           error
	append                              *appendOperation
	writes                              [32]p1RootBoundIO
	data                                P1RootBoundData
	syncReturned                        bool
	syncError                           error
	returned                            bool
	resultError                         error
}

// Only the private Root calls this after its OWN complete successful prepared
// operation; the original service independently checks its same completed call,
// custody/image/charge and immutable birth2. No supplied success/Ready callback.
func (c *ChildPending) CommitP1RootBoundOriginal(ctx context.Context, custody *residentrefs.ServiceOriginal, image *mesh.RuntimeExecutable, charge *ManagerCharge, generation, commitmentSHA string) (resultErr error) {
	if c == nil || c.owner == nil || c.owner.child != c || ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	o := c.owner
	o.mu.Lock()
	p := o.p1PreparedValidation
	valid := o.active == nil && o.firstError == nil && !o.p1RootBoundTried && p != nil && p.owner == o && p.op != nil && p.op.owner == o && p.op.kind == "p1-prepared-validation" && !p.op.abnormal && p.returned && p.resultError == nil && custody != nil && custody == o.p1Child && custody == p.custody && image != nil && image == o.image && image == p.image && charge != nil && charge == o.charge && charge == p.charge && o.debit != nil && o.debit.validateOriginalLocked(o) == nil && o.append == nil && len(o.reads) == 1 && p.birth != nil && p.birth == o.reads[0] && p.birth.finished && p.birth.firstError == nil && p.birth.after != nil && p.data.BirthJournalBytes == o.journal.bytes && p.data.BirthJournalSHA256 == hex.EncodeToString(o.journal.last[:])
	if !valid {
		o.mu.Unlock()
		return ErrInvalid
	}
	o.p1RootBoundTried = true
	op := &operation{owner: o, kind: "p1-root-bound", phase: "registered_before_original_service3_encoding"}
	call := &p1RootBoundCall{owner: o, op: op, prepared: p, custody: custody, image: image, charge: charge, record: RootBoundRecord{DebitID: o.debit.record.ID, LaunchKey: o.debit.record.LaunchKey, Generation: generation, CommitmentSHA256: commitmentSHA}}
	o.p1RootBound, o.active = call, op
	o.mu.Unlock()
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.commitP1RootBoundBody(ctx, call)
	o.mu.Lock()
	call.returned, call.resultError = true, resultErr
	o.mu.Unlock()
	normal = true
	return resultErr
}
func (o *owner) commitP1RootBoundBody(ctx context.Context, call *p1RootBoundCall) error {
	frame, err := EncodeP1RootBoundFrameData(call.record, hex.EncodeToString(o.journal.last[:]))
	if err != nil {
		return err
	}
	candidate := append(append([]Record(nil), o.journal.records...), Record{Schema: 1, Sequence: 3, Kind: "root-bound", Root: &call.record})
	if err = ValidateLedger(candidate); err != nil {
		return ErrInvalid
	}
	journalBytes, err := P1RootBoundJournalBytesData(o.journal.bytes, len(frame))
	if err != nil {
		return err
	}
	call.append = &appendOperation{owner: o, frame: frame, offset: o.journal.bytes} // before metadata/write effects
	call.identityAttempted = true
	call.current, call.identityError = call.custody.ValidateOriginal(ctx)
	call.identityReturned = true
	if call.identityError != nil {
		return call.identityError
	}
	if call.current != o.p1IDs || call.current != call.prepared.data.Identities {
		return ErrInvalid
	}
	call.statAttempted = true
	call.before, call.statError = o.p1Journal.StatOriginal(ctx)
	call.statReturned = true
	if call.statError != nil {
		return call.statError
	}
	birth := call.prepared.birth.after
	if !os.SameFile(birth, call.before) || birth.Size() != call.before.Size() || !birth.ModTime().Equal(call.before.ModTime()) || call.before.Size() != call.append.offset {
		return ErrUnresolved
	}
	a := call.append
	for a.written < len(a.frame) {
		if err = ctx.Err(); err != nil {
			a.firstError = err
			return err
		}
		if a.writes >= uint32(len(call.writes)) {
			a.firstError = io.ErrShortWrite
			return a.firstError
		}
		ioCall := &call.writes[a.writes]
		ioCall.offset, ioCall.requested = a.offset+int64(a.written), len(a.frame)-a.written
		a.writes++ // exact original call registered BEFORE invoking its view
		n, writeErr := o.p1Journal.WriteAtOriginal(ctx, a.frame[a.written:], ioCall.offset)
		ioCall.returned, ioCall.err, ioCall.returnedKnown = n, writeErr, true
		if n < 0 || n > ioCall.requested {
			a.firstError = ErrInvalid
			return ErrInvalid
		}
		a.written += n
		if writeErr != nil {
			a.firstError = writeErr
			return writeErr
		}
		if n == 0 {
			a.firstError = io.ErrNoProgress
			return a.firstError
		}
	}
	a.syncTried = true
	call.syncError = o.p1Journal.SyncOriginal(ctx)
	call.syncReturned = true
	if call.syncError != nil {
		a.firstError = call.syncError
		return call.syncError
	}
	a.committed = true // known actual full write+sync, even if later cancellation
	h := sha256.Sum256(a.frame)
	call.data = P1RootBoundData{Record: call.record, ProfileSHA256: call.prepared.data.ProfileSHA256, ImageSHA256: call.prepared.data.Build.SHA256, JournalBytes: journalBytes, JournalSHA256: hex.EncodeToString(h[:]), HomeID: call.prepared.data.HomeID}
	// Original o.journal/read1/debit birth2 is deliberately NEVER overwritten.
	return ctx.Err()
}

// Pure selector on the SAME actual completed service3 call. A copied child or
// serialized current3 record cannot manufacture this operation or a journal view.
func (c *ChildPending) P1RootBoundOriginalData() (P1RootBoundData, error) {
	if c == nil || c.owner == nil || c.owner.child != c {
		return P1RootBoundData{}, ErrInvalid
	}
	o := c.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	call := o.p1RootBound
	if o.active != nil || o.firstError != nil || call == nil || call.owner != o || call.op == nil || call.op.owner != o || call.op.kind != "p1-root-bound" || call.op.abnormal || !call.returned || call.resultError != nil || call.prepared != o.p1PreparedValidation || call.custody != o.p1Child || call.image != o.image || call.charge != o.charge || call.append == nil || call.append.owner != o || !call.append.committed || !call.append.syncTried || !call.syncReturned || call.syncError != nil || call.append.firstError != nil || call.append.written != len(call.append.frame) || o.debit == nil || o.debit.validateOriginalLocked(o) != nil {
		return P1RootBoundData{}, ErrInvalid
	}
	return call.data, nil
}
