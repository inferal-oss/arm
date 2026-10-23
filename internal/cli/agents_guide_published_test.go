package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishedAgentGuideSeparatesRoutedAndLocalCommunication(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	home := filepath.Join(t.TempDir(), "absent")
	var out, stderr bytes.Buffer
	if code := Main([]string{"--home", home, "agents", "--json"}, strings.NewReader(""), &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatal(code, stderr.String())
	}
	var guide agentGuide
	if err := json.Unmarshal(out.Bytes(), &guide); err != nil {
		t.Fatal(err)
	}
	sections := map[string]string{}
	for _, section := range guide.Sections {
		sections[section.ID] = strings.Join(section.Guidance, " ") + " " + strings.Join(section.Commands, " ")
	}
	body := sections["workspace-agents"]
	for _, need := range []string{"workspace-published-agents-poll-v1", "opt-in", "review_sha256", "--expect REVIEW_SHA256", "--delivery poll", "Native delivery is refused", "Idle", "opaque endpoint ID", "immutable generation", "page revision", "--workspace", "--host", "--endpoint", "--generation", "--registration", "LOCAL inbox message ID", "routed_origin", "ack without --text", "never resends", "do not retarget", "publish plan", "publish apply", "publish list", "publish withdraw", "PUBLICATION_REVISION"} {
		if !strings.Contains(body, need) {
			t.Errorf("published workflow lacks %q", need)
		}
	}
	for _, stale := range []string{"publication of local agent contacts into a workspace remains in development", "forwarding a local agent identity through mesh peers are currently refused"} {
		if strings.Contains(out.String(), stale) {
			t.Errorf("guide retained obsolete claim %q", stale)
		}
	}
	if !strings.Contains(sections["inbox"], "Routed workspace messages require ack without --text") || !strings.Contains(sections["agent-messages"], "Local sends use --to") {
		t.Fatal("local and routed receipt/reply workflows are ambiguous")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("offline published guide created state", err)
	}
	out.Reset()
	a := app{home: home, out: &out, outputTTY: true, colors: true}
	if err := a.agentsGuide(nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\x1b[1;36m") || !strings.Contains(out.String(), "Publish exact agents for poll-only workspace messaging") {
		t.Fatal("terminal guide lost styled publication guidance")
	}
}
