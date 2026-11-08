package guardian

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/usage"
)

// Contact is a registered agent the guardian may ask to reduce its work.
type Contact struct {
	ID, Adapter, CWD string
}

// Hooks connect the runtime to the manager without an import cycle.
type Hooks struct {
	// Pressure returns the latest monitor pressure, its time, whether it
	// is stale, and host capacity.
	Pressure func() (model.MonitorPressure, time.Time, bool, Host)
	// Managed returns identities of live ARM-supervised process roots.
	Managed  func() []model.ProcessIdentity
	Contacts func() []Contact
	// Notify posts an operator inbox message.
	Notify func(body string) error
	// Ask sends a message to a registered agent contact; key identifies
	// the guardian action, so a retried delivery is not duplicated.
	Ask func(contact, key, body string) error
}

type pendingKill struct {
	action string
	id     model.ProcessIdentity
	due    time.Time
}

type Runtime struct {
	cfg     Config
	hooks   Hooks
	journal *Journal
	protect Protection
	collect func(context.Context) Inventory

	mu      sync.Mutex
	engine  *Engine
	prev    *Inventory
	status  model.GuardianStatus
	kills   []pendingKill
	desktop func(title, body string)
	self    *model.ProcessIdentity
	// deferred hooks run after the tick releases mu, so messaging never
	// blocks status reads or holds this lock while taking the manager's.
	deferred []func()
	failures map[string]int // revert attempts that failed, by action
}

const maxTargets = 512 // bounds one journal line

func New(cfg Config, home string, hooks Hooks) (*Runtime, error) {
	cfg = cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	path := ""
	if home != "" {
		path = filepath.Join(home, "guardian", "actions.jsonl")
	}
	j, err := OpenJournal(path)
	if err != nil {
		return nil, err
	}
	r := &Runtime{cfg: cfg, hooks: hooks, journal: j, engine: NewEngine(cfg), collect: Collect, desktop: desktopNotify, failures: map[string]int{}}
	r.protect = Protection{UID: strconv.Itoa(os.Geteuid()), Self: map[int]bool{os.Getpid(): true}, Extra: cfg.Protect}
	if self, err := usage.Capture(os.Getpid()); err == nil {
		r.self = &self
	}
	r.status = model.GuardianStatus{Mode: cfg.Mode, Role: cfg.Role, Ladder: cfg.Ladder, Stale: true, Groups: []model.GuardianGroup{}, Incidents: []model.GuardianIncident{}, Actions: j.Recent(100)}
	return r, nil
}

// Run samples and acts until ctx ends, then reverts active reversible
// actions. Actions left by an earlier run are reverted first.
//
// Recovery runs even when the guardian is off: disabling it after a crash
// must not leave processes frozen or deprioritized.
func (r *Runtime) Run(ctx context.Context) {
	r.recover()
	if r.cfg.Mode == model.GuardianOff {
		r.recoverOff(ctx)
		return
	}
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		r.tick(ctx)
		select {
		case <-ctx.Done():
			r.revertAll("guardian stopped")
			return
		case <-ticker.C:
		}
	}
}

// recover reverts reversible actions recorded by a previous run whose
// targets still have the same identity. Resuming or restoring priority of
// such a process is safe; nothing else is adopted.
func (r *Runtime) recover() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.journal.Unreverted() {
		if r.revert(a.ID, "reverted after restart", time.Now()) != nil {
			// Retry through maintenance: no incident is open for it.
			r.engine.Adopt(a.ID, a.Step, a.Group, a.At, a.Targets)
		}
	}
}

// recoverOff retries startup recovery a bounded number of times when the
// guardian is disabled and nothing else would retry it.
func (r *Runtime) recoverOff(ctx context.Context) {
	for i := 0; i < 5 && len(r.journal.Unreverted()) > 0; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
		r.mu.Lock()
		for _, a := range r.journal.Unreverted() {
			r.revert(a.ID, "reverted after restart", time.Now())
		}
		r.mu.Unlock()
	}
}

func (r *Runtime) tick(ctx context.Context) {
	for _, f := range r.tickLocked(ctx) {
		f()
	}
}

func (r *Runtime) tickLocked(ctx context.Context) []func() {
	bounded, cancel := context.WithTimeout(ctx, r.cfg.Interval)
	inv := r.collect(bounded)
	cancel()
	pressure, at, stale, host := r.hooks.Pressure()
	var managed []model.ProcessIdentity
	if r.hooks.Managed != nil {
		managed = r.hooks.Managed()
	}
	var contacts []Contact
	if r.hooks.Contacts != nil {
		contacts = r.hooks.Contacts()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() { r.deferred = nil }()
	if time.Since(at) > 3*r.cfg.Interval+6*time.Second {
		stale = true
	}
	var rows []Usage
	if r.prev != nil {
		rows = Rates(inv, *r.prev)
	} else {
		rows = Rates(inv, Inventory{})
	}
	r.prev = &inv
	cands := r.candidates(Groups(rows), inv, managed, contacts)
	now := time.Now()
	for _, d := range r.engine.Step(now, pressure, stale, !inv.Complete, cands, host) {
		r.execute(d, now)
	}
	r.runKills(now)
	r.status = r.snapshot(now, pressure, stale, cands, inv)
	return r.deferred
}

func (r *Runtime) candidates(groups []*Group, inv Inventory, managed []model.ProcessIdentity, contacts []Contact) []*Candidate {
	byPID := make(map[int]Process, len(inv.Processes))
	for _, p := range inv.Processes {
		byPID[p.Identity.PID] = p
	}
	roots, rootGroups := map[model.ProcessIdentity]bool{}, map[string]bool{}
	for _, m := range managed {
		roots[m] = true
		// Supervised work that detaches from its parent usually keeps the
		// lease's process group.
		rootGroups[m.Namespace+":"+strconv.Itoa(m.PGID)] = true
	}
	arm := map[model.ProcessIdentity]bool{}
	if r.self != nil {
		arm[*r.self] = true
	}
	agentCache := map[int][2]string{}
	out := make([]*Candidate, 0, len(groups))
	for _, g := range groups {
		c := &Candidate{Group: g, Protected: r.protect.Group(g)}
		for _, m := range g.Members {
			if c.Protected == "" && underManaged(m.Process, byPID, arm) {
				c.Protected = "ARM or a process it started (" + m.Name + ")"
			}
			if !c.Managed && (rootGroups[m.Identity.Namespace+":"+strconv.Itoa(m.Identity.PGID)] || underManaged(m.Process, byPID, roots)) {
				c.Managed = true
			}
			if browser(m.Process) {
				c.Profile = "chromium"
			}
			if c.Agent == "" && len(contacts) > 0 {
				c.Agent, c.AgentEvidence = r.attribute(m.Process, byPID, contacts, agentCache)
			}
		}
		out = append(out, c)
	}
	return out
}

func underManaged(p Process, byPID map[int]Process, roots map[model.ProcessIdentity]bool) bool {
	if len(roots) == 0 {
		return false
	}
	for i := 0; i < 64; i++ {
		if roots[p.Identity] {
			return true
		}
		parent, ok := byPID[p.PPID]
		if !ok || parent.Identity.PID == p.Identity.PID {
			return false
		}
		p = parent
	}
	return false
}

// attribute finds an agent CLI among the process and its ancestors and
// matches exactly one registered contact by adapter and working directory.
func (r *Runtime) attribute(p Process, byPID map[int]Process, contacts []Contact, cache map[int][2]string) (string, string) {
	for i := 0; i < 64; i++ {
		adapter := agentAdapter(p.Name)
		if adapter != "" {
			if v, ok := cache[p.Identity.PID]; ok {
				return v[0], v[1]
			}
			cwd, match := workingDirectory(p.Identity.PID), ""
			for _, c := range contacts {
				if strings.HasPrefix(c.Adapter, adapter) && cwd != "" && filepath.Clean(c.CWD) == filepath.Clean(cwd) {
					if match != "" {
						match = ""
						break
					}
					match = c.ID
				}
			}
			v := [2]string{}
			if match != "" {
				v = [2]string{match, fmt.Sprintf("descendant of %s process %d in %s", adapter, p.Identity.PID, cwd)}
			}
			cache[p.Identity.PID] = v
			return v[0], v[1]
		}
		parent, ok := byPID[p.PPID]
		if !ok || parent.Identity.PID == p.Identity.PID {
			return "", ""
		}
		p = parent
	}
	return "", ""
}

func agentAdapter(name string) string {
	switch strings.ToLower(name) {
	case "claude":
		return "claude"
	case "codex":
		return "codex"
	}
	return ""
}

func key(id model.ProcessIdentity) string { return strconv.Itoa(id.PID) + "@" + id.Start }

func (r *Runtime) action(d Decision, now time.Time) model.GuardianAction {
	a := model.GuardianAction{ID: local.ID(), At: now.UTC(), Incident: d.Incident, Mode: r.cfg.Mode, Resource: d.Resource, Level: d.Level, Step: d.Step, Usage: d.Usage, Reason: d.Reason, Outcome: d.Outcome, Undoes: d.Undoes}
	if d.Group != nil {
		a.Group, a.GroupName = d.Group.ID, d.Group.Name
	}
	for _, t := range d.Targets {
		a.Targets = append(a.Targets, t.Identity)
	}
	return a
}

func (r *Runtime) execute(d Decision, now time.Time) {
	if d.Undoes != "" {
		r.revert(d.Undoes, d.Reason, now)
		return
	}
	if d.Extends != "" {
		r.extend(d, now)
		return
	}
	if len(d.Targets) > maxTargets {
		// Act only on identities the journal records, so every target
		// can be reverted.
		d.Targets = d.Targets[:maxTargets]
	}
	a := r.action(d, now)
	if a.Outcome == "" && !d.Execute {
		a.Outcome = model.GuardianPlanned
	}
	var priorities map[string]Priority
	if a.Outcome == "" {
		a.Outcome, a.Detail, priorities = r.apply(d, &a, now)
	}
	if err := r.journal.Save(a, priorities); err != nil {
		a.Detail = strings.TrimPrefix(a.Detail+"; journal: "+err.Error(), "; ")
	}
	if (a.Step == model.GuardianDeprioritize || a.Step == model.GuardianFreeze) && (a.Outcome == model.GuardianApplied || a.Outcome == model.GuardianPartial) {
		r.engine.Applied(a.ID, d, now, a.Targets)
	}
	if d.Execute && d.Step != model.GuardianNotify && d.Step != model.GuardianAsk && r.hooks.Notify != nil && a.Outcome != model.GuardianSkipped {
		body := fmt.Sprintf("ARM host guardian %s %s: %s. Outcome: %s. %s", a.Step, a.GroupName, a.Reason, a.Outcome, undoHint(a))
		r.deferred = append(r.deferred, func() { r.hooks.Notify(body) })
	}
}

func undoHint(a model.GuardianAction) string {
	if a.Reversible {
		return "Undo with: arm guardian undo " + a.ID
	}
	return ""
}

// apply performs a step. Each target is revalidated immediately before
// its signal or priority change.
func (r *Runtime) apply(d Decision, a *model.GuardianAction, now time.Time) (string, string, map[string]Priority) {
	switch d.Step {
	case model.GuardianNotify:
		body := r.notifyBody(d)
		if r.cfg.Desktop && r.desktop != nil {
			r.desktop("ARM host guardian", body)
		}
		if r.hooks.Notify == nil {
			return model.GuardianApplied, "desktop notification only", nil
		}
		r.later(a.ID, "", func() error { return r.hooks.Notify(body) })
		return model.GuardianPending, "", nil
	case model.GuardianAsk:
		if r.hooks.Ask == nil {
			return model.GuardianSkipped, "agent messaging unavailable", nil
		}
		body := fmt.Sprintf("ARM host guardian: %s pressure is %s. %s, attributed to your session (%s), uses %s. Please reduce or stop this work now. If pressure continues, the guardian's next steps are: %s.", d.Resource, d.Level, d.Group.Name, d.Group.AgentEvidence, d.Usage, r.nextSteps(model.GuardianAsk))
		agent, id := d.Group.Agent, a.ID
		r.later(a.ID, "asked agent "+agent, func() error { return r.hooks.Ask(agent, "guardian-"+id, body) })
		return model.GuardianPending, "asking agent " + agent, nil
	case model.GuardianDeprioritize:
		r.intent(*a)
		priorities := map[string]Priority{}
		ok, failed := 0, []string{}
		for _, t := range d.Targets {
			p, err := lower(t.Identity)
			if err != nil {
				failed = append(failed, fmt.Sprintf("%d: %v", t.Identity.PID, err))
				continue
			}
			priorities[key(t.Identity)] = p
			ok++
		}
		a.Reversible = ok > 0
		return outcome(ok, len(d.Targets)), detail(failed), priorities
	case model.GuardianShed, model.GuardianTerminate, model.GuardianFreeze:
		sig := syscall.SIGTERM
		if d.Step == model.GuardianFreeze {
			sig = syscall.SIGSTOP
			// A process the user already stopped stays theirs: freezing
			// and later thawing it would resume a job they paused. Filter
			// before the intent, so crash recovery cannot resume it either.
			var kept []Process
			var ids []model.ProcessIdentity
			for _, t := range d.Targets {
				if !t.Stopped {
					kept = append(kept, t)
					ids = append(ids, t.Identity)
				}
			}
			d.Targets, a.Targets = kept, ids
			r.intent(*a)
		}
		ok, failed := 0, []string{}
		for _, t := range d.Targets {
			if err := Signal(t.Identity, sig); err != nil {
				failed = append(failed, fmt.Sprintf("%d: %v", t.Identity.PID, err))
				continue
			}
			ok++
			if sig == syscall.SIGTERM {
				// A frozen process cannot handle termination until resumed.
				Signal(t.Identity, syscall.SIGCONT)
				r.kills = append(r.kills, pendingKill{action: a.ID, id: t.Identity, due: now.Add(r.cfg.TerminateGrace)})
			}
		}
		text := detail(failed)
		if d.Step == model.GuardianFreeze {
			a.Reversible = ok > 0
			if d.Resource == "memory" && ok > 0 {
				if msg, err := reclaim(d.Group.Group); err == nil {
					text = strings.TrimPrefix(text+"; "+msg, "; ")
				}
			}
		}
		return outcome(ok, len(d.Targets)), text, nil
	}
	return model.GuardianSkipped, "unknown step", nil
}

// intent records a reversible action before any signal, so a crash during
// a large freeze leaves a record that recovery can revert.
func (r *Runtime) intent(a model.GuardianAction) {
	a.Outcome, a.Reversible = model.GuardianPending, true
	r.journal.Save(a, nil)
}

// later runs a messaging hook after the tick, then records its outcome.
func (r *Runtime) later(id, success string, hook func() error) {
	r.deferred = append(r.deferred, func() {
		err := hook()
		r.mu.Lock()
		defer r.mu.Unlock()
		a, _, ok := r.journal.Get(id)
		if !ok {
			return
		}
		a.Outcome, a.Detail = model.GuardianApplied, success
		if err != nil {
			a.Outcome, a.Detail = model.GuardianFailed, err.Error()
		}
		r.journal.Save(a, nil)
		r.status.Actions = r.journal.Recent(100)
	})
}

func outcome(ok, total int) string {
	switch {
	case total == 0:
		return model.GuardianSkipped
	case ok == total:
		return model.GuardianApplied
	case ok > 0:
		return model.GuardianPartial
	}
	return model.GuardianFailed
}

func detail(failed []string) string {
	if len(failed) == 0 {
		return ""
	}
	if len(failed) > 5 {
		failed = append(failed[:5], fmt.Sprintf("%d more", len(failed)-5))
	}
	return "not applied to " + strings.Join(failed, ", ")
}

// extend applies a sticky deprioritization to new members of the group.
func (r *Runtime) extend(d Decision, now time.Time) {
	a, priorities, ok := r.journal.Get(d.Extends)
	if !ok || a.RevertedAt != nil {
		return
	}
	if priorities == nil {
		priorities = map[string]Priority{}
	}
	// Drop targets that have exited, so a group that keeps spawning
	// short-lived processes does not grow the record without bound.
	var live []model.ProcessIdentity
	for _, t := range a.Targets {
		if sameProcess(t) == nil {
			live = append(live, t)
		} else {
			delete(priorities, key(t))
		}
	}
	a.Targets = live
	var added []model.ProcessIdentity
	for _, t := range d.Targets {
		if len(a.Targets) >= maxTargets {
			break
		}
		if p, err := lower(t.Identity); err == nil {
			priorities[key(t.Identity)] = p
			a.Targets = append(a.Targets, t.Identity)
		}
		// Record attempts either way, so refused members are not retried
		// every tick.
		added = append(added, t.Identity)
	}
	r.engine.Applied(a.ID, d, now, added)
	// Remember every current member, including those over the cap or
	// refused, so none counts as new on the next tick.
	keep := append([]model.ProcessIdentity(nil), a.Targets...)
	for _, m := range d.Group.Members {
		keep = append(keep, m.Identity)
	}
	r.engine.Prune(a.ID, keep)
	r.journal.Save(a, priorities)
}

// revert undoes a reversible action and records the undo as its own action.
func (r *Runtime) revert(id, reason string, now time.Time) error {
	a, priorities, ok := r.journal.Get(id)
	if !ok {
		return model.Err("not_found", "guardian action not found")
	}
	if !a.Reversible || a.RevertedAt != nil {
		r.engine.Release(id)
		return model.Err("invalid_request", "guardian action is not active and reversible")
	}
	undo := model.GuardianAction{ID: local.ID(), At: now.UTC(), Incident: a.Incident, Mode: r.cfg.Mode, Resource: a.Resource, Group: a.Group, GroupName: a.GroupName, Targets: a.Targets, Reason: reason, Undoes: id}
	ok2, gone, kept, failed := 0, 0, 0, []string{}
	for _, t := range a.Targets {
		var err error
		if a.Step == model.GuardianFreeze {
			undo.Step = model.GuardianThaw
			err = Thaw(t)
		} else {
			undo.Step = model.GuardianRestore
			p, found := priorities[key(t)]
			if !found {
				// An intent recorded before a crash has no saved values;
				// restore only what this platform can infer.
				p = inferredPriority()
			}
			err = restore(t, p)
		}
		if errors.Is(err, errIdentity) {
			gone++
			continue
		}
		if errors.Is(err, errNiceKept) {
			kept++
			ok2++
			continue
		}
		if err != nil {
			failed = append(failed, fmt.Sprintf("%d: %v", t.PID, err))
			continue
		}
		ok2++
	}
	if len(failed) > 0 {
		r.failures[id]++
		if r.failures[id] < 5 {
			// Keep the action active; maintenance retries the revert.
			return model.Err("revert_incomplete", detail(failed))
		}
		undo.Detail = "gave up after repeated failures; "
	}
	delete(r.failures, id)
	if gone > 0 {
		undo.Detail += fmt.Sprintf("%d targets had exited; ", gone)
	}
	if kept > 0 {
		undo.Detail += fmt.Sprintf("%d targets keep nice %d without privilege; ", kept, lowNiceValue)
	}
	undo.Outcome, undo.Detail = outcome(ok2, ok2+len(failed)), undo.Detail+detail(failed)
	if undo.Outcome == model.GuardianSkipped {
		undo.Outcome, undo.Detail = model.GuardianApplied, "targets have exited"
	}
	t := now.UTC()
	a.RevertedAt, a.RevertedBy = &t, undo.ID
	r.journal.Save(a, nil)
	r.journal.Save(undo, nil)
	r.engine.Release(id)
	return nil
}

func (r *Runtime) revertAll(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range r.engine.ActiveIDs() {
		r.revert(id, reason, time.Now())
	}
}

// Undo reverts one action on operator request.
func (r *Runtime) Undo(id string) (model.GuardianAction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.revert(id, "undone by operator", time.Now()); err != nil {
		return model.GuardianAction{}, err
	}
	a, _, _ := r.journal.Get(id)
	// The operator overrode the guardian for this program; do not
	// escalate against it again during the open incident.
	r.engine.Spare(a.Group)
	r.status.Actions = r.journal.Recent(100)
	return a, nil
}

// runKills forces termination of processes that outlived the grace period.
func (r *Runtime) runKills(now time.Time) {
	keep := r.kills[:0]
	for _, k := range r.kills {
		if now.Before(k.due) {
			keep = append(keep, k)
			continue
		}
		if Signal(k.id, syscall.SIGKILL) == nil {
			if a, _, ok := r.journal.Get(k.action); ok {
				a.Detail = strings.TrimPrefix(a.Detail+fmt.Sprintf("; forced termination of %d after %s", k.id.PID, r.cfg.TerminateGrace), "; ")
				r.journal.Save(a, nil)
			}
		}
	}
	r.kills = keep
}

func (r *Runtime) nextSteps(after string) string {
	var out []string
	seen := false
	for _, s := range r.cfg.Ladder {
		if seen {
			out = append(out, s.Step)
		}
		seen = seen || s.Step == after
	}
	if len(out) == 0 || r.cfg.Mode != model.GuardianAct {
		return "further notifications only"
	}
	return strings.Join(out, ", ")
}

func (r *Runtime) notifyBody(d Decision) string {
	b := fmt.Sprintf("Host %s pressure is %s (%s).", d.Resource, d.Level, d.Reason)
	if d.Group != nil {
		b += fmt.Sprintf(" Heaviest program: %s, using %s.", d.Group.Name, d.Usage)
		if d.Group.Protected != "" {
			b += " It is protected (" + d.Group.Protected + ") and will not be acted on."
		}
	}
	return b + " Next steps if it continues: " + r.nextSteps(model.GuardianNotify) + "."
}

func (r *Runtime) snapshot(now time.Time, pressure model.MonitorPressure, stale bool, cands []*Candidate, inv Inventory) model.GuardianStatus {
	s := model.GuardianStatus{Mode: r.cfg.Mode, Role: r.cfg.Role, Ladder: r.cfg.Ladder, At: now.UTC(), Stale: stale, Pressure: pressure, Incidents: r.engine.Incidents(), Actions: r.journal.Recent(100), Budget: model.GuardianBudget{Used: r.engine.BudgetUsed(now), Limit: r.cfg.Budget, Window: time.Hour}, Inventory: inv.Detail}
	shown := map[string]bool{}
	for _, res := range resources {
		for i, c := range ranked(res, cands) {
			if i == 6 {
				break
			}
			if shown[c.ID] || metric(res, c.Group) <= 0 {
				continue
			}
			shown[c.ID] = true
			s.Groups = append(s.Groups, view(c, r.engine.ActiveSteps(c.ID)))
		}
	}
	sort.SliceStable(s.Groups, func(i, j int) bool { return rate(s.Groups[i].CPUCores) > rate(s.Groups[j].CPUCores) })
	if s.Groups == nil {
		s.Groups = []model.GuardianGroup{}
	}
	return s
}

func view(c *Candidate, state []string) model.GuardianGroup {
	g := model.GuardianGroup{ID: c.ID, Name: c.Name, Kind: c.Kind, UID: c.UID, Processes: len(c.Members), Protected: c.Protected, Managed: c.Managed, Agent: c.Agent, AgentEvidence: c.AgentEvidence, Profile: c.Profile, State: state, Top: []model.GuardianProcess{}}
	if c.Known {
		cpu, disk := c.CPU, c.Disk
		g.CPUCores, g.DiskBytesPerSecond = &cpu, &disk
	}
	mem := c.Memory
	g.MemoryBytes = &mem
	for i, m := range c.Members {
		if i == 5 {
			break
		}
		g.Top = append(g.Top, model.GuardianProcess{Identity: m.Identity, PPID: m.PPID, Name: m.Name, Executable: m.Executable, Kind: m.Kind, CPUCores: m.CPUCores, MemoryBytes: m.Memory, DiskBytesPerSecond: m.DiskRate})
	}
	return g
}

// Status returns the latest guardian state.
func (r *Runtime) Status() model.GuardianStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// desktopNotify posts a best-effort, bounded desktop notification.
func desktopNotify(title, body string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		switch runtime.GOOS {
		case "darwin":
			script := fmt.Sprintf("display notification %s with title %s", appleScriptString(body), appleScriptString(title))
			exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script).Run()
		case "linux":
			if path, err := exec.LookPath("notify-send"); err == nil {
				exec.CommandContext(ctx, path, "--app-name=ARM", title, body).Run()
			}
		}
	}()
}

func appleScriptString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ").Replace(s) + `"`
}
