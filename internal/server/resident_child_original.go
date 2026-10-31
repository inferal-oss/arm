package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"time"

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
	entry                          *residentCoreEntry
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
	bootstrapCall                  *residentChildBootstrapCall
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
	preparedValidationTried        bool
	preparedValidation             *residentRootPreparedCall
	rootBoundTried                 bool
	rootBound                      *residentRootBoundCall
}

// Owns the one finite bootstrap I/O context attempt. Runtime constructor/cancel
// internals stay opaque; these return records do not assert native quiescence.
type residentChildBootstrapCall struct {
	contextAttempted, contextReturned bool
	context                           context.Context
	cancel                            context.CancelFunc
	receiveAttempted, receiveReturned bool
	receiveError                      error
	cancelAttempted, cancelReturned   bool
}

type residentChildOperation struct {
	kind     string
	phase    string
	abnormal bool
	cause    any
}

// In-place fixed-role initialization. The actual outer entry retains root BEFORE
// this call; no return-then-assignment can lose a partial original. All13 raw
// entries are published before component recipients or native holders exist.
func (root *residentChildPending) initializeOriginal() (resultErr error) {
	if root == nil || root.self != nil || root.mu != nil {
		return errResidentChildOriginal
	}
	root.mu = &sync.Mutex{}
	root.self = root
	defer func() {
		if resultErr != nil {
			root.mu.Lock()
			root.firstError = resultErr
			root.mu.Unlock()
		}
	}()
	resultErr = residentrefs.NewFixedChildPending(&root.refs) // FIRST: same process-wide raw13 claim
	if resultErr != nil {
		return resultErr
	}
	root.rootContext = context.Background() // never a launcher/request lifetime
	root.serviceCustody, root.fenceCustody, root.imageCustody, root.control, resultErr = root.refs.NewOriginalRecipients()
	if resultErr != nil {
		return resultErr
	}
	root.service, resultErr = residentservice.NewP1ChildPending(root.serviceCustody)
	if resultErr != nil {
		return resultErr
	}
	root.fence, resultErr = residentbirth.NewInheritedFencePending(root.rootContext, root.fenceCustody)
	if resultErr != nil {
		return resultErr
	}
	root.image, resultErr = mesh.NewInheritedImagePending(root.rootContext, root.imageCustody)
	if resultErr != nil {
		return resultErr
	}
	return root.refs.DelegateOriginalRecipients(root.serviceCustody, root.fenceCustody, root.imageCustody, root.control)
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
		if op.kind == "prepare" {
			r.bindingsPrepared = *resultErr == nil
		}
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
	op := &residentChildOperation{kind: "prepare", phase: "registered_before_inherited_native_effect"}
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
	err = r.receiveOriginalBootstrap(bootstrapContext)
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
	err = nativeowner.NewInheritedServePending(&r.native, r.fence)
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

// Called ONLY after all13 CLOEXEC and the original control materialization.
// Creating a timer before that seal could leak inherited originals to helpers.
// The call record is retained before the stdlib timeout constructor is entered;
// an unreturned opaque constructor/cancel is UNKNOWN, never fabricated Closed.
func (r *residentChildPending) receiveOriginalBootstrap(parent context.Context) error {
	if parent == nil {
		return errResidentChildOriginal
	}
	_, finite := parent.Deadline()
	if finite || !r.entry.ownsPreparingRoot(r) {
		// Preserve supplied finite-context handling and the existing nonfinite
		// refusal. Only the actual static entry may construct the extra timer.
		var err error
		r.bootstrapRaw, err = r.control.ReceiveOriginalData(parent)
		return err
	}
	r.phase("original_bootstrap_timeout_constructor_pending_after_all13_seal")
	call := &residentChildBootstrapCall{}
	r.mu.Lock()
	r.bootstrapCall = call
	call.contextAttempted = true
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	r.mu.Lock()
	call.context, call.cancel, call.contextReturned = ctx, cancel, true
	call.receiveAttempted = true
	r.mu.Unlock()
	raw, err := r.control.ReceiveOriginalData(ctx)
	r.mu.Lock()
	r.bootstrapRaw = raw
	call.receiveReturned, call.receiveError = true, err
	call.cancelAttempted = true
	r.mu.Unlock()
	cancel() // this finite bootstrap I/O context only; NEVER the Root lifetime
	r.mu.Lock()
	call.cancelReturned = true
	r.mu.Unlock()
	return err
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
