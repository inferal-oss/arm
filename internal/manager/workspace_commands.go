package manager

import (
	"context"

	"getarm.org/internal/workspace"
)

// Native operator and closed transport adapters only. The server must project
// native replies and must never expose asserted TLS identity/accept methods as
// browser, selected-node, direct-mesh or workspace protocol RPC operations.
func (m *Manager) WorkspaceInvitation(ctx context.Context, id string) (workspace.Invitation, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, _ *workspaceBoundary, s *workspace.Store) (workspace.Invitation, error) {
		return s.Invitation(ctx, id)
	})
}
func (m *Manager) WorkspaceConsent(ctx context.Context, id, key string) (workspace.Consent, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, _ *workspaceBoundary, s *workspace.Store) (workspace.Consent, error) {
		return s.Consent(ctx, id, key)
	})
}
func (m *Manager) WorkspacePrepareRecovery(ctx context.Context, kind string, q workspace.RecoveryRequest) (workspace.Operation, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, _ *workspaceBoundary, s *workspace.Store) (workspace.Operation, error) {
		return s.PrepareRecovery(ctx, kind, q)
	})
}
func (m *Manager) WorkspaceDecideRecovery(ctx context.Context, id, key string, revision int64, review string, approve bool) (workspace.Operation, error) {
	return workspaceCall(ctx, m, approve, func(ctx context.Context, w *workspaceBoundary, s *workspace.Store) (workspace.Operation, error) {
		if !approve {
			return w.operator.DeclineRecovery(ctx, id, key, revision, review)
		}
		return s.DecideRecovery(ctx, id, key, revision, review, true)
	})
}
func (m *Manager) WorkspaceCancelRecovery(ctx context.Context, id, key string, revision int64) (workspace.Operation, error) {
	return workspaceCall(ctx, m, false, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.Operation, error) {
		return w.operator.CancelRecovery(ctx, id, key, revision)
	})
}
func (m *Manager) WorkspaceBeginRecoveryProof(ctx context.Context, id, key string) (workspace.Challenge, workspace.Operation, error) {
	type begin struct {
		challenge workspace.Challenge
		operation workspace.Operation
	}
	v, e := workspaceCall(ctx, m, true, func(ctx context.Context, _ *workspaceBoundary, s *workspace.Store) (begin, error) {
		c, o, e := s.BeginRecoveryProof(ctx, id, key)
		return begin{c, o}, e
	})
	return v.challenge, v.operation, e
}
func (m *Manager) WorkspaceCompleteRecoveryProof(ctx context.Context, id, key string, revision int64, proof workspace.Proof) (workspace.Operation, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, _ *workspaceBoundary, s *workspace.Store) (workspace.Operation, error) {
		return s.CompleteRecoveryProof(ctx, id, key, revision, proof)
	})
}
func (m *Manager) WorkspaceRecordRecoveryFailure(ctx context.Context, id, key string, revision int64) error {
	_, e := workspaceCall(ctx, m, true, func(ctx context.Context, _ *workspaceBoundary, s *workspace.Store) (bool, error) {
		return true, s.RecordRecoveryFailure(ctx, id, key, revision)
	})
	return e
}
func (m *Manager) WorkspaceReconcileAdmission(ctx context.Context, id, key string, revision int64) (workspace.Operation, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, _ *workspaceBoundary, s *workspace.Store) (workspace.Operation, error) {
		return s.ReconcileAdmissionRecovery(ctx, id, key, revision)
	})
}
func (m *Manager) WorkspaceOwnAdmissionStatus(ctx context.Context, candidate workspace.Identity, q workspace.AdmissionStatusRequest) (workspace.AdmissionStatus, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, _ *workspaceBoundary, s *workspace.Store) (workspace.AdmissionStatus, error) {
		return s.OwnAdmissionStatus(ctx, candidate, q)
	})
}
func (m *Manager) WorkspaceBeginRecoveryStatus(ctx context.Context, id, key string) (workspace.AdmissionStatusRequest, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.AdmissionStatusRequest, error) {
		return w.runtime.BeginRecoveryStatus(ctx, id, key)
	})
}
func (m *Manager) WorkspaceAcceptRecoveryStatus(ctx context.Context, directory workspace.Identity, status workspace.AdmissionStatus) error {
	_, e := workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (bool, error) {
		return true, w.runtime.AcceptRecoveryStatus(ctx, directory, status)
	})
	return e
}
func (m *Manager) WorkspaceRecoveryStatus(ctx context.Context, id, key string) (workspace.AdmissionStatus, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.AdmissionStatus, error) {
		return w.runtime.RecoveryStatus(id, key)
	})
}
func (m *Manager) WorkspaceFinalizeRecovery(ctx context.Context, id, key string, revision int64) (workspace.Operation, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.Operation, error) {
		return w.runtime.FinalizeRecovery(ctx, id, key, revision)
	})
}
func (m *Manager) WorkspaceAdmissionReview(ctx context.Context, q workspace.NativeAdmissionRequest) (workspace.AdmissionReview, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, _ *workspaceBoundary, s *workspace.Store) (workspace.AdmissionReview, error) {
		return s.ReviewNativeAdmission(ctx, q)
	})
}
func (m *Manager) WorkspaceAdmitReviewed(ctx context.Context, q workspace.NativeAdmissionRequest, review string) (workspace.Operation, bool, error) {
	type result struct {
		op      workspace.Operation
		created bool
	}
	v, e := workspaceCall(ctx, m, true, func(ctx context.Context, _ *workspaceBoundary, s *workspace.Store) (result, error) {
		o, created, e := s.PrepareNativeAdmission(ctx, q, review)
		return result{o, created}, e
	})
	return v.op, v.created, e
}
func (m *Manager) WorkspaceAbandonRefresh(ctx context.Context, nonce string) {
	_, _ = workspaceCall(ctx, m, false, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (bool, error) {
		if w.runtime != nil {
			w.runtime.Abandon(nonce)
		}
		return true, nil
	})
}
