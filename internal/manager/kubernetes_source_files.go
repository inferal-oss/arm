package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"getarm.org/internal/kubeprovision"
	"getarm.org/internal/local"
	"getarm.org/internal/storage"
)

type kubernetesSourceFiles struct {
	owner       *KubernetesSourcePreparation
	directory   *storage.SourceDirectory
	mu          sync.Mutex
	closing     bool
	requestLane bool
	consumed    map[string]bool
	files       []*kubernetesHeldFile
	calls       []*kubeprovision.NativeExecCall
	ops         sync.WaitGroup // Add only while mu says !closing; Wait only after closing
	sticky      error
	// Native fault seams are private, original-instance only, never RPC inputs.
	beforeOpen func(string) // private causal gate; invoked outside locks
	closeFile  func(*os.File) error
	unlinkFile func(*os.File, string) error
}

func newKubernetesSourceFiles(k *KubernetesSourcePreparation, d *storage.SourceDirectory) *kubernetesSourceFiles {
	return &kubernetesSourceFiles{owner: k, directory: d, consumed: map[string]bool{}, closeFile: func(f *os.File) error { return f.Close() }, unlinkFile: d.UnlinkOriginal}
}
func (f *kubernetesSourceFiles) fail(err error) {
	if err == nil {
		return
	}
	f.mu.Lock()
	f.sticky = errors.Join(f.sticky, err)
	f.mu.Unlock()
	f.owner.Cancel()
}
func (f *kubernetesSourceFiles) begin(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("source context required")
	}
	if err := f.owner.CheckActive(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closing || f.sticky != nil {
		return fmt.Errorf("source factory closed or uncertain")
	}
	f.ops.Add(1)
	return nil
}
func (f *kubernetesSourceFiles) create(ctx context.Context, purpose string) (file *kubernetesHeldFile, resultErr error) {
	limit, err := f.owner.scope.Limit(purpose)
	if err != nil {
		return nil, err
	}
	if err = f.begin(ctx); err != nil {
		return nil, err
	}
	defer f.ops.Done()
	f.mu.Lock()
	if f.consumed[purpose] {
		f.mu.Unlock()
		return nil, fmt.Errorf("source purpose already consumed")
	}
	if purpose != "payload" && f.requestLane {
		f.mu.Unlock()
		return nil, fmt.Errorf("original request lane already reserved")
	}
	if purpose != "payload" {
		f.requestLane = true
	}
	f.consumed[purpose] = true
	f.mu.Unlock()
	name, err := storage.SourceFileName(purpose, local.ID())
	if err != nil {
		return nil, err
	}
	// Generate once; OpenExclusive validates the fixed purpose/nonce pair itself.
	nonce := name[len(purpose)+1:]
	if f.beforeOpen != nil {
		f.beforeOpen(purpose)
	}
	if err = f.owner.CheckActive(ctx); err != nil {
		return nil, err
	}
	raw, err := f.directory.OpenExclusive(purpose, nonce)
	if err != nil {
		f.fail(err)
		return nil, err
	}
	file = &kubernetesHeldFile{factory: f, raw: raw, name: name, purpose: purpose, limit: limit, ioGate: make(chan struct{}, 1), ioZero: closedSourceIO(), closeDone: make(chan struct{})}
	// Register immediately before any further fallible work, including stat/SQL.
	f.mu.Lock()
	f.files = append(f.files, file)
	closing := f.closing
	f.mu.Unlock()
	defer func() {
		if resultErr != nil {
			if err := file.Close(); err != nil {
				f.fail(err)
			}
		}
	}()
	if closing {
		return file, fmt.Errorf("source closed during creation")
	}
	if err = f.directory.CheckFile(raw, name, true); err != nil {
		f.fail(err)
		return file, err
	}
	if err = f.owner.captureDebt(ctx); err != nil {
		f.fail(err)
		return file, err
	}
	if err = f.unlinkFile(raw, name); err != nil {
		f.fail(err)
		return file, err
	}
	if err = f.directory.CheckFile(raw, name, false); err != nil {
		f.fail(err)
		return file, err
	}
	if err = f.owner.CheckActive(ctx); err != nil {
		return file, err
	}
	return file, nil
}
func (f *kubernetesSourceFiles) NewPayload(ctx context.Context) (kubeprovision.HeldSourceFile, error) {
	v, err := f.create(ctx, "payload")
	if err != nil {
		return nil, err
	}
	return v, nil
}
func (f *kubernetesSourceFiles) RunFixedExec(ctx context.Context, call *kubeprovision.NativeExecCall, payload kubeprovision.HeldSourceFile) (join kubeprovision.OriginalExecJoin, resultErr error) {
	key, size, purpose, err := call.Scope()
	if err != nil {
		return join, err
	}
	if key != f.owner.scope.Operation || size != f.owner.scope.B || call.Owner() != f.owner.manager.owner {
		return join, fmt.Errorf("fixed call differs from source scope")
	}
	if _, err = f.owner.scope.Limit(purpose); err != nil || purpose == "payload" {
		return join, fmt.Errorf("fixed call unavailable in source stage")
	}
	var data *kubernetesHeldFile
	if purpose == "ship" {
		var ok bool
		data, ok = payload.(*kubernetesHeldFile)
		if !ok || data.factory != f || data.purpose != "payload" {
			return join, fmt.Errorf("original source payload required")
		}
	} else if payload != nil {
		return join, fmt.Errorf("unexpected source payload")
	}
	request, err := f.create(ctx, purpose)
	if err != nil {
		return join, err
	}
	defer func() {
		if err := request.Close(); err != nil {
			f.fail(err)
			resultErr = errors.Join(resultErr, kubeprovision.ErrHelperCleanupUnknown)
		}
	}()
	if err = call.WriteRequest(request); err != nil {
		return join, err
	}
	if _, err = request.Seek(0, io.SeekStart); err != nil {
		return join, err
	}
	if err = f.begin(ctx); err != nil {
		return join, err
	}
	defer f.ops.Done()
	if err = f.directory.CheckFile(request.raw, request.name, false); err != nil {
		f.fail(err)
		return join, err
	}
	if err = request.borrow(); err != nil {
		return join, err
	}
	defer request.returnBorrow()
	if data != nil {
		if err = data.borrow(); err != nil {
			return join, err
		}
		defer data.returnBorrow()
	}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	run, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(f.owner.ctx, cancel)
	defer func() { stop(); cancel() }()
	if err = f.owner.CheckActive(run); err != nil {
		return join, err
	}
	var rawData *os.File
	if data != nil {
		rawData = data.raw
	}
	join, resultErr = call.RunOriginal(run, request.raw, rawData, f.owner.CheckActive)
	if !join.Matches(call) || !join.Closed() {
		request.markLoanUnknown()
		if data != nil {
			data.markLoanUnknown()
		}
		f.fail(kubeprovision.ErrHelperCleanupUnknown)
	}
	if closed, closeErr := join.RequestClose(); closed {
		request.recordNativeClose(closeErr)
	}
	return join, resultErr
}
func (f *kubernetesSourceFiles) closeOriginal() error {
	f.mu.Lock()
	f.closing = true
	calls := append([]*kubeprovision.NativeExecCall(nil), f.calls...)
	f.mu.Unlock()
	for _, c := range calls {
		c.Cancel()
	}
	f.ops.Wait()
	f.mu.Lock()
	files := append([]*kubernetesHeldFile(nil), f.files...)
	f.mu.Unlock()
	for _, v := range files {
		if err := v.Close(); err != nil {
			f.fail(err)
		}
	}
	if err := f.directory.Close(); err != nil {
		f.fail(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sticky
}

type kubernetesHeldFile struct {
	factory         *kubernetesSourceFiles
	raw             *os.File
	name, purpose   string
	limit, spent    int64
	mu              sync.Mutex
	closing, closed bool
	borrowed        int
	loanUnknown     bool
	laneReleased    bool
	closeErr        error
	closeDone       chan struct{}
	ioPending       int
	ioZero          chan struct{}
	ioGate          chan struct{}
}

func (v *kubernetesHeldFile) isClosed() bool { v.mu.Lock(); defer v.mu.Unlock(); return v.closed }
func (v *kubernetesHeldFile) beginIO(write int64) (func(), error) {
	if err := v.factory.begin(v.factory.owner.ctx); err != nil {
		return nil, err
	}
	v.mu.Lock()
	if v.closing || v.closed || v.borrowed != 0 || write < 0 || write > v.limit-v.spent {
		v.mu.Unlock()
		v.factory.ops.Done()
		return nil, fmt.Errorf("source file closed, borrowed or cumulative bound exhausted")
	}
	v.spent += write
	if v.ioPending == 0 {
		v.ioZero = make(chan struct{})
	}
	v.ioPending++
	v.mu.Unlock()
	select {
	case v.ioGate <- struct{}{}:
	case <-v.factory.owner.ctx.Done():
		v.endIO()
		return nil, v.factory.owner.ctx.Err()
	}
	if err := v.factory.owner.CheckActive(v.factory.owner.ctx); err != nil {
		<-v.ioGate
		v.endIO()
		return nil, err
	}
	return func() { <-v.ioGate; v.endIO() }, nil
}
func (v *kubernetesHeldFile) Read(b []byte) (int, error) {
	done, err := v.beginIO(0)
	if err != nil {
		return 0, err
	}
	defer done()
	return v.raw.Read(b)
}
func (v *kubernetesHeldFile) Write(b []byte) (int, error) {
	done, err := v.beginIO(int64(len(b)))
	if err != nil {
		return 0, err
	}
	defer done()
	return v.raw.Write(b)
}
func (v *kubernetesHeldFile) Seek(offset int64, whence int) (int64, error) {
	done, err := v.beginIO(0)
	if err != nil {
		return 0, err
	}
	defer done()
	size, err := v.raw.Stat()
	if err != nil {
		return 0, err
	}
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		at, e := v.raw.Seek(0, io.SeekCurrent)
		if e != nil {
			return 0, e
		}
		next = at + offset
	case io.SeekEnd:
		next = size.Size() + offset
	default:
		return 0, fmt.Errorf("invalid source seek")
	}
	if next < 0 || next > size.Size() {
		return 0, fmt.Errorf("source seek would escape existing content")
	}
	return v.raw.Seek(next, io.SeekStart)
}
func (v *kubernetesHeldFile) borrow() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.closing || v.borrowed != 0 || v.ioPending != 0 {
		return fmt.Errorf("original file is not available for native borrow")
	}
	v.borrowed++
	return nil
}
func (v *kubernetesHeldFile) returnBorrow() {
	v.mu.Lock()
	v.borrowed--
	release := v.takeLaneReleaseLocked()
	v.mu.Unlock()
	if release {
		v.releaseLane()
	}
}
func (v *kubernetesHeldFile) takeLaneReleaseLocked() bool {
	if !v.closed || v.closeErr != nil || v.borrowed != 0 || v.loanUnknown || v.laneReleased {
		return false
	}
	v.laneReleased = true
	return true
}
func (v *kubernetesHeldFile) recordNativeClose(err error) {
	v.mu.Lock()
	v.closed = true
	v.closeErr = err
	release := v.takeLaneReleaseLocked()
	v.mu.Unlock()
	if release {
		v.releaseLane()
	}
	if err != nil {
		v.factory.fail(err)
	}
}
func (v *kubernetesHeldFile) Close() error {
	v.mu.Lock()
	if v.closed {
		err := v.closeErr
		v.mu.Unlock()
		return err
	}
	if v.closing {
		done := v.closeDone
		v.mu.Unlock()
		<-done
		v.mu.Lock()
		err := v.closeErr
		v.mu.Unlock()
		return err
	}
	if v.borrowed != 0 {
		v.mu.Unlock()
		v.factory.fail(kubeprovision.ErrHelperCleanupUnknown)
		return kubeprovision.ErrHelperCleanupUnknown
	}
	v.closing = true
	zero := v.ioZero
	v.mu.Unlock()
	if zero != nil {
		<-zero
	}
	// Serialize with original local I/O without holding a bookkeeping mutex.
	v.ioGate <- struct{}{}
	err := v.factory.closeFile(v.raw)
	<-v.ioGate
	v.mu.Lock()
	v.closed = true
	v.closeErr = err
	close(v.closeDone)
	release := v.takeLaneReleaseLocked()
	v.mu.Unlock()
	if release {
		v.releaseLane()
	}
	if err != nil {
		v.factory.fail(err)
	}
	return err
}

func closedSourceIO() chan struct{} { done := make(chan struct{}); close(done); return done }
func (v *kubernetesHeldFile) endIO() {
	v.mu.Lock()
	v.ioPending--
	if v.ioPending == 0 {
		close(v.ioZero)
	}
	v.mu.Unlock()
	v.factory.ops.Done()
}

func (v *kubernetesHeldFile) releaseLane() {
	if v.purpose == "payload" {
		return
	}
	v.factory.mu.Lock()
	v.factory.requestLane = false
	v.factory.mu.Unlock()
}
func (v *kubernetesHeldFile) markLoanUnknown() { v.mu.Lock(); v.loanUnknown = true; v.mu.Unlock() }
