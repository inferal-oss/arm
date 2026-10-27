package manager

import (
	"database/sql"
	"github.com/inferal-oss/arm/internal/model"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceMalformedPhysicalKeyPreservesOrdinaryStartupAndCleanup(t *testing.T) {
	for _, kind := range []string{"nul-suffix", "blob-same-bytes", "null"} {
		t.Run(kind, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "arm")
			m, op := workspaceManagerFixture(t, home)
			if e := m.Close(); e != nil {
				t.Fatal(e)
			}
			db, e := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if e != nil {
				t.Fatal(e)
			}
			var value any
			switch kind {
			case "nul-suffix":
				value = op.Workspace + "\x00hidden"
			case "blob-same-bytes":
				value = []byte(op.Workspace)
			case "null":
				value = nil
			}
			result, e := db.Exec(`UPDATE workspace_instances SET id=? WHERE id=?`, value, op.Workspace)
			if e != nil {
				db.Close()
				t.Fatal(e)
			}
			n, e := result.RowsAffected()
			db.Close()
			if e != nil || n != 1 {
				t.Fatal(n, e)
			}
			m, e = Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
			if e != nil {
				t.Fatal("malformed optional workspace prevented ordinary startup", e)
			}
			defer m.Close()
			if _, e = m.WorkspaceRead(t.Context()); errorCode(e) != "workspace_state_invalid" {
				t.Fatal("malformed workspace not isolated", e)
			}
			status, e := m.Status()
			if e != nil || status.Workspaces == nil || status.Workspaces.Ledger != "invalid" {
				t.Fatal(status.Workspaces, e)
			}
			lease, e := m.Acquire(model.Request{Resources: model.Resources{CPU: 1}, TTL: time.Minute, Reason: "ordinary resource cleanup after optional workspace refusal", Key: "ordinary-cpu"}, nil)
			if e != nil {
				t.Fatal("ordinary admission blocked by optional invalid workspace", e)
			}
			if _, e = m.End(lease.ID, "released"); e != nil {
				t.Fatal("ordinary cleanup blocked", e)
			}
		})
	}
}
