package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoginServicePromptRequiresInteractiveConsent(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires a supported login manager without a live user session")
	}
	installed := func() (string, error) { return "/bin/sh", nil }
	for _, tc := range []struct {
		name, input                  string
		stdin, stdout, json          bool
		wantPrompt, wantRegistration bool
	}{
		{name: "accept", input: "Yes\n", stdin: true, stdout: true, wantPrompt: true, wantRegistration: true},
		{name: "decline", input: "n\n", stdin: true, stdout: true, wantPrompt: true},
		{name: "default", input: "\n", stdin: true, stdout: true, wantPrompt: true},
		{name: "incomplete", input: "y", stdin: true, stdout: true, wantPrompt: true},
		{name: "json", input: "y\n", stdin: true, stdout: true, json: true},
		{name: "redirected input", input: "y\n", stdout: true},
		{name: "redirected output", input: "y\n", stdin: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := t.TempDir()
			t.Setenv("HOME", user)
			var out, stderr bytes.Buffer
			a := app{ctx: t.Context(), home: t.TempDir(), in: strings.NewReader(tc.input), out: &out, err: &stderr,
				inputTTY: tc.stdin, outputTTY: tc.stdout, forceJSON: tc.json}
			if err := a.offerLoginService(installed); err != nil || stderr.Len() != 0 {
				t.Fatal(err, stderr.String())
			}
			if strings.Contains(out.String(), "Enable start at login? [y/N]") != tc.wantPrompt {
				t.Fatalf("unexpected output %q", out.String())
			}
			if tc.wantPrompt && !strings.Contains(out.String(), "inherit the service manager's environment") {
				t.Fatalf("prompt omitted the environment change: %q", out.String())
			}
			dir := filepath.Join(user, "Library", "LaunchAgents")
			if !tc.wantRegistration {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("service directory created without consent: %v", err)
				}
				return
			}
			if files, err := filepath.Glob(filepath.Join(dir, "org.getarm.arm.*.plist")); err != nil || len(files) != 1 {
				t.Fatalf("expected exactly one launch agent, got %v %v", files, err)
			}
			out.Reset()
			if err := a.offerLoginService(installed); err != nil || out.Len() != 0 || stderr.Len() != 0 {
				t.Fatal("repeat start prompted or failed", err, out.String(), stderr.String())
			}
		})
	}
}
