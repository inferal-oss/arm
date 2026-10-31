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
	parentBootstrap = iota
	parentBoundAck
	parentChallenge
	parentProof
)

var parentControlKinds = [4]string{"p1-transfer", "p1-bound-ack-read", "p1-current-challenge-write", "p1-current-proof-read"}

type p1ParentControlIO struct {
	offset, requested, returned int
	returnedKnown               bool
	err                         error
}
type p1ParentControlCall struct {
	owner                               *P1ServiceGroup
	op                                  *operation
	index                               int
	endpoint                            *retainedFD
	bootstrap                           *channelOperation
	frame                               []byte
	io                                  [32]p1ParentControlIO
	calls, transferred                  int
	bytesComplete, frameVerified        bool
	deadline                            time.Time
	clockAttempted, clockReturned       bool
	observedAt                          time.Time
	deadlineAttempted, deadlineReturned bool
	deadlineError                       error
	normalReturned                      bool
	resultError                         error
	read                                *P1ParentRead
}

// Actual original read operation, published before IO; no from-DATA constructor.
type P1ParentRead struct {
	self  *P1ParentRead
	group *P1ServiceGroup
	call  *p1ParentControlCall
}

func (P1ParentRead) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (*P1ParentRead) UnmarshalJSON([]byte) error  { return ErrInvalid }
func parentControlPrefixData(prior []string, next string) bool {
	if len(prior) >= len(parentControlKinds) || next != parentControlKinds[len(prior)] {
		return false
	}
	for i, kind := range prior {
		if kind != parentControlKinds[i] {
			return false
		}
	}
	return true
}
func (p *P1ServiceGroup) parentControlCompleteLocked(call *p1ParentControlCall, index int) bool {
	return call != nil && call.owner == p && call.index == index && call.op != nil && call.op.owner == p.parent.owner && call.op.kind == parentControlKinds[index] && call.op.normalFinished && !call.op.abnormal && call.normalReturned && call.resultError == nil && call.frameVerified && call.bytesComplete && call.transferred == len(call.frame)
}
func (p *P1ServiceGroup) registerParentControlCall(op *operation, index int, destination **P1ParentRead) (*p1ParentControlCall, error) {
	o := p.parent.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != op || op.owner != o || index < 0 || index >= 4 || p.controlPhases[index] != nil {
		return nil, ErrInvalid
	}
	prior := make([]string, 0, index)
	for i := 0; i < index; i++ {
		if !p.parentControlCompleteLocked(p.controlPhases[i], i) {
			return nil, ErrInvalid
		}
		prior = append(prior, p.controlPhases[i].op.kind)
	}
	if !parentControlPrefixData(prior, parentControlKinds[index]) {
		return nil, ErrInvalid
	}
	if index > 0 && (p.controlPhases[parentBootstrap].bootstrap == nil || p.controlPhases[parentBootstrap].bootstrap != o.channel || o.channel.owner != o || !o.channel.writeComplete || o.channel.firstError != nil || o.channel.transferred != len(o.channel.bytes)) {
		return nil, ErrInvalid
	}
	call := &p1ParentControlCall{owner: p, op: op, index: index, endpoint: o.files.parentChannel}
	p.controlPhases[index] = call
	if index == parentBoundAck || index == parentProof {
		if destination == nil || *destination != nil {
			return nil, ErrInvalid
		}
		read := &P1ParentRead{group: p, call: call}
		read.self = read
		call.read = read
		*destination = read
	}
	return call, nil
}
func (p *P1ServiceGroup) beginParentControl(ctx context.Context, command *exec.Cmd, index int, destination **P1ParentRead) (*p1ParentControlCall, error) {
	if p == nil || p.p1ServiceState == nil || p.self != p || command != p.command || ctx == nil || index <= parentBootstrap || index >= 4 {
		return nil, ErrInvalid
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrInvalid
	}
	if (index == parentBoundAck || index == parentProof) && (destination == nil || *destination != nil) {
		return nil, ErrInvalid
	}
	op, err := p.begin(parentControlKinds[index])
	if err != nil {
		return nil, err
	}
	call, err := p.registerParentControlCall(op, index, destination)
	if err != nil {
		normal := true
		p.parent.owner.finish(op, &normal, &err)
		return nil, err
	}
	return call, nil
}
func (p *P1ServiceGroup) publishParentControlResult(call *p1ParentControlCall, normal *bool, resultErr *error) {
	o := p.parent.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if call == nil || call.owner != p || call.index < 0 || call.index >= 4 || p.controlPhases[call.index] != call || call.op == nil || call.op.owner != o || call.op.kind != parentControlKinds[call.index] {
		panic("original parent control identity lost")
	}
	call.normalReturned = *normal && !call.op.abnormal && call.op.normalFinished
	call.resultError = *resultErr
}
func (p *P1ServiceGroup) parentControlDeadline(ctx context.Context, call *p1ParentControlCall, writing bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if call.endpoint == nil || call.endpoint != p.parent.owner.files.parentChannel || call.endpoint.file == nil || call.endpoint.closeTried {
		return ErrInvalid
	}
	call.deadline, _ = ctx.Deadline()
	if call.index != parentBootstrap {
		call.clockAttempted = true
		call.observedAt = time.Now()
		call.clockReturned = true
		if call.deadline.After(call.observedAt.Add(5 * time.Second)) {
			return ErrInvalid
		}
	}
	p.parent.owner.files.progress("original_P1_phase_endpoint_deadline_pending")
	call.deadlineAttempted = true
	if writing {
		call.deadlineError = call.endpoint.file.SetWriteDeadline(call.deadline)
	} else {
		call.deadlineError = call.endpoint.file.SetReadDeadline(call.deadline)
	}
	call.deadlineReturned = true
	return call.deadlineError
}
func (p *P1ServiceGroup) writeParentControl(ctx context.Context, call *p1ParentControlCall, frame []byte) error {
	if err := validateP1ParentFrameData(frame); err != nil {
		return err
	}
	call.frame = append([]byte(nil), frame...)
	call.frameVerified = true
	if call.bootstrap != nil {
		o := p.parent.owner
		o.mu.Lock()
		call.bootstrap.bytes = call.frame
		o.mu.Unlock()
	}
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
		p.parent.owner.files.progress("original_P1_phase_write_pending")
		if call.bootstrap != nil {
			o := p.parent.owner
			o.mu.Lock()
			call.bootstrap.writes++
			o.mu.Unlock()
		}
		n, err := call.endpoint.file.Write(call.frame[call.transferred:])
		c.returned, c.err, c.returnedKnown = n, err, true
		if n < 0 || n > c.requested {
			return ErrInvalid
		}
		call.transferred += n
		if call.bootstrap != nil {
			o := p.parent.owner
			o.mu.Lock()
			call.bootstrap.transferred = call.transferred
			o.mu.Unlock()
		}
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
func (p *P1ServiceGroup) readParentControl(ctx context.Context, call *p1ParentControlCall) error {
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
			p.parent.owner.files.progress("original_P1_phase_read_pending")
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
func (p *P1ServiceGroup) ReceiveP1BoundAckOriginal(destination **P1ParentRead, ctx context.Context, command *exec.Cmd) (resultErr error) {
	call, err := p.beginParentControl(ctx, command, parentBoundAck, destination)
	if err != nil {
		return err
	}
	normal := false
	defer p.publishParentControlResult(call, &normal, &resultErr)
	defer p.parent.owner.finish(call.op, &normal, &resultErr)
	resultErr = p.readParentControl(ctx, call)
	normal = true
	return resultErr
}
func (p *P1ServiceGroup) SendP1CurrentChallengeOriginal(ctx context.Context, command *exec.Cmd, frame []byte) (resultErr error) {
	call, err := p.beginParentControl(ctx, command, parentChallenge, nil)
	if err != nil {
		return err
	}
	normal := false
	defer p.publishParentControlResult(call, &normal, &resultErr)
	defer p.parent.owner.finish(call.op, &normal, &resultErr)
	resultErr = p.writeParentControl(ctx, call, frame)
	normal = true
	return resultErr
}
func (p *P1ServiceGroup) ReceiveP1CurrentProofOriginal(destination **P1ParentRead, ctx context.Context, command *exec.Cmd) (resultErr error) {
	call, err := p.beginParentControl(ctx, command, parentProof, destination)
	if err != nil {
		return err
	}
	normal := false
	defer p.publishParentControlResult(call, &normal, &resultErr)
	defer p.parent.owner.finish(call.op, &normal, &resultErr)
	resultErr = p.readParentControl(ctx, call)
	normal = true
	return resultErr
}
func (r *P1ParentRead) payloadOriginalData(index int) ([]byte, error) {
	if r == nil || r.self != r || r.group == nil || r.group.p1ServiceState == nil || r.group.self != r.group || r.group.parent == nil || r.group.parent.owner == nil {
		return nil, ErrInvalid
	}
	p := r.group
	o := p.parent.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.p1 != p || o.firstError != nil || r.call == nil || r.call.read != r || p.controlPhases[index] != r.call || !p.parentControlCompleteLocked(r.call, index) {
		return nil, ErrInvalid
	}
	return append([]byte(nil), r.call.frame[36:]...), nil
}
func (r *P1ParentRead) BoundAckPayloadOriginalData() ([]byte, error) {
	return r.payloadOriginalData(parentBoundAck)
}
func (r *P1ParentRead) CurrentProofPayloadOriginalData() ([]byte, error) {
	return r.payloadOriginalData(parentProof)
}
