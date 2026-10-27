package manager

import (
	"encoding/json"
	"getarm.org/internal/published"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewPublishedEscapedWithdrawalMustFit(t *testing.T) {
	m, op := workspaceManagerFixture(t, filepath.Join(t.TempDir(), "arm"))
	defer m.Close()
	c := pollContact(t, m, "final-size")
	q := published.Request{Workspace: op.Workspace, ID: c.ID, Session: c.Session, Registration: c.Registration, Label: strings.Repeat("<", 128), Delivery: "poll", Key: "size", Reason: strings.Repeat("<", 512)}
	plan, err := m.AgentPublicationPlan(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := m.AgentPublicationCreate(t.Context(), q, plan.ReviewSHA)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(pub)
	t.Logf("admitted publication bytes=%d", len(b))
	w := published.Withdrawal{Key: strings.Repeat("<", 128), Reason: strings.Repeat("<", 512), Revision: 1}
	_, err = m.AgentPublicationWithdraw(t.Context(), op.Workspace, pub.Endpoint, w)
	if err != nil {
		t.Fatalf("admitted publication could not finalize with valid bounded withdrawal: %v", err)
	}
}
