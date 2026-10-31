package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sync"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/nativeowner"
	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
	"getarm.org/internal/server/lifetimeview"
)

var errResidentChildOriginal = errors.New("original composed resident child required; retain original custody")

type p1RootBootstrap struct {
	Schema         uint32                          `json:"schema"`
	Service        residentservice.P1BootstrapData `json:"service"`
	Fence          residentbirth.P1FenceData       `json:"fence"`
	Image          mesh.ResidentImageInventory     `json:"image"`
	Settings       daemonconfig.Settings           `json:"settings"`
	SettingsSHA256 string                          `json:"settings_sha256"`
}
type p1LaunchIntentData struct {
	Schema         uint32                       `json:"schema"`
	ServiceDebitID string                       `json:"service_debit_id"`
	LaunchKey      string                       `json:"launch_key"`
	ProfileSHA256  string                       `json:"profile_sha256"`
	Build          residentservice.Build        `json:"build"`
	ControlID      residentservice.NativeID     `json:"control_id"`
	Fence          residentrefs.FenceIdentities `json:"fence"`
	Image          mesh.ResidentImageInventory  `json:"image"`
	SettingsSHA256 string                       `json:"settings_sha256"`
}
type residentChildPending struct {
	mu                             *sync.Mutex
	self                           *residentChildPending
	rootContext                    context.Context // independent of parent/API/UI workers
	refs                           *residentrefs.ChildPending
	control                        *residentrefs.ControlOriginal
	serviceCustody                 *residentrefs.ServiceOriginal
	fenceCustody                   *residentrefs.FenceOriginal
	imageCustody                   *residentrefs.ImageOriginal
	service                        *residentservice.ChildPending
	fence                          *residentbirth.InheritedFencePending
	image                          *mesh.InheritedImagePending
	runtime                        *mesh.RuntimeExecutable // nonclosing borrower; existing native/helper type
	bootstrapRaw                   []byte
	bootstrap                      p1RootBootstrap
	composed                       residentrefs.ComposedIdentities
	native                         *nativeowner.ServeIssuer
	managerView                    *lifetimeview.ManagerView
	serverView                     *lifetimeview.ServerView
	charge                         *residentservice.ManagerCharge
	manager                        *manager.ResidentManagerPending
	server                         *residentServerPending
	active                         *residentChildOperation
	prepareTried, bindingsPrepared bool
	firstError                     error
}
type residentChildOperation struct {
	phase    string
	abnormal bool
	cause    any
}

// Fixed-role constructor: first records ALL13 raw inherited originals. Only
// afterward may it construct actual component recipients or any native holder.
// This is not called by legacy Serve or a worker, and exposes no Close/Manager/
// File/FD/command/getter. A partial original remains retained by its caller.
func newResidentChildPending() (root *residentChildPending, resultErr error) {
	root = &residentChildPending{mu: &sync.Mutex{}}
	root.self = root
	defer func() {
		if resultErr != nil {
			root.mu.Lock()
			root.firstError = resultErr
			root.mu.Unlock()
		}
	}()
	root.refs, resultErr = residentrefs.NewFixedChildPending() // FIRST: pure process-wide raw13 claim
	if resultErr != nil {
		return root, resultErr
	}
	root.rootContext = context.Background() // never a launcher/request lifetime
	root.serviceCustody, root.fenceCustody, root.imageCustody, root.control, resultErr = root.refs.NewOriginalRecipients()
	if resultErr != nil {
		return root, resultErr
	}
	root.service, resultErr = residentservice.NewP1ChildPending(root.serviceCustody)
	if resultErr != nil {
		return root, resultErr
	}
	root.fence, resultErr = residentbirth.NewInheritedFencePending(root.rootContext, root.fenceCustody)
	if resultErr != nil {
		return root, resultErr
	}
	root.image, resultErr = mesh.NewInheritedImagePending(root.rootContext, root.imageCustody)
	if resultErr != nil {
		return root, resultErr
	}
	resultErr = root.refs.DelegateOriginalRecipients(root.serviceCustody, root.fenceCustody, root.imageCustody, root.control)
	return root, resultErr
}
func (r *residentChildPending) phase(value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		panic("composed original child operation missing")
	}
	r.active.phase = value
}
func (r *residentChildPending) finish(op *residentChildOperation, normal *bool, resultErr *error) {
	cause := recover()
	r.mu.Lock()
	if r.active != op {
		r.mu.Unlock()
		panic("original composed child identity lost")
	}
	if !*normal {
		op.abnormal, op.cause = true, cause
		if r.firstError == nil {
			r.firstError = &residentServerAbnormal{cause: cause}
		}
	} else {
		if *resultErr != nil && r.firstError == nil {
			r.firstError = *resultErr
		}
		r.bindingsPrepared = *resultErr == nil
		r.active = nil
	}
	r.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}

// Performs actual original references -> service grant/env -> image/fence ->
// native issuer -> Manager/official DB -> Server binding. It DOES NOT start
// Manager loops, publish ingress, ACK or report arm-start readiness: that must
// be composed with the first genuinely admitted active worker. No error/panic
// closes originals, cancels root lifetime or runs legacy abort/recovery.
func (r *residentChildPending) prepareOriginalBindings(bootstrapContext context.Context) (resultErr error) {
	if r == nil || r.self != r {
		return errResidentChildOriginal
	}
	r.mu.Lock()
	if r.active != nil || r.firstError != nil || r.prepareTried {
		r.mu.Unlock()
		return errResidentChildOriginal
	}
	r.prepareTried = true
	op := &residentChildOperation{phase: "registered_before_inherited_native_effect"}
	r.active = op
	r.mu.Unlock()
	normal := false
	defer r.finish(op, &normal, &resultErr)
	resultErr = r.prepareBody(bootstrapContext)
	normal = true
	return resultErr
}
func (r *residentChildPending) prepareBody(bootstrapContext context.Context) (err error) {
	r.phase("all13_CLOEXEC_before_any_wrapper_or_stat")
	if err = r.refs.SealOriginalDescriptors(bootstrapContext); err != nil {
		return err
	}
	r.phase("actual_original_control_materialization")
	if err = r.control.MaterializeOriginal(bootstrapContext); err != nil {
		return err
	}
	r.phase("actual_original_complete_bounded_bootstrap")
	r.bootstrapRaw, err = r.control.ReceiveOriginalData(bootstrapContext)
	if err != nil {
		return err
	}
	if err = decodeP1BootstrapData(r.bootstrapRaw, &r.bootstrap); err != nil {
		return err
	}
	// Startup/parent reconnect timeout controls ONLY the channel bootstrap. Once
	// received, actual original root work uses its independent funded lifetime.
	ctx := r.rootContext
	r.phase("actual_delegated_service_journal_and_schema2_environment")
	if err = r.service.ReceiveP1Original(ctx, r.bootstrap.Service); err != nil {
		return err
	}
	r.phase("actual_delegated_fence_and_same_intent")
	if err = r.fence.VerifyOriginal(ctx, r.bootstrap.Fence); err != nil {
		return err
	}
	intent, err := r.fence.IntentOriginalData()
	if err != nil {
		return err
	}
	expected, err := encodeP1IntentData(r.bootstrap)
	if err != nil {
		return err
	}
	if !bytes.Equal(intent, expected) {
		return errResidentChildOriginal
	}
	r.phase("actual_delegated_image_and_current_child_runtime")
	if err = r.image.VerifyOriginal(ctx, r.bootstrap.Image); err != nil {
		return err
	}
	r.runtime, err = r.image.RuntimeForOriginalRoot()
	if err != nil {
		return err
	}
	if err = r.service.BindOriginalRuntime(ctx, r.runtime); err != nil {
		return err
	}
	r.phase("actual_all13_current_native_names_before_original_claims")
	r.composed, err = r.refs.ValidateComposedOriginal(ctx, r.bootstrap.Service.DebitID)
	if err != nil {
		return err
	}
	if err = validateP1ComposedData(r.bootstrap, r.composed); err != nil {
		return err
	}
	r.phase("actual_native_issuer_retained_before_Open")
	r.native, err = nativeowner.NewInheritedServePending(r.fence)
	if err != nil {
		return err
	}
	if err = r.native.OpenOriginal(); err != nil {
		return err
	}
	r.phase("actual_original_native_Manager_view")
	r.managerView, err = r.native.ManagerView()
	if err != nil {
		return err
	}
	r.phase("actual_original_native_Server_view")
	r.serverView, err = r.native.ServerView()
	if err != nil {
		return err
	}
	r.phase("actual_original_standing_Manager_charge")
	r.charge, err = r.service.ClaimOriginalManagerCharge()
	if err != nil {
		return err
	}
	r.phase("actual_original_Manager_pending_before_database_effects")
	r.manager, err = manager.NewResidentManagerPending(r.managerView, r.bootstrap.Settings)
	if err != nil {
		return err
	}
	if err = r.manager.BindOriginalStandingCharge(r.charge); err != nil {
		return err
	}
	r.phase("actual_original_Manager_official_database_schema")
	if err = r.manager.OpenOriginal(ctx); err != nil {
		return err
	}
	r.phase("actual_original_Server_pending_and_Manager_borrower")
	r.server, err = newResidentServerPending(r.serverView, r.manager)
	if err != nil {
		return err
	}
	if err = r.server.openOriginal(ctx); err != nil {
		return err
	}
	if err = r.service.ValidateP1EnvironmentOriginal(ctx); err != nil {
		return err
	}
	// No ManagerForOriginalServer, loop start, listener, marker, ACK or retirement
	// is performed here. The actual worker/controller/ingress cut must follow.
	return nil
}
func decodeP1BootstrapData(raw []byte, value *p1RootBootstrap) error {
	if len(raw) == 0 || len(raw) > residentrefs.MaxWireBytes-36 {
		return errResidentChildOriginal
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errResidentChildOriginal
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errResidentChildOriginal
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(raw, canonical) || value.Schema != 2 || value.Service.Schema != 2 {
		return errResidentChildOriginal
	}
	if value.Fence.Home != value.Service.Home || value.Image.Home != value.Service.Home || value.Image.DebitID != value.Service.DebitID || value.Image.Path != filepath.Join(value.Service.Home, "resident-images", "pin-"+value.Service.DebitID, "arm") || value.Image.SHA256 != value.Service.Build.SHA256 || value.Image.Size != value.Service.Build.Size {
		return errResidentChildOriginal
	}
	settings, err := daemonconfig.SnapshotSHA256(value.Settings)
	if err != nil || settings != value.SettingsSHA256 {
		return errResidentChildOriginal
	}
	return nil
}
func encodeP1IntentData(bootstrap p1RootBootstrap) ([]byte, error) {
	intent := p1LaunchIntentData{Schema: 2, ServiceDebitID: bootstrap.Service.DebitID, LaunchKey: bootstrap.Service.LaunchKey, ProfileSHA256: bootstrap.Service.ProfileSHA256, Build: bootstrap.Service.Build, ControlID: bootstrap.Service.ChannelID, Fence: bootstrap.Fence.Identities, Image: bootstrap.Image, SettingsSHA256: bootstrap.SettingsSHA256}
	raw, err := json.Marshal(intent)
	if err != nil || len(raw) > 16<<10 {
		return nil, errResidentChildOriginal
	}
	return raw, nil
}
func validateP1ComposedData(bootstrap p1RootBootstrap, actual residentrefs.ComposedIdentities) error {
	if actual.Control.Device != bootstrap.Service.ChannelID.Device || actual.Control.Inode != bootstrap.Service.ChannelID.Inode || actual.Control.UID != bootstrap.Service.ChannelID.UID || actual.Fence != bootstrap.Fence.Identities {
		return errResidentChildOriginal
	}
	expected := bootstrap.Image
	convert := func(id mesh.ResidentImageInventoryID) residentrefs.Identity {
		return residentrefs.Identity{Device: id.Device, Inode: id.Inode, UID: id.UID, GID: id.GID, Mode: id.Mode}
	}
	if actual.Image.Image != convert(expected.ImageID) || actual.Image.Images != convert(expected.ImagesID) || actual.Image.Directory != convert(expected.DirectoryID) || actual.Fence.Home != convert(expected.HomeID) || actual.Service.Home != actual.Fence.Home {
		return errResidentChildOriginal
	}
	return nil
}
