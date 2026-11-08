package guardian

import (
	"fmt"
	"math"
	"sort"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

var resources = []string{"cpu", "memory", "io"}

// Candidate is a group with the attributes that decide eligibility.
type Candidate struct {
	*Group
	Protected     string
	Managed       bool
	Agent         string
	AgentEvidence string
	Profile       string
}

func (c *Candidate) eligible() bool { return c.Protected == "" && !c.Managed && c.Known }

// Host carries capacity facts for significance thresholds.
type Host struct {
	CPUs        int
	MemoryTotal uint64
}

// Decision is one planned step. The runtime executes it when Execute is
// set and records it either way.
type Decision struct {
	Step, Resource, Level, Incident string
	Group                           *Candidate
	Targets                         []Process
	Execute                         bool
	Reason                          string
	Usage                           string
	Outcome                         string // preset for skipped decisions
	Undoes                          string // revert of this action
	Extends                         string // adds new group members to this action
}

type incident struct {
	id, resource, level, peak string
	since, recovering         time.Time
	ladderStart               time.Time // step delays count from here; restarts move it
	lastApplied               time.Time
	next                      int
	acted                     bool // a process step ran or was planned in this ladder pass
	group, lastStep           string
	held                      string          // last reason the ladder could not advance
	spared                    map[string]bool // groups an operator undid in this incident
}

type active struct {
	id, incident, step, group string
	at                        time.Time
	targets                   map[model.ProcessIdentity]bool
}

// Engine is the pure, single-threaded guardian decision core. It performs
// no I/O; the runtime supplies observations and reports applied actions.
type Engine struct {
	cfg       Config
	incidents map[string]*incident
	applied   []time.Time
	active    map[string]*active
}

func NewEngine(cfg Config) *Engine {
	return &Engine{cfg: cfg, incidents: map[string]*incident{}, active: map[string]*active{}}
}

func metric(resource string, g *Group) float64 {
	switch resource {
	case "cpu":
		return g.CPU
	case "memory":
		return float64(g.Memory)
	}
	return g.Disk
}

func significant(resource string, v float64, h Host) bool {
	switch resource {
	case "cpu":
		return v >= math.Max(1, 0.15*float64(h.CPUs))
	case "memory":
		total := float64(h.MemoryTotal)
		if total == 0 {
			total = 10 << 30
		}
		return v >= 0.1*total
	}
	return v >= 8<<20
}

func usageText(resource string, v float64) string {
	switch resource {
	case "cpu":
		return fmt.Sprintf("%.1f cores", v)
	case "memory":
		return fmt.Sprintf("%.1f GiB", v/(1<<30))
	}
	return fmt.Sprintf("%.1f MiB/s", v/(1<<20))
}

func levelOf(p model.MonitorPressure, resource string) string {
	switch resource {
	case "cpu":
		return p.CPU.Level
	case "memory":
		return p.Memory.Level
	}
	return p.IO.Level
}
func reasonOf(p model.MonitorPressure, resource string) string {
	switch resource {
	case "cpu":
		return p.CPU.Reason
	case "memory":
		return p.Memory.Reason
	}
	return p.IO.Reason
}

// ranked orders candidates by a resource, busiest first.
func ranked(resource string, cands []*Candidate) []*Candidate {
	out := append([]*Candidate(nil), cands...)
	sort.SliceStable(out, func(i, j int) bool { return metric(resource, out[i].Group) > metric(resource, out[j].Group) })
	return out
}

func (e *Engine) budgetLeft(now time.Time) int {
	keep := e.applied[:0]
	for _, t := range e.applied {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	e.applied = keep
	return e.cfg.Budget - len(keep)
}

// Step advances incidents for one observation. Stale or unknown pressure
// holds every incident unchanged: unknown evidence neither escalates nor
// recovers.
//
// A partial inventory may miss protected members, so it holds escalation
// and sticky extension but still permits recovery.
func (e *Engine) Step(now time.Time, pressure model.MonitorPressure, stale, partial bool, cands []*Candidate, host Host) []Decision {
	var out []Decision
	byID := map[string]*Candidate{}
	for _, c := range cands {
		byID[c.ID] = c
	}
	out = append(out, e.maintain(now, byID, partial)...)
	if stale {
		return out
	}
	for _, res := range resources {
		level := levelOf(pressure, res)
		rank, known := model.PressureRank(level)
		inc := e.incidents[res]
		if !known {
			continue
		}
		if rank >= 2 && inc == nil {
			inc = &incident{id: local.ID(), resource: res, since: now, ladderStart: now, level: level, peak: level, spared: map[string]bool{}}
			e.incidents[res] = inc
		}
		if inc == nil {
			continue
		}
		inc.level = level
		if r, _ := model.PressureRank(inc.peak); rank > r {
			inc.peak = level
		}
		if rank <= 1 {
			if inc.recovering.IsZero() {
				inc.recovering = now
			}
			if now.Sub(inc.recovering) >= e.cfg.Recover {
				out = append(out, e.close(inc)...)
			}
			continue
		}
		inc.recovering = time.Time{}
		if partial {
			continue
		}
		out = append(out, e.escalate(now, inc, rank, reasonOf(pressure, res), ranked(res, cands), host)...)
	}
	return out
}

// maintain thaws groups frozen longer than the configured maximum, retries
// reverts whose incident has closed, and extends sticky deprioritization to
// new members of a still-eligible group that keeps an original target.
func (e *Engine) maintain(now time.Time, byID map[string]*Candidate, partial bool) []Decision {
	var out []Decision
	open := map[string]bool{}
	for _, inc := range e.incidents {
		open[inc.id] = true
	}
	ids := make([]string, 0, len(e.active))
	for id := range e.active {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := e.active[id]
		revert := model.GuardianRestore
		if a.step == model.GuardianFreeze {
			revert = model.GuardianThaw
		}
		if a.step == model.GuardianFreeze && now.Sub(a.at) >= e.cfg.FreezeMax {
			out = append(out, Decision{Step: model.GuardianThaw, Incident: a.incident, Group: byID[a.group], Execute: true, Undoes: id, Reason: "frozen for the maximum freeze period"})
			continue
		}
		if !open[a.incident] {
			out = append(out, Decision{Step: revert, Incident: a.incident, Group: byID[a.group], Execute: true, Undoes: id, Reason: "retrying the revert of a closed incident"})
			continue
		}
		g := byID[a.group]
		if partial || a.step != model.GuardianDeprioritize || g == nil || !g.eligible() {
			continue
		}
		var fresh []Process
		original := false
		for _, m := range g.Members {
			if a.targets[m.Identity] {
				original = true
			} else {
				fresh = append(fresh, m.Process)
			}
		}
		if original && len(fresh) > 0 {
			out = append(out, Decision{Step: model.GuardianDeprioritize, Incident: a.incident, Group: g, Targets: fresh, Execute: true, Extends: id, Reason: "new members of a deprioritized group"})
		}
	}
	return out
}

func (e *Engine) close(inc *incident) []Decision {
	delete(e.incidents, inc.resource)
	var out []Decision
	ids := []string{}
	for id, a := range e.active {
		if a.incident == inc.id {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		step := model.GuardianRestore
		if e.active[id].step == model.GuardianFreeze {
			step = model.GuardianThaw
		}
		out = append(out, Decision{Step: step, Resource: inc.resource, Level: inc.level, Incident: inc.id, Execute: true, Undoes: id, Reason: "pressure recovered"})
	}
	return out
}

func (e *Engine) escalate(now time.Time, inc *incident, rank int, reason string, cands []*Candidate, host Host) []Decision {
	ladder := e.cfg.Ladder
	if inc.next >= len(ladder) {
		// The ladder is exhausted. When this pass took a process step and
		// pressure persisted another recovery period, restart after
		// notification so a new culprit can be handled. A pass of only
		// skips does not repeat: nothing would change.
		first := 0
		for first < len(ladder) && ladder[first].Step == model.GuardianNotify {
			first++
		}
		if !inc.acted || first >= len(ladder) || now.Sub(inc.lastApplied) < e.cfg.Recover {
			return nil
		}
		inc.next, inc.acted, inc.ladderStart = first, false, now.Add(-ladder[first].After)
	}
	r := ladder[inc.next]
	need, _ := model.PressureRank(r.Level)
	if rank < need || now.Sub(inc.ladderStart) < r.After {
		return nil
	}
	if r.Step != model.GuardianNotify && !inc.lastApplied.IsZero() && now.Sub(inc.lastApplied) < e.cfg.Cooldown {
		return nil
	}
	d := Decision{Step: r.Step, Resource: inc.resource, Level: inc.level, Incident: inc.id, Execute: e.cfg.executes(r.Step)}
	var culprit *Candidate
	for _, c := range cands {
		if c.eligible() && !inc.spared[c.ID] && significant(inc.resource, metric(inc.resource, c.Group), host) {
			culprit = c
			break
		}
	}
	if r.Step == model.GuardianNotify {
		d.Group = culprit
		if d.Group == nil && len(cands) > 0 {
			d.Group = cands[0]
		}
		d.Reason = fmt.Sprintf("%s pressure %s: %s", inc.resource, inc.level, reason)
		if d.Group != nil {
			d.Usage = usageText(inc.resource, metric(inc.resource, d.Group.Group))
		}
		return e.advance(now, inc, d)
	}
	if culprit == nil {
		hold := "no eligible group with significant usage"
		if inc.held == hold {
			return nil
		}
		inc.held = hold
		return []Decision{{Step: r.Step, Resource: inc.resource, Level: inc.level, Incident: inc.id, Outcome: model.GuardianSkipped, Reason: hold}}
	}
	d.Group, d.Usage = culprit, usageText(inc.resource, metric(inc.resource, culprit.Group))
	d.Reason = fmt.Sprintf("%s pressure %s for %s; %s uses %s", inc.resource, inc.level, now.Sub(inc.since).Round(time.Second), culprit.Name, d.Usage)
	if skip := e.targets(&d, culprit); skip != "" {
		d.Outcome, d.Reason, d.Execute = model.GuardianSkipped, skip, false
		return e.advance(now, inc, d)
	}
	if d.Execute && d.Step != model.GuardianAsk {
		if e.budgetLeft(now) <= 0 {
			hold := "hourly action budget exhausted"
			if inc.held == hold {
				return nil
			}
			inc.held = hold
			return []Decision{{Step: r.Step, Resource: inc.resource, Level: inc.level, Incident: inc.id, Group: culprit, Outcome: model.GuardianSkipped, Reason: hold}}
		}
		e.applied = append(e.applied, now)
	}
	return e.advance(now, inc, d)
}

func (e *Engine) advance(now time.Time, inc *incident, d Decision) []Decision {
	inc.next++
	inc.held = ""
	inc.lastStep = d.Step
	if d.Group != nil {
		inc.group = d.Group.ID
	}
	if d.Outcome != model.GuardianSkipped {
		inc.lastApplied = now
		if d.Step != model.GuardianNotify && d.Step != model.GuardianAsk {
			inc.acted = true
		}
	}
	return []Decision{d}
}

// targets selects processes for a step, or explains why the step does not
// apply to this group.
func (e *Engine) targets(d *Decision, c *Candidate) string {
	members := func() []Process {
		out := make([]Process, 0, len(c.Members))
		for _, m := range c.Members {
			out = append(out, m.Process)
		}
		return out
	}
	switch d.Step {
	case model.GuardianDeprioritize:
		if d.Resource == "memory" {
			return "lower priority does not relieve memory pressure"
		}
		if e.hasActive(c.ID, model.GuardianDeprioritize) {
			return c.Name + " is already deprioritized"
		}
		d.Targets = members()
	case model.GuardianAsk:
		if c.Agent == "" {
			return "no registered agent is attributed to " + c.Name
		}
	case model.GuardianShed:
		if c.Profile != "chromium" {
			return c.Name + " has no profile for shedding part of its work"
		}
		var total float64
		byUse := append([]Usage(nil), c.Members...)
		sort.SliceStable(byUse, func(i, j int) bool { return memberMetric(d.Resource, byUse[i]) > memberMetric(d.Resource, byUse[j]) })
		for _, m := range byUse {
			if m.Kind != "renderer" {
				continue
			}
			v := memberMetric(d.Resource, m)
			if len(d.Targets) == 3 || (d.Resource == "cpu" && v < 0.25) || (d.Resource == "memory" && v < 200<<20) || (d.Resource == "io" && v < 1<<20) {
				continue
			}
			d.Targets = append(d.Targets, m.Process)
			total += v
		}
		if len(d.Targets) == 0 {
			return "no renderer process of " + c.Name + " is busy enough to shed"
		}
		d.Reason += fmt.Sprintf("; shedding %d renderer processes using %s", len(d.Targets), usageText(d.Resource, total))
	case model.GuardianFreeze:
		if e.hasActive(c.ID, model.GuardianFreeze) {
			return c.Name + " is already frozen"
		}
		d.Targets = members()
	case model.GuardianTerminate:
		d.Targets = leavesFirst(members())
	}
	return ""
}

// leavesFirst orders processes so children precede their parents; a
// parent may otherwise restart a child it sees exit.
func leavesFirst(ps []Process) []Process {
	parents := map[int]bool{}
	for _, p := range ps {
		parents[p.PPID] = true
	}
	sort.SliceStable(ps, func(i, j int) bool { return !parents[ps[i].Identity.PID] && parents[ps[j].Identity.PID] })
	return ps
}

// Spare excludes a group from further steps in every open incident after an
// operator undid an action on it.
func (e *Engine) Spare(group string) {
	for _, inc := range e.incidents {
		inc.spared[group] = true
	}
}

func memberMetric(resource string, u Usage) float64 {
	switch resource {
	case "cpu":
		return rate(u.CPUCores)
	case "memory":
		if u.Memory == nil {
			return 0
		}
		return float64(*u.Memory)
	}
	return rate(u.DiskRate)
}

func (e *Engine) hasActive(group, step string) bool {
	for _, a := range e.active {
		if a.group == group && a.step == step {
			return true
		}
	}
	return false
}

// Applied registers a reversible action so recovery can undo it.
func (e *Engine) Applied(id string, d Decision, at time.Time, targets []model.ProcessIdentity) {
	if d.Extends != "" {
		if a := e.active[d.Extends]; a != nil {
			for _, t := range targets {
				a.targets[t] = true
			}
		}
		return
	}
	if d.Group == nil || (d.Step != model.GuardianDeprioritize && d.Step != model.GuardianFreeze) {
		return
	}
	a := &active{id: id, incident: d.Incident, step: d.Step, group: d.Group.ID, at: at, targets: map[model.ProcessIdentity]bool{}}
	for _, t := range targets {
		a.targets[t] = true
	}
	e.active[id] = a
}

// Adopt registers a recovered action whose revert failed, so maintenance
// retries it.
func (e *Engine) Adopt(id, step, group string, at time.Time, targets []model.ProcessIdentity) {
	a := &active{id: id, step: step, group: group, at: at, targets: map[model.ProcessIdentity]bool{}}
	for _, t := range targets {
		a.targets[t] = true
	}
	e.active[id] = a
}

// Prune replaces the identities an extension already considered.
func (e *Engine) Prune(id string, keep []model.ProcessIdentity) {
	a := e.active[id]
	if a == nil {
		return
	}
	a.targets = make(map[model.ProcessIdentity]bool, len(keep))
	for _, t := range keep {
		a.targets[t] = true
	}
}

// Release forgets a reversible action after it was reverted or abandoned.
func (e *Engine) Release(id string) { delete(e.active, id) }

// ActiveSteps lists active reversible steps for a group.
func (e *Engine) ActiveSteps(group string) []string {
	var out []string
	for _, s := range []string{model.GuardianDeprioritize, model.GuardianFreeze} {
		if e.hasActive(group, s) {
			out = append(out, s)
		}
	}
	return out
}

// ActiveIDs lists all active reversible action IDs.
func (e *Engine) ActiveIDs() []string {
	ids := make([]string, 0, len(e.active))
	for id := range e.active {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (e *Engine) Incidents() []model.GuardianIncident {
	out := []model.GuardianIncident{}
	for _, res := range resources {
		inc := e.incidents[res]
		if inc == nil {
			continue
		}
		v := model.GuardianIncident{ID: inc.id, Resource: res, Level: inc.level, Peak: inc.peak, Since: inc.since, Step: inc.lastStep, Group: inc.group}
		if !inc.recovering.IsZero() {
			t := inc.recovering
			v.Recovering = &t
		}
		out = append(out, v)
	}
	return out
}

func (e *Engine) BudgetUsed(now time.Time) int { return e.cfg.Budget - e.budgetLeft(now) }
