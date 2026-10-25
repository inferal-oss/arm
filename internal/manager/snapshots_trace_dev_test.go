//go:build arm_devtrace

package manager

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/snapshotlease"
)

func TestSnapshotReservationActualTransactionTrace(t *testing.T) {
	for _, scenario := range []string{"reserve-cancel", "pre-cancel", "lost-admit-ack", "lost-cancel-ack", "close-reopen", "admission-rollback", "deadline-before-check", "deadline-after-check", "commit-context-cancel"} {
		t.Run(scenario, func(t *testing.T) {
			f := newSnapshotFixture(t)
			observer := &snapshotTraceObserver{}
			snapshotTraceObservers.Store(f.home, observer)
			defer snapshotTraceObservers.Delete(f.home)
			capture := func(action string) { f.m.mu.Lock(); f.m.traceSnapshotLocked(action, ""); f.m.mu.Unlock() }
			capture("initial")
			q := snapshotRequest(f, "one-key")
			if scenario == "deadline-before-check" || scenario == "deadline-after-check" {
				q.WorkTTL = 50 * time.Millisecond
				fired := false
				observer.setHook(func(row snapshotTraceRow) {
					if fired {
						return
					}
					var expiry time.Time
					if scenario == "deadline-before-check" && row.Action == "admission-writes-staged" {
						var r snapshotlease.Record
						if len(row.Copies) != 1 || json.Unmarshal(row.Copies[0], &r) != nil {
							t.Fatal("missing real staged record")
						}
						expiry = r.WorkExpires
					} else if scenario == "deadline-after-check" && row.Action == "admission-check-accepted" {
						expiry = row.Cut.Deadline.WorkExpires
					} else {
						return
					}
					fired = true
					delay := time.Until(expiry) + time.Millisecond
					if delay > 2*time.Second {
						t.Fatal("unexpected native deadline bound")
					}
					if delay > 0 {
						timer := time.NewTimer(delay)
						<-timer.C
					}
				})
				r, e := f.m.reserveSnapshot(t.Context(), q)
				if !fired {
					t.Fatal("original cut not reached")
				}
				if scenario == "deadline-before-check" {
					if e == nil {
						t.Fatal("expired check admitted")
					}
					capture("expired-check-return")
				} else {
					if e != nil {
						t.Fatal(e)
					}
					if !time.Now().UTC().After(r.WorkExpires) {
						t.Fatal("actual delayed Commit did not cross deadline")
					}
					// Background expiry may already have settled this exact no-work tuple.
					// Inspect/cancel under the original Manager lock, preserving either real
					// history and never overwriting an earlier decision to fit the fixture.
					f.m.mu.Lock()
					current := f.m.snapshots.state.Copies[r.ID]
					if current.Phase == "reserved-no-work" {
						e = f.m.cancelSnapshotLocked(t.Context(), current, "settle delayed no-work reservation")
					} else if current.Phase != "cancelled-no-work" {
						e = errors.New("actual no-work tuple disappeared")
					}
					f.m.mu.Unlock()
					if e != nil {
						t.Fatal(e)
					}
					capture("delayed-commit-settled")
				}
				observer.setHook(nil)
			} else if scenario == "commit-context-cancel" {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				fired := false
				observer.setHook(func(row snapshotTraceRow) {
					if row.Action == "before-admit-commit" {
						fired = true
						cancel()
					}
				})
				if _, e := f.m.reserveSnapshot(ctx, q); e == nil {
					t.Fatal("actual cancelled SQL Commit unexpectedly succeeded")
				}
				if !fired {
					t.Fatal("actual Commit cancellation cut absent")
				}
				f.wantCloseError = true
				observer.setHook(nil)
				capture("cancelled-commit-return")
			} else if scenario == "admission-rollback" {
				if _, e := f.m.db.Exec(`CREATE TEMP TRIGGER trace_snapshot_rollback BEFORE INSERT ON events WHEN NEW.type='snapshot.reserved_no_work' BEGIN SELECT RAISE(ABORT,'actual trace final-write rollback'); END`); e != nil {
					t.Fatal(e)
				}
				if _, e := f.m.reserveSnapshot(t.Context(), q); e == nil {
					t.Fatal("actual rollback error absent")
				}
				capture("actual-admission-rollback-return")
			} else if scenario == "pre-cancel" {
				if e := f.m.cancelSnapshotReservation(t.Context(), q.Key, 0, "actual pre-admission cancellation"); e != nil {
					t.Fatal(e)
				}
				if _, e := f.m.reserveSnapshot(t.Context(), q); e == nil {
					t.Fatal("fence refused no request")
				}
				capture("cancelled-request-return")
			} else {
				if scenario == "lost-admit-ack" {
					f.m.snapshots.commitAckFailure = errors.New("actual trace: loss AFTER real commit")
				}
				r, e := f.m.reserveSnapshot(t.Context(), q)
				if scenario == "lost-admit-ack" {
					if e == nil {
						t.Fatal("missing actual unknown error")
					}
					f.wantCloseError = true
					var found bool
					r, found, e = f.m.lookupSnapshotReservation(t.Context(), q.Key)
					if e != nil || !found {
						t.Fatal(r, found, e)
					}
					capture("original-key-lookup-observed")
				} else {
					if e != nil {
						t.Fatal(e)
					}
					if scenario == "reserve-cancel" {
						retry, retryErr := f.m.reserveSnapshot(t.Context(), q)
						if retryErr != nil || retry != r {
							t.Fatal("exact retry changed actual receipt", retry, retryErr)
						}
						capture("exact-key-retry-observed")
						conflict := q
						conflict.Reason = "actual conflicting request"
						if _, e := f.m.reserveSnapshot(t.Context(), conflict); e == nil {
							t.Fatal("conflicting immutable key accepted")
						}
						capture("key-conflict-observed")
					}
					if scenario == "close-reopen" {
						capture("before-actual-manager-close")
						closeErr := f.m.Close()
						f.closed = true
						t.Logf("SNAPSHOT_RESERVATION_OWNER_RECEIPT close_error=%v", closeErr)
						if closeErr != nil {
							t.Fatal(closeErr)
						}
						m, e := Open(f.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 256 << 20})
						if e != nil {
							t.Fatal(e)
						}
						f.m = m
						f.closed = false
						capture("actual-manager-reopened")
					}
					if scenario == "lost-cancel-ack" {
						f.m.snapshots.commitAckFailure = errors.New("actual trace: cancel loss AFTER real commit")
						f.wantCloseError = true
					}
					e = f.m.cancelSnapshotReservation(t.Context(), r.Key, 1, "actual no-work cancel")
					if (e != nil) != (scenario == "lost-cancel-ack") {
						t.Fatal(e)
					}
					capture("cancel-return-observed")
					if scenario == "reserve-cancel" {
						retry, e := f.m.reserveSnapshot(t.Context(), q)
						if e != nil || retry.Phase != "cancelled-no-work" {
							t.Fatal("cancelled key was replayed", retry, e)
						}
						capture("cancelled-key-retry-observed")
					}
				}
			}
			// Stop this exact Manager before dumping so no suffix is lost. Close errors
			// remain raw evidence; they are not a claim of process/descriptor absence.
			closeErr := f.m.Close()
			f.closed = true
			if f.wantCloseError {
				if closeErr == nil {
					t.Fatal("unknown boundary falsely closed cleanly")
				}
			} else if closeErr != nil {
				t.Fatal(closeErr)
			}
			observer.mu.Lock()
			defer observer.mu.Unlock()
			if observer.Failure != nil || len(observer.Rows) == 0 {
				t.Fatal(observer.Failure)
			}
			for _, row := range observer.Rows {
				if row.Error != "" {
					t.Fatal(row.Error)
				}
				raw, e := json.Marshal(row)
				if e != nil {
					t.Fatal(e)
				}
				t.Log("SNAPSHOT_RESERVATION_RAW " + string(raw))
			}
		})
	}
}
