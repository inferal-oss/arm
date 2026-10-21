package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentsGuideWorksOfflineWithoutStateOrAgentCLIs(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	home := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"agents"}, {"agents", "--json"}, {"--json", "agents"}} {
		var out, stderr bytes.Buffer
		code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr)
		var guide agentGuide
		if code != 0 || stderr.Len() != 0 || json.Unmarshal(out.Bytes(), &guide) != nil || guide.StateDirectory != home || len(guide.Sections) == 0 {
			t.Fatalf("offline guide failed: %d %s %s", code, &out, &stderr)
		}
		seen := map[string]bool{}
		for _, section := range guide.Sections {
			if section.ID == "" || seen[section.ID] || section.Title == "" || len(section.Guidance) == 0 || len(section.Commands) == 0 {
				t.Fatalf("unusable guide section: %+v", section)
			}
			seen[section.ID] = true
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("offline guide created state: %v", err)
	}
}

func TestAgentsGuideTerminalEscapesStateAndSupportsForcedJSON(t *testing.T) {
	var out bytes.Buffer
	a := app{home: "/tmp/arm\x1b[2J\nforged", out: &out, outputTTY: true, colors: true}
	if err := a.agentsGuide(nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\x1b[1;36m") || strings.Contains(out.String(), "\x1b[2J") || !strings.Contains(out.String(), `\u001b[2J\nforged`) {
		t.Fatalf("unsafe terminal guide: %q", out.String())
	}
	out.Reset()
	if err := a.agentsGuide([]string{"--json"}); err != nil || !json.Valid(out.Bytes()) || bytes.Contains(out.Bytes(), []byte("\x1b")) {
		t.Fatalf("forced JSON failed: %v %s", err, &out)
	}
}
