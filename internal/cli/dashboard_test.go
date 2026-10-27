package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/server"
)

func TestDashboardLauncherUsesStructuredApplicationOrExecutableArguments(t *testing.T) {
	address := "http://127.0.0.1:1234/#connect=example"
	tests := []struct {
		platform, selector, found, path string
		args                            []string
	}{
		{"darwin", "", "", "/usr/bin/open", []string{address}},
		{"darwin", "Safari", "", "/usr/bin/open", []string{"-a", "Safari", address}},
		{"darwin", "Google Chrome", "", "/usr/bin/open", []string{"-a", "Google Chrome", address}},
		{"darwin", "/Applications/Browser With Spaces.app", "", "/usr/bin/open", []string{"-a", "/Applications/Browser With Spaces.app", address}},
		{"darwin", "Safari", "/safe/bin/Safari", "/safe/bin/Safari", []string{address}},
		{"linux", "firefox", "/safe/bin/firefox", "/safe/bin/firefox", []string{address}},
		{"linux", "", "/safe/bin/xdg-open", "/safe/bin/xdg-open", []string{address}},
	}
	for _, tt := range tests {
		t.Run(tt.platform+"/"+tt.selector, func(t *testing.T) {
			got, err := dashboardLauncher(tt.platform, tt.selector, address, func(name string) (string, error) {
				if tt.found != "" {
					return tt.found, nil
				}
				return "", os.ErrNotExist
			})
			if err != nil || got.path != tt.path || !reflect.DeepEqual(got.args, tt.args) {
				t.Fatalf("launcher=%+v err=%v", got, err)
			}
			if got.detached != (tt.found != "" && tt.selector != "") {
				t.Fatal("only direct browser executables should detach", got)
			}
		})
	}
	for _, selector := range []string{"missing", "/not/an/executable"} {
		if _, err := dashboardLauncher("linux", selector, address, func(string) (string, error) { return "", os.ErrNotExist }); err == nil {
			t.Fatal("missing executable accepted", selector)
		}
	}
	for _, selector := range []string{" ", "-a", "--args", "Safari\n--args", "Safari\x00", strings.Repeat("x", 1025)} {
		if err := validateDashboardBrowser(selector); err == nil {
			t.Fatalf("invalid selector accepted: %q", selector)
		}
	}
}

func dashboardTestBackend(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "state")
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1}) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("dashboard fixture shutdown stalled")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(home, "endpoint.json")); err == nil {
			return home
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("dashboard fixture unavailable")
	return ""
}

func TestDashboardExecutableReceivesOneURLAndSuppressedModesNeverLaunch(t *testing.T) {
	home, dir := dashboardTestBackend(t), t.TempDir()
	executable := filepath.Join(dir, "browser with spaces; literal")
	log := filepath.Join(dir, "argv")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s\\n' \"$#\" \"$1\" > \"$ARM_BROWSER_TEST_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARM_BROWSER_TEST_LOG", log)
	for _, mode := range []struct {
		args []string
		tty  bool
	}{
		{[]string{executable}, false},
		{[]string{executable, "--json"}, true},
		{[]string{"--json", executable}, true},
		{[]string{executable, "--no-open"}, true},
		{[]string{"--no-open", executable}, true},
	} {
		var out, stderr bytes.Buffer
		a := app{home: home, out: &out, err: &stderr, ctx: t.Context(), outputTTY: mode.tty}
		if err := a.dashboard(mode.args); err != nil {
			t.Fatalf("suppressed mode %v: %v", mode.args, err)
		}
		if _, err := os.Stat(log); !os.IsNotExist(err) {
			t.Fatalf("suppressed mode launched browser: %v", mode.args)
		}
		if a.machine() && !json.Valid(out.Bytes()) {
			t.Fatalf("machine output was not JSON: %s", &out)
		}
	}
	var out, stderr bytes.Buffer
	a := app{home: home, out: &out, err: &stderr, ctx: t.Context(), outputTTY: true}
	if err := a.dashboard([]string{executable}); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		raw, err = os.ReadFile(log)
		if err == nil && len(raw) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 || lines[0] != "1" || !strings.HasPrefix(lines[1], "http://127.0.0.1:") || !strings.Contains(lines[1], "/#connect=") {
		t.Fatalf("URL was not one literal argument: %q", raw)
	}
}

func TestDashboardTerminalSelectorRejectsNoninteractiveModesBeforeBackendAccess(t *testing.T) {
	for _, mode := range []struct {
		args                     []string
		input, output, forceJSON bool
		term                     string
	}{
		{args: []string{"terminal"}},
		{args: []string{"terminal", "--json"}, input: true, output: true, term: "xterm-256color"},
		{args: []string{"terminal", "--no-open"}, input: true, output: true, term: "xterm-256color"},
		{args: []string{"terminal"}, input: true, output: true, forceJSON: true, term: "xterm-256color"},
		{args: []string{"terminal"}, input: true, output: true, term: "dumb"},
		{args: []string{"terminal"}, input: false, output: true, term: "xterm-256color"},
	} {
		t.Run(strings.Join(mode.args, "_"), func(t *testing.T) {
			t.Setenv("TERM", mode.term)
			home := filepath.Join(t.TempDir(), "absent")
			a := app{home: home, out: &bytes.Buffer{}, err: &bytes.Buffer{}, ctx: t.Context(), inputTTY: mode.input, outputTTY: mode.output, forceJSON: mode.forceJSON}
			err := a.dashboard(mode.args)
			var api *model.Error
			if !errors.As(err, &api) || (api.Code != "terminal_required" && api.Code != "invalid_request") {
				t.Fatal("terminal selector did not reject noninteractive request", err)
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatal("reserved selector created state")
			}
		})
	}
}

func TestDashboardExecutableLaunchReturnsAndSurvivesCommandContext(t *testing.T) {
	home, dir := dashboardTestBackend(t), t.TempDir()
	executable, release, exited := filepath.Join(dir, "browser"), filepath.Join(dir, "release"), filepath.Join(dir, "exited")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\ni=0\nwhile [ ! -f \"$ARM_BROWSER_RELEASE\" ] && [ \"$i\" -lt 100 ]; do /bin/sleep .05; i=$((i+1)); done\nprintf done > \"$ARM_BROWSER_EXITED\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARM_BROWSER_RELEASE", release)
	t.Setenv("ARM_BROWSER_EXITED", exited)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	defer os.WriteFile(release, nil, 0600)
	a := app{home: home, out: &bytes.Buffer{}, err: &bytes.Buffer{}, ctx: ctx, outputTTY: true}
	done := make(chan error, 1)
	go func() { done <- a.dashboard([]string{executable}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dashboard waited for browser exit")
	}
	cancel() // Main ends its command context immediately after dashboard returns.
	if _, err := os.Stat(exited); !os.IsNotExist(err) {
		t.Fatal("browser fixture exited before its release signal")
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(exited); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("browser did not survive the completed command context")
}

func TestDashboardNativeMacOpenerReportsUnavailableApplication(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("ARM_TEST_NATIVE_OPENER") != "1" {
		t.Skip("set ARM_TEST_NATIVE_OPENER=1 for the native LaunchServices failure-path check")
	}
	home := dashboardTestBackend(t)
	var out, stderr bytes.Buffer
	a := app{home: home, out: &out, err: &stderr, ctx: t.Context(), outputTTY: true}
	err := a.dashboard([]string{"ARM-Nonexistent-Test-Browser-2E973CC6-6212-4659-9957-BB4E853D4ABA"})
	var api *model.Error
	if !errors.As(err, &api) || api.Code != "browser_open_failed" || !strings.Contains(out.String(), "/#connect=") {
		t.Fatalf("native opener failure lost manual connection link: %v %s", err, &out)
	}
}
