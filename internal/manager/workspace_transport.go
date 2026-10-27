package manager

import (
	"context"
	"getarm.org/internal/workspace"
)

// WorkspaceTransport is a private closed-listener adapter, never an RPC grant.
func (m *Manager) WorkspaceTransport(ctx context.Context, id, generation, source, target, class, consent string) (workspace.TransportPermit, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, _ *workspaceBoundary, s *workspace.Store) (workspace.TransportPermit, error) {
		return s.AuthorizeTransport(ctx, id, generation, source, target, class, consent)
	})
}
