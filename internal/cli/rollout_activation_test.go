package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/activation"
	"getarm.org/internal/agentsessions"
	"getarm.org/internal/install"
	"getarm.org/internal/model"
	"github.com/google/uuid"
)

func TestActivationNativeCLIHelper(t *testing.T) {
	if os.Getenv("ARM_ACTIVATION_CLI_HELPER") != "1" {
		return
	}
	var args []string
	for i, a := range os.Args {
		if a == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("fixture 1")
		os.Exit(0)
	}
	if len(args) == 2 && args[0] == "queue" && args[1] == "--help" {
		fmt.Println("--thread --message")
		os.Exit(0)
	}
	home := os.Getenv("CODEX_HOME")
	if len(args) == 5 && args[0] == "queue" && args[1] == "--thread" && args[3] == "--message" {
		f, e := os.OpenFile(filepath.Join(home, "queued.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			os.Exit(21)
		}
		json.NewEncoder(f).Encode(map[string]string{"session": args[2], "message": args[4]})
		f.Close()
		os.Exit(0)
	}
	if len(args) != 2 || args[0] != "app-server" || args[1] != "proxy" {
		os.Exit(22)
	}
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var req struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(scan.Bytes(), &req) != nil {
			os.Exit(23)
		}
		if req.Method == "initialized" {
			continue
		}
		var result any = map[string]any{"userAgent": "fixture"}
		if req.Method == "thread/list" {
			if roots, ok := req.Params["cwd"].([]any); !ok || len(roots) == 0 {
				os.Exit(27)
			}
			if req.Params["useStateDbOnly"] != true {
				os.Exit(24)
			}
			b, e := os.ReadFile(filepath.Join(home, "threads.json"))
			if e != nil || json.Unmarshal(b, &result) != nil {
				os.Exit(25)
			}
		} else if req.Method != "initialize" {
			os.Exit(26)
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"id": req.ID, "result": result})
	}
	os.Exit(0)
}
func activationJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func activationFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "state")
	native := filepath.Join(base, "codex")
	root := filepath.Join(base, "project")
	for _, p := range []string{native, root} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := install.Install(home, []install.Target{{Adapter: "codex", Root: native, Name: "AGENTS.md"}}, false); e != nil {
		t.Fatal(e)
	}
	rolloutApplyCLI(t, home, "rollout", "project", "--project", "demo", "--path", root, "--reason", "Fixture boundary")
	rolloutApplyCLI(t, home, "rollout", "policy", "--project", "demo", "--mode", "all", "--reason", "Fixture eligibility")
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nexec " + activation.Quote(exe) + " -test.run '^TestActivationNativeCLIHelper$' -- \"$@\"\n"
	if e = os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", bin)
	t.Setenv("ARM_ACTIVATION_CLI_HELPER", "1")
	id := uuid.NewString()
	activationJSON(t, filepath.Join(native, "threads.json"), map[string]any{"data": []any{map[string]any{"id": id, "cwd": root, "createdAt": 1760000000, "updatedAt": 1760000010, "status": map[string]any{"type": "active"}, "preview": "PRIVATE_CONVERSATION"}, map[string]any{"id": uuid.NewString(), "cwd": t.TempDir(), "createdAt": 1760000000}}, "nextCursor": nil})
	return home, native, root, id
}
func TestRolloutDiscoveryAndActivationAreSeparateFromEnrollment(t *testing.T) {
	home, native, _, id := activationFixture(t)
	before, e := os.ReadFile(filepath.Join(home, "rollout.json"))
	if e != nil {
		t.Fatal(e)
	}
	code, out, stderr := rolloutCLI(t, home, "rollout", "discover", "--project", "demo", "--adapter", "codex", "--codex-home", native, "--json")
	var data rolloutDiscovery
	if code != 0 || json.Unmarshal(out, &data) != nil || len(data.Sessions) != 1 || data.Sessions[0].ID != id || data.Sessions[0].ARMState != "not-identified" || strings.Contains(string(out), "PRIVATE_") {
		t.Fatal(code, string(out), stderr)
	}
	args := []string{"rollout", "activate", "--project", "demo", "--adapter", "codex", "--session", id, "--codex-home", native, "--json"}
	code, out, stderr = rolloutCLI(t, home, args...)
	var copyPlan activation.Plan
	if code != 0 || json.Unmarshal(out, &copyPlan) != nil || copyPlan.Delivery != "copy-only" || !strings.Contains(copyPlan.Message, id) {
		t.Fatal(code, string(out), stderr)
	}
	if _, e = os.Stat(filepath.Join(native, "queued.jsonl")); !os.IsNotExist(e) {
		t.Fatal("discovery/copy queued a message", e)
	}
	code, out, stderr = rolloutCLI(t, home, append(args, "--send", "--dry-run")...)
	var p activation.Plan
	if code != 0 || json.Unmarshal(out, &p) != nil || p.Delivery != "queue" {
		t.Fatal(code, string(out), stderr)
	}
	if code, _, _ = rolloutCLI(t, home, append(args, "--send", "--yes", "--expect", "wrong", "--key", "fixture-key")...); code == 0 {
		t.Fatal("wrong review sent")
	}
	for i := 0; i < 2; i++ {
		code, out, stderr = rolloutCLI(t, home, append(args, "--send", "--yes", "--expect", p.Review, "--key", "fixture-key")...)
		if code != 0 {
			t.Fatal(code, string(out), stderr)
		}
	}
	b, e := os.ReadFile(filepath.Join(native, "queued.jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 1 {
		t.Fatal("delivery repeated", len(lines))
	}
	var sent map[string]string
	if json.Unmarshal([]byte(lines[0]), &sent) != nil || sent["session"] != id || sent["message"] != p.Message {
		t.Fatal("recipient/content changed", sent)
	}
	after, e := os.ReadFile(filepath.Join(home, "rollout.json"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("activation changed adoption", e)
	}
	t.Setenv("PATH", t.TempDir())
	code, out, stderr = rolloutCLI(t, home, "rollout", "activation", "--key", "fixture-key", "--json")
	var receipt activation.Record
	if code != 0 || json.Unmarshal(out, &receipt) != nil || receipt.State != "queued" || receipt.Message != p.Message {
		t.Fatal(code, string(out), stderr)
	}
	for _, file := range []string{"state.db", "endpoint.json"} {
		if _, e = os.Stat(filepath.Join(home, file)); !os.IsNotExist(e) {
			t.Fatal("backend state created", file, e)
		}
	}
}
func TestRolloutActivationRefusesPolicyAndNativeGenerationChanges(t *testing.T) {
	home, native, root, id := activationFixture(t)
	a := app{home: home, ctx: t.Context()}
	sources, e := a.sessionSources("codex", native, "")
	if e != nil {
		t.Fatal(e)
	}
	p, e := a.activationPlan("demo", "codex", id, sources, true)
	if e != nil {
		t.Fatal(e)
	}
	activationJSON(t, filepath.Join(native, "threads.json"), map[string]any{"data": []any{map[string]any{"id": id, "cwd": root, "createdAt": 1760000020}}, "nextCursor": nil})
	fresh, e := a.activationPlan("demo", "codex", id, sources, true)
	if e != nil || fresh.Review == p.Review {
		t.Fatal("replacement retained review", e)
	}
	if _, e = (activation.Store{Home: home}).Send(t.Context(), p, "stale", func() (activation.Plan, error) { return a.activationPlan("demo", "codex", id, sources, true) }); e == nil {
		t.Fatal("stale native generation sent")
	}
	if _, e = os.Stat(filepath.Join(native, "queued.jsonl")); !os.IsNotExist(e) {
		t.Fatal("stale queue executed", e)
	}
	rolloutApplyCLI(t, home, "rollout", "policy", "--project", "demo", "--mode", "disabled", "--reason", "Disable before activation")
	if _, e = a.activationPlan("demo", "codex", id, sources, false); e == nil {
		t.Fatal("disabled project activated")
	}
}

type activationSource struct {
	sessions []agentsessions.Session
	adapter  string
}

func (s activationSource) Name() string {
	if s.adapter != "" {
		return s.adapter
	}
	return "claude"
}
func (s activationSource) Discover(context.Context) agentsessions.Result {
	return agentsessions.Result{Adapter: "claude", Sessions: s.sessions}
}
func TestRolloutActivationUIReviewCopyAndNoDefaultSend(t *testing.T) {
	fixture, home := onboardReviewFixture(t, true)
	runOnboardCmd(t, fixture, onboardKey(fixture, "y"))
	s := agentsessions.Session{Adapter: "claude", ID: uuid.NewString(), CWD: fixture.binding.Root, Home: t.TempDir()}
	var out bytes.Buffer
	a := app{ctx: t.Context(), home: home, out: &out, err: &out}
	m := newRolloutDiscover(&a, "demo", []agentsessions.Source{activationSource{sessions: []agentsessions.Session{s}}})
	m.Update(m.Init()())
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	if m.stage != discoveryMessage || !strings.Contains(m.content, s.ID) {
		t.Fatal(m.stage, m.content)
	}
	copied := ""
	m.copy = func(text string) tea.Cmd { copied = text; return func() tea.Msg { return nil } }
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'c'})
	m.Update(cmd())
	if copied != m.plan.Message {
		t.Fatal("exact message was not copied")
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: 's'})
	m.Update(cmd())
	if m.sendStarted || m.stage != discoverySelecting || !strings.Contains(m.notice, "copy-only") {
		t.Fatal(m.stage, m.notice)
	}
	if _, e := os.Stat(filepath.Join(home, "rollout-activations.json")); !os.IsNotExist(e) {
		t.Fatal("copy changed activation history", e)
	}
}
func TestRolloutPostOnboardingOfferDefaultsToNo(t *testing.T) {
	var out bytes.Buffer
	a := app{ctx: t.Context(), home: t.TempDir(), in: strings.NewReader("\n"), out: &out}
	if e := a.offerRolloutDiscovery("demo", "/missing/codex", "/missing/claude"); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "Discover agents now? [y/N]") || !strings.Contains(out.String(), "rollout discover --project demo") {
		t.Fatal(out.String())
	}
}
func TestRolloutActivationUnknownSurvivesSharedInterruption(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out, stderr bytes.Buffer
	a := app{ctx: ctx, home: t.TempDir(), out: &out, err: &stderr}
	if code := a.finishCommand(0, a.activationUnknown("original-key", context.Canceled)); code != 130 {
		t.Fatal(code)
	}
	var result struct {
		Error *model.Error `json:"error"`
	}
	if json.Unmarshal(stderr.Bytes(), &result) != nil || result.Error == nil || result.Error.Code != "activation_outcome_unknown" || !strings.Contains(result.Error.Message, "original-key") || !strings.Contains(result.Error.Message, "rollout activation") {
		t.Fatal(stderr.String())
	}
}
func TestRolloutActivationHelpOffline(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("PATH", t.TempDir())
	for _, name := range []string{"discover", "activate", "activation"} {
		code, out, stderr := rolloutCLI(t, home, "rollout", name, "--help")
		if code != 0 || len(out) == 0 {
			t.Fatal(code, string(out), stderr)
		}
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("help created state", e)
	}
}

func TestRolloutActivationUIRequiresSeparateExactSendConfirmation(t *testing.T) {
	home, native, root, id := activationFixture(t)
	s := agentsessions.Session{Adapter: "codex", ID: id, CWD: root, Home: native}
	var output bytes.Buffer
	a := app{ctx: t.Context(), home: home, out: &output, err: &output}
	m := newRolloutDiscover(&a, "demo", []agentsessions.Source{activationSource{sessions: []agentsessions.Session{s}, adapter: "codex"}})
	m.Update(m.Init()())
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	_, cmd = m.Update(tea.KeyPressMsg{Code: 's'})
	m.Update(cmd())
	if m.stage != discoveryReview || !strings.Contains(m.content, id) || !strings.Contains(m.content, m.plan.Message) {
		t.Fatal(m.stage, m.content)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.sendStarted || m.stage != discoveryMessage {
		t.Fatal("default confirmation sent", m.stage)
	}
	if _, e := os.Stat(filepath.Join(native, "queued.jsonl")); !os.IsNotExist(e) {
		t.Fatal("preview sent", e)
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: 's'})
	m.Update(cmd())
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'y'})
	if cmd != nil || m.sendStarted {
		t.Fatal("small review accepted delivery")
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'y'})
	if cmd == nil || m.stage != discoverySending {
		t.Fatal("explicit confirmation did not begin delivery")
	}
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.stage != discoverySending {
		t.Fatal("resize cancelled an in-flight send")
	}
	m.Update(cmd())
	if m.stage != discoveryDone || m.err != nil || m.record.State != "queued" || m.notice != "" {
		t.Fatal(m.stage, m.err, m.notice)
	}
}

func TestRolloutPostOnboardingOfferAcceptsDiscoveryWithoutSending(t *testing.T) {
	home, native, _, id := activationFixture(t)
	before, e := os.ReadFile(filepath.Join(home, "rollout.json"))
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	a := app{ctx: t.Context(), home: home, in: strings.NewReader("yes\n"), out: &out}
	if e = a.offerRolloutDiscovery("demo", native, filepath.Join(t.TempDir(), "absent")); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), id) || !strings.Contains(out.String(), `"read_only": true`) {
		t.Fatal(out.String())
	}
	if _, e = os.Stat(filepath.Join(native, "queued.jsonl")); !os.IsNotExist(e) {
		t.Fatal("post-onboarding discovery sent a message", e)
	}
	after, e := os.ReadFile(filepath.Join(home, "rollout.json"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("post-onboarding discovery enrolled or changed policy", e)
	}
}

type blockingActivationSource struct{ entered, done chan struct{} }

func (blockingActivationSource) Name() string { return "claude" }
func (s blockingActivationSource) Discover(ctx context.Context) agentsessions.Result {
	close(s.entered)
	<-ctx.Done()
	close(s.done)
	return agentsessions.Result{Adapter: "claude"}
}
func TestRolloutDiscoveryClosesAndJoinsOwnedWork(t *testing.T) {
	m0, home := onboardReviewFixture(t, true)
	runOnboardCmd(t, m0, onboardKey(m0, "y"))
	source := blockingActivationSource{entered: make(chan struct{}), done: make(chan struct{})}
	a := app{home: home, ctx: t.Context()}
	m := newRolloutDiscover(&a, "demo", []agentsessions.Source{source})
	done := make(chan struct{})
	go func() { m.Init()(); close(done) }()
	select {
	case <-source.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("controlled metadata source did not start")
	}
	m.close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("owned metadata work was not joined")
	}
	select {
	case <-source.done:
	default:
		t.Fatal("source did not observe cancellation")
	}
	if msg := m.Init()(); msg != nil {
		t.Fatal("late command restarted discovery")
	}
}
