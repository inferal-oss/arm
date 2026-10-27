package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"getarm.org/internal/agents"
	"getarm.org/internal/client"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
)

func TestInboxLongPollMultiplexesAndBoundsOutstandingRequests(t *testing.T) {
	home := backend(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c := agents.Contact{ID: "poll-protocol", Adapter: "generic", Delivery: "poll", Session: uuid.NewString(), CWD: t.TempDir()}
	if err := client.Call(ctx, home, "register", c, &c); err != nil {
		t.Fatal(err)
	}
	conn, err := client.Connect(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	send := func(id, op string, p any) {
		t.Helper()
		raw, _ := json.Marshal(p)
		if err := wsjson.Write(ctx, conn, model.WireRequest{Version: 1, ID: id, Operation: op, Params: raw}); err != nil {
			t.Fatal(err)
		}
	}
	read := func() model.WireResponse {
		t.Helper()
		var r model.WireResponse
		if err := wsjson.Read(ctx, conn, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	p := manager.InboxRequest{ID: c.ID, Session: c.Session, Registration: c.Registration, Wait: 20 * time.Second}
	send("waiting", "agent_inbox", p)
	send("status", "status", nil)
	r := read()
	if r.ID != "status" || r.Error != nil {
		t.Fatalf("long poll blocked another request: %+v", r)
	}
	for i := 1; i < 8; i++ {
		send(fmt.Sprint("waiting", i), "agent_inbox", p)
	}
	send("overflow", "status", nil)
	r = read()
	if r.ID != "overflow" || r.Error == nil || r.Error.Code != "too_many_requests" {
		t.Fatalf("unbounded in-flight requests: %+v", r)
	}
	var msg manager.Message
	if err := client.Call(ctx, home, "message", map[string]string{"audience": c.ID, "body": "wake all polling consumers"}, &msg); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for range 8 {
		r = read()
		if r.Error != nil || ids[r.ID] {
			t.Fatalf("poll response: %+v", r)
		}
		ids[r.ID] = true
		raw, _ := json.Marshal(r.Result)
		var page manager.InboxPage
		if json.Unmarshal(raw, &page) != nil || len(page.Items) != 1 || page.Items[0].Message.ID != msg.ID {
			t.Fatalf("wrong inbox response: %s", raw)
		}
	}
	send("ready", "status", nil)
	if r = read(); r.ID != "ready" || r.Error != nil {
		t.Fatalf("slots not released: %+v", r)
	}
}
