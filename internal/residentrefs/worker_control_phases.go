package residentrefs

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"time"
)

var workerChildControlKinds = [4]string{"worker-bootstrap-read", "worker-bound-ack-write", "worker-current-challenge-read", "worker-current-proof-write"}

type workerControlIOCall struct {
	offset, requested, returned int
	returnedKnown               bool
	err                         error
}
type workerControlPhaseCall struct {
	owner                               *ControlOriginal
	op                                  *originalOperation
	index                               int
	frame                               []byte
	io                                  [32]workerControlIOCall
	calls, transferred                  int
	bytesComplete, frameVerified        bool
	deadline                            time.Time
	clockAttempted, clockReturned       bool
	observedAt                          time.Time
	deadlineAttempted, deadlineReturned bool
	deadlineError                       error
	normalReturned                      bool // immutable final original finish outcome, not ctx.Err later
	resultError                         error
	challenge                           *WorkerChallengeRead
}

// The sole constructor is the actual original read2 method below, before IO.
// No serialized value, FD, successful callback or nonce can construct this link.
type WorkerChallengeRead struct {
	self    *WorkerChallengeRead
	control *ControlOriginal
	call    *workerControlPhaseCall
}

func (WorkerChallengeRead) MarshalJSON() ([]byte, error) { return nil, ErrOriginal }
func (*WorkerChallengeRead) UnmarshalJSON([]byte) error  { return ErrOriginal }

func (r *ControlOriginal) originalWorkerControl() bool {
	return r != nil && r.self == r && r.state != nil && r.state.census != nil && r.state.census.self != nil && r.state.census.self.state == r.state.census && r.state.original == r && r.state.role == "control" && r.state.census.control == r && r.state.census.scope == workerChildCore
}
func workerControlCallCompleteLocked(call *workerControlPhaseCall, r *ControlOriginal, index int) bool {
	return call != nil && call.owner == r && call.index == index && call.op != nil && call.op.kind == workerChildControlKinds[index] && call.op.recipient == r.state && call.op.census == r.state.census && call.op.normalFinished && !call.op.abnormal && call.normalReturned && call.resultError == nil && call.bytesComplete && call.frameVerified && call.transferred == len(call.frame)
}
func workerChildControlPrefixData(prior []string, next string) bool {
	if len(prior) >= len(workerChildControlKinds) || next != workerChildControlKinds[len(prior)] {
		return false
	}
	for i, kind := range prior {
		if kind != workerChildControlKinds[i] {
			return false
		}
	}
	return true
}
func (r *ControlOriginal) registerWorkerControlCall(op *originalOperation, index int, destination **WorkerChallengeRead) (*workerControlPhaseCall, error) {
	s := r.state.census
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != op || op.recipient != r.state || index < 0 || index >= len(r.state.workerControlPhases) || r.state.workerControlPhases[index] != nil {
		return nil, ErrOriginal
	}
	prior := make([]string, 0, index)
	for i := 0; i < index; i++ {
		if !workerControlCallCompleteLocked(r.state.workerControlPhases[i], r, i) {
			return nil, ErrOriginal
		}
		prior = append(prior, r.state.workerControlPhases[i].op.kind)
	}
	if !workerChildControlPrefixData(prior, workerChildControlKinds[index]) {
		return nil, ErrOriginal
	}
	call := &workerControlPhaseCall{owner: r, op: op, index: index}
	r.state.workerControlPhases[index] = call // actual fixed slot before subsequent allocation/IO
	if index == controlChallenge {
		if destination == nil || *destination != nil {
			return nil, ErrOriginal
		}
		read := &WorkerChallengeRead{control: r, call: call}
		read.self = read
		call.challenge = read
		*destination = read
	}
	return call, nil
}
func (r *ControlOriginal) beginWorkerControlPhase(ctx context.Context, index int, destination **WorkerChallengeRead) (*workerControlPhaseCall, error) {
	if !r.originalWorkerControl() || ctx == nil || index < 0 || index >= len(workerChildControlKinds) {
		return nil, ErrOriginal
	}
	if err := r.RequireOriginalWorkerCore(); err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrOriginal
	}
	if index == controlChallenge && (destination == nil || *destination != nil) {
		return nil, ErrOriginal
	}
	op, err := r.state.beginView(ctx, workerChildControlKinds[index], r.state.entries[0])
	if err != nil {
		return nil, err
	}
	call, err := r.registerWorkerControlCall(op, index, destination)
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
func (r *ControlOriginal) publishWorkerControlResult(call *workerControlPhaseCall, normal *bool, resultErr *error) {
	s := r.state.census
	s.mu.Lock()
	defer s.mu.Unlock()
	if call == nil || call.owner != r || call.index < 0 || call.index >= 4 || r.state.workerControlPhases[call.index] != call || call.op == nil || call.op.recipient != r.state || call.op.census != s || call.op.kind != workerChildControlKinds[call.index] {
		panic("original control phase identity lost")
	}
	call.normalReturned = *normal && !call.op.abnormal && call.op.normalFinished
	call.resultError = *resultErr
}
func (r *ControlOriginal) workerControlDeadline(ctx context.Context, call *workerControlPhaseCall, writing bool) error {
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
func (r *ControlOriginal) readWorkerControlFrame(ctx context.Context, call *workerControlPhaseCall) error {
	s := r.state.census
	call.frame = make([]byte, wireHeaderBytes)
	call.op.bytes = call.frame
	if err := r.workerControlDeadline(ctx, call, false); err != nil {
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
func (r *ControlOriginal) writeWorkerControlFrame(ctx context.Context, call *workerControlPhaseCall, frame []byte) error {
	if err := validateControlFrameData(frame); err != nil {
		return err
	}
	call.frame = append([]byte(nil), frame...)
	call.op.bytes = call.frame
	call.frameVerified = true
	if err := r.workerControlDeadline(ctx, call, true); err != nil {
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

// Worker-only fixed four-phase bootstrap. P1 methods keep their own registry.
func (r *ControlOriginal) ReceiveWorkerBootstrapOriginal(ctx context.Context) (payload []byte, resultErr error) {
	call, err := r.beginWorkerControlPhase(ctx, controlBootstrap, nil)
	if err != nil {
		return nil, err
	}
	normal := false
	defer r.publishWorkerControlResult(call, &normal, &resultErr)
	defer r.state.census.finish(call.op, &normal, &resultErr)
	resultErr = r.readWorkerControlFrame(ctx, call)
	if resultErr == nil {
		payload = append([]byte(nil), call.frame[wireHeaderBytes:]...)
	}
	normal = true
	return payload, resultErr
}

func (r *ControlOriginal) SendWorkerBoundAckOriginal(ctx context.Context, frame []byte) (resultErr error) {
	call, err := r.beginWorkerControlPhase(ctx, controlBoundAck, nil)
	if err != nil {
		return err
	}
	normal := false
	defer r.publishWorkerControlResult(call, &normal, &resultErr)
	defer r.state.census.finish(call.op, &normal, &resultErr)
	resultErr = r.writeWorkerControlFrame(ctx, call, frame)
	normal = true
	return resultErr
}
func (r *ControlOriginal) ReceiveWorkerCurrentChallengeOriginal(destination **WorkerChallengeRead, ctx context.Context) (resultErr error) {
	call, err := r.beginWorkerControlPhase(ctx, controlChallenge, destination)
	if err != nil {
		return err
	}
	normal := false
	defer r.publishWorkerControlResult(call, &normal, &resultErr)
	defer r.state.census.finish(call.op, &normal, &resultErr)
	resultErr = r.readWorkerControlFrame(ctx, call)
	normal = true
	return resultErr
}
func (r *ControlOriginal) SendWorkerCurrentProofOriginal(ctx context.Context, frame []byte) (resultErr error) {
	call, err := r.beginWorkerControlPhase(ctx, controlProof, nil)
	if err != nil {
		return err
	}
	normal := false
	defer r.publishWorkerControlResult(call, &normal, &resultErr)
	defer r.state.census.finish(call.op, &normal, &resultErr)
	resultErr = r.writeWorkerControlFrame(ctx, call, frame)
	normal = true
	return resultErr
}

func (r *ServiceOriginal) originalWorkerChallengeLocked(read *WorkerChallengeRead) bool {
	if r == nil || r.self != r || r.state == nil || r.state.census == nil || r.state.original != r || read == nil || read.self != read || !read.control.originalWorkerControl() {
		return false
	}
	s := r.state.census
	c := read.control
	if c.state.census != s || s.service != r || s.firstError != nil || r.state.firstError != nil || c.state.firstError != nil || !s.sealed || !s.delegated || !s.self.ownsProcessClaim() || c.state.workerControlPhases[controlChallenge] != read.call || read.call == nil || read.call.challenge != read || c.state.workerControlPhases[controlProof] != nil || s.active != nil && s.active.recipient == c.state {
		return false
	}
	for i := controlBootstrap; i <= controlChallenge; i++ {
		if !workerControlCallCompleteLocked(c.state.workerControlPhases[i], c, i) {
			return false
		}
	}
	return true
}

// Pure original read/phase linkage. NO current ctx.Err check after normal finish:
// its own later IO-scope cancellation cannot revoke completed normal read facts.
func (r *ServiceOriginal) ValidateOriginalWorkerChallengePhase(read *WorkerChallengeRead) error {
	if r == nil || r.self != r || r.state == nil || r.state.census == nil {
		return ErrOriginal
	}
	s := r.state.census
	s.mu.Lock()
	defer s.mu.Unlock()
	if !r.originalWorkerChallengeLocked(read) {
		return ErrOriginal
	}
	return nil
}
func (r *ServiceOriginal) OriginalWorkerChallengePayloadData(read *WorkerChallengeRead, expectedAckFrame []byte) ([]byte, error) {
	if r == nil || r.self != r || r.state == nil || r.state.census == nil || len(expectedAckFrame) <= wireHeaderBytes || len(expectedAckFrame) > MaxWireBytes {
		return nil, ErrOriginal
	}
	s := r.state.census
	s.mu.Lock()
	defer s.mu.Unlock()
	if !r.originalWorkerChallengeLocked(read) || !bytes.Equal(read.control.state.workerControlPhases[controlBoundAck].frame, expectedAckFrame) {
		return nil, ErrOriginal
	}
	return append([]byte(nil), read.call.frame[wireHeaderBytes:]...), nil
}
