package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket/wsjson"
	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"strings"
	"testing"
	"time"
)

func TestMeshOperatorWatchReplacementHasDistinctGeneration(t *testing.T) {
	home := backend(t)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	connection, e := client.Connect(ctx, home)
	if e != nil {
		t.Fatal(e)
	}
	defer connection.CloseNow()
	var current string
	type response struct {
		ID           string          `json:"id"`
		Subscription string          `json:"subscription"`
		Result       json.RawMessage `json:"result"`
		Error        *model.Error    `json:"error"`
	}
	// Replacing a watch closes its old channel concurrently. Once the new ACK
	// arrives, neither old invalidations nor old subscription_lost may follow it.
	for i := 0; i < 10; i++ {
		id := fmt.Sprint(i)
		if e = wsjson.Write(ctx, connection, model.WireRequest{Version: 1, ID: id, Operation: "mesh_operator_watch", Params: json.RawMessage(`{"key":"watched"}`)}); e != nil {
			t.Fatal(e)
		}
		for {
			var r response
			if e = wsjson.Read(ctx, connection, &r); e != nil {
				t.Fatal(e)
			}
			if r.ID != id {
				continue
			}
			if r.Error != nil {
				t.Fatal(r.Error)
			}
			var ack struct {
				Subscription string `json:"subscription"`
			}
			if e = json.Unmarshal(r.Result, &ack); e != nil {
				t.Fatal(e)
			}
			if !strings.HasPrefix(ack.Subscription, "mesh_operator:") || ack.Subscription == current {
				t.Fatal("watch generation reused", ack)
			}
			current = ack.Subscription
			break
		}
	}
	if _, e = mesh.ClaimCoordination(home, "watched", "connect", strings.Repeat("a", 64), "ssh:fixture"); e != nil {
		t.Fatal(e)
	}
	var change response
	if e = wsjson.Read(ctx, connection, &change); e != nil {
		t.Fatal(e)
	}
	if change.Subscription != current || change.Error != nil {
		t.Fatal("stale old watch sent after new ACK", change)
	}
	var invalidation map[string]any
	if e = json.Unmarshal(change.Result, &invalidation); e != nil || len(invalidation) != 2 || invalidation["key"] != "watched" {
		t.Fatal("watch leaked full operation instead of invalidation", invalidation, e)
	}
}
