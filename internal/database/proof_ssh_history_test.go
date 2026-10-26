package database_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/inferal-oss/arm/internal/database"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
	"golang.org/x/sys/unix"
)

func archivedSSHProof(t *testing.T) (string, sshservice.Binding, sshservice.SourceHistoryOperation) {
	t.Helper()
	// This is a metadata/retirement fixture with an already counted pool
	// claim; it launches no local CPU workload. Ambient CPU pressure must not
	// prevent reaching the history proof under test. Ordinary proof fixtures
	// retain their original CPU claim; remote receipt CPU evidence is unchanged.
	home, b, pin := sshProofStateInHome(t, proofFixtureResources(t, false, model.Resources{}))
	intent := sshProofIntent(t, home, b, pin)
	if e := os.Remove(intent.Directory.Path); e != nil {
		t.Fatal(e)
	}
	if e := sshservice.SettleSource(t.Context(), home, b, intent, proofSSHReceipt(intent)); e != nil {
		t.Fatal(e)
	}
	plan, e := sshservice.PlanSourceHistory(t.Context(), home, b, sshservice.SourceHistorySelection{Limit: 64})
	if e != nil {
		t.Fatal(e)
	}
	op, e := sshservice.ArchiveSourceHistory(t.Context(), home, b, sshservice.SourceHistoryRequest{Schema: 1, Key: "proof-archive", Reason: "Preserve verified settled source evidence", Plan: plan})
	if e != nil {
		t.Fatal(e)
	}
	return home, b, op
}
func TestSSHSourceHistoryIndependentSettledResetRetainsWholeCatalog(t *testing.T) {
	home, b, op := archivedSSHProof(t)
	before := snapshotTree(t, filepath.Join(home, "ssh-source-history"))
	r, e := database.ReviewReset(t.Context(), home, "Retire only private settled source fixture", true)
	if e != nil {
		t.Fatal(e)
	}
	if !r.Plan.Proof.Settled {
		t.Fatal(r.Plan.Proof.Blockers)
	}
	found := false
	for _, n := range r.Plan.Namespaces {
		if n.Name == "ssh-source-history" {
			if n.SSHHistory == nil || len(n.SSHHistory.Packs) != 1 || len(n.Files) != 2 {
				t.Fatal("no complete typed evidence", n)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("source history retirement omitted")
	}
	if !reflect.DeepEqual(before, snapshotTree(t, filepath.Join(home, "ssh-source-history"))) {
		t.Fatal("review changed source catalog")
	}
	result, e := r.Apply(t.Context())
	r.Close()
	if e != nil || !result.Completed {
		t.Fatal(result, e)
	}
	archived := filepath.Join(home, database.ResetDirectoryName, "reset-"+result.Operation, "ssh-source-history")
	if !reflect.DeepEqual(before, snapshotTree(t, archived)) {
		t.Fatal("source history was deleted/changed during retirement")
	}
	if _, e = os.Lstat(filepath.Join(home, "ssh-source-history")); !os.IsNotExist(e) {
		t.Fatal("old source namespace remained at fresh authority", e)
	}
	// Read-only old catalog remains byte evidence, not reusable new-authority keys.
	if _, e = os.Stat(filepath.Join(archived, "packs", "pack-"+op.PackID, "records.bin")); e != nil {
		t.Fatal(e)
	}
	if _, e = sshservice.LookupSourceHistory(context.Background(), home, b, op.Request.Plan.Members[0].Target, op.Request.Plan.Members[0].Key); e == nil {
		t.Fatal("retired authority silently adopted")
	}
}
func TestSSHSourceHistoryIndependentProofRefusesMissingForgedOrBusy(t *testing.T) {
	for _, change := range []string{"missing-catalog", "missing-pack", "missing-ref", "op-alias", "op-null", "op-missing-member", "op-unknown", "op-committed-partial", "op-committed-all", "op-completed-partial", "sidecar", "worker-busy"} {
		t.Run(change, func(t *testing.T) {
			home, _, op := archivedSSHProof(t)
			root := filepath.Join(home, "ssh-source-history")
			var lock *os.File
			switch change {
			case "missing-catalog":
				if e := os.Remove(filepath.Join(root, "catalog.db")); e != nil {
					t.Fatal(e)
				}
			case "missing-pack":
				if e := os.Rename(filepath.Join(root, "packs", "pack-"+op.PackID), filepath.Join(root, "packs", "lost-pack")); e != nil {
					t.Fatal(e)
				}
			case "sidecar":
				if e := os.WriteFile(filepath.Join(root, "catalog.db-journal"), []byte("unreviewed"), 0600); e != nil {
					t.Fatal(e)
				}
			case "worker-busy":
				var e error
				lock, e = os.Open(filepath.Join(root, ".worker.lock"))
				if e != nil {
					t.Fatal(e)
				}
				defer lock.Close()
				if e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
					t.Fatal(e)
				}
				defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
			default:
				db, e := sql.Open("sqlite", filepath.Join(root, "catalog.db"))
				if e != nil {
					t.Fatal(e)
				}
				query := map[string]string{
					"missing-ref":          `DELETE FROM source_history_refs`,
					"op-alias":             `UPDATE source_history_operations SET data=replace(data,'"hot_before":1','"Hot_before":1')`,
					"op-null":              `UPDATE source_history_operations SET data=json_set(data,'$.request.plan.members[0].bytes',NULL)`,
					"op-missing-member":    `UPDATE source_history_operations SET data=json_remove(data,'$.request.plan.members[0].identity')`,
					"op-unknown":           `UPDATE source_history_operations SET data=json_set(data,'$.request.plan.members[0].new_cleanup_authority',true)`,
					"op-committed-partial": `UPDATE source_history_operations SET phase='committed',data=json_set(data,'$.phase','committed','$.cleaned',json('[]'))`,
					"op-committed-all":     `UPDATE source_history_operations SET phase='committed',data=json_set(data,'$.phase','committed')`,
					"op-completed-partial": `UPDATE source_history_operations SET data=json_set(data,'$.cleaned',json('[]'))`,
				}[change]
				if _, e = db.Exec(query); e != nil {
					db.Close()
					t.Fatal(e)
				}
				db.Close()
			}
			before := snapshotTree(t, root)
			r, e := database.ReviewReset(t.Context(), home, "Refuse damaged private history", true)
			if r != nil {
				r.Close()
			}
			if e == nil {
				t.Fatal("unproven source history accepted", change)
			}
			if !reflect.DeepEqual(before, snapshotTree(t, root)) {
				t.Fatal("proof changed damaged source evidence")
			}
		})
	}
}
