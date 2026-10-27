package cli

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// commandTree is the public routing and completion tree. Compatibility handlers
// keep ARM's established parsing rules (including single-dash long options and
// command passthrough). Completion imports their actual flag registrations at
// the parse boundary, never a second list of option names.
func (a *app) commandTree(catalog bool, invoke func() error) *cobra.Command {
	root := &cobra.Command{Use: "arm", SilenceErrors: true, SilenceUsage: true, Args: cobra.ArbitraryArgs,
		DisableFlagParsing: !catalog, RunE: func(*cobra.Command, []string) error { return invoke() }}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(a.out)
	root.SetErr(a.err)
	root.SetIn(a.in)
	nodes := map[string]*cobra.Command{"arm": root}
	var names []string
	for name := range commandHelps {
		if name != "arm" {
			names = append(names, name)
		}
	}
	for name := range groupHelps {
		names = append(names, name)
	}
	names = append(names, "help")
	sort.Strings(names)
	for _, name := range names {
		parent, prefix := root, ""
		for _, part := range strings.Fields(name) {
			prefix = strings.TrimSpace(prefix + " " + part)
			child := nodes[prefix]
			if child == nil {
				child = &cobra.Command{Use: part, DisableFlagParsing: !catalog, Args: cobra.ArbitraryArgs,
					RunE: func(*cobra.Command, []string) error { return invoke() }}
				nodes[prefix] = child
				parent.AddCommand(child)
			}
			parent = child
		}
		h := commandHelps[name]
		parent.Short = h.summary
		parent.Hidden = h.hidden
	}
	root.SetHelpCommand(nodes["help"])
	if !catalog {
		return root
	}
	rootProbe := *a
	addCommandFlags(root, rootProbe.rootFlags(), true)
	for name, cmd := range nodes {
		if name == "arm" || name == "help" {
			continue
		}
		if _, ok := commandHelps[name]; !ok {
			continue
		}
		probe := *a
		probe.in, probe.out, probe.err = strings.NewReader(""), io.Discard, io.Discard
		probe.catalogFlags = func(f *flag.FlagSet, stop bool) { addCommandFlags(cmd, f, stop) }
		// --help reaches each registration boundary without positional values or
		// executing the command. Pre-parse read boundaries also honor this mode.
		_, _ = probe.command(append(strings.Fields(name), "--help"))
		cmd.ValidArgsFunction = a.completeArguments
		cmd.Flags().VisitAll(func(f *pflag.Flag) { a.registerCompletion(cmd, f.Name) })
	}
	return root
}

func (a *app) publicCommand(args []string) (code int, err error) {
	if args[0] == "__complete" || args[0] == "__completeNoDesc" {
		probe := *a
		args = completionRootOptions(args, &probe.home)
		root := probe.commandTree(true, func() error { return nil })
		args = normalizeCompletionFlags(root, args)
		if completionPastCommand(root, args[1:]) {
			_, err := fmt.Fprintln(a.out, ":4")
			return 0, err
		}
		root.SetArgs(args)
		return 0, root.ExecuteContext(a.ctx)
	}
	root := a.commandTree(false, func() error {
		code, err = a.command(args)
		return err
	})
	// Every compatibility handler receives the original argument vector. Cobra
	// routes the public tree without consuming options belonging to ARM/plugins.
	root.SetArgs(args)
	e := root.ExecuteContext(a.ctx)
	if err != nil {
		return code, err
	}
	return code, e
}

// Generated scripts place ARM's original root-prefix options after __complete.
// Consume only finished prefix options here. Keeping --home local to the root
// avoids suggesting it after a command, where execution does not accept it.
func completionRootOptions(args []string, home *string) []string {
	i := 1
	for i < len(args)-1 {
		name, value, inline := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		if !strings.HasPrefix(args[i], "-") {
			break
		}
		switch name {
		case "home":
			if !inline {
				if i+1 >= len(args)-1 {
					return append([]string{args[0]}, args[i:]...)
				}
				i++
				value = args[i]
			}
			if absolute, err := filepath.Abs(value); err == nil {
				*home = absolute
			}
		case "json":
		default:
			return append([]string{args[0]}, args[i:]...)
		}
		i++
	}
	return append([]string{args[0]}, args[i:]...)
}

type repeatableFlagValue struct{ pflag.Value }

func (v repeatableFlagValue) GetSlice() []string { return []string{v.String()} }

func addCommandFlags(cmd *cobra.Command, flags *flag.FlagSet, stop bool) {
	cmd.Flags().SetInterspersed(!stop)
	if stop {
		cmd.Annotations = map[string]string{"arm-stop-at-command": "true"}
	}
	flags.VisitAll(func(f *flag.Flag) {
		converted := pflag.PFlagFromGoFlag(f)
		value := reflect.ValueOf(f.Value)
		if value.Kind() == reflect.Pointer {
			value = value.Elem()
		}
		if value.Kind() == reflect.Slice {
			converted.Value = repeatableFlagValue{converted.Value}
		}
		cmd.Flags().AddFlag(converted)
	})
}

func completionPastCommand(root *cobra.Command, args []string) bool {
	if len(args) < 2 {
		return false
	}
	cmd, rest, err := root.Find(args[:len(args)-1])
	if err != nil {
		return false
	}
	if cmd.CommandPath() == "arm pool adjust" && cmd.DisableFlagParsing {
		for _, arg := range rest {
			if arg == "--" {
				return true
			}
		}
	}
	if cmd.Annotations["arm-stop-at-command"] != "true" {
		return false
	}
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if arg == "--" || !strings.HasPrefix(arg, "-") || arg == "-" {
			return true
		}
		name, _, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		f := cmd.Flags().Lookup(name)
		if f == nil {
			return false
		}
		if !inline && f.NoOptDefVal == "" {
			i++
		}
	}
	return false
}

func normalizeCompletionFlags(root *cobra.Command, args []string) []string {
	if len(args) < 2 {
		return args
	}
	cmd, start := root, 1
	for start < len(args)-1 {
		var child *cobra.Command
		for _, candidate := range cmd.Commands() {
			if candidate.Name() == args[start] {
				child = candidate
				break
			}
		}
		if child == nil {
			break
		}
		cmd = child
		start++
	}
	out := append([]string(nil), args...)
	positionals := 0
	for i := start; i < len(out); i++ {
		arg := out[i]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			if cmd.Annotations["arm-stop-at-command"] == "true" {
				break
			}
			positionals++
			continue
		}
		if cmd.CommandPath() == "arm pool adjust" && positionals == 1 && i < len(out)-1 {
			if delta, err := strconv.ParseInt(arg, 10, 64); err == nil && delta < 0 {
				// The execution parser treats a signed delta as positional.
				// Cobra supports flag-name completion for compatibility commands
				// with parsing disabled, so keep the literal delta and let that
				// path avoid pflag mistaking it for an unknown shorthand. This
				// tree is completion-only; execution receives its original argv.
				cmd.InitDefaultHelpFlag()
				cmd.DisableFlagParsing = true
				positionals++
				continue
			}
		}
		name, _, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		f := cmd.Flags().Lookup(name)
		if f == nil {
			continue
		}
		if !strings.HasPrefix(arg, "--") && len(arg) > 2 {
			out[i] = "-" + arg
		}
		if !inline && f.NoOptDefVal == "" {
			i++
		}
	}
	return out
}

func (a *app) completion(args []string) (int, error) {
	f := a.flags("completion")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 1 {
		return 2, fmt.Errorf("use arm completion bash|zsh|fish; see docs/completion.md for installation")
	}
	if f.Arg(0) != "bash" && f.Arg(0) != "zsh" && f.Arg(0) != "fish" {
		return 2, fmt.Errorf("unsupported shell %q; choose bash, zsh or fish", f.Arg(0))
	}
	root := a.commandTree(true, func() error { return nil })
	return 0, generateCompletionScript(root, f.Arg(0), a.out)
}
