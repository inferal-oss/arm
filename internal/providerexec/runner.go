package providerexec

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/providerproto"
	"github.com/inferal-oss/arm/internal/usage"
)

// Owner retains bounded in-process native duties. Cross-process slot/replay
// authority belongs to the durable coordinator; this is NOT its substitute.
type Owner struct {
	mu         sync.Mutex
	authority  string
	handles    map[string]*Handle
	retired    map[string]string
	ownerToken string
	trace      observer
	// Private deterministic fixtures; nil on the production entrypoint. These
	// pause genuine start/wait boundaries, not outcomes or closure observations.
	beforeStart, beforeWait, beforeGroupAbsence <-chan struct{}
}

func NewOwner(authority string) (*Owner, error) {
	if len(authority) != 32 {
		return nil, fmt.Errorf("exact selected authority required")
	}
	native, err := usage.Capture(os.Getpid())
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(native)
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	return &Owner{authority: strings.Clone(authority), handles: map[string]*Handle{}, retired: map[string]string{}, ownerToken: hash(raw) + ":" + hex.EncodeToString(nonce[:])}, nil
}
func (o *Owner) OwnerToken() string { return o.ownerToken }

type Handle struct {
	mu                        sync.Mutex
	owner                     *Owner
	prepared                  Prepared
	marker                    StartMarker
	observation               Observation
	nonce                     string
	cancelCh                  chan struct{}
	cancelOnce                sync.Once
	helloDone, done           chan struct{}
	helloErr                  error
	helloProcess              model.ProcessIdentity
	doneOnce                  sync.Once
	command                   *exec.Cmd
	readControl, writeControl *os.File
	rawOut, rawErr            *os.File
	childFiles                []*os.File
	grantMu                   sync.Mutex
	grantPublished            bool
	controlClosing            bool
	controlWG                 sync.WaitGroup
	reaped                    bool
	signalsFinished           bool
	stdout, stderr            capture
	stdoutDone, stderrDone    chan struct{}
	startGate, waitGate       <-chan struct{}
	groupGate                 <-chan struct{}
}
type capture struct {
	body, tail []byte
	count      int64
	eof        bool
	err        error
}

// BeginStart registers the bounded retained handle BEFORE any asynchronous OS
// call. On a context timeout callers retain this same handle, never a no-start
// receipt. Existing keys return the same handle read-only, or conflict.
func (o *Owner) BeginStart(ctx context.Context, p Prepared, marker StartMarker) (*Handle, error) {
	if ctx == nil || p.attempt == "" || o.authority != p.held.authority || !marker.matches(p) || marker.OwnerProcess != o.ownerToken {
		return nil, fmt.Errorf("trusted held preparation and durable exact launch marker required")
	}
	marker = StartMarker{p.attempt, p.requestDigest, p.held.authority, p.held.worker, p.held.temp, o.ownerToken}
	o.mu.Lock()
	if old := o.handles[p.attempt]; old != nil {
		o.mu.Unlock()
		if old.prepared.requestDigest != p.requestDigest {
			return old, fmt.Errorf("attempt key conflict")
		}
		return old, nil
	}
	if _, seen := o.retired[p.attempt]; seen {
		o.mu.Unlock()
		return nil, fmt.Errorf("settled native key cannot be replayed")
	}
	if len(o.retired)+len(o.handles) >= 128 {
		o.mu.Unlock()
		return nil, fmt.Errorf("bounded native key history full")
	}
	if len(o.handles) >= 4 {
		o.mu.Unlock()
		return nil, fmt.Errorf("four retained native duties are already owned")
	}
	for _, h := range o.handles {
		if h.prepared.registration == p.registration {
			o.mu.Unlock()
			return nil, fmt.Errorf("registration already has a retained native duty")
		}
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		o.mu.Unlock()
		return nil, err
	}
	h := &Handle{owner: o, prepared: p, marker: marker, nonce: hex.EncodeToString(nonce[:]), observation: Observation{Attempt: p.attempt, StartPending: true}, cancelCh: make(chan struct{}), helloDone: make(chan struct{}), done: make(chan struct{}), stdoutDone: make(chan struct{}), stderrDone: make(chan struct{}), startGate: o.beforeStart, waitGate: o.beforeWait, groupGate: o.beforeGroupAbsence}
	o.handles[p.attempt] = h
	o.trace.emit("handle_retained", p.attempt, 0)
	o.mu.Unlock()
	go func() {
		timer := time.NewTimer(p.timeout)
		nativeFinished, watcherDone := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(watcherDone)
			select {
			case <-ctx.Done():
				h.Cancel()
			case <-timer.C:
				h.Cancel()
			case <-nativeFinished:
			}
		}()
		h.run()
		close(nativeFinished)
		timer.Stop()
		<-watcherDone
		h.mu.Lock()
		if h.observation.CancelAccepted {
			h.observation.ProtocolSuccess = false
		}
		h.mu.Unlock()
		h.doneOnce.Do(func() { close(h.done) })
	}()
	return h, nil
}
func (h *Handle) Snapshot() Observation {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.observation.clone()
}
func (h *Handle) update(f func(*Observation)) { h.mu.Lock(); f(&h.observation); h.mu.Unlock() }
func (h *Handle) Cancel() {
	h.cancelOnce.Do(func() {
		h.mu.Lock()
		h.observation.CancelAccepted = true
		// The private bounded recorder has no callback or I/O. Capture this cut
		// before releasing the same lock that linearizes grant/cancellation.
		h.owner.trace.emit("cancel_accepted", h.prepared.attempt, 0)
		h.mu.Unlock()
		close(h.cancelCh)
	})
}
func (h *Handle) Hello(ctx context.Context) error {
	select {
	case <-h.helloDone:
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.helloErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (h *Handle) Execute(ctx context.Context, marker DispatchMarker) error {
	if err := ctx.Err(); err != nil {
		h.Cancel()
		return err
	}
	if err := h.Hello(ctx); err != nil {
		return err
	}
	h.mu.Lock()
	native := h.helloProcess
	h.mu.Unlock()
	rawNative, _ := json.Marshal(native)
	if marker.Start != h.marker || marker.WorkerProcessSHA256 != hash(rawNative) || len(marker.PublicationSHA256) != 64 {
		return fmt.Errorf("exact trusted durable dispatch receipt required")
	}
	h.grantMu.Lock()
	defer h.grantMu.Unlock()
	h.mu.Lock()
	if h.observation.CancelAccepted || h.controlClosing {
		h.mu.Unlock()
		return fmt.Errorf("cancelled worker cannot receive a grant")
	}
	if h.grantPublished {
		h.mu.Unlock()
		return fmt.Errorf("one grant already published; inspect original attempt")
	}
	h.controlWG.Add(1)
	h.grantPublished = true
	h.observation.GrantPublished = true
	h.owner.trace.emit("grant_published", h.prepared.attempt, 0)
	h.mu.Unlock()
	// Grant was locally published before concurrent cancellation. A failed write
	// is unknown; no second writer/retry or retroactive no-dispatch claim.
	done := make(chan error, 1)
	go func() {
		defer h.controlWG.Done()
		done <- writePacket(h.writeControl, h.nonce, 2, "grant", grantPacket{h.prepared.requestDigest})
	}()
	select {
	case err := <-done:
		if err != nil {
			h.Cancel()
		}
		return err
	case <-ctx.Done():
		h.Cancel()
		return ctx.Err()
	case <-h.done:
		return fmt.Errorf("worker closed during grant")
	}
}
func (h *Handle) run() {
	// No native call below executes under Owner.mu, Handle.mu or a catalogue/SQL
	// lock. The already retained handle owns descriptors even while Start blocks.
	var err error
	var inR, outW, errW, controlW *os.File
	h.writeControl, inR, err = pipeParentWriter()
	if inR != nil {
		h.childFiles = append(h.childFiles, inR)
	}
	if err != nil {
		h.noStart(err)
		return
	}
	h.readControl, controlW, err = os.Pipe()
	if controlW != nil {
		h.childFiles = append(h.childFiles, controlW)
	}
	if err != nil {
		h.noStart(err)
		return
	}
	h.rawOut, outW, err = os.Pipe()
	if outW != nil {
		h.childFiles = append(h.childFiles, outW)
	}
	if err != nil {
		h.noStart(err)
		return
	}
	h.rawErr, errW, err = os.Pipe()
	if errW != nil {
		h.childFiles = append(h.childFiles, errW)
	}
	if err != nil {
		h.noStart(err)
		return
	}
	p := h.prepared
	if err = p.selected.runtime.check(); err == nil {
		err = p.selected.provider.check()
	}
	if err != nil {
		h.noStart(err)
		return
	}
	cmd := exec.Command(p.selected.runtime.path, HelperArgument)
	cmd.Env = []string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/bin"}
	cmd.Dir = p.held.path
	cmd.ExtraFiles = []*os.File{inR, controlW}
	cmd.Stdout = outW
	cmd.Stderr = errW
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	h.command = cmd
	h.owner.trace.emit("native_start_begun", p.attempt, 0)
	if h.startGate != nil {
		<-h.startGate
	}
	err = cmd.Start()
	returned := uint64(0)
	if err == nil {
		returned = 1
	}
	h.owner.trace.emit("native_start_returned", p.attempt, returned)
	if err != nil {
		h.noStart(err)
		return
	}
	for _, f := range h.childFiles {
		f.Close()
	}
	h.childFiles = nil
	h.update(func(o *Observation) { o.StartPending = false; o.StartReturned = true })
	go h.pump(h.rawOut, true)
	go h.pump(h.rawErr, false)
	init := initPacket{p.selected.runtime.packet(), p.selected.provider.packet(), p.held.path, p.held.identity, json.RawMessage(p.request), p.requestDigest}
	helloCh := make(chan error, 1)
	h.controlWG.Add(1)
	go func() {
		defer h.controlWG.Done()
		e := writePacket(h.writeControl, h.nonce, 1, "init", init)
		var hello helloPacket
		if e == nil {
			e = readPacket(h.readControl, h.nonce, 1, "hello", &hello)
		}
		if e == nil && (hello.Process.PID != cmd.Process.Pid || hello.Process.PGID != cmd.Process.Pid || hello.Process.UID != p.held.owner || hello.Process.Start == "" || hello.Process.Namespace == "" || hello.RuntimeSHA256 != p.selected.runtime.sha || hello.RequestSHA256 != p.requestDigest) {
			e = fmt.Errorf("foreign helper hello")
		}
		if e == nil {
			h.mu.Lock()
			h.helloProcess = hello.Process
			h.mu.Unlock()
		}
		helloCh <- e
	}()
	select {
	case err = <-helloCh:
	case <-h.cancelCh:
		err = fmt.Errorf("cancelled during retained helper startup")
	}
	h.mu.Lock()
	h.helloErr = err
	h.observation.HelloVerified = err == nil
	h.mu.Unlock()
	if err == nil {
		h.owner.trace.emit("hello_verified", p.attempt, 0)
	}
	close(h.helloDone)
	if err == nil {
		resultCh := make(chan struct {
			packet resultPacket
			err    error
		}, 1)
		h.controlWG.Add(1)
		go func() {
			defer h.controlWG.Done()
			var result resultPacket
			e := readPacket(h.readControl, h.nonce, 2, "result", &result)
			resultCh <- struct {
				packet resultPacket
				err    error
			}{result, e}
		}()
		select {
		case result := <-resultCh:
			if result.err == nil && result.packet.Started && result.packet.Process.PGID == cmd.Process.Pid && result.packet.Process.UID == p.held.owner {
				exit := result.packet.Exit
				h.update(func(o *Observation) { o.ProviderExit = &exit })
				h.owner.trace.emit("provider_exit_observed", p.attempt, 0)
			} else {
				h.fail("provider_result_unavailable")
			}
		case <-h.cancelCh:
			h.fail("cancelled_or_deadline")
		}
	} else {
		h.fail("helper_startup_unavailable")
	}
	h.cleanup()
}
func pipeParentWriter() (*os.File, *os.File, error) { r, w, err := os.Pipe(); return w, r, err }
func (h *Handle) fail(code string) {
	h.update(func(o *Observation) {
		if o.Failure == "" {
			o.Failure = code
		}
	})
}
func (h *Handle) noStart(err error) {
	if h.command != nil && h.command.Process != nil {
		// A native error carrying a process handle is not proof of no child. This
		// unsupported ambiguity remains owned; never signal a guessed group or grant.
		h.closeFiles()
		h.controlWG.Wait()
		h.mu.Lock()
		h.helloErr = err
		h.observation.StartReturned = true
		h.observation.StartPending = false
		h.observation.Failure = "start_outcome_unknown"
		h.mu.Unlock()
		close(h.helloDone)
		h.owner.trace.emit("start_outcome_unknown", h.prepared.attempt, 0)
		return
	}
	h.fail("known_start_failure")
	h.closeFiles()
	h.controlWG.Wait()
	h.mu.Lock()
	h.helloErr = err
	h.observation.StartReturned = true
	h.observation.StartPending = false
	h.observation.KnownNotStarted = true
	h.observation.PumpsJoined = true
	h.observation.ScopedNativeClosed = true
	h.mu.Unlock()
	close(h.helloDone)
	h.owner.trace.emit("known_not_started", h.prepared.attempt, 0)

}
func (h *Handle) closeFiles() {
	for _, f := range append([]*os.File{h.writeControl, h.readControl, h.rawOut, h.rawErr}, h.childFiles...) {
		if f != nil {
			_ = f.Close()
		}
	}
}
func (h *Handle) signalGroup(sig syscall.Signal) error {
	h.mu.Lock()
	if h.reaped || h.command == nil || h.command.Process == nil || h.signalsFinished {
		h.mu.Unlock()
		return fmt.Errorf("no current unreaped owned anchor")
	}
	pid := h.command.Process.Pid
	h.mu.Unlock()
	h.owner.trace.emit("group_signal_before_reap", h.prepared.attempt, uint64(sig))
	err := syscall.Kill(-pid, sig)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}
func (h *Handle) cleanup() {
	h.mu.Lock()
	h.controlClosing = true
	h.mu.Unlock()
	if h.Snapshot().CancelAccepted {
		_ = h.signalGroup(syscall.SIGTERM)
		timer := time.NewTimer(2 * time.Second)
		<-timer.C
	}
	_ = h.signalGroup(syscall.SIGKILL)
	h.mu.Lock()
	h.signalsFinished = true
	h.mu.Unlock()
	// Only this exact live owner's Wait is used, strictly after all signals.
	waited := make(chan error, 1)
	go func() {
		if h.waitGate != nil {
			<-h.waitGate
		}
		waited <- h.command.Wait()
	}()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		h.fail("native_wait_pending")
		h.owner.trace.emit("wait_pending", h.prepared.attempt, 0)
		<-waited
	}
	h.mu.Lock()
	h.reaped = true
	h.observation.HelperWaitObserved = true
	h.mu.Unlock()
	h.owner.trace.emit("helper_reaped", h.prepared.attempt, 0)
	// No nonzero signal is ever sent after reap. Retain this handle while
	// observing disappearance for at most two seconds. A still-visible group,
	// an error, or reuse by another group cannot establish scoped closure.
	if h.groupGate != nil {
		<-h.groupGate
	}
	absent := observeGroupAbsence(h.command.Process.Pid)
	h.update(func(o *Observation) { o.GroupAbsentObserved = absent })
	if !absent {
		h.fail("native_group_presence_unresolved")
	}
	joined := make(chan struct{})
	go func() { <-h.stdoutDone; <-h.stderrDone; close(joined) }()
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		h.rawOut.Close()
		h.rawErr.Close()
		h.fail("output_pumps_pending")
		<-joined
	}
	h.closeFiles()
	h.controlWG.Wait()
	h.mu.Lock()
	h.observation.PumpsJoined = true
	h.observation.StdoutBytes = h.stdout.count
	h.observation.StderrBytes = h.stderr.count
	h.observation.StdoutEOF = h.stdout.eof
	h.observation.StderrEOF = h.stderr.eof
	h.observation.StderrTail = append([]byte(nil), h.stderr.tail...)
	h.observation.ResponseSHA256 = hash(h.stdout.body)
	h.observation.ScopedNativeClosed = absent
	response := append([]byte(nil), h.stdout.body...)
	h.mu.Unlock()
	h.match(response)
	h.owner.trace.emit("native_observation_complete", h.prepared.attempt, 0)

}

// observeGroupAbsence is observation only: it never signals a saved/reaped
// process or adopts a process group. ESRCH is the only positive absence fact.
func observeGroupAbsence(pid int) bool {
	if pid <= 0 {
		return false
	}
	return pollGroupAbsence(func() error { return syscall.Kill(-pid, 0) }, 2*time.Second)
}

func pollGroupAbsence(probe func() error, budget time.Duration) bool {
	if budget <= 0 || budget > 2*time.Second {
		return false
	}
	deadline := time.Now().Add(budget)
	for {
		if !time.Now().Before(deadline) {
			return false
		}
		if probe() == syscall.ESRCH {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		time.Sleep(min(20*time.Millisecond, remaining))
	}
}

func (h *Handle) pump(f *os.File, stdout bool) {
	done := h.stderrDone
	if stdout {
		done = h.stdoutDone
	}
	defer close(done)
	var c capture
	var buf [4096]byte
	limit := int64(MaxStderrBytes)
	if stdout {
		limit = providerproto.MaxResponseBytes + 5
	}
	for {
		n, err := f.Read(buf[:])
		if n > 0 {
			c.count += int64(n)
			part := buf[:n]
			if stdout {
				remaining := int(limit+1) - len(c.body)
				if remaining > 0 {
					c.body = append(c.body, part[:min(remaining, n)]...)
				}
			} else {
				c.tail = append(c.tail, part...)
				if len(c.tail) > MaxStderrTail {
					c.tail = append([]byte(nil), c.tail[len(c.tail)-MaxStderrTail:]...)
				}
			}
			if c.count > limit {
				c.err = fmt.Errorf("output byte bound")
				h.fail("output_limit")
				h.Cancel()
				break
			}
		}
		if err != nil {
			c.eof = err == io.EOF
			c.err = err
			break
		}
		if n == 0 {
			c.err = io.ErrNoProgress
			h.Cancel()
			break
		}
	}
	h.mu.Lock()
	if stdout {
		h.stdout = c
	} else {
		h.stderr = c
	}
	h.mu.Unlock()
}
func (h *Handle) match(body []byte) {
	o := h.Snapshot()
	if o.CancelAccepted || !o.StdoutEOF || !o.HelperWaitObserved || o.ProviderExit == nil || o.ProviderExit.Code != 0 || o.ProviderExit.Signal != 0 {
		h.fail("diagnostic_unconfirmed")
		return
	}
	decoded, err := providerproto.ReadResponse(bytes.NewReader(body))
	if err != nil {
		h.fail("protocol_frame_invalid")
		return
	}
	matched, err := providerproto.MatchResponse(decoded, h.prepared.sent, h.prepared.reg, h.prepared.original, nil)
	if err != nil {
		h.fail("protocol_response_mismatch")
		return
	}
	response := matched.Response()
	selected, err := json.Marshal(response.Result)
	if response.Error != nil {
		selected, err = json.Marshal(response.Error)
	}
	if err != nil || len(selected) > SelectedResultBytes {
		h.fail("selected_result_too_large")
		return
	}
	h.update(func(o *Observation) {
		o.ProtocolMatched = true
		o.ProtocolSuccess = response.Error == nil
		o.Selected = append(json.RawMessage(nil), selected...)
	})
}
func processExit(err error) Exit {
	if err == nil {
		return Exit{}
	}
	if exit, ok := err.(*exec.ExitError); ok {
		if st, ok := exit.Sys().(syscall.WaitStatus); ok {
			if st.Signaled() {
				return Exit{Signal: int(st.Signal())}
			}
			return Exit{Code: st.ExitStatus()}
		}
	}
	return Exit{Code: 125}
}

// RetireFromTrusted must follow independently positive temp cleanup and ordinary
// worker release. No provider metadata/boolean establishes these trust inputs.
func (o *Owner) RetireFromTrusted(h *Handle, worker, temp modelLeaseReceipt) error {
	if h == nil || worker.ID != h.prepared.held.worker || temp.ID != h.prepared.held.temp || worker.Authority != o.authority || temp.Authority != o.authority || worker.Outcome != "released" || temp.Outcome != "released" || worker.SHA256 == "" || temp.SHA256 == "" {
		return fmt.Errorf("exact trusted positive compensation receipts required")
	}
	select {
	case <-h.done:
	default:
		return fmt.Errorf("native work has not completed")
	}
	ob := h.Snapshot()
	if !ob.ScopedNativeClosed && !ob.KnownNotStarted {
		return fmt.Errorf("scoped native ownership unresolved")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.handles[h.prepared.attempt] != h {
		return fmt.Errorf("foreign native handle")
	}
	o.retired[h.prepared.attempt] = h.prepared.requestDigest
	delete(o.handles, h.prepared.attempt)
	o.trace.emit("fully_settled", h.prepared.attempt, 0)
	return nil
}

// Kept internal until the typed P1b manager adapter can produce verified receipts.
type modelLeaseReceipt struct{ ID, Authority, Outcome, SHA256 string }
