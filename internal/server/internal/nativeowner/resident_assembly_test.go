package nativeowner_test

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
	"getarm.org/internal/database"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"getarm.org/internal/server/internal/nativeowner"
)

// Only this test executable recognizes these fixture selectors. The parent
// retains the actual child Cmd through Wait. No issuer/claim is serialized or
// forged: the child constructs the original native issuer and actual borrowers.
var residentFixtureChild = flag.String("resident-fixture-child", "", "private resident fixture child")
var residentFixtureHome = flag.String("resident-fixture-home", "", "parent-owned fixture directory")

func runOriginalFixtureChild(t *testing.T, kind string, body func(string)) {
	t.Helper()
	if *residentFixtureChild != "" {
		if *residentFixtureChild != kind || *residentFixtureHome == "" {
			t.Fatal("wrong original fixture selector")
		}
		body(*residentFixtureHome)
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
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v", "-resident-fixture-child="+kind, "-resident-fixture-home="+home)
	output, err := command.CombinedOutput() // retains and waits for this exact child
	if err != nil || command.ProcessState == nil || !command.ProcessState.Exited() || command.ProcessState.ExitCode() != 0 || !strings.Contains(string(output), "original "+kind+" fixture initialized\n") {
		t.Fatalf("original fixture child failed: %v\n%s", err, output)
	}
	// Parent cleanup runs only after its original child joined. A whole fixture
	// process ends with retained objects; no low-level Retire/Close or synthetic
	// native/pool/task/borrower Closed/resource-credit witness is manufactured.
}

func openFixtureIssuer(t *testing.T, home string) *nativeowner.ServeIssuer {
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

func TestResidentActualOriginalDatabaseClaimAndPoolBinding(t *testing.T) {
	runOriginalFixtureChild(t, "database", func(home string) {
		issuer := openFixtureIssuer(t, home)
		view, err := issuer.ManagerView()
		if err != nil {
			t.Fatal(err)
		}
		claim, err := view.ClaimOriginal()
		if err != nil {
			t.Fatal(err)
		}
		pending, err := database.NewResidentOpen(claim)
		if err != nil {
			t.Fatal(err)
		}
		if pending.InspectOriginal().PoolRetained {
			t.Fatal("pure pending construction created a pool")
		}
		binding, err := pending.OpenOriginal(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		pool, err := pending.DatabaseForOriginalBinding(binding)
		if err != nil {
			t.Fatal(err)
		}
		same, err := pending.DatabaseForOriginalBinding(binding)
		if err != nil || same != pool {
			t.Fatal("actual original binding changed pool", err)
		}
		if _, err = pending.DatabaseForOriginalBinding(nil); err == nil {
			t.Fatal("nil binding returned original pool")
		}
		var selected string
		if err = pool.QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&selected); err != nil || selected != filepath.Join(home, "state.db", "ledger.sqlite") {
			t.Fatal("original pool selected another file", selected, err)
		}
		seen := pending.InspectOriginal()
		if !seen.PoolRetained || !seen.BindingRetained || seen.OriginalOperationPending || !seen.Connector.ConnectAttempted || seen.Connector.ConnectPending || seen.Connector.ConnectAbnormal || seen.FirstError != nil || seen.Close.NativeOutcome != "unknown" {
			t.Fatal("actual original pool/binding not initialized", seen)
		}
		if _, err = pending.OpenOriginal(t.Context()); err == nil {
			t.Fatal("original database was reopened")
		}
		if err = pool.PingContext(t.Context()); err != nil {
			t.Fatal("refused duplicate open disturbed original pool", err)
		}
		println("original database fixture initialized")
	})
}

func TestResidentActualOriginalManagerInitializesSameOwnedDatabase(t *testing.T) {
	runOriginalFixtureChild(t, "manager", func(home string) {
		issuer := openFixtureIssuer(t, home)
		view, err := issuer.ManagerView()
		if err != nil {
			t.Fatal(err)
		}
		// Configuration is a fixture ceiling, never a standing service grant.
		settings := daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0"}
		pending, err := manager.NewResidentManagerPending(view, settings)
		if err != nil {
			t.Fatal(err)
		}
		before := pending.InspectOriginal()
		if !before.ManagerRetained || before.ClaimRetained || before.DatabaseRetained || before.OpenAttempted {
			t.Fatal("pending Manager performed effects", before)
		}
		if err = pending.OpenOriginal(t.Context()); err != nil {
			t.Fatal(err)
		}
		seen := pending.InspectOriginal()
		if !seen.Initialized || !seen.ClaimRetained || !seen.DatabaseRetained || !seen.BindingRetained || !seen.SchemaTransactionRetained || !seen.SchemaCommitAttempted || seen.SchemaCommitError != nil || seen.FirstError != nil || seen.OperationPending || seen.LoopsLaunched || seen.NativeOutcome != "unknown" {
			t.Fatal("actual original Manager initialization incomplete", seen)
		}
		if _, err = view.ClaimOriginal(); err == nil {
			t.Fatal("Manager did not consume its sole actual claim")
		}
		if _, err = pending.ManagerForOriginalServer(nil); err == nil {
			t.Fatal("unbound Server obtained original Manager")
		}
		serverView, err := issuer.ServerView()
		if err != nil {
			t.Fatal(err)
		}
		serverClaim, err := serverView.ClaimOriginal()
		if err != nil {
			t.Fatal(err)
		}
		serverBinding, err := pending.ClaimOriginalServer(serverClaim)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pending.ManagerForOriginalServer(serverBinding); err == nil {
			t.Fatal("unstarted Manager was lent to Server")
		}
		if !pending.InspectOriginal().ServerBorrowerValidated {
			t.Fatal("actual same-issuer Server borrower not retained")
		}
		println("original manager fixture initialized")
	})
}
