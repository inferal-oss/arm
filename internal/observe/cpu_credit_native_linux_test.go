package observe

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/usage"
)

func TestNativeLinuxManagedCPUZeroFallback(t *testing.T) {
	if os.Getenv("ARM_TEST_NATIVE_CPU_CREDIT") != "1" {
		t.Skip("enable bounded native Linux credit fallback check")
	}
	child := exec.Command("/bin/sleep", "1")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			if err := child.Wait(); err != nil {
				t.Error("original sleeping helper Wait", err)
			}
		}
	}()
	root, err := usage.Capture(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if cookie, err := usage.CaptureCPUAnchor(root); err == nil || cookie != "" {
		t.Fatal("Linux accounting unexpectedly became supported", cookie, err)
	}
	// Even a claimed cookie from an internal caller cannot promote unsupported
	// Linux process/host accounting into a native CPU credit candidate.
	owner := usage.CPUOwner{Token: "native-linux-unsupported", Root: root, Cookie: "unverified-cookie"}
	sampler := NewCPUSampler()
	defer sampler.Close()
	collect := func(ctx context.Context) usage.CPUCut { return usage.CollectCPU(ctx, []usage.CPUOwner{owner}) }
	sampler.SampleManaged(t.Context(), collect)
	time.Sleep(200 * time.Millisecond)
	reading, frame := sampler.SampleManaged(t.Context(), collect)
	if !reading.Known || reading.Available == nil {
		t.Fatal("real native CPU headroom was lost", reading)
	}
	if frame.Eligible || len(frame.Credits) != 0 || frame.Reason == "" {
		t.Fatal("unsupported Linux accounting received managed credit", frame)
	}
	if err := child.Wait(); err != nil {
		waited = true
		t.Fatal("original Linux sleeping helper Wait", err)
	}
	waited = true
	t.Logf("actual native Linux headroom %.3f remains known; managed credit0 (%s); original sleeping helper Wait0", *reading.Available, frame.Reason)
}
