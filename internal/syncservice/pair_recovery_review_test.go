package syncservice

import (
	"testing"

	"github.com/yrashk/arm/internal/syncfs"
	"github.com/yrashk/arm/internal/syncreconcile"
)

func TestReviewPairChecksOneSidedConfirmedEvidence(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"missing-baseline", `DELETE FROM sync_baselines`},
		{"changed-baseline", `UPDATE sync_baselines SET seal='changed'`},
		{"missing-confirmed-peer-copy", `UPDATE sync_operations SET remote_receipt=NULL WHERE side='local'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			leftDB, rightDB := openDB(t), openDB(t)
			leftIntent, rightIntent, rows := intents(t)
			reserve(t, leftDB, leftIntent, rows)
			reserve(t, rightDB, rightIntent, rows)
			left := &Ledger{DB: leftDB, Key: leftIntent.Key, Side: syncreconcile.Local}
			right := &Ledger{DB: rightDB, Key: rightIntent.Key, Side: syncreconcile.Remote}
			leftReceipt, err := left.CompleteOwnSide(ctx)
			if err != nil {
				t.Fatal(err)
			}
			effect := syncfs.Receipt{Key: rightIntent.Key, PlanDigest: rightIntent.PlanDigest, Generation: rightIntent.Generation,
				Side: syncreconcile.Remote, Index: 0, Action: expectedActions(rightIntent, syncreconcile.Remote)[0], State: "prepared"}
			if err := right.Prepare(ctx, effect); err != nil {
				t.Fatal(err)
			}
			effect.State = "applied"
			if err := right.Outcome(ctx, effect); err != nil {
				t.Fatal(err)
			}
			rightReceipt, err := right.CompleteOwnSide(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := left.RecordPeerReceipt(ctx, rightReceipt); err != nil {
				t.Fatal(err)
			}
			if err := right.RecordPeerReceipt(ctx, leftReceipt); err != nil {
				t.Fatal(err)
			}
			if _, err := left.ConfirmBaseline(ctx); err != nil {
				t.Fatal(err)
			}
			view, err := InspectPair(ctx, left, right)
			if err != nil || !view.NeedsAttention || view.JointlyConfirmed || view.LocalState != "confirmed" || view.RemoteState != "complete" {
				t.Fatal("valid one-sided confirmation lost", view, err)
			}
			if _, err := leftDB.ExecContext(ctx, tc.query); err != nil {
				t.Fatal(err)
			}
			var before, after, peerBefore, peerAfter int64
			if err := leftDB.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if err := rightDB.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&peerBefore); err != nil {
				t.Fatal(err)
			}
			if _, err := InspectPair(ctx, left, right); err == nil {
				t.Error("one-sided confirmed endpoint lost durable evidence without refusal")
			}
			if err := leftDB.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil || before != after {
				t.Fatal("inspection changed durable endpoint evidence", before, after, err)
			}
			if err := rightDB.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&peerAfter); err != nil || peerBefore != peerAfter {
				t.Fatal("inspection changed durable peer evidence", peerBefore, peerAfter, err)
			}
		})
	}
}
