package sshservice

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSourceHistoryCleanupCheckpointCannotOverwriteChangedRow(t *testing.T) {
	home, binding, _ := settledSourceHistoryFixture(t, sourceHistoryCleanupBatchMembers+1)
	request := sourceArchiveRequest(t, home, binding, "changed-checkpoint-row")
	changed := false
	_, err := archiveSourceHistory(t.Context(), home, binding, request, func(stage string) error {
		if stage != "before_cleanup_checkpoint" || changed {
			return nil
		}
		changed = true
		db, e := sql.Open("sqlite", filepath.Join(home, SourceHistoryName, SourceHistoryCatalogName))
		if e != nil {
			return e
		}
		defer db.Close()
		_, e = db.Exec(`UPDATE source_history_operations SET data=json_set(data,'$.revision',json_extract(data,'$.revision')+1) WHERE key=?`, request.Key)
		return e
	})
	if err == nil || !changed {
		t.Fatal("stale cleanup overwrote a changed durable row", err)
	}
	saved, err := InspectSourceHistoryOperation(t.Context(), home, binding, request.Key)
	if err != nil || saved.Phase != "committed" || len(saved.Cleaned) != 0 {
		t.Fatal("stale batch replaced observed checkpoint", saved, err)
	}
	last := request.Plan.Members[len(request.Plan.Members)-1]
	if _, err = os.Stat(filepath.Join(home, "mesh", last.File)); err != nil {
		t.Fatal("conflicted checkpoint continued to a later batch", err)
	}
}

func TestSourceHistoryCleanupChecksAuthorityBetweenBatches(t *testing.T) {
	for _, change := range []string{"missing-cold-ref", "replaced-worker-lock"} {
		t.Run(change, func(t *testing.T) {
			home, binding, _ := settledSourceHistoryFixture(t, sourceHistoryCleanupBatchMembers+1)
			request := sourceArchiveRequest(t, home, binding, "changed-batch-authority")
			last := request.Plan.Members[len(request.Plan.Members)-1]
			path := filepath.Join(home, "mesh", last.File)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			changed := false
			_, err = archiveSourceHistory(t.Context(), home, binding, request, func(stage string) error {
				if stage != "after_cleanup_checkpoint" || changed {
					return nil
				}
				changed = true
				root := filepath.Join(home, SourceHistoryName)
				if change == "replaced-worker-lock" {
					name := filepath.Join(root, sourceHistoryWorkerLockName)
					if e := os.Rename(name, name+".retained"); e != nil {
						return e
					}
					return os.WriteFile(name, nil, 0600)
				}
				db, e := sql.Open("sqlite", filepath.Join(root, SourceHistoryCatalogName))
				if e != nil {
					return e
				}
				defer db.Close()
				_, e = db.Exec(`DELETE FROM source_history_refs WHERE source=? AND target=? AND key=?`, last.Source, last.Target, last.Key)
				return e
			})
			if err == nil || !changed {
				t.Fatal("changed authority permitted a later cleanup batch", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatal("later hot evidence not preserved", err)
			}
		})
	}
}

func TestSourceHistoryCleanupCheckpointContinuesOnlyOriginal(t *testing.T) {
	for _, stage := range []string{"before_cleanup_checkpoint", "after_cleanup_checkpoint"} {
		t.Run(stage, func(t *testing.T) {
			home, binding, intents := settledSourceHistoryFixture(t, sourceHistoryCleanupBatchMembers+1)
			request := sourceArchiveRequest(t, home, binding, "bounded-cleanup")
			lost := errors.New("retained cleanup checkpoint acknowledgement loss")
			_, err := archiveSourceHistory(t.Context(), home, binding, request, func(at string) error {
				if at == stage {
					return lost
				}
				return nil
			})
			if !errors.Is(err, lost) {
				t.Fatal(err)
			}
			saved, err := InspectSourceHistoryOperation(t.Context(), home, binding, request.Key)
			if err != nil || saved.Phase != "committed" {
				t.Fatal(saved, err)
			}
			want := 0
			if stage == "after_cleanup_checkpoint" {
				want = sourceHistoryCleanupBatchMembers
			}
			if len(saved.Cleaned) != want {
				t.Fatal("checkpoint acknowledgement differs", len(saved.Cleaned), want)
			}
			last := request.Plan.Members[len(request.Plan.Members)-1]
			remainingPath := filepath.Join(home, "mesh", last.File)
			before, err := os.ReadFile(remainingPath)
			if err != nil {
				t.Fatal(err)
			}
			again, err := ArchiveSourceHistory(t.Context(), home, binding, request)
			if err != nil || !reflect.DeepEqual(again, saved) {
				t.Fatal("ordinary exact-key retry resumed cleanup", again, err)
			}
			after, err := os.ReadFile(remainingPath)
			if err != nil || string(after) != string(before) {
				t.Fatal("ordinary retry changed remaining hot evidence", err)
			}
			for _, intent := range intents {
				if err = ReserveSource(t.Context(), home, binding, intent); !errors.Is(err, ErrIntentExists) {
					t.Fatal("partially cleaned cold key lost its permanent fence", err)
				}
			}
			done, err := ReconcileSourceHistory(t.Context(), home, binding, request.Key)
			if err != nil || done.Phase != "completed" || len(done.Cleaned) != len(request.Plan.Members) || !reflect.DeepEqual(done.Request, request) {
				t.Fatal(done, err)
			}
			for _, m := range request.Plan.Members {
				if _, err = os.Lstat(filepath.Join(home, "mesh", m.File)); !os.IsNotExist(err) {
					t.Fatal("original member not cleaned", err)
				}
			}
		})
	}
}

func TestSourceHistoryCleanupReappearingCheckpointedFileIsNotReplayed(t *testing.T) {
	home, binding, _ := settledSourceHistoryFixture(t, sourceHistoryCleanupBatchMembers+1)
	request := sourceArchiveRequest(t, home, binding, "reappeared-checkpoint")
	first := request.Plan.Members[0]
	name := filepath.Join(home, "mesh", first.File)
	original, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	lost := errors.New("pause after positive checkpoint")
	_, err = archiveSourceHistory(t.Context(), home, binding, request, func(stage string) error {
		if stage == "after_cleanup_checkpoint" {
			return lost
		}
		return nil
	})
	if !errors.Is(err, lost) {
		t.Fatal(err)
	}
	saved, err := InspectSourceHistoryOperation(t.Context(), home, binding, request.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(name, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ReconcileSourceHistory(t.Context(), home, binding, request.Key); err == nil {
		t.Fatal("reappearing hot file was treated as replayable cleanup")
	}
	retained, err := os.ReadFile(name)
	if err != nil || string(retained) != string(original) {
		t.Fatal("replacement evidence not retained", err)
	}
	after, err := InspectSourceHistoryOperation(t.Context(), home, binding, request.Key)
	if err != nil || !reflect.DeepEqual(saved, after) {
		t.Fatal("failed continuation advanced original checkpoint", after, err)
	}
}

func TestSourceHistoryCleanupUsesSeparateUnchangedDeadlineWindows(t *testing.T) {
	home, binding, _ := settledSourceHistoryFixture(t, sourceHistoryCleanupBatchMembers+1)
	request := sourceArchiveRequest(t, home, binding, "separate-cleanup-windows")
	windows := 0
	op, err := archiveSourceHistory(t.Context(), home, binding, request, func(stage string) error {
		if stage == "before_cleanup_checkpoint" {
			windows++
			// Two sub-five-second windows deliberately exceed one five-second
			// deadline in total. No deadline or selection bound is enlarged.
			time.Sleep(3 * time.Second)
		}
		return nil
	})
	if err != nil || op.Phase != "completed" || windows != 2 {
		t.Fatal("cleanup reused one cumulative mutation deadline", windows, op, err)
	}
}
