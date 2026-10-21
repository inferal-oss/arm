package usage

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
)

func TestCaptureRejectsOutOfRangePIDAndCancelledCollection(t *testing.T) {
	for _, pid := range []int{-1, 0, math.MaxInt32 + 1} {
		if _, err := Capture(pid); err == nil {
			t.Fatalf("accepted invalid PID %d", pid)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	snapshot := Collect(ctx, model.ProcessIdentity{PID: os.Getpid(), PGID: os.Getpid(), UID: "test", Start: "test", Namespace: "test"})
	if snapshot.Verified || snapshot.Complete || !strings.Contains(snapshot.Detail, "cancelled") || len(snapshot.Members) != 0 {
		t.Fatalf("cancelled collection returned evidence: %+v", snapshot)
	}
}

func TestNativeCapturedGroupCountersAndIdentityChanges(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^TestUsageWorkloadChild$")
	child.Env = append(os.Environ(), "ARM_USAGE_FIXTURE=1")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	identity, err := Capture(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if identity.PID != child.Process.Pid || identity.PGID != identity.PID || identity.UID != strconv.Itoa(os.Geteuid()) || identity.Start == "" || identity.Namespace == "" {
		t.Fatalf("identity evidence: %+v", identity)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	before := Collect(ctx, identity)
	if !before.Verified || len(before.Members) == 0 {
		t.Fatalf("native snapshot unavailable: %+v", before)
	}
	time.Sleep(350 * time.Millisecond)
	after := Collect(ctx, identity)
	v := View(after, &before)
	if !v.IdentityVerified || v.CPUCores == nil || *v.CPUCores <= 0 || *v.CPUCores > 2 || v.AccountedMemoryBytes == nil || *v.AccountedMemoryBytes < 8<<20 {
		b, _ := json.Marshal(v)
		t.Fatalf("native counters/units: %s", b)
	}
	changed := identity
	changed.Start += "other"
	invalid := Collect(ctx, changed)
	if invalid.Verified || View(invalid, &after).AccountedMemoryBytes != nil {
		t.Fatal("reused PID accepted another start identity")
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	child.Wait()
	gone := Collect(ctx, identity)
	if gone.Verified || len(gone.Members) > 0 {
		t.Fatal("dead leader provided live attribution")
	}
}
func TestUsageWorkloadChild(t *testing.T) {
	if os.Getenv("ARM_USAGE_FIXTURE") != "1" {
		t.Skip("subprocess fixture")
	}
	memory := make([]byte, 24<<20)
	for i := 0; i < len(memory); i += 4096 {
		memory[i] = 1
	}
	deadline := time.Now().Add(20 * time.Second)
	var n uint64
	for time.Now().Before(deadline) {
		for range 100000 {
			n = n*1664525 + 1013904223
		}
		if n == 0 {
			memory[0]++
		}
	}
	t.Log(len(memory), n)
}

func TestViewCounterMembershipAndUncertainty(t *testing.T) {
	root := model.ProcessIdentity{PID: 1, PGID: 1, UID: "test", Start: "one", Namespace: "test"}
	second := root
	second.PID = 2
	second.Start = "two"
	at := time.Now()
	cpuBefore, cpuAfter := 2.0, 3.0
	memory := uint64(100)
	before := Snapshot{Started: at, Finished: at.Add(time.Millisecond), Root: root, Verified: true, Members: []model.ProcessUsage{{Identity: root, CPUSeconds: &cpuBefore}}}
	after := Snapshot{Started: at.Add(time.Second), Finished: at.Add(2 * time.Second), Root: root, Verified: true, Members: []model.ProcessUsage{{Identity: root, CPUSeconds: &cpuAfter, AccountedMemoryBytes: &memory}, {Identity: second, CPUSeconds: &cpuAfter, AccountedMemoryBytes: &memory}}}
	got := View(after, &before)
	if got.CPUCores == nil || *got.CPUCores != .5 || got.AccountedMemoryBytes == nil || *got.AccountedMemoryBytes != 200 {
		t.Fatalf("new member's entire lifetime CPU was charged: %+v", got)
	}
	after.Members[0].AccountedMemoryBytes = nil
	if View(after, &before).AccountedMemoryBytes != nil {
		t.Fatal("missing memory was counted as zero")
	}
	after.Verified = false
	if View(after, &before).CPUCores != nil {
		t.Fatal("unverified root provided CPU attribution")
	}
}
