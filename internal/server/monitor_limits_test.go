package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/observe"
)

func TestMonitorSubscriptionBoundReplacementAndRelease(t *testing.T) {
	s := &Server{token: "fixture-token", monitor: observe.NewMonitor("host", nil), monitorSlots: make(chan struct{}, 1)}
	h := httptest.NewServer(http.HandlerFunc(s.ws))
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connect := func() *websocket.Conn {
		c, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(h.URL, "http"), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer fixture-token"}}})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { c.CloseNow() })
		return c
	}
	call := func(c *websocket.Conn, id, op string) model.WireResponse {
		t.Helper()
		if e := wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: id, Operation: op, Params: json.RawMessage(`{}`)}); e != nil {
			t.Fatal(e)
		}
		var r model.WireResponse
		if e := wsjson.Read(ctx, c, &r); e != nil {
			t.Fatal(e)
		}
		if r.ID != id {
			t.Fatal(r)
		}
		return r
	}
	a, b := connect(), connect()
	if r := call(a, "one", "monitor_subscribe"); r.Error != nil {
		t.Fatal(r.Error)
	}
	if r := call(a, "replace", "monitor_subscribe"); r.Error != nil {
		t.Fatal("replacement consumed another subscription slot", r.Error)
	}
	if r := call(b, "two", "monitor_subscribe"); r.Error == nil || r.Error.Code != "monitor_busy" {
		t.Fatal("subscription bound failed", r)
	}
	if r := call(a, "stop", "monitor_unsubscribe"); r.Error != nil {
		t.Fatal(r.Error)
	}
	if r := call(b, "retry", "monitor_subscribe"); r.Error != nil {
		t.Fatal("unsubscribe leaked slot", r.Error)
	}
	b.CloseNow()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r := call(a, "after-disconnect", "monitor_subscribe")
		if r.Error == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnect leaked slot", r.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
