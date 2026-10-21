package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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

func TestAgentMessageBrowserCannotUseNativeSenderOperations(t *testing.T) {
	home := backend(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	register := func(id string) agents.Contact {
		var result agents.Contact
		err := client.Call(ctx, home, "register", agents.Contact{ID: id, Adapter: "generic", Delivery: "poll", Session: uuid.NewString(), CWD: t.TempDir()}, &result)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	source, target := register("native-source"), register("native-target")
	request := manager.AgentMessageRequest{ID: source.ID, Session: source.Session, Registration: source.Registration, Key: "native-operation", Audience: target.ID, Targets: []agents.Contact{target}, Body: "native sender permitted"}
	var original manager.Message
	if err := client.Call(ctx, home, "agent_message", request, &original); err != nil {
		t.Fatal("native authenticated endpoint rejected agent send", err)
	}
	var lookup manager.MessageLookup
	if err := client.Call(ctx, home, "agent_message_lookup", request, &lookup); err != nil || !lookup.Found || lookup.Message.ID != original.ID {
		t.Fatalf("native lookup %+v %v", lookup, err)
	}
	var link model.BrowserLink
	if err := client.Call(ctx, home, "browser_ticket", nil, &link); err != nil {
		t.Fatal(err)
	}
	base, ticket, ok := strings.Cut(link.URL, "/#connect=")
	if !ok {
		t.Fatal("missing bootstrap ticket")
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
	if response.StatusCode != 200 || json.Unmarshal(data, &session) != nil {
		t.Fatalf("session %d %s", response.StatusCode, data)
	}
	endpoint, _ := local.ReadEndpoint(home)
	conn, _, err := websocket.Dial(ctx, endpoint.URL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{base}}, Subprotocols: []string{"arm.v1", "arm.auth." + session.Token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	request.Key = "browser-must-not-create"
	raw, _ := json.Marshal(request)
	for _, operation := range []string{"agent_message", "agent_message_lookup"} {
		if err := wsjson.Write(ctx, conn, model.WireRequest{Version: 1, ID: operation, Operation: operation, Params: raw}); err != nil {
			t.Fatal(err)
		}
		var result model.WireResponse
		if err := wsjson.Read(ctx, conn, &result); err != nil {
			t.Fatal(err)
		}
		if result.ID != operation || result.Error == nil || result.Error.Code != "forbidden" || result.Result != nil {
			t.Fatalf("browser reached native agent endpoint %+v", result)
		}
	}
	if err := client.Call(ctx, home, "agent_message_lookup", request, &lookup); err != nil || lookup.Found {
		t.Fatalf("browser rejection mutated intent %+v %v", lookup, err)
	}
}
