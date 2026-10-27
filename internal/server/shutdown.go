package server

import (
	"context"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func (s *Server) requestShutdown(request model.ShutdownRequest) (model.ShutdownReport, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.lifecycleClosing {
		return model.ShutdownReport{}, model.Err("backend_stopping", "backend is shutting down")
	}
	r, err := s.Manager.BeginShutdown(request)
	if err != nil && !s.Manager.ShutdownActive() {
		return r, err
	}
	s.startShutdownWorkerLocked()
	return r, err
}

func (s *Server) startShutdownWorker() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if !s.lifecycleClosing {
		s.startShutdownWorkerLocked()
	}
}

func (s *Server) startShutdownWorkerLocked() {
	s.shutdownWorker.Do(func() {
		s.shutdownWG.Go(func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			ctx := s.lifecycleContext
			if ctx == nil {
				ctx = context.Background()
			}
			if traceStopWorkerPause(s, ctx) {
				return
			}
			for {
				report, err := s.Manager.AdvanceShutdown()
				if err == nil && report.Phase == "ready" && s.nodeStopDrainReady(report) == nil {
					s.admission.Lock()
					s.stopping = true
					s.admission.Unlock()
					// Allow the initiating operation/status response to flush.
					select {
					case <-ctx.Done():
					case <-s.Manager.Done():
					case <-time.After(100 * time.Millisecond):
						if s.stop != nil {
							s.stop()
						}
					}
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-s.Manager.Done():
					return
				case <-ticker.C:
				}
			}
		})
	})
}
