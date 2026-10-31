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

// Current observation DATA only; no grant, Ready, native handle or closer.
type P1CurrentLaunchData struct {
	Identities   residentrefs.FenceIdentities
	Footer       P1RootBoundFooterResultData
	IntentSHA256 string
}
type p1CurrentFenceCall struct {
	owner                                                          *inheritedFenceState
	op                                                             *inheritedFenceOperation
	footer                                                         *p1RootBoundFooterCall
	service                                                        *residentservice.ChildPending
	observation                                                    *residentservice.P1CurrentServiceObservation
	view                                                           *residentservice.CurrentChallengeView
	charge                                                         *residentservice.ManagerCharge
	serviceAttempted, serviceReturned                              bool
	serviceData                                                    residentservice.P1CurrentServiceData
	serviceError                                                   error
	serviceRecheckAttempted, serviceRecheckReturned                bool
	serviceRecheck                                                 residentservice.P1CurrentServiceData
	serviceRecheckError                                            error
	identityAttempted, identityReturned                            bool
	identities                                                     residentrefs.FenceIdentities
	identityError                                                  error
	before, after                                                  os.FileInfo
	beforeAttempted, beforeReturned, afterAttempted, afterReturned bool
	beforeError, afterError                                        error
	journal                                                        []byte
	reads                                                          [32]p1FooterIO
	readCalls, readBytes                                           int
	data                                                           P1CurrentLaunchData
	normalReturned                                                 bool
	resultError                                                    error
}

func (s *inheritedFenceState) completedFooterLocked(service *residentservice.ChildPending) bool {
	c := s.footer
	return s.verified && s.footerTried && s.firstError == nil && c != nil && c.owner == s && c.op != nil && c.op.kind == "rootbound-footer" && c.op.normalFinished && !c.op.abnormal && c.returned && c.resultError == nil && c.committed && c.syncAttempted && c.syncReturned && c.syncError == nil && c.written == len(c.frame) && c.eof && c.service == service && service != nil && len(s.intent) > 0 && len(s.intent) <= maxIntentBytes && s.bytesRead == len(s.intent)
}
func (p *InheritedFencePending) beginP1Current(ctx context.Context, service *residentservice.ChildPending, observation *residentservice.P1CurrentServiceObservation, view *residentservice.CurrentChallengeView, charge *residentservice.ManagerCharge) (*p1CurrentFenceCall, error) {
	if p == nil || p.state == nil || p.state.self != p || ctx == nil || service == nil || observation == nil || view == nil || charge == nil {
		return nil, errOriginalFence
	}
	s := p.state
	s.mu.Lock()
	valid := s.completedFooterLocked(service)
	s.mu.Unlock()
	if !valid {
		return nil, errOriginalFence
	}
	op, err := p.begin(ctx, "p1-current-journal")
	if err != nil {
		return nil, err
	}
	call := &p1CurrentFenceCall{owner: s, op: op, footer: s.footer, service: service, observation: observation, view: view, charge: charge}
	s.mu.Lock()
	s.current = call
	s.mu.Unlock() // BEFORE any foreign/census/native effect
	return call, nil
}
func (s *inheritedFenceState) currentFenceLocked(c *p1CurrentFenceCall) bool {
	return c != nil && c == s.current && c.owner == s && c.op != nil && c.op.kind == "p1-current-journal" && !c.op.abnormal && s.active == c.op && s.currentTried && c.footer == s.footer && s.completedFooterLocked(c.service) && c.observation != nil && c.view != nil && c.charge != nil
}
func (s *inheritedFenceState) currentFenceGate(c *p1CurrentFenceCall) error {
	s.mu.Lock()
	valid := s.currentFenceLocked(c)
	s.mu.Unlock()
	if !valid {
		return errOriginalFence
	}
	return s.rootContext.Err()
}
func (s *inheritedFenceState) publishP1CurrentResult(c *p1CurrentFenceCall, normal *bool, resultErr *error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c == nil || c != s.current || c.owner != s || c.op == nil || c.op.kind != "p1-current-journal" {
		panic("original current launch observation identity lost")
	}
	c.normalReturned = *normal && !c.op.abnormal && c.op.normalFinished
	c.resultError = *resultErr
}
func (p *InheritedFencePending) ValidateP1CurrentOriginal(ctx context.Context, service *residentservice.ChildPending, observation *residentservice.P1CurrentServiceObservation, view *residentservice.CurrentChallengeView, charge *residentservice.ManagerCharge) (data P1CurrentLaunchData, resultErr error) {
	c, err := p.beginP1Current(ctx, service, observation, view, charge)
	if err != nil {
		return data, err
	}
	s := p.state
	normal := false
	defer s.publishP1CurrentResult(c, &normal, &resultErr)
	defer s.finish(c.op, &normal, &resultErr)
	resultErr = s.validateP1CurrentBody(c)
	if resultErr == nil {
		data = c.data
	}
	normal = true
	return data, resultErr
}
func (s *inheritedFenceState) validateP1CurrentBody(c *p1CurrentFenceCall) error {
	if err := s.currentFenceGate(c); err != nil {
		return err
	}
	// The actual footer captured this exact service child. The original service
	// handle also proves the service/fence SAME census pointers, never IDs alone.
	c.serviceAttempted = true
	c.serviceData, c.serviceError = c.observation.ValidateOriginalForFence(c.service, s.custody, c.view, c.charge)
	c.serviceReturned = true
	if c.serviceError != nil {
		return c.serviceError
	}
	if err := s.currentFenceGate(c); err != nil {
		return err
	}
	if c.serviceData.RootBound.HomeID != sharedServiceHomeData(s.original.Home) {
		return errOriginalFence
	}
	ctx := s.rootContext // existing original independent lifetime, never IO/request scope
	c.identityAttempted = true
	c.identities, c.identityError = s.custody.ValidateOriginal(ctx)
	c.identityReturned = true
	if c.identityError != nil {
		return c.identityError
	}
	if c.identities != s.original {
		return errOriginalFence
	}
	if err := s.currentFenceGate(c); err != nil {
		return err
	}
	c.beforeAttempted = true
	c.before, c.beforeError = s.journal.StatOriginal(ctx)
	c.beforeReturned = true
	if c.beforeError != nil {
		return c.beforeError
	}
	if c.before == nil || c.before.Size() <= 0 || c.before.Size() > MaxP1LaunchJournalBytes || c.before.Size() != int64(c.footer.result.JournalBytes) || c.footer.after == nil || !os.SameFile(c.before, c.footer.after) {
		return errOriginalFence
	}
	c.journal = make([]byte, int(c.before.Size())) // separate read3 retained before IO
	for c.readBytes < len(c.journal) {
		if err := s.currentFenceGate(c); err != nil {
			return err
		}
		if c.readCalls >= len(c.reads) {
			return io.ErrNoProgress
		}
		read := &c.reads[c.readCalls]
		read.offset, read.requested = int64(c.readBytes), len(c.journal)-c.readBytes
		c.readCalls++
		n, err := s.journal.ReadAtOriginal(ctx, c.journal[c.readBytes:], read.offset)
		read.returned, read.err, read.returnedKnown = n, err, true
		if n < 0 || n > read.requested {
			return errOriginalFence
		}
		c.readBytes += n
		if err != nil && !(errors.Is(err, io.EOF) && c.readBytes == len(c.journal)) {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	if err := s.currentFenceGate(c); err != nil {
		return err
	}
	c.afterAttempted = true
	c.after, c.afterError = s.journal.StatOriginal(ctx)
	c.afterReturned = true
	if c.afterError != nil {
		return c.afterError
	}
	if c.after == nil || !os.SameFile(c.before, c.after) || c.before.Size() != c.after.Size() || !c.before.ModTime().Equal(c.after.ModTime()) {
		return errOriginalFence
	}
	footer, err := validateP1CurrentLaunchJournalData(c.journal, s.intent, c.footer.frame, c.footer.result)
	if err != nil {
		return err
	}
	expected, err := P1FooterForServiceData(c.serviceData.RootBound, s.expected.IntentSHA256, c.footer.settingsSHA)
	if err != nil || footer.Footer != expected || footer.Footer != c.footer.data {
		return errOriginalFence
	}
	intentSHA := sha256.Sum256(s.intent)
	intentHash := hex.EncodeToString(intentSHA[:])
	contextData := c.serviceData.Context
	if contextData.IntentSHA256 != intentHash || intentHash != s.expected.IntentSHA256 || contextData.FooterBytes != int64(footer.FooterBytes) || contextData.FooterSHA256 != footer.FooterSHA256 || contextData.LaunchJournalBytes != int64(footer.JournalBytes) || contextData.LaunchJournalSHA256 != footer.JournalSHA256 || contextData.SettingsSHA256 != c.footer.settingsSHA {
		return errOriginalFence
	}
	// No fence mutex is held while consulting the same actual service call.
	c.serviceRecheckAttempted = true
	c.serviceRecheck, c.serviceRecheckError = c.observation.ValidateOriginalForFence(c.service, s.custody, c.view, c.charge)
	c.serviceRecheckReturned = true
	if c.serviceRecheckError != nil {
		return c.serviceRecheckError
	}
	if c.serviceRecheck != c.serviceData {
		return errOriginalFence
	}
	if err = s.currentFenceGate(c); err != nil {
		return err
	}
	c.data = P1CurrentLaunchData{Identities: c.identities, Footer: footer, IntentSHA256: intentHash}
	return ctx.Err()
}
func sharedServiceHomeData(id residentrefs.Identity) residentservice.NativeID {
	return residentservice.NativeID{Device: id.Device, Inode: id.Inode, UID: id.UID}
}

// Exact bytes/hashes from current read DATA; never an original-operation grant.
func validateP1CurrentLaunchJournalData(current, intent, footer []byte, expected P1RootBoundFooterResultData) (P1RootBoundFooterResultData, error) {
	var empty P1RootBoundFooterResultData
	if len(current) == 0 || len(current) > MaxP1LaunchJournalBytes || len(current) != expected.JournalBytes || len(intent) == 0 || len(intent) > maxIntentBytes || len(intent) >= len(current) || len(current)-len(intent) != len(footer) || len(footer) != expected.FooterBytes || !bytes.Equal(current[:len(intent)], intent) || !bytes.Equal(current[len(intent):], footer) {
		return empty, errOriginalFence
	}
	decoded, err := DecodeP1RootBoundFooterData(current[len(intent):])
	if err != nil || decoded != expected.Footer {
		return empty, errOriginalFence
	}
	f := sha256.Sum256(current[len(intent):])
	j := sha256.Sum256(current)
	result := P1RootBoundFooterResultData{Footer: decoded, FooterBytes: len(footer), JournalBytes: len(current), FooterSHA256: hex.EncodeToString(f[:]), JournalSHA256: hex.EncodeToString(j[:])}
	if result != expected {
		return empty, errOriginalFence
	}
	return result, nil
}
func (s *inheritedFenceState) completedCurrentFenceLocked(c *p1CurrentFenceCall, service *residentservice.ChildPending, observation *residentservice.P1CurrentServiceObservation, view *residentservice.CurrentChallengeView, charge *residentservice.ManagerCharge) bool {
	return s.active == nil && s.currentTried && c != nil && c == s.current && c.owner == s && c.op != nil && c.op.kind == "p1-current-journal" && c.op.normalFinished && !c.op.abnormal && c.normalReturned && c.resultError == nil && c.footer == s.footer && s.completedFooterLocked(service) && c.service == service && c.observation == observation && c.view == view && c.charge == charge && c.serviceAttempted && c.serviceReturned && c.serviceError == nil && c.serviceRecheckAttempted && c.serviceRecheckReturned && c.serviceRecheckError == nil && c.serviceRecheck == c.serviceData && c.identityAttempted && c.identityReturned && c.identityError == nil && c.beforeAttempted && c.beforeReturned && c.beforeError == nil && c.afterAttempted && c.afterReturned && c.afterError == nil && c.readBytes == len(c.journal) && c.data.Identities == s.original
}

// Bounded DATA from the SAME completed current call. No native/owner getter.
func (p *InheritedFencePending) P1CurrentOriginalData(service *residentservice.ChildPending, observation *residentservice.P1CurrentServiceObservation, view *residentservice.CurrentChallengeView, charge *residentservice.ManagerCharge) (P1CurrentLaunchData, error) {
	if p == nil || p.state == nil || p.state.self != p {
		return P1CurrentLaunchData{}, errOriginalFence
	}
	s := p.state
	s.mu.Lock()
	c := s.current
	valid := s.completedCurrentFenceLocked(c, service, observation, view, charge)
	s.mu.Unlock()
	if !valid {
		return P1CurrentLaunchData{}, errOriginalFence
	}
	data, err := observation.ValidateOriginalForFence(service, s.custody, view, charge)
	if err != nil {
		return P1CurrentLaunchData{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.completedCurrentFenceLocked(c, service, observation, view, charge) || data != c.serviceData {
		return P1CurrentLaunchData{}, errOriginalFence
	}
	return c.data, nil
}
