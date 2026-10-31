package server

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"getarm.org/internal/manager"
	"getarm.org/internal/meshforward"
	"getarm.org/internal/meshsshrelay"
	"getarm.org/internal/model"
)

const (
	forwardUnixMaxWorkers = 8
	forwardUnixMaxPairs   = 16
	forwardUnixPairsEach  = 2
	forwardUnixOpenLimit  = 15 * time.Second
	forwardUnixCopyBytes  = 32 << 10
)

// These maps retain actual original owners. An uncertain native close keeps
// its pair and worker registered rather than recycling a nominal slot.
var forwardUnixProcess struct {
	sync.Mutex
	workers map[*forwardUnixWorker]struct{}
	pending map[*forwardUnixWorker]struct{}
	pairs   map[*forwardUnixWorkerPair]struct{}
}

type forwardUnixWorker struct {
	source         *forwardSource
	open           func(context.Context) (*meshforward.AdmittedRoute, error)
	bound          *manager.ForwardUnixBound
	binding        model.ForwardSourceBinding
	ctx            context.Context
	cancel         context.CancelFunc
	joined         chan struct{}
	done           chan struct{}
	mu             sync.Mutex
	active         map[*forwardUnixWorkerPair]struct{}
	wg             sync.WaitGroup
	closeMu        sync.Mutex
	closed         bool
	cleanupErr     error
	applicationErr error
}

type forwardUnixWorkerPair struct {
	worker     *forwardUnixWorker
	local      *net.UnixConn
	mu         sync.Mutex
	remote     *meshforward.AdmittedRoute
	aborted    bool
	closeLocal sync.Once
	closePeer  sync.Once
	nativeErr  error
	remoteErr  error
}

// startPrivateUnixWorker is deliberately absent from public start/dispatch.
// The caller supplies the original pair; BindOriginal owns its native socket
// and this worker owns the exact channel supplied for its actual joins.
func (r *forwardSource) startPrivateUnixWorker(ctx context.Context, pair *manager.ForwardUnixPair) (*forwardUnixWorker, error) {
	if r == nil || r.handle == nil || pair == nil || ctx == nil || ctx.Err() != nil {
		return nil, model.Err("invalid_request", "original forward source and pair required")
	}
	joined := make(chan struct{})
	workerCtx, cancel := context.WithCancel(r.handle.Context())
	w := &forwardUnixWorker{source: r, open: r.openAdmitted, ctx: workerCtx, cancel: cancel, joined: joined, done: make(chan struct{}), active: make(map[*forwardUnixWorkerPair]struct{})}
	forwardUnixProcess.Lock()
	if forwardUnixProcess.workers == nil {
		forwardUnixProcess.workers = make(map[*forwardUnixWorker]struct{})
	}
	if len(forwardUnixProcess.workers) >= forwardUnixMaxWorkers {
		forwardUnixProcess.Unlock()
		cancel()
		return nil, model.Err("resource_unavailable", "forward Unix source worker capacity exhausted")
	}
	forwardUnixProcess.workers[w] = struct{}{}
	forwardUnixProcess.Unlock()
	bound, err := pair.BindOriginal(ctx, joined)
	if err != nil {
		forwardUnixProcess.Lock()
		delete(forwardUnixProcess.workers, w)
		forwardUnixProcess.Unlock()
		cancel()
		return nil, err
	}
	w.bound = bound
	w.binding, err = bound.SourceBinding(ctx)
	if err == nil && (w.binding != r.binding || bound.SourceLeaseID() != r.handle.LeaseID()) {
		err = model.Err("forbidden", "forward Unix original source differs")
	}
	if err != nil {
		close(joined)
		cleanupErr := bound.CloseAfterJoin(context.Background())
		cancel()
		if cleanupErr == nil {
			forwardUnixProcess.Lock()
			delete(forwardUnixProcess.workers, w)
			forwardUnixProcess.Unlock()
		}
		return nil, errors.Join(err, cleanupErr)
	}
	go w.run()
	return w, nil
}

func (w *forwardUnixWorker) addCleanup(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	if w.cleanupErr == nil {
		w.cleanupErr = err
	}
	w.mu.Unlock()
}

func (w *forwardUnixWorker) addApplication(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	if w.applicationErr == nil {
		w.applicationErr = err
	}
	w.mu.Unlock()
}

func (w *forwardUnixWorker) reserveAccept() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	forwardUnixProcess.Lock()
	defer forwardUnixProcess.Unlock()
	if w.ctx.Err() != nil || len(w.active) >= forwardUnixPairsEach || len(forwardUnixProcess.pairs)+len(forwardUnixProcess.pending) >= forwardUnixMaxPairs {
		return false
	}
	if forwardUnixProcess.pending == nil {
		forwardUnixProcess.pending = make(map[*forwardUnixWorker]struct{})
	}
	forwardUnixProcess.pending[w] = struct{}{}
	return true
}

func (w *forwardUnixWorker) releaseAccept() {
	forwardUnixProcess.Lock()
	delete(forwardUnixProcess.pending, w)
	forwardUnixProcess.Unlock()
}

func (w *forwardUnixWorker) reserve(local *net.UnixConn) *forwardUnixWorkerPair {
	w.mu.Lock()
	defer w.mu.Unlock()
	forwardUnixProcess.Lock()
	defer forwardUnixProcess.Unlock()
	if _, ok := forwardUnixProcess.pending[w]; !ok {
		return nil
	}
	delete(forwardUnixProcess.pending, w)
	if w.ctx.Err() != nil || len(w.active) >= forwardUnixPairsEach {
		return nil
	}
	if forwardUnixProcess.pairs == nil {
		forwardUnixProcess.pairs = make(map[*forwardUnixWorkerPair]struct{})
	}
	p := &forwardUnixWorkerPair{worker: w, local: local}
	w.active[p] = struct{}{}
	forwardUnixProcess.pairs[p] = struct{}{}
	w.wg.Add(1)
	return p
}

func (w *forwardUnixWorker) release(p *forwardUnixWorkerPair) {
	w.mu.Lock()
	forwardUnixProcess.Lock()
	p.mu.Lock()
	nativeErr := p.nativeErr
	p.mu.Unlock()
	if nativeErr == nil {
		delete(w.active, p)
		delete(forwardUnixProcess.pairs, p)
	}
	forwardUnixProcess.Unlock()
	w.mu.Unlock()
	w.wg.Done()
}

func (w *forwardUnixWorker) run() {
	defer close(w.done)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		<-w.ctx.Done()
		w.addCleanup(w.bound.StopOriginalAdmission())
		w.mu.Lock()
		pairs := make([]*forwardUnixWorkerPair, 0, len(w.active))
		for p := range w.active {
			pairs = append(pairs, p)
		}
		w.mu.Unlock()
		for _, p := range pairs {
			p.abort()
		}
	}()
	for {
		if !w.reserveAccept() {
			if w.ctx.Err() != nil {
				break
			}
			select {
			case <-w.ctx.Done():
				break
			case <-time.After(20 * time.Millisecond):
			}
			if w.ctx.Err() != nil {
				break
			}
			continue
		}
		local, err := w.bound.AcceptOriginal(w.ctx)
		if err != nil {
			w.releaseAccept()
			if w.ctx.Err() == nil {
				w.addCleanup(err)
			}
			break
		}
		p := w.reserve(local)
		if p == nil {
			w.addCleanup(w.bound.CloseAcceptedOriginal(local))
			continue
		}
		go p.serve()
	}
	w.cancel()
	<-watchDone
	w.wg.Wait()
	close(w.joined)
}

func (p *forwardUnixWorkerPair) recordNative(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	p.nativeErr = errors.Join(p.nativeErr, err)
	p.mu.Unlock()
}

func (p *forwardUnixWorkerPair) recordRemote(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	p.remoteErr = errors.Join(p.remoteErr, err)
	p.mu.Unlock()
}

func (p *forwardUnixWorkerPair) recordRemoteNative(err error) {
	if err == nil {
		return
	}
	// Manager-owned source/storage debt must be sticky before the actual
	// worker join closes, even if the local Unix descriptor closed cleanly.
	p.recordNative(errors.Join(err, p.worker.bound.MarkOriginalTransportCloseUncertain()))
}

func (p *forwardUnixWorkerPair) result() (error, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.nativeErr, p.remoteErr
}

func (p *forwardUnixWorkerPair) setRemote(remote *meshforward.AdmittedRoute) bool {
	p.mu.Lock()
	aborted := p.aborted
	if !aborted {
		p.remote = remote
	}
	p.mu.Unlock()
	if aborted {
		if remote != nil && remote.Conn() != nil {
			p.closePeer.Do(func() { p.closeRemoteOriginal(remote) })
		}
		return false
	}
	return true
}

func (p *forwardUnixWorkerPair) abort() {
	p.mu.Lock()
	p.aborted = true
	remote := p.remote
	p.mu.Unlock()
	p.closeLocal.Do(func() { p.recordNative(p.worker.bound.CloseAcceptedOriginal(p.local)) })
	if remote != nil && remote.Conn() != nil {
		p.closePeer.Do(func() { p.closeRemoteOriginal(remote) })
	}
}

func (p *forwardUnixWorkerPair) closeRemoteOriginal(remote *meshforward.AdmittedRoute) {
	applicationErr, nativeErr := meshsshrelay.CloseOriginalTLS(remote.Conn())
	p.recordRemote(applicationErr)
	p.recordRemoteNative(nativeErr)
}

func (p *forwardUnixWorkerPair) serve() {
	w := p.worker
	defer func() {
		p.abort()
		nativeErr, remoteErr := p.result()
		w.addCleanup(nativeErr)
		w.addApplication(remoteErr)
		w.release(p)
	}()
	openCtx, cancelOpen := context.WithCancel(w.ctx)
	defer cancelOpen()
	var timerMu sync.Mutex
	pending := true
	deadline := time.Now().Add(forwardUnixOpenLimit)
	timerDone := make(chan struct{})
	timer := time.AfterFunc(time.Until(deadline), func() {
		defer close(timerDone)
		timerMu.Lock()
		if pending {
			cancelOpen()
		}
		timerMu.Unlock()
	})
	admitted, err := w.open(openCtx)
	returnedAt := time.Now()
	timerMu.Lock()
	pending = false
	stopped := timer.Stop()
	timerMu.Unlock()
	if stopped {
		close(timerDone)
	} else {
		<-timerDone
	}
	if admitted != nil && !p.setRemote(admitted) {
		return
	}
	if err != nil || admitted == nil || openCtx.Err() != nil || !returnedAt.Before(deadline) {
		var uncertain *meshsshrelay.NativeCloseUncertain
		if errors.As(err, &uncertain) {
			p.recordRemoteNative(uncertain)
		}
		w.addApplication(err)
		return
	}
	q := admitted.Request()
	if q.Workspace != w.binding.Workspace || q.Generation != w.source.authority.Generation || q.Source != w.source.authority.Origin.Host || q.SourcePin != w.source.authority.Origin.Fingerprint || q.Target != w.binding.Target || q.TargetPin != w.source.authority.Destination.Fingerprint || q.Service != w.binding.Service || q.GrantRevision != w.binding.GrantRevision {
		w.addApplication(model.Err("forbidden", "forward Unix destination admission differs"))
		return
	}
	if err := w.bound.CheckBeforeApplicationBytes(openCtx); err != nil {
		w.addApplication(err)
		return
	}
	if err := w.source.currentForwardAuthority(openCtx); err != nil {
		w.addApplication(err)
		return
	}
	if err := w.bound.CheckBeforeApplicationBytes(openCtx); err != nil {
		w.addApplication(err)
		return
	}
	if w.ctx.Err() != nil {
		return
	}
	finished := make(chan error, 2)
	go func() {
		err := forwardUnixCopy(w.ctx, admitted.Conn(), p.local)
		if err == nil {
			err = admitted.Conn().CloseWrite()
		}
		finished <- err
	}()
	go func() {
		err := forwardUnixCopy(w.ctx, p.local, admitted.Conn())
		if err == nil {
			err = p.local.CloseWrite()
		}
		finished <- err
	}()
	first := <-finished
	if first != nil {
		p.abort()
	}
	second := <-finished
	if first != nil || second != nil {
		w.addApplication(errors.Join(first, second))
	}
}

func forwardUnixCopy(ctx context.Context, dst io.Writer, src io.Reader) error {
	buf := make([]byte, forwardUnixCopyBytes)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := src.Read(buf)
		if n < 0 || n > len(buf) {
			return errors.New("invalid forward Unix read count")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		for written := 0; written < n; {
			if err := ctx.Err(); err != nil {
				return err
			}
			m, err := dst.Write(buf[written:n])
			if m < 0 || m > n-written {
				return errors.New("invalid forward Unix write count")
			}
			written += m
			if err != nil {
				return err
			}
			if m == 0 {
				return io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
}

func (w *forwardUnixWorker) Close(ctx context.Context) error {
	if w == nil || ctx == nil {
		return model.Err("invalid_request", "original forward Unix worker required")
	}
	w.closeMu.Lock()
	defer w.closeMu.Unlock()
	if w.closed {
		return w.cleanupErr
	}
	w.cancel()
	select {
	case <-w.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	err := w.bound.CloseAfterJoin(ctx)
	w.addCleanup(err)
	w.mu.Lock()
	result := w.cleanupErr
	active := len(w.active)
	w.mu.Unlock()
	if err == nil && active == 0 {
		forwardUnixProcess.Lock()
		delete(forwardUnixProcess.workers, w)
		forwardUnixProcess.Unlock()
	}
	w.closed = result == nil && active == 0
	return result
}

func (w *forwardUnixWorker) ApplicationError() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.applicationErr
}
