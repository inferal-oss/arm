package cli

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Real fixture processes enter here before testing flag parsing. The driver
// exercises the production Main/native exec path; the extension is independent
// process code. Nothing probes or executes this helper during discovery.
func init() {
	if os.Getenv("ARM_EXTENSION_TEST") != "1" {
		return
	}
	base := filepath.Base(os.Args[0])
	if base == "extension-driver" {
		os.Exit(Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	if !strings.HasPrefix(base, "arm-") {
		return
	}
	if marker := os.Getenv("ARM_EXTENSION_MARKER"); marker != "" {
		if err := os.WriteFile(marker, []byte("executed"), 0600); err != nil {
			panic(err)
		}
	}
	switch os.Getenv("ARM_EXTENSION_MODE") {
	case "environment":
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"home": os.Getenv("ARM_HOME"), "json": os.Getenv("ARM_JSON")})
		os.Exit(0)
	case "wait":
		fmt.Fprintln(os.Stdout, os.Getpid())
		for {
			time.Sleep(time.Hour)
		}
	case "raw":
		fmt.Fprint(os.Stdout, "raw\x00out\x1b[32m")
		fmt.Fprint(os.Stderr, "raw\x00err")
		os.Exit(7)
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	cwd, _ := os.Getwd()
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"argv": os.Args[1:], "stdin": string(input), "home": os.Getenv("ARM_HOME"), "json": os.Getenv("ARM_JSON"), "inherited": os.Getenv("ARM_EXTENSION_SENTINEL"), "pid": os.Getpid(), "cwd": cwd, "executable": os.Args[0]})
	fmt.Fprint(os.Stderr, "extension diagnostic")
	code, _ := strconv.Atoi(os.Getenv("ARM_EXTENSION_EXIT"))
	os.Exit(code)
}

func extensionFixture(t *testing.T, dir, name string) string {
	t.Helper()
	src, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, name)
	if err = os.Link(src, dst); err != nil {
		in, err := os.Open(src)
		if err != nil {
			t.Fatal(err)
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatal(copyErr, closeErr)
		}
	}
	return dst
}
func extensionCall(args ...string) (int, string, string) {
	var out, err bytes.Buffer
	code := Main(args, strings.NewReader("input\nwith boundaries"), &out, &err)
	return code, out.String(), err.String()
}
func extensionSetup(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(t.TempDir(), "absent")
	t.Setenv("PATH", dir)
	t.Setenv("ARM_EXTENSION_TEST", "1")
	t.Setenv("ARM_HOME", filepath.Join(t.TempDir(), "ignored-home"))
	t.Setenv("ARM_JSON", "stale")
	t.Setenv("NO_COLOR", "")
	return dir, home
}
func assertNoExtensionState(t *testing.T, home string) {
	t.Helper()
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatal("extension operation created ARM state", err)
	}
}

func TestCLIPluginsVerbatimArgumentsStreamsExitAndEnvironment(t *testing.T) {
	dir, home := extensionSetup(t)
	extensionFixture(t, dir, "arm-fixture")
	t.Setenv("ARM_EXTENSION_SENTINEL", "inherited value")
	t.Setenv("ARM_EXTENSION_EXIT", "23")
	args := []string{"--home", home, "--json", "fixture", "one argument", "", "--home", "plugin-home", "--json=false", "--", "; touch should-not-exist", "$(echo bad)", "*", "日本語"}
	code, out, diagnostic := extensionCall(args...)
	if code != 23 || diagnostic != "extension diagnostic" {
		t.Fatal(code, out, diagnostic)
	}
	var got struct {
		Args                  []string `json:"argv"`
		Input                 string   `json:"stdin"`
		Home, JSON, Inherited string
		Cwd                   string
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err, out)
	}
	if !reflect.DeepEqual(got.Args, args[4:]) || got.Input != "input\nwith boundaries" || got.Home != home || got.JSON != "1" || got.Inherited != "inherited value" {
		t.Fatal(got, args[4:])
	}
	assertNoExtensionState(t, home)
	t.Setenv("ARM_EXTENSION_MODE", "raw")
	code, out, diagnostic = extensionCall("--home", home, "--json", "fixture")
	if code != 7 || out != "raw\x00out\x1b[32m" || diagnostic != "raw\x00err" {
		t.Fatal("wrapped plugin streams", code, out, diagnostic)
	}
}

func TestCLIPluginsPATHPrecedenceRelativeEntriesAndReplacement(t *testing.T) {
	first, home := extensionSetup(t)
	second := t.TempDir()
	t.Setenv("PATH", first+string(os.PathListSeparator)+second)
	if err := os.WriteFile(filepath.Join(first, "arm-fixture"), []byte("not executable"), 0600); err != nil {
		t.Fatal(err)
	}
	chosen := extensionFixture(t, second, "arm-fixture")
	code, out, diagnostic := extensionCall("--home", home, "fixture")
	if code != 0 || !strings.Contains(out, chosen) {
		t.Fatal(code, out, diagnostic)
	}
	if err := os.Remove(filepath.Join(first, "arm-fixture")); err != nil {
		t.Fatal(err)
	}
	extensionFixture(t, first, "arm-fixture")
	code, out, diagnostic = extensionCall("--home", home, "fixture")
	if code != 0 || !strings.Contains(out, filepath.Join(first, "arm-fixture")) {
		t.Fatal(code, out, diagnostic)
	}
	// Discovery is not a pin: an explicit later invocation resolves the actual
	// current executable. No managed provider or deletion authority is retained.
	report := discoverPlugins(os.Getenv("PATH"))
	if len(report.Extensions) != 1 || report.Extensions[0].Path != filepath.Join(first, "arm-fixture") {
		t.Fatal(report)
	}
	replacement := filepath.Join(first, "replacement")
	if err := os.WriteFile(replacement, []byte("#!/bin/sh\nprintf replacement"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, filepath.Join(first, "arm-fixture")); err != nil {
		t.Fatal(err)
	}
	code, out, diagnostic = extensionCall("--home", home, "fixture")
	if code != 0 || out != "replacement" {
		t.Fatal(code, out, diagnostic)
	}
	t.Chdir(first)
	t.Setenv("PATH", ".")
	code, out, diagnostic = extensionCall("--home", home, "fixture")
	if code != 0 || out != "replacement" {
		t.Fatal(code, out, diagnostic)
	}
	t.Setenv("PATH", string(os.PathListSeparator)+second)
	code, out, diagnostic = extensionCall("--home", home, "fixture")
	if code != 0 || out != "replacement" {
		t.Fatal("empty PATH component lost precedence", code, out, diagnostic)
	}
	assertNoExtensionState(t, home)
}

func TestCLIPluginsDiscoveryHelpAndBuiltinCollisionsNeverExecute(t *testing.T) {
	dir, home := extensionSetup(t)
	marker := filepath.Join(t.TempDir(), "not-executed")
	t.Setenv("ARM_EXTENSION_MARKER", marker)
	for _, name := range []string{"fixture", "version", "status", "help", "plugins"} {
		extensionFixture(t, dir, "arm-"+name)
	}
	for _, name := range []string{"arm-BAD", "arm-../bad", "arm-trailing-"} {
		if strings.Contains(name, "/") {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("ignored"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"plugins"}, {"plugins", "list", "--json"}, {"plugins", "inspect", "fixture"}, {"help", "fixture"}, {"plugins", "--help"}, {"help", "plugins"}, {"version"}, {"status", "--help"}, {"help"}} {
		code, out, diagnostic := extensionCall(append([]string{"--home", home}, args...)...)
		if code != 0 || diagnostic != "" {
			t.Fatal(args, code, out, diagnostic)
		}
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatal("discovery/help executed a candidate", err)
	}
	report := discoverPlugins(os.Getenv("PATH"))
	if len(report.Extensions) != 5 || report.Truncated {
		t.Fatal(report)
	}
	for _, card := range report.Extensions {
		if card.Name != "fixture" && card.State != "built-in" {
			t.Fatal(card)
		}
	}
	assertNoExtensionState(t, home)
	code, _, _ := extensionCall("--home", home, "fixture", "--help")
	if code != 0 {
		t.Fatal(code)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("explicit extension --help was intercepted", err)
	}
}

func TestCLIPluginsInvalidMissingUnavailableAndExecFormat(t *testing.T) {
	dir, home := extensionSetup(t)
	for _, name := range []string{"", "../escape", "/absolute", "a/b", "a\\b", "é", "UPPER", "two words", "a\x1b", "-bad", "trailing-", strings.Repeat("a", 65)} {
		// -- separates root flags so leading-dash names reach name validation.
		code, _, diagnostic := extensionCall("--home", home, "--", name)
		if code != 2 || !json.Valid([]byte(diagnostic)) {
			t.Fatal(name, code, diagnostic)
		}
	}
	code, _, diagnostic := extensionCall("--home", home, "missing")
	if code != 127 || !strings.Contains(diagnostic, "command_not_found") {
		t.Fatal(code, diagnostic)
	}
	if err := os.WriteFile(filepath.Join(dir, "arm-noexec"), []byte("not executable"), 0600); err != nil {
		t.Fatal(err)
	}
	code, _, diagnostic = extensionCall("--home", home, "noexec")
	if code != 126 || !strings.Contains(diagnostic, "plugin_unavailable") || !strings.Contains(diagnostic, "arm-noexec") {
		t.Fatal(code, diagnostic)
	}
	for _, entry := range []struct{ name, content string }{{"format", "no shebang and no binary format"}, {"dependency", "#!/missing/arm-test-interpreter\n"}} {
		if err := os.WriteFile(filepath.Join(dir, "arm-"+entry.name), []byte(entry.content), 0700); err != nil {
			t.Fatal(err)
		}
		code, out, diagnostic := extensionCall("--home", home, entry.name)
		if code != 126 || out != "" || !strings.Contains(diagnostic, "plugin_exec_failed") {
			t.Fatal(code, out, diagnostic)
		}
	}
	extensionFixture(t, dir, "arm-gone")
	_ = discoverPlugins(os.Getenv("PATH"))
	if err := os.Remove(filepath.Join(dir, "arm-gone")); err != nil {
		t.Fatal(err)
	}
	code, _, diagnostic = extensionCall("--home", home, "gone")
	if code != 127 {
		t.Fatal(code, diagnostic)
	}
	maximum := strings.Repeat("a", pluginNameLimit)
	extensionFixture(t, dir, "arm-"+maximum)
	code, _, diagnostic = extensionCall("--home", home, maximum)
	if code != 0 {
		t.Fatal("exact maximum valid name rejected", code, diagnostic)
	}
	assertNoExtensionState(t, home)
}

func TestCLIPluginsBoundedNonExecutingDiscovery(t *testing.T) {
	dir, home := extensionSetup(t)
	for n := 0; n < pluginScanNames+1; n++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("arm-plugin-%03d", n)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	report := discoverPlugins(dir)
	if !report.Truncated || len(report.Extensions) != pluginScanNames || len(report.Warnings) == 0 {
		t.Fatal(len(report.Extensions), report.Truncated)
	}
	report = discoverPlugins(strings.Repeat("x", pluginPathBytes+1))
	if !report.Truncated || len(report.Extensions) != 0 {
		t.Fatal(report)
	}
	// A non-directory/FIFO PATH component cannot make discovery wait for data.
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	report = discoverPlugins(fifo)
	if len(report.Extensions) != 0 || len(report.Warnings) == 0 {
		t.Fatal(report)
	}
	assertNoExtensionState(t, home)
}

func TestCLIPluginsNativeExecPreservesPIDAndSignal(t *testing.T) {
	dir, home := extensionSetup(t)
	driver := extensionFixture(t, t.TempDir(), "extension-driver")
	extensionFixture(t, dir, "arm-fixture")
	t.Setenv("ARM_EXTENSION_MODE", "wait")
	cmd := exec.Command(driver, "--home", home, "fixture")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	var childPID int
	ready := make(chan error, 1)
	go func() { _, e := fmt.Fscanln(stdout, &childPID); ready <- e }()
	select {
	case err = <-ready:
		if err != nil {
			t.Fatal(err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("extension did not start")
	}
	if childPID != cmd.Process.Pid {
		t.Fatal("CLI did not preserve native PID", childPID, cmd.Process.Pid)
	}
	if err = cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	var exit *exec.ExitError
	if !errorsAsExit(err, &exit) {
		t.Fatal(err)
	}
	status := exit.Sys().(syscall.WaitStatus)
	if !status.Signaled() || status.Signal() != syscall.SIGTERM || stderr.Len() != 0 {
		t.Fatal("signal status/output changed", status, stderr.String())
	}
	assertNoExtensionState(t, home)
}
func errorsAsExit(err error, out **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*out = e
		return true
	}
	return false
}

func TestCLIPluginsTTYMachineAndRawHandoff(t *testing.T) {
	dir, home := extensionSetup(t)
	driver := extensionFixture(t, t.TempDir(), "extension-driver")
	extensionFixture(t, dir, "arm-fixture")
	for _, args := range [][]string{{"plugins"}, {"plugins", "--json"}, {"--json", "plugins"}} {
		cmd := exec.Command(driver, append([]string{"--home", home}, args...)...)
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
		term, err := pty.Start(cmd)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(term)
		term.Close()
		if err = cmd.Wait(); err != nil {
			t.Fatal(err, string(raw))
		}
		forced := strings.Contains(strings.Join(args, " "), "--json")
		if forced {
			if !json.Valid(bytes.TrimSpace(raw)) || bytes.Contains(raw, []byte("\x1b[")) {
				t.Fatal("TTY JSON changed", string(raw))
			}
		} else if !bytes.Contains(raw, []byte("ARM")) || !bytes.Contains(raw, []byte("\x1b[")) || json.Valid(bytes.TrimSpace(raw)) {
			t.Fatal("TTY human output missing", string(raw))
		}
	}
	for _, forced := range []bool{false, true} {
		args := []string{"--home", home}
		if forced {
			args = append(args, "--json")
		}
		args = append(args, "fixture")
		cmd := exec.Command(driver, args...)
		cmd.Env = append(os.Environ(), "TERM=xterm-256color", "ARM_EXTENSION_MODE=environment")
		term, err := pty.Start(cmd)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(term)
		term.Close()
		if err = cmd.Wait(); err != nil {
			t.Fatal(err, string(raw))
		}
		var got map[string]string
		if err = json.Unmarshal(bytes.TrimSpace(raw), &got); err != nil {
			t.Fatal(err, string(raw))
		}
		expected := "0"
		if forced {
			expected = "1"
		}
		if got["json"] != expected || got["home"] != home {
			t.Fatal("TTY effective environment changed", got)
		}
	}
	for _, env := range [][]string{{"TERM=xterm-256color", "NO_COLOR=1"}, {"TERM=dumb", "NO_COLOR="}} {
		cmd := exec.Command(driver, "--home", home, "plugins")
		cmd.Env = append(os.Environ(), env...)
		term, err := pty.Start(cmd)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(term)
		term.Close()
		if err = cmd.Wait(); err != nil {
			t.Fatal(err, string(raw))
		}
		if bytes.Contains(raw, []byte("\x1b[")) || json.Valid(bytes.TrimSpace(raw)) || !bytes.Contains(raw, []byte("CLI extensions")) {
			t.Fatal("plain terminal output changed", string(raw))
		}
	}
	cmd := exec.Command(driver, "--home", home, "--json", "fixture")
	cmd.Env = append(os.Environ(), "ARM_EXTENSION_MODE=raw")
	var out, diagnostic bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &diagnostic
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 7 || out.String() != "raw\x00out\x1b[32m" || diagnostic.String() != "raw\x00err" {
		t.Fatal(err, out.String(), diagnostic.String())
	}
	assertNoExtensionState(t, home)
}

// Embed the actual compiled dispatcher source so the consistency guard also
// runs on uploaded Linux test executables, without a developer source checkout.
//
//go:embed cli.go
var extensionDispatcherSource string

func TestCLIPluginsBuiltinRegistryCoversActualDispatcher(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "cli.go", extensionDispatcherSource, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, decl := range file.Decls {
		method, ok := decl.(*ast.FuncDecl)
		if !ok || method.Name.Name != "command" || method.Recv == nil {
			continue
		}
		for _, statement := range method.Body.List {
			dispatch, ok := statement.(*ast.SwitchStmt)
			if !ok {
				continue
			}
			tag, ok := dispatch.Tag.(*ast.Ident)
			if !ok || tag.Name != "cmd" {
				continue
			}
			for _, clause := range dispatch.Body.List {
				branch := clause.(*ast.CaseClause)
				for _, expr := range branch.List {
					value, ok := expr.(*ast.BasicLit)
					if !ok || value.Kind != token.STRING {
						t.Fatalf("nonliteral built-in command requires registry review: %T", expr)
					}
					name, err := strconv.Unquote(value.Value)
					if err != nil {
						t.Fatal(err)
					}
					if !builtinPluginName(name) {
						t.Errorf("built-in %q is absent from discovery/cancellation registry; add its command/group help", name)
					}
					found++
				}
			}
		}
	}
	if found == 0 {
		t.Fatal("actual dispatcher switch was not examined")
	}
	if builtinPluginName("arm") || builtinPluginName("fixture") {
		t.Fatal("non-command help metadata classified as a built-in")
	}
}
