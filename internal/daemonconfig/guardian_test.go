package daemonconfig

import (
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
)

func TestGuardianSection(t *testing.T) {
	raw := "schema = 1\n[guardian]\nenabled = true\nmode = \"observe\"\nrole = \"unmanned\"\ncooldown = \"20s\"\nprotect = [\"Slack\"]\n[[guardian.ladder]]\nstep = \"notify\"\nlevel = \"high\"\nafter = \"10s\"\n"
	r := Resolved{Sources: map[string]string{}}
	r.Settings.Resources.CPU, r.Settings.Listen = 1, "127.0.0.1:0"
	if err := applyDocument([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	g := r.Settings.Guardian
	if g == nil || g.Enabled == nil || !*g.Enabled || g.Mode != "observe" || g.Cooldown != 20*time.Second || len(g.Ladder) != 1 || g.Ladder[0].After != 10*time.Second || r.Sources["guardian"] != "file" {
		t.Fatalf("guardian %+v", g)
	}
	if err := r.Settings.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Settings.Guardian.Mode = "loud"
	if r.Settings.Validate() == nil {
		t.Fatal("accepted invalid mode")
	}
	if err := applyDocument([]byte("schema = 1\n[guardian]\ncooldown = \"soon\"\n"), &Resolved{Sources: map[string]string{}, Settings: r.Settings}); err == nil {
		t.Fatal("accepted invalid duration")
	}
}

func TestGuardianEdit(t *testing.T) {
	out, err := editDocument([]byte("schema = 1\n[daemon]\ncpu = 2\n"), Edit{Key: "guardian.enabled", Value: "true"})
	if err != nil || !strings.HasSuffix(string(out), "\n[guardian]\nenabled = true\n") {
		t.Fatalf("%q %v", out, err)
	}
	out, err = editDocument(out, Edit{Key: "guardian.enabled", Value: "false"})
	if err != nil || strings.Count(string(out), "enabled") != 1 || !strings.Contains(string(out), "enabled = false") {
		t.Fatalf("%q %v", out, err)
	}
	out, err = editDocument(out, Edit{Key: "guardian.mode", Value: "advise"})
	if err != nil || !strings.Contains(string(out), "enabled = false\nmode = \"advise\"\n") {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := editDocument(out, Edit{Key: "guardian.enabled", Value: "maybe"}); err == nil {
		t.Fatal("accepted non-boolean")
	}
	if _, err := editDocument(out, Edit{Key: "guardian.unknown", Value: "1"}); err == nil {
		t.Fatal("accepted unknown key")
	}
}

func TestGuardianEditWithoutTrailingNewline(t *testing.T) {
	out, err := editDocument([]byte("schema = 1\n[guardian]\nmode = \"act\""), Edit{Key: "guardian.enabled", Value: "true"})
	if err != nil || !strings.Contains(string(out), "mode = \"act\"\nenabled = true\n") {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := editDocument([]byte("schema = 1\nguardian.mode = \"act\"\n"), Edit{Key: "guardian.enabled", Value: "true"}); err == nil {
		t.Fatal("dotted form edit accepted")
	}
}

func TestGuardianLadderEdit(t *testing.T) {
	ladder := []model.GuardianStepRule{{Step: "notify", Level: "high", After: 15 * time.Second}, {Step: "freeze", Level: "critical", After: 90 * time.Second}}
	parse := func(raw []byte) *model.GuardianSettings {
		t.Helper()
		r := Resolved{Sources: map[string]string{}}
		r.Settings.Resources.CPU, r.Settings.Listen = 1, "127.0.0.1:0"
		if err := applyDocument(raw, &r); err != nil {
			t.Fatalf("%v\n%s", err, raw)
		}
		if err := r.Settings.Validate(); err != nil {
			t.Fatalf("%v\n%s", err, raw)
		}
		return r.Settings.Guardian
	}
	// Without a [guardian] table, the ladder is appended and nothing is enabled.
	out, err := editDocument([]byte("schema = 1\n[daemon]\ncpu = 2\n"), Edit{Key: "guardian.ladder", Ladder: ladder})
	if err != nil {
		t.Fatal(err)
	}
	if g := parse(out); g == nil || g.Enabled != nil || len(g.Ladder) != 2 || g.Ladder[1].After != 90*time.Second {
		t.Fatalf("%+v\n%s", g, out)
	}
	// Replacement keeps guardian keys, comments and later tables.
	raw := "schema = 1\n[guardian]\nenabled = true # chosen\nrole = \"manned\"\n\n[[guardian.ladder]]\nstep = \"notify\"\n# keep calm\nlevel = \"high\"\nafter = \"15s\"\n\n[[guardian.ladder]]\nstep = \"shed\"\nlevel = \"critical\"\nafter = \"1m0s\"\n\n# pools below\n[[storage_pools]]\nname = \"scratch\"\npath = \"/tmp\"\n"
	out, err = editDocument([]byte(raw), Edit{Key: "guardian.ladder", Ladder: ladder[1:]})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "enabled = true # chosen\nrole = \"manned\"\n\n[[guardian.ladder]]\nstep = \"freeze\"\nlevel = \"critical\"\nafter = \"1m30s\"\n") || DurationText(time.Hour) != "1h" || DurationText(90*time.Minute) != "1h30m" || DurationText(2*time.Minute) != "2m" || strings.Count(s, "[[guardian.ladder]]") != 1 || !strings.Contains(s, "# pools below\n[[storage_pools]]") {
		t.Fatalf("%s", s)
	}
	if g := parse(out); len(g.Ladder) != 1 || g.Ladder[0].Step != "freeze" || g.Enabled == nil || !*g.Enabled {
		t.Fatalf("%+v", g)
	}
	// Later scalar edits still land in [guardian], not in a ladder table.
	out, err = editDocument(out, Edit{Key: "guardian.mode", Value: "observe"})
	if err != nil {
		t.Fatal(err)
	}
	if g := parse(out); g.Mode != "observe" || len(g.Ladder) != 1 {
		t.Fatalf("%+v\n%s", g, out)
	}
	// Reset removes every ladder table and restores the role default.
	out, err = editDocument(out, Edit{Key: "guardian.ladder", Remove: true})
	if err != nil {
		t.Fatal(err)
	}
	if g := parse(out); g.Ladder != nil || strings.Contains(string(out), "ladder") || !strings.Contains(string(out), "[[storage_pools]]") {
		t.Fatalf("%+v\n%s", g, out)
	}
	if _, err := editDocument([]byte("schema = 1\n[guardian]\nladder = []\n"), Edit{Key: "guardian.ladder", Ladder: ladder}); err == nil {
		t.Fatal("inline ladder rewritten")
	}
	if _, err := editDocument([]byte("schema = 1\n"), Edit{Key: "guardian.ladder"}); err == nil {
		t.Fatal("empty ladder accepted")
	}
}

func TestGuardianLadderEditAfterArraysAndCRLF(t *testing.T) {
	ladder := []model.GuardianStepRule{{Step: "freeze", Level: "critical", After: 90 * time.Second}}
	check := func(raw string) string {
		t.Helper()
		out, err := editDocument([]byte(raw), Edit{Key: "guardian.ladder", Ladder: ladder})
		if err != nil {
			t.Fatal(err)
		}
		r := Resolved{Sources: map[string]string{}}
		r.Settings.Resources.CPU, r.Settings.Listen = 1, "127.0.0.1:0"
		if err := applyDocument(out, &r); err != nil {
			t.Fatalf("%v\n%q", err, out)
		}
		g := r.Settings.Guardian
		if len(g.Protect) != 2 || len(g.Ladder) != 1 || g.Ladder[0].Step != "freeze" {
			t.Fatalf("%+v\n%q", g, out)
		}
		return string(out)
	}
	// An array value last in [guardian] reports no source range.
	check("schema = 1\n[guardian]\nenabled = true\nprotect = [\"Slack\", \"/opt/critical/\"]\n\n[[guardian.ladder]]\nstep = \"notify\"\nlevel = \"high\"\nafter = \"15s\"\n")
	out := check("schema = 1\n[guardian]\nprotect = [\n  \"Slack\",\n  \"/opt/critical/\",\n]\n# daemon below\n[daemon]\ncpu = 2\n")
	if !strings.Contains(out, "]\n\n[[guardian.ladder]]") || !strings.Contains(out, "after = \"1m30s\"\n\n# daemon below\n[daemon]") {
		t.Fatalf("%q", out)
	}
	// CRLF files keep CRLF, and set/reset cycles do not grow blank lines.
	raw := "schema = 1\r\n[guardian]\r\nenabled = true\r\nprotect = [\"a\", \"b\"]\r\n\r\n[daemon]\r\ncpu = 2\r\n"
	out = check(raw)
	if strings.Count(out, "\n") != strings.Count(out, "\r\n") {
		t.Fatalf("mixed line endings %q", out)
	}
	back, err := editDocument([]byte(out), Edit{Key: "guardian.ladder", Remove: true})
	if err != nil || string(back) != raw {
		t.Fatalf("%q %v", back, err)
	}
	// A line starting with # inside a multi-line string is not a comment.
	out = check("schema = 1\n[guardian]\nprotect = [\"a\", '''\n#b''']\n# next\n[daemon]\ncpu = 2\n")
	if !strings.Contains(out, "#b''']\n\n[[guardian.ladder]]") || !strings.Contains(out, "\n# next\n[daemon]") {
		t.Fatalf("%q", out)
	}
	if _, err := editDocument([]byte("schema = 1\n"), Edit{Key: "guardian.ladder", Value: "x"}); err == nil || !strings.Contains(err.Error(), "arm guardian ladder") {
		t.Fatalf("config set guardian.ladder: %v", err)
	}
}
