package kubeprovision

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// This uses the actual ARM entrypoint, not a test-only imitation of the
// hidden dispatch. Set ARM_KUBE_TEST_BINARY to a freshly built ARM executable.
func TestOwnedHelperRealBinaryCancellationJoins(t *testing.T) {
	image := os.Getenv("ARM_KUBE_TEST_BINARY")
	if image == "" {
		t.Skip("set ARM_KUBE_TEST_BINARY to a freshly built ARM executable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, err := startOwnedHelperImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	pid := h.cmd.Process.Pid
	select {
	case <-h.Done():
		t.Fatalf("helper exited before cancellation: %v", h.Err())
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	deadline, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := h.Close(deadline); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled helper result = %v", err)
	}
	select {
	case <-h.Done():
	default:
		t.Fatal("Close returned before original Cmd.Wait")
	}
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("original child %d remains after Wait: %v", pid, err)
	}
}

func TestOwnedHelperRealBinaryCloseJoins(t *testing.T) {
	image := os.Getenv("ARM_KUBE_TEST_BINARY")
	if image == "" {
		t.Skip("set ARM_KUBE_TEST_BINARY to a freshly built ARM executable")
	}
	h, err := startOwnedHelperImage(context.Background(), image)
	if err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.Close(deadline); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(deadline); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
}

func TestOwnedHelperRejectsPublicInvocation(t *testing.T) {
	image := os.Getenv("ARM_KUBE_TEST_BINARY")
	if image == "" {
		t.Skip("set ARM_KUBE_TEST_BINARY to a freshly built ARM executable")
	}
	cmd := exec.Command(image, HelperArgument)
	if err := cmd.Run(); err == nil {
		t.Fatal("helper accepted invocation without private inherited pipes")
	}
}
