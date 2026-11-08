package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func guardianHome(t *testing.T) string {
	t.Helper()
	// The temporary directory may sit behind a symlink, which ARM homes refuse.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "arm")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func readConfig(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRecommendGuardianRecordsChoices(t *testing.T) {
	for input, want := range map[string]string{"\n": "enabled = true", "y\n": "enabled = true", "n\n": "enabled = false", "maybe\n": "", "": ""} {
		home := guardianHome(t)
		var out bytes.Buffer
		a := app{home: home, ctx: t.Context(), in: strings.NewReader(input), out: &out, outputTTY: true, inputTTY: true, commandName: "start"}
		if err := a.recommendGuardian(); err != nil {
			t.Fatal(err)
		}
		got := readConfig(t, home)
		if want == "" && strings.Contains(got, "enabled") || want != "" && !strings.Contains(got, want) {
			t.Fatalf("input %q wrote %q", input, got)
		}
		if !strings.Contains(out.String(), "strongly recommended") {
			t.Fatalf("no recommendation: %s", out.String())
		}
		// A recorded choice is never asked again.
		if want != "" {
			out.Reset()
			a.in = strings.NewReader("n\n")
			a.recommendGuardian()
			if out.Len() != 0 || !strings.Contains(readConfig(t, home), want) {
				t.Fatalf("asked again after %q: %s", input, out.String())
			}
		}
	}
}

func TestRecommendGuardianNeverPromptsScripts(t *testing.T) {
	home := guardianHome(t)
	var out bytes.Buffer
	for _, a := range []app{
		{home: home, ctx: t.Context(), in: strings.NewReader("y\n"), out: &out, outputTTY: true, inputTTY: false},
		{home: home, ctx: t.Context(), in: strings.NewReader("y\n"), out: &out, outputTTY: false, inputTTY: true},
		{home: home, ctx: t.Context(), in: strings.NewReader("y\n"), out: &out, outputTTY: true, inputTTY: true, forceJSON: true},
	} {
		if err := a.recommendGuardian(); err != nil {
			t.Fatal(err)
		}
	}
	if out.Len() != 0 || readConfig(t, home) != "" {
		t.Fatalf("prompted a script: %q %q", out.String(), readConfig(t, home))
	}
}

func TestRecommendGuardianInterruptAborts(t *testing.T) {
	home := guardianHome(t)
	ctx, cancel := context.WithCancel(t.Context())
	reader, writer, _ := os.Pipe()
	defer writer.Close()
	var out bytes.Buffer
	a := app{home: home, ctx: ctx, in: reader, out: &out, outputTTY: true, inputTTY: true}
	cancel()
	if err := a.recommendGuardian(); err == nil {
		t.Fatalf("interrupted prompt did not abort: %q", out.String())
	}
	if readConfig(t, home) != "" {
		t.Fatal("interrupted prompt wrote configuration")
	}
}
