package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/install"
	"getarm.org/internal/model"
)

func confirmationPlan(t *testing.T) (*install.Plan, string, string) {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "state")
	root := filepath.Join(base, "agent")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "AGENTS.md")
	if e := os.WriteFile(path, []byte("User instructions.\n"), 0640); e != nil {
		t.Fatal(e)
	}
	p, e := install.PreviewInstall(home, []install.Target{{Adapter: "codex", Root: root, Name: "AGENTS.md"}})
	if e != nil {
		t.Fatal(e)
	}
	return p, home, path
}

func TestInstallConfirmationDiffReturnsToPromptWithoutApplying(t *testing.T) {
	for _, input := range []string{"d\ny\n", "diff\nd\nyes\n", "d\n\n", "diff\nn\n", "d\n", "d\n" + strings.Repeat("x", 256) + "\n"} {
		t.Run(strings.ReplaceAll(input, "\n", "/"), func(t *testing.T) {
			p, home, path := confirmationPlan(t)
			var out bytes.Buffer
			a := app{home: home, ctx: t.Context(), in: strings.NewReader(input), out: &out, outputTTY: true, inputTTY: true, commandName: "install"}
			e := a.approve(p, false)
			wantYes := strings.HasSuffix(input, "y\n") || strings.HasSuffix(input, "yes\n")
			if (e == nil) != wantYes {
				t.Fatal(e, out.String())
			}
			if !strings.Contains(out.String(), "Instruction-file diff") || !strings.Contains(out.String(), "+Installation alone does not authorize adoption.") || strings.Count(out.String(), "Apply these changes? [y/N/diff]") < 2 {
				t.Fatal(out.String())
			}
			if strings.Contains(input, "diff\nd") && strings.Count(out.String(), "Instruction-file diff") != 2 {
				t.Fatal("repeat diff was suppressed", out.String())
			}
			if _, e = os.Stat(home); !os.IsNotExist(e) {
				t.Fatal("review created state or backups", e)
			}
			data, e := os.ReadFile(path)
			if e != nil || string(data) != "User instructions.\n" {
				t.Fatal("review changed instructions", e)
			}
		})
	}
}

type confirmationWriter struct {
	bytes.Buffer
	prompts chan struct{}
	onDiff  func()
}

func (w *confirmationWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *confirmationWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "Instruction-file diff") && w.onDiff != nil {
		w.onDiff()
	}
	n, e := w.Buffer.Write(p)
	if strings.Contains(string(p), "Apply these changes?") && w.prompts != nil {
		w.prompts <- struct{}{}
	}
	return n, e
}

func TestInstallConfirmationRechecksChangedFilesAfterDiff(t *testing.T) {
	p, home, path := confirmationPlan(t)
	out := &confirmationWriter{onDiff: func() {
		if e := os.WriteFile(path, []byte("Later user edit.\n"), 0640); e != nil {
			t.Error(e)
		}
	}}
	a := app{home: home, ctx: t.Context(), in: strings.NewReader("d\ny\n"), out: out, outputTTY: true, inputTTY: true, commandName: "install"}
	var detail *model.Error
	if e := a.approve(p, false); !errors.As(e, &detail) || detail.Code != "plan_changed" {
		t.Fatal("changed preview accepted", e)
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("rejection created state", e)
	}
}

func TestInstallConfirmationCancellationAfterDiffDoesNotWaitForInput(t *testing.T) {
	p, home, _ := confirmationPlan(t)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	out := &confirmationWriter{prompts: make(chan struct{}, 2)}
	a := app{home: home, ctx: ctx, in: reader, out: out, outputTTY: true, inputTTY: true, commandName: "install"}
	done := make(chan error, 1)
	go func() { done <- a.approve(p, false) }()
	select {
	case <-out.prompts:
	case <-time.After(time.Second):
		t.Fatal("first prompt missing")
	}
	if _, e := io.WriteString(writer, "d\n"); e != nil {
		t.Fatal(e)
	}
	select {
	case <-out.prompts:
	case <-time.After(time.Second):
		t.Fatal("diff did not return to prompt")
	}
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation waited for terminal input")
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("cancellation created state", e)
	}
}

func TestUninstallConfirmationDiffPreservesInstructionsUntilApprovalApplied(t *testing.T) {
	p, home, path := confirmationPlan(t)
	if _, e := p.Apply(); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	p, e = install.PreviewUninstall(home)
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	a := app{home: home, ctx: t.Context(), in: strings.NewReader("diff\nn\n"), out: &out, outputTTY: true, inputTTY: true, commandName: "uninstall"}
	if e = a.approve(p, false); e == nil {
		t.Fatal("diff accepted uninstall")
	}
	if !strings.Contains(out.String(), "-Installation alone does not authorize adoption.") {
		t.Fatal(out.String())
	}
	after, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("viewing diff removed instructions", e)
	}
}

func TestInstallCompletionOffersStateBoundRolloutCommand(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "state with ' quote")
	root := filepath.Join(base, "agent")
	t.Setenv("PATH", t.TempDir())
	var out, errout bytes.Buffer
	a := app{home: home, ctx: t.Context(), in: strings.NewReader("d\ny\n"), out: &out, err: &errout, outputTTY: true, inputTTY: true}
	if e := a.integration("install", []string{"--codex-home", root}); e != nil {
		t.Fatal(e, out.String(), errout.String())
	}
	command := installNextCommand(home, "rollout status")
	if !strings.Contains(out.String(), "Use `arm rollout`") || !strings.Contains(out.String(), "Next: review project enrollment with:\n  `"+command+"`") || !strings.Contains(command, "'\\''") {
		t.Fatal(out.String())
	}
	if _, e := os.Stat(filepath.Join(home, "rollout.json")); !os.IsNotExist(e) {
		t.Fatal("next command was executed automatically", e)
	}
	out.Reset()
	a.in = strings.NewReader("n\n")
	a.diffShown = false
	if e := a.integration("install", []string{"--codex-home", root}); e == nil || strings.Contains(out.String(), "Next: review project enrollment with:") {
		t.Fatal("declined install offered completed-install next step", e, out.String())
	}
}

func TestInstallNextCommandOmitsDefaultHomeAndRetainsCustomHome(t *testing.T) {
	defaultHome := t.TempDir()
	t.Setenv("ARM_HOME", defaultHome)
	for _, home := range []string{defaultHome, filepath.Join(defaultHome, ".")} {
		if got := installNextCommand(home, "rollout project --help"); got != "arm rollout project --help" {
			t.Fatal(got)
		}
	}
	alias := filepath.Join(t.TempDir(), "default-alias")
	if e := os.Symlink(defaultHome, alias); e != nil {
		t.Fatal(e)
	}
	if got := installNextCommand(alias, "rollout status"); got != "arm rollout status" {
		t.Fatal(got)
	}
	custom := filepath.Join(t.TempDir(), "custom state")
	if got := installNextCommand(custom, "install"); got != "arm --home '"+custom+"' install" {
		t.Fatal(got)
	}
	t.Setenv("ARM_HOME", "")
	config, e := os.UserConfigDir()
	if e != nil {
		t.Fatal(e)
	}
	if got := installNextCommand(filepath.Join(config, "arm"), "rollout status"); got != "arm rollout status" {
		t.Fatal(got)
	}
}
