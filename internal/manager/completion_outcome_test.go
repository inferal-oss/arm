package manager

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func TestSupervisedNaturalCompletionRecordsExitOutcome(t *testing.T) {
	for _, code := range []int{0, 7} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			m := newManager(t)
			r := request("natural-completion")
			run := RunRequest{Request: r, Command: []string{"/bin/sh", "-c", "exit " + strconv.Itoa(code)}, CWD: t.TempDir()}
			lease, err := m.Acquire(r, &run)
			if err != nil {
				t.Fatal(err)
			}
			eventually(t, func() bool { lease, err = m.Inspect(lease.ID); return err == nil && lease.State == "terminal" })
			want := "completed"
			if code != 0 {
				want = "failed"
			}
			if lease.Outcome != want || lease.ExitCode == nil || *lease.ExitCode != code {
				t.Fatalf("natural exit lost result: %+v", lease)
			}
			status, err := m.Status()
			if err != nil || status.Reserved.CPU != 0 {
				t.Fatalf("finished process remains charged: %+v %v", status, err)
			}
			retry, err := m.Acquire(r, &run)
			if err != nil || retry.ID != lease.ID || retry.Outcome != want {
				t.Fatalf("exact retry lost completion: %+v %v", retry, err)
			}
		})
	}
}

func TestSupervisedExplicitOutcomeSurvivesGracefulZeroExit(t *testing.T) {
	for _, outcome := range []string{"released", "cancelled", "expired", "revoked"} {
		t.Run(outcome, func(t *testing.T) {
			m := newManager(t)
			cwd := t.TempDir()
			ready := filepath.Join(cwd, "ready")
			r := request("explicit-" + outcome)
			run := RunRequest{Request: r, Command: []string{"/bin/sh", "-c", "trap 'exit 0' TERM; printf ready > \"$1\"; while :; do sleep 0.05; done", "sh", ready}, CWD: cwd}
			lease, err := m.Acquire(r, &run)
			if err != nil {
				t.Fatal(err)
			}
			eventually(t, func() bool { _, err := os.Stat(ready); return err == nil })
			if _, err = m.End(lease.ID, outcome); err != nil {
				t.Fatal(err)
			}
			eventually(t, func() bool { lease, err = m.Inspect(lease.ID); return err == nil && lease.State == "terminal" })
			if lease.Outcome != outcome || lease.ExitCode == nil || *lease.ExitCode != 0 {
				t.Fatalf("graceful exit overwrote explicit outcome: %+v", lease)
			}
			if lease.Ended == nil || time.Since(*lease.Ended) > 5*time.Second {
				t.Fatal("missing settled completion timestamp")
			}
		})
	}
}

func TestCompletionOutcomeRecoveryPreservesHistoricalRecords(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	m, err := Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	r := request("durable-completion")
	run := RunRequest{Request: r, Command: []string{"/bin/sh", "-c", "exit 0"}, CWD: t.TempDir()}
	done, err := m.Acquire(r, &run)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	eventually(t, func() bool { done, err = m.Inspect(done.ID); return err == nil && done.State == "terminal" })
	legacy, err := m.Acquire(request("historical-released"), nil)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	legacy, err = m.End(legacy.ID, "released")
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	// A valid pre-feature record may say released even for a failed process.
	// Recovery must preserve that historical meaning rather than infer a rewrite.
	code := 7
	legacy.ExitCode = &code
	if err = m.put(legacy, "fixture.historical_record"); err != nil {
		m.Close()
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	restored, err := m.Inspect(done.ID)
	if err != nil || restored.Outcome != "completed" {
		t.Fatalf("new completion not durable: %+v %v", restored, err)
	}
	restored, err = m.Inspect(legacy.ID)
	if err != nil || restored.Outcome != "released" || restored.ExitCode == nil || *restored.ExitCode != 7 {
		t.Fatalf("historical outcome rewritten: %+v %v", restored, err)
	}
}
