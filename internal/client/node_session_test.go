package client

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/inferal-oss/arm/internal/model"
)

func TestNodeReportRequiresExactAuthorityAndDestinationScope(t *testing.T) {
	selection := model.NodeSelection{Host: strings.Repeat("a", 32), Source: strings.Repeat("b", 32)}
	report := model.NodeContextReport{ReadOnly: true, Context: model.NodeContext{Host: selection.Host, Source: selection.Source, Pair: strings.Repeat("c", 64), Pin: strings.Repeat("d", 64), LocalRevision: 1, RemoteRevision: 2}}
	report.AccessScope = &model.NodeAccessScope{Source: selection.Source, Target: selection.Host, Pair: report.Context.Pair, Revision: 2}
	if err := validateNodeReport(selection, report); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*model.NodeContextReport){
		func(r *model.NodeContextReport) { r.Context.Host = selection.Source },
		func(r *model.NodeContextReport) { r.Context.Local = true },
		func(r *model.NodeContextReport) { r.ReadOnly = false },
		func(r *model.NodeContextReport) { r.Context.RemoteRevision++ },
		func(r *model.NodeContextReport) { r.AccessScope = nil },
	} {
		copy := report
		change(&copy)
		if err := validateNodeReport(selection, copy); err == nil {
			t.Fatal("unsafe context accepted", copy)
		}
	}
}

func TestSessionRoutesOnlyCurrentWorkflowInvalidations(t *testing.T) {
	s := sessionFixture(t, func(ctx context.Context, c *websocket.Conn) {
		var req model.WireRequest
		if err := wsjson.Read(ctx, c, &req); err != nil {
			t.Error(err)
			return
		}
		wsjson.Write(ctx, c, model.WireResponse{Version: 1, ID: req.ID, Result: map[string]string{"subscription": "mesh_operator:current"}})
		wsjson.Write(ctx, c, model.WireResponse{Version: 1, Subscription: "mesh_operator:stale", Result: json.RawMessage(`{"key":"wrong","revision":9}`)})
		wsjson.Write(ctx, c, model.WireResponse{Version: 1, Subscription: "mesh_operator:current", Result: json.RawMessage(`{"key":"expected","revision":2}`)})
		<-ctx.Done()
	})
	if err := s.Call(t.Context(), "mesh_operator_watch", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case change := <-s.WorkflowChanges():
		if change.Key != "expected" || change.Revision != 2 {
			t.Fatal(change)
		}
	case <-time.After(time.Second):
		t.Fatal("durable workflow invalidation lost")
	}
}
