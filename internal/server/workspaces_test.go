package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/workspace"
	api "github.com/inferal-oss/arm/internal/workspaceapi"
)

func nativeWorkspaceCall(t *testing.T, s *Server, op string, q, result any) {
	t.Helper()
	raw, e := json.Marshal(q)
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.dispatch(t.Context(), model.WireRequest{Operation: op, Params: raw})
	if e != nil {
		t.Fatalf("%s: %v", op, e)
	}
	raw, e = json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, result); e != nil {
		t.Fatal(e)
	}
}
func workspaceDecideTest(t *testing.T, s *Server, op workspace.Operation) workspace.Operation {
	t.Helper()
	var out workspace.Operation
	nativeWorkspaceCall(t, s, "workspace_decide", api.Decision{Workspace: op.Workspace, Key: op.Key, Revision: op.Revision, Review: op.ReviewHash, Approve: true}, &out)
	return out
}
func workspaceJoinTest(t *testing.T, d, c *Server, id, key string) (workspace.Operation, workspace.Operation) {
	t.Helper()
	var invite workspace.Invitation
	nativeWorkspaceCall(t, d, "workspace_invitation", api.Selector{Workspace: id}, &invite)
	var join workspace.Operation
	nativeWorkspaceCall(t, c, "workspace_join", api.Join{Key: key, Invitation: invite, Reason: "real pinned native workspace test"}, &join)
	join = workspaceDecideTest(t, c, join)
	var consent workspace.Consent
	nativeWorkspaceCall(t, c, "workspace_consent", api.Key{Workspace: id, Key: join.Key}, &consent)
	request := workspace.NativeAdmissionRequest{Workspace: id, Key: "admit-" + key, Consent: consent, Reason: "explicit exact candidate review"}
	var review workspace.AdmissionReview
	nativeWorkspaceCall(t, d, "workspace_admission_review", request, &review)
	var admitted workspace.Operation
	nativeWorkspaceCall(t, d, "workspace_admit", api.AdmissionCommit{Request: request, Review: review.Hash}, &admitted)
	if admitted.Phase != "completed" || admitted.Result == nil || admitted.Result.Identity != consent.Candidate {
		t.Fatal("real candidate proof not committed", admitted)
	}
	var refreshed api.RefreshResult
	nativeWorkspaceCall(t, c, "workspace_refresh", api.Selector{Workspace: id}, &refreshed)
	if refreshed.Members < 2 {
		t.Fatal("missing real directory roster", refreshed)
	}
	var current api.LookupResult
	nativeWorkspaceCall(t, c, "workspace_lookup", api.Lookup{Workspace: id, Key: join.Key}, &current)
	var finalized workspace.Operation
	nativeWorkspaceCall(t, c, "workspace_finalize", api.Finalize{Workspace: id, Key: join.Key, Revision: current.Operation.Revision}, &finalized)
	if finalized.Phase != "completed" {
		t.Fatal("candidate finalization failed", finalized)
	}
	return finalized, admitted
}
func TestWorkspaceNativeThreeNodesRealPinnedProofAndDirectory(t *testing.T) {
	a, b, as, bs := registryPair(t, mesh.Grant{Permissions: []string{"observe-host"}}, mesh.Grant{Permissions: []string{"observe-host"}})
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
	for _, r := range []struct {
		store    *mesh.Store
		identity mesh.Identity
	}{{as, cs.Config().Identity}, {cs, as.Config().Identity}} {
		if _, e = r.store.Pair(r.identity, mesh.Grant{Permissions: []string{"observe-host"}}, pair, "third private direct enrollment"); e != nil {
			t.Fatal(e)
		}
	}
	for _, r := range []struct {
		store  *mesh.Store
		target string
	}{{as, cs.Config().Identity.Host}, {cs, as.Config().Identity.Host}} {
		if _, e = r.store.Call(t.Context(), r.target, mesh.Request{Operation: "hello", Params: json.RawMessage(`{"pair":"` + pair + `"}`)}); e != nil {
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
	nativeWorkspaceCall(t, a, "workspace_create", workspace.CreateRequest{Key: "create", Name: "three nodes", Policy: workspace.DefaultPolicy(), Reason: "actual pinned workspace"}, &created)
	created = workspaceDecideTest(t, a, created)
	workspaceJoinTest(t, a, b, created.Workspace, "join-b")
	workspaceJoinTest(t, a, c, created.Workspace, "join-c")
	var refreshed api.RefreshResult
	nativeWorkspaceCall(t, b, "workspace_refresh", api.Selector{Workspace: created.Workspace}, &refreshed)
	if refreshed.Members != 3 {
		t.Fatal("not all three reported", refreshed)
	}
	records, e := b.Manager.WorkspaceRead(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	var found bool
	for _, member := range records.Trusted {
		if member.Identity.Host == cs.Config().Identity.Host && member.State == "active" {
			found = true
		}
	}
	if !found {
		t.Fatal("B did not discover C through directory")
	}
	if _, e = bs.Resolve(cs.Config().Identity.Host); e == nil {
		t.Fatal("workspace promoted C to direct B peer")
	}
	if _, e = cs.Resolve(bs.Config().Identity.Host); e == nil {
		t.Fatal("workspace promoted B to direct C peer")
	}
	if _, e = b.Manager.WorkspaceCheck(t.Context(), created.Workspace, cs.Config().Identity.Host); e != nil {
		t.Fatal("fresh exact directory membership missing", e)
	}
	// No workspace dispatcher grants appear on the normal peer RPC.
	for _, operation := range []string{"workspace_create", "workspace_decide", "workspace_prove_join", "workspace_admit", "workspace_recovery_run"} {
		if _, e = bs.Call(t.Context(), as.Config().Identity.Host, mesh.Request{Operation: operation, Params: json.RawMessage(`{}`)}); e == nil {
			t.Fatal("peer reached native workspace admin", operation)
		}
	}
}
func TestWorkspaceNativeBrowserRefusesEveryAdministrativeOperation(t *testing.T) {
	s := meshFixture(t)
	ctx := context.WithValue(t.Context(), browserContextKey{}, true)
	for _, op := range []string{"workspace_list", "workspace_create", "workspace_decide", "workspace_join", "workspace_admission_review", "workspace_admit", "workspace_refresh", "workspace_recovery_prepare_auto", "workspace_recovery_run", "workspace_retire"} {
		if _, e := s.dispatch(ctx, model.WireRequest{Operation: op, Params: json.RawMessage(`{}`)}); e == nil {
			t.Fatal("browser reached native workspace", op)
		}
	}
}
