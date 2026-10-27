package server_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/model"
	"github.com/coder/websocket/wsjson"
)

func TestMonitorStreamMultiplexesAndResumesWithoutAuditTraffic(t *testing.T) {
	home := backend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var initial model.MonitorPage
	if e := client.Call(ctx, home, "monitor", model.MonitorQuery{}, &initial); e != nil {
		t.Fatal(e)
	}
	if initial.Host == "" || initial.Epoch == "" || initial.Cadence != 2*time.Second {
		t.Fatal(initial)
	}
	var status model.Status
	if e := client.Call(ctx, home, "status", nil, &status); e != nil {
		t.Fatal(e)
	}
	c, e := client.Connect(ctx, home)
	if e != nil {
		t.Fatal(e)
	}
	defer c.CloseNow()
	raw, _ := json.Marshal(model.MonitorQuery{Epoch: initial.Epoch, After: initial.Cursor})
	if e = wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: "monitor-one", Operation: "monitor_subscribe", Params: raw}); e != nil {
		t.Fatal(e)
	}
	if e = wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: "status", Operation: "status", Params: json.RawMessage("null")}); e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	var streamed model.MonitorPage
	for !(seen["monitor-one"] && seen["status"] && seen["sample"]) {
		var response struct {
			ID           string             `json:"id"`
			Subscription string             `json:"subscription"`
			Telemetry    *model.MonitorPage `json:"telemetry"`
			Error        *model.Error       `json:"error"`
		}
		if e = wsjson.Read(ctx, c, &response); e != nil {
			t.Fatal(e)
		}
		if response.Error != nil {
			t.Fatal(response.Error)
		}
		if response.ID != "" {
			seen[response.ID] = true
		}
		if response.Telemetry != nil {
			if response.Subscription != "monitor-one" || response.Telemetry.Latest == nil || response.Telemetry.Cursor <= initial.Cursor || response.Telemetry.Host != initial.Host {
				t.Fatal(response)
			}
			streamed = *response.Telemetry
			seen["sample"] = true
		}
	}
	var events []model.Event
	if e = client.Call(ctx, home, "events", map[string]int64{"after": status.Cursor}, &events); e != nil || len(events) != 0 {
		t.Fatal("telemetry wrote durable audit traffic", events, e)
	}
	var resumed model.MonitorPage
	if e = client.Call(ctx, home, "monitor", model.MonitorQuery{Epoch: streamed.Epoch, After: streamed.Cursor}, &resumed); e != nil || resumed.Gap || len(resumed.History) != 0 {
		t.Fatal("resume repeated an emitted sample", resumed, e)
	}
	if e = client.Call(ctx, home, "monitor", model.MonitorQuery{Epoch: "old-collector", After: 999999}, &resumed); e != nil || !resumed.Gap || len(resumed.History) == 0 {
		t.Fatal("collector restart did not resynchronize", resumed, e)
	}
	if e = wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: "stop-monitor", Operation: "monitor_unsubscribe"}); e != nil {
		t.Fatal(e)
	}
	for {
		var response model.WireResponse
		if e = wsjson.Read(ctx, c, &response); e != nil {
			t.Fatal(e)
		}
		if response.ID == "stop-monitor" {
			break
		}
	}
	for _, q := range []model.MonitorQuery{{Host: "other-host"}, {After: 1}, {Epoch: initial.Epoch, After: 999999}} {
		if e = client.Call(ctx, home, "monitor", q, nil); e == nil {
			t.Fatal("invalid monitor query accepted", q)
		}
	}
}

func TestMonitorClientReceivesSnapshotAndStream(t *testing.T) {
	home := backend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	pages := 0
	var previous int64
	err := client.WatchMonitor(ctx, home, model.MonitorQuery{}, func(page model.MonitorPage) error {
		if pages > 0 && page.Cursor <= previous {
			t.Fatal("stream did not advance")
		}
		previous = page.Cursor
		pages++
		if pages == 2 {
			cancel()
		}
		return nil
	})
	if pages != 2 || err == nil || ctx.Err() == nil {
		t.Fatal(pages, err)
	}
}
