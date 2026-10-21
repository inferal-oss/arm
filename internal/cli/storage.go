package cli

import (
	"fmt"
	"path/filepath"

	"github.com/yrashk/arm/internal/model"
)

func (a *app) storage(args []string) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("storage requires create, list, inspect PATH, cleanup ID, or handoff ID")
	}
	action, args := args[0], args[1:]
	if action == "create" {
		return a.acquire("storage", args)
	}
	f := a.flags("storage " + action)
	dry := f.Bool("dry-run", false, "preview without mutation")
	f.Bool("json", true, "JSON output")
	if action == "list" {
		if err := f.Parse(args); err != nil {
			return 2, err
		}
		if len(f.Args()) != 0 {
			return 2, fmt.Errorf("unexpected storage list arguments")
		}
		var result []model.Lease
		if err := a.call("storage_list", nil, &result); err != nil {
			return 1, err
		}
		return 0, a.print(result)
	}
	if len(args) == 0 {
		return 2, fmt.Errorf("storage %s requires a path or lease ID", action)
	}
	target := args[0]
	if err := f.Parse(args[1:]); err != nil {
		return 2, err
	}
	if len(f.Args()) != 0 {
		return 2, fmt.Errorf("unexpected storage arguments")
	}
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
