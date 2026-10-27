package cli

import (
	"fmt"
	"path/filepath"

	"getarm.org/internal/model"
)

func (a *app) storage(args []string) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("storage requires a command; use arm storage --help for create, list, inspect, export, exports, retain, cleanup and handoff")
	}
	action, args := args[0], args[1:]
	if action == "create" {
		return a.acquire("storage", args)
	}
	if action == "export" {
		return a.storageExport(args)
	}
	if action == "exports" {
		return a.storageExports(args)
	}
	if action == "retain" {
		return a.storageRetain(args)
	}
	if action != "list" && action != "inspect" && action != "cleanup" && action != "handoff" {
		return 2, fmt.Errorf("unknown storage action %q; use arm storage --help", action)
	}
	f := a.flags("storage " + action)
	dry := new(bool)
	if action == "cleanup" {
		f.BoolVar(dry, "dry-run", false, "preview cleanup without changing data")
	}
	f.Bool("json", false, "force JSON output (default: automatic)")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if action == "list" {
		if len(f.Args()) != 0 {
			return 2, fmt.Errorf("unexpected storage list arguments")
		}
		var result []model.Lease
		if err := a.call("storage_list", nil, &result); err != nil {
			return 1, err
		}
		return 0, a.print(result)
	}
	target := f.Args()[0]
	var result any
	switch action {
	case "inspect":
		path, err := filepath.Abs(target)
		if err != nil {
			return 2, err
		}
		if err := a.call("storage_inspect", map[string]string{"path": path}, &result); err != nil {
			return 1, err
		}
	case "cleanup", "handoff":
		op := "storage_" + action
		if *dry {
			op = "storage_preview"
		}
		if err := a.call(op, map[string]string{"id": target}, &result); err != nil {
			return 1, err
		}
	default:
		return 2, fmt.Errorf("unknown storage action %q", action)
	}
	return 0, a.print(result)
}
