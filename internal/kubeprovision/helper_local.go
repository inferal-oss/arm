package kubeprovision

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// HelperArgument is a hidden, pipe-only self-helper entrypoint. No Kubernetes
// effect is admitted by this initial lifecycle primitive.
const HelperArgument = "_kube-stream-helper"

const maxHelperPacket = 4096

type helperPacket struct {
	Version int    `json:"version"`
	Nonce   string `json:"nonce"`
	PID     int    `json:"pid,omitempty"`
	PGID    int    `json:"pgid,omitempty"`
}

func writeHelperPacket(w io.Writer, value helperPacket) error {
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || len(data) > maxHelperPacket {
		return errors.New("invalid bounded Kubernetes helper packet")
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(data)))
	if err = writeHelperBytes(w, size[:]); err != nil {
		return err
	}
	return writeHelperBytes(w, data)
}

func writeHelperBytes(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func readHelperPacket(r io.Reader) (helperPacket, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return helperPacket{}, err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > maxHelperPacket {
		return helperPacket{}, errors.New("Kubernetes helper packet exceeds bound")
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return helperPacket{}, err
	}
	var packet helperPacket
	if err := json.Unmarshal(data, &packet); err != nil {
		return helperPacket{}, err
	}
	return packet, nil
}

// HelperMain accepts only the two inherited anonymous pipes. Parent pipe loss
// ends this helper. It deliberately has no arbitrary command or stream mode.
func HelperMain(args []string) int {
	if len(args) != 0 {
		return 125
	}
	in, out := os.NewFile(3, "kube-helper-in"), os.NewFile(4, "kube-helper-out")
	if in == nil || out == nil {
		return 125
	}
	defer in.Close()
	defer out.Close()
	for _, file := range []*os.File{in, out} {
		info, err := file.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			return 125
		}
		syscall.CloseOnExec(int(file.Fd()))
	}
	if pgid, err := syscall.Getpgid(0); err != nil || pgid != os.Getpid() {
		return 125
	}
	init, err := readHelperPacket(in)
	if err != nil || init.Version != 1 || len(init.Nonce) != 64 {
		return 125
	}
	if raw, err := hex.DecodeString(init.Nonce); err != nil || len(raw) != 32 {
		return 125
	}
	if err := writeHelperPacket(out, helperPacket{Version: 1, Nonce: init.Nonce, PID: os.Getpid(), PGID: os.Getpid()}); err != nil {
		return 125
	}
	var one [1]byte
	// Extra control bytes are unsupported. EOF is the normal parent close.
	if _, err := in.Read(one[:]); err != io.EOF {
		return 125
	}
	return 0
}

// OwnedHelper retains the original child handle until its supervisor has
// signaled only that unreaped child's group and called Cmd.Wait. Done is local
// process closure; it never establishes a remote Kubernetes outcome.
type OwnedHelper struct {
	cmd      *exec.Cmd
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	err      error
}

func (h *OwnedHelper) Done() <-chan struct{} { return h.done }
func (h *OwnedHelper) Err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

// Close requests cancellation and waits for the exact original child. If the
// caller's deadline expires, the handle remains usable for a later Close.
func (h *OwnedHelper) Close(ctx context.Context) error {
	h.stopOnce.Do(func() { close(h.stop) })
	select {
	case <-h.done:
		return h.Err()
	case <-ctx.Done():
		return fmt.Errorf("Kubernetes helper wait pending: %w", ctx.Err())
	}
}

func StartOwnedHelper(ctx context.Context) (*OwnedHelper, error) {
	image, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return startOwnedHelperImage(ctx, image)
}

func startOwnedHelperImage(ctx context.Context, image string) (*OwnedHelper, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	cmd := exec.Command(image, HelperArgument)
	cmd.Env = []string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/bin"}
	cmd.ExtraFiles = []*os.File{inR, outW}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		inR.Close()
		outW.Close()
		inW.Close()
		outR.Close()
		return nil, err
	}
	// An early child exit must produce EOF on outR without relying on context
	// cancellation. The parent retains no copy of the child-only write end.
	inR.Close()
	outW.Close()
	h := &OwnedHelper{cmd: cmd, stop: make(chan struct{}), done: make(chan struct{})}
	// A single supervisor owns every signal and the one Cmd.Wait. It never
	// signals a saved PID after Wait, when that number could be reused.
	helloReady := make(chan error, 1)
	ipcDone := make(chan struct{})
	go func() {
		defer close(ipcDone)
		var nonce [32]byte
		_, err := rand.Read(nonce[:])
		if err == nil {
			err = writeHelperPacket(inW, helperPacket{Version: 1, Nonce: hex.EncodeToString(nonce[:])})
		}
		if err == nil {
			var hello helperPacket
			hello, err = readHelperPacket(outR)
			if err == nil && (hello.Version != 1 || hello.Nonce != hex.EncodeToString(nonce[:]) || hello.PID != cmd.Process.Pid || hello.PGID != cmd.Process.Pid) {
				err = errors.New("foreign Kubernetes helper hello")
			}
		}
		helloReady <- err
		if err == nil {
			// Exactly one hello is permitted. A single extra byte is enough to
			// reject the stream; never drain unbounded child output.
			var extra [1]byte
			if n, _ := outR.Read(extra[:]); n != 0 {
				h.mu.Lock()
				h.err = errors.New("Kubernetes helper sent unbounded extra output")
				h.mu.Unlock()
			}
		}
	}()
	go func() {
		var reason string
		select {
		case <-ctx.Done():
			reason = "context"
		case <-h.stop:
			reason = "stop"
		case <-ipcDone:
			reason = "child"
		}
		signalErr := signalCurrentKubeGroup(cmd.Process.Pid)
		if signalErr != nil {
			_ = cmd.Process.Kill() // original unreaped child only
		}
		inW.Close()
		outR.Close()
		waitErr := cmd.Wait()
		<-ipcDone
		groupGone := observeKubeGroupAbsence(cmd.Process.Pid)
		h.mu.Lock()
		if h.err == nil {
			if signalErr != nil || !groupGone {
				h.err = fmt.Errorf("Kubernetes helper group closure unresolved: signal=%v absent=%t", signalErr, groupGone)
			} else {
				switch reason {
				case "context":
					h.err = ctx.Err()
				case "stop":
					if ctx.Err() != nil {
						h.err = ctx.Err()
					}
				case "child":
					h.err = fmt.Errorf("Kubernetes helper exited before stop: %v", waitErr)
				}
			}
		}
		h.mu.Unlock()
		close(h.done)
	}()
	var helloErr error
	select {
	case helloErr = <-helloReady:
	case <-ctx.Done():
		helloErr = ctx.Err()
	}
	if helloErr != nil {
		h.mu.Lock()
		h.err = helloErr
		h.mu.Unlock()
		h.stopOnce.Do(func() { close(h.stop) })
		<-h.done
		return nil, helloErr
	}
	if err := ctx.Err(); err != nil {
		h.stopOnce.Do(func() { close(h.stop) })
		<-h.done
		return nil, err
	}
	return h, nil
}
