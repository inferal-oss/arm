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
	"github.com/google/uuid"
	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/manager"
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
	for _, path := range []string{"/", "/style.css", "/app.mjs", "/core.mjs", "/json-view.mjs", "/metric-help.mjs", "/lease-actions.mjs", "/lease-state.mjs", "/io-devices.mjs", "/io-selector.mjs", "/route.mjs", "/dashboard-shortcuts.mjs", "/activity-feed.mjs", "/operator-messaging.mjs", "/connection-links.mjs", "/pool-model.mjs", "/pool-operations.mjs", "/pool-console.mjs"} {
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
					if json.Unmarshal(raw, &status) != nil || !slices.Contains(status.Capabilities, "browser-dashboard-observation") || !slices.Contains(status.Capabilities, "browser-reviewed-lease-controls") || !slices.Contains(status.Capabilities, "browser-reviewed-messaging") || !slices.Contains(status.Capabilities, "browser-reviewed-pool-controls") {
						t.Fatal("dashboard capabilities do not advertise supported controls", status.Capabilities)
					}
				}
				break
			}
		}
	}
	for _, operation := range []string{"browser_ticket", "shutdown", "shutdown_status", "bulk_end", "release", "renew", "cancel", "acquire", "message", "run"} {
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

	// Browser pool controls use the same native manager ledger, with complete
	// reviewed scope and exact integer fields rather than rounded UI numbers.
	type poolWireReply struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *model.Error    `json:"error,omitempty"`
	}
	callPool := func(operation string, params any) poolWireReply {
		t.Helper()
		raw, _ := json.Marshal(params)
		if err := wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: "pool-control", Operation: operation, Params: raw}); err != nil {
			t.Fatal(err)
		}
		for {
			var reply poolWireReply
			if err := wsjson.Read(ctx, c, &reply); err != nil {
				t.Fatal(err)
			}
			if reply.ID == "pool-control" {
				return reply
			}
		}
	}
	var poolStatus model.Status
	if err := client.Call(ctx, home, "status", nil, &poolStatus); err != nil {
		t.Fatal(err)
	}
	maxCapacity := int64(9223372036854775807)
	poolCreate := model.PoolMutation{Operation: "create", Name: "browser exact permits", Scope: "global", Unit: "permits", Capacity: &maxCapacity, Reason: "reviewed browser creation", Key: local.ID()}
	if reply := callPool("pool_mutate", poolCreate); reply.Error == nil || reply.Error.Code != "invalid_request" {
		t.Fatal("unreviewed browser creation allowed", reply)
	}
	poolCreate.Reviewed = &model.PoolReview{Authority: poolStatus.Host, Consumers: []model.LeaseRevision{}}
	reply := callPool("pool_mutate", poolCreate)
	var created model.PoolChange
	poolRaw, _ := json.Marshal(reply.Result)
	if reply.Error != nil || json.Unmarshal(poolRaw, &created) != nil || created.Pool.Capacity != maxCapacity {
		t.Fatal("browser int64 pool creation", reply)
	}
	rename := model.PoolMutation{Operation: "rename", ID: created.Pool.ID, Name: "forbidden rename", Revision: &created.Pool.Revision, Reason: "unsupported dashboard edit", Key: local.ID(), Reviewed: poolCreate.Reviewed}
	if reply := callPool("pool_mutate", rename); reply.Error == nil || reply.Error.Code != "forbidden" {
		t.Fatal("browser raw unsupported mutation allowed", reply)
	}
	var poolLease model.Lease
	if err := client.Call(ctx, home, "acquire", model.Request{Reason: "browser reviewed pool consumer", TTL: time.Minute, Pools: []model.PoolClaim{{Pool: created.Pool.ID, Quantity: maxCapacity}}}, &poolLease); err != nil {
		t.Fatal(err)
	}
	var poolView model.PoolView
	if err := client.Call(ctx, home, "pool_inspect", map[string]string{"id": created.Pool.ID}, &poolView); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	poolSetRevision := poolView.Pool.Revision
	poolSet := model.PoolMutation{Operation: "set", ID: created.Pool.ID, Capacity: &zero, Revision: &poolSetRevision, Reason: "retain exact obligations below capacity", Key: local.ID(), Reviewed: poolView.Review}
	reply = callPool("pool_mutate", poolSet)
	if reply.Error != nil {
		t.Fatal("browser reviewed reduction", reply)
	}
	if err := client.Call(ctx, home, "pool_inspect", map[string]string{"id": created.Pool.ID}, &poolView); err != nil || poolView.Reserved != maxCapacity || poolView.Deficit != maxCapacity {
		t.Fatal("browser reduction lost obligations", poolView, err)
	}
	lookupReply := callPool("pool_lookup", map[string]string{"idempotency_key": poolSet.Key})
	var poolLookup manager.PoolMutationLookup
	poolRaw, _ = json.Marshal(lookupReply.Result)
	if lookupReply.Error != nil || json.Unmarshal(poolRaw, &poolLookup) != nil || !poolLookup.Found || poolLookup.Change.Pool.Capacity != 0 {
		t.Fatal("browser read-only pool lookup", lookupReply)
	}
	stalePool := poolSet
	stalePool.Key = local.ID()
	stalePool.Revision = &poolView.Pool.Revision
	stalePool.Reviewed = poolCreate.Reviewed
	if reply := callPool("pool_mutate", stalePool); reply.Error == nil || reply.Error.Code != "review_conflict" {
		t.Fatal("browser incomplete pool review allowed", reply)
	}
	poolDelete := model.PoolMutation{Operation: "delete", ID: created.Pool.ID, Revision: &poolView.Pool.Revision, Reason: "must preserve live obligation", Key: local.ID(), Reviewed: poolView.Review}
	if reply := callPool("pool_mutate", poolDelete); reply.Error == nil || reply.Error.Code != "pool_in_use" {
		t.Fatal("browser deleted active obligation", reply)
	}
	if err := client.Call(ctx, home, "release", map[string]string{"id": poolLease.ID}, nil); err != nil {
		t.Fatal(err)
	}
	// Exact key returns its committed result even though the reviewed consumer
	// has ended since the original mutation; it must not apply the change twice.
	if reply := callPool("pool_mutate", poolSet); reply.Error != nil {
		t.Fatal("browser exact-key retry lost prior result", reply)
	}

	callMessage := func(operation string, params any) model.WireResponse {
		t.Helper()
		raw, _ := json.Marshal(params)
		if err := wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: "messaging", Operation: operation, Params: raw}); err != nil {
			t.Fatal(err)
		}
		for {
			var response model.WireResponse
			if err := wsjson.Read(ctx, c, &response); err != nil {
				t.Fatal(err)
			}
			if response.ID == "messaging" {
				return response
			}
		}
	}
	var contact agents.Contact
	if err := client.Call(ctx, home, "register", agents.Contact{ID: "browser-agent", Adapter: "codex", Session: uuid.NewString(), CWD: t.TempDir(), Delivery: "poll"}, &contact); err != nil {
		t.Fatal(err)
	}
	reviewed := manager.MessageRequest{Key: "browser-reviewed-message", Audience: contact.ID, Body: "Browser exact session", Targets: []agents.Contact{contact}}
	for _, invalid := range []manager.MessageRequest{{Key: "missing-targets", Audience: contact.ID, Body: "No implicit targeting"}, {Key: "operator-inbox", Audience: "operator", Body: "Use CLI", Targets: []agents.Contact{}}} {
		response := callMessage("message_send", invalid)
		if response.Error == nil || response.Error.Code != "invalid_request" {
			t.Fatal("browser accepted implicit/operator audience", response)
		}
	}
	responseMessage := callMessage("message_send", reviewed)
	if responseMessage.Error != nil {
		t.Fatal("reviewed browser send", responseMessage.Error)
	}
	lookup := callMessage("message_lookup", map[string]string{"idempotency_key": reviewed.Key})
	if lookup.Error != nil {
		t.Fatal("browser read lookup", lookup.Error)
	}
	raw, _ := json.Marshal(lookup.Result)
	var found manager.MessageLookup
	if json.Unmarshal(raw, &found) != nil || !found.Found || found.Message.Body != reviewed.Body {
		t.Fatal("lookup mismatch", lookup)
	}
	if retry := callMessage("message_send", reviewed); retry.Error != nil {
		t.Fatal("exact browser retry", retry.Error)
	}
	stale := reviewed
	stale.Key = "browser-stale"
	stale.Targets = append([]agents.Contact(nil), reviewed.Targets...)
	stale.Targets[0].Session = uuid.NewString()
	if response := callMessage("message_send", stale); response.Error == nil || response.Error.Code != "contact_changed" {
		t.Fatal("browser silently retargeted", response)
	}
	// Native clients retain operator inbox access; the browser restriction is local
	// to the browser context and does not narrow the CLI reviewed-send protocol.
	var native manager.Message
	if err := client.Call(ctx, home, "message_send", manager.MessageRequest{Key: "native-operator", Audience: "operator", Body: "Native inbox", Targets: []agents.Contact{}}, &native); err != nil || native.Audience != "operator" {
		t.Fatal("native inbox regression", err)
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
