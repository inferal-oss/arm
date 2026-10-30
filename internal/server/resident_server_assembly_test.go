package server

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"getarm.org/internal/server/internal/nativeowner"
)

// Only this test executable recognizes these fixture selectors. The parent
// retains the actual child Cmd through Wait. No issuer/claim is serialized or
// forged: the child constructs the original native issuer and actual borrowers.
var residentServerFixtureChild = flag.String("resident-server-fixture-child", "", "private resident fixture child")
var residentServerFixtureHome = flag.String("resident-server-fixture-home", "", "parent-owned fixture directory")

func runOriginalServerFixtureChild(t *testing.T, kind string, body func(string)) {
	t.Helper()
	if *residentServerFixtureChild != "" {
		if *residentServerFixtureChild != kind || *residentServerFixtureHome == "" {
			t.Fatal("wrong original fixture selector")
		}
		body(*residentServerFixtureHome)
		return
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(home, "state.db"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"daemon.lock", "state.db/ledger.sqlite"} {
		f, err := os.OpenFile(filepath.Join(home, name), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v", "-resident-server-fixture-child="+kind, "-resident-server-fixture-home="+home)
	output, err := command.CombinedOutput() // retains and waits for this exact child
	if err != nil || command.ProcessState == nil || !command.ProcessState.Exited() || command.ProcessState.ExitCode() != 0 || !strings.Contains(string(output), "original "+kind+" fixture initialized\n") {
		t.Fatalf("original fixture child failed: %v\n%s", err, output)
	}
	// Parent cleanup runs only after its original child joined. A whole fixture
	// process ends with retained objects; no low-level Retire/Close or synthetic
	// native/pool/task/borrower Closed/resource-credit witness is manufactured.
}

func openOriginalServerFixtureIssuer(t *testing.T, home string) *nativeowner.ServeIssuer {
	t.Helper()
	issuer, err := nativeowner.NewServePending(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = issuer.OpenOriginal(); err != nil {
		t.Fatal(err)
	}
	return issuer
}

func TestOriginalResidentServerBindsActualManagerAndNativeIngress(t *testing.T) {
	runOriginalServerFixtureChild(t, "server", func(home string) {
		issuer := openOriginalServerFixtureIssuer(t, home)
		mv, err := issuer.ManagerView()
		if err != nil {
			t.Fatal(err)
		}
		originalManager, err := manager.NewResidentManagerPending(mv, daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0"})
		if err != nil {
			t.Fatal(err)
		}
		if err = originalManager.OpenOriginal(t.Context()); err != nil {
			t.Fatal(err)
		}
		sv, err := issuer.ServerView()
		if err != nil {
			t.Fatal(err)
		}
		pending, err := newResidentServerPending(sv, originalManager)
		if err != nil {
			t.Fatal(err)
		}
		actual := pending.server
		if err = pending.openOriginal(t.Context()); err != nil {
			t.Fatal(err)
		}
		before := pending.inspectOriginal()
		if !before.Opened || !before.ClaimRetained || !before.ManagerBorrowerRetained || !before.BindingRetained || before.Started || actual.Manager != nil {
			t.Fatal(before)
		}
		proof := &ingressProof{generation: actual.generation, original: pending.binding}
		if err := actual.validateOriginalIngressProof(t.Context(), proof); err == nil {
			t.Fatal("unstarted original Manager accepted ingress")
		}
		if err = pending.startOriginalManager(); err != nil {
			t.Fatal(err)
		}
		after := pending.inspectOriginal()
		if pending.server != actual || !after.Started || after.OperationPending || after.FirstError != nil || after.NativeOutcome != "unknown" || actual.Manager == nil || actual.nodeStopDirectory != nil {
			t.Fatal(after)
		}
		if err := actual.validateOriginalIngressProof(t.Context(), proof); err != nil {
			t.Fatal("actual original Server ingress refused", err)
		}
		forged := &residentServerBinding{owner: pending, server: actual, claim: pending.claim}
		if err := actual.validateOriginalIngressProof(t.Context(), &ingressProof{generation: actual.generation, original: forged}); err == nil {
			t.Fatal("copied Server binding accepted")
		}
		if err := actual.validateOriginalIngressProof(t.Context(), &ingressProof{generation: actual.generation}); err == nil {
			t.Fatal("resident Server accepted legacy proof")
		}
		if err := actual.Manager.Close(); err == nil {
			t.Fatal("legacy close accepted original Manager")
		}
		if err := actual.validateOriginalIngressProof(t.Context(), proof); err != nil {
			t.Fatal("refused legacy close disturbed original ingress", err)
		}
		println("original server fixture initialized")
	})
}
