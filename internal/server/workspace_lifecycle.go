package server

import (
	"context"

	"github.com/yrashk/arm/internal/model"
)

// Native workspace workflows own their complete transport interval, including
// the gap between individual manager transactions. Shutdown cancels and joins
// this interval before SQLite closes; there is no late proof/finalize callback.
func (s *Server) beginWorkspaceCall(parent context.Context) (context.Context, func(), error) {
	s.workspaceStreamsMu.Lock()
	if s.workspaceCallsClosed {
		s.workspaceStreamsMu.Unlock()
		return nil, nil, model.Err("backend_stopping", "workspace workflows are closing")
	}
	if s.workspaceCallsContext == nil {
		life := s.lifecycleContext
		if life == nil {
			life = context.Background()
		}
		s.workspaceCallsContext, s.workspaceCallsCancel = context.WithCancel(life)
	}
	ctx, cancel := context.WithCancel(s.workspaceCallsContext)
	s.workspaceCalls.Add(1)
	s.workspaceStreamsMu.Unlock()
	stop := context.AfterFunc(parent, cancel)
	return ctx, func() { stop(); cancel(); s.workspaceCalls.Done() }, nil
}
