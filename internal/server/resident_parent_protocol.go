package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"time"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/mesh"
	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
)

type residentParentEntropyCall struct {
	buffer              [32]byte
	attempted, returned bool
	n                   int
	err                 error
}
type residentParentStartCall struct {
	owner               *residentParentPending
	op                  *residentParentOperation
	command             *exec.Cmd
	attempted, returned bool
	err                 error
	process             *os.Process
}
type residentParentProtocolCall struct {
	owner                                          *residentParentPending
	op                                             *residentParentOperation // SAME still-active original launch-and-transfer
	ctx                                            context.Context
	command                                        *exec.Cmd
	process                                        *os.Process // EXACT returned original, never a PID lookup
	start                                          *residentParentStartCall
	service                                        *residentservice.ParentPending
	debit                                          *residentservice.OriginalDebit
	group                                          *residentservice.P1ServiceGroup
	image                                          *mesh.ResidentImageP1Pending
	pin                                            *mesh.ResidentImagePinPending
	fence                                          *residentbirth.FreshFence
	preparedFence                                  *residentbirth.PreparedFence
	journal                                        *residentbirth.LaunchJournal
	bootstrapFrame, intent, challengeFrame         []byte
	ackRead, proofRead                             *residentservice.P1ParentRead
	ack                                            *residentservice.P1ParentAckObservation
	serviceObservation                             *residentservice.P1ParentCurrentObservation
	ackAuth, ackData, serviceCurrent, fenceCurrent residentProtocolResult
	context                                        residentprotocol.ContextData
	serviceData                                    residentservice.P1ParentCurrentServiceData
	fenceData                                      residentbirth.P1ParentPostStartData
	serviceSelector, fenceSelector, structural     residentProtocolResult
	serviceRecheck                                 residentservice.P1ParentCurrentServiceData
	fenceRecheck                                   residentbirth.P1ParentPostStartData
	entropy                                        residentParentEntropyCall // retained with owner BEFORE its sole Read
	challengeSign, proofAuth                       residentProtocolResult
	proof                                          residentprotocol.CurrentProofBodyData
	bootstrapProjection                            residentservice.P1TransferData
	bootstrapProjectionError                       error
	bodyReturned, normalReturned                   bool
	bodyError, finalError                          error
}

func (r *residentParentPending) parentProtocolLocked(c *residentParentProtocolCall) bool {
	p := r.prepareOperation
	if c == nil || c != r.protocol || c.owner != r || r.firstError != nil || p == nil || p.kind != "prepare" || !p.normalFinished || p.abnormal || p.finalError != nil || !r.prepared || c.op == nil || c.op != r.launchOperation || r.active != c.op || c.op.kind != "launch-and-transfer" || c.op.normalFinished || c.op.abnormal || !sameResidentProtocolContextData(c.ctx, c.op.ctx) || !r.startAttempted || !r.startReturned || r.startError != nil || r.process == nil || c.process != r.process || c.command == nil || c.command != r.command || c.command.Process != c.process || c.service != r.service || c.service == nil || c.debit != r.debit || c.debit == nil || c.group != r.group || c.group == nil || c.image != r.image || c.image == nil || c.pin != r.pin || c.pin == nil || c.fence != r.fence || c.fence == nil || c.preparedFence != r.preparedFence || c.preparedFence == nil || c.journal != r.journal || c.journal == nil || !sameResidentProtocolFrameData(c.bootstrapFrame, r.frame) || len(c.intent) == 0 || !bytes.Equal(c.intent, r.intent) {
		return false
	}
	s := c.start
	if s == nil || s != r.startCall || s.owner != r || s.op != c.op || s.command != c.command || !s.attempted || !s.returned || s.err != nil || s.process != c.process {
		return false
	}
	if len(r.extra) != 13 || len(c.command.ExtraFiles) != 13 || &r.extra[0] != &c.command.ExtraFiles[0] {
		return false
	}
	for i, file := range c.command.ExtraFiles {
		if file == nil || file != r.bound[i] {
			return false
		}
	}
	return true
}
func (r *residentParentPending) parentProtocolGate(c *residentParentProtocolCall) error {
	if r == nil || r.self != r || r.mu == nil {
		return errResidentParentOriginal
	}
	r.mu.Lock()
	valid := r.parentProtocolLocked(c)
	r.mu.Unlock()
	if !valid {
		return errResidentParentOriginal
	}
	return c.ctx.Err()
}
func (r *residentParentPending) parentProtocolPhaseLocked(c *residentParentProtocolCall, index int) bool {
	if c == nil || index < 0 || index >= 4 {
		return false
	}
	p := r.protocolPhases[index]
	kinds := [4]string{"Wboot", "Rack", "Wchallenge", "Rproof"}
	if p == nil || p.parent != r || p.child != nil || p.parentCall != c || p.index != index || p.kind != kinds[index] || p.bootstrapPostConstructorClock || !sameResidentProtocolContextData(p.lifetime, c.ctx) || !residentProtocolIOCompleteData(p) || !p.startupDeadlineAttempted || !p.startupDeadlineReturned || !p.startupDeadlineFinite || !p.requestedDeadline.Equal(residentProtocolDeadlineData(p.observedAt, p.startupDeadline, true)) || !p.deadline.Equal(p.requestedDeadline) || p.deadline.After(p.startupDeadline) {
		return false
	}
	if index == 0 {
		return sameResidentProtocolFrameData(p.frame, c.bootstrapFrame)
	}
	if index == 2 {
		return sameResidentProtocolFrameData(p.frame, c.challengeFrame)
	}
	return true
}
func (r *residentParentPending) beginParentProtocolOriginal() (*residentParentProtocolCall, error) {
	if r == nil || r.self != r || r.mu == nil {
		return nil, errResidentParentOriginal
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.protocol != nil || r.active == nil || r.active != r.launchOperation || r.active.kind != "launch-and-transfer" {
		return nil, errResidentParentOriginal
	}
	for _, phase := range r.protocolPhases {
		if phase != nil {
			return nil, errResidentParentOriginal
		}
	}
	c := &residentParentProtocolCall{owner: r, op: r.active, ctx: r.active.ctx, command: r.command, process: r.process, start: r.startCall, service: r.service, debit: r.debit, group: r.group, image: r.image, pin: r.pin, fence: r.fence, preparedFence: r.preparedFence, journal: r.journal, bootstrapFrame: r.frame, intent: r.intent}
	// Register BEFORE all phase clocks/contexts/auth/current/RNG work; original
	// Start already happened in the SAME original caller's active operation.
	r.protocol = c
	if !r.parentProtocolLocked(c) {
		return nil, errResidentParentOriginal
	}
	return c, nil
}
func (r *residentParentPending) beginParentProtocolPhase(c *residentParentProtocolCall, index int) (*residentProtocolPhase, error) {
	if err := r.parentProtocolGate(c); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if index < 0 || index >= 4 || r.protocolPhases[index] != nil {
		r.mu.Unlock()
		return nil, errResidentParentOriginal
	}
	for i := 0; i < index; i++ {
		if !r.parentProtocolPhaseLocked(c, i) {
			r.mu.Unlock()
			return nil, errResidentParentOriginal
		}
	}
	for i := index + 1; i < 4; i++ {
		if r.protocolPhases[i] != nil {
			r.mu.Unlock()
			return nil, errResidentParentOriginal
		}
	}
	if index >= 2 && (!c.ackAuth.completeData() || !c.ackData.completeData() || !c.serviceCurrent.completeData() || !c.fenceCurrent.completeData() || !c.structural.completeData() || !c.serviceSelector.completeData() || !c.fenceSelector.completeData() || !c.entropy.attempted || !c.entropy.returned || c.entropy.n != 32 || c.entropy.err != nil || !c.challengeSign.completeData()) {
		r.mu.Unlock()
		return nil, errResidentParentOriginal
	}
	kinds := [4]string{"Wboot", "Rack", "Wchallenge", "Rproof"}
	p := &residentProtocolPhase{parent: r, parentCall: c, index: index, kind: kinds[index], lifetime: c.ctx}
	r.protocolPhases[index] = p // Wboot is UNATTEMPTED here, not circularly completed
	p.clockAttempted = true
	r.mu.Unlock()
	p.observedAt = time.Now()
	p.clockReturned = true
	p.startupDeadlineAttempted = true
	p.startupDeadline, p.startupDeadlineFinite = c.ctx.Deadline()
	p.startupDeadlineReturned = true
	if !p.startupDeadlineFinite {
		return nil, errResidentParentOriginal
	}
	p.requestedDeadline = residentProtocolDeadlineData(p.observedAt, p.startupDeadline, true)
	p.contextAttempted = true
	p.context, p.cancel = context.WithDeadline(c.ctx, p.requestedDeadline)
	p.contextReturned = true
	p.deadlineAttempted = true
	p.deadline, p.deadlineFinite = p.context.Deadline()
	p.deadlineReturned = true
	if !residentProtocolContextCompleteData(p) || !p.deadline.Equal(p.requestedDeadline) {
		return nil, errResidentParentOriginal
	}
	return p, r.parentProtocolGate(c)
}
func (r *residentParentPending) exchangeParentProtocolOriginal() (resultErr error) {
	c, err := r.beginParentProtocolOriginal()
	if err != nil {
		return err
	}
	resultErr = r.exchangeParentProtocolBody(c)
	r.mu.Lock()
	c.bodyReturned, c.bodyError = true, resultErr
	r.mu.Unlock()
	// Final normal/error publication belongs to the SAME original parent's
	// finishOriginal AFTER its late startup ctx.Err outcome, never to a DTO.
	return resultErr
}
func (r *residentParentPending) exchangeParentProtocolBody(c *residentParentProtocolCall) error {
	p, err := r.beginParentProtocolPhase(c, 0)
	if err != nil {
		return err
	}
	p.frame = c.bootstrapFrame
	p.ioAttempted = true
	err = c.group.SendOriginalP1Frame(p.context, c.command, c.bootstrapFrame)
	if err = p.finishIOOriginal(err); err != nil {
		return err
	}
	c.bootstrapProjection, c.bootstrapProjectionError = c.group.TransferOriginalP1Data()
	r.mu.Lock()
	r.transferWritten = c.bootstrapProjection.FullWrite
	r.mu.Unlock() // DATA ONLY; no gate uses this projection
	p, err = r.beginParentProtocolPhase(c, 1)
	if err != nil {
		return err
	}
	p.ioAttempted = true
	err = c.group.ReceiveP1BoundAckOriginal(&c.ackRead, p.context, c.command)
	if err = p.finishIOOriginal(err); err != nil {
		return err
	}
	if err = r.parentProtocolGate(c); err != nil {
		return err
	}
	c.ackAuth.attempted = true
	c.ackAuth.err = c.group.AuthenticateP1BoundAckOriginal(&c.ack, c.ctx, c.command, c.ackRead)
	c.ackAuth.returned = true
	if c.ackAuth.err != nil {
		return c.ackAuth.err
	}
	c.ackData.attempted = true
	c.context, c.ackData.err = c.ack.OriginalData(c.group, c.command)
	c.ackData.returned = true
	if c.ackData.err != nil {
		return c.ackData.err
	}
	// OWN secret authentication above precedes EVERY new native/journal effect.
	c.serviceCurrent.attempted = true
	c.serviceData, c.serviceCurrent.err = c.group.ValidateP1ParentCurrentOriginal(&c.serviceObservation, c.ctx, c.command, c.ack)
	c.serviceCurrent.returned = true
	if c.serviceCurrent.err != nil {
		return c.serviceCurrent.err
	}
	c.fenceCurrent.attempted = true
	c.fenceData, c.fenceCurrent.err = c.fence.ValidateP1PostStartOriginal(c.ctx, c.preparedFence, c.command, c.serviceObservation)
	c.fenceCurrent.returned = true
	if c.fenceCurrent.err != nil {
		return c.fenceCurrent.err
	}
	c.serviceSelector.attempted = true
	c.serviceRecheck, c.serviceSelector.err = c.serviceObservation.ValidateOriginalGroup(c.group, c.command)
	c.serviceSelector.returned = true
	if c.serviceSelector.err != nil {
		return c.serviceSelector.err
	}
	c.fenceSelector.attempted = true
	c.fenceRecheck, c.fenceSelector.err = c.fence.P1PostStartOriginalData(c.preparedFence, c.command, c.serviceObservation)
	c.fenceSelector.returned = true
	if c.fenceSelector.err != nil {
		return c.fenceSelector.err
	}
	c.structural.attempted = true
	c.structural.err = r.checkParentProtocolContext(c)
	c.structural.returned = true
	if c.structural.err != nil {
		return c.structural.err
	}
	if err = r.parentProtocolGate(c); err != nil {
		return err
	}
	if c.entropy.attempted {
		return errResidentParentOriginal
	}
	c.entropy.attempted = true
	c.entropy.n, c.entropy.err = rand.Read(c.entropy.buffer[:]) // THE ONE actual retained Root entropy call
	c.entropy.returned = true
	if c.entropy.err != nil {
		return c.entropy.err
	}
	if c.entropy.n != len(c.entropy.buffer) {
		return io.ErrUnexpectedEOF
	}
	c.challengeSign.attempted = true
	c.challengeFrame, c.challengeSign.err = c.group.SignP1CurrentChallengeOriginal(c.ctx, c.command, c.ack, c.serviceObservation, c.entropy.buffer)
	c.challengeSign.returned = true
	if c.challengeSign.err != nil {
		return c.challengeSign.err
	}
	p, err = r.beginParentProtocolPhase(c, 2)
	if err != nil {
		return err
	}
	p.frame = c.challengeFrame
	p.ioAttempted = true
	err = c.group.SendP1CurrentChallengeOriginal(p.context, c.command, c.challengeFrame)
	if err = p.finishIOOriginal(err); err != nil {
		return err
	}
	p, err = r.beginParentProtocolPhase(c, 3)
	if err != nil {
		return err
	}
	p.ioAttempted = true
	err = c.group.ReceiveP1CurrentProofOriginal(&c.proofRead, p.context, c.command)
	if err = p.finishIOOriginal(err); err != nil {
		return err
	}
	if err = r.parentProtocolGate(c); err != nil {
		return err
	}
	c.proofAuth.attempted = true
	c.proof, c.proofAuth.err = c.group.AuthenticateP1CurrentProofOriginal(c.ctx, c.command, c.proofRead)
	c.proofAuth.returned = true
	if c.proofAuth.err != nil {
		return c.proofAuth.err
	}
	if err = residentprotocol.MatchContextData(c.proof.Context, c.context); err != nil {
		return err
	}
	if err = residentprotocol.MatchNonceData(c.proof.Nonce, c.entropy.buffer); err != nil {
		return err
	}
	// Rack-only service/fence selectors correctly refuse now. Retain the OWN
	// actual normal pre-challenge calls; never reinterpret/relax that phase.
	return r.parentProtocolGate(c)
}
func (r *residentParentPending) checkParentProtocolContext(c *residentParentProtocolCall) error {
	if err := r.parentProtocolGate(c); err != nil {
		return err
	}
	if !c.ackAuth.completeData() || !c.ackData.completeData() || !c.serviceCurrent.completeData() || !c.fenceCurrent.completeData() || !c.serviceSelector.completeData() || !c.fenceSelector.completeData() || c.serviceData != c.serviceRecheck || c.fenceData != c.fenceRecheck || c.fenceData.Service != c.serviceData || c.serviceData.Context != c.context {
		return errResidentParentOriginal
	}
	settings, err := daemonconfig.SnapshotSHA256(r.settings)
	if err != nil || settings != r.settingsSHA || settings != r.bootstrap.SettingsSHA256 || settings != c.context.SettingsSHA256 || settings != c.fenceData.Launch.Footer.SettingsSHA256 {
		return errResidentParentOriginal
	}
	bootstrapSettings, err := daemonconfig.SnapshotSHA256(r.bootstrap.Settings)
	if err != nil || bootstrapSettings != settings {
		return errResidentParentOriginal
	}
	intent, err := encodeP1IntentData(r.bootstrap)
	if err != nil || !bytes.Equal(intent, c.intent) || !bytes.Equal(intent, r.intent) {
		return errResidentParentOriginal
	}
	ih := sha256.Sum256(intent)
	intentSHA := hex.EncodeToString(ih[:])
	key, err := residentbirth.P1LaunchKeyCommitmentData(r.launchKey)
	if err != nil {
		return err
	}
	f, s := c.fenceData.Launch, c.serviceData.RootBound
	if c.context.Validate() != nil || c.context.IntentSHA256 != intentSHA || f.Footer.IntentSHA256 != intentSHA || c.context.DebitID != r.selectors.DebitID || c.context.DebitID != r.bootstrap.Service.DebitID || c.context.Generation != s.Record.Generation || c.context.PreparedCommitmentSHA256 != s.Record.CommitmentSHA256 || s.Record.LaunchKey != r.launchKey || c.context.LaunchKeySHA256 != key || c.context.ProfileSHA256 != r.selectors.ProfileSHA256 || c.context.ImageSHA256 != r.selectors.Build.SHA256 || c.context.ImageBytes != r.selectors.Build.Size || c.context.ImageSHA256 != r.bootstrap.Image.SHA256 || c.context.ImageBytes != r.bootstrap.Image.Size || c.context.ServiceJournalBytes != s.JournalBytes || c.context.ServiceJournalSHA256 != s.JournalSHA256 || c.context.FooterBytes != int64(f.FooterBytes) || c.context.FooterSHA256 != f.FooterSHA256 || c.context.LaunchJournalBytes != int64(f.JournalBytes) || c.context.LaunchJournalSHA256 != f.JournalSHA256 {
		return errResidentParentOriginal
	}
	return nil
}
