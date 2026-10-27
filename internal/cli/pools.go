package cli

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

type poolFlags []model.PoolClaim

func (p *poolFlags) String() string { return fmt.Sprint([]model.PoolClaim(*p)) }
func (p *poolFlags) Set(s string) error {
	id, quantity, ok := strings.Cut(s, "=")
	if !ok || id == "" {
		return fmt.Errorf("pool claim requires POOL_ID=COUNT")
	}
	q, err := strconv.ParseInt(quantity, 10, 64)
	if err != nil || q <= 0 {
		return fmt.Errorf("pool count must be a positive int64 integer")
	}
	*p = append(*p, model.PoolClaim{Pool: id, Quantity: q})
	return nil
}
func (a *app) pool(args []string) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("use arm pool --help")
	}
	action, args := args[0], args[1:]
	switch action {
	case "lookup", "roots", "children", "consumers", "create", "set", "adjust", "rename", "describe", "drain", "resume", "delete", "list", "inspect", "history":
	default:
		return 2, fmt.Errorf("unknown pool action %q; use arm pool --help", action)
	}
	if action == "lookup" {
		return a.poolLookup(args)
	}
	if action == "roots" || action == "children" || action == "consumers" {
		return a.poolHierarchy(action, args)
	}
	f := a.flags("pool " + action)
	f.Bool("json", false, "JSON output; mutations include before/after state and journal sequence")
	if action == "list" || action == "inspect" || action == "history" {
		all := new(bool)
		after := new(int64)
		if action == "list" {
			f.BoolVar(all, "all", false, "include deleted definitions")
		}
		if action == "history" {
			f.Int64Var(after, "after", 0, "resume after this journal sequence (up to 100 entries per page)")
		}
		if err := a.parse(f, args, false); err != nil {
			return 2, err
		}
		id := ""
		if action != "list" {
			id = f.Args()[0]
		}
		var result any
		if err := a.call("pool_"+action, map[string]any{"id": id, "all": *all, "after": *after}, &result); err != nil {
			return 1, err
		}
		return 0, a.print(result)
	}
	r := model.PoolMutation{Operation: action}
	f.StringVar(&r.Reason, "reason", "", "required purpose for this change")
	f.StringVar(&r.Key, "key", "", "durable operation idempotency key (default: generated)")
	f.StringVar(&r.Agent, "agent", "", "registered agent contact for attribution")
	f.StringVar(&r.Project, "project", "", "project attribution")
	capacity, revision := int64(0), int64(0)
	parentRevision := int64(0)
	if action == "create" {
		f.StringVar(&r.Scope, "scope", "", "root scope: host or global; inherited with --parent")
		f.StringVar(&r.Parent, "parent", "", "carve from this pool; scope, host and unit are inherited")
		f.Int64Var(&parentRevision, "parent-revision", 0, "required expected parent revision for child creation")
		f.StringVar(&r.Host, "host", "", "host pool location (default: localhost); omit for global")
		f.StringVar(&r.Unit, "unit", "", "counted unit label")
		f.StringVar(&r.Description, "description", "", "resource description")
		f.Int64Var(&capacity, "capacity", 0, "required nonnegative integral capacity; zero pauses admission")
	} else {
		f.Int64Var(&revision, "revision", 0, "required expected revision; optional for atomic adjust")
	}
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if strings.TrimSpace(r.Reason) == "" {
		return 2, fmt.Errorf("--reason is required; see arm pool %s --help", action)
	}
	providedRevision, providedCapacity, providedParentRevision := false, false, false
	f.Visit(func(v *flag.Flag) {
		if v.Name == "revision" {
			providedRevision = true
		}
		if v.Name == "parent-revision" {
			providedParentRevision = true
		}
		if v.Name == "capacity" {
			providedCapacity = true
		}
	})
	if action == "create" {
		if !providedCapacity {
			return 2, fmt.Errorf("pool create requires --capacity COUNT (zero is allowed)")
		}
		if capacity < 0 || r.Parent == "" && ((r.Scope != "host" && r.Scope != "global") || (r.Scope == "global" && r.Host != "")) {
			return 2, fmt.Errorf("create requires nonnegative --capacity and --scope host|global; global pools have no --host")
		}
		if r.Parent != "" {
			if parentRevision < 1 {
				return 2, fmt.Errorf("child creation requires --parent-revision; inspect the parent first")
			}
			r.ParentRevision = &parentRevision
		} else if providedParentRevision {
			return 2, fmt.Errorf("--parent-revision requires --parent")
		}
		r.Name = f.Args()[0]
		r.Capacity = &capacity
	} else {
		r.ID = f.Args()[0]
		if providedRevision && revision < 1 {
			return 2, fmt.Errorf("--revision must be positive")
		}
		if providedRevision {
			r.Revision = &revision
		}
		if action == "set" || action == "adjust" {
			n, err := strconv.ParseInt(f.Args()[1], 10, 64)
			if err != nil {
				return 2, fmt.Errorf("quantity must be an int64 integer: %w", err)
			}
			if action == "set" {
				if n < 0 {
					return 2, fmt.Errorf("capacity must be nonnegative")
				}
				r.Capacity = &n
			} else {
				if n == 0 {
					return 2, fmt.Errorf("delta must be nonzero")
				}
				r.Delta = &n
			}
		}
		if action == "rename" {
			r.Name = f.Args()[1]
		}
		if action == "describe" {
			r.Description = f.Args()[1]
		}
		if action != "adjust" && r.Revision == nil {
			return 2, fmt.Errorf("--revision is required; use arm pool inspect %s to read the current revision", r.ID)
		}
	}
	if r.Key == "" {
		r.Key = local.ID()
	}
	var result model.PoolChange
	if err := a.call("pool_mutate", r, &result); err != nil {
		var api *model.Error
		if errors.As(err, &api) {
			return 1, model.Err(api.Code, fmt.Sprintf("%s (pool operation key %q)", api.Message, r.Key))
		}
		return 1, fmt.Errorf("pool operation key %q; retry with this --key and the same parameters if the outcome is uncertain: %w", r.Key, err)
	}
	return 0, a.print(result)
}
