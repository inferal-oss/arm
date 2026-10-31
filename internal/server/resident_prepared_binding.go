package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/nativeowner"
	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
	"getarm.org/internal/server/lifetimeview"
)

type residentRootPreparedCall struct {
	owner                             *residentChildPending
	op                                *residentChildOperation
	native                            *nativeowner.ServeIssuer
	manager                           *manager.ResidentManagerPending
	server                            *residentServerPending
	service                           *residentservice.ChildPending
	fence                             *residentbirth.InheritedFencePending
	image                             *mesh.InheritedImagePending
	runtime                           *mesh.RuntimeExecutable
	managerView                       *lifetimeview.ManagerView
	serverView                        *lifetimeview.ServerView
	charge                            *residentservice.ManagerCharge
	nativeAttempted, nativeReturned   bool
	nativeError                       error
	nativeData                        lifetimeview.Snapshot
	serviceAttempted, serviceReturned bool
	serviceError                      error
	serviceData                       residentservice.P1PreparedServiceData
	managerAttempted, managerReturned bool
	managerError                      error
	managerData                       manager.ResidentPreparedManagerData
	serverAttempted, serverReturned   bool
	serverError                       error
	serverData                        residentPreparedServerData
	layoutAttempted, layoutReturned   bool
	layoutError                       error
	layout                            residentrefs.ComposedIdentities
	intent                            []byte
	payload                           residentPreparedCommitmentData
	canonical                         []byte
	commitmentSHA256                  string
	returned                          bool
	resultError                       error
}

// Canonical DATA only. Future service3/footer hashes are deliberately absent:
// original intent ->prepared commitment ->service3 ->footer ->ACK is noncircular.
// There is no constructor from this record to Root/native/readiness authority.
type residentPreparedCommitmentData struct {
	Schema, Domain                    string
	Generation, Home                  string
	UID                               uint32
	Device, Inode                     uint64
	Layout                            residentrefs.ComposedIdentities
	SettingsSHA256, WorkspaceFormat   string
	DebitID, LaunchKey, ProfileSHA256 string
	EnvironmentSHA256, IntentSHA256   string
	ImageSHA256                       string
	ImageBytes                        int64
	BirthJournalSHA256                string
	BirthJournalBytes                 int64
}

// Genuine original Root only, prepared/unstarted. Registers the actual fixed
// owners and call BEFORE checks; no supplied DTO, snapshot, bool or callback.
// Success derives internal DATA; it sends/writes nothing and starts no loops.
func (r *residentChildPending) validateOriginalPreparedBindings() (resultErr error) {
	if r == nil || r.self != r || r.rootContext == nil || !r.entry.ownsPreparingRoot(r) {
		return errResidentChildOriginal
	}
	r.mu.Lock()
	if r.active != nil || r.firstError != nil || !r.bindingsPrepared || r.preparedValidationTried || r.native == nil || r.manager == nil || r.server == nil || r.service == nil || r.fence == nil || r.image == nil || r.runtime == nil || r.managerView == nil || r.serverView == nil || r.charge == nil || r.refs == nil {
		r.mu.Unlock()
		return errResidentChildOriginal
	}
	r.preparedValidationTried = true
	op := &residentChildOperation{kind: "validate_prepared", phase: "original_prepared_bindings_registered_before_native_checks"}
	call := &residentRootPreparedCall{owner: r, op: op, native: r.native, manager: r.manager, server: r.server, service: r.service, fence: r.fence, image: r.image, runtime: r.runtime, managerView: r.managerView, serverView: r.serverView, charge: r.charge}
	r.preparedValidation, r.active = call, op
	r.mu.Unlock()
	normal := false
	defer r.finish(op, &normal, &resultErr)
	resultErr = r.validatePreparedBody(call)
	r.mu.Lock()
	call.returned, call.resultError = true, resultErr
	r.mu.Unlock()
	normal = true
	return resultErr
}
func (r *residentChildPending) validatePreparedBody(call *residentRootPreparedCall) error {
	ctx := r.rootContext // independent original Root lifetime
	if err := ctx.Err(); err != nil {
		return err
	}
	r.phase("prepared_actual_issuer_fence_Manager_Server_views")
	call.nativeAttempted = true
	var err error
	call.nativeData, call.nativeError = call.native.ValidateInheritedOriginalViews(call.managerView, call.serverView, call.fence)
	call.nativeReturned = true
	if call.nativeError != nil {
		return call.nativeError
	}
	if !call.nativeData.InheritedFence || call.nativeData.Path != r.bootstrap.Service.Home {
		return errResidentChildOriginal
	}
	actualImage, err := call.image.RuntimeForOriginalRoot()
	if err != nil || actualImage != call.runtime {
		return errResidentChildOriginal
	}
	r.phase("prepared_actual_service_birth2_Env_A_and_charge")
	call.serviceAttempted = true
	call.serviceData, call.serviceError = call.service.ValidateP1PreparedOriginal(ctx, r.serviceCustody, call.runtime, call.charge)
	call.serviceReturned = true
	if call.serviceError != nil {
		return call.serviceError
	}
	r.phase("prepared_actual_Manager_official_DB_schema_and_settings")
	call.managerAttempted = true
	call.managerData, call.managerError = call.manager.ValidatePreparedOriginal(ctx, call.managerView, call.charge, call.server.managerBound)
	call.managerReturned = true
	if call.managerError != nil {
		return call.managerError
	}
	r.phase("prepared_actual_unstarted_Server_borrower")
	call.serverAttempted = true
	call.serverData, call.serverError = call.server.validatePreparedOriginal(ctx, call.serverView, call.manager)
	call.serverReturned = true
	if call.serverError != nil {
		return call.serverError
	}
	if call.managerData.Native != call.nativeData || call.serverData.Native != call.nativeData || call.managerData.SettingsSHA256 != r.bootstrap.SettingsSHA256 || call.serverData.SettingsSHA256 != call.managerData.SettingsSHA256 || call.serverData.WorkspaceFormat != call.managerData.WorkspaceFormat || call.managerData.WorkspaceFormat != "4" {
		return errResidentChildOriginal
	}
	settingsSHA, err := daemonconfig.SnapshotSHA256(r.bootstrap.Settings)
	if err != nil || settingsSHA != call.managerData.SettingsSHA256 {
		return errResidentChildOriginal
	}
	service := call.serviceData
	if service.Home != call.nativeData.Path || service.HomeID.Device != call.nativeData.Device || service.HomeID.Inode != call.nativeData.Inode || service.HomeID.UID != call.nativeData.UID || service.DebitID != r.bootstrap.Service.DebitID || service.LaunchKey != r.bootstrap.Service.LaunchKey || service.ProfileSHA256 != r.bootstrap.Service.ProfileSHA256 || service.Build != r.bootstrap.Service.Build || service.DebitID != call.managerData.DebitID || service.ProfileSHA256 != call.managerData.ProfileSHA256 {
		return errResidentChildOriginal
	}
	r.phase("prepared_actual_current_all13_layout_without_empty_ledger_check")
	call.layoutAttempted = true
	call.layout, call.layoutError = r.refs.ValidateComposedOriginal(ctx, service.DebitID)
	call.layoutReturned = true
	if call.layoutError != nil {
		return call.layoutError
	}
	if call.layout != r.composed || call.layout.Service != service.Identities || call.layout.Fence.Directory != call.nativeData.PreparedState || call.layout.Fence.Ledger != call.nativeData.PreparedLedger {
		return errResidentChildOriginal
	}
	if err = validateP1ComposedData(r.bootstrap, call.layout); err != nil {
		return err
	}
	call.intent, err = call.fence.IntentOriginalData() // retained original read1; no reread
	if err != nil {
		return err
	}
	expected, err := encodeP1IntentData(r.bootstrap)
	if err != nil || !bytes.Equal(expected, call.intent) {
		return errResidentChildOriginal
	}
	intentSHA := sha256.Sum256(call.intent)
	call.payload = residentPreparedCommitmentData{Schema: "1", Domain: "arm-resident-prepared-binding-v1", Generation: call.nativeData.Generation, Home: call.nativeData.Path, UID: call.nativeData.UID, Device: call.nativeData.Device, Inode: call.nativeData.Inode, Layout: call.layout, SettingsSHA256: settingsSHA, WorkspaceFormat: call.managerData.WorkspaceFormat, DebitID: service.DebitID, LaunchKey: service.LaunchKey, ProfileSHA256: service.ProfileSHA256, EnvironmentSHA256: service.EnvironmentSHA256, IntentSHA256: hex.EncodeToString(intentSHA[:]), ImageSHA256: service.Build.SHA256, ImageBytes: service.Build.Size, BirthJournalSHA256: service.BirthJournalSHA256, BirthJournalBytes: service.BirthJournalBytes}
	call.canonical, call.commitmentSHA256, err = encodePreparedCommitmentData(call.payload)
	if err != nil {
		return err
	}
	return ctx.Err()
}
func encodePreparedCommitmentData(data residentPreparedCommitmentData) ([]byte, string, error) {
	if data.Schema != "1" || data.Domain != "arm-resident-prepared-binding-v1" || len(data.Generation) != 32 || len(data.Home) == 0 || len(data.Home) > 4096 || !filepath.IsAbs(data.Home) || filepath.Clean(data.Home) != data.Home || data.Home == "/" || strings.ContainsRune(data.Home, 0) || data.WorkspaceFormat != "4" || len(data.LaunchKey) == 0 || len(data.LaunchKey) > 4096 || data.ImageBytes <= 0 || data.ImageBytes > mesh.MaxArtifactBytes || data.BirthJournalBytes <= 0 || data.BirthJournalBytes > residentservice.MaxJournalBytes {
		return nil, "", errResidentChildOriginal
	}
	if strings.ToLower(data.Generation) != data.Generation {
		return nil, "", errResidentChildOriginal
	}
	if _, err := hex.DecodeString(data.Generation); err != nil {
		return nil, "", errResidentChildOriginal
	}
	for _, value := range []string{data.SettingsSHA256, data.DebitID, data.ProfileSHA256, data.EnvironmentSHA256, data.IntentSHA256, data.ImageSHA256, data.BirthJournalSHA256} {
		if len(value) != 64 || strings.ToLower(value) != value {
			return nil, "", errResidentChildOriginal
		}
		if _, err := hex.DecodeString(value); err != nil {
			return nil, "", errResidentChildOriginal
		}
	}
	raw, err := json.Marshal(data)
	if err != nil || len(raw) > 32<<10 {
		return nil, "", errResidentChildOriginal
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}
