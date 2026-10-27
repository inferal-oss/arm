package observe

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"getarm.org/internal/usage"
)

func TestNativeDarwinContainedCPUDescendantCredit(t *testing.T) {
	if os.Getenv("ARM_TEST_NATIVE_CPU_CREDIT") != "1" {
		t.Skip("enable bounded native contained CPU credit observation")
	}
	if _, _, err := usage.CPUClockQuantum(); err != nil {
		t.Skipf("native managed CPU credit scope unavailable: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	ready := filepath.Join(t.TempDir(), "original-grandchild-ready")
	cmd := exec.Command(exe, "-test.run=^TestNativeCPUDescendantCreditHelper$", "-test.timeout=8s")
	cmd.Env = append(os.Environ(), "ARM_NATIVE_CREDIT_HELPER=root", "ARM_NATIVE_CREDIT_READY="+ready, "GOMAXPROCS=1")
	cmd.ExtraFiles = []*os.File{reader}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader.Close()
	waited := false
	defer func() {
		writer.Close()
		if !waited {
			if err := cmd.Wait(); err != nil {
				t.Errorf("original bounded CPU helper Wait: %v\n%s", err, output.String())
			}
		}
	}()
	root, err := usage.Capture(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := usage.CaptureCPUAnchor(root)
	if err != nil || cookie == "" {
		t.Fatal("original gated CPU root anchor unavailable", cookie, err)
	}
	if _, err := writer.Write([]byte("go\n")); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatal("original owned grandchild never became ready", err)
	}
	owner := usage.CPUOwner{Token: "native-contained-descendant-credit", Root: root, Cookie: cookie}
	sampler := NewCPUSampler()
	defer sampler.Close()
	collect := func(ctx context.Context) usage.CPUCut { return usage.CollectCPU(ctx, []usage.CPUOwner{owner}) }
	first, _ := sampler.SampleManaged(t.Context(), collect)
	if first.Known {
		t.Fatal("first CPU snapshot invented a counter interval")
	}
	time.Sleep(1200 * time.Millisecond)
	reading, frame := sampler.SampleManaged(t.Context(), collect)
	if !reading.Known || !frame.Eligible || len(frame.Credits) != 1 {
		t.Fatal("native contained managed CPU candidate unavailable", reading, frame)
	}
	credit := frame.Credits[0]
	if credit.Token != owner.Token || credit.Root != owner.Root || credit.Cookie != owner.Cookie || credit.MatchedProcesses < 3 || credit.Cores <= 0 || credit.Cores > 1.1 {
		t.Fatal("native descendant usage misattributed or misnormalized", credit, frame.Evidence)
	}
	facts := frame.Evidence
	if facts.Quantum == 0 || facts.CorrectionTicks != 2*uint64(*reading.EligibleCPUs) || facts.MaxTotalDelta == 0 || facts.PreviousPostStarted.Before(facts.PreviousHostFinished) || facts.CurrentPreFinished.After(facts.HostStarted) {
		t.Fatal("native contained interval/quantization evidence missing", facts)
	}
	if len(facts.Terms) != 1 || facts.Terms[0].RawDelta <= facts.Quantum*facts.CorrectionTicks || facts.Terms[0].MatchedProcesses < 3 {
		t.Fatal("descendant raw CPU increment did not exceed conservative host margin", facts.Terms)
	}
	if err := cmd.Wait(); err != nil {
		waited = true
		t.Fatalf("original native helper Wait failed: %v\n%s", err, output.String())
	}
	waited = true
	t.Logf("actual contained CPU candidate %.6f cores from %d matched root/child/grandchild processes, quantum=%d, correction=%d, maxTotalDelta=%d; original Wait0", credit.Cores, credit.MatchedProcesses, facts.Quantum, facts.CorrectionTicks, facts.MaxTotalDelta)
}

// This fixed helper is executable only in the opt-in native test. Exactly one
// grandchild burns for four seconds; its original parent and root both Wait.
func TestNativeCPUDescendantCreditHelper(t *testing.T) {
	mode := os.Getenv("ARM_NATIVE_CREDIT_HELPER")
	if mode == "" {
		t.Skip("native owned helper only")
	}
	if mode == "burn" {
		if err := os.WriteFile(os.Getenv("ARM_NATIVE_CREDIT_READY"), []byte("ready\n"), 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(4 * time.Second)
		var n uint64
		for time.Now().Before(deadline) {
			for range 10000 {
				n = n*1664525 + 1013904223
			}
		}
		runtime.KeepAlive(n)
		return
	}
	if mode == "root" {
		gate := os.NewFile(3, "original-launch-gate")
		if gate == nil {
			t.Fatal("missing original root gate")
		}
		line, err := bufio.NewReader(gate).ReadString('\n')
		gate.Close()
		if err != nil || line != "go\n" {
			t.Fatal("original root gate was not authorized", err)
		}
	} else if mode != "parent" {
		t.Fatal("unknown fixed helper mode")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^TestNativeCPUDescendantCreditHelper$", "-test.timeout=7s")
	next := "burn"
	if mode == "root" {
		next = "parent"
	}
	child.Env = append(os.Environ(), "ARM_NATIVE_CREDIT_HELPER="+next, "GOMAXPROCS=1")
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal("original descendant Wait", err)
	}
}
