package residentrefs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"time"
)

const (
	controlBootstrap = iota
	controlBoundAck
	controlChallenge
	controlProof
)

var childControlKinds = [4]string{"p1-bootstrap-read", "p1-bound-ack-write", "p1-current-challenge-read", "p1-current-proof-write"}

type controlIOCall struct {
	offset, requested, returned int
	returnedKnown               bool
	err                         error
}
type controlPhaseCall struct {
	owner                               *ControlOriginal
	op                                  *originalOperation
	index                               int
	frame                               []byte
	io                                  [32]controlIOCall
	calls, transferred                  int
	bytesComplete, frameVerified        bool
	deadline                            time.Time
	clockAttempted, clockReturned       bool
	observedAt                          time.Time
	deadlineAttempted, deadlineReturned bool
	deadlineError                       error
	normalReturned                      bool // immutable final original finish outcome, not ctx.Err later
	resultError                         error
	challenge                           *P1ChallengeRead
}

// The sole constructor is the actual original read2 method below, before IO.
// No serialized value, FD, successful callback or nonce can construct this link.
type P1ChallengeRead struct {
	self    *P1ChallengeRead
	control *ControlOriginal
	call    *controlPhaseCall
}

func (P1ChallengeRead) MarshalJSON() ([]byte, error) { return nil, ErrOriginal }
func (*P1ChallengeRead) UnmarshalJSON([]byte) error  { return ErrOriginal }

func (r *ControlOriginal) originalControl() bool {
	return r != nil && r.self == r && r.state != nil && r.state.census != nil && r.state.census.self != nil && r.state.census.self.state == r.state.census && r.state.original == r && r.state.role == "control" && r.state.census.control == r
}
func controlCallCompleteLocked(call *controlPhaseCall, r *ControlOriginal, index int) bool {
	return call != nil && call.owner == r && call.index == index && call.op != nil && call.op.kind == childControlKinds[index] && call.op.recipient == r.state && call.op.census == r.state.census && call.op.normalFinished && !call.op.abnormal && call.normalReturned && call.resultError == nil && call.bytesComplete && call.frameVerified && call.transferred == len(call.frame)
}
func childControlPrefixData(prior []string, next string) bool {
	if len(prior) >= len(childControlKinds) || next != childControlKinds[len(prior)] {
		return false
	}
	for i, kind := range prior {
		if kind != childControlKinds[i] {
			return false
		}
	}
	return true
}
func (r *ControlOriginal) registerControlCall(op *originalOperation, index int, destination **P1ChallengeRead) (*controlPhaseCall, error) {
	s := r.state.census
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != op || op.recipient != r.state || index < 0 || index >= len(r.state.controlPhases) || r.state.controlPhases[index] != nil {
		return nil, ErrOriginal
	}
	prior := make([]string, 0, index)
	for i := 0; i < index; i++ {
		if !controlCallCompleteLocked(r.state.controlPhases[i], r, i) {
			return nil, ErrOriginal
		}
		prior = append(prior, r.state.controlPhases[i].op.kind)
	}
	if !childControlPrefixData(prior, childControlKinds[index]) {
		return nil, ErrOriginal
	}
	call := &controlPhaseCall{owner: r, op: op, index: index}
	r.state.controlPhases[index] = call // actual fixed slot before subsequent allocation/IO
	if index == controlChallenge {
		if destination == nil || *destination != nil {
			return nil, ErrOriginal
		}
		read := &P1ChallengeRead{control: r, call: call}
		read.self = read
		call.challenge = read
		*destination = read
	}
	return call, nil
}
func (r *ControlOriginal) beginControlPhase(ctx context.Context, index int, destination **P1ChallengeRead) (*controlPhaseCall, error) {
	if !r.originalControl() || ctx == nil || index < 0 || index >= len(childControlKinds) {
		return nil, ErrOriginal
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrOriginal
	}
	if index == controlChallenge && (destination == nil || *destination != nil) {
		return nil, ErrOriginal
	}
	op, err := r.state.beginView(ctx, childControlKinds[index], r.state.entries[0])
	if err != nil {
		return nil, err
	}
	call, err := r.registerControlCall(op, index, destination)
	if err != nil {
		normal := true
		r.state.census.finish(op, &normal, &err)
		return nil, err
	}
	return call, nil
}

// Registered BEFORE the directly deferred original finish at every callsite, so
// this runs AFTER finish and observes its final error, including late cancellation.
// It does not recover: original finish alone records/rethrows panic/Goexit state.
func (r *ControlOriginal) publishControlResult(call *controlPhaseCall, normal *bool, resultErr *error) {
	s := r.state.census
	s.mu.Lock()
	defer s.mu.Unlock()
	if call == nil || call.owner != r || call.index < 0 || call.index >= 4 || r.state.controlPhases[call.index] != call || call.op == nil || call.op.recipient != r.state || call.op.census != s || call.op.kind != childControlKinds[call.index] {
		panic("original control phase identity lost")
	}
	call.normalReturned = *normal && !call.op.abnormal && call.op.normalFinished
	call.resultError = *resultErr
}
func (r *ControlOriginal) controlDeadline(ctx context.Context, call *controlPhaseCall, writing bool) error {
	s := r.state.census
	if err := s.before(call.op, "original_control_phase_deadline_pending"); err != nil {
		return err
	}
	call.deadline, _ = ctx.Deadline()
	// Existing bootstrap finite-context handling stays. New phases require <=5s;
	// actual scope construction/cancellation belongs to later Root orchestration.
	if call.index != controlBootstrap {
		call.clockAttempted = true
		call.observedAt = time.Now()
		call.clockReturned = true
		if call.deadline.After(call.observedAt.Add(5 * time.Second)) {
			return ErrOriginal
		}
	}
	call.deadlineAttempted = true
	if writing {
		call.deadlineError = call.op.entry.file.SetWriteDeadline(call.deadline)
	} else {
		call.deadlineError = call.op.entry.file.SetReadDeadline(call.deadline)
	}
	call.deadlineReturned = true
	return call.deadlineError
}
func validateControlFrameData(frame []byte) error {
	if len(frame) <= wireHeaderBytes || len(frame) > MaxWireBytes || uint64(binary.BigEndian.Uint32(frame[:4])) != uint64(len(frame)-wireHeaderBytes) {
		return ErrOriginal
	}
	h := sha256.Sum256(frame[wireHeaderBytes:])
	if !bytes.Equal(h[:], frame[4:wireHeaderBytes]) {
		return ErrOriginal
	}
	return nil
}
func (r *ControlOriginal) readControlFrame(ctx context.Context, call *controlPhaseCall) error {
	s := r.state.census
	call.frame = make([]byte, wireHeaderBytes)
	call.op.bytes = call.frame
	if err := r.controlDeadline(ctx, call, false); err != nil {
		return err
	}
	readTo := func(want int) error {
		for call.transferred < want {
			if err := s.before(call.op, "original_control_phase_read_pending"); err != nil {
				return err
			}
			if call.calls >= len(call.io) {
				return io.ErrNoProgress
			}
			c := &call.io[call.calls]
			c.offset, c.requested = call.transferred, want-call.transferred
			call.calls++
			n, err := call.op.entry.file.Read(call.frame[call.transferred:want])
			c.returned, c.err, c.returnedKnown = n, err, true
			if n < 0 || n > c.requested {
				return ErrOriginal
			}
			call.transferred += n
			call.op.returned, call.op.ioError = call.transferred, err
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrNoProgress
			}
		}
		return nil
	}
	if err := readTo(wireHeaderBytes); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(call.frame[:4])
	if length == 0 || length > MaxWireBytes-wireHeaderBytes {
		return ErrOriginal
	}
	call.frame = append(call.frame, make([]byte, int(length))...)
	call.op.bytes = call.frame
	if err := readTo(len(call.frame)); err != nil {
		return err
	}
	call.bytesComplete = true
	if err := validateControlFrameData(call.frame); err != nil {
		return err
	}
	call.frameVerified = true
	return nil
}
func (r *ControlOriginal) writeControlFrame(ctx context.Context, call *controlPhaseCall, frame []byte) error {
	if err := validateControlFrameData(frame); err != nil {
		return err
	}
	call.frame = append([]byte(nil), frame...)
	call.op.bytes = call.frame
	call.frameVerified = true
	if err := r.controlDeadline(ctx, call, true); err != nil {
		return err
	}
	for call.transferred < len(call.frame) {
		if err := r.state.census.before(call.op, "original_control_phase_write_pending"); err != nil {
			return err
		}
		if call.calls >= len(call.io) {
			return io.ErrShortWrite
		}
		c := &call.io[call.calls]
		c.offset, c.requested = call.transferred, len(call.frame)-call.transferred
		call.calls++
		n, err := call.op.entry.file.Write(call.frame[call.transferred:])
		c.returned, c.err, c.returnedKnown = n, err, true
		if n < 0 || n > c.requested {
			return ErrOriginal
		}
		call.transferred += n
		call.op.returned, call.op.ioError = call.transferred, err
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	call.bytesComplete = true // actual full normal writes; finish can still refuse
	return nil
}

// Existing two owned bootstrap callsites keep this bootstrap-only API.
func (r *ControlOriginal) ReceiveOriginalData(ctx context.Context) (payload []byte, resultErr error) {
	call, err := r.beginControlPhase(ctx, controlBootstrap, nil)
	if err != nil {
		return nil, err
	}
	normal := false
	defer r.publishControlResult(call, &normal, &resultErr)
	defer r.state.census.finish(call.op, &normal, &resultErr)
	resultErr = r.readControlFrame(ctx, call)
	if resultErr == nil {
		payload = call.frame[wireHeaderBytes:]
	}
	normal = true
	return payload, resultErr
}

// No generic alternate first write. Only dedicated original phase methods may IO.
func (*ControlOriginal) SendOriginalData(context.Context, []byte) error { return ErrOriginal }
func (r *ControlOriginal) SendP1BoundAckOriginal(ctx context.Context, frame []byte) (resultErr error) {
	call, err := r.beginControlPhase(ctx, controlBoundAck, nil)
	if err != nil {
		return err
	}
	normal := false
	defer r.publishControlResult(call, &normal, &resultErr)
	defer r.state.census.finish(call.op, &normal, &resultErr)
	resultErr = r.writeControlFrame(ctx, call, frame)
	normal = true
	return resultErr
}
func (r *ControlOriginal) ReceiveP1CurrentChallengeOriginal(destination **P1ChallengeRead, ctx context.Context) (resultErr error) {
	call, err := r.beginControlPhase(ctx, controlChallenge, destination)
	if err != nil {
		return err
	}
	normal := false
	defer r.publishControlResult(call, &normal, &resultErr)
	defer r.state.census.finish(call.op, &normal, &resultErr)
	resultErr = r.readControlFrame(ctx, call)
	normal = true
	return resultErr
}
func (r *ControlOriginal) SendP1CurrentProofOriginal(ctx context.Context, frame []byte) (resultErr error) {
	call, err := r.beginControlPhase(ctx, controlProof, nil)
	if err != nil {
		return err
	}
	normal := false
	defer r.publishControlResult(call, &normal, &resultErr)
	defer r.state.census.finish(call.op, &normal, &resultErr)
	resultErr = r.writeControlFrame(ctx, call, frame)
	normal = true
	return resultErr
}

func (r *ServiceOriginal) originalChallengeLocked(read *P1ChallengeRead) bool {
	if r == nil || r.self != r || r.state == nil || r.state.census == nil || r.state.original != r || read == nil || read.self != read || !read.control.originalControl() {
		return false
	}
	s := r.state.census
	c := read.control
	if c.state.census != s || s.service != r || s.firstError != nil || r.state.firstError != nil || c.state.firstError != nil || !s.sealed || !s.delegated || !s.self.ownsProcessClaim() || c.state.controlPhases[controlChallenge] != read.call || read.call == nil || read.call.challenge != read || c.state.controlPhases[controlProof] != nil || s.active != nil && s.active.recipient == c.state {
		return false
	}
	for i := controlBootstrap; i <= controlChallenge; i++ {
		if !controlCallCompleteLocked(c.state.controlPhases[i], c, i) {
			return false
		}
	}
	return true
}

// Pure original read/phase linkage. NO current ctx.Err check after normal finish:
// its own later IO-scope cancellation cannot revoke completed normal read facts.
func (r *ServiceOriginal) ValidateOriginalChallengePhase(read *P1ChallengeRead) error {
	if r == nil || r.self != r || r.state == nil || r.state.census == nil {
		return ErrOriginal
	}
	s := r.state.census
	s.mu.Lock()
	defer s.mu.Unlock()
	if !r.originalChallengeLocked(read) {
		return ErrOriginal
	}
	return nil
}
func (r *ServiceOriginal) OriginalChallengePayloadData(read *P1ChallengeRead, expectedAckFrame []byte) ([]byte, error) {
	if r == nil || r.self != r || r.state == nil || r.state.census == nil || len(expectedAckFrame) <= wireHeaderBytes || len(expectedAckFrame) > MaxWireBytes {
		return nil, ErrOriginal
	}
	s := r.state.census
	s.mu.Lock()
	defer s.mu.Unlock()
	if !r.originalChallengeLocked(read) || !bytes.Equal(read.control.state.controlPhases[controlBoundAck].frame, expectedAckFrame) {
		return nil, ErrOriginal
	}
	return append([]byte(nil), read.call.frame[wireHeaderBytes:]...), nil
}
