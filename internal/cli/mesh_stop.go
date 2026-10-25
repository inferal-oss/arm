package cli

import (
	"flag"
	"fmt"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
	"github.com/yrashk/arm/internal/server"
)

func (a *app) meshStop(args []string) (int, error) {
	f := a.flags("mesh stop")
	reason := f.String("reason", "", "audited purpose for stopping this ARM authority")
	key := f.String("key", "", "immutable Stop key (generated for a new intent)")
	force := f.Bool("force", false, "reclaim only verified ARM-owned work")
	wait := f.Bool("wait", false, "drain existing work under destination policy")
	timeout := f.Int64("timeout", 0, "force after positive seconds, requires --wait")
	yes := f.Bool("yes", false, "approve this concrete exact-generation Stop review")
	dry := f.Bool("dry-run", false, "preview without admitting Stop or changing durable intent")
	lookup := f.Bool("lookup", false, "inspect the original key only; never reapply Stop")
	f.Bool("json", false, "structured output")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 1 {
		return 2, fmt.Errorf("mesh stop requires one stable HOST_ID")
	}
	timeoutSet, effectFlags := false, false
	f.Visit(func(v *flag.Flag) {
		switch v.Name {
		case "timeout":
			timeoutSet = true
			effectFlags = true
		case "force", "wait", "reason", "yes", "dry-run":
			effectFlags = true
		}
	})
	if *lookup && effectFlags {
		return 2, fmt.Errorf("--lookup accepts only the original host/key and output flags; remove policy, reason and approval options")
	}
	if *force && *wait || timeoutSet && (!*wait || *timeout <= 0) {
		return 2, fmt.Errorf("--force and --wait are exclusive; explicit --timeout requires --wait and positive seconds")
	}
	if *timeout < 0 || *timeout > int64((1<<63-1)/int64(time.Second)) {
		return 2, fmt.Errorf("timeout outside duration range")
	}
	q := meshops.StopDraft{Host: f.Arg(0), Reason: *reason, Policy: model.ShutdownRequest{Force: *force, Wait: *wait, Timeout: time.Duration(*timeout) * time.Second}}
	if *lookup {
		if *key == "" {
			return 2, fmt.Errorf("--lookup requires the original --key")
		}
		original, exists, err := mesh.ReadCoordination(a.home, *key)
		if err != nil {
			return 1, err
		}
		if !exists || original.Kind != "stop" {
			return 1, model.Err("not_found", "original Stop source receipt not found")
		}
		var saved meshops.StartRequest
		if err = mesh.Decode(original.Request, &saved); err != nil || saved.Stop == nil || saved.Stop.Host != q.Host {
			return 1, model.Err("idempotency_conflict", "key belongs to another target")
		}
		var result meshops.Operation
		if err = a.call("mesh_operator_stop_lookup", struct {
			Key      string `json:"key"`
			Revision int64  `json:"revision"`
		}{*key, original.Revision}, &result); err != nil {
			return 1, err
		}
		return 0, a.print(result)
	}
	if *key != "" {
		original, exists, err := mesh.ReadCoordination(a.home, *key)
		if err != nil {
			return 1, err
		}
		if exists {
			var saved meshops.StartRequest
			if err = mesh.Decode(original.Request, &saved); err != nil || original.Kind != "stop" || saved.Stop == nil || saved.Key != *key {
				return 1, model.Err("idempotency_conflict", "key belongs to another operation")
			}
			selected := *saved.Stop
			selected.Host = q.Host
			f.Visit(func(v *flag.Flag) {
				switch v.Name {
				case "reason":
					selected.Reason = q.Reason
				case "force":
					selected.Policy.Force = q.Policy.Force
				case "wait":
					selected.Policy.Wait = q.Policy.Wait
				case "timeout":
					selected.Policy.Timeout = q.Policy.Timeout
				}
			})
			q = selected
		}
	}
	if *key == "" {
		*key = local.ID() + local.ID()
	}
	return a.meshCoordinator().Stop(*key, q, *yes, *dry)
}
func (a *app) meshStopInspectHelper(raw []byte) (int, error) {
	var q meshops.StopInspectRequest
	if err := nodestop.Decode(raw, &q); err != nil {
		return 2, err
	}
	if q.Action != "node_stop_inspect" || q.Home != q.Target.Home || !mesh.ValidKey(q.Key) {
		return 2, model.Err("invalid_request", "exact fixed Stop inspection scope required")
	}
	result, err := server.InspectNodeStop(a.ctx, q.Home, q.Target, q.Key)
	if err != nil {
		return 1, err
	}
	return 0, a.print(result)
}
