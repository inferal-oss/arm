package kubeprovision

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/client"
	"github.com/inferal-oss/arm/internal/model"
)

type lockedOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (o *lockedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.buffer.Len() > 8192 {
		return len(p), nil
	}
	return o.buffer.Write(p)
}
func (o *lockedOutput) String() string { o.mu.Lock(); defer o.mu.Unlock(); return o.buffer.String() }

// This optional acceptance runs the actual ARM CLI with the same derived
// budget arguments as the Pod bootstrap. It does not exercise /arm/private,
// the marker, carrier catalog, Kubernetes or a Pod cgroup.
func TestActualARMBootstrapBudgetArguments(t *testing.T) {
	binary := os.Getenv("ARM_KUBE_TEST_BINARY")
	if binary == "" {
		t.Skip("set ARM_KUBE_TEST_BINARY to an absolute built ARM executable")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("ARM_KUBE_TEST_BINARY must be absolute")
	}
	st, err := os.Stat(binary)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&0111 == 0 {
		t.Fatalf("unusable ARM binary: %v", err)
	}
	budgets := Resources{CPURequest: "100m", CPULimit: "500m", MemoryRequest: "128Mi", MemoryLimit: "512Mi", EphemeralRequest: "256Mi", EphemeralLimit: "1Gi"}
	cpu, memory, disk, err := bootstrapBudgets(budgets)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "state")
	cmd := exec.Command(binary, "--home", home, "serve", "--listen", "127.0.0.1:0", "--cpu", cpu, "--memory", memory, "--disk", disk)
	var output lockedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waited := false
	defer func() {
		if waited {
			return
		}
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	deadline := time.Now().Add(15 * time.Second)
	var status model.Status
	for {
		select {
		case err := <-done:
			waited = true
			t.Fatalf("ARM exited before authenticated readiness: %v\n%s", err, output.String())
		default:
		}
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		err = client.Call(ctx, home, "status", nil, &status)
		cancel()
		if err == nil && status.Host != "" && status.CPU != nil && status.CPU.Ceiling != nil &&
			status.Memory != nil && status.Memory.Ceiling != nil && status.Disk != nil && status.Disk.Ceiling != nil {
			// Bootstrap arguments set ceilings, not physical availability.
			// Hidden quota ancestry or busy native resources must remain
			// unknown/zero instead of fabricating positive grant capacity.
			if *status.CPU.Ceiling != 0.1 || *status.Memory.Ceiling != 128<<20 || *status.Disk.Ceiling != 256<<20 {
				t.Fatalf("bootstrap ceilings differ from reviewed requests: %+v", status)
			}
			if status.Capacity.CPU < 0 || status.Capacity.CPU > *status.CPU.Ceiling ||
				!status.CPU.Known && status.Capacity.CPU != 0 {
				t.Fatalf("unproven CPU capacity escaped native admission guard: %+v", status.CPU)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ARM did not publish authenticated reviewed budget ceilings: %v", err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		waited = true
	case <-time.After(10 * time.Second):
		t.Fatal("ARM did not stop after owned SIGTERM")
	}
	if _, err := os.Stat(filepath.Join(home, "endpoint.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned endpoint remains after Wait: %v", err)
	}
}
