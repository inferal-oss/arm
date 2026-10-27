package client

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"getarm.org/internal/model"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestNamingDiagnosticsMatchOnlyCurrentResponseAndKeepExactResult(t *testing.T) {
	session := sessionFixture(t, func(ctx context.Context, conn *websocket.Conn) {
		var request model.WireRequest
		if err := wsjson.Read(ctx, conn, &request); err != nil {
			return
		}
		_ = wsjson.Write(ctx, conn, model.WireResponse{Version: 1, ID: "unrelated", Result: true, NamingDiagnostic: &model.Error{Code: "name_metadata_invalid", Message: "foreign-secret"}})
		_ = wsjson.Write(ctx, conn, model.WireResponse{Version: 1, Subscription: "foreign-stream", NamingDiagnostic: &model.Error{Code: "name_metadata_invalid", Message: "subscription-secret"}})
		_ = wsjson.Write(ctx, conn, model.WireResponse{Version: 1, ID: request.ID, Result: map[string]any{"id": "canonical-result", "capacity": json.Number("9223372036854775807")}, NamingDiagnostic: &model.Error{Code: "name_metadata_invalid", Message: "\x1b]52;c;untrusted-secret\a"}})
		if err := wsjson.Read(ctx, conn, &request); err != nil {
			return
		}
		_ = wsjson.Write(ctx, conn, model.WireResponse{Version: 1, ID: request.ID, Result: map[string]bool{"healthy": true}})
		<-ctx.Done()
	})
	var result struct {
		ID       string      `json:"id"`
		Capacity json.Number `json:"capacity"`
	}
	diagnostic, err := session.CallWithDiagnostics(t.Context(), "release", map[string]string{"id": "canonical-result"}, &result)
	if err != nil || result.ID != "canonical-result" || result.Capacity.String() != "9223372036854775807" || diagnostic == nil || diagnostic.Code != "name_metadata_invalid" {
		t.Fatal("diagnostic changed exact action result", result, diagnostic, err)
	}
	if strings.Contains(diagnostic.Message, "secret") || strings.Contains(diagnostic.Message, "\x1b") {
		t.Fatal("untrusted diagnostic text escaped normalization", diagnostic)
	}
	var healthy map[string]bool
	diagnostic, err = session.CallWithDiagnostics(t.Context(), "status", nil, &healthy)
	if err != nil || diagnostic != nil || !healthy["healthy"] {
		t.Fatal("old diagnostic crossed request boundary", healthy, diagnostic, err)
	}
}

func TestNamingDiagnosticsPreserveActionErrorsAndLegacyCall(t *testing.T) {
	session := sessionFixture(t, func(ctx context.Context, conn *websocket.Conn) {
		for i := 0; i < 2; i++ {
			var request model.WireRequest
			if wsjson.Read(ctx, conn, &request) != nil {
				return
			}
			response := model.WireResponse{Version: 1, ID: request.ID, NamingDiagnostic: &model.Error{Code: "name_metadata_unavailable", Message: strings.Repeat("private", 10000)}}
			if i == 0 {
				response.Error = &model.Error{Code: "stale_revision", Message: "review again"}
			} else {
				response.Result = true
			}
			if wsjson.Write(ctx, conn, response) != nil {
				return
			}
		}
		<-ctx.Done()
	})
	diagnostic, err := session.CallWithDiagnostics(t.Context(), "lease_action", nil, nil)
	if diagnostic == nil || diagnostic.Code != "name_metadata_unavailable" || len(diagnostic.Message) > 256 {
		t.Fatal("cosmetic diagnostic unbounded", diagnostic)
	}
	api, ok := err.(*model.Error)
	if !ok || api.Code != "stale_revision" {
		t.Fatal("cosmetic diagnostic obscured action error", err)
	}
	var result bool
	if err = session.Call(t.Context(), "release", nil, &result); err != nil || !result {
		t.Fatal("legacy Call lost successful exact result", result, err)
	}
}
