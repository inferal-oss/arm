package server_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/client"
	"github.com/inferal-oss/arm/internal/manager"
	"github.com/inferal-oss/arm/internal/model"
)

func TestGuardedOperatorOperationsUseSharedAPI(t *testing.T) {
	home := backend(t)
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	c := agents.Contact{ID: "operator-test", Adapter: "generic", Session: "00000000-0000-4000-8000-000000000001", CWD: t.TempDir(), Delivery: "poll"}
	if err := client.Call(ctx, home, "register", c, &c); err != nil {
		t.Fatal(err)
	}
	r := manager.MessageRequest{Key: "wire-send", Audience: c.ID, Body: "Guarded wire send", Targets: []agents.Contact{c}}
	var first, retry manager.Message
	if err := client.Call(ctx, home, "message_send", r, &first); err != nil {
		t.Fatal(err)
	}
	if err := client.Call(ctx, home, "message_send", r, &retry); err != nil || first.ID != retry.ID {
		t.Fatal("wire retry duplicated send", err)
	}
	var lease model.Lease
	if err := client.Call(ctx, home, "acquire", model.Request{Reason: "guarded wire lease", Resources: model.Resources{CPU: 1}, TTL: time.Minute}, &lease); err != nil {
		t.Fatal(err)
	}
	action := model.LeaseAction{ID: lease.ID, Revision: lease.Revision, Action: "renew", TTL: 2 * time.Minute}
	if err := client.Call(ctx, home, "lease_action", action, &lease); err != nil {
		t.Fatal(err)
	}
	action.Action = "release"
	action.TTL = 0
	err := client.Call(ctx, home, "lease_action", action, nil)
	var api *model.Error
	if !errors.As(err, &api) || api.Code != "revision_conflict" {
		t.Fatal("wire stale action accepted", err)
	}
	action.Revision = lease.Revision
	if err = client.Call(ctx, home, "lease_action", action, &lease); err != nil || lease.State != "terminal" {
		t.Fatal("wire release failed", err)
	}
}
