package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalRenderingPreservesCountsAndEscapesControls(t *testing.T) {
	var out bytes.Buffer
	a := app{out: &out, outputTTY: true, colors: true, commandName: "pool inspect"}
	value := map[string]any{"capacity": json.Number("9223372036854775807"), "reason": "test\x1b[2J\nforged prompt", "memory_bytes": json.Number("1073741824"), "ttl_ns": json.Number("60000000000")}
	if err := a.print(value); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"\x1b[1;36m", "9223372036854775807", "1 GiB", "1m0s", `\u001b[2J\nforged prompt`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
	if strings.Contains(text, "\x1b[2J") {
		t.Fatal("caller controlled terminal escape reached output")
	}
	out.Reset()
	a.forceJSON = true
	if err := a.print(value); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) || strings.Contains(out.String(), "\x1b") {
		t.Fatal("forced JSON had terminal formatting")
	}
}

func TestAuthorizedEmptyUninstallDoesNotCreateState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	var out, stderr bytes.Buffer
	code := Main([]string{"--home", home, "uninstall", "--yes"}, strings.NewReader(""), &out, &stderr)
	if code != 0 || !json.Valid(out.Bytes()) || stderr.Len() != 0 {
		t.Fatal(code, &out, &stderr)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("no-op uninstall created state")
	}
}

func TestPipedResultsAndErrorsRemainJSON(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"version", "--json=false"}, {"--json", "version"}, {"--bad-flag"}, {"storage", "create", "bad-size", "--reason", "test"}} {
		var out, errout bytes.Buffer
		code := Main(args, strings.NewReader(""), &out, &errout)
		data := out.Bytes()
		if code != 0 {
			data = errout.Bytes()
		}
		if !json.Valid(data) || bytes.Contains(data, []byte("\x1b")) {
			t.Fatalf("non-JSON output for %v: %d %s %s", args, code, &out, &errout)
		}
	}
}
