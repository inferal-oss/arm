package sshservice

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewHistoryBatchRejectsChangedExpectedRow(t *testing.T) {
	for _, change := range []string{"same-revision-created", "completed-with-hot-member"} {
		t.Run(change, func(t *testing.T) {
			home, binding, _ := settledSourceHistoryFixture(t, sourceHistoryCleanupBatchMembers+1)
			request := sourceArchiveRequest(t, home, binding, "review-"+change)
			changed := false
			op, err := archiveSourceHistory(t.Context(), home, binding, request, func(stage string) error {
				if stage != "after_cleanup_checkpoint" || changed {
					return nil
				}
				changed = true
				db, e := sql.Open("sqlite", filepath.Join(home, SourceHistoryName, SourceHistoryCatalogName))
				if e != nil {
					return e
				}
				defer db.Close()
				var raw string
				if e = db.QueryRow("SELECT data FROM source_history_operations WHERE key=?", request.Key).Scan(&raw); e != nil {
					return e
				}
				var saved SourceHistoryOperation
				if e = json.Unmarshal([]byte(raw), &saved); e != nil {
					return e
				}
				if change == "same-revision-created" {
					saved.Created = saved.Created.Add(-time.Hour)
				} else {
					saved.Phase = "completed"
					saved.Cleaned = []string{}
					for _, m := range saved.Request.Plan.Members {
						saved.Cleaned = append(saved.Cleaned, m.File)
					}
				}
				encoded, e := json.Marshal(saved)
				if e != nil {
					return e
				}
				_, e = db.Exec("UPDATE source_history_operations SET phase=?,data=? WHERE key=?", saved.Phase, string(encoded), request.Key)
				return e
			})
			if !changed {
				t.Fatal("did not reach between-batch mutation", err)
			}
			last := request.Plan.Members[len(request.Plan.Members)-1]
			_, nativeErr := os.Lstat(filepath.Join(home, "mesh", last.File))
			if err == nil {
				t.Fatalf("changed expected row accepted: phase=%s remaining-hot-exists=%t", op.Phase, nativeErr == nil)
			}
			if !strings.Contains(err.Error(), "expected row changed") {
				t.Fatal("independent counterexample did not reach the expected-row guard", err)
			}
			if nativeErr != nil {
				t.Fatal("changed expected row consumed the final selected hot file", nativeErr)
			}
			if op.Phase != "committed" || len(op.Cleaned) != sourceHistoryCleanupBatchMembers {
				t.Fatal("uncertain continuation promoted an unacknowledged row", op)
			}
			if change == "completed-with-hot-member" {
				// A separately requested completed lookup remains read-only. It
				// reports stored history; it does not re-run cleanup or certify
				// a newly observed row as this in-flight worker's checkpoint.
				observed, lookupErr := ReconcileSourceHistory(t.Context(), home, binding, request.Key)
				if lookupErr != nil || observed.Phase != "completed" {
					t.Fatal("completed lookup semantics changed", observed, lookupErr)
				}
				if _, lookupErr = os.Lstat(filepath.Join(home, "mesh", last.File)); lookupErr != nil {
					t.Fatal("completed lookup replayed cleanup", lookupErr)
				}
			}
		})
	}
}
