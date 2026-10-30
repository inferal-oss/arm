package database

import (
	"context"
	"errors"
	"getarm.org/internal/server/lifetimeview"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
)

// These negatives exercise the real owner entry point. Package init can have
// unrelated native state; zero owner records are not zero-native-process proof.
func TestResidentDatabaseOriginalClaimBeforePhysicalEffects(t *testing.T) {
	if _, err := NewResidentOpen(nil); !errors.Is(err, errResidentDatabaseOriginal) {
		t.Fatal("nil claim accepted", err)
	}
	owner, err := NewResidentOpen(&lifetimeview.ManagerClaim{})
	if err != nil {
		t.Fatal(err)
	}
	before := owner.InspectOriginal()
	if before.ConnectorRetained || before.PoolRetained || before.FilesOpened != 0 {
		t.Fatal("pure owner construction performed physical effects", before)
	}
	binding, first := owner.OpenOriginal(context.Background())
	if first == nil || binding != nil {
		t.Fatal("zero original claim opened a database", binding, first)
	}
	after := owner.InspectOriginal()
	if after.ConnectorRetained || after.PoolRetained || after.FilesOpened != 0 || after.FirstError != first {
		t.Fatal("refusal lost originals or reached physical effects", after)
	}
	if _, err := owner.OpenOriginal(context.Background()); err != first {
		t.Fatal("failed original open replaced its error", err, first)
	}
}
func TestResidentDatabaseForeignBindingsAndStopsRefuse(t *testing.T) {
	owner, err := NewResidentOpen(&lifetimeview.ManagerClaim{})
	if err != nil {
		t.Fatal(err)
	}
	supplied := &ResidentDatabaseBinding{owner: owner, generation: "supplied"}
	if _, err := owner.DatabaseForOriginalBinding(supplied); !errors.Is(err, errResidentDatabaseOriginal) {
		t.Fatal("supplied binding created borrow authority", err)
	}
	observation, err := owner.CloseOriginalSQLAPI(context.Background(), &lifetimeview.ManagerStop{})
	if err == nil || observation.NativeOutcome != "unknown" || observation.PoolCloseReturned || observation.PoolCloseAttempted {
		t.Fatal("foreign stop supplied cleanup/completion", observation, err)
	}
	after := owner.InspectOriginal()
	if after.ConnectorRetained || after.PoolRetained || after.FilesOpened != 0 || after.OriginalOperationPending {
		t.Fatal("foreign stop reached actual physical effects", after)
	}
}

// This owned negative tests only the private file primitive, not a fabricated
// Manager claim or positive SQL binding. Every actual fixture FD is registered
// before its effects and closed through the original file records.
func TestResidentPreparedFIFORefusesBeforeReadWriteOpen(t *testing.T) {
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err = unix.Mkfifo(filepath.Join(path, "ledger.sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	owner, err := NewResidentOpen(&lifetimeview.ManagerClaim{})
	if err != nil {
		t.Fatal(err)
	}
	op, err := owner.beginOriginal("open")
	if err != nil {
		t.Fatal(err)
	}
	normal := false
	var outcome error
	defer owner.finishOriginal(op, &normal, &outcome)
	func() {
		defer func() {
			for _, f := range owner.files.slots {
				if f != nil {
					if err := owner.files.closeAuxiliary(f); err != nil {
						t.Error(err)
					}
				}
			}
		}()
		owner.files.path = path
		owner.files.uid = uint32(os.Geteuid())
		home, err := owner.files.openHome()
		if err != nil {
			t.Fatal(err)
		}
		before := owner.files.opened
		file, err := owner.files.open(home, "ledger.sqlite", false)
		if err == nil || file != nil || owner.files.opened != before {
			t.Fatal("malformed prepared FIFO reached read-write open", file, err)
		}
		if owner.InspectOriginal().ConnectorRetained || owner.InspectOriginal().PoolRetained {
			t.Fatal("file-only malformed input reached SQLite")
		}
	}()
	normal = true
}
