package manager

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"getarm.org/internal/database"
	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
)

type sourceCrashReceipt struct{ Source, Parent string }

func TestKubernetesSourceCrashWriter(t *testing.T) {
	home := os.Getenv("ARM_KUBE_CORE_CRASH_HOME")
	if home == "" {
		return
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, model.Resources{CPU: 1, Memory: 512 << 20, Disk: 512 << 20})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 64, 64, 0, 64, 0)
	mode := os.Getenv("ARM_KUBE_CORE_CRASH_MODE")
	joined := make(chan struct{})
	if mode == "partial-active" {
		transit, e := m.acquireKubernetesSourceTransit(context.Background(), sourceCoreScope(kubesource.Ship), m.canonicalBudgetSelector(""), joined)
		if e != nil {
			t.Fatal(e)
		}
		sourceWriteCrashReceipt(t, home, sourceCrashReceipt{Source: transit.id})
	}
	if mode == "partial-terminal" {
		m.capacity.Disk = 1
	}
	owner, err := m.PrepareKubernetesSource(context.Background(), sourceCoreScope(kubesource.Ship), joined)
	if mode == "partial-terminal" {
		rows, e := m.list()
		if err == nil || e != nil || len(rows) != 1 || rows[0].State != "terminal" {
			t.Fatal(rows, err, e)
		}
		sourceWriteCrashReceipt(t, home, sourceCrashReceipt{Source: rows[0].ID})
	}
	if err != nil {
		t.Fatal(err)
	}
	payload, err := owner.Files().NewPayload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = payload.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if mode == "ending" {
		if l, e := m.End(owner.transit.id, "released"); e != nil || l.State != "ending" {
			t.Fatal(l, e)
		}
	}
	if mode == "needs_attention" || mode == "terminal" {
		if mode == "needs_attention" {
			if e := owner.transit.MarkCleanupUncertain("fixture original owner lost"); e != nil {
				t.Fatal(e)
			}
		}
		close(joined)
		result, e := owner.FinishAfterJoin()
		if mode == "terminal" && (e != nil || !result.SourceSettled) {
			t.Fatal(result, e)
		}
		if mode == "needs_attention" && (e == nil || result.SourceSettled) {
			t.Fatal(result, e)
		}
		sourceCoreDisk(t, m, owner.parent.ID)
	}
	sourceWriteCrashReceipt(t, home, sourceCrashReceipt{Source: owner.transit.id, Parent: owner.parent.ID})
}
func sourceWriteCrashReceipt(t *testing.T, home string, receipt sourceCrashReceipt) {
	t.Helper()
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(home+"-receipt.json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Deliberate abrupt owner loss: no caller join, payload Close or Manager Close.
	os.Exit(86)
}
func sourceCrashFixture(t *testing.T, home string) sourceCrashReceipt {
	return sourceCrashFixtureMode(t, home, "")
}
func sourceCrashFixtureMode(t *testing.T, home, mode string) sourceCrashReceipt {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestKubernetesSourceCrashWriter$", "-test.timeout=30s")
	cmd.Env = append(os.Environ(), "ARM_KUBE_CORE_CRASH_HOME="+home, "ARM_KUBE_CORE_CRASH_MODE="+mode)
	out, err := cmd.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 86 {
		t.Fatalf("source owner did not reach crash receipt: %v %s", err, out)
	}
	raw, err := os.ReadFile(home + "-receipt.json")
	if err != nil {
		t.Fatal(err)
	}
	var receipt sourceCrashReceipt
	if err = json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}
func TestKubernetesSourceStoppedProofLifecycleFixtures(t *testing.T) {
	root := os.Getenv("ARM_KUBE_SOURCE_FIXTURE_ROOT")
	if root == "" {
		root = t.TempDir()
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	var err error
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"active", "ending", "needs_attention", "terminal", "partial-active", "partial-terminal"} {
		t.Run(mode, func(t *testing.T) {
			home := filepath.Join(root, mode)
			if _, err := os.Lstat(home); !os.IsNotExist(err) {
				t.Fatal("fixture must be new", err)
			}
			receipt := sourceCrashFixtureMode(t, home, mode)
			s, err := database.Offline(t.Context(), home)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			proof, err := database.ProveDatabaseSettled(t.Context(), s, strconv.Itoa(os.Geteuid()))
			if err != nil || !proof.Complete || proof.Settled != (mode == "partial-terminal") {
				t.Fatal(mode, proof.Complete, proof.Settled, err)
			}
			if receipt.Parent != "" {
				found := false
				for _, b := range proof.Blockers {
					if b.Kind == "storage" && b.Entity == receipt.Parent {
						found = true
					}
				}
				if !found {
					t.Fatal("written parent missing from reset blockers")
				}
			}
		})
	}
}
func TestKubernetesSourceRestartCannotAdoptOrReleaseUnknownPair(t *testing.T) {
	home := filepath.Join(t.TempDir(), "source")
	receipt := sourceCrashFixture(t, home)
	m, err := Open(home, model.Resources{CPU: 1, Memory: 512 << 20, Disk: 512 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	source, err := m.Inspect(receipt.Source)
	if err != nil || source.State != "needs_attention" {
		t.Fatal("restart adopted source", source, err)
	}
	if _, err = m.End(receipt.Source, "released"); err == nil {
		t.Fatal("saved source row settled original ownership")
	}
	if _, err = m.Renew(receipt.Source, kubesource.SourceTTL); err == nil {
		t.Fatal("source renewed")
	}
	if _, err = m.Reconcile(receipt.Source); err == nil {
		t.Fatal("source reconciled from record")
	}
	if _, err = m.StorageHandoff(receipt.Parent); err == nil {
		t.Fatal("parent handed off despite unresolved consumer")
	}
	if _, err = m.End(receipt.Parent, "released"); err != nil {
		t.Fatal(err)
	}
	parent := sourceCoreDisk(t, m, receipt.Parent)
	if parent.State != "needs_attention" || !parent.Artifact.ContentObserved {
		t.Fatal(parent)
	}
	next := sourceCoreScope(kubesource.Ship)
	next.Attempt = strings.Repeat("d", 32)
	if owner, err := m.PrepareKubernetesSource(t.Context(), next, make(chan struct{})); err == nil || owner != nil {
		t.Fatal("new attempt hid old unknown source")
	}
	status, err := m.Status()
	if err != nil || status.Reserved.CPU != kubesource.SourceCPU || status.Reserved.Disk != next.DiskBytes() {
		t.Fatal("restart lost pair charge", status.Reserved, err)
	}
}
