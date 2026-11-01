package residentservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"os/exec"
	"time"

	"getarm.org/internal/residentrefs"
)

const (
	workerParentBootstrap = iota
	workerParentBoundAck
	workerParentChallenge
	workerParentProof
)

var workerParentControlKinds = [4]string{"worker-parent-bootstrap-write", "worker-parent-ack-read", "worker-parent-challenge-write", "worker-parent-proof-read"}

type workerParentControlIO struct {
	offset, requested, returned int
	returnedKnown               bool
	err                         error
}
type workerParentControlCall struct {
	owner                               *WorkerServiceGroup
	op                                  *operation
	index                               int
	endpoint                            *retainedFD
	frame                               []byte
	io                                  [32]workerParentControlIO
	calls, transferred                  int
	bytesComplete, frameVerified        bool
	deadline                            time.Time
	clockAttempted, clockReturned       bool
	observedAt                          time.Time
	deadlineAttempted, deadlineReturned bool
	deadlineError                       error
	normalReturned                      bool
	resultError                         error
	read                                *WorkerParentRead
}

// Actual original read operation, published before IO; no from-DATA constructor.
type WorkerParentRead struct {
	self  *WorkerParentRead
	group *WorkerServiceGroup
	call  *workerParentControlCall
}

func (WorkerParentRead) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (*WorkerParentRead) UnmarshalJSON([]byte) error  { return ErrInvalid }
func workerParentControlPrefixData(prior []string, next string) bool {
	if len(prior) >= len(workerParentControlKinds) || next != workerParentControlKinds[len(prior)] {
		return false
	}
	for i, kind := range prior {
		if kind != workerParentControlKinds[i] {
			return false
		}
	}
	return true
}
func (p *WorkerServiceGroup) parentControlCompleteLocked(call *workerParentControlCall, index int) bool {
	return call != nil && call.owner == p && call.index == index && call.op != nil && call.op.owner == p.parent.custody.raw && call.op.kind == workerParentControlKinds[index] && call.op.normalFinished && !call.op.abnormal && call.normalReturned && call.resultError == nil && call.frameVerified && call.bytesComplete && call.transferred == len(call.frame)
}
func (p *WorkerServiceGroup) registerParentControlCall(op *operation, index int, destination **WorkerParentRead) (*workerParentControlCall, error) {
	o := p.parent.custody.raw
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != op || op.owner != o || index < 0 || index >= 4 || p.controlPhases[index] != nil {
		return nil, ErrInvalid
	}
	if index >= workerParentChallenge && !p.challengeCompletedLocked() {
		return nil, ErrInvalid
	}
	prior := make([]string, 0, index)
	for i := 0; i < index; i++ {
		if !p.parentControlCompleteLocked(p.controlPhases[i], i) {
			return nil, ErrInvalid
		}
		prior = append(prior, p.controlPhases[i].op.kind)
	}
	if !workerParentControlPrefixData(prior, workerParentControlKinds[index]) {
		return nil, ErrInvalid
	}
	call := &workerParentControlCall{owner: p, op: op, index: index, endpoint: o.files.parentChannel}
	p.controlPhases[index] = call
	if index == workerParentBoundAck || index == workerParentProof {
		if destination == nil || *destination != nil {
			return nil, ErrInvalid
		}
		read := &WorkerParentRead{group: p, call: call}
		read.self = read
		call.read = read
		*destination = read
	}
	return call, nil
}
func (p *WorkerServiceGroup) beginParentControl(ctx context.Context, command *exec.Cmd, index int, destination **WorkerParentRead) (*workerParentControlCall, error) {
	if p == nil || p.self != p || p.parent == nil || p.parent.custody == nil || command != p.command || command == nil || command.Process == nil || ctx == nil || !p.shortSealed || !p.writerWithdrawn || index < workerParentBootstrap || index >= 4 {
		return nil, ErrInvalid
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrInvalid
	}
	if (index == workerParentBoundAck || index == workerParentProof) && (destination == nil || *destination != nil) {
		return nil, ErrInvalid
	}
	op, err := p.beginOriginal(workerParentControlKinds[index])
	if err != nil {
		return nil, err
	}
	call, err := p.registerParentControlCall(op, index, destination)
	if err != nil {
		normal := true
		p.parent.custody.raw.finish(op, &normal, &err)
		return nil, err
	}
	return call, nil
}
func (p *WorkerServiceGroup) publishParentControlResult(call *workerParentControlCall, normal *bool, resultErr *error) {
	o := p.parent.custody.raw
	o.mu.Lock()
	defer o.mu.Unlock()
	if call == nil || call.owner != p || call.index < 0 || call.index >= 4 || p.controlPhases[call.index] != call || call.op == nil || call.op.owner != o || call.op.kind != workerParentControlKinds[call.index] {
		panic("original parent control identity lost")
	}
	call.normalReturned = *normal && !call.op.abnormal && call.op.normalFinished
	call.resultError = *resultErr
}
func (p *WorkerServiceGroup) parentControlDeadline(ctx context.Context, call *workerParentControlCall, writing bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if call.endpoint == nil || call.endpoint != p.parent.custody.raw.files.parentChannel || call.endpoint.file == nil || call.endpoint.closeTried {
		return ErrInvalid
	}
	call.deadline, _ = ctx.Deadline()
	if call.index != workerParentBootstrap {
		call.clockAttempted = true
		call.observedAt = time.Now()
		call.clockReturned = true
		if call.deadline.After(call.observedAt.Add(5 * time.Second)) {
			return ErrInvalid
		}
	}
	p.parent.custody.raw.files.progress("worker_original_phase_endpoint_deadline_pending")
	call.deadlineAttempted = true
	if writing {
		call.deadlineError = call.endpoint.file.SetWriteDeadline(call.deadline)
	} else {
		call.deadlineError = call.endpoint.file.SetReadDeadline(call.deadline)
	}
	call.deadlineReturned = true
	return call.deadlineError
}
func (p *WorkerServiceGroup) writeParentControl(ctx context.Context, call *workerParentControlCall, frame []byte) error {
	if call.index == workerParentChallenge && (p.challenge == nil || !bytes.Equal(frame, p.challenge.frame)) {
		return ErrInvalid
	}
	if err := validateWorkerParentFrameData(frame); err != nil {
		return err
	}
	call.frame = append([]byte(nil), frame...)
	call.frameVerified = true
	if err := p.parentControlDeadline(ctx, call, true); err != nil {
		return err
	}
	for call.transferred < len(call.frame) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if call.calls >= len(call.io) {
			return io.ErrShortWrite
		}
		c := &call.io[call.calls]
		c.offset, c.requested = call.transferred, len(call.frame)-call.transferred
		call.calls++
		p.parent.custody.raw.files.progress("worker_original_phase_write_pending")
		n, err := call.endpoint.file.Write(call.frame[call.transferred:])
		c.returned, c.err, c.returnedKnown = n, err, true
		if n < 0 || n > c.requested {
			return ErrInvalid
		}
		call.transferred += n
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	call.bytesComplete = true
	return ctx.Err()
}
func (p *WorkerServiceGroup) readParentControl(ctx context.Context, call *workerParentControlCall) error {
	call.frame = make([]byte, 36)
	if err := p.parentControlDeadline(ctx, call, false); err != nil {
		return err
	}
	readTo := func(want int) error {
		for call.transferred < want {
			if err := ctx.Err(); err != nil {
				return err
			}
			if call.calls >= len(call.io) {
				return io.ErrNoProgress
			}
			c := &call.io[call.calls]
			c.offset, c.requested = call.transferred, want-call.transferred
			call.calls++
			p.parent.custody.raw.files.progress("worker_original_phase_read_pending")
			n, err := call.endpoint.file.Read(call.frame[call.transferred:want])
			c.returned, c.err, c.returnedKnown = n, err, true
			if n < 0 || n > c.requested {
				return ErrInvalid
			}
			call.transferred += n
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrNoProgress
			}
		}
		return nil
	}
	if err := readTo(36); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(call.frame[:4])
	if length == 0 || length > residentrefs.MaxWireBytes-36 {
		return ErrInvalid
	}
	call.frame = append(call.frame, make([]byte, int(length))...)
	if err := readTo(len(call.frame)); err != nil {
		return err
	}
	call.bytesComplete = true
	h := sha256.Sum256(call.frame[36:])
	if !bytes.Equal(h[:], call.frame[4:36]) {
		return ErrInvalid
	}
	call.frameVerified = true
	return ctx.Err()
}

// Transport facts only. C4d MUST gate these sole future Root callsites using its
// OWN normal Start/returned Process/bootstrap and native/journal/auth chain.
// A projected FullWrite/nonnil Process/received DTO is never that Root gate.
func (p *WorkerServiceGroup) ReceiveWorkerBoundAckOriginal(destination **WorkerParentRead, ctx context.Context, command *exec.Cmd) (resultErr error) {
	call, err := p.beginParentControl(ctx, command, workerParentBoundAck, destination)
	if err != nil {
		return err
	}
	normal := false
	defer p.publishParentControlResult(call, &normal, &resultErr)
	defer p.parent.custody.raw.finish(call.op, &normal, &resultErr)
	resultErr = p.readParentControl(ctx, call)
	normal = true
	return resultErr
}
func (p *WorkerServiceGroup) SendWorkerCurrentChallengeOriginal(ctx context.Context, command *exec.Cmd, frame []byte) (resultErr error) {
	call, err := p.beginParentControl(ctx, command, workerParentChallenge, nil)
	if err != nil {
		return err
	}
	normal := false
	defer p.publishParentControlResult(call, &normal, &resultErr)
	defer p.parent.custody.raw.finish(call.op, &normal, &resultErr)
	resultErr = p.writeParentControl(ctx, call, frame)
	normal = true
	return resultErr
}
func (p *WorkerServiceGroup) ReceiveWorkerCurrentProofOriginal(destination **WorkerParentRead, ctx context.Context, command *exec.Cmd) (resultErr error) {
	call, err := p.beginParentControl(ctx, command, workerParentProof, destination)
	if err != nil {
		return err
	}
	normal := false
	defer p.publishParentControlResult(call, &normal, &resultErr)
	defer p.parent.custody.raw.finish(call.op, &normal, &resultErr)
	resultErr = p.readParentControl(ctx, call)
	normal = true
	return resultErr
}
func (r *WorkerParentRead) payloadOriginalData(index int) ([]byte, error) {
	if r == nil || r.self != r || r.group == nil || r.group.self != r.group || r.group.parent == nil || r.group.parent.custody == nil || r.group.parent.custody.raw == nil {
		return nil, ErrInvalid
	}
	p := r.group
	o := p.parent.custody.raw
	o.mu.Lock()
	defer o.mu.Unlock()
	if p.parent.group != p || o.firstError != nil || r.call == nil || r.call.read != r || p.controlPhases[index] != r.call || !p.parentControlCompleteLocked(r.call, index) {
		return nil, ErrInvalid
	}
	return append([]byte(nil), r.call.frame[36:]...), nil
}
func (r *WorkerParentRead) BoundAckPayloadOriginalData() ([]byte, error) {
	return r.payloadOriginalData(workerParentBoundAck)
}
func (r *WorkerParentRead) CurrentProofPayloadOriginalData() ([]byte, error) {
	return r.payloadOriginalData(workerParentProof)
}

func (p *WorkerServiceGroup) SendWorkerBootstrapOriginal(ctx context.Context, command *exec.Cmd, frame []byte) (resultErr error) {
	if validateWorkerParentFrameData(frame) != nil {
		return ErrInvalid
	}
	call, err := p.beginParentControl(ctx, command, workerParentBootstrap, nil)
	if err != nil {
		return err
	}
	normal := false
	defer p.publishParentControlResult(call, &normal, &resultErr)
	defer p.parent.custody.raw.finish(call.op, &normal, &resultErr)
	resultErr = p.writeParentControl(ctx, call, frame)
	normal = true
	return resultErr
}

func validateWorkerParentFrameData(frame []byte) error {
	if len(frame) <= 36 || len(frame) > residentrefs.MaxWireBytes || int(binary.BigEndian.Uint32(frame[:4])) != len(frame)-36 {
		return ErrInvalid
	}
	sum := sha256.Sum256(frame[36:])
	if !bytes.Equal(sum[:], frame[4:36]) {
		return ErrInvalid
	}
	return nil
}
