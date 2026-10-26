package kubeprovision

import (
	"context"
	"errors"
	"io"
	"sync"
)

// streamInput and streamOutput seal user-owned I/O before Exec returns. The
// upstream SPDY executor may return on context cancellation before its stdin
// copier exits; sealing prevents that copier touching a caller's source later.
type streamInput struct {
	mu     sync.Mutex
	reader io.Reader
	closed bool
}

func (s *streamInput) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, io.ErrClosedPipe
	}
	return s.reader.Read(p)
}
func (s *streamInput) seal() { s.mu.Lock(); s.closed = true; s.mu.Unlock() }

type streamOutput struct {
	mu     sync.Mutex
	writer io.Writer
	closed bool
	err    error
	cancel context.CancelFunc
}

func (s *streamOutput) Write(p []byte) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	n, err := s.writer.Write(p)
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	if err != nil && s.err == nil {
		s.err = err
	}
	cancel := s.cancel
	s.mu.Unlock()
	if err != nil {
		cancel()
	}
	return n, err
}
func (s *streamOutput) seal() error { s.mu.Lock(); defer s.mu.Unlock(); s.closed = true; return s.err }

func streamError(result error, outputs ...*streamOutput) error {
	for _, output := range outputs {
		if output != nil {
			if err := output.seal(); err != nil {
				result = errors.Join(result, err)
			}
		}
	}
	return result
}
