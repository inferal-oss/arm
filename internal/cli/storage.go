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
	if action == "pools" || action == "benchmark" {
		return a.storagePoolsCommand(action, args)
	}
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
	if err := a.resolveValidatedAliases(f); err != nil {
		return 1, err
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

func (a *app) storagePoolsCommand(action string, args []string) (int, error) {
	f := a.flags("storage " + action)
	f.Bool("json", false, "JSON output")
	size, reason, agent := new(string), new(string), new(string)
	if action == "benchmark" {
		f.StringVar(size, "size", "256MiB", "benchmark scratch size (8MiB..1GiB, whole MiB)")
		f.StringVar(reason, "reason", "", "purpose for opt-in performance testing")
		f.StringVar(agent, "agent", "", "registered agent contact")
	}
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if action == "pools" {
		var result []model.StoragePoolView
		if err := a.call("storage_pools", nil, &result); err != nil {
			return 1, err
		}
		return 0, a.print(result)
	}
	bytes, err := bytesValue(*size)
	if err != nil {
		return 2, err
	}
	var result model.StorageBenchmarkResult
	if err = a.call("storage_benchmark", map[string]any{"name": f.Arg(0), "bytes": bytes, "reason": *reason, "agent": *agent}, &result); err != nil {
		return 1, err
	}
	return 0, a.print(result)
}
