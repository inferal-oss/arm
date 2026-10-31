package residentrefs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
)

// Includes all header/checksum bytes. A 16KiB payload plus header is refused.
const MaxWireBytes = 16 << 10
const wireHeaderBytes = 36

// DATA encoding only. It issues no admission, birth, Ready, ACK or closure.
func EncodeFrameData(payload []byte) ([]byte, error) {
	if len(payload) == 0 || len(payload) > MaxWireBytes-wireHeaderBytes {
		return nil, ErrOriginal
	}
	frame := make([]byte, wireHeaderBytes+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	digest := sha256.Sum256(payload)
	copy(frame[4:36], digest[:])
	copy(frame[36:], payload)
	return frame, nil
}
func (r *ControlOriginal) beginChannel(ctx context.Context, writing bool) (*originalOperation, error) {
	if r == nil || r.self != r || r.state == nil {
		return nil, ErrOriginal
	}
	s := r.state.census
	// All channel operations require a finite original context; no unbounded
	// blocking Read/Write or cancellation goroutine is introduced.
	if ctx == nil {
		return nil, ErrOriginal
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrOriginal
	}
	kind := "channel_read"
	if writing {
		kind = "channel_write"
	}
	op, err := r.state.beginView(ctx, kind, r.state.entries[0])
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	duplicate := r.state.readTried
	if writing {
		duplicate = r.state.writeTried
		r.state.writeTried = true
	} else {
		r.state.readTried = true
	}
	s.mu.Unlock()
	if duplicate {
		// This is still an original operation: no deferred native effect is lost.
		resultErr := ErrOriginal
		normal := true
		s.finish(op, &normal, &resultErr)
		return nil, resultErr
	}
	return op, nil
}
func (r *ControlOriginal) ReceiveOriginalData(ctx context.Context) (payload []byte, resultErr error) {
	op, err := r.beginChannel(ctx, false)
	if err != nil {
		return nil, err
	}
	s := r.state.census
	normal := false
	defer s.finish(op, &normal, &resultErr)
	op.bytes = make([]byte, wireHeaderBytes)
	resultErr = func() error {
		deadline, _ := ctx.Deadline()
		if err := s.before(op, "original_channel_read_deadline_pending"); err != nil {
			return err
		}
		if err := op.entry.file.SetReadDeadline(deadline); err != nil {
			return err
		}
		at, calls := 0, 0
		readTo := func(want int) error {
			for at < want {
				if err := s.before(op, "original_channel_read_pending"); err != nil {
					return err
				}
				if calls >= 32 {
					return io.ErrNoProgress
				}
				calls++
				n, err := op.entry.file.Read(op.bytes[at:want])
				at += n
				s.mu.Lock()
				op.returned, op.ioError = at, err
				s.mu.Unlock()
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
		length := int(binary.BigEndian.Uint32(op.bytes[:4]))
		if length <= 0 || length > MaxWireBytes-wireHeaderBytes {
			return ErrOriginal
		}
		op.bytes = append(op.bytes, make([]byte, length)...)
		if err := readTo(wireHeaderBytes + length); err != nil {
			return err
		}
		digest := sha256.Sum256(op.bytes[wireHeaderBytes:])
		if !bytes.Equal(digest[:], op.bytes[4:36]) {
			return ErrOriginal
		}
		payload = op.bytes[wireHeaderBytes:]
		return nil
	}()
	normal = true
	return payload, resultErr
}

// Original root must validate its actual component/native/Manager/DB/Server/
// funding bindings before sending its authenticated ACK payload. This method only
// writes bounded DATA on the original channel; a full write is not an ACK proof.
func (r *ControlOriginal) SendOriginalData(ctx context.Context, payload []byte) (resultErr error) {
	op, err := r.beginChannel(ctx, true)
	if err != nil {
		return err
	}
	s := r.state.census
	normal := false
	defer s.finish(op, &normal, &resultErr)
	op.bytes, resultErr = EncodeFrameData(payload)
	if resultErr == nil {
		resultErr = func() error {
			deadline, _ := ctx.Deadline()
			if err := s.before(op, "original_channel_write_deadline_pending"); err != nil {
				return err
			}
			if err := op.entry.file.SetWriteDeadline(deadline); err != nil {
				return err
			}
			for at, calls := 0, 0; at < len(op.bytes); calls++ {
				if err := s.before(op, "original_channel_write_pending"); err != nil {
					return err
				}
				if calls >= 32 {
					return io.ErrShortWrite
				}
				n, err := op.entry.file.Write(op.bytes[at:])
				at += n
				s.mu.Lock()
				op.returned, op.ioError = at, err
				s.mu.Unlock()
				if err != nil {
					return err
				}
				if n == 0 {
					return io.ErrNoProgress
				}
			}
			return nil
		}()
	}
	normal = true
	return resultErr
}
