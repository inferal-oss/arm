package server

import (
	"encoding/json"
	"github.com/yrashk/arm/internal/model"
)

// Policy administration is deliberately native-only. The existing browser and
// mesh operation allowlists do not include these operations.
func (s *Server) budgetOperation(op string, raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, model.Err("invalid_request", "budget parameters required")
	}
	decode := func(value any) error {
		if err := json.Unmarshal(raw, value); err != nil {
			return model.Err("invalid_request", err.Error())
		}
		return nil
	}
	var value any
	var err error
	switch op {
	case "budget_mutate":
		var q model.BudgetMutation
		if err = decode(&q); err == nil {
			value, err = s.Manager.MutateBudget(q)
		}
	case "budget_lookup":
		var q struct {
			Key string `json:"idempotency_key"`
		}
		if err = decode(&q); err == nil {
			value, err = s.Manager.LookupBudgetMutation(q.Key)
		}
	case "budget_list", "budget_inspect", "budget_consumers", "budget_history":
		var q model.BudgetQuery
		if err = decode(&q); err != nil {
			break
		}
		switch op {
		case "budget_list":
			value, err = s.Manager.Budgets(q)
		case "budget_inspect":
			value, err = s.Manager.BudgetInspect(q.ID)
		case "budget_consumers":
			value, err = s.Manager.BudgetConsumers(q)
		case "budget_history":
			value, err = s.Manager.BudgetHistory(q)
		}
	default:
		return nil, model.Err("invalid_request", "unknown budget operation")
	}
	if err != nil {
		if _, ok := err.(*json.SyntaxError); ok {
			return nil, model.Err("invalid_request", err.Error())
		}
		if _, ok := err.(*json.UnmarshalTypeError); ok {
			return nil, model.Err("invalid_request", err.Error())
		}
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 3<<20 {
		return nil, model.Err("response_too_large", "complete policy snapshot exceeds the 3 MiB read boundary; use smaller definition/consumer/history pages. Nothing was truncated")
	}
	return value, nil
}
