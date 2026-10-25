package cli

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
	"github.com/yrashk/arm/internal/storage"
)

func restartCLIHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "state")
	m, err := manager.Open(home, model.Resources{CPU: 2})
	if err != nil {
		t.Fatal(err)
	}
	status, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	id, err := storage.DirectoryIdentity(int(f.Fd()))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	b, err := nodestop.NewBoundary(nodestop.Target{Host: status.Host, Owner: strconv.Itoa(os.Geteuid()), Home: home, Directory: id, Generation: strings.Repeat("3", 32), Certificate: strings.Repeat("a", 64), SPKI: strings.Repeat("b", 64), Startup: nodestop.StartupContract})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.ConfigureNodeStop(b); err != nil {
		t.Fatal(err)
	}
	actor := nodestop.Actor{Host: strings.Repeat("2", 32), Certificate: strings.Repeat("c", 64), SPKI: strings.Repeat("d", 64), Pair: strings.Repeat("4", 64), GrantRevision: 1}
	impact := nodestop.Impact{Complete: true, SHA256: strings.Repeat("5", 64), Obligations: []nodestop.Obligation{}, Blockers: []nodestop.Blocker{}, Peers: []string{}}
	p, err := m.PlanNodeStop("cli-stop", actor, model.ShutdownRequest{}, "private restart CLI test", impact)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.BeginNodeStop(nodestop.Apply{Key: p.Key, Plan: p, ReviewSHA256: digest}, actor, true, impact); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	return home
}
func TestStartCommandPreviewAndMissingConfirmationPreserveStoppedIntent(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(strconv.FormatBool(dry), func(t *testing.T) {
			home := restartCLIHome(t)
			var out, stderr bytes.Buffer
			args := []string{"--home", home, "start", "--reason", "review only", "--json"}
			if dry {
				args = append(args, "--dry-run")
			}
			code := Main(args, strings.NewReader(""), &out, &stderr)
			if dry && code != 0 || !dry && code == 0 {
				t.Fatal(code, out.String(), stderr.String())
			}
			if !strings.Contains(out.String(), `"operation": "restart-arm-authority"`) {
				t.Fatal(out.String())
			}
			db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			state, present, err := nodestop.ReadState(db)
			if err != nil || !present || state.Running || state.Revision != 2 {
				t.Fatal(state, present, err)
			}
			if _, err = os.Stat(filepath.Join(home, "endpoint.json")); !os.IsNotExist(err) {
				t.Fatal("preview launched backend", err)
			}
		})
	}
}
func TestStartDryRunCreatesNoHomeAndHelpExplainsRestartReview(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	var out, stderr bytes.Buffer
	if code := Main([]string{"--home", home, "start", "--dry-run", "--json"}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("dry-run created state", err)
	}
	out.Reset()
	stderr.Reset()
	if code := Main([]string{"start", "--help"}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	for _, flag := range []string{"--reason", "--yes", "--dry-run"} {
		if !strings.Contains(out.String(), flag) {
			t.Fatal("missing restart flag", flag, out.String())
		}
	}
}
