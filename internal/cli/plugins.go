package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/yrashk/arm/internal/model"
)

const (
	pluginNameLimit       = 64
	pluginPathBytes       = 64 << 10
	pluginPathDirectories = 256
	pluginScanEntries     = 32768
	pluginScanCandidates  = 1024
	pluginScanNames       = 256
)

func validPluginName(name string) bool {
	if len(name) == 0 || len(name) > pluginNameLimit || name[0] < 'a' || name[0] > 'z' || name[len(name)-1] == '-' {
		return false
	}
	for _, c := range []byte(name) {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// The dispatcher remains authoritative for execution. This shared registry
// labels discovery collisions AND selects built-in cancellation in Main. The
// dispatcher coverage regression requires every actual built-in case to appear
// here, including when new command/help entries are added. Arguments never let
// a PATH candidate override a built-in.
func builtinPluginName(name string) bool {
	if name == "arm" {
		return false
	}
	if name == "help" || name == "plugins" {
		return true
	}
	if _, ok := commandHelps[name]; ok {
		return true
	}
	_, ok := groupHelps[name]
	return ok
}

type pluginLookup struct {
	path, candidate string
	err             error
}

// Resolve ordinary caller PATH order, including explicit relative/empty entries.
// Absolute LookPath performs access checks without executing the candidate. A
// fresh invocation resolves again; this is neither a provider pin nor consent.
func resolvePlugin(name, path string) pluginLookup {
	var first pluginLookup
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		candidate, err := filepath.Abs(filepath.Join(dir, "arm-"+name))
		if err != nil {
			if first.err == nil {
				first = pluginLookup{candidate: dir, err: err}
			}
			continue
		}
		resolved, err := exec.LookPath(candidate)
		if err == nil {
			return pluginLookup{path: resolved}
		}
		if first.err == nil {
			if _, e := os.Lstat(candidate); e == nil || !errors.Is(e, os.ErrNotExist) {
				first = pluginLookup{candidate: candidate, err: err}
			}
		}
	}
	if first.err != nil {
		return first
	}
	return pluginLookup{err: exec.ErrNotFound}
}

func pluginEnvironment(home string, jsonMode bool) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "ARM_HOME=") && !strings.HasPrefix(entry, "ARM_JSON=") {
			env = append(env, entry)
		}
	}
	mode := "0"
	if jsonMode {
		mode = "1"
	}
	return append(env, "ARM_HOME="+home, "ARM_JSON="+mode)
}

func (a *app) externalPlugin(name string, args []string) (int, error) {
	if !validPluginName(name) {
		return 2, model.Err("invalid_plugin_name", "unknown command: plugin names must be 1–64 lowercase ASCII letters, digits or hyphens, begin with a letter and end with a letter or digit")
	}
	a.externalDispatch = true
	resolved := resolvePlugin(name, os.Getenv("PATH"))
	if resolved.path == "" {
		if resolved.candidate != "" {
			return 126, model.Err("plugin_unavailable", fmt.Sprintf("cannot execute %s from PATH: %v", resolved.candidate, resolved.err))
		}
		return 127, model.Err("command_not_found", fmt.Sprintf("unknown command %q; no executable arm-%s found on PATH (see arm plugins)", name, name))
	}
	env := pluginEnvironment(a.home, a.machine())
	// The real CLI hands over its process. This preserves PID, terminal job
	// control, signals, descriptors, exit status and raw output without a shell.
	if a.in == os.Stdin && a.out == os.Stdout && a.err == os.Stderr {
		if a.stopSignals != nil {
			a.stopSignals()
		}
		if err := syscall.Exec(resolved.path, append([]string{resolved.path}, args...), env); err != nil {
			return 126, model.Err("plugin_exec_failed", fmt.Sprintf("cannot start %s: %v", resolved.path, err))
		}
		panic("exec returned without an error")
	}
	// Embedders may supply non-file streams. Use an ordinary child, not
	// CommandContext (cancellation is not permission to change SIGTERM to SIGKILL).
	if a.stopSignals != nil {
		a.stopSignals()
	}
	cmd := exec.Command(resolved.path, args...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, a.in, a.out, a.err
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return 126, model.Err("plugin_exec_failed", fmt.Sprintf("cannot start %s: %v", resolved.path, err))
	}
	done, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		for {
			select {
			case sig := <-signals:
				_ = cmd.Process.Signal(sig)
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	<-joined
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal()), nil
		}
		return exit.ExitCode(), nil
	}
	return 126, model.Err("plugin_exec_failed", fmt.Sprintf("cannot wait for %s: %v", resolved.path, err))
}

type pluginCard struct {
	Name       string   `json:"name"`
	Executable string   `json:"executable"`
	State      string   `json:"state"`
	Path       string   `json:"path,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	Detail     string   `json:"detail,omitempty"`
}
type pluginDiscovery struct {
	Schema     int          `json:"schema_version"`
	Kind       string       `json:"kind"`
	Extensions []pluginCard `json:"extensions"`
	Truncated  bool         `json:"truncated"`
	Warnings   []string     `json:"warnings"`
	Scope      string       `json:"scope"`
}

const pluginScope = "CLI extensions only. Metadata discovery executes nothing, registers no provider and grants no permissions. Explicit invocation runs trusted code with your normal OS privileges. Managed provisioning is not implemented."

func pluginCardFor(name, path string, candidates []string) pluginCard {
	card := pluginCard{Name: name, Executable: "arm-" + name, Candidates: candidates, State: "unavailable"}
	if builtinPluginName(name) {
		card.State = "built-in"
		card.Detail = "ARM built-in takes precedence; matching PATH files cannot override it"
		return card
	}
	found := resolvePlugin(name, path)
	if found.path != "" {
		card.State = "discovered"
		card.Path = found.path
		card.Detail = "PATH executable found; not run or checked for dependencies"
	} else if found.candidate != "" {
		card.Detail = fmt.Sprintf("%s: %v", found.candidate, found.err)
	} else {
		card.Detail = "No executable found on PATH"
	}
	return card
}

func discoverPlugins(path string) pluginDiscovery {
	report := pluginDiscovery{Schema: 1, Kind: "cli-extensions", Extensions: []pluginCard{}, Warnings: []string{}, Scope: pluginScope}
	dirs := filepath.SplitList(path)
	if len(path) > pluginPathBytes || len(dirs) > pluginPathDirectories {
		report.Truncated = true
		report.Warnings = append(report.Warnings, "PATH exceeds discovery limits (64 KiB / 256 directories); use plugins inspect NAME for an exact lookup")
		return report
	}
	names := map[string][]string{}
	entries, candidates := 0, 0
scan:
	for _, dir := range dirs {
		if dir == "" {
			dir = "."
		}
		f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_DIRECTORY, 0)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				report.Warnings = append(report.Warnings, fmt.Sprintf("cannot read PATH directory %s: %v", dir, err))
			}
			continue
		}
		for {
			batch, readErr := f.Readdirnames(128)
			for _, filename := range batch {
				entries++
				if entries > pluginScanEntries {
					report.Truncated = true
					f.Close()
					break scan
				}
				if !strings.HasPrefix(filename, "arm-") {
					continue
				}
				name := strings.TrimPrefix(filename, "arm-")
				if !validPluginName(name) {
					continue
				}
				if _, ok := names[name]; !ok && len(names) >= pluginScanNames || candidates >= pluginScanCandidates {
					report.Truncated = true
					f.Close()
					break scan
				}
				full, e := filepath.Abs(filepath.Join(dir, filename))
				if e != nil {
					continue
				}
				names[name] = append(names[name], full)
				candidates++
			}
			if readErr != nil {
				if readErr != io.EOF {
					report.Warnings = append(report.Warnings, fmt.Sprintf("cannot finish PATH directory %s: %v", dir, readErr))
				}
				break
			}
		}
		f.Close()
	}
	keys := make([]string, 0, len(names))
	for name := range names {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		report.Extensions = append(report.Extensions, pluginCardFor(name, path, names[name]))
	}
	if report.Truncated {
		report.Warnings = append(report.Warnings, "Discovery limit reached; list may be incomplete. Explicit invocation and plugins inspect NAME resolve the requested name directly")
	}
	return report
}

func (a *app) plugins(args []string) (int, error) {
	action := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	if action != "list" && action != "inspect" {
		return 2, model.Err("invalid_request", "use arm plugins list or arm plugins inspect NAME")
	}
	f := a.flags("plugins " + action)
	f.Bool("json", false, "force JSON output")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if action == "inspect" {
		name := f.Arg(0)
		if !validPluginName(name) {
			return 2, model.Err("invalid_plugin_name", "plugin NAME must be a single lowercase command name, up to 64 bytes")
		}
		card := pluginCardFor(name, os.Getenv("PATH"), nil)
		return 0, a.print(struct {
			Schema    int        `json:"schema_version"`
			Kind      string     `json:"kind"`
			Extension pluginCard `json:"extension"`
			Scope     string     `json:"scope"`
		}{1, "cli-extension", card, pluginScope})
	}
	return 0, a.printPlugins(discoverPlugins(os.Getenv("PATH")))
}

func (a *app) pluginHelp(name string) (int, error) {
	if !validPluginName(name) {
		return 2, fmt.Errorf("unknown help topic %q", name)
	}
	found := resolvePlugin(name, os.Getenv("PATH"))
	if found.path == "" {
		return 2, model.Err("command_not_found", fmt.Sprintf("unknown help topic %q; see arm plugins for non-executing discovery", name))
	}
	return a.plugins([]string{"inspect", name})
}

// Keep the human catalog compact; detailed cards remain available through
// inspect, and machine discovery has the same complete bounded metadata.
func (a *app) printPlugins(report pluginDiscovery) error {
	if a.machine() {
		return a.print(report)
	}
	var b bytes.Buffer
	fmt.Fprintln(&b, a.paint("1;36", "ARM · CLI extensions"))
	fmt.Fprintln(&b, "PATH metadata only — executables and dependencies have not been tested.")
	fmt.Fprintln(&b)
	if len(report.Extensions) == 0 {
		fmt.Fprintln(&b, "No extension candidates found.")
	} else {
		table := tabwriter.NewWriter(&b, 0, 4, 3, ' ', 0)
		fmt.Fprintln(table, "NAME\tSTATUS\tEXECUTABLE")
		for _, card := range report.Extensions {
			state, path := "Found", card.Path
			if card.State == "built-in" {
				state = "Built-in wins"
				path = "ARM built-in; PATH candidate ignored"
			} else if card.State != "discovered" {
				state = "Unavailable"
				path = card.Detail
			}
			fmt.Fprintf(table, "%s\t%s\t%s\n", card.Name, state, terminalSafe(path))
		}
		table.Flush()
	}
	for _, warning := range report.Warnings {
		fmt.Fprintln(&b, "\n"+a.paint("33", "Notice: ")+terminalSafe(warning))
	}
	fmt.Fprintln(&b, "\nUse arm plugins inspect NAME for details; arm NAME --help explicitly runs that extension.")
	fmt.Fprintln(&b, "Running an extension uses your normal OS privileges. Discovery registers no resource provider.")
	_, err := a.out.Write(b.Bytes())
	return err
}
