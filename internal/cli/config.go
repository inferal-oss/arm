package cli

import (
	"flag"
	"fmt"
	"runtime"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/model"
)

func daemonDefaults() daemonconfig.Settings {
	return daemonconfig.Settings{Resources: model.Resources{CPU: float64(max(1, runtime.NumCPU()-1))}, Listen: "127.0.0.1:0"}
}

func startupOverrides(f *flag.FlagSet, cpu *float64, memory, disk, listen *string) daemonconfig.Overrides {
	var o daemonconfig.Overrides
	f.Visit(func(v *flag.Flag) {
		switch v.Name {
		case "cpu":
			o.CPU = cpu
		case "memory":
			o.Memory = memory
		case "disk":
			o.Disk = disk
		case "listen":
			o.Listen = listen
		}
	})
	return o
}

func startupArguments(home string, f *flag.FlagSet) []string {
	args := []string{"--home", home, "serve"}
	f.Visit(func(v *flag.Flag) {
		switch v.Name {
		case "cpu", "memory", "disk", "listen":
			args = append(args, "--"+v.Name, v.Value.String())
		}
	})
	return args
}

func (a *app) configCommand(args []string) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("config requires check or show; see arm config --help")
	}
	action, args := args[0], args[1:]
	if action == "set" || action == "pool" {
		return a.configEdit(action, args)
	}
	if action != "check" && action != "show" {
		return 2, fmt.Errorf("unknown config action %q; use check or show", action)
	}
	f := a.flags("config " + action)
	f.Bool("json", false, "force JSON output (default: automatic)")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	r, err := daemonconfig.Resolve(a.home, daemonDefaults(), daemonconfig.Overrides{})
	if err != nil {
		return 2, fmt.Errorf("configuration refused; nothing applied: %w", err)
	}
	return 0, a.print(r)
}

func (a *app) configEdit(action string, args []string) (int, error) {
	label := "config " + action
	edit := daemonconfig.Edit{}
	if action == "pool" {
		if len(args) == 0 {
			return 2, fmt.Errorf("config pool requires set or remove")
		}
		operation := args[0]
		args = args[1:]
		if operation != "set" && operation != "remove" {
			return 2, fmt.Errorf("config pool requires set or remove")
		}
		label += " " + operation
		edit.Remove = operation == "remove"
	}
	f := a.flags(label)
	f.Bool("json", false, "JSON output")
	f.BoolVar(&edit.DryRun, "dry-run", false, "show validated proposed TOML without writing")
	f.StringVar(&edit.ExpectFile, "expect-file", "", "expected existing file SHA256 (optional concurrency guard)")
	path, tier, ceiling := new(string), new(string), new(string)
	if action == "pool" && !edit.Remove {
		f.StringVar(path, "path", "", "existing absolute storage directory; required for new pool")
		f.StringVar(tier, "tier", "", "operator workload label")
		f.StringVar(ceiling, "ceiling", "", "optional byte ceiling, or auto")
	}
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if action == "set" {
		edit.Key = f.Arg(0)
		edit.Value = f.Arg(1)
	} else {
		edit.Pool = f.Arg(0)
		edit.Fields = map[string]string{}
		f.Visit(func(v *flag.Flag) {
			switch v.Name {
			case "path":
				edit.Fields["path"] = *path
			case "tier":
				edit.Fields["tier"] = *tier
			case "ceiling":
				edit.Fields["ceiling"] = *ceiling
			}
		})
	}
	result, err := daemonconfig.EditFile(a.home, daemonDefaults(), edit)
	if err != nil {
		return 2, err
	}
	return 0, a.print(result)
}
