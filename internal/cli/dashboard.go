package cli

import (
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"runtime"

	"github.com/yrashk/arm/internal/model"
)

func (a *app) dashboard(args []string) error {
	f := a.flags("dashboard")
	noOpen := f.Bool("no-open", false, "print a one-use connection link without opening a browser")
	f.Bool("json", false, "force JSON output (never opens a browser)")
	if err := a.parse(f, args, false); err != nil {
		return err
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
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(a.ctx, "/usr/bin/open", link.URL)
	case "linux":
		command = exec.CommandContext(a.ctx, "xdg-open", link.URL)
	default:
		fmt.Fprintln(a.out, "Open the link above in your browser.")
		return nil
	}
	if err := command.Run(); err != nil {
		fmt.Fprintln(a.out, "Could not open a browser automatically. Open the link above manually.")
	}
	return nil
}
