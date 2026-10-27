package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/knowledge"
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
		foundRust := false
		for _, card := range guide.Cards {
			foundRust = foundRust || card.ID == "rust-build" && card.Adapter == "rust-build"
		}
		if !foundRust {
			t.Fatal("offline guide omits embedded card discovery", guide.Cards)
		}
		seen := map[string]bool{}
		for _, section := range guide.Sections {
			if section.ID == "" || seen[section.ID] || section.Title == "" || len(section.Guidance) == 0 || len(section.Commands) == 0 {
				t.Fatalf("unusable guide section: %+v", section)
			}
			seen[section.ID] = true
		}
		if !seen["budgets"] {
			t.Fatal("guide omits authenticated owner policy discovery")
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("offline guide created state: %v", err)
	}
}

func TestKnowledgeCardOfflineJSONMarkdownAndTerminal(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	home := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"agents", "card", "rust-build"}, {"agents", "card", "rust-build", "--json"}, {"--json", "agents", "card", "rust-build"}} {
		var out, stderr bytes.Buffer
		code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr)
		var card knowledge.Card
		if code != 0 || stderr.Len() != 0 || json.Unmarshal(out.Bytes(), &card) != nil || card.ID != "rust-build" || !strings.Contains(card.Markdown, "export") {
			t.Fatal("offline card retrieval failed", code, out.String(), stderr.String())
		}
	}
	var out, stderr bytes.Buffer
	if code := Main([]string{"--home", home, "agents", "card", "rust-build", "--markdown"}, strings.NewReader(""), &out, &stderr); code != 0 || !strings.HasPrefix(out.String(), "# Rust builds") || strings.Contains(out.String(), "schema_version:") || stderr.Len() != 0 {
		t.Fatal("explicit Markdown failed", code, out.String(), stderr.String())
	}
	out.Reset()
	a := app{home: home, out: &out, outputTTY: true, colors: true}
	if err := a.agentsCard([]string{"rust-build"}); err != nil || !strings.HasPrefix(out.String(), "# Rust builds") || strings.Contains(out.String(), "\x1b") {
		t.Fatal("terminal card is not readable Markdown", err, out.String())
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("offline cards created state", err)
	}
}

func TestKnowledgeCardErrorsAreStructuredAndMutationFree(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"card"}, {"card", "rust-build", "extra"}, {"card", "../secret"}, {"card", "unavailable"}, {"card", "rust-build", "--json", "--markdown"}, {"card", "rust-build", "--cpu", "1"}} {
		var out, stderr bytes.Buffer
		code := Main(append([]string{"--home", home, "agents"}, args...), strings.NewReader(""), &out, &stderr)
		if code == 0 || out.Len() != 0 || !json.Valid(stderr.Bytes()) {
			t.Fatal("card error was not a structured failure", args, code, out.String(), stderr.String())
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("card errors created state", err)
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

func TestAgentGuideExplainsReplyIdentityAndUnknownSend(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := Main([]string{"--home", filepath.Join(t.TempDir(), "absent"), "agents", "--json"}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	var guide agentGuide
	if err := json.Unmarshal(out.Bytes(), &guide); err != nil {
		t.Fatal(err)
	}
	for _, section := range guide.Sections {
		if section.ID == "agent-messages" {
			body := strings.Join(section.Guidance, " ") + strings.Join(section.Commands, " ")
			for _, need := range []string{"sender_contact", "agent reply", "--registration", "message-lookup", "polling inbox", "not separate authentication", "replacement"} {
				if !strings.Contains(body, need) {
					t.Fatalf("guide omits %q", need)
				}
			}
			return
		}
	}
	t.Fatal("missing agent communication workflow")
}
