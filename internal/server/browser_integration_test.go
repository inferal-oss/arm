package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

func TestDashboardBootstrapAndSharedWebSocket(t *testing.T) {
	home := backend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var link model.BrowserLink
	if err := client.Call(ctx, home, "browser_ticket", nil, &link); err != nil {
		t.Fatal(err)
	}
	ep, _ := local.ReadEndpoint(home)
	base, ticket, ok := strings.Cut(link.URL, "/#connect=")
	if !ok || strings.Contains(link.URL, ep.Token) {
		t.Fatal("unsafe browser link")
	}
	for _, path := range []string{"/", "/style.css", "/app.mjs", "/core.mjs", "/json-view.mjs"} {
		r, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || len(data) == 0 || bytes.Contains(data, []byte(ep.Token)) || r.Header.Get("Content-Security-Policy") == "" || r.Header.Get("Cache-Control") != "no-store" || r.Header.Get("Set-Cookie") != "" {
			t.Fatalf("unsafe asset %s: %d %v", path, r.StatusCode, r.Header)
		}
	}
	exchange := func(origin, body, content string) (*http.Response, []byte) {
		t.Helper()
		r, _ := http.NewRequestWithContext(ctx, "POST", base+"/v1/browser/session", strings.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", content)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		return response, data
	}
	body := `{"ticket":"` + ticket + `"}`
	for _, origin := range []string{"", "null", "http://attacker.invalid", base + "9"} {
		r, _ := exchange(origin, body, "application/json")
		if r.StatusCode != 403 {
			t.Fatalf("origin %q accepted: %d", origin, r.StatusCode)
		}
	}
	for _, invalid := range []string{body + " {}", `{"ticket":"` + ticket + `","unknown":true}`, strings.Repeat("x", 1025)} {
		r, _ := exchange(base, invalid, "application/json")
		if r.StatusCode != 400 {
			t.Fatalf("invalid body accepted: %d", r.StatusCode)
		}
	}
	r, _ := exchange(base, body, "text/plain")
	if r.StatusCode != 415 {
		t.Fatal("form-compatible request accepted")
	}
	r, data := exchange(base, body, "application/json")
	var session struct {
		Token string `json:"token"`
	}
	if r.StatusCode != 200 || r.Header.Get("Set-Cookie") != "" || json.Unmarshal(data, &session) != nil || len(session.Token) != 64 {
		t.Fatalf("exchange: %d %s", r.StatusCode, data)
	}
	r, _ = exchange(base, body, "application/json")
	if r.StatusCode != 401 {
		t.Fatal("ticket replay accepted")
	}
	options := &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{base}}, Subprotocols: []string{"arm.v1", "arm.auth." + session.Token}}
	c, response, err := websocket.Dial(ctx, ep.URL, options)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	c.SetReadLimit(4 << 20) // Match ARM's native and browser response boundary.
	if c.Subprotocol() != "arm.v1" || strings.Contains(response.Header.Get("Sec-WebSocket-Protocol"), session.Token) {
		t.Fatal("credential reflected in selected subprotocol")
	}
	for _, operation := range []string{"status", "monitor_subscribe", "subscribe", "messages"} {
		if err := wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: operation, Operation: operation, Params: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
		for {
			var response model.WireResponse
			if err := wsjson.Read(ctx, c, &response); err != nil {
				t.Fatal(err)
			}
			if response.ID == operation {
				if response.Error != nil {
					t.Fatal(response.Error)
				}
				break
			}
		}
	}
	for _, operation := range []string{"browser_ticket", "shutdown", "release", "message", "message_send", "lease_action", "pool_mutate", "run"} {
		if err := wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: operation, Operation: operation, Params: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
		for {
			var response model.WireResponse
			if err := wsjson.Read(ctx, c, &response); err != nil {
				t.Fatal(err)
			}
			if response.ID == operation {
				if response.Error == nil || response.Error.Code != "forbidden" {
					t.Fatalf("browser mutation %s: %+v", operation, response)
				}
				break
			}
		}
	}
	options.HTTPHeader.Set("Origin", "http://unrelated.invalid")
	if connection, _, err := websocket.Dial(ctx, ep.URL, options); err == nil {
		connection.CloseNow()
		t.Fatal("cross-origin session accepted")
	}
	// No bearer credential means no query-string or cookie shortcut either.
	if connection, _, err := websocket.Dial(ctx, ep.URL+"?token="+session.Token, nil); err == nil {
		connection.CloseNow()
		t.Fatal("query credential accepted")
	}
}
