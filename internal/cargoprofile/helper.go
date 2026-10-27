package cargoprofile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"time"

	"github.com/inferal-oss/arm/internal/leaseclock"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/usage"
)

// HelperRuntime is supplied from startup-verified image evidence by the native
// entrypoint. It is not supplied by a user request or used as release trust.
type HelperRuntime struct {
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}
type HelperInit struct {
	Clock              *leaseclock.Deadline `json:"elapsed_deadline"`
	Command            Command              `json:"command"`
	CWD                string               `json:"cwd"`
	Allocation         string               `json:"allocation"`
	AllocationIdentity model.FileIdentity   `json:"allocation_identity"`
	Runtime            HelperRuntime        `json:"runtime"`
	Expires            time.Time            `json:"expires"`
	StartDeadline      *time.Time           `json:"start_deadline,omitempty"`
}
type HelperHello struct {
	Runtime HelperRuntime         `json:"runtime"`
	Process model.ProcessIdentity `json:"process"`
}
type HelperGrant struct {
	Clock          *leaseclock.Deadline `json:"elapsed_deadline"`
	Approved       bool                 `json:"approved"`
	Revision       int                  `json:"revision"`
	Expires        time.Time            `json:"expires"`
	StartDeadline  *time.Time           `json:"start_deadline,omitempty"`
	EvidenceSHA256 string               `json:"evidence_sha256,omitempty"`
}
type HelperAck struct {
	Step     int `json:"step"`
	Revision int `json:"revision"`
}
type HelperPolicy struct {
	Evidence *FileEvidence `json:"evidence"`
}
type HelperStarted struct {
	At      time.Time             `json:"at"`
	Process model.ProcessIdentity `json:"process"`
}
type HelperFinished struct {
	Code           int       `json:"code"`
	CommandStarted bool      `json:"command_started"`
	At             time.Time `json:"at"`
	Failure        string    `json:"failure,omitempty"`
}

func PreparedDigest(prepared Prepared) (string, error) {
	raw, e := json.Marshal(prepared)
	if e != nil || len(raw) > 256<<10 {
		return "", fmt.Errorf("Cargo prepared evidence exceeds durable record bound")
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}
func grantValid(grant HelperGrant, init HelperInit) bool {
	sample, clockErr := leaseclock.Read()
	remaining, deadlineErr := init.Clock.Remaining(sample, &init.Expires)
	if clockErr != nil || deadlineErr != nil || remaining <= 0 || !reflect.DeepEqual(grant.Clock, init.Clock) {
		return false
	}
	if !grant.Approved || grant.Revision < 1 || grant.Expires.IsZero() || !grant.Expires.Equal(init.Expires) {
		return false
	}
	if (grant.StartDeadline == nil) != (init.StartDeadline == nil) {
		return false
	}
	if grant.StartDeadline != nil && (!grant.StartDeadline.Equal(*init.StartDeadline) || !time.Now().Before(*grant.StartDeadline)) {
		return false
	}
	return true
}

// RunHelper is only the hidden fixed native child entrypoint. Its inherited
// pipes are separate from project stdout/stderr. No source/tool work happens
// until a captured native identity has received its durable preflight grant.
func RunHelper(parent context.Context, in io.Reader, out io.Writer, runtime HelperRuntime, stdout, stderr io.Writer) int {
	// Private parent IPC must not survive exec into Cargo/rustc/build scripts.
	for _, value := range []any{in, out} {
		if file, ok := value.(*os.File); ok {
			syscall.CloseOnExec(int(file.Fd()))
		}
	}
	packet, e := ReadHelperPacket(in)
	if e != nil || packet.Kind != "init" || packet.Sequence != 1 {
		return 125
	}
	var init HelperInit
	if e = DecodeCanonical(packet.Data, &init); e != nil {
		return 125
	}
	if init.Runtime != runtime || len(runtime.SHA256) != 64 || runtime.Size < 1 || runtime.Version == "" || runtime.Protocol < 1 || !filepath.IsAbs(init.CWD) || !validText(init.CWD) {
		return 125
	}
	canonical, e := Parse(init.Command.Original, float64(init.Command.Jobs))
	if e != nil || !sameCommand(canonical, init.Command) {
		return 125
	}
	process, e := usage.Capture(os.Getpid())
	if e != nil || process.PGID != os.Getpid() || process.PID != os.Getpid() {
		return 125
	}
	channel := HelperChannel{In: in, Out: out, Lease: packet.Lease, Nonce: packet.Nonce, received: 1}
	if e = channel.Send("hello", HelperHello{Runtime: runtime, Process: process}); e != nil {
		return 125
	}
	var grant HelperGrant
	if e = channel.Receive("preflight", &grant); e != nil || !grantValid(grant, init) {
		return 125
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	// Pump bounded bytes, not parsed future work, so parent EOF is noticed even
	// while an admitted tool query is running. The group was captured above as
	// this current helper's own PGID. No recovered PID is signalled here.
	pipeIn, pipeOut := io.Pipe()
	defer pipeIn.Close()
	normalDone := make(chan struct{})
	defer close(normalDone)
	parentLost := func() {
		select {
		case <-normalDone:
			return
		default:
			cancel()
			_ = syscall.Kill(-os.Getpid(), syscall.SIGKILL)
		}
	}
	channel.In = pipeIn
	go func() {
		_, _ = io.Copy(pipeOut, in)
		_ = pipeOut.Close()
		parentLost()
	}()
	sample, clockErr := leaseclock.Read()
	remaining, deadlineErr := init.Clock.Remaining(sample, &init.Expires)
	if clockErr != nil || deadlineErr != nil || remaining <= 0 {
		return 125
	}
	prepareCtx, prepareCancel := context.WithTimeout(ctx, remaining)
	if init.StartDeadline != nil {
		// Caller start deadlines retain their separately reviewed authorization semantics.
		startCtx, startCancel := context.WithDeadline(prepareCtx, *init.StartDeadline)
		defer startCancel()
		prepareCtx = startCtx
	}
	defer prepareCancel()
	finish := func(code int, started bool, err error) int {
		failure := ""
		if err != nil {
			failure = err.Error()
			if len(failure) > 4096 {
				failure = "Cargo helper failed; diagnostics omitted"
			}
		}
		_ = channel.Send("finished", HelperFinished{Code: code, CommandStarted: started, At: time.Now().UTC(), Failure: failure})
		return code
	}
	owned, e := CreateOwnedPaths(prepareCtx, init.Allocation, init.AllocationIdentity, packet.Lease, func(step BirthStep) error {
		if e := channel.Send("birth", step); e != nil {
			return e
		}
		var ack HelperAck
		if e := channel.Receive("recorded", &ack); e != nil {
			return e
		}
		if ack.Step != step.Step || ack.Revision < 1 {
			return fmt.Errorf("Cargo born evidence was not durably acknowledged")
		}
		return nil
	})
	if e != nil {
		return finish(125, false, e)
	}
	prepared, e := Prepare(prepareCtx, init.Command, init.CWD, owned, os.Environ(), func(evidence *FileEvidence) error {
		if e := channel.Send("policy", HelperPolicy{Evidence: evidence}); e != nil {
			return e
		}
		var ack HelperAck
		if e := channel.Receive("recorded", &ack); e != nil {
			return e
		}
		if ack.Step != 5 || ack.Revision < 1 {
			return fmt.Errorf("Cargo policy was not durably acknowledged")
		}
		return nil
	})
	if e != nil {
		return finish(125, false, e)
	}
	digest, e := PreparedDigest(prepared)
	if e != nil {
		return finish(125, false, e)
	}
	if e = channel.Send("prepared", prepared); e != nil {
		return 125
	}
	if e = channel.Receive("compile", &grant); e != nil || !grantValid(grant, init) || grant.EvidenceSHA256 != digest {
		return finish(125, false, fmt.Errorf("Cargo compilation gate was not authorized"))
	}
	if e = prepared.Revalidate(prepareCtx, os.Environ()); e != nil {
		return finish(125, false, e)
	}
	if !grantValid(grant, init) || prepareCtx.Err() != nil {
		return finish(125, false, fmt.Errorf("Cargo start deadline/lease expired during final local checks"))
	}
	// All further input is invalid. EOF means the supervising parent vanished;
	// this is our CURRENT, self-created process group, not a saved/log-named PID.
	go func() {
		var extra [1]byte
		_, _ = channel.In.Read(extra[:])
		parentLost()
	}()
	cmd := exec.CommandContext(ctx, prepared.Tools.Cargo.Path, prepared.Plan.Arguments...)
	cmd.Dir = prepared.Audit.CWD
	cmd.Env = prepared.Plan.Environment
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if e = cmd.Start(); e != nil {
		return finish(125, false, fmt.Errorf("Cargo compilation process failed to start"))
	}
	child, e := usage.Capture(cmd.Process.Pid)
	if e != nil || child.PGID != process.PGID || child.UID != process.UID {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return finish(125, true, fmt.Errorf("Cargo child group identity could not be verified"))
	}
	if e = channel.Send("started", HelperStarted{At: time.Now().UTC(), Process: child}); e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 125
	}
	e = cmd.Wait()
	code := 0
	if e != nil {
		code = 1
		if exit, ok := e.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
		if code < 1 {
			code = 1
		}
	}
	return finish(code, true, nil)
}
func sameCommand(a, b Command) bool {
	return reflect.DeepEqual(a, b)
}
