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
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

func nativeNodeSocket(t *testing.T, s *Server) (*websocket.Conn, context.Context) {
	t.Helper()
	s.token = local.ID() + local.ID()
	h := httptest.NewServer(http.HandlerFunc(s.ws))
	t.Cleanup(h.Close)
	s.authority = strings.TrimPrefix(h.URL, "http://")
	ctx, stop := context.WithTimeout(t.Context(), 12*time.Second)
	t.Cleanup(stop)
	c, _, err := websocket.Dial(ctx, strings.Replace(h.URL, "http://", "ws://", 1), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + s.token}, "Arm-Node-Context": {"v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c, ctx
}

func nativeNodeRequest(t *testing.T, ctx context.Context, c *websocket.Conn, operation string, params any) model.WireResponse {
	t.Helper()
	raw, _ := json.Marshal(params)
	id := local.ID()
	if err := wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: id, Operation: operation, Params: raw}); err != nil {
		t.Fatal(err)
	}
	var response model.WireResponse
	if err := wsjson.Read(ctx, c, &response); err != nil || response.ID != id || response.Version != 1 {
		t.Fatal("native response mismatch", response, err)
	}
	return response
}

func TestNativeNodeHandshakeCannotExecuteFirstLocalOperation(t *testing.T) {
	s := meshFixture(t)
	c, ctx := nativeNodeSocket(t, s)
	response := nativeNodeRequest(t, ctx, c, "acquire", model.Request{Resources: model.Resources{CPU: 1}, Reason: "must never execute", TTL: time.Minute})
	if response.Error == nil || response.Error.Code != "protocol_error" {
		t.Fatal("ordinary first frame admitted", response)
	}
	status, err := s.Manager.Status()
	if err != nil || len(status.Leases) != 0 {
		t.Fatal("handshake ran local work", status, err)
	}
	if _, _, err := c.Read(ctx); err == nil {
		t.Fatal("failed negotiation left socket usable")
	}
}

func TestNativeNodeSessionClosedReadScopeAndLiveRevocation(t *testing.T) {
	s, _, store, _, bound := scopedRegistry(t)
	c, ctx := nativeNodeSocket(t, s)
	response := nativeNodeRequest(t, ctx, c, "node_context_open", model.NodeSelection{Host: bound.Host, Source: bound.Source})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	raw, _ := json.Marshal(response.Result)
	var report model.NodeContextReport
	if json.Unmarshal(raw, &report) != nil || report.Context.Host != bound.Host || report.Context.Source != bound.Source || !report.ReadOnly || report.AccessScope == nil {
		t.Fatal("bound context missing", string(raw))
	}
	for _, operation := range []string{"subscribe", "monitor_subscribe", "monitor_unsubscribe", "mesh_operator_watch", "node_context_open", "node_list", "mesh_dispatch", "messages", "agents", "pool_list", "database_stats", "acquire", "lease_action"} {
		response = nativeNodeRequest(t, ctx, c, operation, map[string]any{})
		if response.Error == nil || response.Event != nil || response.Telemetry != nil || response.Subscription != "" {
			t.Fatalf("scoped session escaped through %s: %+v", operation, response)
		}
	}
	response = nativeNodeRequest(t, ctx, c, "status", map[string]any{})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	raw, _ = json.Marshal(response.Result)
	var host struct {
		Host string `json:"host"`
	}
	json.Unmarshal(raw, &host)
	if host.Host != bound.Host {
		t.Fatal("remote status fell back to local", string(raw))
	}
	if err := store.Revoke(bound.Host, "native scope regression"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatal("revoked idle scoped connection did not close", err)
	}
}

func TestNativeNodeFirstFrameRequiresExactSourceAndTarget(t *testing.T) {
	s := meshFixture(t)
	status, _ := s.Manager.Status()
	for _, q := range []model.NodeSelection{
		{Host: status.Host, Source: strings.Repeat("f", 32)},
		{Host: "localhost", Source: status.Host},
		{Host: strings.Repeat("f", 32), Source: status.Host},
		{Host: status.Host},
	} {
		raw, _ := json.Marshal(q)
		if _, _, err := s.nativeNodeFirstFrame(t.Context(), model.WireRequest{Version: 1, ID: "open", Operation: "node_context_open", Params: raw}); err == nil {
			t.Fatal("unknown or changed authority bound", q)
		}
	}
}
