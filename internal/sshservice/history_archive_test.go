package sshservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
)

func settledSourceHistoryFixture(t *testing.T, count int) (string, Binding, []SourceIntent) {
	t.Helper()
	home, b := stateFixture(t)
	pin := statePin(t, home, b)
	items := []SourceIntent{}
	for i := 0; i < count; i++ {
		intent := sourceFixture(t, home, b, pin, fmt.Sprintf("history-%03d", i))
		if e := ReserveSource(t.Context(), home, b, intent); e != nil {
			t.Fatal(e)
		}
		if e := os.Remove(intent.Directory.Path); e != nil {
			t.Fatal(e)
		}
		if e := SettleSource(t.Context(), home, b, intent, terminalReceipt(intent)); e != nil {
			t.Fatal(e)
		}
		items = append(items, intent)
	}
	return home, b, items
}
func sourceArchiveRequest(t *testing.T, home string, b Binding, key string) SourceHistoryRequest {
	t.Helper()
	p, e := PlanSourceHistory(t.Context(), home, b, SourceHistorySelection{Limit: MaxCompletions})
	if e != nil {
		t.Fatal(e)
	}
	return SourceHistoryRequest{1, key, "Archive verified existing source receipts without rerunning SSH", p}
}
func TestSourceHistoryArchivePublicSourceRoundtrip(t *testing.T) {
	home, b, items := settledSourceHistoryFixture(t, 2)
	q := sourceArchiveRequest(t, home, b, "archive-two")
	op, e := ArchiveSourceHistory(t.Context(), home, b, q)
	if e != nil {
		t.Fatal(e)
	}
	if op.Phase != "completed" || len(op.Cleaned) != 2 {
		t.Fatal(op)
	}
	saved, e := InspectSourceHistoryOperation(t.Context(), home, b, q.Key)
	if e != nil || !reflect.DeepEqual(saved, op) {
		t.Fatal("exact archive lookup changed", e)
	}
	again, e := ArchiveSourceHistory(t.Context(), home, b, q)
	if e != nil || !reflect.DeepEqual(again, op) {
		t.Fatal("exact retry changed history", e)
	}
	s, e := ReadState(t.Context(), home, b)
	if e != nil || len(s.Completions) != 0 || s.History.Archived != 2 {
		t.Fatalf("source history roster/count mismatch: %v", e)
	}
	for _, intent := range items {
		got, e := LookupSourceHistory(t.Context(), home, b, intent.Request.Pin.Target, intent.Request.Key)
		if e != nil || !got.Archived || got.Completion == nil || !exactIntent(got.Completion.Intent, intent) {
			t.Fatal("cold exact receipt absent", e)
		}
		if e = ReserveSource(t.Context(), home, b, intent); !errors.Is(e, ErrIntentExists) {
			t.Fatal("archived key unfenced", e)
		}
		if e = SettleSource(t.Context(), home, b, intent, got.Completion.Receipt); e != nil {
			t.Fatal("read-only original settlement retry failed", e)
		}
	}
	changed := q
	changed.Reason = "another reason"
	if _, e = ArchiveSourceHistory(t.Context(), home, b, changed); e == nil {
		t.Fatal("samekey changedrequest accepted")
	}
}
func TestSourceHistory64ArchiveThen65EveryOldKeyFencedAfterReopen(t *testing.T) {
	home, b, items := settledSourceHistoryFixture(t, MaxCompletions)
	next := sourceFixture(t, home, b, items[0].Request.Pin, "new-key-65")
	if e := ReserveSource(t.Context(), home, b, next); e == nil {
		t.Fatal("full hot history admitted65th before archive")
	}
	q := sourceArchiveRequest(t, home, b, "archive64")
	if _, e := ArchiveSourceHistory(t.Context(), home, b, q); e != nil {
		t.Fatal(e)
	}
	for _, old := range items {
		if e := ReserveSource(t.Context(), home, b, old); !errors.Is(e, ErrIntentExists) {
			t.Fatal("old key became dispatchable", old.Request.Key, e)
		}
	}
	if e := ReserveSource(t.Context(), home, b, next); e != nil {
		t.Fatal("65th after explicit archival refused", e)
	}
	s, e := ReadState(t.Context(), home, b)
	if e != nil || s.History.Archived != 64 || len(s.Intents) != 1 {
		t.Fatalf("source history roster/count mismatch: %v", e)
	}
}
func TestSourceHistoryLostAcknowledgementsReconcileOnlyOriginal(t *testing.T) {
	for _, point := range []string{"after_publish", "before_catalog_commit", "after_catalog_commit", "before_unlink", "after_unlink", "after_unlink_fsync"} {
		t.Run(point, func(t *testing.T) {
			home, b, items := settledSourceHistoryFixture(t, 1)
			q := sourceArchiveRequest(t, home, b, "archive-loss")
			sentinel := errors.New("test lost acknowledgement")
			op, e := archiveSourceHistory(t.Context(), home, b, q, func(stage string) error {
				if stage == point {
					return sentinel
				}
				return nil
			})
			if !errors.Is(e, sentinel) || op.Request.Key != q.Key {
				t.Fatal("failure lost exact op", op, e)
			}
			before, e := InspectSourceHistoryOperation(t.Context(), home, b, q.Key)
			if e != nil {
				t.Fatal(e)
			}
			rawBefore, _ := json.Marshal(before)
			retry, e := ArchiveSourceHistory(t.Context(), home, b, q)
			if e != nil {
				t.Fatal(e)
			}
			rawRetry, _ := json.Marshal(retry)
			if string(rawRetry) != string(rawBefore) {
				t.Fatal("ordinary retry resumed unknown work")
			}
			done, e := ReconcileSourceHistory(t.Context(), home, b, q.Key)
			if e != nil || done.Phase != "completed" {
				t.Fatal("explicit exact continuation failed", done, e)
			}
			got, e := LookupSourceHistory(t.Context(), home, b, items[0].Request.Pin.Target, items[0].Request.Key)
			if e != nil || !got.Archived {
				t.Fatal(e)
			}
			again, e := ReconcileSourceHistory(t.Context(), home, b, q.Key)
			if e != nil || !reflect.DeepEqual(again, done) {
				t.Fatal("settled reconcile grew history", e)
			}
		})
	}
}
func TestSourceHistoryPlanNoWritesAndUnknownArtifactsRefusal(t *testing.T) {
	home, b := stateFixture(t)
	before, _ := os.ReadDir(home)
	if _, e := PlanSourceHistory(t.Context(), home, b, SourceHistorySelection{Limit: 64}); e == nil {
		t.Fatal("empty source history eligible")
	}
	after, _ := os.ReadDir(home)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("plan created catalog")
	}
	pin := statePin(t, home, b)
	intent := sourceFixture(t, home, b, pin, "pending-unknown")
	if e := ReserveSource(t.Context(), home, b, intent); e != nil {
		t.Fatal(e)
	}
	if _, e := PlanSourceHistory(t.Context(), home, b, SourceHistorySelection{Limit: 64}); e == nil {
		t.Fatal("pending work inferred settled")
	}
	if e := os.Remove(intent.Directory.Path); e != nil {
		t.Fatal(e)
	}
	if e := SettleSource(t.Context(), home, b, intent, terminalReceipt(intent)); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(intent.Directory.Path, 0700); e != nil {
		t.Fatal(e)
	}
	defer os.Remove(intent.Directory.Path)
	if _, e := PlanSourceHistory(t.Context(), home, b, SourceHistorySelection{Before: time.Now().Add(time.Hour), Limit: 64}); e == nil {
		t.Fatal("reappeared artifacts inferred safe")
	}
}
