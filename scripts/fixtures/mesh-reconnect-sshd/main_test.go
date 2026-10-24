package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// These unit tests do not open an SSH listener or launch an ARM helper.
func TestListenAddressRefusesNonLoopbackBeforeBinding(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "127.0.0.1:12345", "127.0.0.1:65535"} {
		if err := validateListenAddress(address); err != nil {
			t.Fatal(address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:0", "localhost:0", "[::1]:0", ":0", "127.0.0.1:", "127.0.0.1:-1", "127.0.0.1:+1", "127.0.0.1:http", "127.0.0.1:65536", "127.0.0.1:000000", strings.Repeat("x", 33)} {
		if err := validateListenAddress(address); err == nil {
			t.Fatal("unsafe fixture bind accepted", address)
		}
	}
}

func TestPacketHomeAndJSONBounds(t *testing.T) {
	home := "/private/fixture/state"
	good := []byte(`{"Home":"/private/fixture/state","Action":"lifecycle_inspect"}`)
	if p, e := parsePacket(good, home); e != nil || p.Action != "lifecycle_inspect" {
		t.Fatal(p, e)
	}
	for _, raw := range []string{`{"Home":"/other","Action":"lifecycle_inspect"}`, `{"Home":"/private/fixture/state"}`, `{"Home":"/private/fixture/state","Home":"/private/fixture/state","Action":"lifecycle_inspect"}`, `null`, string(good) + " {}", strings.Repeat(" ", inputLimit) + string(good)} {
		if _, e := parsePacket([]byte(raw), home); e == nil {
			t.Fatal("unfenced fixture packet", len(raw))
		}
	}
}
func TestOneShotFaultMatchesExactRevokeKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control")
	s := endpoint{options: options{control: path}}
	if e := os.WriteFile(path, []byte(`{"key":"saved-key","stage":"after"}`), 0600); e != nil {
		t.Fatal(e)
	}
	wrong := packet{Action: "lifecycle_revoke"}
	wrong.Revoke.Key = "other"
	if stage, e := s.takeFault(wrong); e != nil || stage != "" {
		t.Fatal(stage, e)
	}
	if _, e := os.Stat(path); e != nil {
		t.Fatal("wrong key consumed fault")
	}
	exact := wrong
	exact.Revoke.Key = "saved-key"
	if stage, e := s.takeFault(exact); e != nil || stage != "after" {
		t.Fatal(stage, e)
	}
	if stage, e := s.takeFault(exact); e != nil || stage != "" {
		t.Fatal("fault replayed", stage, e)
	}
	if e := os.WriteFile(path, []byte(`{"key":"saved-key","stage":"after","injected":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.takeFault(exact); e == nil {
		t.Fatal("open fault schema")
	}
}
func TestBoundedOutputCapturePreservesOverflowEvidence(t *testing.T) {
	w := &capture{limit: 3}
	if n, e := w.Write([]byte("four")); e != nil || n != 4 || string(w.raw) != "fou" || !w.overflow {
		t.Fatal(w, n, e)
	}
}

func TestImageDigestRejectsOutsideBoundAndUnsafeLeaf(t *testing.T) {
	p := filepath.Join(t.TempDir(), "image")
	if e := os.WriteFile(p, []byte("small"), 0700); e != nil {
		t.Fatal(e)
	}
	if got, e := imageDigest(p, 5); e != nil || got != digest([]byte("small")) {
		t.Fatal(got, e)
	}
	if _, e := imageDigest(p, 4); e == nil {
		t.Fatal("oversize admitted")
	}
	if e := os.Chmod(p, 0777); e != nil {
		t.Fatal(e)
	}
	if _, e := imageDigest(p, 5); e == nil {
		t.Fatal("unsafe mode admitted")
	}
}

type fixtureBlockedOutput struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	unblock sync.Once
}

func (w *fixtureBlockedOutput) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}
func (w *fixtureBlockedOutput) close() { w.unblock.Do(func() { close(w.release) }) }

func TestActualZeroExitWaitDelayCannotBecomeSuccessfulResponse(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(root, "fixed-output-helper")
	// No child/daemon is spawned: the actual helper prints one byte, then its
	// builtin read awaits the parent-owned exit gate on descriptor4. Native exit
	// cannot outrun the owned output copier entering below.
	if err = os.WriteFile(image, []byte("#!/bin/sh\nprintf x\nIFS= read -r arm_fixture_gate <&4 || :\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	gateReader, gateWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer gateReader.Close()
	defer gateWriter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := fixtureCommand(ctx, image)
	command.WaitDelay = 50 * time.Millisecond
	command.ExtraFiles = []*os.File{writer, gateReader}
	output := &fixtureBlockedOutput{entered: make(chan struct{}), release: make(chan struct{})}
	defer output.close()
	command.Stdout = output
	if err = command.Start(); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	writer.Close()
	gateReader.Close()
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	var waitErr error
	joined := false
	defer func() {
		gateWriter.Close()
		output.close()
		cancel()
		if !joined {
			select {
			case <-waited:
			case <-time.After(3 * time.Second):
				t.Error("owned fixed helper Wait not joined")
			}
		}
	}()
	select {
	case <-output.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("owned output copy did not enter")
	}
	// Release only after the real copier has entered. The helper remains the
	// sole native holder of descriptor3 and exits0 after this exact EOF.
	if err := gateWriter.Close(); err != nil {
		t.Fatal(err)
	}
	// Descriptor3 is inherited only by this helper and is never explicitly
	// closed by the script. EOF observes its native teardown, with no saved PID
	// probing, signalling or escaped descendant to hold the stdout pipe.
	nativeClosed := make(chan error, 1)
	go func() { _, e := io.Copy(io.Discard, reader); nativeClosed <- e }()
	select {
	case e := <-nativeClosed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		reader.Close()
		<-nativeClosed
		t.Fatal("owned helper native descriptor did not close")
	}
	// The copier deliberately remains blocked past WaitDelay. A real Wait must
	// report its actual timeout once the owned writer is released; this is not a
	// fabricated error or a process left behind to retain a pipe.
	select {
	case e := <-waited:
		joined = true
		t.Fatalf("Wait returned before owned output joined: %v", e)
	case <-time.After(time.Second):
	}
	output.close()
	select {
	case waitErr = <-waited:
		joined = true
	case <-time.After(2 * time.Second):
		t.Fatal("owned Wait did not complete after output release")
	}
	if !errors.Is(waitErr, exec.ErrWaitDelay) || command.ProcessState == nil || command.ProcessState.ExitCode() != 0 {
		t.Fatalf("expected actual exit0 plus WaitDelay: state=%v error=%v", command.ProcessState, waitErr)
	}
	status, complete := helperResponseStatus(true, command.ProcessState.ExitCode(), waitErr, false)
	if status == 0 || complete {
		t.Fatal("actual WaitDelay became successful/complete response", status, complete)
	}
	t.Logf("actual helper exit0/WaitDelay returns failure%d after owned output copier joins", status)
}
func TestHelperResponseRequiresSuccessfulWaitAndNativeExit(t *testing.T) {
	for _, test := range []struct {
		name     string
		started  bool
		exit     int
		err      error
		forced   bool
		status   int
		complete bool
	}{
		{"success", true, 0, nil, false, 0, true},
		{"negative-without-error", true, -1, nil, false, 91, false},
		{"start-failure", false, -1, nil, false, 91, false},
		{"context-error-with-zero-exit", true, 0, context.Canceled, false, 91, false},
		{"positive-failure", true, 7, errors.New("fixed wait failure"), false, 7, false},
		{"fault-or-overflow", true, 0, nil, true, 91, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, complete := helperResponseStatus(test.started, test.exit, test.err, test.forced)
			if status != test.status || complete != test.complete {
				t.Fatal(status, complete)
			}
		})
	}
}
