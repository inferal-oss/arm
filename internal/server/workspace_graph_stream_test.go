package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/routing"
	"github.com/inferal-oss/arm/internal/workspace"
	route "github.com/inferal-oss/arm/internal/workspaceroute"
)

func graphStreamFixture(t *testing.T) (*Server, *Server, *Server, string) {
	t.Helper()
	a, b, as, _ := registryPair(t, mesh.Grant{Permissions: []string{"observe-host"}}, mesh.Grant{Permissions: []string{"observe-host"}})
	c := meshFixture(t)
	meta, e := c.nodeMetadata()
	if e != nil {
		t.Fatal(e)
	}
	address := meshAddress(t)
	cs, e := mesh.Init(c.home, meta.Host, "third", address, address)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.startMesh(cs); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(c.closeMesh)
	pair := strings.Repeat("f", 64)
	for _, row := range []struct {
		store    *mesh.Store
		identity mesh.Identity
	}{{as, cs.Config().Identity}, {cs, as.Config().Identity}} {
		if _, e = row.store.Pair(row.identity, mesh.Grant{Permissions: []string{"observe-host"}}, pair, "private graph stream fixture"); e != nil {
			t.Fatal(e)
		}
	}
	for _, row := range []struct {
		store  *mesh.Store
		target string
	}{{as, cs.Config().Identity.Host}, {cs, as.Config().Identity.Host}} {
		if _, e = row.store.Call(t.Context(), row.target, mesh.Request{Operation: "hello", Params: json.RawMessage(`{"pair":"` + pair + `"}`)}); e != nil {
			t.Fatal(e)
		}
	}
	if e = as.Activate(cs.Config().Identity.Host, pair); e != nil {
		t.Fatal(e)
	}
	if e = cs.Activate(as.Config().Identity.Host, pair); e != nil {
		t.Fatal(e)
	}
	var created workspace.Operation
	nativeWorkspaceCall(t, a, "workspace_create", workspace.CreateRequest{Key: "create", Name: "graph stream publication", Policy: workspace.DefaultPolicy(), Reason: "real relayed single request across refresh"}, &created)
	created = workspaceDecideTest(t, a, created)
	workspaceJoinTest(t, a, b, created.Workspace, "join-b")
	workspaceJoinTest(t, a, c, created.Workspace, "join-c")
	if _, e = b.workspaceRefresh(t.Context(), created.Workspace); e != nil {
		t.Fatal(e)
	}
	return a, b, c, created.Workspace
}

func TestGraphRefreshPreservesAuthenticatedSingleMessageStream(t *testing.T) {
	graphRefreshSingleMessageStream(t, false)
}
func TestGraphRefreshPreservesDispatchedSingleMessageReceipt(t *testing.T) {
	graphRefreshSingleMessageStream(t, true)
}
func graphRefreshSingleMessageStream(t *testing.T, dispatched bool) {
	_, b, c, id := graphStreamFixture(t)
	dest, e := c.nodeMetadata()
	if e != nil {
		t.Fatal(e)
	}
	before, e := b.Manager.WorkspaceGraphHints(t.Context(), id, dest.Host, false)
	if e != nil || len(before.Paths) == 0 {
		t.Fatal(before, e)
	}
	out, _, e := b.Manager.WorkspaceRoutingPrepare(t.Context(), routing.Intent{Workspace: id, Key: "one-original-request", Target: dest.Host, Body: "do not replay", TTL: time.Minute})
	if e != nil {
		t.Fatal(e)
	}
	// The real final TLS handler is paused before its one ReadRequest. No request
	// bytes or receipt are fabricated; graph refresh has a deterministic window.
	targetService, e := c.workspaceService()
	if e != nil {
		t.Fatal(e)
	}
	original := targetService.routed.Final
	entered, release := make(chan struct{}), make(chan struct{})
	targetService.routed.Final = func(ctx context.Context, outer route.Outer, o route.Open, p route.Permit, inner *tls.Conn) {
		if o.Class == route.ClassMessage {
			close(entered)
			<-release
		}
		original(ctx, outer, o, p, inner)
	}
	defer func() { targetService.routed.Final = original }()
	conn, o, _, e := b.workspaceRouteDial(t.Context(), id, dest.Host, route.ClassMessage)
	if e != nil {
		close(release)
		t.Fatal(e)
	}
	defer conn.Close()
	<-entered
	q := routedWireRequest(o, route.OperationMessage)
	q.Envelope = &out.Envelope
	if dispatched {
		if e = route.WriteRequest(conn, o, q); e != nil {
			close(release)
			t.Fatal(e)
		}
	}
	if _, e = b.workspaceRefresh(t.Context(), id); e != nil {
		close(release)
		t.Fatal(e)
	}
	after, e := b.Manager.WorkspaceGraphHints(t.Context(), id, dest.Host, false)
	if e != nil || before.Snapshot.Hash == after.Snapshot.Hash {
		close(release)
		t.Fatal("actual refresh did not replace graph", e)
	}
	// Exercise real carrier periodic authorization after the refreshed hash has
	// replaced the selected observation, while the original TLS stream is open.
	time.Sleep(1200 * time.Millisecond)
	close(release)
	if !dispatched {
		if e = route.WriteRequest(conn, o, q); e != nil {
			t.Fatal("benign graph refresh killed original admitted stream:", e)
		}
	}
	reply, e := route.ReadReply(conn, q)
	if e != nil || reply.Receipt == nil || reply.Failure != nil {
		t.Fatal("original request did not receive its exact receipt:", reply, e)
	}
	records, e := c.Manager.WorkspaceRoutingRead(t.Context())
	if e != nil || len(records.Inbox) != 1 || records.Inbox[0].Envelope.ID != out.Envelope.ID {
		t.Fatal("single original inbox evidence lost or duplicated", e)
	}
}

func TestGraphFirstApplicationWriteRefusesRemovedSelectedPath(t *testing.T) {
	a, b, c, id := graphStreamFixture(t)
	dest, e := c.nodeMetadata()
	if e != nil {
		t.Fatal(e)
	}
	out, _, e := b.Manager.WorkspaceRoutingPrepare(t.Context(), routing.Intent{Workspace: id, Key: "removed-path-no-dispatch", Target: dest.Host, Body: "must not execute", TTL: time.Minute})
	if e != nil {
		t.Fatal(e)
	}
	access, e := b.Manager.WorkspaceRouteAccess(t.Context(), id, out.Envelope.Origin, dest.Host, workspace.RouteMessage)
	if e != nil {
		t.Fatal(e)
	}
	candidates := b.graphRouteCandidates(t.Context(), access, route.ClassMessage)
	if len(candidates) == 0 || candidates[0].graph == "" {
		t.Fatal("fixture did not select an actual graph path")
	}
	conn, o, _, e := b.workspaceRouteDial(t.Context(), id, dest.Host, route.ClassMessage)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	// A lawful sparse directory report supersedes the reachability path while
	// leaving all membership/deny/pin authority intact. This is an actual typed
	// SQL producer, not an edited in-memory graph or a fabricated TLS proof.
	q, e := b.Manager.WorkspaceBeginRefresh(t.Context(), id)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Manager.WorkspaceAbandonRefresh(t.Context(), q.Nonce)
	records, e := b.Manager.WorkspaceRead(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	root, e := workspaceRoot(records, id)
	if e != nil {
		t.Fatal(e)
	}
	bs, e := b.workspaceService()
	if e != nil {
		t.Fatal(e)
	}
	report, e := b.Manager.WorkspaceGraphReport(t.Context(), q, graphNeighbors(bs.store.Config(), records, root))
	if e != nil {
		t.Fatal(e)
	}
	gs, e := a.graphState()
	if e != nil {
		t.Fatal(e)
	}
	bundle, live, e := a.Manager.WorkspaceIssueGraph(t.Context(), q, report, nil, gs.observations(id))
	if e != nil {
		t.Fatal(e)
	}
	gs.publish(id, live)
	if e = b.Manager.WorkspaceAcceptGraphDirectoryPath(t.Context(), bundle.Membership.Directory, bundle, []string{out.Envelope.Origin, bundle.Membership.Directory.Host}); e != nil {
		t.Fatal(e)
	}
	if _, e = b.Manager.WorkspaceRouteAccess(t.Context(), id, out.Envelope.Origin, dest.Host, workspace.RouteMessage); e != nil {
		t.Fatal("membership changed instead of reachability", e)
	}
	hints, e := b.Manager.WorkspaceGraphHints(t.Context(), id, dest.Host, false)
	if e != nil || len(hints.Paths) != 0 {
		t.Fatal("sparse graph did not remove selected path", hints, e)
	}
	request := routedWireRequest(o, route.OperationMessage)
	request.Envelope = &out.Envelope
	if e = route.WriteRequest(conn, o, request); e == nil {
		t.Fatal("first application bytes escaped removed selected graph path")
	}
	var api *model.Error
	if !errors.As(e, &api) || api.Code != "graph_stale" {
		t.Fatal("expected explicit pre-write graph rejection, got", e)
	}
	delivered, e := c.Manager.WorkspaceRoutingRead(t.Context())
	if e != nil || len(delivered.Inbox) != 0 {
		t.Fatal("rejected pre-write request executed", e)
	}
}
