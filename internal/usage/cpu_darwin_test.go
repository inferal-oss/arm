package usage

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// Real CPU-only ownership/ABI evidence, with no busy worker. The original root
// is held behind a private launch gate until its native cookie is captured;
// its child/grandchild finish naturally and the original parent Wait joins it.
func TestNativeDarwinCPUCutOwnsGatedDescendants(t *testing.T) {
	if os.Getenv("ARM_TEST_NATIVE_CPU_CREDIT") != "1" {
		t.Skip("enable bounded native CPU ancestry observation")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	cmd := exec.Command("/bin/sh", "-c", `IFS= read -r cpu_gate <&3 || exit 125; [ "$cpu_gate" = go ] || exit 125; exec 3<&-; /bin/sh -c '/bin/sleep 1 & wait' & wait`)
	cmd.ExtraFiles = []*os.File{reader}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader.Close()
	waited := false
	defer func() {
		writer.Close()
		if !waited {
			if err := cmd.Wait(); err != nil {
				t.Errorf("original gated child cleanup Wait: %v", err)
			}
		}
	}()
	root, err := Capture(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := CaptureCPUAnchor(root)
	if err != nil || cookie == "" {
		t.Fatal("original gated native root cookie unavailable", cookie, err)
	}
	if _, err := writer.Write([]byte("go\n")); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	owner := CPUOwner{Token: "native-gated-cargo-like-tree", Root: root, Cookie: cookie}
	var before CPUCut
	deadline := time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		before = CollectCPU(t.Context(), []CPUOwner{owner})
		if len(before.Members) >= 3 {
			break
		}
		before.Close()
		time.Sleep(10 * time.Millisecond)
	}
	defer before.Close()
	if len(before.Members) < 3 {
		t.Fatal("native root/child/grandchild ancestry unavailable", before.Method, before.Detail, before.Members)
	}
	time.Sleep(50 * time.Millisecond)
	after := CollectCPU(t.Context(), []CPUOwner{owner})
	defer after.Close()
	deltas := ContainedCPUDeltas(before, after)
	if len(deltas) != 1 || deltas[0].Owner != owner || deltas[0].MatchedProcesses < 3 {
		t.Fatal("native unique-parent descendant evidence did not survive both cuts", deltas)
	}
	for _, p := range after.Members {
		if p.Unit != CPUUnitDarwinMach || p.Cookie == "" {
			t.Fatal("missing native raw units or process cookie", p)
		}
	}
	if err := cmd.Wait(); err != nil {
		waited = true
		t.Fatal("original native tree Wait failed", err)
	}
	waited = true
	gone := CollectCPU(t.Context(), []CPUOwner{owner})
	defer gone.Close()
	if len(gone.Members) != 0 {
		t.Fatal("historical root PID retained CPU ownership", gone.Members)
	}
	t.Logf("native raw Mach counters for %d matched processes; root cookie=%s; original Wait0; no memory or busy workload", deltas[0].MatchedProcesses, cookie)
}
