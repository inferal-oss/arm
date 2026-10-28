package kubeprovision

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// HeldSourceFile exposes no raw FD, path, unbounded copier or truncation method.
// The native factory checks original instance membership, never interface claims.
type HeldSourceFile interface {
	io.Reader
	io.Writer
	io.Seeker
	io.Closer
}
type SourceFiles interface {
	NewPayload(context.Context) (HeldSourceFile, error)
	RunFixedExec(context.Context, *NativeExecCall, HeldSourceFile) (OriginalExecJoin, error)
}

// NativeExecCall is a one-shot, closed fixed-helper call. Only this package's
// validated OwnedExec builder can initialize it; a record/FD/boolean cannot.
// It is not connected to Provider or any public request handler.
type NativeExecCall struct {
	beforeVerify    func() // private causal fixture; outside bookkeeping locks
	afterWait       func() // private causal fixture after original native Wait
	mu              sync.Mutex
	current         func(context.Context) error
	original        *OwnedExec
	pod             Pod
	kind            reviewedExecKind
	command         []string
	nonce           string
	encoded         []byte
	used, cancelled bool
	cancel          context.CancelFunc
	done            chan struct{}
	request         *os.File
	requestClosed   bool
	requestCloseErr error
	cmd             *exec.Cmd
	workerDone      <-chan struct{}
	waited          bool
	waitStarted     bool
	fallbackSignal  bool
	groupGone       bool
}

func (o *OwnedExec) NewSourceCall(pod Pod, kind string) (*NativeExecCall, error) {
	if o == nil || o.image == "" {
		return nil, fmt.Errorf("original fixed helper unavailable")
	}
	raw, err := json.Marshal(o.Plan)
	if err != nil {
		return nil, err
	}
	var plan Plan
	if err = json.Unmarshal(raw, &plan); err != nil || plan.Validate() != nil {
		return nil, fmt.Errorf("invalid frozen source plan")
	}
	clone := &OwnedExec{Plan: plan, image: o.image}
	k := reviewedExecKind(kind)
	command, err := fixedExecCommand(clone.Plan, pod, k)
	if err != nil {
		return nil, err
	}
	var presence io.Reader
	if k == execShip {
		presence = bytes.NewReader(nil)
	}
	if _, err = classifyReviewedExec(clone.Plan, pod, command, presence); err != nil {
		return nil, err
	}
	var n [32]byte
	if _, err = rand.Read(n[:]); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(n[:])
	encoded, err := json.Marshal(struct {
		Nonce   string `json:"nonce"`
		Request any    `json:"request"`
	}{nonce, reviewedExecRequest{Plan: clone.Plan, Pod: pod, Kind: k}})
	if err != nil || len(encoded) > 65536 {
		return nil, fmt.Errorf("fixed request exceeds source bound")
	}
	return &NativeExecCall{original: clone, pod: pod, kind: k, command: command, nonce: nonce, encoded: encoded, done: make(chan struct{})}, nil
}

// NewSourceCallForExec accepts only the exact already-reviewed provider call
// map. The boolean describes payload presence for classification, not resource
// admission. The factory still requires its concrete original payload loan.
func (o *OwnedExec) NewSourceCallForExec(pod Pod, command []string, hasPayload bool) (*NativeExecCall, error) {
	if o == nil {
		return nil, fmt.Errorf("original fixed helper unavailable")
	}
	var presence io.Reader
	if hasPayload {
		presence = bytes.NewReader(nil)
	}
	kind, err := classifyReviewedExec(o.Plan, pod, command, presence)
	if err != nil {
		return nil, err
	}
	return o.NewSourceCall(pod, string(kind))
}
func (c *NativeExecCall) Scope() (key string, size int64, kind string, err error) {
	if c == nil || c.original == nil || c.done == nil {
		return "", 0, "", fmt.Errorf("original fixed call required")
	}
	return c.original.Plan.Review.Key, c.original.Plan.Review.BinaryBytes, string(c.kind), nil
}
func (c *NativeExecCall) WriteRequest(w io.Writer) error {
	if _, _, _, err := c.Scope(); err != nil {
		return err
	}
	return writeHelperBytes(w, c.encoded)
}

// OriginalExecJoin has no public constructor or exported authority fields.
// Returned records/tickets cannot replace the original call pointer.
type OriginalExecJoin struct {
	call             *NativeExecCall
	returned, closed bool
	requestClosed    bool
	requestErr       error
	output, stderr   string
}

func (j OriginalExecJoin) Matches(c *NativeExecCall) bool {
	return c != nil && j.call == c && j.returned
}
func (j OriginalExecJoin) Closed() bool                { return j.returned && j.closed }
func (j OriginalExecJoin) RequestClose() (bool, error) { return j.requestClosed, j.requestErr }
func (j OriginalExecJoin) Output() (string, string)    { return j.output, j.stderr }
func (c *NativeExecCall) recordClose(f *os.File, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f == c.request {
		c.requestClosed = true
		c.requestCloseErr = err
	}
}
func (c *NativeExecCall) recordCmd(cmd *exec.Cmd) { c.mu.Lock(); c.cmd = cmd; c.mu.Unlock() }
func (c *NativeExecCall) recordWorker(done <-chan struct{}) {
	c.mu.Lock()
	c.workerDone = done
	c.mu.Unlock()
}
func (c *NativeExecCall) recordWait(gone bool) {
	c.mu.Lock()
	c.waited = true
	c.groupGone = gone
	c.mu.Unlock()
}
func (c *NativeExecCall) retireBeforeWait() {
	c.mu.Lock()
	c.waitStarted = true
	c.mu.Unlock()
}
func (c *NativeExecCall) Cancel() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.cancelled = true
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (c *NativeExecCall) stopUnjoined() {
	c.mu.Lock()
	cmd, worker := c.cmd, c.workerDone
	// Retire signalability before the sole Wait. Even a panic after reaping
	// must never turn the retained PID into authority over a reused process.
	canWait := cmd != nil && cmd.Process != nil && !c.waitStarted
	if canWait {
		c.waitStarted = true
		c.fallbackSignal = true
	}
	c.mu.Unlock()
	if canWait {
		if signalCurrentKubeGroup(cmd.Process.Pid) != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}
	if worker != nil {
		<-worker
	}
	if canWait {
		c.recordWait(observeKubeGroupAbsence(cmd.Process.Pid))
	}
}

// RunOriginal is called only by the factory with its registered borrowed files.
// It returns a sealed original join even on a target failure, and preserves
// uncertainty after panic/native close failure. No path lookup/SQL is used here.
func (c *NativeExecCall) RunOriginal(ctx context.Context, request, payload *os.File, current func(context.Context) error) (join OriginalExecJoin, resultErr error) {
	if _, _, _, err := c.Scope(); err != nil {
		return join, err
	}
	if ctx == nil || current == nil {
		return join, fmt.Errorf("original current-pair checker required")
	}
	c.mu.Lock()
	if c.used {
		c.mu.Unlock()
		return join, fmt.Errorf("fixed call already consumed")
	}
	c.used = true
	c.request = request
	c.current = current
	cancelled := c.cancelled
	run, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.mu.Unlock()
	if cancelled {
		cancel()
	}
	var out, stderr bytes.Buffer
	panicked := false
	defer func() {
		if recover() != nil {
			panicked = true
			resultErr = ErrHelperCleanupUnknown
		}
		cancel()
		c.stopUnjoined()
		c.mu.Lock()
		noProcess := c.cmd == nil || c.cmd.Process == nil
		closed := noProcess || c.waited && c.groupGone
		if c.requestCloseErr != nil || errors.Is(resultErr, ErrHelperCleanupUnknown) || panicked {
			closed = false
		}
		join = OriginalExecJoin{call: c, returned: true, closed: closed, requestClosed: c.requestClosed, requestErr: c.requestCloseErr, output: out.String(), stderr: stderr.String()}
		close(c.done)
		c.mu.Unlock()
	}()
	if err := c.checkCurrent(run); err != nil {
		return join, err
	}
	if request == nil {
		return join, fmt.Errorf("original request loan required")
	}
	if c.beforeVerify != nil {
		c.beforeVerify()
	}
	info, err := request.Stat()
	if err != nil || info.Size() != int64(len(c.encoded)) {
		return join, fmt.Errorf("source request differs")
	}
	actual, err := io.ReadAll(io.LimitReader(io.NewSectionReader(request, 0, 65537), 65537))
	if err != nil || sha256.Sum256(actual) != sha256.Sum256(c.encoded) {
		return join, fmt.Errorf("source request bytes differ")
	}
	if _, err = request.Seek(0, io.SeekStart); err != nil {
		return join, err
	}
	var input io.Reader
	if c.kind == execShip {
		if payload == nil {
			return join, fmt.Errorf("original payload loan required")
		}
		input = payload
	} else if payload != nil {
		return join, fmt.Errorf("unexpected payload loan")
	}
	owned := *c.original
	owned.sourceCall = c
	resultErr = owned.Exec(run, c.pod, c.command, input, &out, &stderr)
	return join, resultErr
}

func (c *NativeExecCall) checkCurrent(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		if ctx != nil {
			return ctx.Err()
		}
		return fmt.Errorf("original context required")
	}
	c.mu.Lock()
	check := c.current
	cancelled := c.cancelled
	c.mu.Unlock()
	if cancelled || check == nil {
		return context.Canceled
	}
	if err := check(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	cancelled = c.cancelled
	c.mu.Unlock()
	if cancelled {
		return context.Canceled
	}
	return nil
}

func (c *NativeExecCall) Owner() string {
	if c == nil || c.original == nil {
		return ""
	}
	return c.original.Plan.Review.Owner
}
