package database_test

import (
	"database/sql"
	"fmt"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/workspace"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspacePhysicalKeysIndependentResetRefusesAliases(t *testing.T) {
	home, id := workspaceProofFixture(t, "active")
	m, e := manager.Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	records, e := m.WorkspaceRead(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	_, e = m.WorkspaceDeny(t.Context(), id, workspace.MutationRequest{Key: "physical-key-deny", Host: strings.Repeat("a", 32), Revision: records.Instances[0].Revision, Reason: "actual deny evidence"})
	if e != nil {
		t.Fatal(e)
	}
	records, e = m.WorkspaceRead(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	_, e = m.WorkspaceRetire(t.Context(), id, workspace.MutationRequest{Key: "physical-key-retire", Revision: records.Instances[0].Revision, Reason: "explicit local retirement"})
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	observedHome := publishedProofFixture(t, true)
	for _, tc := range []struct {
		home, table, column string
		limit               int
	}{
		{home, "workspace_instances", "id", 32},
		{home, "workspace_directory_members", "workspace", 32}, {home, "workspace_directory_members", "host", 32},
		{home, "workspace_trusted_members", "workspace", 32}, {home, "workspace_trusted_members", "host", 32},
		{observedHome, "workspace_observations", "workspace", 32},
		{home, "workspace_operations", "workspace", 32}, {home, "workspace_operations", "principal", 512}, {home, "workspace_operations", "key", 128}, {home, "workspace_operations", "request", 1 << 20},
		{home, "workspace_changes", "workspace", 32},
		{home, "workspace_denies", "workspace", 32}, {home, "workspace_denies", "host", 32},
		{home, "workspace_clocks", "workspace", 32},
	} {
		t.Run(tc.table+"/"+tc.column, func(t *testing.T) {
			db, e := sql.Open("sqlite", filepath.Join(tc.home, "state.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			var original, body string
			if e = db.QueryRow(fmt.Sprintf("SELECT %s,data FROM %s LIMIT 1", tc.column, tc.table)).Scan(&original, &body); e != nil {
				t.Fatal("safe actual record missing", e)
			}
			p, e := prove(t, tc.home)
			if e != nil || !p.Complete || !p.Settled {
				t.Fatal("safe reset control", p, e)
			}
			for _, kind := range []string{"null", "blob-same-bytes", "nul-suffix", "invalid-utf8", "empty", "overlong"} {
				t.Run(kind, func(t *testing.T) {
					var value any
					expression := "?"
					switch kind {
					case "null":
						value = nil
					case "blob-same-bytes":
						value = []byte(original)
					case "nul-suffix":
						value = original + "\x00hidden"
					case "invalid-utf8":
						value = append([]byte(original), 0xff)
						expression = "CAST(? AS TEXT)"
					case "empty":
						value = ""
					case "overlong":
						value = strings.Repeat("x", tc.limit+1)
					}
					result, e := db.Exec(fmt.Sprintf("UPDATE %s SET %s=%s WHERE data=?", tc.table, tc.column, expression), value, body)
					if e != nil {
						if kind != "null" || !strings.Contains(e.Error(), "NOT NULL constraint") {
							t.Fatal(e)
						}
						t.Log("SQLite NOT NULL constraint refused mutation")
						return
					}
					n, e := result.RowsAffected()
					if e != nil || n != 1 {
						t.Fatal(n, e)
					}
					defer func() {
						if _, e := db.Exec(fmt.Sprintf("UPDATE %s SET %s=? WHERE data=?", tc.table, tc.column), original, body); e != nil {
							t.Fatal(e)
						}
					}()
					p, e := prove(t, tc.home)
					if e == nil && p.Complete {
						t.Fatal("independent reset accepted malformed physical evidence", p)
					}
				})
			}
		})
	}
}

func TestWorkspacePhysicalJournalSequenceIndependentResetRefusal(t *testing.T) {
	home, _ := workspaceProofFixture(t, "retired")
	db, e := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var sequence int64
	var body string
	if e = db.QueryRow(`SELECT sequence,data FROM workspace_changes ORDER BY sequence LIMIT 1`).Scan(&sequence, &body); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []int64{0, -1} {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			result, e := db.Exec(`UPDATE workspace_changes SET sequence=? WHERE data=?`, bad, body)
			if e != nil {
				t.Fatal(e)
			}
			n, e := result.RowsAffected()
			if e != nil || n != 1 {
				t.Fatal(n, e)
			}
			defer func() {
				if _, e := db.Exec(`UPDATE workspace_changes SET sequence=? WHERE data=?`, sequence, body); e != nil {
					t.Fatal(e)
				}
			}()
			p, e := prove(t, home)
			if e == nil && p.Complete {
				t.Fatal("nonpositive journal key accepted by reset", p)
			}
		})
	}
}
