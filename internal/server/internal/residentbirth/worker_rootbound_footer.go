package residentbirth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/residentservice"
)

type workerRootBoundFooterCall struct {
	owner                                                          *workerInheritedFenceState
	op                                                             *workerInheritedFenceOperation
	service                                                        *residentservice.WorkerChildPending
	serviceData                                                    residentservice.WorkerRootBoundData
	serviceSelectorError, serviceRecheckError                      error
	serviceRecheck                                                 residentservice.WorkerRootBoundData
	settingsSHA                                                    string
	data                                                           WorkerRootBoundFooterData
	frame                                                          []byte
	current                                                        residentrefs.FenceIdentities
	identityAttempted, identityReturned                            bool
	identityError                                                  error
	before, after                                                  os.FileInfo
	beforeAttempted, beforeReturned, afterAttempted, afterReturned bool
	beforeError, afterError                                        error
	prefixEOF                                                      []byte // distinct original read2, initial read1 remains immutable
	reads                                                          [32]workerFooterIO
	readCalls, readBytes                                           int
	eof                                                            bool
	writes                                                         [32]workerFooterIO
	writeCalls, written                                            int
	syncAttempted, syncReturned, committed                         bool
	syncError                                                      error
	result                                                         WorkerRootBoundFooterResultData
	returned                                                       bool
	resultError                                                    error
}

// Private Root must gate this call with its SAME successful prepared operation
// before effects. This owner derives current3 from the SAME actual service's
// completed original append/sync; no caller-supplied current3 DTO or bool enters.
func (p *WorkerInheritedFencePending) AppendWorkerRootBoundFooterOriginal(ctx context.Context, service *residentservice.WorkerChildPending, settingsSHA string) (resultErr error) {
	if service == nil || !footerDigestData(settingsSHA, sha256.Size) {
		return errOriginalFence
	}
	op, err := p.begin(ctx, "worker-rootbound-footer")
	if err != nil {
		return err
	}
	s := p.state
	call := &workerRootBoundFooterCall{owner: s, op: op, service: service, settingsSHA: settingsSHA}
	s.mu.Lock()
	s.footer = call
	s.mu.Unlock() // before service selector/encoding/native calls
	normal := false
	defer s.finish(op, &normal, &resultErr)
	resultErr = s.appendWorkerRootBoundFooterBody(call)
	s.mu.Lock()
	call.returned, call.resultError = true, resultErr
	s.mu.Unlock()
	normal = true
	return resultErr
}
func (s *workerInheritedFenceState) appendWorkerRootBoundFooterBody(call *workerRootBoundFooterCall) error {
	ctx := s.rootContext // original root lifetime, never caller/worker cancellation
	var err error
	call.serviceData, call.serviceSelectorError = call.service.WorkerRootBoundOriginalData()
	if call.serviceSelectorError != nil {
		return call.serviceSelectorError
	}
	home := s.expected.Identities.Home
	if call.serviceData.HomeID.Device != home.Device || call.serviceData.HomeID.Inode != home.Inode || call.serviceData.HomeID.UID != home.UID || call.serviceData.Record.ContractSHA256 != s.decoded.ContractSHA256 || call.serviceData.InitialPackageSHA256 != s.decoded.InitialPackageSHA256 || call.serviceData.InitialManifestSHA256 != s.decoded.InitialManifestSHA256 || call.settingsSHA != s.decoded.SettingsSHA256 || call.serviceData.Record.DebitID != s.decoded.ServiceDebitID || call.serviceData.Record.LaunchKey != s.decoded.LaunchKey {
		return errOriginalFence
	}
	call.data, err = WorkerFooterForServiceData(call.serviceData, s.expected.IntentSHA256, call.settingsSHA)
	if err != nil {
		return err
	}
	call.frame, err = EncodeWorkerRootBoundFooterData(call.data)
	if err != nil {
		return err
	}
	if _, err = WorkerLaunchJournalCapacityData(len(s.intent), len(call.frame)); err != nil {
		return err
	}
	call.identityAttempted = true
	call.current, call.identityError = s.custody.ValidateOriginal(ctx)
	call.identityReturned = true
	if call.identityError != nil {
		return call.identityError
	}
	if call.current != s.original {
		return errOriginalFence
	}
	call.beforeAttempted = true
	call.before, call.beforeError = s.journal.StatOriginal(ctx)
	call.beforeReturned = true
	if call.beforeError != nil {
		return call.beforeError
	}
	if call.before.Size() != int64(len(s.intent)) {
		return errOriginalFence
	}
	call.prefixEOF = make([]byte, len(s.intent)+1)
	for !call.eof {
		if err = ctx.Err(); err != nil {
			return err
		}
		if call.readCalls >= len(call.reads) || call.readBytes >= len(call.prefixEOF) {
			return io.ErrNoProgress
		}
		c := &call.reads[call.readCalls]
		c.offset, c.requested = int64(call.readBytes), len(call.prefixEOF)-call.readBytes
		call.readCalls++
		n, readErr := s.journal.ReadAtOriginal(ctx, call.prefixEOF[call.readBytes:], c.offset)
		c.returned, c.err, c.returnedKnown = n, readErr, true
		if n < 0 || n > c.requested {
			return errOriginalFence
		}
		call.readBytes += n
		if errors.Is(readErr, io.EOF) {
			call.eof = true
		} else if readErr != nil {
			return readErr
		} else if n == 0 {
			return io.ErrNoProgress
		}
	}
	if !validWorkerPrefixEOFData(s.intent, call.prefixEOF[:call.readBytes], call.eof) {
		return errOriginalFence
	}
	call.afterAttempted = true
	call.after, call.afterError = s.journal.StatOriginal(ctx)
	call.afterReturned = true
	if call.afterError != nil {
		return call.afterError
	}
	if !os.SameFile(call.before, call.after) || call.before.Size() != call.after.Size() || !call.before.ModTime().Equal(call.after.ModTime()) {
		return errOriginalFence
	}
	// Consult that SAME original completed service call again before first write.
	call.serviceRecheck, call.serviceRecheckError = call.service.WorkerRootBoundOriginalData()
	if call.serviceRecheckError != nil {
		return call.serviceRecheckError
	}
	if call.serviceRecheck != call.serviceData {
		return errOriginalFence
	}
	for call.written < len(call.frame) {
		if err = ctx.Err(); err != nil {
			return err
		}
		if call.writeCalls >= len(call.writes) {
			return io.ErrShortWrite
		}
		c := &call.writes[call.writeCalls]
		c.offset, c.requested = int64(len(s.intent)+call.written), len(call.frame)-call.written
		call.writeCalls++
		n, writeErr := s.journal.WriteAtOriginal(ctx, call.frame[call.written:], c.offset)
		c.returned, c.err, c.returnedKnown = n, writeErr, true
		if n < 0 || n > c.requested {
			return errOriginalFence
		}
		call.written += n
		if writeErr != nil {
			return writeErr
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	call.syncAttempted = true
	call.syncError = s.journal.SyncOriginal(ctx)
	call.syncReturned = true
	if call.syncError != nil {
		return call.syncError
	}
	call.committed = true
	footerSHA := sha256.Sum256(call.frame)
	h := sha256.New()
	h.Write(s.intent)
	h.Write(call.frame)
	call.result = WorkerRootBoundFooterResultData{Footer: call.data, FooterBytes: len(call.frame), JournalBytes: len(s.intent) + len(call.frame), FooterSHA256: hex.EncodeToString(footerSHA[:]), JournalSHA256: hex.EncodeToString(h.Sum(nil))}
	return ctx.Err()
}
func validWorkerPrefixEOFData(intent, current []byte, eof bool) bool {
	return len(intent) > 0 && len(intent) <= maxWorkerIntentBytes && eof && bytes.Equal(intent, current)
}
func (p *WorkerInheritedFencePending) WorkerRootBoundFooterOriginalData() (WorkerRootBoundFooterResultData, error) {
	if p == nil || p.state == nil || p.state.self != p {
		return WorkerRootBoundFooterResultData{}, errOriginalFence
	}
	s := p.state
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.footer
	if s.active != nil || s.firstError != nil || c == nil || c.owner != s || c.op == nil || c.op.kind != "worker-rootbound-footer" || !c.op.normalFinished || c.op.abnormal || !c.returned || c.resultError != nil || !c.committed || !c.syncAttempted || !c.syncReturned || c.syncError != nil || c.written != len(c.frame) || !c.eof {
		return WorkerRootBoundFooterResultData{}, errOriginalFence
	}
	return c.result, nil
}
