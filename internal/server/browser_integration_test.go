package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
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
	for _, path := range []string{"/", "/style.css", "/app.mjs", "/core.mjs", "/json-view.mjs", "/metric-help.mjs", "/lease-actions.mjs", "/route.mjs"} {
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
				if operation == "status" {
					raw, _ := json.Marshal(response.Result)
					var status model.Status
					if json.Unmarshal(raw, &status) != nil || !slices.Contains(status.Capabilities, "browser-dashboard-observation") || !slices.Contains(status.Capabilities, "browser-reviewed-lease-controls") {
						t.Fatal("dashboard capabilities do not advertise supported controls", status.Capabilities)
					}
				}
				break
			}
		}
	}
	for _, operation := range []string{"browser_ticket", "shutdown", "shutdown_status", "bulk_end", "release", "renew", "cancel", "acquire", "message", "message_send", "pool_mutate", "run"} {
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
	callAction := func(action model.LeaseAction) model.WireResponse {
		t.Helper()
		raw, _ := json.Marshal(action)
		if err := wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: "action", Operation: "lease_action", Params: raw}); err != nil {
			t.Fatal(err)
		}
		for {
			var response model.WireResponse
			if err := wsjson.Read(ctx, c, &response); err != nil {
				t.Fatal(err)
			}
			if response.ID == "action" {
				return response
			}
		}
	}
	decodeLease := func(response model.WireResponse, lease *model.Lease) error {
		raw, err := json.Marshal(response.Result)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, lease)
	}
	var lease model.Lease
	if err := client.Call(ctx, home, "acquire", model.Request{Reason: "browser reviewed controls", Resources: model.Resources{CPU: 1}, TTL: time.Minute}, &lease); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"reconcile", "storage_cleanup", "storage_handoff"} {
		response := callAction(model.LeaseAction{ID: lease.ID, Revision: lease.Revision, Action: action})
		if response.Error == nil || response.Error.Code != "forbidden" {
			t.Fatalf("unoffered browser action %s accepted: %+v", action, response)
		}
	}
	actionResponse := callAction(model.LeaseAction{ID: lease.ID, Revision: lease.Revision, Action: "release"})
	if actionResponse.Error == nil || actionResponse.Error.Code != "invalid_request" {
		t.Fatal("browser destructive action omitted reviewed consumers", actionResponse)
	}
	actionResponse = callAction(model.LeaseAction{ID: lease.ID, Revision: lease.Revision, Action: "renew", TTL: 2 * time.Minute})
	var renewed model.Lease
	if actionResponse.Error != nil || decodeLease(actionResponse, &renewed) != nil || renewed.Revision != lease.Revision+1 || !renewed.Expires.After(*lease.Expires) {
		t.Fatalf("browser renewal: %+v", actionResponse)
	}
	actionResponse = callAction(model.LeaseAction{ID: lease.ID, Revision: lease.Revision, Action: "release", Consumers: []model.LeaseRevision{}})
	if actionResponse.Error == nil || actionResponse.Error.Code != "revision_conflict" {
		t.Fatal("browser stale release accepted", actionResponse)
	}
	var queued model.Lease
	if err := client.Call(ctx, home, "acquire", model.Request{Reason: "browser queued cancellation", Resources: model.Resources{CPU: 1}, TTL: time.Minute, Queue: true}, &queued); err != nil || queued.State != "queued" {
		t.Fatal("queue fixture", err)
	}
	actionResponse = callAction(model.LeaseAction{ID: queued.ID, Revision: queued.Revision, Action: "cancel", Consumers: []model.LeaseRevision{}})
	var cancelled model.Lease
	if actionResponse.Error != nil || decodeLease(actionResponse, &cancelled) != nil || cancelled.State != "terminal" || cancelled.Outcome != "cancelled" {
		t.Fatalf("browser queue cancellation: %+v", actionResponse)
	}
	actionResponse = callAction(model.LeaseAction{ID: lease.ID, Revision: renewed.Revision, Action: "release", Consumers: []model.LeaseRevision{}})
	var released model.Lease
	if actionResponse.Error != nil || decodeLease(actionResponse, &released) != nil || released.State != "terminal" || released.Outcome != "released" {
		t.Fatalf("browser release: %+v", actionResponse)
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
