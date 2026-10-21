package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/server"
)

func backend(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "arm")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(5 * time.Second):
			t.Error("backend shutdown stalled")
		}
	})
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		if _, e := os.Stat(filepath.Join(home, "endpoint.json")); e == nil {
			return home
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("backend unavailable")
	return ""
}

func TestWebSocketMultiplexesResponsesAndEvents(t *testing.T) {
	home := backend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var snapshot model.Status
	if e := client.Call(ctx, home, "status", nil, &snapshot); e != nil {
		t.Fatal(e)
	}
	c, e := client.Connect(ctx, home)
	if e != nil {
		t.Fatal(e)
	}
	defer c.CloseNow()
	raw, _ := json.Marshal(map[string]int64{"after": snapshot.Cursor})
	if e = wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: "subscribe", Operation: "subscribe", Params: raw}); e != nil {
		t.Fatal(e)
	}
	if e = wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: "status", Operation: "status", Params: json.RawMessage("null")}); e != nil {
		t.Fatal(e)
	}
	var l model.Lease
	r := model.Request{Reason: "protocol attribution fixture", Resources: model.Resources{CPU: 1}, TTL: time.Minute}
	if e = client.Call(ctx, home, "acquire", r, &l); e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for !(seen["subscribe"] && seen["status"] && seen["event"]) {
		var msg model.WireResponse
		if e = wsjson.Read(ctx, c, &msg); e != nil {
			t.Fatal(e)
		}
		if msg.Error != nil {
			t.Fatal(msg.Error)
		}
		if msg.ID != "" {
			seen[msg.ID] = true
		}
		if msg.Event != nil && msg.Event.Entity == l.ID {
			seen["event"] = true
		}
	}
	if e = client.Call(ctx, home, "shutdown", nil, nil); e == nil {
		t.Fatal("stopped backend with an outstanding lease")
	}
	if e = client.Call(ctx, home, "release", map[string]string{"id": l.ID}, nil); e != nil {
		t.Fatal(e)
	}
	if e = client.Call(ctx, home, "shutdown", nil, nil); e != nil {
		t.Fatal(e)
	}
}

func TestLoopbackRequiresAuthenticationAndBrowserOrigin(t *testing.T) {
	home := backend(t)
	ep, e := local.ReadEndpoint(home)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, response, e := websocket.Dial(ctx, ep.URL, nil)
	if e == nil || response == nil || response.StatusCode != 401 {
		t.Fatalf("unauthenticated connection accepted: %v %v", response, e)
	}
	_, response, e = websocket.Dial(ctx, ep.URL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + ep.Token}, "Origin": []string{"https://unrelated.invalid"}}})
	if e == nil || response == nil || response.StatusCode != 403 {
		t.Fatalf("cross-origin connection accepted: %v %v", response, e)
	}
}

func TestDuplicateAuthorityCannotOpenState(t *testing.T) {
	home := backend(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e := server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1}); e == nil {
		t.Fatal("second authority acquired the same state")
	}
}
