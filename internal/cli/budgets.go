package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"io"
	"strings"
)

func parseBudgetLimits(raw string) (model.BudgetLimits, error) {
	var limits model.BudgetLimits
	if len(raw) > 48<<10 || !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return limits, fmt.Errorf("--limits requires one JSON object, at most 48 KiB")
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&limits); err != nil {
		return limits, fmt.Errorf("invalid --limits: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return limits, fmt.Errorf("--limits requires exactly one JSON object")
	}
	if limits.CPU != nil && *limits.CPU < 0 || limits.Memory != nil && *limits.Memory < 0 || limits.Disk != nil && *limits.Disk < 0 {
		return limits, fmt.Errorf("policy ceilings must be nonnegative; zero forbids positive claims")
	}
	if len(limits.Pools) > 64 {
		return limits, fmt.Errorf("at most 64 explicit pool ceilings")
	}
	for id, q := range limits.Pools {
		if strings.TrimSpace(id) == "" || len(id) > 128 || q < 0 {
			return limits, fmt.Errorf("pool ceilings require a bounded pool ID and a nonnegative int64 count")
		}
	}
	return limits, nil
}
func (a *app) budget(args []string) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("use arm budget --help")
	}
	action, args := args[0], args[1:]
	switch action {
	case "list", "inspect", "consumers", "history", "lookup", "create", "set", "rename", "describe", "drain", "resume", "delete":
	default:
		return 2, fmt.Errorf("unknown budget action %q; use arm budget --help", action)
	}
	f := a.flags("budget " + action)
	f.Bool("json", false, "JSON output (automatic when redirected)")
	switch action {
	case "list", "inspect", "consumers", "history":
		q := model.BudgetQuery{}
		if action == "list" || action == "consumers" || action == "history" {
			f.IntVar(&q.Limit, "limit", 50, "page size (1..100)")
		}
		if action == "list" || action == "consumers" {
			f.StringVar(&q.After, "after", "", "resume after the returned opaque cursor")
		}
		if action == "history" {
			f.Int64Var(&q.Sequence, "after", 0, "resume after returned journal sequence")
		}
		if action == "list" {
			f.BoolVar(&q.All, "all", false, "include deleted definitions")
			f.StringVar(&q.Parent, "parent", "", "only direct children of this ID (self for owner root)")
		}
		if err := a.parse(f, args, false); err != nil {
			return 2, err
		}
		if (action != "inspect" && q.Limit < 1) || q.Limit > 100 || q.Sequence < 0 || len(q.After) > 128 {
			return 2, fmt.Errorf("invalid page boundary")
		}
		if action != "list" {
			q.ID = f.Args()[0]
		}
		var result any
		if err := a.call("budget_"+action, q, &result); err != nil {
			return 1, err
		}
		return 0, a.print(result)
	case "lookup":
		key := f.String("key", "", "original durable policy operation key")
		if err := a.parse(f, args, false); err != nil {
			return 2, err
		}
		if *key == "" || len(*key) > 128 {
			return 2, fmt.Errorf("--key is required and at most 128 bytes")
		}
		var result any
		if err := a.call("budget_lookup", map[string]string{"idempotency_key": *key}, &result); err != nil {
			return 1, err
		}
		return 0, a.print(result)
	}
	r := model.BudgetMutation{Operation: action}
	f.StringVar(&r.Reason, "reason", "", "required purpose, recorded transactionally")
	f.StringVar(&r.Key, "key", "", "durable exact-retry operation key (generated when omitted)")
	f.StringVar(&r.Agent, "agent", "", "registered contact attribution (same OS-user authority)")
	f.StringVar(&r.Project, "project", "", "project attribution")
	revision := int64(0)
	limits := new(string)
	if action == "create" {
		f.StringVar(&r.Parent, "parent", "self", "immutable parent policy ID; default authenticated owner root")
		f.StringVar(&r.Description, "description", "", "policy description")
	} else {
		f.Int64Var(&revision, "revision", 0, "required current definition revision")
	}
	if action == "create" || action == "set" {
		limits = f.String("limits", "", "complete limits JSON: cpu, memory_bytes, disk_bytes, pools; null/missing = no extra cap, 0 = zero")
	}
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if strings.TrimSpace(r.Reason) == "" || len(r.Reason) > 4096 || len(r.Key) > 128 {
		return 2, fmt.Errorf("bounded --reason required; --key at most 128 bytes")
	}
	if action == "create" {
		r.Name = f.Args()[0]
	} else {
		r.ID = f.Args()[0]
		if revision < 1 {
			return 2, fmt.Errorf("--revision must be positive; read arm budget inspect %s first", r.ID)
		}
		r.Revision = &revision
	}
	if action == "rename" {
		r.Name = f.Args()[1]
	}
	if action == "describe" {
		r.Description = f.Args()[1]
	}
	if action == "set" && *limits == "" {
		return 2, fmt.Errorf("--limits is required for complete replacement; use '{}' to explicitly clear every extra ceiling")
	}
	if *limits != "" {
		parsed, err := parseBudgetLimits(*limits)
		if err != nil {
			return 2, err
		}
		r.Limits = &parsed
	}
	if r.Key == "" {
		r.Key = local.ID()
	}
	var result model.BudgetChange
	if err := a.call("budget_mutate", r, &result); err != nil {
		if a.ctx.Err() != nil {
			return 130, model.Err("budget_outcome_unknown", fmt.Sprintf("policy change may have committed; inspect arm budget lookup --key %s before retrying; retain the identical request and key", r.Key))
		}
		var api *model.Error
		if errors.As(err, &api) {
			return 1, model.Err(api.Code, fmt.Sprintf("%s (policy key %q; use arm budget lookup --key %s before a different change)", api.Message, r.Key, r.Key))
		}
		return 1, fmt.Errorf("policy outcome may be unknown; inspect arm budget lookup --key %s, or explicitly retry the identical request/key: %w", r.Key, err)
	}
	return 0, a.print(result)
}
