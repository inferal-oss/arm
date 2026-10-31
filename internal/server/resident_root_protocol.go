package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"time"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/nativeowner"
	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
	"getarm.org/internal/server/lifetimeview"
)

// One actual IO scope per fixed role phase. Child phase0 IS the original C1
// bootstrap allocation via its private alias; its original fields stay here.
// Constructor/cancel returns never imply timer/poller/native quiescence.
type residentProtocolPhase struct {
	child                                                                    *residentChildPending
	parent                                                                   *residentParentPending
	childCall                                                                *residentRootProtocolCall
	parentCall                                                               *residentParentProtocolCall
	childOperation                                                           *residentChildOperation
	index                                                                    int
	kind                                                                     string
	lifetime                                                                 context.Context
	contextAttempted, contextReturned                                        bool
	context                                                                  context.Context
	cancel                                                                   context.CancelFunc
	receiveAttempted, receiveReturned                                        bool
	receiveError                                                             error
	cancelAttempted, cancelReturned                                          bool
	clockAttempted, clockReturned                                            bool
	observedAt                                                               time.Time
	bootstrapPostConstructorClock                                            bool
	startupDeadlineAttempted, startupDeadlineReturned, startupDeadlineFinite bool
	startupDeadline                                                          time.Time
	requestedDeadline                                                        time.Time
	deadlineAttempted, deadlineReturned, deadlineFinite                      bool
	deadline                                                                 time.Time
	ioAttempted, ioReturned                                                  bool
	ioError                                                                  error
	frame                                                                    []byte // noncopying alias, never another full-wire allocation
}

type residentProtocolResult struct {
	attempted, returned bool
	err                 error
}

func (r residentProtocolResult) completeData() bool { return r.attempted && r.returned && r.err == nil }
func sameResidentProtocolContextData(a, b context.Context) bool {
	return a != nil && b != nil && reflect.ValueOf(a).Comparable() && reflect.ValueOf(b).Comparable() && a == b
}
func residentProtocolDeadlineData(now, startup time.Time, finite bool) time.Time {
	d := now.Add(5 * time.Second)
	if finite && startup.Before(d) {
		d = startup
	}
	return d
}
func residentProtocolContextCompleteData(p *residentProtocolPhase) bool {
	return p != nil && p.contextAttempted && p.contextReturned && p.context != nil && p.cancel != nil && p.clockAttempted && p.clockReturned && p.deadlineAttempted && p.deadlineReturned && p.deadlineFinite && !p.deadline.After(p.observedAt.Add(5*time.Second))
}
func residentProtocolIOCompleteData(p *residentProtocolPhase) bool {
	if !residentProtocolContextCompleteData(p) || !p.cancelAttempted || !p.cancelReturned {
		return false
	}
	if p.bootstrapPostConstructorClock {
		return p.receiveAttempted && p.receiveReturned && p.receiveError == nil
	}
	return p.ioAttempted && p.ioReturned && p.ioError == nil
}
func sameResidentProtocolFrameData(a, b []byte) bool {
	return len(a) > 36 && len(a) <= residentrefs.MaxWireBytes && len(a) == len(b) && &a[0] == &b[0]
}

// Called only AFTER an actual typed original IO method returned. No user
// callback/method provider is accepted. Cancel only this owned IO scope.
func (p *residentProtocolPhase) finishIOOriginal(ioError error) error {
	if p == nil || p.index < 0 || p.index >= 4 || p.bootstrapPostConstructorClock || !p.ioAttempted || p.ioReturned || p.cancelAttempted || !residentProtocolContextCompleteData(p) {
		return errResidentChildOriginal
	}
	owned := false
	if p.child != nil && p.parent == nil && p.child.mu != nil {
		p.child.mu.Lock()
		owned = p.child.rootProtocolLocked(p.childCall) && p.child.protocolPhases[p.index] == p
		p.child.mu.Unlock()
	}
	if p.parent != nil && p.child == nil && p.parent.mu != nil {
		p.parent.mu.Lock()
		owned = p.parent.parentProtocolLocked(p.parentCall) && p.parent.protocolPhases[p.index] == p
		p.parent.mu.Unlock()
	}
	if !owned {
		return errResidentChildOriginal
	}
	p.ioReturned, p.ioError = true, ioError // actual normal method result BEFORE own cancel
	p.cancelAttempted = true
	p.cancel()
	p.cancelReturned = true
	return ioError // NEVER p.context.Err() after our own cancellation
}

type residentRootCompletionCheck struct {
	attempted                              bool
	service, launch, manager, server, view residentProtocolResult
	serviceData                            residentservice.P1CurrentServiceData
	launchData                             residentbirth.P1CurrentLaunchData
	managerData                            manager.ResidentCurrentManagerData
	serverData                             residentCurrentServerData
	viewData                               residentprotocol.CurrentChallengeBodyData
	complete                               bool
}

type residentRootProtocolCall struct {
	owner                                                        *residentChildPending
	op                                                           *residentChildOperation
	ctx                                                          context.Context
	prepared                                                     *residentRootPreparedCall
	rootBound                                                    *residentRootBoundCall
	bootstrap                                                    *residentChildBootstrapCall
	refs                                                         *residentrefs.ChildPending
	control                                                      *residentrefs.ControlOriginal
	serviceCustody                                               *residentrefs.ServiceOriginal
	fenceCustody                                                 *residentrefs.FenceOriginal
	service                                                      *residentservice.ChildPending
	fence                                                        *residentbirth.InheritedFencePending
	manager                                                      *manager.ResidentManagerPending
	server                                                       *residentServerPending
	native                                                       *nativeowner.ServeIssuer
	image                                                        *mesh.InheritedImagePending
	runtime                                                      *mesh.RuntimeExecutable
	managerView                                                  *lifetimeview.ManagerView
	serverView                                                   *lifetimeview.ServerView
	managerBound                                                 *manager.ResidentManagerServerBinding
	charge                                                       *residentservice.ManagerCharge
	contextBuild, ackSign, challengeAuth                         residentProtocolResult
	context                                                      residentprotocol.ContextData
	ackFrame, proofFrame                                         []byte
	challengeRead                                                *residentrefs.P1ChallengeRead
	view                                                         *residentservice.CurrentChallengeView
	serviceCurrent, launchCurrent, managerCurrent, serverCurrent residentProtocolResult
	serviceObservation                                           *residentservice.P1CurrentServiceObservation
	serviceData                                                  residentservice.P1CurrentServiceData
	launchData                                                   residentbirth.P1CurrentLaunchData
	managerData                                                  manager.ResidentCurrentManagerData
	serverData                                                   residentCurrentServerData
	nativeCurrent, layoutCurrent, imageBorrow, currentA          residentProtocolResult
	nativeData                                                   lifetimeview.Snapshot
	layout                                                       residentrefs.ComposedIdentities
	imageBorrower                                                *mesh.RuntimeExecutable
	imageData                                                    mesh.RuntimeProof
	freshBuild                                                   residentProtocolResult
	freshPayload                                                 residentPreparedCommitmentData
	freshCanonical                                               []byte
	freshSHA                                                     string
	checks                                                       [2]residentRootCompletionCheck
	proofSign                                                    residentProtocolResult
	normalReturned                                               bool
	resultError                                                  error
}

func (r *residentChildPending) originalProtocolPriorLocked() bool {
	if r.refs == nil || r.control == nil || r.serviceCustody == nil || r.fenceCustody == nil || r.service == nil || r.fence == nil || r.manager == nil || r.server == nil || r.server.managerBound == nil || r.native == nil || r.image == nil || r.runtime == nil || r.managerView == nil || r.serverView == nil || r.charge == nil {
		return false
	}
	p, b := r.preparedValidation, r.rootBound
	return r.firstError == nil && r.bindingsPrepared && r.prepareOperation != nil && r.prepareOperation.kind == "prepare" && r.prepareOperation.normalFinished && !r.prepareOperation.abnormal && r.prepareOperation.finalError == nil && r.preparedRootBoundLinkLocked(p) && b != nil && b == r.rootBound && b.owner == r && b.op != nil && b.op.kind == "root-bound" && b.op.normalFinished && !b.op.abnormal && b.op.finalError == nil && b.returned && b.resultError == nil && b.prepared == p && b.service == r.service && b.fence == r.fence && b.serviceAttempted && b.serviceReturned && b.serviceError == nil && b.serviceSelectorError == nil && b.footerAttempted && b.footerReturned && b.footerError == nil && b.footerSelectorError == nil && b.serviceData == b.expectedService && b.footerData.Footer == b.expectedFooter
}
func (r *residentChildPending) childProtocolPhaseLocked(c *residentRootProtocolCall, index int) bool {
	if c == nil || index < 0 || index >= 4 {
		return false
	}
	p := r.protocolPhases[index]
	if p == nil || p.child != r || p.parent != nil || p.index != index || !residentProtocolIOCompleteData(p) {
		return false
	}
	kinds := [4]string{"Rboot", "Wack", "Rchallenge", "Wproof"}
	if p.kind != kinds[index] {
		return false
	}
	if index == 0 {
		return p == r.bootstrapCall && p == c.bootstrap && p.childOperation == r.prepareOperation && p.childCall == nil && p.bootstrapPostConstructorClock
	}
	if p.childCall != c || p.childOperation != c.op || p.bootstrapPostConstructorClock || !sameResidentProtocolContextData(p.lifetime, c.ctx) || !p.requestedDeadline.Equal(p.observedAt.Add(5*time.Second)) || !p.deadline.Equal(p.requestedDeadline) {
		return false
	}
	if index == 1 {
		return sameResidentProtocolFrameData(p.frame, c.ackFrame)
	}
	if index == 3 {
		return sameResidentProtocolFrameData(p.frame, c.proofFrame)
	}
	return true
}
func (r *residentChildPending) rootProtocolLocked(c *residentRootProtocolCall) bool {
	return c != nil && c == r.protocol && c.owner == r && c.op != nil && c.op.kind == "root-protocol" && !c.op.normalFinished && !c.op.abnormal && r.active == c.op && r.protocolTried && r.originalProtocolPriorLocked() && sameResidentProtocolContextData(c.ctx, r.rootContext) && c.prepared == r.preparedValidation && c.rootBound == r.rootBound && c.refs == r.refs && c.control == r.control && c.serviceCustody == r.serviceCustody && c.fenceCustody == r.fenceCustody && c.service == r.service && c.fence == r.fence && c.manager == r.manager && c.server == r.server && c.managerBound == r.server.managerBound && c.native == r.native && c.image == r.image && c.runtime == r.runtime && c.managerView == r.managerView && c.serverView == r.serverView && c.charge == r.charge && r.childProtocolPhaseLocked(c, 0)
}
func (r *residentChildPending) rootProtocolGate(c *residentRootProtocolCall) error {
	if r == nil || r.self != r || r.mu == nil || !r.entry.ownsPreparingRoot(r) {
		return errResidentChildOriginal
	}
	r.mu.Lock()
	valid := r.rootProtocolLocked(c)
	r.mu.Unlock()
	if !valid {
		return errResidentChildOriginal
	}
	return c.ctx.Err()
}
func (r *residentChildPending) beginRootProtocolOriginal() (*residentRootProtocolCall, error) {
	if r == nil || r.self != r || r.mu == nil || !r.entry.ownsPreparingRoot(r) {
		return nil, errResidentChildOriginal
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil || r.protocolTried || r.protocol != nil || !r.originalProtocolPriorLocked() || r.bootstrapCall == nil || r.protocolPhases[0] != r.bootstrapCall || r.protocolPhases[1] != nil || r.protocolPhases[2] != nil || r.protocolPhases[3] != nil {
		return nil, errResidentChildOriginal
	}
	r.protocolTried = true
	op := &residentChildOperation{kind: "root-protocol", phase: "registered_before_own_boundAck_context_and_secret"}
	c := &residentRootProtocolCall{owner: r, op: op, ctx: r.rootContext, prepared: r.preparedValidation, rootBound: r.rootBound, bootstrap: r.bootstrapCall, refs: r.refs, control: r.control, serviceCustody: r.serviceCustody, fenceCustody: r.fenceCustody, service: r.service, fence: r.fence, manager: r.manager, server: r.server, managerBound: r.server.managerBound, native: r.native, image: r.image, runtime: r.runtime, managerView: r.managerView, serverView: r.serverView, charge: r.charge}
	r.protocol, r.active = c, op
	return c, nil
}
func (r *residentChildPending) beginChildProtocolPhase(c *residentRootProtocolCall, index int) (*residentProtocolPhase, error) {
	if err := r.rootProtocolGate(c); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if index < 1 || index > 3 || r.protocolPhases[index] != nil || !c.contextBuild.completeData() {
		r.mu.Unlock()
		return nil, errResidentChildOriginal
	}
	if !c.ackSign.completeData() || len(c.ackFrame) <= 36 || len(c.ackFrame) > residentrefs.MaxWireBytes || index == 3 && (!c.checks[0].complete || !c.proofSign.completeData() || len(c.proofFrame) <= 36 || len(c.proofFrame) > residentrefs.MaxWireBytes) {
		r.mu.Unlock()
		return nil, errResidentChildOriginal
	}
	for i := 0; i < index; i++ {
		if !r.childProtocolPhaseLocked(c, i) {
			r.mu.Unlock()
			return nil, errResidentChildOriginal
		}
	}
	for i := index + 1; i < 4; i++ {
		if r.protocolPhases[i] != nil {
			r.mu.Unlock()
			return nil, errResidentChildOriginal
		}
	}
	kinds := [4]string{"Rboot", "Wack", "Rchallenge", "Wproof"}
	p := &residentProtocolPhase{child: r, childCall: c, childOperation: c.op, index: index, kind: kinds[index], lifetime: c.ctx}
	r.protocolPhases[index] = p // BEFORE clock/constructor
	p.clockAttempted = true
	r.mu.Unlock()
	p.observedAt = time.Now()
	p.clockReturned = true
	p.requestedDeadline = residentProtocolDeadlineData(p.observedAt, time.Time{}, false)
	p.contextAttempted = true
	p.context, p.cancel = context.WithDeadline(c.ctx, p.requestedDeadline)
	p.contextReturned = true
	p.deadlineAttempted = true
	p.deadline, p.deadlineFinite = p.context.Deadline()
	p.deadlineReturned = true
	if !residentProtocolContextCompleteData(p) || !p.deadline.Equal(p.requestedDeadline) {
		return nil, errResidentChildOriginal
	}
	return p, r.rootProtocolGate(c)
}
func (r *residentChildPending) publishRootProtocol(c *residentRootProtocolCall, normal *bool, resultErr *error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c == nil || c != r.protocol || c.owner != r || c.op == nil || c.op.kind != "root-protocol" {
		panic("original Root protocol identity lost")
	}
	c.normalReturned = *normal && !c.op.abnormal && c.op.normalFinished && c.op.finalError == nil && *resultErr == nil
	c.resultError = *resultErr // no consumed-view validation after W2
}
func (r *residentChildPending) exchangeRootProtocolOriginal() (resultErr error) {
	c, err := r.beginRootProtocolOriginal()
	if err != nil {
		return err
	}
	normal := false
	defer r.publishRootProtocol(c, &normal, &resultErr)
	defer r.finish(c.op, &normal, &resultErr)
	resultErr = r.exchangeRootProtocolBody(c)
	normal = true
	return resultErr
}
func originalRootProtocolContextData(p *residentRootPreparedCall, b *residentRootBoundCall) (residentprotocol.ContextData, error) {
	var empty residentprotocol.ContextData
	if p == nil || b == nil || b.prepared != p || b.serviceData != b.expectedService || b.footerData.Footer != b.expectedFooter {
		return empty, errResidentChildOriginal
	}
	canonical, digest, err := encodePreparedCommitmentData(p.payload)
	if err != nil || digest != p.commitmentSHA256 || !bytes.Equal(canonical, p.canonical) {
		return empty, errResidentChildOriginal
	}
	key, err := residentbirth.P1LaunchKeyCommitmentData(p.serviceData.LaunchKey)
	if err != nil {
		return empty, err
	}
	f := b.footerData
	expectedFooter, err := residentbirth.P1FooterForServiceData(b.serviceData, p.payload.IntentSHA256, p.payload.SettingsSHA256)
	if err != nil || f.Footer != expectedFooter || b.serviceData.Record.DebitID != p.serviceData.DebitID || b.serviceData.Record.LaunchKey != p.serviceData.LaunchKey || b.serviceData.ProfileSHA256 != p.serviceData.ProfileSHA256 || b.serviceData.ImageSHA256 != p.serviceData.Build.SHA256 || b.serviceData.HomeID != p.serviceData.HomeID {
		return empty, errResidentChildOriginal
	}
	c := residentprotocol.ContextData{Generation: p.nativeData.Generation, DebitID: p.serviceData.DebitID, LaunchKeySHA256: key, ProfileSHA256: p.serviceData.ProfileSHA256, ImageSHA256: p.serviceData.Build.SHA256, ImageBytes: p.serviceData.Build.Size, SettingsSHA256: p.payload.SettingsSHA256, EnvironmentSHA256: p.serviceData.EnvironmentSHA256, IntentSHA256: p.payload.IntentSHA256, PreparedCommitmentSHA256: p.commitmentSHA256, ServiceJournalBytes: b.serviceData.JournalBytes, ServiceJournalSHA256: b.serviceData.JournalSHA256, FooterBytes: int64(f.FooterBytes), FooterSHA256: f.FooterSHA256, LaunchJournalBytes: int64(f.JournalBytes), LaunchJournalSHA256: f.JournalSHA256}
	if c.Validate() != nil || c.Generation != b.serviceData.Record.Generation || c.PreparedCommitmentSHA256 != b.serviceData.Record.CommitmentSHA256 || f.Footer.Generation != c.Generation || f.Footer.IntentSHA256 != c.IntentSHA256 || f.Footer.SettingsSHA256 != c.SettingsSHA256 {
		return empty, errResidentChildOriginal
	}
	return c, nil
}
func (r *residentChildPending) exchangeRootProtocolBody(c *residentRootProtocolCall) error {
	if err := r.rootProtocolGate(c); err != nil {
		return err
	}
	c.contextBuild.attempted = true
	c.context, c.contextBuild.err = originalRootProtocolContextData(c.prepared, c.rootBound)
	c.contextBuild.returned = true
	if c.contextBuild.err != nil {
		return c.contextBuild.err
	}
	c.ackSign.attempted = true
	c.ackFrame, c.ackSign.err = c.service.SignP1BoundAckOriginal(c.ctx, c.context)
	c.ackSign.returned = true
	if c.ackSign.err != nil {
		return c.ackSign.err
	}
	p, err := r.beginChildProtocolPhase(c, 1)
	if err != nil {
		return err
	}
	p.frame = c.ackFrame
	p.ioAttempted = true
	err = c.control.SendP1BoundAckOriginal(p.context, c.ackFrame)
	if err = p.finishIOOriginal(err); err != nil {
		return err
	}
	p, err = r.beginChildProtocolPhase(c, 2)
	if err != nil {
		return err
	}
	p.ioAttempted = true
	err = c.control.ReceiveP1CurrentChallengeOriginal(&c.challengeRead, p.context)
	if err = p.finishIOOriginal(err); err != nil {
		return err
	}
	if err = r.rootProtocolGate(c); err != nil {
		return err
	}
	c.challengeAuth.attempted = true
	c.challengeAuth.err = c.service.AuthenticateP1CurrentChallengeOriginal(&c.view, c.ctx, c.challengeRead, c.charge)
	c.challengeAuth.returned = true
	if c.challengeAuth.err != nil {
		return c.challengeAuth.err
	}
	if err = r.validateRootProtocolCurrent(c); err != nil {
		return err
	}
	if err = r.checkRootProtocolCompleted(c, 0); err != nil {
		return err
	}
	c.proofSign.attempted = true
	c.proofFrame, c.proofSign.err = c.service.SignP1CurrentProofOriginal(c.ctx, c.serviceObservation, c.view, c.charge)
	c.proofSign.returned = true
	if c.proofSign.err != nil {
		return c.proofSign.err
	}
	p, err = r.beginChildProtocolPhase(c, 3)
	if err != nil {
		return err
	}
	p.frame = c.proofFrame
	if err = r.checkRootProtocolCompleted(c, 1); err != nil {
		return err
	}
	p.ioAttempted = true // ONLY after actual complete/no-pending gate and signer normal return
	err = c.control.SendP1CurrentProofOriginal(p.context, c.proofFrame)
	if err = p.finishIOOriginal(err); err != nil {
		return err
	}
	// W2 has irreversibly consumed the view. Retain actual completed gate + IO
	// facts; never rerun a view-dependent selector here or reset its phase.
	return c.ctx.Err()
}

func (r *residentChildPending) validateRootProtocolCurrent(c *residentRootProtocolCall) error {
	if err := r.rootProtocolGate(c); err != nil {
		return err
	}
	r.mu.Lock()
	ready := c.challengeAuth.completeData() && c.view != nil && r.childProtocolPhaseLocked(c, 2)
	r.mu.Unlock()
	if !ready {
		return errResidentChildOriginal
	}
	c.serviceCurrent.attempted = true
	c.serviceData, c.serviceCurrent.err = c.service.ValidateP1CurrentOriginal(&c.serviceObservation, c.ctx, c.view, c.charge)
	c.serviceCurrent.returned = true
	if c.serviceCurrent.err != nil {
		return c.serviceCurrent.err
	}
	c.launchCurrent.attempted = true
	c.launchData, c.launchCurrent.err = c.fence.ValidateP1CurrentOriginal(c.ctx, c.service, c.serviceObservation, c.view, c.charge)
	c.launchCurrent.returned = true
	if c.launchCurrent.err != nil {
		return c.launchCurrent.err
	}
	c.managerCurrent.attempted = true
	c.managerData, c.managerCurrent.err = c.manager.ValidateCurrentOriginal(c.ctx, c.managerView, c.view, c.charge, c.managerBound)
	c.managerCurrent.returned = true
	if c.managerCurrent.err != nil {
		return c.managerCurrent.err
	}
	c.serverCurrent.attempted = true
	c.serverData, c.serverCurrent.err = c.server.validateCurrentOriginal(c.ctx, c.serverView, c.view, c.charge)
	c.serverCurrent.returned = true
	if c.serverCurrent.err != nil {
		return c.serverCurrent.err
	}
	c.nativeCurrent.attempted = true
	c.nativeData, c.nativeCurrent.err = c.native.ValidateInheritedOriginalViews(c.managerView, c.serverView, c.fence)
	c.nativeCurrent.returned = true
	if c.nativeCurrent.err != nil {
		return c.nativeCurrent.err
	}
	c.layoutCurrent.attempted = true
	c.layout, c.layoutCurrent.err = c.refs.ValidateComposedOriginal(c.ctx, c.serviceData.Prepared.DebitID)
	c.layoutCurrent.returned = true
	if c.layoutCurrent.err != nil {
		return c.layoutCurrent.err
	}
	c.imageBorrow.attempted = true
	c.imageBorrower, c.imageBorrow.err = c.image.RuntimeForOriginalRoot()
	c.imageBorrow.returned = true
	if c.imageBorrow.err != nil || c.imageBorrower != c.runtime {
		return errResidentChildOriginal
	}
	c.currentA.attempted = true
	c.imageData, c.currentA.err = c.runtime.Proof()
	c.currentA.returned = true
	if c.currentA.err != nil {
		return c.currentA.err
	}
	c.freshBuild.attempted = true
	c.freshPayload, c.freshCanonical, c.freshSHA, c.freshBuild.err = r.freshRootProtocolCommitmentData(c)
	c.freshBuild.returned = true
	if c.freshBuild.err != nil {
		return c.freshBuild.err
	}
	return r.rootProtocolGate(c)
}
func (r *residentChildPending) freshRootProtocolCommitmentData(c *residentRootProtocolCall) (residentPreparedCommitmentData, []byte, string, error) {
	var empty residentPreparedCommitmentData
	s, n, m, v := c.serviceData.Prepared, c.nativeData, c.managerData, c.serverData
	if !n.InheritedFence || n.Path != r.bootstrap.Service.Home || m.Native != n || v.Native != n || m.SettingsSHA256 != c.context.SettingsSHA256 || v.SettingsSHA256 != m.SettingsSHA256 || m.WorkspaceFormat != "4" || v.WorkspaceFormat != m.WorkspaceFormat || s.Home != n.Path || s.HomeID.Device != n.Device || s.HomeID.Inode != n.Inode || s.HomeID.UID != n.UID || s.DebitID != m.DebitID || s.ProfileSHA256 != m.ProfileSHA256 || s.Build.SHA256 != c.imageData.SHA256 || s.Build.Size != c.imageData.Size || c.layout != r.composed || c.layout.Service != s.Identities || c.layout.Fence.Directory != n.PreparedState || c.layout.Fence.Ledger != n.PreparedLedger || c.launchData.Identities != c.layout.Fence || c.serviceData.Context != c.context || c.launchData.Footer != c.rootBound.footerData {
		return empty, nil, "", errResidentChildOriginal
	}
	if c.context.Generation != n.Generation || c.context.DebitID != s.DebitID || c.context.ProfileSHA256 != s.ProfileSHA256 || c.context.EnvironmentSHA256 != s.EnvironmentSHA256 || c.context.ImageSHA256 != c.imageData.SHA256 || c.context.ImageBytes != c.imageData.Size || c.context.ServiceJournalBytes != c.serviceData.RootBound.JournalBytes || c.context.ServiceJournalSHA256 != c.serviceData.RootBound.JournalSHA256 {
		return empty, nil, "", errResidentChildOriginal
	}
	if err := validateP1ComposedData(r.bootstrap, c.layout); err != nil {
		return empty, nil, "", err
	}
	settings, err := daemonconfig.SnapshotSHA256(r.bootstrap.Settings)
	if err != nil || settings != m.SettingsSHA256 {
		return empty, nil, "", errResidentChildOriginal
	}
	intent, err := encodeP1IntentData(r.bootstrap)
	if err != nil || !bytes.Equal(intent, c.prepared.intent) {
		return empty, nil, "", errResidentChildOriginal
	}
	ih := sha256.Sum256(intent)
	intentSHA := hex.EncodeToString(ih[:])
	if intentSHA != c.context.IntentSHA256 || intentSHA != c.launchData.IntentSHA256 {
		return empty, nil, "", errResidentChildOriginal
	}
	p := residentPreparedCommitmentData{Schema: "1", Domain: "arm-resident-prepared-binding-v1", Generation: n.Generation, Home: n.Path, UID: n.UID, Device: n.Device, Inode: n.Inode, Layout: c.layout, SettingsSHA256: settings, WorkspaceFormat: m.WorkspaceFormat, DebitID: s.DebitID, LaunchKey: s.LaunchKey, ProfileSHA256: s.ProfileSHA256, EnvironmentSHA256: s.EnvironmentSHA256, IntentSHA256: intentSHA, ImageSHA256: c.imageData.SHA256, ImageBytes: c.imageData.Size, BirthJournalSHA256: s.BirthJournalSHA256, BirthJournalBytes: s.BirthJournalBytes}
	canonical, digest, err := encodePreparedCommitmentData(p)
	if err != nil || digest != c.prepared.commitmentSHA256 || digest != c.context.PreparedCommitmentSHA256 || !bytes.Equal(canonical, c.prepared.canonical) || c.serviceData.RootBound.Record.CommitmentSHA256 != digest || c.serviceData.RootBound != c.rootBound.serviceData {
		return empty, nil, "", errResidentChildOriginal
	}
	return p, canonical, digest, nil
}
func (r *residentChildPending) checkRootProtocolCompleted(c *residentRootProtocolCall, index int) error {
	if err := r.rootProtocolGate(c); err != nil {
		return err
	}
	r.mu.Lock()
	ready := index >= 0 && index < len(c.checks) && !c.checks[index].attempted && c.serviceCurrent.completeData() && c.launchCurrent.completeData() && c.managerCurrent.completeData() && c.serverCurrent.completeData() && c.nativeCurrent.completeData() && c.layoutCurrent.completeData() && c.imageBorrow.completeData() && c.currentA.completeData() && c.freshBuild.completeData() && c.challengeAuth.completeData() && r.childProtocolPhaseLocked(c, 1) && r.childProtocolPhaseLocked(c, 2)
	if ready && index == 0 {
		ready = r.protocolPhases[3] == nil && !c.proofSign.attempted
	}
	if ready && index == 1 {
		p := r.protocolPhases[3]
		ready = c.checks[0].complete && c.proofSign.completeData() && p != nil && p.child == r && p.parent == nil && p.childCall == c && p.childOperation == c.op && p.index == 3 && p.kind == "Wproof" && !p.bootstrapPostConstructorClock && sameResidentProtocolContextData(p.lifetime, c.ctx) && residentProtocolContextCompleteData(p) && p.requestedDeadline.Equal(p.observedAt.Add(5*time.Second)) && p.deadline.Equal(p.requestedDeadline) && sameResidentProtocolFrameData(p.frame, c.proofFrame) && !p.ioAttempted && !p.ioReturned && !p.cancelAttempted
	}
	if ready {
		c.checks[index].attempted = true
	}
	r.mu.Unlock()
	if !ready {
		return errResidentChildOriginal
	}
	canonical, digest, err := encodePreparedCommitmentData(c.freshPayload)
	if err != nil || digest != c.freshSHA || digest != c.context.PreparedCommitmentSHA256 || !bytes.Equal(canonical, c.freshCanonical) || !bytes.Equal(canonical, c.prepared.canonical) {
		return errResidentChildOriginal
	}
	x := &c.checks[index]
	x.service.attempted = true
	x.serviceData, x.service.err = c.serviceObservation.ValidateOriginalForFence(c.service, c.fenceCustody, c.view, c.charge)
	x.service.returned = true
	if x.service.err != nil {
		return x.service.err
	}
	x.launch.attempted = true
	x.launchData, x.launch.err = c.fence.P1CurrentOriginalData(c.service, c.serviceObservation, c.view, c.charge)
	x.launch.returned = true
	if x.launch.err != nil {
		return x.launch.err
	}
	x.manager.attempted = true
	x.managerData, x.manager.err = c.manager.OriginalCurrentData(c.managerView, c.view, c.charge, c.managerBound)
	x.manager.returned = true
	if x.manager.err != nil {
		return x.manager.err
	}
	x.server.attempted = true
	x.serverData, x.server.err = c.server.currentOriginalData(c.serverView, c.view, c.charge)
	x.server.returned = true
	if x.server.err != nil {
		return x.server.err
	}
	x.view.attempted = true
	x.viewData, x.view.err = c.view.ValidateOriginal(c.charge)
	x.view.returned = true
	if x.view.err != nil {
		return x.view.err
	}
	if x.serviceData != c.serviceData || x.launchData != c.launchData || x.managerData != c.managerData || x.serverData != c.serverData || x.viewData.Context != c.context || index == 1 && x.viewData != c.checks[0].viewData {
		return errResidentChildOriginal
	}
	// Actual completed selectors enforce nil-active DB/Manager/Server/service/
	// fence operations. Root's own current calls and fresh canonical completed
	// above on the SAME originals; no pending result/DTO can authorize W2.
	if err := r.rootProtocolGate(c); err != nil {
		return err
	}
	r.mu.Lock()
	x.complete = true
	r.mu.Unlock()
	return nil
}
