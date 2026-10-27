package manager

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"getarm.org/internal/childowner"
)

// Unexpected native binding failures retain the original handle without
// reconstructing signal or Wait authority from its numeric PID.
var cargoCancelUnknownFixtures struct {
	sync.Mutex
	commands []*exec.Cmd
}

// A second cleanup operation leaves a bounded opportunity for EOF-triggered
// code to write its marker if IPC closes before the original signal. Success
// still requires the actual parent KILL, no marker, and a positive sole join.
type cargoEOFMarkerCloser struct{ marker string }

func (c cargoEOFMarkerCloser) Close() error {
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		_, err := os.Stat(c.marker)
		if err == nil {
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCargoOriginalCancellationPrecedesIPCClose(t *testing.T) {
	if os.Getenv("ARM_CARGO_CANCEL_NATIVE_CHILD") == "1" {
		if syscall.Getpgrp() != os.Getpid() {
			os.Exit(120)
		}
		gate := os.NewFile(3, "original-private-gate")
		ipc := os.NewFile(4, "original-parent-ipc")
		var token [3]byte
		if _, err := io.ReadFull(gate, token[:]); err != nil || string(token[:]) != "go\n" {
			os.Exit(121)
		}
		_ = gate.Close()
		if err := os.WriteFile(os.Getenv("ARM_CARGO_CANCEL_READY"), []byte("ready"), 0600); err != nil {
			os.Exit(122)
		}
		if _, err := io.Copy(io.Discard, ipc); err != nil {
			os.Exit(123)
		}
		if err := os.WriteFile(os.Getenv("ARM_CARGO_CANCEL_EOF_MARKER"), []byte("unsafe EOF before original signal"), 0600); err != nil {
			os.Exit(124)
		}
		// Mirror the fixed helper's parent-loss cleanup: it kills its own group.
		_ = syscall.Kill(0, syscall.SIGKILL)
		os.Exit(125)
	}
	if err := childowner.Available(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ready := filepath.Join(root, "ready")
	marker := filepath.Join(root, "after-eof")
	gateRead, gateWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ipcRead, ipcWrite, err := os.Pipe()
	if err != nil {
		gateRead.Close()
		gateWrite.Close()
		t.Fatal(err)
	}
	files := []*os.File{gateRead, gateWrite, ipcRead, ipcWrite}
	var cmd *exec.Cmd
	var owner *childowner.Owner
	waited := false
	retainUnknown := func() {
		cargoCancelUnknownFixtures.Lock()
		cargoCancelUnknownFixtures.commands = append(cargoCancelUnknownFixtures.commands, cmd)
		cargoCancelUnknownFixtures.Unlock()
	}
	t.Cleanup(func() {
		if owner != nil && !waited {
			signalErr := cancelCargoOriginal(owner, gateWrite, ipcWrite)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			waited = true
			result := owner.Wait(ctx)
			cancel()
			if signalErr != nil && !errors.Is(signalErr, childowner.ErrRetired) && !errors.Is(signalErr, syscall.ESRCH) {
				t.Errorf("original causal fixture cleanup signal: %v", signalErr)
			}
			if !result.Reaped || result.CleanupError != nil {
				t.Errorf("original causal fixture join uncertain: %+v", result)
				if !result.Reaped {
					retainUnknown()
				}
			}
		}
		for _, file := range files {
			_ = file.Close()
		}
	})
	image, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(image, "-test.run=^TestCargoOriginalCancellationPrecedesIPCClose$", "-test.timeout=10s")
	cmd.Env = append(os.Environ(), "ARM_CARGO_CANCEL_NATIVE_CHILD=1", "ARM_CARGO_CANCEL_READY="+ready, "ARM_CARGO_CANCEL_EOF_MARKER="+marker)
	cmd.ExtraFiles = []*os.File{gateRead, ipcRead}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	owner, err = childowner.New(cmd)
	if err != nil {
		retainUnknown()
		t.Fatalf("original causal fixture binding unknown; no signal or Wait issued: %v", err)
	}
	_ = gateRead.Close()
	_ = ipcRead.Close()
	if _, err = gateWrite.Write([]byte("go\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(ready); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if !time.Now().Before(deadline) {
			t.Fatal("original native child did not reach its actual IPC read")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// This calls the same production helper used by BOTH cancellation watcher and
	// final cleanup. The original child is live and blocked on its actual IPC.
	if err = cancelCargoOriginal(owner, ipcWrite, cargoEOFMarkerCloser{marker: marker}); err != nil {
		t.Fatal("original KILL failed before IPC cancellation", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	waited = true
	result := owner.Wait(ctx)
	if !result.Reaped || result.CleanupError != nil || cmd.ProcessState == nil {
		if !result.Reaped {
			retainUnknown()
		}
		t.Fatal("original causal fixture did not positively join", result)
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("original fixture did not receive the parent's KILL", cmd.ProcessState)
	}
	if _, err = os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("IPC EOF ran helper parent-loss action before original control", err)
	}
	if err = syscall.Kill(-cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("original group absence was not established after sole Wait", err)
	}
	if err = owner.Signal(syscall.SIGKILL); !errors.Is(err, childowner.ErrRetired) {
		t.Fatal("signal authority survived original Wait", err)
	}
}
