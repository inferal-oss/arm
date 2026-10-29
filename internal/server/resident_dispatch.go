package server

import (
	"crypto/sha256"
	"sync/atomic"

	"getarm.org/internal/model"
)

// preparedCall owns the original public request bytes. It is not a wire DTO,
// receipt or resource admission; there is no caller-selected worker route.
type preparedCall struct {
	owner      *Server
	caller     *authenticatedCaller
	request    model.WireRequest
	paramsHash [32]byte
	used       atomic.Bool
}

func (s *Server) prepareCall(caller *authenticatedCaller, request model.WireRequest) (*preparedCall, error) {
	if _, err := s.callerContext(caller); err != nil {
		return nil, err
	}
	request.Params = append([]byte(nil), request.Params...)
	return &preparedCall{owner: s, caller: caller, request: request, paramsHash: sha256.Sum256(request.Params)}, nil
}

// Stage one accepts no replacement context: domain handlers receive their
// original authenticated ingress context, with existing cancellation semantics.
func (s *Server) dispatchPrepared(call *preparedCall) dispatchResult {
	if call == nil || call.owner != s || call.paramsHash != sha256.Sum256(call.request.Params) {
		return deniedDispatch("", model.Err("forbidden", "original prepared call required"))
	}
	if !call.used.CompareAndSwap(false, true) {
		return deniedDispatch(call.request.ID, model.Err("forbidden", "prepared call already dispatched"))
	}
	ctx, err := s.callerContext(call.caller)
	if err != nil {
		return deniedDispatch(call.request.ID, err)
	}
	result, err := s.dispatch(ctx, call.request)
	return s.projectDispatch(ctx, call.request, result, err)
}
