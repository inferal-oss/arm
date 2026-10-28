package kubeprovision

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// OwnedExec routes only the provider's fixed scripts through one self-helper
// per stream. The provider remains the owner of durable intent and lookup.
type OwnedExec struct {
	sourceCall *NativeExecCall // unused sealed factory bridge; never a wire field
	Plan       Plan
	image      string
}

// ErrHelperCleanupUnknown distinguishes unresolved original source ownership
// from an unknown remote effect. Positive target lookup cannot settle it.
var ErrHelperCleanupUnknown = errors.New("original Kubernetes helper cleanup unresolved")

func NewOwnedExec(plan Plan) (*OwnedExec, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	image, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &OwnedExec{Plan: plan, image: image}, nil
}

func heldExecRequest(request reviewedExecRequest, nonce string) (*os.File, error) {
	return heldOperationRequest(request, nonce)
}

func heldOperationRequest(request any, nonce string) (*os.File, error) {
	raw, err := json.Marshal(struct {
		Nonce   string `json:"nonce"`
		Request any    `json:"request"`
	}{nonce, request})
	if err != nil || len(raw) == 0 || len(raw) > 65536 {
		return nil, errors.New("reviewed Kubernetes helper request exceeds bound")
	}
	file, err := os.CreateTemp("", "arm-kube-exec-request-*")
	if err != nil {
		return nil, err
	}
	failed := func(err error) (*os.File, error) { return failHeldOperationRequest(file, err) }
	if err := os.Remove(file.Name()); err != nil {
		return failed(err)
	}
	if err := writeHelperBytes(file, raw); err != nil {
		return failed(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return failed(err)
	}
	return file, nil
}

// File construction can fail before OwnedExec registers its descriptors.
// Even these early paths retain a failed original close as source uncertainty.
func failHeldOperationRequest(file io.Closer, cause error) (*os.File, error) {
	if err := file.Close(); err != nil {
		return nil, ErrHelperCleanupUnknown
	}
	return nil, cause
}

// Exec accepts only a command byte-for-byte equal to one of the fixed scripts
// derived from this reviewed plan and exact Pod. Binary input must already be
// the provider's private unlinked regular spool; the child rehashes it.
func (o *OwnedExec) Exec(ctx context.Context, pod Pod, command []string, input io.Reader, output, stderr io.Writer) (resultErr error) {
	if o == nil || o.image == "" {
		return errors.New("owned Kubernetes exec helper unavailable")
	}
	kind, err := classifyReviewedExec(o.Plan, pod, command, input)
	if err != nil {
		return err
	}
	var payload *os.File
	if kind == execShip {
		var ok bool
		payload, ok = input.(*os.File)
		if !ok {
			return errors.New("private held Kubernetes payload file required")
		}
		if err := verifyHeldPayload(payload, o.Plan.Review.BinaryBytes, o.Plan.Review.BinarySHA256); err != nil {
			return err
		}
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	nonce := hex.EncodeToString(random[:])
	if o.sourceCall != nil {
		nonce = o.sourceCall.nonce
	}
	// Own each original descriptor exactly once. A native close error must
	// survive successful helper/target evidence; retrying Close could target a
	// reused descriptor, while ignoring it would falsely settle the source.
	files := []*os.File{}
	closed := map[*os.File]bool{}
	var closeErr error
	closeFile := func(f *os.File) {
		if f == nil || closed[f] {
			return
		}
		closed[f] = true
		nativeErr := f.Close()
		closeErr = errors.Join(closeErr, nativeErr)
		if o.sourceCall != nil {
			o.sourceCall.recordClose(f, nativeErr)
		}
	}
	defer func() {
		for _, f := range files {
			closeFile(f)
		}
		if closeErr != nil {
			resultErr = ErrHelperCleanupUnknown
		}
	}()
	var requestFile *os.File
	if o.sourceCall != nil {
		requestFile = o.sourceCall.request
		if requestFile == nil {
			return errors.New("original request loan unavailable")
		}
	} else {
		requestFile, err = heldExecRequest(reviewedExecRequest{Plan: o.Plan, Pod: pod, Kind: kind}, nonce)
	}
	if err != nil {
		return err
	}
	files = append(files, requestFile)
	controlR, controlW, err := os.Pipe()
	if err != nil {
		return err
	}
	files = append(files, controlR, controlW)
	replyR, replyW, err := os.Pipe()
	if err != nil {
		closeFile(controlR)
		closeFile(controlW)
		return err
	}
	files = append(files, replyR, replyW)
	cmd := exec.Command(o.image, ExecHelperArgument)
	cmd.Env = os.Environ()
	cmd.ExtraFiles = []*os.File{requestFile, controlR, replyW}
	if payload != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, payload)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if o.sourceCall != nil {
		o.sourceCall.recordCmd(cmd)
	}
	if o.sourceCall != nil {
		if err := o.sourceCall.checkCurrent(ctx); err != nil {
			return err
		}
	}
	if err := cmd.Start(); err != nil {
		closeFile(controlR)
		closeFile(replyW)
		closeFile(controlW)
		closeFile(replyR)
		return err
	}
	// These ends belong only to the child. Keeping replyW open in the parent
	// would hide an early child exit from the bounded IPC reader.
	closeFile(controlR)
	closeFile(replyW)
	// Parent owns one bounded IPC worker. Closing both pipes after signaling
	// unblocks it, and the worker is joined before returning.
	type reply struct {
		result reviewedExecResult
		err    error
	}
	replied := make(chan reply, 1)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		var hello helperPacket
		err := readBoundedHelperJSON(replyR, &hello, maxHelperPacket)
		if err == nil && (hello.Version != 1 || hello.Nonce != nonce || hello.PID != cmd.Process.Pid || hello.PGID != cmd.Process.Pid) {
			err = errors.New("foreign Kubernetes exec helper hello")
		}
		if err == nil && o.sourceCall != nil {
			err = o.sourceCall.checkCurrent(ctx)
		}
		if err == nil {
			err = writeHelperBytes(controlW, []byte{'G'})
		}
		var result reviewedExecResult
		if err == nil {
			err = readBoundedHelperJSON(replyR, &result, 12288)
		}
		replied <- reply{result, err}
	}()
	if o.sourceCall != nil {
		o.sourceCall.recordWorker(workerDone)
	}
	var observed reply
	select {
	case observed = <-replied:
	case <-ctx.Done():
		observed.err = ctx.Err()
	}
	// All signals precede Cmd.Wait on this original child. The process group
	// remains anchored by the unreaped child, avoiding a saved-PID signal.
	signalErr := signalCurrentKubeGroup(cmd.Process.Pid)
	if signalErr != nil {
		_ = cmd.Process.Kill() // still the original unreaped child
	}
	closeFile(controlW)
	closeFile(replyR)
	if o.sourceCall != nil {
		o.sourceCall.retireBeforeWait()
	}
	waitErr := cmd.Wait()
	if o.sourceCall != nil && o.sourceCall.afterWait != nil {
		o.sourceCall.afterWait()
	}
	<-workerDone
	groupGone := observeKubeGroupAbsence(cmd.Process.Pid)
	if o.sourceCall != nil {
		o.sourceCall.recordWait(groupGone)
	}
	if signalErr != nil || !groupGone {
		return fmt.Errorf("%w: signal=%v absent=%t", ErrHelperCleanupUnknown, signalErr, groupGone)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if observed.err != nil {
		return observed.err
	}
	if !ownedHelperKilled(waitErr) {
		return fmt.Errorf("Kubernetes exec helper did not end by owned signal: %v", waitErr)
	}
	if len(observed.result.Output) > 4096 || len(observed.result.Stderr) > 4096 || len(observed.result.Error) > 4096 {
		return errors.New("Kubernetes helper output exceeded bound")
	}
	if observed.result.Error != "" {
		return errors.New(observed.result.Error)
	}
	if output != nil {
		if err := writeHelperBytes(output, []byte(observed.result.Output)); err != nil {
			return fmt.Errorf("Kubernetes helper stdout: %w", err)
		}
	}
	if stderr != nil {
		if err := writeHelperBytes(stderr, []byte(observed.result.Stderr)); err != nil {
			return fmt.Errorf("Kubernetes helper stderr: %w", err)
		}
	}
	return nil
}

func ownedHelperKilled(err error) bool {
	var exited *exec.ExitError
	if !errors.As(err, &exited) {
		return false
	}
	status, ok := exited.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGKILL
}
