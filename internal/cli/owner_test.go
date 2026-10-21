package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestOwnerTerminalLabelPreservesUIDAndEscapesName(t *testing.T) {
	record := map[string]any{"owner": "501", "owner_username": "alice\x1b[31m"}
	var out bytes.Buffer
	a := app{out: &out, outputTTY: true}
	if err := a.print(record); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `Owner: alice\u001b[31m (UID 501)`) || strings.Contains(out.String(), "Owner username:") || strings.Contains(out.String(), "\x1b[31m") {
		t.Fatalf("unsafe or duplicate account label: %q", out.String())
	}
	if accountLabel(map[string]any{"owner": "501"}, "owner") != "UID 501" {
		t.Fatal("invented missing username")
	}
	out.Reset()
	a.forceJSON = true
	if err := a.print(record); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["owner"] != "501" || decoded["owner_username"] != record["owner_username"] {
		t.Fatalf("machine identity changed: %s", out.String())
	}
}
