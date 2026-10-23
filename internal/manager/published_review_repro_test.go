package manager

import (
	"path/filepath"
	"testing"
)

func TestReviewPublishedRawNULKeyMustRefuse(t *testing.T) {
	for _, col := range []string{"workspace", "endpoint", "generation", "key"} {
		t.Run(col, func(t *testing.T) {
			m, op := workspaceManagerFixture(t, filepath.Join(t.TempDir(), "arm"))
			defer m.Close()
			_, _ = publishFixture(t, m, op.Workspace, "nul-key")
			if _, err := m.db.Exec("UPDATE published_agents SET " + col + "=" + col + "||char(0)||'hidden-suffix'"); err != nil {
				t.Fatal(err)
			}
			v, err := m.AgentPublicationRead(t.Context())
			if err == nil {
				t.Fatalf("accepted malformed raw SQL %s key with embedded NUL; publications=%d", col, len(v.Publications))
			}
		})
	}
}
