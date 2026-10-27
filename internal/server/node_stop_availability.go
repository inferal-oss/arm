package server

import (
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
)

// This is an observation, not mutation permission. Admission rechecks current
// peer grant, exact generation and complete impact under their actual locks.
func (s *Server) stopAvailability(peer mesh.Peer) *model.NodeStopAvailability {
	out := &model.NodeStopAvailability{Reason: "Remote Stop is unavailable", HistoryCapacity: nodestop.MaxRecords}
	target, err := s.nodeStopBinding()
	if err != nil {
		out.Reason = err.Error()
		return out
	}
	out.Generation = target.Generation
	if !s.nodeStopAdminEnabled {
		out.Reason = "Remote Stop awaits the reviewed local Start and source recovery workflow"
		return out
	}
	if !peer.Grant.Has("node-stop") {
		out.Reason = "Destination has not granted node-stop to this enrollment"
		return out
	}
	history, err := s.Manager.NodeStopHistory()
	if err != nil {
		out.Reason = "Stop history needs explicit inspection"
		return out
	}
	out.HistoryUsed = len(history)
	if len(history) >= nodestop.MaxRecords {
		out.Reason = "Stop history has reached its128-operation capacity; archival is not yet available. Existing receipts and unknown duties are preserved."
		return out
	}
	status, err := s.Manager.Status()
	if err != nil {
		out.Reason = "Current authority state unavailable"
		return out
	}
	if status.Draining {
		out.Reason = "Authority is already draining; inspect its original operation"
		return out
	}
	out.DesiredRevision = 1
	if len(history) > 0 { // Last state may include an explicitly reviewed restart.
		state, present, err := s.Manager.NodeStopDesiredState()
		if err != nil || !present {
			out.Reason = "Desired state unavailable"
			return out
		}
		out.DesiredRevision = state.Revision
		if !state.Running {
			out.Reason = "Authority is intentionally stopped; use original-key inspection"
			return out
		}
	}
	out.Supported = true
	out.Reason = "Exact generation Stop review is available; current admission checks still apply"
	return out
}
