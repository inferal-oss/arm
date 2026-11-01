package residentservice

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"

	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/residentrefs"
)

// These originals are issued only by the actual registered parent operations.
// Read payloads/context observations are DATA, never source/native authority.
type WorkerParentAckAuthentication struct {
	self           *WorkerParentAckAuthentication
	group          *WorkerServiceGroup
	op             *operation
	read           *WorkerParentRead
	context        residentprotocol.WorkerContextData
	journal        workerJournalData
	normalReturned bool
	resultError    error
}
type WorkerParentChallenge struct {
	self           *WorkerParentChallenge
	group          *WorkerServiceGroup
	op             *operation
	ack            *WorkerParentAckAuthentication
	nonce          [32]byte
	frame          []byte
	normalReturned bool
	resultError    error
}
type WorkerParentProofAuthentication struct {
	self           *WorkerParentProofAuthentication
	group          *WorkerServiceGroup
	op             *operation
	read           *WorkerParentRead
	challenge      *WorkerParentChallenge
	normalReturned bool
	resultError    error
}

func WorkerLaunchKeyCommitmentData(key string) (string, error) {
	if !boundedText(key, 128) {
		return "", ErrInvalid
	}
	return digest(append([]byte("arm-resident-worker-launch-key-v1\x00"), []byte(key)...)), nil
}

func (p *WorkerServiceGroup) ackCompletedLocked() bool {
	a := p.ackAuthentication
	return a != nil && a.self == a && a.group == p && a.op != nil && a.op.owner == p.parent.custody.raw && a.op.kind == "worker-parent-ack-auth" && a.op.normalFinished && !a.op.abnormal && a.normalReturned && a.resultError == nil && a.read != nil && p.controlPhases[workerParentBoundAck] != nil && p.controlPhases[workerParentBoundAck].read == a.read
}
func (p *WorkerServiceGroup) challengeCompletedLocked() bool {
	c := p.challenge
	return p.ackCompletedLocked() && c != nil && c.self == c && c.group == p && c.ack == p.ackAuthentication && c.op != nil && c.op.owner == p.parent.custody.raw && c.op.kind == "worker-parent-challenge-sign" && c.op.normalFinished && !c.op.abnormal && c.normalReturned && c.resultError == nil && len(c.frame) > 36 && len(c.frame) <= residentrefs.MaxWireBytes
}
func (p *WorkerServiceGroup) proofCompletedLocked() bool {
	a := p.proofAuthentication
	if !p.challengeCompletedLocked() || a == nil || a.self != a || a.group != p || a.challenge != p.challenge || a.op == nil || a.op.owner != p.parent.custody.raw || a.op.kind != "worker-parent-proof-auth" || !a.op.normalFinished || a.op.abnormal || !a.normalReturned || a.resultError != nil || a.read == nil || p.controlPhases[workerParentProof] == nil || p.controlPhases[workerParentProof].read != a.read {
		return false
	}
	for index, call := range p.controlPhases {
		if !p.parentControlCompleteLocked(call, index) {
			return false
		}
	}
	return bytes.Equal(p.controlPhases[workerParentChallenge].frame, p.challenge.frame)
}

func (p *WorkerServiceGroup) publishAckAuthentication(a *WorkerParentAckAuthentication, normal *bool, err *error) {
	w := p.parent.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if p.ackAuthentication != a || a.group != p || a.op == nil {
		panic("worker parent ACK authentication identity lost")
	}
	a.normalReturned = *normal && a.op.normalFinished && !a.op.abnormal
	a.resultError = *err
}

func (p *WorkerServiceGroup) AuthenticateWorkerBoundAckOriginal(destination **WorkerParentAckAuthentication, ctx context.Context, read *WorkerParentRead, expected residentprotocol.WorkerContextData) (resultErr error) {
	if destination == nil || *destination != nil || p == nil || p.self != p || read == nil || read.self != read || read.group != p || expected.Validate() != nil {
		return ErrInvalid
	}
	op, err := p.beginOriginal("worker-parent-ack-auth")
	if err != nil {
		return err
	}
	a := &WorkerParentAckAuthentication{group: p, op: op, read: read}
	a.self = a
	p.parent.custody.raw.mu.Lock()
	p.ackAuthentication = a
	*destination = a
	p.parent.custody.raw.mu.Unlock()
	normal := false
	defer p.publishAckAuthentication(a, &normal, &resultErr)
	defer p.parent.custody.raw.finish(op, &normal, &resultErr)
	resultErr = p.authenticateAckBodyOriginal(ctx, a, expected)
	normal = true
	return resultErr
}

func (p *WorkerServiceGroup) authenticateAckBodyOriginal(ctx context.Context, a *WorkerParentAckAuthentication, expected residentprotocol.WorkerContextData) error {
	if err := workerContextData(ctx); err != nil {
		return err
	}
	w := p.parent.custody
	w.raw.mu.Lock()
	validRead := a.read == p.controlPhases[workerParentBoundAck].read
	w.raw.mu.Unlock()
	if !validRead {
		return ErrInvalid
	}
	payload, err := a.read.BoundAckPayloadOriginalData()
	if err != nil {
		return err
	}
	body, err := residentprotocol.DecodeWorkerBoundAckPayloadData(w.secret, payload)
	if err != nil {
		return err
	}
	if residentprotocol.MatchWorkerContextData(body.Context, expected) != nil {
		return ErrInvalid
	}
	d := p.debit.record
	profile := w.grant.Intent.Profile
	keySHA, err := WorkerLaunchKeyCommitmentData(d.LaunchKey)
	if err != nil {
		return err
	}
	envSHA, err := p1EnvironmentSHA256Data(profile.Core.RuntimeEnvironment)
	if err != nil {
		return err
	}
	if expected.DebitID != d.ID || expected.LaunchKeySHA256 != keySHA || expected.ProfileSHA256 != d.ProfileSHA256 || expected.ContractSHA256 != d.ContractSHA256 || expected.InitialPackageSHA256 != d.InitialPackageSHA256 || expected.InitialManifestSHA256 != d.InitialManifestSHA256 || expected.ImageSHA256 != profile.Core.Build.SHA256 || expected.ImageBytes != profile.Core.Build.Size || expected.EnvironmentSHA256 != envSHA {
		return ErrInvalid
	}
	if err = w.raw.files.validateCurrent(w.path); err != nil {
		return err
	}
	// Fourth and final bounded parent journal read: actual child RootBound3.
	current, err := w.readJournalOriginal(ctx)
	if err != nil {
		return err
	}
	if len(current.records) != 3 {
		return ErrUnresolved
	}
	prefix, err := encodeWorkerJournalData(current.records[:2])
	if err != nil {
		return err
	}
	decoded, err := decodeWorkerJournalData(prefix)
	if err != nil {
		return err
	}
	if decoded.bytes != w.journal.bytes || decoded.last != w.journal.last {
		return ErrUnresolved
	}
	root := current.records[2].Root
	if root == nil || root.DebitID != d.ID || root.LaunchKey != d.LaunchKey || root.Generation != expected.Generation || root.CommitmentSHA256 != expected.PreparedCommitmentSHA256 || root.ContractSHA256 != expected.ContractSHA256 || current.bytes != expected.ServiceJournalBytes || hex.EncodeToString(current.last[:]) != expected.ServiceJournalSHA256 {
		return ErrInvalid
	}
	w.raw.mu.Lock()
	a.context = expected
	a.journal = current
	w.raw.mu.Unlock()
	return ctx.Err()
}

func (p *WorkerServiceGroup) publishChallenge(c *WorkerParentChallenge, normal *bool, err *error) {
	w := p.parent.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if p.challenge != c || c.group != p || c.op == nil {
		panic("worker parent challenge identity lost")
	}
	c.normalReturned = *normal && c.op.normalFinished && !c.op.abnormal
	c.resultError = *err
}
func (p *WorkerServiceGroup) SignWorkerCurrentChallengeOriginal(destination **WorkerParentChallenge, ctx context.Context, ack *WorkerParentAckAuthentication) (frame []byte, resultErr error) {
	if destination == nil || *destination != nil || p == nil || p.self != p || ack == nil || ack.self != ack || ack.group != p {
		return nil, ErrInvalid
	}
	op, err := p.beginOriginal("worker-parent-challenge-sign")
	if err != nil {
		return nil, err
	}
	c := &WorkerParentChallenge{group: p, op: op, ack: ack}
	c.self = c
	w := p.parent.custody
	w.raw.mu.Lock()
	p.challenge = c
	*destination = c
	w.raw.mu.Unlock()
	normal := false
	defer p.publishChallenge(c, &normal, &resultErr)
	defer w.raw.finish(op, &normal, &resultErr)
	resultErr = workerContextData(ctx)
	if resultErr == nil && ack != p.ackAuthentication {
		resultErr = ErrInvalid
	}
	if resultErr == nil {
		w.raw.files.progress("worker_parent_current_nonce_pending")
		_, resultErr = rand.Read(c.nonce[:])
	}
	if resultErr == nil {
		var payload []byte
		payload, resultErr = residentprotocol.EncodeWorkerCurrentChallengePayloadData(w.secret, ack.context, c.nonce)
		if resultErr == nil {
			c.frame, resultErr = residentrefs.EncodeFrameData(payload)
		}
	}
	if resultErr == nil {
		resultErr = ctx.Err()
	}
	if resultErr == nil {
		frame = append([]byte(nil), c.frame...)
	}
	normal = true
	return frame, resultErr
}

func (p *WorkerServiceGroup) publishProofAuthentication(a *WorkerParentProofAuthentication, normal *bool, err *error) {
	w := p.parent.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if p.proofAuthentication != a || a.group != p || a.op == nil {
		panic("worker parent proof authentication identity lost")
	}
	a.normalReturned = *normal && a.op.normalFinished && !a.op.abnormal
	a.resultError = *err
}
func (p *WorkerServiceGroup) AuthenticateWorkerCurrentProofOriginal(destination **WorkerParentProofAuthentication, ctx context.Context, read *WorkerParentRead, challenge *WorkerParentChallenge) (resultErr error) {
	if destination == nil || *destination != nil || p == nil || p.self != p || read == nil || read.self != read || read.group != p || challenge == nil || challenge.self != challenge || challenge.group != p {
		return ErrInvalid
	}
	op, err := p.beginOriginal("worker-parent-proof-auth")
	if err != nil {
		return err
	}
	a := &WorkerParentProofAuthentication{group: p, op: op, read: read, challenge: challenge}
	a.self = a
	w := p.parent.custody
	w.raw.mu.Lock()
	p.proofAuthentication = a
	*destination = a
	w.raw.mu.Unlock()
	normal := false
	defer p.publishProofAuthentication(a, &normal, &resultErr)
	defer w.raw.finish(op, &normal, &resultErr)
	resultErr = workerContextData(ctx)
	if resultErr == nil && (challenge != p.challenge || read != p.controlPhases[workerParentProof].read) {
		resultErr = ErrInvalid
	}
	if resultErr == nil {
		var payload []byte
		payload, resultErr = read.CurrentProofPayloadOriginalData()
		if resultErr == nil {
			var body residentprotocol.WorkerCurrentProofBodyData
			body, resultErr = residentprotocol.DecodeWorkerCurrentProofPayloadData(w.secret, payload)
			if resultErr == nil {
				resultErr = residentprotocol.MatchWorkerContextData(body.Context, challenge.ack.context)
			}
			if resultErr == nil {
				resultErr = residentprotocol.MatchNonceData(body.Nonce, challenge.nonce)
			}
		}
	}
	if resultErr == nil && !bytes.Equal(p.controlPhases[workerParentChallenge].frame, challenge.frame) {
		resultErr = ErrInvalid
	}
	if resultErr == nil {
		resultErr = w.raw.files.validateCurrent(w.path)
	}
	if resultErr == nil {
		resultErr = ctx.Err()
	}
	normal = true
	return resultErr
}

func (a *WorkerParentAckAuthentication) ContextOriginalData() (residentprotocol.WorkerContextData, error) {
	if a == nil || a.self != a || a.group == nil || a.group.parent == nil || a.group.parent.custody == nil {
		return residentprotocol.WorkerContextData{}, ErrInvalid
	}
	w := a.group.parent.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if !a.group.ackCompletedLocked() || a.group.ackAuthentication != a || w.raw.active != nil || w.raw.firstError != nil {
		return residentprotocol.WorkerContextData{}, ErrInvalid
	}
	return a.context, nil
}
