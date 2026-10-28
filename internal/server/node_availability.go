package server

import (
	"context"
	"encoding/json"

	"getarm.org/internal/model"
	"getarm.org/internal/nodeavailability"
)

// Prepared optional operation handlers. Central dispatch must authorize the
// versioned read with observe-host, keep the setter local-owner-only and
// advertise node-availability-v1 only when the read is actually wired.
func (s *Server) nodeAvailabilityRead(raw json.RawMessage) (nodeavailability.Report, error) {
	if err := nodeMetadataParams(raw); err != nil {
		return nodeavailability.Report{}, err
	}
	return s.Manager.NodeAvailability()
}

func (s *Server) nodeAvailabilitySet(raw json.RawMessage) (nodeavailability.Document, error) {
	update, err := nodeavailability.DecodeUpdate(raw)
	if err != nil {
		return nodeavailability.Document{}, model.Err("invalid_request", err.Error())
	}
	return s.Manager.SetNodeAvailability(update)
}

// Prepared typed browser/local-gateway chosen read. It shares the observer's
// two-slot cap and three-second whole deadline, and cannot dispatch arbitrary RPC.
func (s *Server) nodeAvailabilityChosenRead(ctx context.Context, raw json.RawMessage) (nodeavailability.Report, error) {
	host, err := nodeavailability.DecodeReadTarget(raw)
	if err != nil {
		return nodeavailability.Report{}, model.Err("invalid_request", err.Error())
	}
	ctx, cancel := context.WithTimeout(ctx, nodeReadTimeout)
	defer cancel()
	status, err := s.Manager.Status()
	if err != nil {
		return nodeavailability.Report{}, err
	}
	if host == "localhost" || host == status.Host {
		return s.Manager.NodeAvailability()
	}
	s.meshMu.Lock()
	service := s.mesh
	s.meshMu.Unlock()
	if service == nil {
		return nodeavailability.Report{}, model.Err("host_unavailable", "local mesh is unavailable; no local fallback")
	}
	peer, err := service.store.Resolve(host)
	if err != nil {
		return nodeavailability.Report{}, err
	}
	state, err := s.probeMeshAvailability(ctx, service, status.Host, peer)
	if err != nil {
		_, code := availabilityFailure(err)
		return nodeavailability.Report{}, model.Err(code, "Current owner availability observation is unavailable; no profile or authority is inferred")
	}
	if state.Declared == nil || state.declarationObservedAt == nil {
		return nodeavailability.Report{}, model.Err("unsupported", "owner declaration is unsupported, denied or currently unavailable")
	}
	return nodeavailability.Report{Schema: 1, Host: peer.Identity.Host, OwnerDeclared: *state.Declared, ObservedAt: *state.declarationObservedAt}, nil
}
