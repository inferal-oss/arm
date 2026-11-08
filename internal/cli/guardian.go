package cli

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/model"
)

func (a *app) guardianCommand(args []string) (int, error) {
	action := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	switch action {
	case "status":
		f := a.flags("guardian status")
		f.Bool("json", false, "JSON guardian state, pressure, groups and actions")
		if err := a.parse(f, args, false); err != nil {
			return 2, err
		}
		var s model.GuardianStatus
		if err := a.call("guardian_status", nil, &s); err != nil {
			return 1, err
		}
		return 0, a.print(s)
	case "undo":
		f := a.flags("guardian undo")
		f.Bool("json", false, "JSON reverted action")
		if err := a.parse(f, args, true); err != nil {
			return 2, err
		}
		if f.NArg() != 1 {
			return 2, fmt.Errorf("guardian undo requires one ACTION_ID")
		}
		var undone model.GuardianAction
		if err := a.call("guardian_undo", model.GuardianUndoRequest{Action: f.Arg(0)}, &undone); err != nil {
			return 1, err
		}
		return 0, a.print(undone)
	}
	return 2, fmt.Errorf("unknown guardian action %q; use arm guardian --help", action)
}

func pressureBadge(a *app, level string) string {
	code := map[string]string{model.PressureNormal: "32", model.PressureElevated: "33", model.PressureHigh: "1;33", model.PressureCritical: "1;31"}[level]
	if code == "" {
		code = "2"
	}
	return a.paint(code, strings.ToUpper(level))
}

func (a *app) guardianHuman(s model.GuardianStatus) error {
	var b bytes.Buffer
	fmt.Fprintln(&b, a.paint("1;36", "ARM · host guardian"))
	mode := s.Mode
	if mode == model.GuardianOff {
		mode = "off (enable with: arm config set guardian.enabled true, then restart the backend)"
	}
	fmt.Fprintf(&b, "Mode %s · role %s · actions %d/%d this hour\n", mode, s.Role, s.Budget.Used, s.Budget.Limit)
	if s.Stale {
		fmt.Fprintln(&b, a.paint("2", "Pressure is stale or unavailable; the guardian holds all actions."))
	}
	fmt.Fprintln(&b)
	for _, r := range []struct {
		name string
		p    model.PressureReading
	}{{"CPU", s.Pressure.CPU}, {"Memory", s.Pressure.Memory}, {"I/O", s.Pressure.IO}} {
		level := r.p.Level
		if level == "" {
			level = model.PressureUnknown
		}
		fmt.Fprintf(&b, "  %-7s %s", r.name, pressureBadge(a, level))
		if r.p.Reason != "" {
			fmt.Fprintf(&b, "  %s", r.p.Reason)
		}
		fmt.Fprintln(&b)
	}
	if len(s.Incidents) > 0 {
		fmt.Fprintln(&b, "\nIncidents")
		for _, i := range s.Incidents {
			fmt.Fprintf(&b, "  %-7s %s since %s (peak %s), last step %s\n", i.Resource, pressureBadge(a, i.Level), i.Since.Local().Format(time.TimeOnly), i.Peak, orDash(i.Step))
		}
	}
	if len(s.Groups) > 0 {
		fmt.Fprintln(&b, "\nHeaviest programs")
		fmt.Fprintf(&b, "  %-30s %7s %10s %10s %5s  %s\n", "PROGRAM", "CPU", "MEMORY", "DISK/S", "PROCS", "NOTES")
		for _, g := range s.Groups {
			notes := []string{}
			if g.Protected != "" {
				notes = append(notes, "protected: "+g.Protected)
			}
			if g.Managed {
				notes = append(notes, "ARM-managed")
			}
			if g.Agent != "" {
				notes = append(notes, "agent "+g.Agent)
			}
			notes = append(notes, g.State...)
			fmt.Fprintf(&b, "  %-30s %7s %10s %10s %5d  %s\n", truncate(g.Name, 30), cores(g.CPUCores), sizeOf(g.MemoryBytes), rateOf(g.DiskBytesPerSecond), g.Processes, strings.Join(notes, "; "))
		}
	}
	if len(s.Actions) > 0 {
		fmt.Fprintln(&b, "\nRecent actions")
		for i, x := range s.Actions {
			if i == 15 {
				fmt.Fprintf(&b, "  … %d more (use --json)\n", len(s.Actions)-i)
				break
			}
			undo := ""
			if x.Reversible && x.RevertedAt == nil && (x.Outcome == model.GuardianApplied || x.Outcome == model.GuardianPartial) {
				undo = a.paint("36", "  undo: arm guardian undo "+x.ID)
			}
			fmt.Fprintf(&b, "  %s %-12s %-9s %s — %s%s\n", x.At.Local().Format(time.TimeOnly), x.Step, x.Outcome, orDash(x.GroupName), x.Reason, undo)
		}
	}
	_, err := a.out.Write(b.Bytes())
	return err
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
func cores(v *float64) string {
	if v == nil {
		return "?"
	}
	return fmt.Sprintf("%.1f", *v)
}
func sizeOf(v *uint64) string {
	if v == nil {
		return "?"
	}
	return fmt.Sprintf("%.1f GiB", float64(*v)/(1<<30))
}
func rateOf(v *float64) string {
	if v == nil {
		return "?"
	}
	return fmt.Sprintf("%.1f MiB", *v/(1<<20))
}

// recommendGuardian asks an interactive operator to decide on the host
// guardian while the configuration records no choice, and saves the answer.
// Scripts and JSON output are never prompted; the guardian stays off.
// Interruption returns the context error so the caller stops.
func (a *app) recommendGuardian() error {
	if a.machine() || !a.inputTTY {
		return nil
	}
	resolved, err := daemonconfig.Resolve(a.home, daemonDefaults(), daemonconfig.Overrides{})
	if err != nil {
		return nil
	}
	if g := resolved.Settings.Guardian; g != nil && g.Enabled != nil {
		return nil
	}
	fmt.Fprintln(a.out)
	fmt.Fprintln(a.out, a.paint("1;33", "Host guardian — strongly recommended"))
	fmt.Fprintln(a.out, "ARM can watch CPU, memory and I/O pressure and step in before a runaway")
	fmt.Fprintln(a.out, "program makes this computer unresponsive. It notifies you, lowers the")
	fmt.Fprintln(a.out, "priority of the heaviest program, asks the responsible agent to back off,")
	fmt.Fprintln(a.out, "and under critical pressure closes the busiest browser tabs. System, session")
	fmt.Fprintln(a.out, "and remote-access programs are never touched. Every action is recorded;")
	fmt.Fprintln(a.out, "reversible ones can be undone with arm guardian undo.")
	fmt.Fprintln(a.out, a.paint("2", "Unattended host? Afterwards run: arm config set guardian.role unmanned"))
	fmt.Fprint(a.out, a.paint("1;33", "Enable the host guardian? [Y/n] "))
	type line struct {
		text string
		err  error
	}
	answer := make(chan line, 1)
	go func() {
		// Read byte by byte: a buffered reader could consume input meant
		// for later prompts.
		var buf []byte
		b := make([]byte, 1)
		for len(buf) < 64 {
			n, err := a.in.Read(b)
			if n == 1 {
				if b[0] == '\n' {
					answer <- line{text: string(buf)}
					return
				}
				buf = append(buf, b[0])
			}
			if err != nil {
				answer <- line{text: string(buf), err: err}
				return
			}
		}
		answer <- line{text: string(buf)}
	}()
	var got line
	select {
	case <-a.ctx.Done():
		fmt.Fprintln(a.out)
		return a.ctx.Err()
	case got = <-answer:
	}
	if got.err != nil {
		// End of input is not consent.
		fmt.Fprintln(a.out, "\nNo choice recorded; you will be asked again next time.")
		return nil
	}
	reply := got.text
	enable := ""
	switch strings.ToLower(strings.TrimSpace(reply)) {
	case "", "y", "yes":
		enable = "true"
	case "n", "no":
		enable = "false"
	default:
		fmt.Fprintln(a.out, "No choice recorded; you will be asked again next time.")
		return nil
	}
	if _, err := daemonconfig.EditFile(a.home, daemonDefaults(), daemonconfig.Edit{Key: "guardian.enabled", Value: enable}); err != nil {
		fmt.Fprintln(a.out, a.paint("31", "Could not save the guardian choice: "+err.Error()))
		return nil
	}
	if enable == "true" {
		fmt.Fprintln(a.out, a.paint("32", "Host guardian enabled in config.toml (guardian.enabled = true)."))
	} else {
		fmt.Fprintln(a.out, "Host guardian left off (guardian.enabled = false). Enable later with: arm config set guardian.enabled true")
	}
	return nil
}
