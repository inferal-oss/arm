package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

func resetCLIState(t *testing.T, active bool, configured bool) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "state")
	m, err := manager.Open(home, model.Resources{CPU: 2, Disk: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if active {
		if _, err = m.Acquire(model.Request{Resources: model.Resources{CPU: 1}, Reason: "outstanding fixture", TTL: time.Hour}, nil); err != nil {
			t.Fatal(err)
		}
	}
	status, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if configured {
		store, err := mesh.Init(home, status.Host, "private-node", "127.0.0.1:32123", "127.0.0.1:32123")
		if err != nil {
			t.Fatal(err)
		}
		store.Close()
	}
	return home
}

type resetCLITreeEntry struct {
	Mode fs.FileMode
	Data string
}

func resetCLITree(t *testing.T, home string) map[string]resetCLITreeEntry {
	t.Helper()
	tree := map[string]resetCLITreeEntry{}
	if err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entry := resetCLITreeEntry{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry.Data = string(data)
		}
		relative, _ := filepath.Rel(home, path)
		tree[relative] = entry
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return tree
}
func TestDatabaseRemoveDryRunPipeAndTerminalDeclineAreUnchanged(t *testing.T) {
	for _, kind := range []string{"dry-run", "pipe", "terminal-n", "terminal-enter", "terminal-eof"} {
		t.Run(kind, func(t *testing.T) {
			home := resetCLIState(t, false, false)
			before := resetCLITree(t, home)
			var out, errout bytes.Buffer
			args := []string{"remove", "--reason", "private fixture review"}
			a := app{home: home, ctx: context.Background(), in: strings.NewReader(""), out: &out, err: &errout}
			if kind == "dry-run" {
				args = append(args, "--dry-run")
			}
			if strings.HasPrefix(kind, "terminal") {
				a.outputTTY, a.inputTTY, a.colors = true, true, true
				answer := "n\n"
				if kind == "terminal-enter" {
					answer = "\n"
				}
				if kind == "terminal-eof" {
					answer = ""
				}
				a.in = strings.NewReader(answer)
			}
			code, err := a.database(args)
			if kind == "dry-run" {
				if code != 0 || err != nil {
					t.Fatal(code, err)
				}
			} else if err == nil || code == 0 {
				t.Fatal("unconfirmed reset accepted")
			}
			if after := resetCLITree(t, home); !reflect.DeepEqual(before, after) {
				t.Fatal("preview/decline changed source names, bytes or modes")
			}
			if strings.HasPrefix(kind, "terminal") {
				if !strings.Contains(out.String(), "[y/N]") || !strings.Contains(out.String(), "Exact SQLite erase scope") || !strings.Contains(out.String(), "\x1b[") {
					t.Fatal("missing concrete colorized default-no review", out.String())
				}
			} else {
				var preview databaseRemovePreview
				if err = json.Unmarshal(out.Bytes(), &preview); err != nil || preview.Plan.OldHost == "" || len(preview.Plan.Files) != 3 {
					t.Fatal("missing structured preview", out.String(), err)
				}
			}
		})
	}
}
func TestDatabaseRemoveExplicitPrivateFixtureAndCompletionLookup(t *testing.T) {
	home := resetCLIState(t, false, false)
	var out, errout bytes.Buffer
	code := Main([]string{"--home", home, "database", "remove", "--yes", "--reason", "erase settled private fixture"}, strings.NewReader(""), &out, &errout)
	if code != 0 {
		t.Fatal(code, errout.String(), out.String())
	}
	var outcome databaseRemoveOutcome
	if err := json.Unmarshal(out.Bytes(), &outcome); err != nil || !outcome.Result.Completed || outcome.Plan.OldHost == outcome.Plan.NewHost {
		t.Fatalf("%+v %v", outcome, err)
	}
	if _, err := os.Lstat(filepath.Join(home, "state.db")); !os.IsNotExist(err) {
		t.Fatal("old ledger remains", err)
	}
	before := resetCLITree(t, home)
	out.Reset()
	errout.Reset()
	code = Main([]string{"--home", home, "database", "remove", "--continue", outcome.Result.Operation, "--dry-run"}, strings.NewReader(""), &out, &errout)
	if code != 0 {
		t.Fatal(code, errout.String())
	}
	if after := resetCLITree(t, home); !reflect.DeepEqual(before, after) {
		t.Fatal("continuation preview replayed a mutation")
	}
}
func TestDatabaseRemoveRefusesUnsettledAndUnreviewedMesh(t *testing.T) {
	for _, kind := range []string{"active", "mesh"} {
		t.Run(kind, func(t *testing.T) {
			home := resetCLIState(t, kind == "active", kind == "mesh")
			before := resetCLITree(t, home)
			var out, errout bytes.Buffer
			code := Main([]string{"--home", home, "database", "remove", "--yes", "--reason", "private test"}, strings.NewReader(""), &out, &errout)
			if code == 0 {
				t.Fatal("unsettled/unreviewed scope erased")
			}
			if after := resetCLITree(t, home); !reflect.DeepEqual(before, after) {
				t.Fatal("refused scope changed")
			}
			var preview databaseRemovePreview
			if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
				t.Fatal("refusal omitted concrete preview", out.String(), err)
			}
		})
	}
}
func TestDatabaseRemoveRetainsPrivateMeshArchive(t *testing.T) {
	home := resetCLIState(t, false, true)
	keyBefore, err := os.ReadFile(filepath.Join(home, "mesh", "node-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	var out, errout bytes.Buffer
	if code := Main([]string{"--home", home, "database", "remove", "--retire-mesh", "--yes", "--reason", "retire private fixture identity"}, strings.NewReader(""), &out, &errout); code != 0 {
		t.Fatal(code, errout.String(), out.String())
	}
	var outcome databaseRemoveOutcome
	if err = json.Unmarshal(out.Bytes(), &outcome); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(home, database.ResetDirectoryName, "reset-"+outcome.Result.Operation, "mesh", "node-key.pem")
	keyAfter, err := os.ReadFile(archive)
	if err != nil || !bytes.Equal(keyBefore, keyAfter) {
		t.Fatal("reviewed key archive lost", err)
	}
	if _, err = os.Lstat(filepath.Join(home, "mesh")); !os.IsNotExist(err) {
		t.Fatal("old trust still active", err)
	}
	if strings.Contains(out.String(), string(keyBefore)) {
		t.Fatal("private key leaked into output")
	}
	info, err := os.Stat(archive)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private archive mode changed", err)
	}
}
func TestDatabaseRemoveInvalidFlagsNeverCreateState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"remove", "--force"}, {"remove", "--yes"}, {"remove", "extra", "--dry-run"}, {"remove", "--yes", "--dry-run"}, {"remove", "--continue", "bad", "--reason", "changed"}, {"remove", "--continue", "bad"}} {
		var out, errout bytes.Buffer
		if code := Main(append([]string{"--home", home, "database"}, args...), strings.NewReader(""), &out, &errout); code == 0 {
			t.Fatal("invalid request accepted", args)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("invalid reset created state")
		}
	}
}

func TestDatabaseRemoveSchemaRefusalIsReadableAndPreservesLedger(t *testing.T) {
	for _, kind := range []string{"missing-pressure-table", "pre-pressure-pair", "altered-table", "unknown-table", "missing-index"} {
		t.Run(kind, func(t *testing.T) {
			home := resetCLIState(t, false, false)
			db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			queries := map[string]string{
				"missing-pressure-table": `DROP TABLE host_pressure_state`,
				"pre-pressure-pair":      `DROP TABLE host_pressure_state; DROP TABLE host_pressure_outbox`,
				"altered-table":          `ALTER TABLE host_pressure_state ADD COLUMN future TEXT`,
				"unknown-table":          `CREATE TABLE future_obligations(data TEXT)`,
				"missing-index":          `DROP INDEX pools_name`,
			}
			_, err = db.Exec(queries[kind])
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			before := resetCLITree(t, home)
			for _, format := range []string{"pipe", "terminal", "forced-json"} {
				var out, errout bytes.Buffer
				a := app{home: home, ctx: t.Context(), in: strings.NewReader("y\n"), out: &out, err: &errout}
				a.outputTTY, a.inputTTY, a.errorTTY = format != "pipe", true, format != "pipe"
				a.forceJSON = format == "forced-json"
				code, err := a.database([]string{"remove", "--yes", "--reason", "wipe"})
				if code == 0 || err == nil {
					t.Fatal("incompatible schema removed")
				}
				a.reportError(err)
				if out.Len() != 0 {
					t.Fatal("refused schema produced an approvable preview")
				}
				text := errout.String()
				if strings.ContainsAny(text, "\x1f\x1b") || strings.Contains(text, `\u001f`) || strings.Contains(text, "create\\u") {
					t.Fatal("internal SQL formatting leaked", text)
				}
				if format == "terminal" {
					if !strings.HasPrefix(text, "Error: ") || !strings.Contains(text, "Code: database_schema_unsupported") {
						t.Fatal("missing readable terminal error", text)
					}
				} else {
					var envelope struct {
						Error model.Error `json:"error"`
					}
					if err := json.Unmarshal(errout.Bytes(), &envelope); err != nil || envelope.Error.Code != "database_schema_unsupported" {
						t.Fatal("missing structured schema refusal", text, err)
					}
				}
				if after := resetCLITree(t, home); !reflect.DeepEqual(before, after) {
					t.Fatal("refusal migrated, repaired or erased state")
				}
			}
		})
	}
}
