package syncworker

import (
	"context"
	"errors"
	"io"
	"sync"
)

// singleStreamSession runs the fixed control protocol and each sequential
// protocol-27 batch on one already authenticated, admitted SSH exec stream.
// The source caller's waiter must observe the actual SSH command exit status;
// a destination worker with no child transport may use a completed waiter.
// A batch handle does not close the stream on normal completion; the
// control protocol resumes at the next byte after that rsync exchange.
type singleStreamSession struct {
	stream         io.ReadWriteCloser
	wait           func() error
	abortTransport func() error
	mu             sync.Mutex
	busy           bool
	closed         bool
	joined         bool
	closeOnce      sync.Once
	closeErr       error
	abortOnce      sync.Once
	abortErr       error
	joinOnce       sync.Once
	joinErr        error
}

func newSingleStreamSession(stream io.ReadWriteCloser, wait func() error) (*singleStreamSession, error) {
	if stream == nil || wait == nil {
		return nil, errors.New("owned SSH exec stream and waiter required")
	}
	return newOwnedSingleStreamSession(stream, wait, stream.Close)
}

// Production bridges provide the captured original SSH carrier closer. A raw
// Channel.Close alone can block and cannot establish transport termination.
func newOwnedSingleStreamSession(stream io.ReadWriteCloser, wait, abort func() error) (*singleStreamSession, error) {
	if stream == nil || wait == nil || abort == nil {
		return nil, errors.New("original SSH stream, waiter and transport closer required")
	}
	return &singleStreamSession{stream: stream, wait: wait, abortTransport: abort}, nil
}

func (s *singleStreamSession) Control() io.ReadWriteCloser { return s.stream }
func (s *singleStreamSession) OpenTransfer(ctx context.Context, _ batch) (transferHandle, error) {
	if ctx == nil {
		return nil, errors.New("transfer context required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.busy || s.joined {
		return nil, errors.New("SSH transfer stream unavailable")
	}
	s.busy = true
	return &singleStreamHandle{session: s, ctx: ctx}, nil
}
func (s *singleStreamSession) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		if half, ok := s.stream.(interface{ CloseWrite() error }); ok {
			s.closeErr = half.CloseWrite()
		} else {
			s.closeErr = s.stream.Close()
		}
	})
	return s.closeErr
}
func (s *singleStreamSession) Abort() error {
	s.abortOnce.Do(func() { s.mu.Lock(); s.closed = true; s.mu.Unlock(); s.abortErr = s.abortTransport() })
	return s.abortErr
}
func (s *singleStreamSession) Join() error {
	s.joinOnce.Do(func() {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if !closed {
			s.joinErr = errors.New("SSH stream must close before join")
			return
		}
		s.joinErr = s.wait()
		s.mu.Lock()
		s.joined = true
		s.mu.Unlock()
	})
	return s.joinErr
}

type singleStreamHandle struct {
	session *singleStreamSession
	ctx     context.Context
	once    sync.Once
}

func (h *singleStreamHandle) Read(p []byte) (int, error)  { return h.session.stream.Read(p) }
func (h *singleStreamHandle) Write(p []byte) (int, error) { return h.session.stream.Write(p) }
func (h *singleStreamHandle) Close() error {
	if h.ctx.Err() != nil {
		return h.session.Abort()
	}
	return nil
}

// Abort is the exact session capability minted with this original handle.
func (h *singleStreamHandle) Abort() error { return h.session.Abort() }
func (h *singleStreamHandle) Join() error {
	h.once.Do(func() { h.session.mu.Lock(); h.session.busy = false; h.session.mu.Unlock() })
	return nil
}
