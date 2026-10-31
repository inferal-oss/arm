package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"

	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
)

type residentRootBoundCall struct {
	owner                             *residentChildPending
	op                                *residentChildOperation
	prepared                          *residentRootPreparedCall
	service                           *residentservice.ChildPending
	fence                             *residentbirth.InheritedFencePending
	serviceFrame                      []byte // actual encoding checked BEFORE either append
	expectedService                   residentservice.P1RootBoundData
	expectedFooter                    residentbirth.P1RootBoundFooterData
	footerFrame                       []byte
	serviceAttempted, serviceReturned bool
	serviceError                      error
	serviceData                       residentservice.P1RootBoundData
	serviceSelectorError              error
	footerAttempted, footerReturned   bool
	footerError                       error
	footerData                        residentbirth.P1RootBoundFooterResultData
	footerSelectorError               error
	returned                          bool
	resultError                       error
}

func (r *residentChildPending) preparedRootBoundLinkLocked(p *residentRootPreparedCall) bool {
	return p != nil && p == r.preparedValidation && p.owner == r && p.op != nil && p.op.kind == "validate_prepared" && !p.op.abnormal && p.returned && p.resultError == nil && p.nativeAttempted && p.nativeReturned && p.nativeError == nil && p.serviceAttempted && p.serviceReturned && p.serviceError == nil && p.managerAttempted && p.managerReturned && p.managerError == nil && p.serverAttempted && p.serverReturned && p.serverError == nil && p.layoutAttempted && p.layoutReturned && p.layoutError == nil && p.native == r.native && p.manager == r.manager && p.server == r.server && p.service == r.service && p.fence == r.fence && p.image == r.image && p.runtime == r.runtime && p.managerView == r.managerView && p.serverView == r.serverView && p.charge == r.charge && p.layout == r.composed && len(p.intent) > 0 && len(p.canonical) > 0 && p.commitmentSHA256 != ""
}
func (r *residentChildPending) beginOriginalRootBound() (*residentRootBoundCall, error) {
	if r == nil || r.self != r || r.rootContext == nil || !r.entry.ownsPreparingRoot(r) {
		return nil, errResidentChildOriginal
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil || r.firstError != nil || !r.bindingsPrepared || r.rootBoundTried || !r.preparedRootBoundLinkLocked(r.preparedValidation) {
		return nil, errResidentChildOriginal
	}
	r.rootBoundTried = true
	op := &residentChildOperation{kind: "root-bound", phase: "registered_before_RootBound_capacity_or_native_effects"}
	call := &residentRootBoundCall{owner: r, op: op, prepared: r.preparedValidation, service: r.service, fence: r.fence}
	r.rootBound, r.active = call, op
	return call, nil
}

// Rechecked structurally before BOTH service and footer effects. The only inputs
// are this original operation and its exact completed original prepared call.
func (r *residentChildPending) rootBoundGate(call *residentRootBoundCall) error {
	if r == nil || r.self != r || r.rootContext == nil || !r.entry.ownsPreparingRoot(r) {
		return errResidentChildOriginal
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if call == nil || call != r.rootBound || call.owner != r || call.op == nil || r.active != call.op || call.op.abnormal || r.firstError != nil || !r.bindingsPrepared || !r.preparedRootBoundLinkLocked(call.prepared) || call.service != r.service || call.fence != r.fence || call.serviceError != nil || call.footerError != nil {
		return errResidentChildOriginal
	}
	canonical, digest, err := encodePreparedCommitmentData(call.prepared.payload)
	if err != nil || digest != call.prepared.commitmentSHA256 || !bytes.Equal(canonical, call.prepared.canonical) {
		return errResidentChildOriginal
	}
	return r.rootContext.Err()
}
func (r *residentChildPending) commitOriginalRootBound() (resultErr error) {
	call, err := r.beginOriginalRootBound()
	if err != nil {
		return err
	}
	normal := false
	defer r.finish(call.op, &normal, &resultErr)
	resultErr = r.commitOriginalRootBoundBody(call)
	r.mu.Lock()
	call.returned, call.resultError = true, resultErr
	r.mu.Unlock()
	normal = true
	return resultErr
}
func (r *residentChildPending) commitOriginalRootBoundBody(call *residentRootBoundCall) error {
	if err := r.rootBoundGate(call); err != nil {
		return err
	}
	p := call.prepared
	record := residentservice.RootBoundRecord{DebitID: p.serviceData.DebitID, LaunchKey: p.serviceData.LaunchKey, Generation: p.nativeData.Generation, CommitmentSHA256: p.commitmentSHA256}
	var err error
	call.serviceFrame, err = residentservice.EncodeP1RootBoundFrameData(record, p.serviceData.BirthJournalSHA256)
	if err != nil {
		return err
	}
	serviceBytes, err := residentservice.P1RootBoundJournalBytesData(p.serviceData.BirthJournalBytes, len(call.serviceFrame))
	if err != nil {
		return err
	}
	h := sha256.Sum256(call.serviceFrame)
	call.expectedService = residentservice.P1RootBoundData{Record: record, ProfileSHA256: p.serviceData.ProfileSHA256, ImageSHA256: p.serviceData.Build.SHA256, HomeID: p.serviceData.HomeID, JournalBytes: serviceBytes, JournalSHA256: hex.EncodeToString(h[:])}
	call.expectedFooter, err = residentbirth.P1FooterForServiceData(call.expectedService, p.payload.IntentSHA256, p.payload.SettingsSHA256)
	if err != nil {
		return err
	}
	call.footerFrame, err = residentbirth.EncodeP1RootBoundFooterData(call.expectedFooter)
	if err != nil {
		return err
	}
	if _, err = residentbirth.P1LaunchJournalCapacityData(len(p.intent), len(call.footerFrame)); err != nil {
		return err
	}
	// Both actual encodings fit BEFORE the first RootBound append.
	if err = r.rootBoundGate(call); err != nil {
		return err
	}
	r.phase("actual_original_service3_append_sync_after_prepared_binding")
	call.serviceAttempted = true
	call.serviceError = call.service.CommitP1RootBoundOriginal(r.rootContext, r.serviceCustody, p.runtime, p.charge, record.Generation, record.CommitmentSHA256)
	call.serviceReturned = true
	if call.serviceError != nil {
		return call.serviceError
	}
	call.serviceData, call.serviceSelectorError = call.service.P1RootBoundOriginalData()
	if call.serviceSelectorError != nil {
		return call.serviceSelectorError
	}
	if call.serviceData != call.expectedService {
		return errResidentChildOriginal
	}
	if err = r.rootBoundGate(call); err != nil {
		return err
	}
	r.phase("actual_same_inherited_intent_prefix_EOF_then_footer_append_sync")
	call.footerAttempted = true
	call.footerError = call.fence.AppendP1RootBoundFooterOriginal(r.rootContext, call.service, p.payload.SettingsSHA256)
	call.footerReturned = true
	if call.footerError != nil {
		return call.footerError
	}
	call.footerData, call.footerSelectorError = call.fence.P1RootBoundFooterOriginalData()
	if call.footerSelectorError != nil {
		return call.footerSelectorError
	}
	if call.footerData.Footer != call.expectedFooter || call.footerData.FooterBytes != len(call.footerFrame) || call.footerData.JournalBytes != len(p.intent)+len(call.footerFrame) {
		return errResidentChildOriginal
	}
	f := sha256.Sum256(call.footerFrame)
	journal := sha256.New()
	journal.Write(p.intent)
	journal.Write(call.footerFrame)
	if call.footerData.FooterSHA256 != hex.EncodeToString(f[:]) || call.footerData.JournalSHA256 != hex.EncodeToString(journal.Sum(nil)) {
		return errResidentChildOriginal
	}
	return r.rootContext.Err() // no ACK/readiness/Manager loop/reference retirement
}
