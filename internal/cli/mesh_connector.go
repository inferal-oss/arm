package cli

import (
	"fmt"
	"github.com/yrashk/arm/internal/mesh"
)

// Connector configuration is native-only and immutable by ID. The dashboard
// can select a reviewed ID but cannot supply an executable, home or SSH option.
func (a *app) meshConnector(args []string) (int, error) {
	if a.group("mesh connector", args) {
		return 0, nil
	}
	if len(args) == 0 {
		return 2, fmt.Errorf("mesh connector requires list or add")
	}
	action, args := args[0], args[1:]
	f := a.flags("mesh connector " + action)
	if action == "list" {
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		if f.NArg() != 0 {
			return 2, fmt.Errorf("connector list takes no operands")
		}
		out, e := mesh.ReadConnectors(a.home)
		if e != nil {
			return 1, e
		}
		return 0, a.print(out)
	}
	if action != "add" {
		return 2, fmt.Errorf("mesh connector requires list or add")
	}
	binary := f.String("binary", "arm", "installed remote ARM executable")
	home := f.String("remote-home", "", "explicit absolute remote ARM state directory")
	reason := f.String("reason", "", "required audited purpose")
	yes := f.Bool("yes", false, "save this reviewed local connector")
	dry := f.Bool("dry-run", false, "preview without changing state")
	if e := a.parse(f, args, false); e != nil {
		return 2, e
	}
	if f.NArg() != 2 {
		return 2, fmt.Errorf("connector add requires ID SSH_DEST")
	}
	q := mesh.Connector{ID: f.Arg(0), Destination: f.Arg(1), Binary: *binary, Home: *home, Reason: *reason}
	if e := q.Validate(); e != nil {
		return 2, e
	}
	plan := map[string]any{"operation": "save-immutable-mesh-connector", "connector": q, "effect": "allow local operator dashboard workflows to invoke this exact installed ARM helper and selected private home via verified direct SSH; creates no peer trust and installs no executable"}
	if e := a.meshConfirm(plan, *yes, *dry); e != nil {
		return 1, e
	}
	if *dry {
		return 0, nil
	}
	if e := mesh.SaveConnector(a.home, q); e != nil {
		return 1, e
	}
	return 0, a.print(map[string]any{"saved": q.ID})
}
