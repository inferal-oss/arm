package cli

import (
	"flag"
	"fmt"
	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/meshops"
	"getarm.org/internal/model"
	"getarm.org/internal/nodestop"
	"getarm.org/internal/server"
	"time"
)

func (a *app) meshUninstall(args []string) (int, error) {
	f := a.flags("mesh uninstall")
	key := f.String("key", "", "immutable uninstall operation key")
	provision := f.String("provision-key", "", "original source provisioning operation key")
	reason := f.String("reason", "", "audited reason")
	yes := f.Bool("yes", false, "approve concrete exact-installation review")
	dry := f.Bool("dry-run", false, "show review without stopping or removing")
	lookup := f.Bool("lookup", false, "read original operation; never retry effects")
	finish := f.Bool("finish", false, "explicitly reconcile original admitted removal after positive Stop")
	force := f.Bool("force", false, "Stop only verified ARM-owned work")
	wait := f.Bool("wait", false, "drain existing obligations")
	timeout := f.Int64("timeout", 0, "positive seconds before force; requires --wait")
	f.Bool("json", false, "structured output")
	if e := a.parse(f, args, false); e != nil {
		return 2, e
	}
	if f.NArg() != 1 {
		return 2, fmt.Errorf("mesh uninstall requires exact HOST_ID")
	}
	timeoutSet, forbiddenReadFlag := false, false
	f.Visit(func(v *flag.Flag) {
		switch v.Name {
		case "timeout":
			timeoutSet = true
			forbiddenReadFlag = true
		case "reason", "provision-key", "dry-run", "force", "wait":
			forbiddenReadFlag = true
		case "yes":
			if *lookup {
				forbiddenReadFlag = true
			}
		}
	})
	if *lookup && *finish || (*lookup || *finish) && forbiddenReadFlag {
		return 2, fmt.Errorf("lookup accepts host/key/output only; finish additionally accepts --yes")
	}
	if *force && *wait || timeoutSet && (!*wait || *timeout <= 0) || *timeout > int64((1<<63-1)/int64(time.Second)) {
		return 2, fmt.Errorf("valid exclusive wait/force and positive bounded timeout required")
	}
	if (*lookup || *finish) && *key == "" {
		return 2, fmt.Errorf("original --key required")
	}
	if e := a.resolveValidatedAliases(f); e != nil {
		return 1, e
	}
	c := a.meshCoordinator()
	if *lookup || *finish {
		return c.UninstallLookup(*key, f.Arg(0), *finish, *yes)
	}
	if *key == "" {
		*key = local.ID() + local.ID()
	}
	return c.Uninstall(*key, meshops.UninstallDraft{Host: f.Arg(0), ProvisionKey: *provision, Reason: *reason, Policy: model.ShutdownRequest{Force: *force, Wait: *wait, Timeout: time.Duration(*timeout) * time.Second}}, *yes, *dry)
}
func (a *app) meshUninstallHelper(raw []byte) (int, error) {
	var q meshops.UninstallNativeRequest
	if e := mesh.Decode(raw, &q); e != nil {
		return 2, e
	}
	if q.Action != "node_uninstall_lookup" && q.Action != "node_uninstall_finish" {
		return 2, fmt.Errorf("fixed uninstall helper action required")
	}
	r, e := mesh.LookupUninstall(q.Transfer, q.Key)
	if e != nil {
		return 1, e
	}
	if e = mesh.CheckUninstallHelper(r); e != nil {
		return 1, e
	}
	if q.Action == "node_uninstall_lookup" {
		if e = mesh.AuthorizeUninstallRead(r.Plan); e != nil {
			return 1, e
		}
		result := mesh.UninstallResult{Receipt: r, Detail: "Original retained receipt; no Stop or removal repeated."}
		observation, err := server.InspectNodeStop(a.ctx, r.Plan.Stop.Plan.Target.Home, r.Plan.Stop.Plan.Target, r.Plan.Stop.Plan.Key)
		if err == nil {
			result, err = mesh.ObserveUninstall(r, observation)
		}
		if err != nil {
			result.Detail = "Original receipt retained; current completion observation unavailable: " + err.Error()
		}
		return 0, a.print(result)
	}
	if e = mesh.AuthorizeUninstallNative(r.Plan); e != nil {
		return 1, e
	}
	var result mesh.UninstallResult
	_, e = server.WithInspectedNodeStop(a.ctx, r.Plan.Stop.Plan.Target.Home, r.Plan.Stop.Plan.Target, r.Plan.Stop.Plan.Key, func(observation nodestop.Observation) error {
		if e := a.ctx.Err(); e != nil {
			return e
		}
		var err error
		result, err = mesh.RemoveUninstallArtifact(r.Plan, observation)
		return err
	})
	if e != nil {
		return 1, e
	}
	return 0, a.print(result)
}
