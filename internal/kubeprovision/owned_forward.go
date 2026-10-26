package kubeprovision

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// OwnedForwardSession keeps the original helper process and joins it when
// closed. LocalPort is only transport; it conveys no mesh authorization.
type OwnedForwardSession struct {
	LocalPort uint16
	cmd       *exec.Cmd
	stop      chan struct{}
	done      chan struct{}
	once      sync.Once
	mu        sync.Mutex
	err       error
}

func (s *OwnedForwardSession) Done() <-chan struct{} { return s.done }
func (s *OwnedForwardSession) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
func (s *OwnedForwardSession) Close(ctx context.Context) error {
	s.once.Do(func() { close(s.stop) })
	select {
	case <-s.done:
		return s.Err()
	case <-ctx.Done():
		return fmt.Errorf("Kubernetes forwarding helper wait pending: %w", ctx.Err())
	}
}

// ForwardOwned uses the original keyed journal and an exact live Pod lookup
// before launching the separate process. The child independently rechecks all
// reviewed authority and Pod fields before opening the stream.
func (p Provider) ForwardOwned(ctx context.Context, key string) (*OwnedForwardSession, error) {
	if p.Journal == nil || p.API == nil {
		return nil, errors.New("Kubernetes journal and API required")
	}
	r, ok, err := p.Journal.Read(ctx, key)
	if err != nil {
		return nil, err
	}
	if !ok || r.Pod == nil || r.Phase != PhaseCreated || r.StartPhase != "marker_verified" {
		return nil, ErrUnknown
	}
	pod, err := p.ExactPod(ctx, key)
	if err != nil {
		return nil, err
	}
	transport, err := NewOwnedExec(r.Plan)
	if err != nil {
		return nil, err
	}
	if proof, err := p.LookupRuntime(ctx, key, transport); err != nil || proof.PodUID != pod.UID {
		if err != nil {
			return nil, err
		}
		return nil, ErrIdentity
	}
	return StartOwnedForward(ctx, r.Plan, pod)
}

func StartOwnedForward(ctx context.Context, plan Plan, pod Pod) (*OwnedForwardSession, error) {
	image, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return startOwnedForwardImage(ctx, image, plan, pod)
}

func startOwnedForwardImage(ctx context.Context, image string, plan Plan, pod Pod) (*OwnedForwardSession, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if err := checkPod(plan, pod); err != nil {
		return nil, err
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(random[:])
	request, err := heldOperationRequest(reviewedForwardRequest{Plan: plan, Pod: pod, RemotePort: reviewedARMForwardPort}, nonce)
	if err != nil {
		return nil, err
	}
	defer request.Close()
	controlR, controlW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	replyR, replyW, err := os.Pipe()
	if err != nil {
		controlR.Close()
		controlW.Close()
		return nil, err
	}
	cmd := exec.Command(image, ForwardHelperArgument)
	cmd.Env = os.Environ()
	cmd.ExtraFiles = []*os.File{request, controlR, replyW}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		controlR.Close()
		replyW.Close()
		controlW.Close()
		replyR.Close()
		return nil, err
	}
	// Only the child may retain these ends. In particular, parent-held replyW
	// prevents EOF when the helper exits before hello or readiness.
	controlR.Close()
	replyW.Close()
	session := &OwnedForwardSession{cmd: cmd, stop: make(chan struct{}), done: make(chan struct{})}
	type ready struct {
		port uint16
		err  error
	}
	readyCh := make(chan ready, 1)
	ipcDone := make(chan struct{})
	go func() {
		defer close(ipcDone)
		var hello helperPacket
		err := readBoundedHelperJSON(replyR, &hello, maxHelperPacket)
		if err == nil && (hello.Version != 1 || hello.Nonce != nonce || hello.PID != cmd.Process.Pid || hello.PGID != cmd.Process.Pid) {
			err = errors.New("foreign Kubernetes forwarding helper hello")
		}
		if err == nil {
			err = writeHelperBytes(controlW, []byte{'G'})
		}
		var result reviewedForwardResult
		if err == nil {
			err = readBoundedHelperJSON(replyR, &result, maxHelperPacket)
		}
		if err == nil && result.Error != "" {
			err = errors.New(result.Error)
		}
		if err == nil && result.LocalPort == 0 {
			err = errors.New("Kubernetes forwarding helper has no loopback port")
		}
		readyCh <- ready{result.LocalPort, err}
		if err == nil {
			var extra [1]byte
			if n, _ := replyR.Read(extra[:]); n != 0 {
				session.mu.Lock()
				session.err = errors.New("Kubernetes forwarding helper sent extra output")
				session.mu.Unlock()
			}
		}
	}()
	go func() {
		var reason string
		select {
		case <-ctx.Done():
			reason = "context"
		case <-session.stop:
			reason = "stop"
		case <-ipcDone:
			reason = "child"
		}
		signalErr := signalCurrentKubeGroup(cmd.Process.Pid)
		if signalErr != nil {
			_ = cmd.Process.Kill() // still the original unreaped child
		}
		controlW.Close()
		replyR.Close()
		waitErr := cmd.Wait()
		<-ipcDone
		groupGone := observeKubeGroupAbsence(cmd.Process.Pid)
		session.mu.Lock()
		if session.err == nil {
			if signalErr != nil || !groupGone {
				session.err = fmt.Errorf("Kubernetes forwarding helper group closure unresolved: signal=%v absent=%t", signalErr, groupGone)
			} else if !ownedHelperKilled(waitErr) && reason != "child" {
				session.err = fmt.Errorf("Kubernetes forwarding helper did not end by owned signal: %v", waitErr)
			} else {
				switch reason {
				case "context":
					session.err = ctx.Err()
				case "stop":
					if ctx.Err() != nil {
						session.err = ctx.Err()
					}
				case "child":
					session.err = fmt.Errorf("Kubernetes forwarding helper exited before close: %v", waitErr)
				}
			}
		}
		session.mu.Unlock()
		close(session.done)
	}()
	var prepared ready
	select {
	case prepared = <-readyCh:
	case <-ctx.Done():
		prepared.err = ctx.Err()
	}
	if prepared.err != nil {
		session.once.Do(func() { close(session.stop) })
		<-session.done
		return nil, prepared.err
	}
	if err := ctx.Err(); err != nil {
		session.once.Do(func() { close(session.stop) })
		<-session.done
		return nil, err
	}
	select {
	case <-session.done:
		return nil, fmt.Errorf("Kubernetes forwarding helper ended at readiness: %w", session.Err())
	default:
	}
	session.LocalPort = prepared.port
	return session, nil
}
