package server

import (
	"context"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/routing"
	"github.com/inferal-oss/arm/internal/workspace"
	api "github.com/inferal-oss/arm/internal/workspaceapi"
	route "github.com/inferal-oss/arm/internal/workspaceroute"
)

func (s *Server) workspaceRefresh(parent context.Context, id string) (api.RefreshResult, error) {
	ctx, done, e := s.beginWorkspaceCall(parent)
	if e != nil {
		return api.RefreshResult{}, e
	}
	defer done()
	g, e := s.graphState()
	if e != nil {
		return api.RefreshResult{}, e
	}
	return g.coalesce(ctx, id, func(ctx context.Context) (api.RefreshResult, error) { return s.workspaceGraphRefresh(ctx, id, g) })
}
func (s *Server) workspaceGraphRefresh(ctx context.Context, id string, g *workspaceGraphState) (api.RefreshResult, error) {
	records, e := s.Manager.WorkspaceRead(ctx)
	if e != nil {
		return api.RefreshResult{}, e
	}
	root, e := workspaceRoot(records, id)
	if e != nil {
		return api.RefreshResult{}, e
	}
	service, e := s.workspaceService()
	if e != nil {
		return api.RefreshResult{}, e
	}
	config := service.store.Config() // no trust/store/SQL lock held across capture
	neighbors := graphNeighbors(config, records, root)
	if root.Role == "directory" {
		bundle, next, e := s.Manager.WorkspaceIssueLocalGraph(ctx, id, neighbors, g.observations(id))
		if e != nil {
			return api.RefreshResult{}, e
		}
		g.publish(id, next)
		return graphRefreshResult(bundle.Membership, bundle.Snapshot.Hash, bundle.Snapshot.Revision, bundle.Snapshot.ValidUntil), nil
	}
	ticket, e := s.Manager.WorkspaceBeginRefresh(ctx, id)
	if e != nil {
		return api.RefreshResult{}, e
	}
	defer s.Manager.WorkspaceAbandonRefresh(context.Background(), ticket.Nonce)
	report, e := s.Manager.WorkspaceGraphReport(ctx, ticket, neighbors)
	if e != nil {
		return api.RefreshResult{}, e
	}
	conn, o, a, e := s.workspaceRouteDial(ctx, id, root.Directory.Host, route.ClassDirectory)
	if e != nil {
		return api.RefreshResult{}, e
	}
	defer conn.Close()
	q := routedWireRequest(o, route.OperationRefresh)
	q.Refresh = &ticket
	q.Report = &report
	if e = route.WriteRequest(conn, o, q); e != nil {
		return api.RefreshResult{}, e
	}
	bundle, e := route.ReadDirectoryBundle(conn, q)
	if e != nil {
		return api.RefreshResult{}, model.Err("invalid_snapshot", e.Error())
	}
	if e = s.Manager.WorkspaceAcceptGraphDirectoryPath(ctx, a.Destination, bundle, o.Path); e != nil {
		return api.RefreshResult{}, e
	}
	return graphRefreshResult(bundle.Membership, bundle.Snapshot.Hash, bundle.Snapshot.Revision, bundle.Snapshot.ValidUntil), nil
}

func graphRefreshResult(m workspace.Snapshot, hash string, revision int64, until time.Time) api.RefreshResult {
	return api.RefreshResult{Workspace: m.Workspace, Revision: m.Revision, Members: len(m.Members), ValidUntil: m.ValidUntil, GraphHash: hash, GraphRevision: revision, GraphValidUntil: until}
}

// graphDirectoryReplies runs only after the caller's pinned inner-TLS source
// was matched by the closed final handler. Preflight encodes bounded replies
// inside SQL, while actual network writes happen ONLY after commit/revalidation.
func (s *Server) graphDirectoryReplies(ctx context.Context, q route.Request) ([]route.Reply, error) {
	if q.Refresh == nil || q.Report == nil {
		return nil, model.Err("invalid_request", "complete graph refresh request required")
	}
	service, e := s.workspaceService()
	if e != nil {
		return nil, e
	}
	records, e := s.Manager.WorkspaceRead(ctx)
	if e != nil {
		return nil, e
	}
	root, e := workspaceRoot(records, q.Workspace)
	if e != nil {
		return nil, e
	}
	neighbors := graphNeighbors(service.store.Config(), records, root)
	g, e := s.graphState()
	if e != nil {
		return nil, e
	}
	var replies []route.Reply
	_, next, e := s.Manager.WorkspaceIssueGraphChecked(ctx, *q.Refresh, *q.Report, neighbors, g.observations(q.Workspace), func(b routing.GraphBundle) error {
		var e error
		replies, e = route.DirectoryReplies(q, b)
		if e != nil {
			return model.Err("response_too_large", e.Error())
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	g.publish(q.Workspace, next)
	return replies, nil
}
