package guardian

import (
	"testing"
	"time"

	"getarm.org/internal/model"
)

func pressure(cpu string) model.MonitorPressure {
	return model.MonitorPressure{CPU: model.PressureReading{Level: cpu, Reason: "test"}, Memory: model.PressureReading{Level: model.PressureNormal}, IO: model.PressureReading{Level: model.PressureNormal}}
}

func busy(id string, cores float64, pids ...int) *Candidate {
	g := &Group{ID: id, Kind: "process-group", Name: id, CPU: cores, Known: true}
	for _, pid := range pids {
		c := cores / float64(len(pids))
		g.Members = append(g.Members, Usage{Process: Process{Identity: model.ProcessIdentity{PID: pid, Start: "s"}, Name: id}, CPUCores: &c, Available: true})
	}
	return &Candidate{Group: g}
}

var host = Host{CPUs: 8, MemoryTotal: 16 << 30}

func run(e *Engine, start time.Time, seconds int, level string, cands ...*Candidate) []Decision {
	var all []Decision
	for s := 0; s <= seconds; s += 5 {
		all = append(all, e.Step(start.Add(time.Duration(s)*time.Second), pressure(level), false, false, cands, host)...)
	}
	return all
}

func stepsOf(ds []Decision) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Step+":"+map[bool]string{true: "x", false: "-"}[d.Execute])
	}
	return out
}

func TestMannedLadderInActMode(t *testing.T) {
	e := NewEngine(Config{Mode: model.GuardianAct, Role: model.GuardianManned}.Defaults())
	c := busy("chrome", 6, 10, 11)
	c.Profile = "chromium"
	c.Members[0].Kind = "renderer"
	ds := run(e, time.Unix(0, 0), 115, model.PressureCritical, c)
	got := stepsOf(ds)
	want := []string{"notify:x", "deprioritize:x", "ask:-", "shed:x"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if ds[2].Outcome != model.GuardianSkipped || len(ds[3].Targets) != 1 {
		t.Fatalf("ask %+v shed %+v", ds[2], ds[3])
	}
}

func TestObserveModeOnlyPlans(t *testing.T) {
	e := NewEngine(Config{Mode: model.GuardianObserve, Role: model.GuardianUnmanned}.Defaults())
	for _, d := range run(e, time.Unix(0, 0), 400, model.PressureCritical, busy("job", 7, 20)) {
		if d.Execute {
			t.Fatalf("observe executed %+v", d)
		}
	}
}

func TestAdviseExecutesOnlyNotifyAndAsk(t *testing.T) {
	e := NewEngine(Config{Mode: model.GuardianAdvise, Role: model.GuardianUnmanned}.Defaults())
	c := busy("job", 7, 20)
	c.Agent = "contact"
	for _, d := range run(e, time.Unix(0, 0), 400, model.PressureCritical, c) {
		if d.Execute != (d.Step == model.GuardianNotify || d.Step == model.GuardianAsk) {
			t.Fatalf("advise %s execute=%v", d.Step, d.Execute)
		}
	}
}

func TestProtectedAndManagedAreNeverTargets(t *testing.T) {
	e := NewEngine(Config{Mode: model.GuardianAct, Role: model.GuardianUnmanned}.Defaults())
	p, m := busy("windowserver", 7, 30), busy("lease", 6, 31)
	p.Protected, m.Managed = "protected", true
	for _, d := range run(e, time.Unix(0, 0), 400, model.PressureCritical, p, m) {
		if d.Step != model.GuardianNotify && len(d.Targets) > 0 {
			t.Fatalf("targeted %+v", d)
		}
	}
}

func TestUnknownAndStaleHold(t *testing.T) {
	e := NewEngine(Config{Mode: model.GuardianAct}.Defaults())
	c := busy("job", 7, 40)
	start := time.Unix(0, 0)
	if ds := run(e, start, 100, model.PressureUnknown, c); len(ds) != 0 {
		t.Fatalf("unknown pressure acted: %v", stepsOf(ds))
	}
	for s := 0; s < 100; s += 5 {
		if ds := e.Step(start.Add(time.Duration(s)*time.Second), pressure(model.PressureCritical), true, false, []*Candidate{c}, host); len(ds) != 0 {
			t.Fatal("stale pressure acted")
		}
	}
}

func TestRecoveryRevertsWithHysteresis(t *testing.T) {
	cfg := Config{Mode: model.GuardianAct}.Defaults()
	e := NewEngine(cfg)
	c := busy("job", 7, 50)
	start := time.Unix(0, 0)
	ds := run(e, start, 35, model.PressureHigh, c)
	var dep Decision
	for _, d := range ds {
		if d.Step == model.GuardianDeprioritize {
			dep = d
		}
	}
	if dep.Step == "" {
		t.Fatalf("no deprioritize: %v", stepsOf(ds))
	}
	e.Applied("a1", dep, start, []model.ProcessIdentity{c.Members[0].Identity})
	// Brief relief, then pressure again: no revert.
	if ds := run(e, start.Add(40*time.Second), 20, model.PressureNormal, c); len(ds) != 0 {
		t.Fatalf("reverted before hysteresis: %v", stepsOf(ds))
	}
	run(e, start.Add(65*time.Second), 0, model.PressureHigh, c)
	ds = run(e, start.Add(70*time.Second), 60, model.PressureNormal, c)
	if len(ds) != 1 || ds[0].Step != model.GuardianRestore || ds[0].Undoes != "a1" {
		t.Fatalf("recovery %v", stepsOf(ds))
	}
	// Until the runtime releases a successful revert, it is retried.
	if ds := e.Step(start.Add(135*time.Second), pressure(model.PressureNormal), false, false, []*Candidate{c}, host); len(ds) != 1 || ds[0].Undoes != "a1" {
		t.Fatalf("retry %v", stepsOf(ds))
	}
	e.Release("a1")
	if ds := e.Step(start.Add(140*time.Second), pressure(model.PressureNormal), false, false, []*Candidate{c}, host); len(ds) != 0 {
		t.Fatalf("released action reverted again: %v", stepsOf(ds))
	}
	if len(e.Incidents()) != 0 {
		t.Fatal("incident stayed open")
	}
}

func TestSkipOnlyPassDoesNotRepeat(t *testing.T) {
	e := NewEngine(Config{Mode: model.GuardianAct}.Defaults())
	c := busy("job", 7, 80)
	var ds []Decision
	for s := 0; s <= 900; s += 5 {
		for _, d := range e.Step(time.Unix(int64(s), 0), pressure(model.PressureCritical), false, false, []*Candidate{c}, host) {
			if d.Execute && d.Outcome == "" && d.Step == model.GuardianDeprioritize {
				e.Applied("dep", d, time.Unix(int64(s), 0), nil) // as the runtime does
			}
			ds = append(ds, d)
		}
	}
	// Deprioritize applies, then ask and shed skip; the ladder restarts
	// once after the recovery period, then only skips remain.
	counts := map[string]int{}
	for _, d := range ds {
		counts[d.Step]++
	}
	if counts[model.GuardianNotify] != 1 || counts[model.GuardianAsk] > 2 || counts[model.GuardianShed] > 2 {
		t.Fatalf("repeated steps %v", counts)
	}
	if inc := e.Incidents(); len(inc) != 1 || !inc[0].Since.Equal(time.Unix(0, 0)) {
		t.Fatalf("incident start moved: %+v", inc)
	}
}

func TestOperatorUndoSparesGroup(t *testing.T) {
	e := NewEngine(Config{Mode: model.GuardianAct}.Defaults())
	c := busy("job", 7, 81)
	run(e, time.Unix(0, 0), 20, model.PressureCritical, c)
	e.Spare(c.ID)
	for _, d := range run(e, time.Unix(25, 0), 400, model.PressureCritical, c) {
		if len(d.Targets) > 0 {
			t.Fatalf("targeted spared group: %+v", d)
		}
	}
}

func TestPartialInventoryHoldsEscalation(t *testing.T) {
	e := NewEngine(Config{Mode: model.GuardianAct}.Defaults())
	c := busy("job", 7, 82)
	for s := 0; s < 200; s += 5 {
		for _, d := range e.Step(time.Unix(int64(s), 0), pressure(model.PressureCritical), false, true, []*Candidate{c}, host) {
			t.Fatalf("partial inventory escalated: %+v", d)
		}
	}
}

func TestBudgetLimitsActions(t *testing.T) {
	cfg := Config{Mode: model.GuardianAct, Role: model.GuardianUnmanned, Budget: 1}.Defaults()
	e := NewEngine(cfg)
	ds := run(e, time.Unix(0, 0), 400, model.PressureCritical, busy("job", 7, 60))
	executed, held := 0, 0
	for _, d := range ds {
		if d.Execute && d.Step != model.GuardianNotify && d.Step != model.GuardianAsk {
			executed++
		}
		if d.Reason == "hourly action budget exhausted" {
			held++
		}
	}
	if executed != 1 || held != 1 {
		t.Fatalf("executed %d held %d: %v", executed, held, stepsOf(ds))
	}
}

func TestStickyDeprioritizeAndFreezeMax(t *testing.T) {
	cfg := Config{Mode: model.GuardianAct}.Defaults()
	e := NewEngine(cfg)
	c := busy("job", 4, 70)
	e.incidents["cpu"] = &incident{id: "i", spared: map[string]bool{}}
	d := Decision{Step: model.GuardianDeprioritize, Group: c, Incident: "i"}
	e.Applied("dep", d, time.Unix(0, 0), []model.ProcessIdentity{c.Members[0].Identity})
	extra := 1.0
	c.Members = append(c.Members, Usage{Process: Process{Identity: model.ProcessIdentity{PID: 71, Start: "s"}}, CPUCores: &extra})
	ds := e.maintain(time.Unix(1, 0), map[string]*Candidate{c.ID: c}, false)
	if len(ds) != 1 || ds[0].Extends != "dep" || len(ds[0].Targets) != 1 || ds[0].Targets[0].Identity.PID != 71 {
		t.Fatalf("extend %+v", ds)
	}
	// A reused group without any original target is not extended.
	stranger := busy("job", 4, 99)
	if ds := e.maintain(time.Unix(1, 0), map[string]*Candidate{c.ID: stranger}, false); len(ds) != 0 {
		t.Fatalf("extended a group with no original member: %+v", ds)
	}
	// Nor is a group that became ineligible, such as ARM-managed work.
	c.Managed = true
	if ds := e.maintain(time.Unix(1, 0), map[string]*Candidate{c.ID: c}, false); len(ds) != 0 {
		t.Fatalf("extended an ineligible group: %+v", ds)
	}
	e.Applied("frz", Decision{Step: model.GuardianFreeze, Group: c, Incident: "i"}, time.Unix(0, 0), nil)
	ds = e.maintain(time.Unix(0, 0).Add(cfg.FreezeMax), map[string]*Candidate{}, false)
	if len(ds) != 1 || ds[0].Step != model.GuardianThaw || ds[0].Undoes != "frz" {
		t.Fatalf("freeze max %+v", ds)
	}
}

func TestConfigValidation(t *testing.T) {
	if err := (Config{Mode: "act"}).Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := Config{Mode: "act", Ladder: []model.GuardianStepRule{rule(model.GuardianFreeze, model.PressureHigh, 0), rule(model.GuardianNotify, model.PressureHigh, 0)}}.Defaults()
	if bad.Validate() == nil {
		t.Fatal("accepted out-of-order ladder")
	}
	if (Config{Mode: "loud"}).Defaults().Validate() == nil {
		t.Fatal("accepted unknown mode")
	}
}

func TestGroupingAndProtection(t *testing.T) {
	cores := 1.0
	rows := []Usage{
		{Process: Process{Identity: model.ProcessIdentity{PID: 100, PGID: 100, UID: "501"}, Name: "Google Chrome", Executable: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}, CPUCores: &cores},
		{Process: Process{Identity: model.ProcessIdentity{PID: 101, PGID: 101, UID: "501"}, Name: "Google Chrome Helper (Renderer)", Executable: "/Applications/Google Chrome.app/Contents/Frameworks/x.framework/Helpers/Google Chrome Helper (Renderer).app/Contents/MacOS/Google Chrome Helper (Renderer)"}, CPUCores: &cores},
		{Process: Process{Identity: model.ProcessIdentity{PID: 200, PGID: 200, UID: "501"}, Name: "python"}, CPUCores: &cores},
		{Process: Process{Identity: model.ProcessIdentity{PID: 300, PGID: 300, UID: "1000"}, Name: "firefox", Cgroup: "/user.slice/user-1000.slice/user@1000.service/app.slice/app-gnome-firefox-4242.scope"}, CPUCores: &cores},
	}
	groups := Groups(rows)
	if len(groups) != 3 {
		t.Fatalf("groups %d", len(groups))
	}
	names := map[string]int{}
	for _, g := range groups {
		names[g.Name] = len(g.Members)
	}
	if names["Google Chrome"] != 2 || names["python"] != 1 || names["gnome-firefox"] != 1 {
		t.Fatalf("names %v", names)
	}
	pr := Protection{UID: "501", Self: map[int]bool{200: true}}
	for _, g := range groups {
		reason := pr.Group(g)
		switch g.Name {
		case "Google Chrome":
			if reason != "" {
				t.Fatalf("chrome protected: %s", reason)
			}
		default:
			if reason == "" {
				t.Fatalf("%s unprotected", g.Name)
			}
		}
	}
	if pr.Process(Process{Identity: model.ProcessIdentity{PID: 5, UID: "501"}, Name: "WindowServer"}) == "" || pr.Process(Process{Identity: model.ProcessIdentity{PID: 6, UID: "501"}, Executable: "/usr/libexec/x"}) == "" {
		t.Fatal("system process unprotected")
	}
	if chromiumType([]byte("chrome\x00--type=renderer\x00--x")) != "renderer" || chromiumType([]byte("chrome\x00--type=gpu-process")) != "" {
		t.Fatal("chromium type")
	}
}

func TestTerminalScopesDoNotGroupShells(t *testing.T) {
	for _, path := range []string{"/user.slice/user-1000.slice/user@1000.service/app.slice/vte-spawn-1b2c.scope", "/user.slice/user-1000.slice/user@1000.service/app.slice/tmux-spawn-9.scope", "/user.slice/user-1000.slice/user@1000.service/app.slice/app-org.gnome.Terminal.slice/vte-spawn-x.scope"} {
		if cgroupUnit(path) != "" {
			t.Fatalf("grouped terminal scope %s", path)
		}
	}
	if cgroupUnit("/system.slice/ssh.service") == "" {
		t.Fatal("service unit not recognized")
	}
}

func TestPGIDReuseChangesGroup(t *testing.T) {
	one := func(start string) string {
		cores := 1.0
		g := Groups([]Usage{{Process: Process{Identity: model.ProcessIdentity{PID: 500, PGID: 500, Start: start, Namespace: "n"}, Name: "job"}, CPUCores: &cores}})
		return g[0].ID
	}
	if one("a") == one("b") {
		t.Fatal("reused PGID kept the group identity")
	}
}

func TestPrunedExtensionDoesNotChurn(t *testing.T) {
	e := NewEngine(Config{Mode: model.GuardianAct}.Defaults())
	e.incidents["cpu"] = &incident{id: "i", spared: map[string]bool{}}
	c := busy("job", 4, 1, 2, 3)
	e.Applied("dep", Decision{Step: model.GuardianDeprioritize, Group: c, Incident: "i"}, time.Unix(0, 0), []model.ProcessIdentity{c.Members[0].Identity})
	var all []model.ProcessIdentity
	for _, m := range c.Members {
		all = append(all, m.Identity)
	}
	// The runtime records every current member after an extension, even
	// those it could not lower or that exceed the cap.
	e.Prune("dep", all)
	if ds := e.maintain(time.Unix(1, 0), map[string]*Candidate{c.ID: c}, false); len(ds) != 0 {
		t.Fatalf("extension repeated: %+v", ds)
	}
}
