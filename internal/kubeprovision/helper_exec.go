package kubeprovision

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// ExecHelperArgument admits only the fixed Pod scripts below. It is not
// an arbitrary Kubernetes command runner.
const ExecHelperArgument = "_kube-exec-helper"

type reviewedExecKind string

const (
	execShip         reviewedExecKind = "ship"
	execLookupBinary reviewedExecKind = "lookup_binary"
	execCatalogProof reviewedExecKind = "catalog_proof"
	execRuntimeProof reviewedExecKind = "runtime_proof"
	execStart        reviewedExecKind = "start_marker"
	execLookupStart  reviewedExecKind = "lookup_start_marker"
)

type reviewedExecRequest struct {
	Plan Plan             `json:"plan"`
	Pod  Pod              `json:"pod"`
	Kind reviewedExecKind `json:"kind"`
}

type reviewedExecResult struct {
	Output string `json:"output"`
	Stderr string `json:"stderr"`
	Error  string `json:"error,omitempty"`
}

func fixedExecCommand(plan Plan, pod Pod, kind reviewedExecKind) ([]string, error) {
	key := plan.Review.Key
	common := []string{key, plan.Review.BinarySHA256, strconv.FormatInt(plan.Review.BinaryBytes, 10), pod.UID}
	var script, name string
	switch kind {
	case execShip:
		script, name = shipScript, "arm-ship"
	case execLookupBinary:
		script, name = lookupBinaryScript, "arm-lookup"
	case execCatalogProof:
		script, name = catalogProofScript, "arm-catalog-proof"
	case execRuntimeProof:
		script, name = runtimeProofScript, "arm-runtime-proof"
	case execStart:
		script, name = startMarkerScript, "arm-start"
	case execLookupStart:
		script, name = lookupStartScript, "arm-start-lookup"
	default:
		return nil, errors.New("unsupported Kubernetes helper operation")
	}
	command := append([]string{"/bin/sh", "-ec", script, name}, common...)
	if kind == execStart {
		command = append(command, plan.Review.CatalogSHA256)
	}
	return command, nil
}

func classifyReviewedExec(plan Plan, pod Pod, command []string, input io.Reader) (reviewedExecKind, error) {
	if err := plan.Validate(); err != nil {
		return "", err
	}
	if err := checkPod(plan, pod); err != nil {
		return "", err
	}
	for _, kind := range []reviewedExecKind{execShip, execLookupBinary, execCatalogProof, execStart, execLookupStart, execRuntimeProof} {
		fixed, _ := fixedExecCommand(plan, pod, kind)
		if reflect.DeepEqual(command, fixed) {
			if (kind == execShip) != (input != nil) {
				return "", errors.New("Kubernetes helper payload presence changed")
			}
			return kind, nil
		}
	}
	return "", errors.New("Kubernetes helper accepts only fixed reviewed scripts")
}

func verifyHeldPayload(file *os.File, size int64, digest string) error {
	if file == nil || size < 1 || size > MaxBinaryBytes {
		return errors.New("held Kubernetes payload required")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0077 != 0 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 0 || stat.Size != size {
		return errors.New("unsafe held Kubernetes payload")
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(file, 0, size)); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return errors.New("held Kubernetes payload digest changed")
	}
	_, err := file.Seek(0, io.SeekStart)
	return err
}

func executeReviewedPod(ctx context.Context, request reviewedExecRequest, payload *os.File) reviewedExecResult {
	var result reviewedExecResult
	failed := func(error) reviewedExecResult {
		// REST statuses and credential-plugin errors may include secrets even
		// when short. Never cross the helper boundary with their text or with
		// partial remote stdout/stderr from a failed script.
		return reviewedExecResult{Error: "Kubernetes exec unavailable; inspect the original operation key"}
	}
	if err := request.Plan.Validate(); err != nil {
		return failed(err)
	}
	if err := checkPod(request.Plan, request.Pod); err != nil {
		return failed(err)
	}
	command, err := fixedExecCommand(request.Plan, request.Pod, request.Kind)
	if err != nil {
		return failed(err)
	}
	if request.Kind == execShip {
		if err := verifyHeldPayload(payload, request.Plan.Review.BinaryBytes, request.Plan.Review.BinarySHA256); err != nil {
			return failed(err)
		}
	} else if payload != nil {
		return failed(errors.New("unexpected Kubernetes helper payload"))
	}
	api, err := OpenReviewedContext(request.Plan)
	if err != nil {
		return failed(err)
	}
	provider := Provider{API: api}
	if err := provider.Check(ctx, request.Plan); err != nil {
		return failed(err)
	}
	pod, err := api.GetPod(ctx, request.Pod.Namespace, request.Pod.Name)
	if err != nil {
		return failed(err)
	}
	if err := checkPod(request.Plan, pod); err != nil || pod.UID != request.Pod.UID {
		return failed(ErrIdentity)
	}
	output, stderr := newBoundedOutput(4096), newBoundedOutput(4096)
	var input io.Reader
	if request.Kind == execShip {
		input = payload
	}
	err = api.Exec(ctx, pod, command, input, output, stderr)
	result.Output, result.Stderr = output.String(), stderr.String()
	if output.Overflow() || stderr.Overflow() {
		return failed(errors.New("Kubernetes helper output exceeded bound"))
	}
	if err != nil {
		return failed(err)
	}
	if result.Stderr != "" {
		return failed(errors.New("unexpected fixed-script stderr"))
	}
	switch request.Kind {
	case execShip, execStart:
		if result.Output != "" {
			return failed(errors.New("unexpected fixed-script output"))
		}
	case execLookupBinary:
		if result.Output != "ARM_BINARY_VERIFIED\n" {
			return failed(errors.New("binary receipt unavailable"))
		}
	case execCatalogProof:
		if result.Output != "ARM_CATALOG_VERIFIED "+request.Plan.Review.CatalogSHA256+"\n" {
			return failed(errors.New("carrier catalog proof unavailable"))
		}
	case execLookupStart:
		if result.Output != "ARM_START_MARKER_VERIFIED\n" {
			return failed(errors.New("start marker receipt unavailable"))
		}
	case execRuntimeProof:
		if !runtimeProofPattern.MatchString(result.Output) {
			return failed(errors.New("authenticated runtime proof unavailable"))
		}
	}
	return result
}

// ExecHelperMain runs only with a private, already opened request and optional
// held regular payload. It sends a hello, waits for a one-byte grant, performs
// one fixed operation, sends a bounded result, then remains an unreaped anchor
// until its parent closes or kills it.
func ExecHelperMain(args []string) int {
	if len(args) != 0 {
		return 125
	}
	requestFile := os.NewFile(3, "kube-exec-request")
	control := os.NewFile(4, "kube-exec-control")
	reply := os.NewFile(5, "kube-exec-reply")
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
		Nonce   string              `json:"nonce"`
		Request reviewedExecRequest `json:"request"`
	}
	if err := jsonUnmarshalStrict(raw, &envelope); err != nil || len(envelope.Nonce) != 64 {
		return 125
	}
	if _, err := hex.DecodeString(envelope.Nonce); err != nil {
		return 125
	}
	var payload *os.File
	if envelope.Request.Kind == execShip {
		payload = os.NewFile(6, "kube-exec-payload")
		if payload == nil {
			return 125
		}
		defer payload.Close()
		syscall.CloseOnExec(int(payload.Fd()))
	}
	if err := writeBoundedHelperJSON(reply, helperPacket{Version: 1, Nonce: envelope.Nonce, PID: os.Getpid(), PGID: os.Getpid()}, maxHelperPacket); err != nil {
		return 125
	}
	var grant [1]byte
	if _, err := io.ReadFull(control, grant[:]); err != nil || grant[0] != 'G' {
		return 125
	}
	// Parent loss kills this current helper group even while client-go is
	// blocked. No recovered PID or prior child is ever signaled.
	go func() { var b [1]byte; _, _ = control.Read(b[:]); _ = syscall.Kill(-os.Getpid(), syscall.SIGKILL) }()
	result := executeReviewedPod(context.Background(), envelope.Request, payload)
	if err := writeBoundedHelperJSON(reply, result, 12288); err != nil {
		return 125
	}
	select {}
}

func jsonUnmarshalStrict(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing Kubernetes helper request")
	}
	return nil
}
