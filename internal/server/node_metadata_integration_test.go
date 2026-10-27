package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/inferal-oss/arm/internal/client"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

func TestNodeMetadataActualNativeAndBrowserReadIdenticalSchema(t *testing.T) {
	home := backend(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var native model.NodeMetadata
	if err := client.Call(ctx, home, "node_metadata", nil, &native); err != nil {
		t.Fatal(err)
	}
	var link model.BrowserLink
	if err := client.Call(ctx, home, "browser_ticket", nil, &link); err != nil {
		t.Fatal(err)
	}
	base, ticket, ok := strings.Cut(link.URL, "/#connect=")
	if !ok {
		t.Fatal("missing fixture ticket")
	}
	exchange, _ := http.NewRequestWithContext(ctx, "POST", base+"/v1/browser/session", strings.NewReader(`{"ticket":"`+ticket+`"}`))
	exchange.Header.Set("Origin", base)
	exchange.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(exchange)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	var session struct {
		Token string `json:"token"`
	}
	if response.StatusCode != 200 || json.Unmarshal(data, &session) != nil || session.Token == "" {
		t.Fatal("fixture session exchange failed", response.StatusCode)
	}
	endpoint, _ := local.ReadEndpoint(home)
	conn, _, err := websocket.Dial(ctx, endpoint.URL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{base}}, Subprotocols: []string{"arm.v1", "arm.auth." + session.Token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err = wsjson.Write(ctx, conn, model.WireRequest{Version: 1, ID: "node", Operation: "node_metadata", Params: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	var wire model.WireResponse
	if err = wsjson.Read(ctx, conn, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Error != nil || wire.ID != "node" {
		t.Fatal(wire)
	}
	raw, _ := json.Marshal(wire.Result)
	var browser model.NodeMetadata
	if err = json.Unmarshal(raw, &browser); err != nil {
		t.Fatal(err)
	}
	if native.Host != browser.Host || native.SchemaVersion != browser.SchemaVersion || native.Hostname != browser.Hostname || native.Platform != browser.Platform || native.Build != browser.Build || !reflect.DeepEqual(native.Capabilities, browser.Capabilities) || browser.Inventory.LogicalCPUs < 1 {
		t.Fatal(native, browser)
	}
	// Local browser access still cannot forward an arbitrary peer RPC. The later
	// node-window coordinator needs its own closed read routing contract.
	if err = wsjson.Write(ctx, conn, model.WireRequest{Version: 1, ID: "forbidden", Operation: "mesh_dispatch", Params: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err = wsjson.Read(ctx, conn, &wire); err != nil || wire.Error == nil || wire.Error.Code != "forbidden" {
		t.Fatal(wire, err)
	}
}
