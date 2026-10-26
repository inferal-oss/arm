package kubeprovision

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const ForwardHelperArgument = "_kube-forward-helper"
const reviewedARMForwardPort uint16 = 9443

type reviewedForwardRequest struct {
	Plan       Plan   `json:"plan"`
	Pod        Pod    `json:"pod"`
	RemotePort uint16 `json:"remote_port"`
}

type reviewedForwardResult struct {
	LocalPort uint16 `json:"local_port,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ForwardHelperMain owns the client-go upgrade and every local forwarding
// worker inside one current process group. Its only permitted remote port is
// ARM's fixed local control listener.
func ForwardHelperMain(args []string) int {
	if len(args) != 0 {
		return 125
	}
	requestFile := os.NewFile(3, "kube-forward-request")
	control := os.NewFile(4, "kube-forward-control")
	reply := os.NewFile(5, "kube-forward-reply")
	if requestFile == nil || control == nil || reply == nil {
		return 125
	}
	defer requestFile.Close()
	defer control.Close()
	defer reply.Close()
	for _, file := range []*os.File{requestFile, control, reply} {
		syscall.CloseOnExec(int(file.Fd()))
	}
	for _, file := range []*os.File{control, reply} {
		info, err := file.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			return 125
		}
	}
	if pgid, err := syscall.Getpgid(0); err != nil || pgid != os.Getpid() {
		return 125
	}
	var held unix.Stat_t
	if err := unix.Fstat(int(requestFile.Fd()), &held); err != nil || held.Mode&unix.S_IFMT != unix.S_IFREG || held.Mode&0077 != 0 || held.Uid != uint32(os.Geteuid()) || held.Nlink != 0 || held.Size < 1 || held.Size > 65536 {
		return 125
	}
	raw, err := io.ReadAll(io.LimitReader(requestFile, 65537))
	if err != nil || len(raw) != int(held.Size) {
		return 125
	}
	var envelope struct {
		Nonce   string                 `json:"nonce"`
		Request reviewedForwardRequest `json:"request"`
	}
	if err := jsonUnmarshalStrict(raw, &envelope); err != nil || len(envelope.Nonce) != 64 || envelope.Request.RemotePort != reviewedARMForwardPort {
		return 125
	}
	if _, err := hex.DecodeString(envelope.Nonce); err != nil {
		return 125
	}
	if err := writeBoundedHelperJSON(reply, helperPacket{Version: 1, Nonce: envelope.Nonce, PID: os.Getpid(), PGID: os.Getpid()}, maxHelperPacket); err != nil {
		return 125
	}
	var grant [1]byte
	if _, err := io.ReadFull(control, grant[:]); err != nil || grant[0] != 'G' {
		return 125
	}
	go func() { var b [1]byte; _, _ = control.Read(b[:]); _ = syscall.Kill(-os.Getpid(), syscall.SIGKILL) }()
	failed := func(error) int {
		// Credential-plugin, REST and remote stream failures are untrusted.
		// Their text never enters parent IPC, UI output or logs.
		if writeBoundedHelperJSON(reply, reviewedForwardResult{Error: "Kubernetes forwarding unavailable; inspect the original operation key"}, maxHelperPacket) != nil {
			return 125
		}
		select {}
	}
	request := envelope.Request
	if err := request.Plan.Validate(); err != nil {
		return failed(err)
	}
	if err := checkPod(request.Plan, request.Pod); err != nil {
		return failed(err)
	}
	api, err := OpenReviewedContext(request.Plan)
	if err != nil {
		return failed(err)
	}
	provider := Provider{API: api}
	if err := provider.Check(context.Background(), request.Plan); err != nil {
		return failed(err)
	}
	pod, err := api.GetPod(context.Background(), request.Pod.Namespace, request.Pod.Name)
	if err != nil {
		return failed(err)
	}
	if err := checkPod(request.Plan, pod); err != nil || pod.UID != request.Pod.UID {
		return failed(ErrIdentity)
	}
	session, err := api.Forward(context.Background(), pod, reviewedARMForwardPort)
	if err != nil {
		return failed(err)
	}
	if session.LocalPort == 0 {
		_ = session.Close()
		return failed(errors.New("Kubernetes forwarding helper has no loopback port"))
	}
	if err := writeBoundedHelperJSON(reply, reviewedForwardResult{LocalPort: session.LocalPort}, maxHelperPacket); err != nil {
		_ = session.Close()
		return 125
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-session.Done():
				return
			case <-ticker.C:
				checkCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := provider.Check(checkCtx, request.Plan)
				if err == nil {
					current, getErr := api.GetPod(checkCtx, request.Pod.Namespace, request.Pod.Name)
					if getErr != nil || checkPod(request.Plan, current) != nil || current.UID != request.Pod.UID {
						err = ErrIdentity
					}
				}
				cancel()
				if err != nil {
					// The current helper is its own group anchor. Ending it
					// closes the listener and upstream client-go workers.
					_ = syscall.Kill(-os.Getpid(), syscall.SIGKILL)
					return
				}
			}
		}
	}()
	<-session.Done()
	return 1
}
