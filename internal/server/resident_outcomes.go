package server

import (
	"context"
	"errors"

	"getarm.org/internal/model"
)

type dispatchDisposition uint8

const (
	dispatchNotInvoked dispatchDisposition = iota
	dispatchDomainResult
)

// This in-process result retains the existing wire encoder and typed results.
// A domain error is not classified as proof that no effect happened. No generic
// replay receipt, new public field or post-read worker byte ceiling is added.
type dispatchResult struct {
	response    model.WireResponse
	disposition dispatchDisposition
}

func deniedDispatch(id string, err error) dispatchResult {
	return dispatchResult{response: dispatchError(id, err), disposition: dispatchNotInvoked}
}
func dispatchError(id string, err error) model.WireResponse {
	response := model.WireResponse{Version: 1, ID: id}
	var api *model.Error
	if errors.As(err, &api) {
		response.Error = api
	} else {
		response.Error = &model.Error{Code: "internal_error", Message: "backend operation failed"}
	}
	return response
}

// projectDispatch is the former ordinary WS response projection, unchanged in
// order and public types. Special subscription responses keep their own writer.
func (s *Server) projectDispatch(ctx context.Context, req model.WireRequest, result any, err error) dispatchResult {
	resp := model.WireResponse{Version: 1, ID: req.ID}
	if err != nil {
		resp = dispatchError(req.ID, err)
	} else {
		resp.Result = s.namedView(req.Operation, result)
		if source, ok := result.(sourceNamedRead); ok {
			resp.Result, resp.NamingDiagnostic = source.raw, source.diagnostic
		} else if !remoteNodeContext(ctx) {
			resp.Result, resp.NamingDiagnostic = s.customNamedView(ctx, req.Operation, resp.Result)
			if poolProjectedRead(req.Operation) {
				bounded, boundErr := boundedPoolRead(resp.Result, nil)
				if boundErr != nil {
					var api *model.Error
					if errors.As(boundErr, &api) {
						resp.Error = api
					} else {
						resp.Error = &model.Error{Code: "internal_error", Message: "pool view could not be encoded"}
					}
					resp.Result = nil
				} else {
					resp.Result = bounded
				}
			}
		}
	}
	return dispatchResult{response: resp, disposition: dispatchDomainResult}
}
