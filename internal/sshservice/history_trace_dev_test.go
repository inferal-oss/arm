//go:build arm_devtrace

package sshservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestHistoryCleanupActualTrace(t *testing.T) {
	for _, scenario := range []string{"progress64", "lost-before", "lost-after", "lost-unlink", "same-revision", "false-completed", "reappear", "cancel", "worker", "cas-replacement", "cold-ref"} {
		t.Run(scenario, func(t *testing.T) {
			count := 5
			if scenario == "progress64" {
				count = 64
			}
			home, b, _ := settledSourceHistoryFixture(t, count)
			q := sourceArchiveRequest(t, home, b, "formal-cleanup")
			original, err := os.ReadFile(filepath.Join(home, "mesh", q.Plan.Members[0].File))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			known := map[string]bool{}
			var last SourceHistoryOperation
			historyCleanupObserver = func(v historyCleanupObservation) {
				last = v.Expected
				rows := map[string]string{}
				for _, op := range []SourceHistoryOperation{v.Expected, v.Current} {
					raw := mustHistoryJSON(op)
					h := stateHash([]byte(raw))
					if !known[h] {
						rows[h] = raw
						known[h] = true
					}
				}
				raw, e := json.Marshal(struct {
					Observation historyCleanupObservation `json:"observation"`
					Rows        map[string]string         `json:"rows"`
				}{v, rows})
				if e != nil {
					t.Fatal(e)
				}
				t.Log("HISTORY_ACTUAL " + string(raw))
			}
			defer func() { historyCleanupObserver = nil }()
			changed := false
			lost := errors.New("retained development acknowledgement loss")
			fault := func(stage string) error {
				if changed {
					return nil
				}
				match := stage == "after_cleanup_checkpoint"
				if scenario == "lost-before" || scenario == "cas-replacement" {
					match = stage == "before_cleanup_checkpoint"
				}
				if scenario == "lost-unlink" {
					match = stage == "after_unlink"
				}
				if !match || scenario == "progress64" {
					return nil
				}
				changed = true
				switch scenario {
				case "lost-before", "lost-after", "lost-unlink":
					return lost
				case "cancel":
					cancel()
					historyCleanupObserve("cancel", ctx, home, last, nil, len(last.Cleaned))
				case "cold-ref":
					db, e := sql.Open("sqlite", filepath.Join(home, SourceHistoryName, SourceHistoryCatalogName))
					if e != nil {
						return e
					}
					defer db.Close()
					if _, e = db.Exec(`DELETE FROM source_history_refs WHERE key=?`, q.Plan.Members[count-1].Key); e != nil {
						return e
					}
					historyCleanupObserve("cold", ctx, home, last, nil, len(last.Cleaned))
				case "worker":
					name := filepath.Join(home, SourceHistoryName, sourceHistoryWorkerLockName)
					if e := os.Rename(name, name+".retained"); e != nil {
						return e
					}
					if e := os.WriteFile(name, nil, 0600); e != nil {
						return e
					}
					historyCleanupObserve("worker", ctx, home, last, nil, len(last.Cleaned))
				case "reappear":
					if e := os.WriteFile(filepath.Join(home, "mesh", q.Plan.Members[0].File), original, 0600); e != nil {
						return e
					}
					historyCleanupObserve("reappear", ctx, home, last, nil, len(last.Cleaned))
				default:
					db, e := sql.Open("sqlite", filepath.Join(home, SourceHistoryName, SourceHistoryCatalogName))
					if e != nil {
						return e
					}
					defer db.Close()
					var raw string
					if e = db.QueryRow(`SELECT data FROM source_history_operations WHERE key=?`, q.Key).Scan(&raw); e != nil {
						return e
					}
					var op SourceHistoryOperation
					if e = json.Unmarshal([]byte(raw), &op); e != nil {
						return e
					}
					if scenario == "false-completed" {
						op.Phase = "completed"
						op.Cleaned = []string{}
						for _, m := range q.Plan.Members {
							op.Cleaned = append(op.Cleaned, m.File)
						}
					} else {
						op.Created = op.Created.Add(-time.Hour)
					}
					if _, e = db.Exec(`UPDATE source_history_operations SET phase=?,data=? WHERE key=?`, op.Phase, mustHistoryJSON(op), q.Key); e != nil {
						return e
					}
					historyCleanupObserve("replace", ctx, home, last, nil, len(last.Cleaned))
				}
				return nil
			}
			op, e := archiveSourceHistory(ctx, home, b, q, fault)
			if scenario == "progress64" {
				if e != nil || op.Phase != "completed" {
					t.Fatal(op, e)
				}
			} else if e == nil || !changed {
				t.Fatal("expected refusal", e)
			}
			historyCleanupObserve("stop", context.WithoutCancel(ctx), home, last, nil, len(last.Cleaned))
			saved, e := InspectSourceHistoryOperation(t.Context(), home, b, q.Key)
			if e != nil {
				if scenario != "worker" && scenario != "cold-ref" {
					t.Fatal(e)
				}
				db, err := sql.Open("sqlite", filepath.Join(home, SourceHistoryName, SourceHistoryCatalogName))
				if err != nil {
					t.Fatal(err)
				}
				var raw string
				err = db.QueryRow(`SELECT data FROM source_history_operations WHERE key=?`, q.Key).Scan(&raw)
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal([]byte(raw), &saved); err != nil {
					t.Fatal(err)
				}
			}
			before := make([][]byte, count)
			for i, m := range q.Plan.Members {
				before[i], _ = os.ReadFile(filepath.Join(home, "mesh", m.File))
			}
			retry, e := ArchiveSourceHistory(t.Context(), home, b, q)
			if ((scenario == "worker" || scenario == "cold-ref") && e == nil) || (scenario != "worker" && scenario != "cold-ref" && (e != nil || !reflect.DeepEqual(retry, saved))) {
				t.Fatal("retry changed operation", e)
			}
			historyCleanupObserve("retry", context.WithoutCancel(ctx), home, last, nil, len(last.Cleaned))
			for i, m := range q.Plan.Members {
				after, _ := os.ReadFile(filepath.Join(home, "mesh", m.File))
				if !reflect.DeepEqual(before[i], after) {
					t.Fatal("retry changed native evidence")
				}
			}
			if scenario == "lost-before" || scenario == "lost-after" || scenario == "lost-unlink" || scenario == "cancel" {
				historyCleanupObserve("reconcile", t.Context(), home, saved, nil, len(saved.Cleaned))
				done, e := ReconcileSourceHistory(t.Context(), home, b, q.Key)
				if e != nil || done.Phase != "completed" {
					t.Fatal(done, e)
				}
				historyCleanupObserve("stop", t.Context(), home, last, nil, len(last.Cleaned))
			}
		})
	}
}
