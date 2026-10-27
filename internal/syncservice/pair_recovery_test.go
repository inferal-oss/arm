package syncservice

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"getarm.org/internal/syncfs"
	"getarm.org/internal/syncreconcile"
)

func TestInspectPairRequiresBothDurableConfirmations(t *testing.T) {
	ctx := t.Context()
	localDB, remoteDB := openDB(t), openDB(t)
	localIntent, remoteIntent, rows := intents(t)
	reserve(t, localDB, localIntent, rows)
	reserve(t, remoteDB, remoteIntent, rows)
	local := &Ledger{DB: localDB, Key: localIntent.Key, Side: syncreconcile.Local}
	remote := &Ledger{DB: remoteDB, Key: remoteIntent.Key, Side: syncreconcile.Remote}
	checkAttention := func(stage string) {
		t.Helper()
		view, err := InspectPair(ctx, local, remote)
		if err != nil || !view.NeedsAttention || view.JointlyConfirmed || view.Key != localIntent.Key {
			t.Fatalf("%s: invalid pair recovery view: %+v %v", stage, view, err)
		}
	}
	checkAttention("reserved")
	localReceipt, err := local.CompleteOwnSide(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := syncfs.Receipt{Key: remoteIntent.Key, PlanDigest: remoteIntent.PlanDigest, Generation: remoteIntent.Generation,
		Side: syncreconcile.Remote, Index: 0, Action: expectedActions(remoteIntent, syncreconcile.Remote)[0], State: "prepared"}
	if err := remote.Prepare(ctx, r); err != nil {
		t.Fatal(err)
	}
	checkAttention("remote effect prepared")
	r.State = "applied"
	if err := remote.Outcome(ctx, r); err != nil {
		t.Fatal(err)
	}
	remoteReceipt, err := remote.CompleteOwnSide(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checkAttention("separate own receipts")
	if err := local.RecordPeerReceipt(ctx, remoteReceipt); err != nil {
		t.Fatal(err)
	}
	if err := remote.RecordPeerReceipt(ctx, localReceipt); err != nil {
		t.Fatal(err)
	}
	if _, err := local.ConfirmBaseline(ctx); err != nil {
		t.Fatal(err)
	}
	checkAttention("only source confirmed")
	if _, err := remote.ConfirmBaseline(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := InspectPair(ctx, local, remote)
	if err != nil || !view.JointlyConfirmed || view.NeedsAttention || view.LocalState != "confirmed" || view.RemoteState != "confirmed" {
		t.Fatalf("joint confirmation absent: %+v %v", view, err)
	}
	// A peer copy with a different but syntactically valid effect digest is
	// not the source's durable own completion, even after both states confirm.
	changed := localReceipt
	changed.EffectsDigest = digest("different effects")
	raw, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remoteDB.ExecContext(ctx, `UPDATE sync_operations SET local_receipt=? WHERE key=? AND side='remote'`, raw, remoteIntent.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectPair(ctx, local, remote); !errors.Is(err, ErrUnknown) {
		t.Fatalf("divergent peer receipt accepted: %v", err)
	}
}

func TestInspectPairRejectsDifferentReviewedIntent(t *testing.T) {
	ctx := context.Background()
	localDB, remoteDB := openDB(t), openDB(t)
	localIntent, remoteIntent, rows := intents(t)
	remoteIntent.Reason = "different reviewed reason"
	reserve(t, localDB, localIntent, rows)
	reserve(t, remoteDB, remoteIntent, rows)
	local := &Ledger{DB: localDB, Key: localIntent.Key, Side: syncreconcile.Local}
	remote := &Ledger{DB: remoteDB, Key: remoteIntent.Key, Side: syncreconcile.Remote}
	if _, err := InspectPair(ctx, local, remote); !errors.Is(err, ErrUnknown) {
		t.Fatalf("different endpoint intent accepted: %v", err)
	}
}
