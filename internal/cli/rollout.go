package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"getarm.org/internal/install"
	"getarm.org/internal/rollout"
)

type rolloutStatus struct {
	Default      string                       `json:"default"`
	Revision     int                          `json:"revision"`
	Instructions install.InstructionReadiness `json:"instructions"`
	Projects     []rollout.Project            `json:"projects"`
	Sessions     []rollout.Participation      `json:"sessions"`
	Suggestion   string                       `json:"suggestion"`
	NextCommand  string                       `json:"next_command"`
	Boundaries   []rollout.BoundaryStatus     `json:"boundaries"`
}

func (a *app) rollout(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("rollout requires status, check, history, project, bind, policy, pause, resume, invite, revoke, identify or ack")
	}
	action, args := args[0], args[1:]
	switch action {
	case "discover":
		return a.rolloutDiscover(args)
	case "activate":
		return a.rolloutActivate(args)
	case "activation":
		return a.rolloutActivation(args)
	}
	if action == "project" && len(args) > 0 && args[0] == "onboard" {
		return a.rolloutOnboard(args[1:])
	}
	command := "rollout " + action
	if (action == "project" || action == "binding") && len(args) > 0 && args[0] == "remove" {
		command += " remove"
		action, args = "remove-"+action, args[1:]
	}
	f := a.flags(command)
	var project, path, adapter, clientProject, session string
	f.StringVar(&project, "project", "", "exact ARM project ID")
	if action == "check" || action == "identify" || action == "ack" {
		f.StringVar(&path, "cwd", "", "working directory (default: current directory)")
		f.StringVar(&session, "session", "", "exact original session UUID")
	}
	if action == "check" || action == "identify" || action == "ack" || action == "bind" || action == "remove-binding" {
		f.StringVar(&adapter, "adapter", "", "codex, claude or generic")
		f.StringVar(&clientProject, "client-project", "", "explicit client project ID; never discovered automatically")
	}
	var kind, root, mode, reason, selected, expect string
	var dry, yes bool
	mutation := action == "project" || action == "bind" || action == "policy" || action == "pause" || action == "resume" || action == "invite" || action == "revoke" || action == "remove-project" || action == "remove-binding"
	if mutation {
		if action == "project" {
			f.StringVar(&kind, "kind", "directory", "directory or git checkout binding")
			f.StringVar(&root, "path", "", "explicit scope directory; git selects its checkout root")
		}
		if action == "policy" {
			f.StringVar(&mode, "mode", "", "disabled, observe, new-sessions (suggested), or all")
		}
		if action == "remove-binding" {
			f.StringVar(&root, "path", "", "exact recorded absolute root, including missing paths; alternatively select --adapter and --client-project")
		}
		f.StringVar(&reason, "reason", "", "required reason for this policy decision")
		if action == "invite" || action == "revoke" {
			f.StringVar(&selected, "identities", "", "comma-separated exact roster identities for a cohort")
		}
		f.StringVar(&expect, "expect", "", "review_sha256 from the identical preview")
		f.BoolVar(&dry, "dry-run", false, "preview only; creates no state or lock")
		f.BoolVar(&yes, "yes", false, "apply the explicitly authorized exact review")
	}
	var started, needs, capabilities, identity string
	var protocol, revision int
	if action == "identify" {
		f.StringVar(&started, "started-at", "", "original session start in RFC3339; unknown stays ineligible for new-session adoption")
		f.StringVar(&needs, "needs", "", "bounded description of resource needs; no secrets")
		f.StringVar(&capabilities, "capabilities", "", "comma-separated coordination capabilities")
		f.IntVar(&protocol, "protocol", rollout.Protocol, "supported adoption protocol version")
	}
	if action == "ack" {
		f.StringVar(&identity, "identity", "", "exact current identity returned by identify")
		f.IntVar(&revision, "policy-revision", 0, "exact current project policy revision")
	}
	f.Bool("json", false, "force JSON output (default: automatic)")
	var includeRemoved bool
	if action == "status" {
		f.BoolVar(&includeRemoved, "include-removed", false, "include removed project history and retained boundary fences")
	}
	if e := a.parse(f, args, false); e != nil {
		return e
	}
	store := rollout.Store{Home: a.home}
	if mutation {
		c := rollout.Change{Action: action, Project: project, Mode: mode, Reason: reason}
		if action == "project" {
			if root == "" {
				return fmt.Errorf("project requires --path")
			}
			c.Binding = &rollout.Binding{Kind: kind, Root: root}
		}
		if action == "bind" {
			c.Client = &rollout.ClientBinding{Adapter: adapter, ID: clientProject}
		}
		if action == "remove-binding" {
			if root != "" {
				c.Binding = &rollout.Binding{Root: root}
			}
			if adapter != "" || clientProject != "" {
				c.Client = &rollout.ClientBinding{Adapter: adapter, ID: clientProject}
			}
		}
		if selected != "" {
			c.Sessions = strings.Split(selected, ",")
		}
		plan, e := store.Preview(c)
		if e != nil {
			return e
		}
		if dry {
			return a.printRollout(plan)
		}
		if !yes || expect == "" {
			return fmt.Errorf("preview with --dry-run; applying requires --expect REVIEW_SHA256 --yes after user authorization")
		}
		if e := a.ctx.Err(); e != nil {
			return e
		}
		result, e := store.Apply(c, expect)
		if a.ctx.Err() != nil {
			return rolloutOutcomeUnknown(a.home, a.ctx.Err())
		}
		if e != nil {
			return e
		}
		return a.printRollout(result)
	}
	if action == "status" || action == "history" {
		st, e := store.Read()
		if e != nil {
			return e
		}
		if action == "history" {
			history := []rollout.Event{}
			for _, event := range st.History {
				if project == "" || event.Project == project {
					history = append(history, event)
				}
			}
			return a.printRollout(history)
		}
		projects := []rollout.Project{}
		for _, p := range st.Projects {
			if (includeRemoved || !p.Removed) && (project == "" || p.ID == project) {
				projects = append(projects, p)
			}
		}
		sessions := store.Roster(st, project)
		status := rolloutStatus{Default: "disabled", Revision: st.Revision, Instructions: install.InspectInstructions(a.home), Projects: projects, Sessions: sessions, Suggestion: "Create a project boundary, then choose new-sessions (suggested), all, or observe for a manual pilot. Installation never enrolls sessions.", NextCommand: installNextCommand(a.home, "rollout project onboard")}
		status.Boundaries = rollout.InspectBoundaries(a.ctx, projects)
		if !status.Instructions.Ready {
			status.Suggestion = "Review or install managed instructions before starting a rollout. Saved policy and existing resource obligations are unchanged."
			status.NextCommand = installNextCommand(a.home, "install")
		} else if len(projects) > 0 {
			status.Suggestion = "Review project boundaries and cohorts before expanding. Installation never enrolls sessions."
			if len(projects)==1&&!projects[0].Removed&&projects[0].Mode!="disabled" {
				status.Suggestion="Project enabled. Discover existing sessions and explicitly copy or send an activation message; installation does not identify or enroll agents."
				status.NextCommand=installNextCommand(a.home,"rollout discover --project "+projects[0].ID)
			}
		}
		for _, boundary := range status.Boundaries {
			if boundary.State == "stale" || boundary.State == "unknown" {
				status.Suggestion = "Some boundaries are stale or unverified. Review their recorded paths, explicitly rebind, or remove them; installation readiness does not make these scopes usable."
				break
			}
		}
		return a.printRollout(status)
	}
	if action != "check" && action != "identify" && action != "ack" {
		return fmt.Errorf("unknown rollout action %q", action)
	}
	if path == "" {
		var e error
		path, e = os.Getwd()
		if e != nil {
			return e
		}
	}
	q := rollout.Query{CWD: path, Adapter: adapter, ClientProject: clientProject, Session: session}
	// --project is an assertion, never a way to bypass path resolution.
	d, e := store.Check(q)
	if e != nil {
		return e
	}
	if project != "" && (d.Project == nil || d.Project.ID != project) {
		return fmt.Errorf("selected project does not match resolved scope")
	}
	switch action {
	case "check":
		return a.printRollout(d)
	case "identify":
		var at time.Time
		if started != "" {
			at, e = time.Parse(time.RFC3339Nano, started)
			if e != nil {
				return e
			}
		}
		caps := []string{}
		if capabilities != "" {
			caps = strings.Split(capabilities, ",")
		}
		d, e = store.Identify(rollout.Identification{Query: q, Started: at, Protocol: protocol, Needs: needs, Capabilities: caps})
	case "ack":
		d, e = store.Acknowledge(q, identity, revision)
	}
	if e != nil {
		return e
	}
	return a.printRollout(d)
}

// Keep the CLI-first policy surface readable without teaching generic resource
// renderers about adoption types. JSON strings escape terminal control bytes.
func (a *app) printRollout(value any) error {
	if a.machine() {
		return a.print(value)
	}
	if status, ok := value.(rolloutStatus); ok {
		return a.rolloutStatusHuman(status)
	}
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	_, e = fmt.Fprintln(a.out, string(b))
	return e
}

func (a *app) rolloutStatusHuman(status rolloutStatus) error {
	var b strings.Builder
	fmt.Fprintln(&b, a.paint("1;36", "ARM · Agent rollout"))
	readiness := "Not ready"
	color := "33"
	if status.Instructions.Ready {
		readiness = "Ready"
		color = "32"
	} else if status.Instructions.State == "partial" {
		readiness = "Partially ready"
	} else if status.Instructions.State == "unknown" {
		readiness = "Unknown"
	}
	fmt.Fprintln(&b, "\nInstructions: "+a.paint(color, readiness))
	fmt.Fprintln(&b, "  "+terminalSafe(status.Instructions.Summary))
	for _, target := range status.Instructions.Targets {
		fmt.Fprintln(&b, "  "+label(target.Adapter)+": "+label(target.State)+" · "+terminalSafe(target.Path))
		fmt.Fprintln(&b, "    "+terminalSafe(target.Detail))
	}
	fmt.Fprintln(&b, "\nUnconfigured projects: Disabled")
	fmt.Fprintln(&b, "\nProjects")
	if len(status.Projects) == 0 {
		fmt.Fprintln(&b, "  None configured.")
	}
	modes := map[string]string{"disabled": "Disabled", "observe": "Discovery only (manual enrollment)", "new-sessions": "New sessions only", "all": "All sessions in project"}
	for _, project := range status.Projects {
		state := "enrollment open"
		if project.Mode == "disabled" {
			state = "enrollment disabled"
		} else if project.Paused {
			state = "enrollment paused"
		}
		ready, unavailable := 0, 0
		for _, boundary := range status.Boundaries {
			if boundary.Project != project.ID {
				continue
			}
			if boundary.State == "ready" {
				ready++
			} else if boundary.State != "removed" {
				unavailable++
			}
		}
		if project.Removed {
			state = "REMOVED · history retained"
		} else if len(status.Boundaries) > 0 && ready == 0 {
			state = "BLOCKED · no verified active boundary"
		} else if unavailable > 0 {
			state += " · boundary warnings"
		}
		fmt.Fprintf(&b, "  %s · %s · %s\n", terminalSafe(project.ID), modes[project.Mode], state)
		fmt.Fprintf(&b, "    Policy revision: %d\n", project.Revision)
		if project.Mode == "new-sessions" {
			fmt.Fprintln(&b, "    Sessions started after: "+project.NewAfter.Format(time.RFC3339))
		}
		for _, binding := range project.Bindings {
			kind := "Directory"
			if binding.Kind == "git" {
				kind = "Git checkout"
			}
			label := ""
			if binding.Removed || project.Removed {
				label = " [REMOVED]"
			}
			fmt.Fprintln(&b, "    "+kind+label+": "+terminalSafe(binding.Root))
			for _, boundary := range status.Boundaries {
				if boundary.Project == project.ID && boundary.Binding == binding && boundary.State != "ready" {
					fmt.Fprintln(&b, "      "+strings.ToUpper(boundary.State)+": "+terminalSafe(boundary.Detail))
				}
			}
		}
		for _, client := range project.Clients {
			fmt.Fprintln(&b, "    "+label(client.Adapter)+" project: "+terminalSafe(client.ID))
		}
	}
	fmt.Fprintln(&b, "\nSessions")
	if len(status.Sessions) == 0 {
		fmt.Fprintln(&b, "  None reported.")
	}
	for _, session := range status.Sessions {
		report := "report stale or uncertain"
		if session.Live {
			report = "recent report"
		}
		coordination := "not coordinating"
		if session.Coordinate {
			coordination = "coordinating"
		}
		fmt.Fprintln(&b, "  "+terminalSafe(session.Project)+" · "+label(session.Adapter)+" · "+terminalSafe(session.Session.Session))
		fmt.Fprintln(&b, "    "+label(session.State)+" · "+report+" · "+coordination)
		fmt.Fprintln(&b, "    Needs: "+terminalSafe(session.Needs))
		fmt.Fprintln(&b, "    Eligibility: "+terminalSafe(session.Why))
		fmt.Fprintln(&b, "    Invite/revoke identity: "+terminalSafe(session.Identity))
	}
	fmt.Fprintln(&b, "\n"+terminalSafe(status.Suggestion))
	fmt.Fprintln(&b, "Next: `"+terminalSafe(status.NextCommand)+"`")
	_, e := fmt.Fprint(a.out, b.String())
	return e
}
