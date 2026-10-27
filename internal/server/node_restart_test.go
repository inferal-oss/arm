package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
)

func TestStartupAndRestartRefuseImpossibleDurablePhaseWithoutMutation(t *testing.T) {
	for _, kind := range []string{"running-accepted", "stopped-interrupted"} {
		t.Run(kind, func(t *testing.T) {
			home, record, state := stoppedLedger(t)
			if kind == "running-accepted" {
				state.Running = true
				state.Revision++
			} else {
				record.Phase = "interrupted"
			}
			if err := state.Validate(record); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			recordRaw, _ := json.Marshal(record)
			stateRaw, _ := json.Marshal(state)
			if _, err = db.Exec(`UPDATE node_stop_operations SET data=? WHERE key=?`, string(recordRaw), record.Plan.Key); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if _, err = db.Exec(`UPDATE node_stop_state SET data=? WHERE singleton=1`, string(stateRaw)); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			before := retainedStopRows(t, home)
			if review, _, err := PrepareNodeRestart(t.Context(), home, "must not approve impossible durable history"); err == nil || review != nil {
				if review != nil {
					_ = review.Close()
				}
				t.Fatal("restart accepted impossible phase", err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if err = Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 4}); err == nil {
				t.Fatal("startup accepted impossible durable phase")
			}
			if _, err = local.ReadEndpoint(home); !os.IsNotExist(err) {
				t.Fatal("startup published before refusing malformed history", err)
			}
			if after := retainedStopRows(t, home); strings.Join(before, "\n") != strings.Join(after, "\n") {
				t.Fatal("refusal mutated retained history")
			}
		})
	}
}

func TestLocalRestartReviewKeepsExactLockAndRetainsOldDuty(t *testing.T) {
	home, old, state := stoppedLedger(t)
	r, needed, err := PrepareNodeRestart(t.Context(), home, "operator reviewed a fresh generation")
	if err != nil || !needed || r == nil {
		t.Fatal(needed, err)
	}
	defer r.Close()
	p, err := r.Plan()
	if err != nil || p.Review.Expected != state {
		t.Fatal(p, err)
	}
	// Altering a display copy cannot change this process's retained approval.
	p.Review.Expected.Revision++
	p.Review.Reason = "changed display copy"
	if err = r.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = r.Apply(t.Context()); err == nil {
		t.Fatal("consumed restart review replayed")
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	current, present, err := nodestop.ReadState(db)
	if err != nil || !present || !current.Running || current.Revision != state.Revision+1 || current.Key != state.Key {
		t.Fatal(current, present, err)
	}
	retained, err := nodestop.Lookup(db, old.Plan.Key)
	if err != nil || retained.Phase != "interrupted" || retained.Digest != old.Digest {
		t.Fatal(retained, err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM events WHERE type='node_stop.restart_reviewed'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
func TestLocalRestartDeclineAndUnknownStateDoNotClearIntent(t *testing.T) {
	home, stopped, state := stoppedLedger(t)
	r, needed, err := PrepareNodeRestart(t.Context(), home, "preview only")
	if err != nil || !needed {
		t.Fatal(needed, err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	current, present, err := nodestop.ReadState(db)
	if err != nil || !present || current != state {
		t.Fatal("decline changed intent", current, err)
	}
	r, needed, err = PrepareNodeRestart(t.Context(), home, "changed state refusal")
	if err != nil || !needed {
		t.Fatal(needed, err)
	}
	defer r.Close()
	// Independent SQLite writer commits the complete reviewed-restart shape
	// after review. A valid new revision still cannot authorize this old review.
	current.Running = true
	current.Revision++
	stopped.Phase = "interrupted"
	raw, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	stopRaw, err := json.Marshal(stopped)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE node_stop_operations SET data=? WHERE key=?`, string(stopRaw), stopped.Plan.Key); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE node_stop_state SET data=? WHERE singleton=1`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = r.Apply(t.Context()); err == nil {
		t.Fatal("changed revision accepted")
	}
	retained, present, err := nodestop.ReadState(db)
	if err != nil || !present || retained != current {
		t.Fatal("refusal overwrote independent state", retained, err)
	}
}
func TestLocalRestartRefusesReplacedLockAndCreatesNoMissingState(t *testing.T) {
	home, _, _ := stoppedLedger(t)
	r, needed, err := PrepareNodeRestart(t.Context(), home, "lock binding control")
	if err != nil || !needed {
		t.Fatal(needed, err)
	}
	defer r.Close()
	if err = os.Rename(filepath.Join(home, "daemon.lock"), filepath.Join(home, "original.lock")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err = r.Apply(t.Context()); err == nil {
		t.Fatal("replacement lock accepted")
	}
	missing := filepath.Join(t.TempDir(), "never-created")
	if got, needed, err := PrepareNodeRestart(t.Context(), missing, "fresh start"); err != nil || needed || got != nil {
		t.Fatal(got, needed, err)
	}
	if _, err = os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("inspection created missing authority", err)
	}
}
