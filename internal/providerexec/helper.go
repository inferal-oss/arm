package providerexec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	"getarm.org/internal/model"
	"getarm.org/internal/providerproto"
	"getarm.org/internal/storage"
	"getarm.org/internal/usage"
)

// HelperMain accepts only inherited anonymous private control pipes. It never
// opens ARM state, reads ambient configuration or establishes provider authority.
func HelperMain(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		return 125
	}
	in, out := os.NewFile(3, "provider-parent-input"), os.NewFile(4, "provider-parent-output")
	if in == nil || out == nil {
		return 125
	}
	defer in.Close()
	defer out.Close()
	for _, f := range []*os.File{in, out} {
		s, err := f.Stat()
		if err != nil || s.Mode()&os.ModeNamedPipe == 0 {
			return 125
		}
		syscall.CloseOnExec(int(f.Fd()))
	}
	return runHelper(in, out, stdout, stderr)
}
func runHelper(in, out *os.File, stdout, stderr io.Writer) int {
	var init initPacket
	// Capture nonce from the one initial packet, without accepting a second init.
	// Initial packet nonce must be returned exactly in every subsequent packet.
	p, err := readInitial(in, &init)
	if err != nil {
		return 125
	}
	// The private init channel is still bounded by each image's intended role;
	// refuse oversized reconstructed claims before any native image I/O.
	if _, err := SelectFromTrusted(init.Runtime.image(), init.Provider.image()); err != nil {
		return 125
	}
	nonce := p.Nonce
	self, err := usage.Capture(os.Getpid())
	if err != nil || self.PID != self.PGID {
		return 125
	}
	own, err := os.Executable()
	if err != nil || own != init.Runtime.Path || init.Runtime.image().checkWithin(MaxRuntimeBytes) != nil || init.Provider.image().checkWithin(MaxImageBytes) != nil {
		return 125
	}
	dir, err := os.OpenFile(init.TempPath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return 125
	}
	id, err := storage.DirectoryIdentity(int(dir.Fd()))
	dir.Close()
	if err != nil || id != init.TempIdentity {
		return 125
	}
	decoded, err := providerproto.DecodeRequest(init.Request)
	if err != nil {
		return 125
	}
	canonical, canonicalErr := providerproto.EncodeRequest(decoded.Request())
	if canonicalErr != nil || hash(canonical) != init.RequestSHA256 {
		return 125
	}
	q := decoded.Request()
	switch q.Method {
	case providerproto.Describe, providerproto.Validate, providerproto.Plan:
	default:
		return 125
	}
	// Ignore polite group TERM while retaining the parent-owned unreaped anchor.
	// Parent is solely responsible for eventual group KILL and reaping this image.
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(term)
	if err = writePacket(out, nonce, 1, "hello", helloPacket{self, init.Runtime.SHA256, init.RequestSHA256}); err != nil {
		return 125
	}
	var grant grantPacket
	if err = readPacket(in, nonce, 2, "grant", &grant); err != nil || grant.RequestSHA256 != init.RequestSHA256 {
		return 125
	}
	// Control pipe loss or extra bytes terminates only this current helper's own
	// group. This never re-signals a saved or recovered numeric process identity.
	go func() { var b [1]byte; _, _ = in.Read(b[:]); _ = syscall.Kill(-os.Getpid(), syscall.SIGKILL) }()
	var framed bytes.Buffer
	if err = providerproto.WriteRequest(&framed, q); err != nil {
		return 125
	}
	cmd := exec.Command(init.Provider.Path, "--arm-provider-v1")
	cmd.Dir = init.TempPath
	cmd.Env = []string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/bin", "TMPDIR=" + init.TempPath}
	cmd.Stdin = &framed
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err = init.Provider.image().checkWithin(MaxImageBytes); err != nil {
		return 125
	}
	started := cmd.Start() == nil
	var native model.ProcessIdentity
	if started {
		v, e := usage.Capture(cmd.Process.Pid)
		if e == nil {
			native = v
		}
	}
	var exit Exit
	if started {
		err = cmd.Wait()
		exit = processExit(err)
	} else {
		exit.Code = 125
	}
	if err = writePacket(out, nonce, 2, "result", resultPacket{native, exit, started}); err != nil {
		return 125
	}
	// Keep the group anchor alive even after provider exit until parent cleanup.
	for {
		select {
		case <-term:
		case <-context.Background().Done():
		}
	}
}

func readInitial(in io.Reader, out any) (packet, error) {
	// Read exactly one strict envelope before knowing its fresh parent nonce.
	body, err := readControlBody(in)
	if err != nil {
		return packet{}, err
	}
	var p packet
	if err = strictJSON(body, &p); err != nil {
		return p, err
	}
	if p.Version != 1 || len(p.Nonce) != 32 || p.Sequence != 1 || p.Kind != "init" {
		return p, fmt.Errorf("invalid initial packet")
	}
	if _, err = strconv.ParseUint(p.Nonce[:16], 16, 64); err != nil {
		return p, err
	}
	if _, err = strconv.ParseUint(p.Nonce[16:], 16, 64); err != nil {
		return p, err
	}
	return p, strictJSON(p.Data, out)
}
