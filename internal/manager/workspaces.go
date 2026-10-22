package manager

import (
	"context"
	"github.com/yrashk/arm/internal/workspace"
)

// These are internal, native-owned lifecycle adapters. No server dispatcher,
// CLI, listener or browser/mesh operation exposes them in this checkpoint.
func (m *Manager) WorkspaceRead(ctx context.Context) (workspace.Records, error) {
	return workspaceCall(ctx, m, false, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.Records, error) {
		return w.operator.Read(ctx)
	})
}
func (m *Manager) WorkspaceLookup(ctx context.Context, id, key string) (workspace.Operation, bool, error) {
	type lookup struct {
		op    workspace.Operation
		found bool
	}
	v, e := workspaceCall(ctx, m, false, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (lookup, error) {
		op, found, e := w.operator.Lookup(ctx, id, key)
		return lookup{op, found}, e
	})
	return v.op, v.found, e
}
func (m *Manager) WorkspacePrepareCreate(ctx context.Context, q workspace.CreateRequest) (workspace.Operation, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, s *workspace.Store) (workspace.Operation, error) {
		return s.PrepareCreate(ctx, q)
	})
}
func (m *Manager) WorkspacePrepareJoin(ctx context.Context, q workspace.JoinRequest) (workspace.Operation, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, s *workspace.Store) (workspace.Operation, error) {
		return s.PrepareJoin(ctx, q)
	})
}
func (m *Manager) WorkspaceDecide(ctx context.Context, id, key string, revision int64, hash string, approve bool) (workspace.Operation, error) {
	return workspaceCall(ctx, m, approve, func(ctx context.Context, w *workspaceBoundary, s *workspace.Store) (workspace.Operation, error) {
		if !approve {
			return w.operator.Decline(ctx, id, key, revision, hash)
		}
		return s.Decide(ctx, id, key, revision, hash, true)
	})
}
func (m *Manager) WorkspaceDeny(ctx context.Context, id string, q workspace.MutationRequest) (workspace.Operation, error) {
	return workspaceCall(ctx, m, false, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.Operation, error) {
		return w.operator.Deny(ctx, id, q)
	})
}
func (m *Manager) WorkspaceRevoke(ctx context.Context, id string, q workspace.MutationRequest) (workspace.Operation, error) {
	return workspaceCall(ctx, m, false, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.Operation, error) {
		return w.operator.Revoke(ctx, id, q)
	})
}
func (m *Manager) WorkspaceRetire(ctx context.Context, id string, q workspace.MutationRequest) (workspace.Operation, error) {
	return workspaceCall(ctx, m, false, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.Operation, error) {
		return w.operator.Retire(ctx, id, q)
	})
}
func (m *Manager) WorkspaceCancelAdmission(ctx context.Context, id, source, key string, revision int64) (workspace.Operation, error) {
	return workspaceCall(ctx, m, false, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.Operation, error) {
		return w.operator.CancelAdmission(ctx, id, source, key, revision)
	})
}
func (m *Manager) WorkspaceCancelJoin(ctx context.Context, id, key string, revision int64) (workspace.Operation, error) {
	return workspaceCall(ctx, m, false, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.Operation, error) {
		return w.operator.CancelJoin(ctx, id, key, revision)
	})
}
func (m *Manager) WorkspacePrepareAdmission(ctx context.Context, p workspace.Principal, q workspace.AdmissionRequest) (workspace.Operation, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, s *workspace.Store) (workspace.Operation, error) {
		return s.PrepareAdmission(ctx, p, q)
	})
}
func (m *Manager) WorkspaceActivate(ctx context.Context, p workspace.Principal, key string, transport workspace.ProofTransport) (workspace.Operation, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.Operation, error) {
		return w.service.Activate(ctx, p, key, transport)
	})
}
func (m *Manager) WorkspaceFinalize(ctx context.Context, id, key string, revision int64) (workspace.Operation, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.Operation, error) {
		return w.runtime.Finalize(ctx, id, key, revision)
	})
}

func (m *Manager) WorkspaceIssue(ctx context.Context, q workspace.RefreshRequest) (workspace.Snapshot, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, s *workspace.Store) (workspace.Snapshot, error) {
		return s.Issue(ctx, q)
	})
}
func (m *Manager) WorkspaceBeginRefresh(ctx context.Context, id string) (workspace.RefreshRequest, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.RefreshRequest, error) {
		return w.runtime.Begin(ctx, id)
	})
}
func (m *Manager) WorkspaceAccept(ctx context.Context, directory workspace.Identity, snapshot workspace.Snapshot) error {
	_, e := workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (bool, error) {
		return true, w.runtime.Accept(ctx, directory, snapshot)
	})
	return e
}
func (m *Manager) WorkspaceProveJoin(ctx context.Context, directory workspace.Identity, c workspace.Challenge) (workspace.Proof, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, s *workspace.Store) (workspace.Proof, error) {
		return s.ProveJoin(ctx, directory, c)
	})
}
func (m *Manager) WorkspaceCheck(ctx context.Context, id, target string) (workspace.Access, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.Access, error) {
		return w.runtime.Check(ctx, id, target)
	})
}
