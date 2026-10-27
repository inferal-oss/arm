package cli

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode"

	"getarm.org/internal/model"
	"getarm.org/internal/tui"
)

func (a *app) dashboard(args []string) error {
	f := a.flags("dashboard")
	noOpen := f.Bool("no-open", false, "print a one-use connection link without opening a browser")
	f.Bool("json", false, "force JSON output (never opens a browser)")
	if err := a.parse(f, args, false); err != nil {
		return err
	}
	selector := ""
	if f.NArg() == 1 {
		selector = f.Arg(0)
		if err := validateDashboardBrowser(selector); err != nil {
			return &argumentError{err, "dashboard"}
		}
		if selector == "terminal" {
			if *noOpen {
				return model.Err("invalid_request", "--no-open cannot be combined with dashboard terminal")
			}
			if a.forceJSON || !a.outputTTY || !a.inputTTY {
				return model.Err("terminal_required", "dashboard terminal requires interactive terminal input and output; use arm dashboard --json or arm monitor --json for scripts")
			}
			if os.Getenv("TERM") == "dumb" {
				return model.Err("terminal_required", "dashboard terminal requires cursor-capable terminal support; use arm monitor for this terminal")
			}
			return tui.Run(a.ctx, a.home, a.in, a.out)
		}
	}
	var link model.BrowserLink
	if err := a.call("browser_ticket", nil, &link); err != nil {
		return err
	}
	u, err := url.Parse(link.URL)
	if err != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || u.User != nil {
		return model.Err("invalid_endpoint", "backend returned an invalid dashboard address")
	}
	if a.machine() {
		return a.print(link)
	}
	fmt.Fprintln(a.out, a.paint("1;36", "Local ARM dashboard"))
	fmt.Fprintln(a.out, "Monitor, resource budgets, leases, messages and activity. Extend, release or cancel leases from their details; use the CLI for other changes.")
	fmt.Fprintln(a.out, "One-use connection link (valid for two minutes):")
	fmt.Fprintln(a.out, link.URL)
	if *noOpen {
		return nil
	}
	launcher, err := dashboardLauncher(runtime.GOOS, selector, link.URL, exec.LookPath)
	if err != nil {
		return err
	}
	if launcher.path == "" {
		fmt.Fprintln(a.out, "Open the link above in your browser.")
		return nil
	}
	if launcher.detached {
		if err := a.ctx.Err(); err != nil {
			return err
		}
		// A graphical executable can run for the browser's whole lifetime.
		// It must survive ARM's command context and leave the caller's terminal.
		command := exec.Command(launcher.path, launcher.args...)
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := command.Start(); err != nil {
			return model.Err("browser_open_failed", "could not start the selected browser executable; open the connection link above manually or choose another browser")
		}
		go func() { _ = command.Wait() }()
		return nil
	}
	if err := exec.CommandContext(a.ctx, launcher.path, launcher.args...).Run(); err != nil {
		return model.Err("browser_open_failed", "could not open the selected browser; open the connection link above manually or choose another browser")
	}
	return nil
}

type dashboardLaunch struct {
	path     string
	args     []string
	detached bool
}

func validateDashboardBrowser(selector string) error {
	if selector == "" || strings.TrimSpace(selector) == "" || len(selector) > 1024 || strings.HasPrefix(selector, "-") || strings.IndexFunc(selector, unicode.IsControl) >= 0 {
		return fmt.Errorf("BROWSER must be an application name, .app path, or executable name/path without control characters or leading '-'")
	}
	return nil
}

// Launchers receive the URL as one argument, never shell source. Executables
// take precedence over application names; macOS .app bundles use LaunchServices.
func dashboardLauncher(platform, selector, address string, lookup func(string) (string, error)) (dashboardLaunch, error) {
	if selector != "" {
		if err := validateDashboardBrowser(selector); err != nil {
			return dashboardLaunch{}, err
		}
		if selector == "terminal" {
			return dashboardLaunch{}, model.Err("invalid_request", "terminal selects the interactive dashboard, not a browser launcher")
		}
		if platform == "darwin" && strings.HasSuffix(strings.TrimRight(selector, "/"), ".app") {
			return dashboardLaunch{path: "/usr/bin/open", args: []string{"-a", selector, address}}, nil
		}
		if executable, err := lookup(selector); err == nil {
			return dashboardLaunch{path: executable, args: []string{address}, detached: true}, nil
		}
		if platform == "darwin" && !strings.ContainsRune(selector, filepath.Separator) {
			return dashboardLaunch{path: "/usr/bin/open", args: []string{"-a", selector, address}}, nil
		}
		return dashboardLaunch{}, model.Err("browser_unavailable", "browser executable was not found; provide an executable on PATH or an executable path (macOS also accepts application names and .app paths)")
	}
	switch platform {
	case "darwin":
		return dashboardLaunch{path: "/usr/bin/open", args: []string{address}}, nil
	case "linux":
		if executable, err := lookup("xdg-open"); err == nil {
			return dashboardLaunch{path: executable, args: []string{address}}, nil
		}
		return dashboardLaunch{}, model.Err("browser_unavailable", "xdg-open was not found; open the connection link manually or specify a browser executable")
	default:
		return dashboardLaunch{}, nil
	}
}
