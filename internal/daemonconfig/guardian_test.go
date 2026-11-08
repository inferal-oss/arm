package daemonconfig

import (
	"strings"
	"testing"
	"time"
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
