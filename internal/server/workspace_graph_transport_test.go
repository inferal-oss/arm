package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/inferal-oss/arm/internal/model"
	"reflect"
	"testing"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/routing"
	"github.com/inferal-oss/arm/internal/workspace"
	api "github.com/inferal-oss/arm/internal/workspaceapi"
	route "github.com/inferal-oss/arm/internal/workspaceroute"
)

func TestGraphWholeResponsePreflightRollsBackActualDirectoryWriter(t *testing.T) {
	a, b, _, _ := registryPair(t, mesh.Grant{Permissions: []string{"observe-host"}}, mesh.Grant{Permissions: []string{"observe-host"}})
	var created workspace.Operation
	nativeWorkspaceCall(t, a, "workspace_create", workspace.CreateRequest{Key: "create", Name: "actual complete graph preflight", Policy: workspace.DefaultPolicy(), Reason: "rollback directory floor and graph together"}, &created)
	created = workspaceDecideTest(t, a, created)
	workspaceJoinTest(t, a, b, created.Workspace, "join")
	q, e := b.Manager.WorkspaceBeginRefresh(t.Context(), created.Workspace)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Manager.WorkspaceAbandonRefresh(t.Context(), q.Nonce)
	records, e := b.Manager.WorkspaceRead(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	root, e := workspaceRoot(records, created.Workspace)
	if e != nil {
		t.Fatal(e)
	}
	service, e := b.workspaceService()
	if e != nil {
		t.Fatal(e)
	}
	report, e := b.Manager.WorkspaceGraphReport(t.Context(), q, graphNeighbors(service.store.Config(), records, root))
	if e != nil {
		t.Fatal(e)
	}
	before, e := a.Manager.WorkspaceRead(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	beforeGraph, e := a.Manager.WorkspaceRoutingRead(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	sentinel := errors.New("complete response exceeds negotiated transport budget")
	_, _, e = a.Manager.WorkspaceIssueGraphChecked(t.Context(), q, report, nil, nil, func(bundle routing.GraphBundle) error {
		request := route.Request{Version: route.Version, Route: "12345678123456781234567812345678", Workspace: created.Workspace, Generation: q.Source.Generation, Source: q.Source.Host, Target: bundle.Membership.Directory.Host, Operation: route.OperationRefresh, Refresh: &q, Report: &report}
		if _, e := route.DirectoryReplies(request, bundle); e != nil {
			t.Errorf("bounded ordinary response failed preflight: %v", e)
		}
		return sentinel
	})
	if !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	after, e := a.Manager.WorkspaceRead(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	afterGraph, e := a.Manager.WorkspaceRoutingRead(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeGraph, afterGraph) {
		t.Fatal("failed whole-response preflight changed floors, source reports, counters or graph authority")
	}
	// Read-only local inspection does not silently refresh or mutate either ledger.
	raw, _ := json.Marshal(api.GraphRead{Workspace: created.Workspace, Section: "nodes", Offset: 0, Limit: 1})
	if _, e = a.workspaceGraphRead(t.Context(), raw); e != nil {
		t.Fatal(e)
	}
	after, e = a.Manager.WorkspaceRead(t.Context())
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("graph read mutated directory evidence", e)
	}
}

func TestGraphNativeReadRefusesBrowserAndNeverInitializesLedger(t *testing.T) {
	s := meshFixture(t)
	q, _ := json.Marshal(api.GraphRead{Workspace: "12345678123456781234567812345678", Section: "nodes", Offset: 0, Limit: 1})
	if _, e := s.dispatch(context.WithValue(t.Context(), browserContextKey{}, true), model.WireRequest{Operation: "workspace_graph_read", Params: q}); e == nil {
		t.Fatal("browser gained graph native authority")
	}
	if _, e := s.workspaceGraphRead(t.Context(), q); e == nil {
		t.Fatal("missing workspace became a graph")
	}
	v, e := s.Manager.WorkspaceRoutingRead(t.Context())
	if e != nil || v.Initialized {
		t.Fatal("read initialized routing graph", e)
	}
	w, e := s.Manager.WorkspaceRead(t.Context())
	if e != nil || w.Initialized {
		t.Fatal("read initialized workspace", e)
	}
}
