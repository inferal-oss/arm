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
