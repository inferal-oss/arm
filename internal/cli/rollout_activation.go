package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"getarm.org/internal/activation"
	"getarm.org/internal/agents"
	"getarm.org/internal/agentsessions"
	"getarm.org/internal/install"
	"getarm.org/internal/model"
	"getarm.org/internal/rollout"
)

type rolloutCandidate struct {
	agentsessions.Session
	ARMState string `json:"arm_state"`
	ARMLive  bool   `json:"arm_report_recent"`
	Delivery string `json:"activation_delivery"`
}
type rolloutDiscovery struct {
	ReadOnly     bool                         `json:"read_only"`
	Project      rollout.Project              `json:"project"`
	Instructions install.InstructionReadiness `json:"instructions"`
	Sessions     []rolloutCandidate           `json:"sessions"`
	Warnings     []string                     `json:"warnings"`
}

func (a *app) nativeHome(kind, explicit string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	env := "CODEX_HOME"
	if kind == "claude" {
		env = "CLAUDE_CONFIG_DIR"
	}
	if v := os.Getenv(env); v != "" {
		return filepath.Abs(v)
	}
	var roots []string
	for _, t := range install.InspectInstructions(a.home).Targets {
		if t.Adapter == kind && t.State == "ready" {
			roots = append(roots, filepath.Dir(t.Path))
		}
	}
	if len(roots) == 1 {
		return filepath.Abs(roots[0])
	}
	h, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(h, "."+kind), nil
}
func (a *app) sessionSources(adapter, codexHome, claudeHome string, codexCWD ...string) ([]agentsessions.Source, error) {
	if adapter != "all" && adapter != "codex" && adapter != "claude" {
		return nil, fmt.Errorf("adapter must be all, codex or claude")
	}
	out := []agentsessions.Source{}
	if adapter == "all" || adapter == "codex" {
		h, e := a.nativeHome("codex", codexHome)
		if e != nil {
			return nil, e
		}
		bin, _ := agents.Binary("codex")
		native := agentsessions.Codex{Home: h, Binary: bin}
		if len(codexCWD) > 0 && codexCWD[0] != "" {
			root, e := filepath.EvalSymlinks(codexCWD[0])
			if e != nil {
				return nil, e
			}
			root, e = filepath.Abs(root)
			if e != nil {
				return nil, e
			}
			native.Roots = []string{root}
		}
		out = append(out, native)
	}
	if adapter == "all" || adapter == "claude" {
		h, e := a.nativeHome("claude", claudeHome)
		if e != nil {
			return nil, e
		}
		out = append(out, agentsessions.Claude{Home: h})
	}
	return out, nil
}
func (a *app) discoverSessions(project string, sources []agentsessions.Source) (rolloutDiscovery, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	out := rolloutDiscovery{ReadOnly: true, Sessions: []rolloutCandidate{}, Warnings: []string{}, Instructions: install.InspectInstructions(a.home)}
	st, e := (rollout.Store{Home: a.home}).Read()
	if e != nil {
		return out, e
	}
	found := false
	for _, p := range st.Projects {
		if p.ID == project && !p.Removed {
			out.Project = p
			found = true
			break
		}
	}
	if !found {
		return out, fmt.Errorf("select an existing active project with --project; removed and unmatched projects are not discovery targets")
	}
	type scope struct {
		p       *rollout.Project
		blocked string
		err     error
	}
	cache := map[string]scope{}
	seen := map[string]bool{}
	for _, source := range sources {
		if native, ok := source.(agentsessions.Codex); ok {
			explicit := len(native.Roots) > 0
			if len(native.Roots) == 0 {
				for _, b := range out.Project.Bindings {
					if !b.Removed {
						native.Roots = append(native.Roots, b.Root)
					}
				}
				out.Warnings = append(out.Warnings, "Codex discovery queries exact bound working directories; use --codex-cwd for a session working in a nested directory.")
			}
			verified := []string{}
			for _, root := range native.Roots {
				p, blocked, e := rollout.ResolveScopeContext(ctx, st, rollout.Query{CWD: root, Adapter: "codex"})
				if e != nil || p == nil || p.ID != project || blocked != "" {
					if explicit {
						return out, fmt.Errorf("Codex metadata directory is outside a verified active project boundary")
					}
					continue
				}
				verified = append(verified, root)
			}
			if len(verified) == 0 {
				out.Warnings = append(out.Warnings, "No verified Codex project roots are available; review stale boundaries first.")
				continue
			}
			native.Roots = verified
			source = native
		}
		result := source.Discover(ctx)
		out.Warnings = append(out.Warnings, result.Warnings...)
		if len(result.Sessions) > agentsessions.Limit {
			result.Sessions = result.Sessions[:agentsessions.Limit]
			out.Warnings = append(out.Warnings, "Adapter session discovery is truncated.")
		}
		for _, s := range result.Sessions {
			if e := ctx.Err(); e != nil {
				return out, e
			}
			if s.Adapter != source.Name() {
				continue
			}
			var err error
			s, err = agentsessions.Normalize(s)
			if err != nil {
				continue
			}
			key := s.CWD + "\x00" + s.Adapter
			v, ok := cache[key]
			if !ok {
				v.p, v.blocked, v.err = rollout.ResolveScopeContext(ctx, st, rollout.Query{CWD: s.CWD, Adapter: s.Adapter})
				cache[key] = v
			}
			if v.err != nil || v.p == nil || v.p.ID != project || v.blocked != "" {
				continue
			}
			identity := s.Adapter + ":" + s.ID
			if seen[identity] {
				continue
			}
			seen[identity] = true
			c := rolloutCandidate{Session: s, ARMState: "not-identified", Delivery: "copy-only"}
			if s.Adapter == "codex" {
				c.Delivery = "queue-capability-check-required"
			}
			for _, x := range st.Sessions {
				if x.Project == project && x.Adapter == s.Adapter && x.Session == s.ID {
					c.ARMState = x.State
					c.ARMLive = time.Now().Before(x.Expires) && !time.Now().Before(x.LastSeen)
					break
				}
			}
			out.Sessions = append(out.Sessions, c)
			if len(out.Sessions) >= agentsessions.Limit {
				out.Warnings = append(out.Warnings, "Combined session discovery limit reached.")
				return out, nil
			}
		}
	}
	return out, ctx.Err()
}

func (a *app) activationPlan(project, adapter, id string, sources []agentsessions.Source, send bool) (activation.Plan, error) {
	selectedSources := []agentsessions.Source{}
	for _, source := range sources {
		if source.Name() == adapter {
			selectedSources = append(selectedSources, source)
		}
	}
	data, e := a.discoverSessions(project, selectedSources)
	if e != nil {
		return activation.Plan{}, e
	}
	if data.Project.Mode == "disabled" || !data.Instructions.Ready {
		return activation.Plan{}, fmt.Errorf("enable a reviewed project policy and install managed instructions before activation")
	}
	for _, s := range data.Sessions {
		if s.Adapter != adapter || s.ID != id {
			continue
		}
		executable, e := os.Executable()
		if e != nil {
			return activation.Plan{}, e
		}
		for _, r := range executable + a.home {
			if r < 32 || r == 127 {
				return activation.Plan{}, fmt.Errorf("activation command paths contain unsupported control characters")
			}
		}
		prefix := activation.Quote(executable) + " --home " + activation.Quote(a.home)
		p := activation.Plan{Project: project, PolicyRevision: data.Project.Revision, Session: s.Session, Delivery: "copy-only"}
		p.Message = activation.Message(prefix, project, p.PolicyRevision, s.Session)
		if send {
			if adapter != "codex" {
				return p, fmt.Errorf("this adapter is copy-only; active Claude injection is not supported")
			}
			ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
			defer cancel()
			cap := agents.Probe(ctx, "codex")
			if cap.Mode != "queue-existing-session" || cap.Error != "" {
				return p, fmt.Errorf("installed Codex does not expose verified exact-session queue delivery; copy the activation message instead")
			}
			p.Binary = cap.Path
			p.BinarySHA, e = activation.BinaryHash(cap.Path)
			if e != nil {
				return p, e
			}
			p.Delivery = "queue"
		}
		return activation.Seal(p, struct {
			Project      rollout.Project
			Instructions install.InstructionReadiness
		}{data.Project, data.Instructions}), nil
	}
	return activation.Plan{}, fmt.Errorf("exact native session is no longer discoverable in this project; discover again without retargeting")
}

func (a *app) rolloutDiscover(args []string) error {
	f := a.flags("rollout discover")
	project := f.String("project", "", "exact ARM project boundary to inspect")
	adapter := f.String("adapter", "all", "native metadata sources: all, codex or claude")
	codex := f.String("codex-home", "", "explicit existing Codex home; never starts a native server")
	claude := f.String("claude-home", "", "explicit existing Claude session-metadata home")
	codexCWD := f.String("codex-cwd", "", "exact Codex working directory within this project (default: bound roots)")
	f.Bool("json", false, "read-only session metadata JSON; no interactive activation")
	if e := a.parse(f, args, false); e != nil {
		return e
	}
	sources, e := a.sessionSources(*adapter, *codex, *claude, *codexCWD)
	if e != nil {
		return e
	}
	if !a.machine() && a.inputTTY {
		return a.discoverInteractive(*project, sources)
	}
	data, e := a.discoverSessions(*project, sources)
	if e != nil {
		return e
	}
	return a.print(data)
}
func (a *app) rolloutActivate(args []string) error {
	f := a.flags("rollout activate")
	project := f.String("project", "", "reviewed ARM project ID")
	adapter := f.String("adapter", "", "exact discovered adapter")
	id := f.String("session", "", "exact discovered original session UUID")
	codex := f.String("codex-home", "", "explicit existing Codex metadata home")
	claude := f.String("claude-home", "", "explicit existing Claude metadata home")
	codexCWD := f.String("codex-cwd", "", "exact Codex working directory within this project (default: bound roots)")
	send := f.Bool("send", false, "queue the reviewed message to this exact supported Codex session")
	dry := f.Bool("dry-run", false, "preview only; never queues a message")
	expect := f.String("expect", "", "exact activation review hash")
	key := f.String("key", "", "stable delivery key; inspect after uncertain output, never substitute blindly")
	yes := f.Bool("yes", false, "confirm exact recipient and message after review")
	f.Bool("json", false, "machine-readable activation preview or receipt")
	if e := a.parse(f, args, false); e != nil {
		return e
	}
	sources, e := a.sessionSources(*adapter, *codex, *claude, *codexCWD)
	if e != nil {
		return e
	}
	p, e := a.activationPlan(*project, *adapter, *id, sources, *send)
	if e != nil {
		return e
	}
	if !*send || *dry {
		return a.printActivationPlan(p)
	}
	if !*yes || *expect == "" || *expect != p.Review || *key == "" {
		return fmt.Errorf("preview the identical --send request with --dry-run, then supply --expect REVIEW --key KEY --yes after authorizing exact recipient and message")
	}
	return a.sendActivation(p, *key, sources)
}
func (a *app) printActivationPlan(p activation.Plan) error {
	if a.machine() {
		return a.print(p)
	}
	fmt.Fprintf(a.out, "ARM · Activation message\nProject: %s · revision %d\nRecipient: %s · %s\nDirectory: %s\nDelivery: %s\nReview: %s\n\n%s\n", terminalSafe(p.Project), p.PolicyRevision, p.Session.Adapter, p.Session.ID, terminalSafe(p.Session.CWD), p.Delivery, p.Review, activationDisplayMessage(p.Message))
	if p.Delivery == "queue" {
		fmt.Fprintf(a.out, "\nNative CLI: %s\nExecutable SHA256: %s\n", terminalSafe(p.Binary), p.BinarySHA)
	}
	return nil
}
func (a *app) activationUnknown(key string, e error) error {
	return model.Err("activation_outcome_unknown", fmt.Sprintf("activation delivery may have been queued; do not resend or substitute a key. Inspect `%s --key %s`: %v", installNextCommand(a.home, "rollout activation"), key, e))
}
func (a *app) sendActivation(p activation.Plan, key string, sources []agentsessions.Source) error {
	r, e := (activation.Store{Home: a.home}).Send(a.ctx, p, key, func() (activation.Plan, error) {
		return a.activationPlan(p.Project, p.Session.Adapter, p.Session.ID, sources, true)
	})
	if r.State == "unknown" || r.State == "pending" || a.ctx.Err() != nil && r.Key != "" {
		return a.activationUnknown(key, e)
	}
	if e != nil {
		return e
	}
	return a.printActivationReceipt(r)
}
func (a *app) rolloutActivation(args []string) error {
	f := a.flags("rollout activation")
	key := f.String("key", "", "original activation delivery key; lookup never sends")
	f.Bool("json", false, "machine-readable saved delivery outcome")
	if e := a.parse(f, args, false); e != nil {
		return e
	}
	r, e := (activation.Store{Home: a.home}).Lookup(*key)
	if e != nil {
		return e
	}
	return a.printActivationReceipt(r)
}

func (a *app) printActivationReceipt(r activation.Record) error {
	if a.machine() {
		return a.print(r)
	}
	fmt.Fprintf(a.out, "ARM · Activation receipt\nKey: %s\nOutcome: %s\nProject: %s · policy revision %d\nRecipient: %s · %s\nDirectory: %s\nRecorded: %s\n\nQueued is separate from agent identification and enrollment. Pending/unknown must not be retried with a new key.\n\nOriginal message:\n%s\n", terminalSafe(r.Key), terminalSafe(r.State), terminalSafe(r.Project), r.PolicyRevision, terminalSafe(r.Adapter), terminalSafe(r.Session), terminalSafe(r.CWD), r.At.Format(time.RFC3339), activationDisplayMessage(r.Message))
	return nil
}

func activationLookupCommand(home, key string) string {
	return installNextCommand(home, "rollout activation") + " --key " + activation.Quote(key)
}
func shortSession(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return strings.TrimSpace(s)
}
