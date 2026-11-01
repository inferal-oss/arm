package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"

	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
)

type residentWorkerRootBoundCall struct {
	owner                             *residentWorkerChildPending
	op                                *residentWorkerChildOperation
	prepared                          *residentWorkerRootPreparedCall
	service                           *residentservice.WorkerChildPending
	fence                             *residentbirth.WorkerInheritedFencePending
	serviceFrame                      []byte // actual encoding checked BEFORE either append
	expectedService                   residentservice.WorkerRootBoundData
	expectedFooter                    residentbirth.WorkerRootBoundFooterData
	footerFrame                       []byte
	serviceAttempted, serviceReturned bool
	serviceError                      error
	serviceData                       residentservice.WorkerRootBoundData
	serviceSelectorError              error
	footerAttempted, footerReturned   bool
	footerError                       error
	footerData                        residentbirth.WorkerRootBoundFooterResultData
	footerSelectorError               error
	returned                          bool
	resultError                       error
}

func (r *residentWorkerChildPending) preparedRootBoundLinkLocked(p *residentWorkerRootPreparedCall) bool {
	return p != nil && p == r.preparedValidation && p.owner == r && p.op != nil && p.op.kind == "validate_prepared" && !p.op.abnormal && p.op.normalFinished && p.op.finalError == nil && p.returned && p.resultError == nil && p.nativeAttempted && p.nativeReturned && p.nativeError == nil && p.runtimeOwnerAttempted && p.runtimeOwnerReturned && p.runtimeOwnerError == nil && p.runtimeAttempted && p.runtimeReturned && p.runtimeError == nil && p.serviceAttempted && p.serviceReturned && p.serviceError == nil && p.managerAttempted && p.managerReturned && p.managerError == nil && p.serverAttempted && p.serverReturned && p.serverError == nil && p.layoutAttempted && p.layoutReturned && p.layoutError == nil && p.native == r.native && p.manager == r.manager && p.server == r.server && p.service == r.service && p.fence == r.fence && p.image == r.image && p.runtime == r.runtime && p.managerView == r.managerView && p.serverView == r.serverView && p.charge == r.charge && p.layout == r.composed && len(p.intent) > 0 && len(p.canonical) > 0 && p.commitmentSHA256 != ""
}
func (r *residentWorkerChildPending) beginOriginalRootBound() (*residentWorkerRootBoundCall, error) {
	if r == nil || r.self != r || r.rootContext == nil || !r.entry.ownsPreparingRoot(r) {
		return nil, errResidentWorkerChildOriginal
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil || r.firstError != nil || !r.bindingsPrepared || r.rootBoundTried || !r.preparedRootBoundLinkLocked(r.preparedValidation) {
		return nil, errResidentWorkerChildOriginal
	}
	r.rootBoundTried = true
	op := &residentWorkerChildOperation{kind: "root-bound", phase: "registered_before_RootBound_capacity_or_native_effects"}
	call := &residentWorkerRootBoundCall{owner: r, op: op, prepared: r.preparedValidation, service: r.service, fence: r.fence}
	r.rootBound, r.active = call, op
	return call, nil
}

// Rechecked structurally before BOTH service and footer effects. The only inputs
// are this original operation and its exact completed original prepared call.
func (r *residentWorkerChildPending) rootBoundGate(call *residentWorkerRootBoundCall) error {
	if r == nil || r.self != r || r.rootContext == nil || !r.entry.ownsPreparingRoot(r) {
		return errResidentWorkerChildOriginal
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if call == nil || call != r.rootBound || call.owner != r || call.op == nil || r.active != call.op || call.op.abnormal || r.firstError != nil || !r.bindingsPrepared || !r.preparedRootBoundLinkLocked(call.prepared) || call.service != r.service || call.fence != r.fence || call.serviceError != nil || call.footerError != nil {
		return errResidentWorkerChildOriginal
	}
	canonical, digest, err := encodeWorkerPreparedCommitmentData(call.prepared.payload)
	if err != nil || digest != call.prepared.commitmentSHA256 || !bytes.Equal(canonical, call.prepared.canonical) {
		return errResidentWorkerChildOriginal
	}
	return r.rootContext.Err()
}
func (r *residentWorkerChildPending) commitOriginalRootBound() (resultErr error) {
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
func (r *residentWorkerChildPending) commitOriginalRootBoundBody(call *residentWorkerRootBoundCall) error {
	if err := r.rootBoundGate(call); err != nil {
		return err
	}
	p := call.prepared
	record := residentservice.WorkerRootBoundRecord{DebitID: p.serviceData.DebitID, LaunchKey: p.serviceData.LaunchKey, Generation: p.nativeData.Generation, CommitmentSHA256: p.commitmentSHA256, ContractSHA256: p.serviceData.ContractSHA256}
	var err error
	call.serviceFrame, err = residentservice.EncodeWorkerRootBoundFrameData(record, p.serviceData.BirthJournalSHA256)
	if err != nil {
		return err
	}
	call.expectedService, err = workerExpectedRootBoundServiceData(p.serviceData, p.payload, record, call.serviceFrame)
	if err != nil {
		return err
	}
	call.expectedFooter, err = residentbirth.WorkerFooterForServiceData(call.expectedService, p.payload.IntentSHA256, p.payload.SettingsSHA256)
	if err != nil {
		return err
	}
	call.footerFrame, err = residentbirth.EncodeWorkerRootBoundFooterData(call.expectedFooter)
	if err != nil {
		return err
	}
	if _, err = residentbirth.WorkerLaunchJournalCapacityData(len(p.intent), len(call.footerFrame)); err != nil {
		return err
	}
	// Both actual encodings fit BEFORE the first RootBound append.
	if err = r.rootBoundGate(call); err != nil {
		return err
	}
	r.phase("actual_original_service3_append_sync_after_prepared_binding")
	call.serviceAttempted = true
	call.serviceError = call.service.CommitWorkerRootBoundOriginal(r.rootContext, r.serviceCustody, p.runtime, p.charge, record.Generation, record.CommitmentSHA256)
	call.serviceReturned = true
	if call.serviceError != nil {
		return call.serviceError
	}
	call.serviceData, call.serviceSelectorError = call.service.WorkerRootBoundOriginalData()
	if call.serviceSelectorError != nil {
		return call.serviceSelectorError
	}
	if call.serviceData != call.expectedService {
		return errResidentWorkerChildOriginal
	}
	if err = r.rootBoundGate(call); err != nil {
		return err
	}
	r.phase("actual_same_inherited_intent_prefix_EOF_then_footer_append_sync")
	call.footerAttempted = true
	call.footerError = call.fence.AppendWorkerRootBoundFooterOriginal(r.rootContext, call.service, p.payload.SettingsSHA256)
	call.footerReturned = true
	if call.footerError != nil {
		return call.footerError
	}
	call.footerData, call.footerSelectorError = call.fence.WorkerRootBoundFooterOriginalData()
	if call.footerSelectorError != nil {
		return call.footerSelectorError
	}
	if call.footerData.Footer != call.expectedFooter || call.footerData.FooterBytes != len(call.footerFrame) || call.footerData.JournalBytes != len(p.intent)+len(call.footerFrame) {
		return errResidentWorkerChildOriginal
	}
	f := sha256.Sum256(call.footerFrame)
	journal := sha256.New()
	journal.Write(p.intent)
	journal.Write(call.footerFrame)
	if call.footerData.FooterSHA256 != hex.EncodeToString(f[:]) || call.footerData.JournalSHA256 != hex.EncodeToString(journal.Sum(nil)) {
		return errResidentWorkerChildOriginal
	}
	return r.rootContext.Err() // no ACK/readiness/Manager loop/reference retirement
}

// Expected pre-append DATA from the exact original prepared snapshots. This
// constructs no service/fence owner and cannot authorize native effects. The
// root's original gate separately validates the complete prepared commitment.
func workerExpectedRootBoundServiceData(service residentservice.WorkerPreparedServiceData, commitment residentWorkerPreparedCommitmentData, record residentservice.WorkerRootBoundRecord, frame []byte) (residentservice.WorkerRootBoundData, error) {
	if service.InitialPackageSHA256 != commitment.InitialPackageSHA256 || service.InitialManifestSHA256 != commitment.InitialManifestSHA256 {
		return residentservice.WorkerRootBoundData{}, errResidentWorkerChildOriginal
	}
	bytes, err := residentservice.WorkerRootBoundJournalBytesData(service.BirthJournalBytes, len(frame))
	if err != nil {
		return residentservice.WorkerRootBoundData{}, err
	}
	hash := sha256.Sum256(frame)
	return residentservice.WorkerRootBoundData{Record: record, ProfileSHA256: service.ProfileSHA256, ImageSHA256: service.Build.SHA256, InitialPackageSHA256: service.InitialPackageSHA256, InitialManifestSHA256: service.InitialManifestSHA256, HomeID: service.HomeID, JournalBytes: bytes, JournalSHA256: hex.EncodeToString(hash[:])}, nil
}
