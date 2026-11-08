package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/model"
)

func ladderRun(t *testing.T, home string, args ...string) (int, guardianLadderReport, error) {
	t.Helper()
	var out bytes.Buffer
	a := app{home: home, ctx: t.Context(), in: strings.NewReader(""), out: &out, err: &out, forceJSON: true}
	code, err := a.guardianLadder(args)
	var r guardianLadderReport
	if err == nil {
		if e := json.Unmarshal(out.Bytes(), &r); e != nil {
			t.Fatalf("%v: %s", e, out.String())
		}
	}
	return code, r, err
}

func ladderSteps(l []model.GuardianStepRule) string {
	var s []string
	for _, r := range l {
		s = append(s, r.Step+"@"+r.Level+"+"+daemonconfig.DurationText(r.After))
	}
	return strings.Join(s, " ")
}

func TestGuardianLadderCommands(t *testing.T) {
	home := guardianHome(t)
	_, r, err := ladderRun(t, home)
	if err != nil || r.Source != "role-default" || r.Role != "manned" || ladderSteps(r.Ladder) != "notify@high+15s deprioritize@high+30s ask@high+45s shed@critical+1m" || strings.Join(r.Unused, ",") != "freeze,terminate" {
		t.Fatalf("%+v %v", r, err)
	}
	if readConfig(t, home) != "" {
		t.Fatal("show wrote configuration")
	}
	// A new step takes its default and its place in the order.
	if _, r, err = ladderRun(t, home, "set", "freeze"); err != nil || r.Source != "config" || ladderSteps(r.Ladder) != "notify@high+15s deprioritize@high+30s ask@high+45s shed@critical+1m freeze@critical+2m" || !r.Edit.Written {
		t.Fatalf("%+v %v", r, err)
	}
	if _, r, err = ladderRun(t, home, "set", "notify", "--level", "elevated", "--after", "5s"); err != nil || !strings.HasPrefix(ladderSteps(r.Ladder), "notify@elevated+5s deprioritize") {
		t.Fatalf("%+v %v", r, err)
	}
	if _, r, err = ladderRun(t, home, "remove", "shed"); err != nil || strings.Contains(ladderSteps(r.Ladder), "shed") {
		t.Fatalf("%+v %v", r, err)
	}
	// The saved file is what show reads back.
	if _, r, err = ladderRun(t, home, "show"); err != nil || r.Source != "config" || ladderSteps(r.Ladder) != "notify@elevated+5s deprioritize@high+30s ask@high+45s freeze@critical+2m" {
		t.Fatalf("%+v %v", r, err)
	}
	before := readConfig(t, home)
	if _, r, err = ladderRun(t, home, "preset", "unmanned", "--dry-run"); err != nil || r.Edit.Written || readConfig(t, home) != before || !strings.Contains(r.Edit.TOML, "terminate") {
		t.Fatalf("%+v %v", r, err)
	}
	if _, r, err = ladderRun(t, home, "preset", "unmanned"); err != nil || len(r.Ladder) != 6 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, r, err = ladderRun(t, home, "reset"); err != nil || r.Source != "role-default" || strings.Contains(readConfig(t, home), "ladder") {
		t.Fatalf("%+v %v %s", r, err, readConfig(t, home))
	}
	for _, bad := range [][]string{{"set", "explode"}, {"set", "notify", "--level", "normal"}, {"set", "notify", "--after", "-1s"}, {"set", "notify", "--after", "25h"}, {"remove", "freeze"}, {"preset", "robot"}, {"set"}, {"wiggle"}} {
		if code, _, err := ladderRun(t, home, bad...); err == nil || code != 2 {
			t.Fatalf("%v accepted", bad)
		}
	}
	if _, _, err = ladderRun(t, home, "set", "ask", "--expect-file", strings.Repeat("0", 64)); err == nil {
		t.Fatal("stale expect-file accepted")
	}
	// The last step cannot be removed.
	for _, s := range []string{"notify", "deprioritize", "ask"} {
		if _, _, err = ladderRun(t, home, "remove", s); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = ladderRun(t, home, "remove", "shed"); err == nil {
		t.Fatal("removed the last step")
	}
}

func TestGuardianLadderHumanWarns(t *testing.T) {
	home := guardianHome(t)
	var out bytes.Buffer
	a := app{home: home, ctx: t.Context(), out: &out, err: &out, outputTTY: true}
	if _, err := a.guardianLadder([]string{"preset", "unmanned"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"custom ladder in", "terminate", "guardian is off", "suspend or terminate", "Saved to", "arm stop, then arm start"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q:\n%s", want, s)
		}
	}
}

func key(m tea.Model, keys ...string) tea.Model {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "right":
			msg = tea.KeyPressMsg{Code: tea.KeyRight}
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "space":
			msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		case "backspace":
			msg = tea.KeyPressMsg{Code: tea.KeyBackspace}
		default:
			r := []rune(k)[0]
			msg = tea.KeyPressMsg{Code: r, Text: k}
		}
		m, _ = m.Update(msg)
	}
	return m
}

func TestLadderEditorKeys(t *testing.T) {
	home := guardianHome(t)
	a := app{home: home, ctx: t.Context()}
	r, err := a.readGuardianLadder()
	if err != nil {
		t.Fatal(err)
	}
	m := newLadderEditor(r)
	// Row 4 is freeze: enable it, raise nothing (already critical), add 1m.
	key(m, "down", "down", "down", "down", "space", "]")
	if got := ladderSteps(m.ladder()); !strings.HasSuffix(got, "freeze@critical+3m") || !m.custom || !m.dirty {
		t.Fatalf("%s", got)
	}
	// Typed delay on notify; an invalid entry is refused and keeps editing.
	key(m, "down", "down", "enter")
	for range 8 {
		key(m, "backspace")
	}
	key(m, "x", "enter")
	if !m.editing || m.notice == "" {
		t.Fatal("accepted invalid delay")
	}
	key(m, "backspace", "9", "0", "s", "enter")
	if m.editing || !strings.HasPrefix(ladderSteps(m.ladder()), "notify@high+1m30s") {
		t.Fatalf("%s %q", ladderSteps(m.ladder()), m.input.Value())
	}
	// Quitting with changes asks first.
	key(m, "q")
	if !m.discard || m.save {
		t.Fatal("quit without warning")
	}
	key(m, "d")
	if m.custom || ladderSteps(m.ladder()) != ladderSteps(r.Ladder) {
		t.Fatal("role default not restored")
	}
	key(m, "u")
	if len(m.ladder()) != 6 || !m.custom {
		t.Fatal("unmanned preset not loaded")
	}
	// Saving an empty custom ladder is refused.
	for range 6 {
		key(m, "space", "down")
	}
	key(m, "s")
	if m.save {
		t.Fatal("saved an empty ladder")
	}
	key(m, "space", "s")
	if !m.save || len(m.ladder()) != 1 {
		t.Fatalf("%v %s", m.save, ladderSteps(m.ladder()))
	}
	if v := m.View().Content; !strings.Contains(v, "[x] notify") {
		t.Fatalf("%s", v)
	}
}

func TestLadderEditRequiresTerminal(t *testing.T) {
	home := guardianHome(t)
	var out bytes.Buffer
	a := app{home: home, ctx: t.Context(), out: &out, err: &out, forceJSON: true}
	if code, err := a.guardianLadder([]string{"edit"}); err == nil || code != 2 {
		t.Fatal("edit ran without a terminal")
	}
}

func TestGuardianVerdict(t *testing.T) {
	s := model.GuardianStatus{}
	s.Pressure.CPU.Level, s.Pressure.Memory.Level, s.Pressure.IO.Level = "normal", "normal", "unknown"
	if _, v := guardianVerdict(s); v != "No pressure detected; I/O unknown" {
		t.Fatal(v)
	}
	s.Pressure.IO.Level = "normal"
	if _, v := guardianVerdict(s); !strings.HasPrefix(v, "All clear") {
		t.Fatal(v)
	}
	if _, v := guardianVerdict(model.GuardianStatus{}); !strings.HasPrefix(v, "Pressure unknown") {
		t.Fatal(v)
	}
	s.Pressure.Memory.Level, s.Incidents = "critical", []model.GuardianIncident{{}}
	if code, v := guardianVerdict(s); code != "1;31" || v != "Under pressure: memory critical · 1 open incident(s)" {
		t.Fatal(code, v)
	}
	s.Stale = true
	if _, v := guardianVerdict(s); !strings.HasPrefix(v, "Pressure unknown") {
		t.Fatal(v)
	}
}

func TestGuardianWatchJSONLinesSurviveErrors(t *testing.T) {
	home := guardianHome(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2500*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	a := app{home: home, ctx: ctx, out: &out, err: &out, forceJSON: true}
	if code, err := a.guardianWatch(time.Second); err != nil || code != 0 {
		t.Fatal(code, err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("%q", out.String())
	}
	for _, l := range lines {
		var v map[string]any
		if err := json.Unmarshal([]byte(l), &v); err != nil || v["error"] == nil {
			t.Fatalf("%q %v", l, err)
		}
	}
}

func TestGuardianWatchView(t *testing.T) {
	a := &app{outputTTY: true}
	m := &guardianWatchModel{a: a, interval: 2 * time.Second, width: 80, height: 12}
	if !strings.Contains(m.View().Content, "Reading") {
		t.Fatal("no loading view")
	}
	s := model.GuardianStatus{Mode: "act", Role: "manned"}
	for range 30 {
		s.Groups = append(s.Groups, model.GuardianGroup{Name: "busy"})
	}
	_, cmd := m.Update(guardianReadMsg{status: s, at: time.Now()})
	if cmd == nil {
		t.Fatal("no next tick")
	}
	v := m.View().Content
	if !strings.Contains(v, "Pressure unknown") || !strings.Contains(v, "q quits") || strings.Count(v, "\n") > 12 {
		t.Fatalf("%s", v)
	}
	m.Update(guardianReadMsg{err: context.DeadlineExceeded, at: time.Now()})
	if !strings.Contains(m.View().Content, "unavailable") {
		t.Fatal("no error view")
	}
}

func TestGuardianLadderReviewRegressions(t *testing.T) {
	home := guardianHome(t)
	// Reset on a fresh home writes nothing.
	if _, r, err := ladderRun(t, home, "reset"); err != nil || r.Edit.Written || readConfig(t, home) != "" {
		t.Fatalf("%+v %v %q", r, err, readConfig(t, home))
	}
	if _, _, err := ladderRun(t, home, "reset", "--expect-file", strings.Repeat("0", 64)); err == nil {
		t.Fatal("stale expect-file accepted by a no-op reset")
	}
	if _, _, err := ladderRun(t, home, "set", "notify", "--after", "1500ms"); err == nil {
		t.Fatal("sub-second delay accepted")
	}
	// A file changed after it was read is never overwritten.
	var out bytes.Buffer
	a := app{home: home, ctx: t.Context(), out: &out, err: &out, forceJSON: true}
	rep, err := a.readGuardianLadder()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ladderRun(t, home, "set", "freeze"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveGuardianLadder(rep, rep.Ladder, false, false, ""); err == nil {
		t.Fatal("overwrote a concurrent change")
	}
	// Enabled with mode off warns.
	on := true
	if w := ladderWarnings(model.GuardianOff, model.GuardianManned, &on, nil); len(w) != 1 || !strings.Contains(w[0], "Mode off") {
		t.Fatal(w)
	}
	// Bare --help lists the subcommands.
	out.Reset()
	h := app{home: home, ctx: t.Context(), out: &out, err: &out, outputTTY: true}
	if code, err := h.guardianLadder([]string{"--help"}); err != nil || code != 0 || !strings.Contains(out.String(), "preset ROLE") {
		t.Fatalf("%d %v %s", code, err, out.String())
	}
	// The live view needs terminal input to quit.
	w := app{home: home, ctx: t.Context(), out: &out, err: &out, outputTTY: true}
	if code, err := w.guardianWatch(time.Second); err == nil || code != 2 {
		t.Fatal("live view without terminal input")
	}
}
