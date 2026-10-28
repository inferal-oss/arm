package childowner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestOriginalOwnerNativePauseResumeAndRetirement(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "progress")
	cmd := exec.Command("/bin/sh", "-c", fmt.Sprintf("while :; do printf x >> %s; sleep 0.05; done", strconv.Quote(marker)))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	owner, err := New(cmd)
	if err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		t.Fatal(err)
	}
	done := make(chan Result, 1)
	go func() { done <- owner.Wait(context.Background()) }()
	joined := false
	t.Cleanup(func() {
		if joined {
			return
		}
		_ = owner.Signal(syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			t.Error("original native waiter did not join")
		}
	})
	size := func() int64 {
		info, err := os.Stat(marker)
		if err != nil {
			return 0
		}
		return info.Size()
	}
	until := time.Now().Add(3 * time.Second)
	for size() < 2 && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	if size() < 2 || !owner.OwnsUnreaped(cmd) {
		t.Fatal("fixture lacked a progressing original child")
	}
	if err := owner.Pause(); err != nil || !owner.Paused() {
		t.Fatal("original owner could not pause", err)
	}
	if err := owner.Pause(); !errors.Is(err, ErrPauseState) {
		t.Fatal("duplicate pause did not refuse a second effect", err)
	}
	time.Sleep(150 * time.Millisecond) // Let an already started write finish.
	stoppedAt := size()
	time.Sleep(250 * time.Millisecond)
	if size() != stoppedAt {
		t.Fatal("original child made progress after its group STOP")
	}
	if err := owner.Resume(); err != nil || owner.Paused() {
		owner.mu.Lock()
		failure, retired, signalsRetired := owner.failure, owner.retired, owner.signalsRetired
		owner.mu.Unlock()
		t.Fatal("original owner could not resume", err, failure, retired, signalsRetired, cmd.ProcessState)
	}
	until = time.Now().Add(3 * time.Second)
	for size() <= stoppedAt && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	if size() <= stoppedAt {
		t.Fatal("original child did not progress after CONT")
	}
	if err := owner.Signal(syscall.SIGKILL); err != nil {
		t.Fatal("original child cleanup failed", err)
	}
	select {
	case result := <-done:
		joined = true
		if !result.Reaped || result.CleanupError != nil || owner.OwnsUnreaped(cmd) {
			t.Fatal("sole original Wait did not retire signal authority", result)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("sole original Wait did not complete")
	}
	if err := owner.Resume(); !errors.Is(err, ErrRetired) {
		t.Fatal("retired original owner accepted a late resume", err)
	}
}

func TestPausePreflightUncertaintyStillAllowsOriginalCleanup(t *testing.T) {
	signals := 0
	o := &Owner{pid: 123, observe: func(int) (terminal, error) { return terminal{}, syscall.EACCES },
		signal: func(_ int, sig syscall.Signal) error {
			if sig != syscall.SIGKILL {
				t.Fatalf("uncertain pause sent %v", sig)
			}
			signals++
			return nil
		}}
	if err := o.Pause(); !errors.Is(err, syscall.EACCES) || o.Paused() {
		t.Fatal("uncertain observation did not refuse pause", err)
	}
	if err := o.Signal(syscall.SIGKILL); err != nil || signals != 1 {
		t.Fatal("uncertain pause disabled the original cleanup owner", err, signals)
	}
}
