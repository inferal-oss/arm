package routing

import (
	"database/sql"
	"testing"
	"time"

	"getarm.org/internal/workspace"
)

func TestRoutingActualProducerExpiresBeforeFinalCommitAndRollsBackAllEvidence(t *testing.T) {
	id, _, b, c := messageFixture(t)
	before := readMessageRecords(t, b)
	workspaceBefore, e := b.store.Read(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	_, e = b.runtime.RouteTransaction(t.Context(), id, b.identity.Host, c.identity.Host, workspace.RouteMessage, func(tx *sql.Tx, a workspace.RouteAccess) error {
		_, created, e := Prepare(t.Context(), tx, a, Intent{Workspace: id, Key: "expired-before-commit", Target: c.identity.Host, From: HostEndpoint(a.OriginGeneration), To: HostEndpoint(a.DestinationGeneration), Body: "never durable after current validity ended", TTL: time.Hour})
		if e != nil {
			return e
		}
		if !created {
			t.Fatal("actual source producer was not staged")
		}
		b.clock.advance(10 * time.Minute)
		return nil
	})
	if e == nil {
		t.Fatal("actual prepare committed after membership expiry")
	}
	after := readMessageRecords(t, b)
	if hash(before) != hash(after) || after.Initialized {
		t.Fatal("expired transaction left routing schema/rows/journal/counters")
	}
	workspaceAfter, e := b.store.Read(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if hash(workspaceBefore) != hash(workspaceAfter) {
		t.Fatal("failed routed admission changed workspace authority/cache evidence")
	}
	if _, e = b.runtime.RouteAccess(t.Context(), id, b.identity.Host, c.identity.Host, workspace.RouteMessage); e == nil {
		t.Fatal("rolled-back admission refreshed expired membership")
	}
}
