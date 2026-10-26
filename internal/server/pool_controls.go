package server

import (
	"encoding/json"
	"github.com/inferal-oss/arm/internal/model"
)

func browserPoolMutation(request model.PoolMutation) error {
	switch request.Operation {
	case "create", "set", "adjust", "drain", "resume", "delete":
	default:
		return model.Err("forbidden", "dashboard pool controls support only create, set, adjust, drain, resume and delete")
	}
	if request.Reviewed == nil || request.Reviewed.Authority == "" || request.Reviewed.Consumers == nil {
		return model.Err("invalid_request", "dashboard pool changes require an authority and complete reviewed consumer snapshot")
	}
	if request.Operation != "create" && (request.Revision == nil || *request.Revision <= 0) {
		return model.Err("invalid_request", "dashboard pool changes require a positive reviewed definition revision")
	}
	return nil
}

// Read snapshots never become mutations. Keep their complete encoded result
// comfortably inside clients' 4 MiB response boundary; never truncate a review.
func boundedPoolRead(value any, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(raw) > 3<<20 {
		return nil, model.Err("response_too_large", "complete pool snapshot exceeds the 3 MiB response budget; no scope was truncated. Use an individual pool ID for inspection or its paged journal. Use the bounded child and consumer pages for browsing; a mutation still requires a complete untruncated review")
	}
	return value, nil
}
