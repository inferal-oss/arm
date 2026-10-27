package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/manager"
	"github.com/inferal-oss/arm/internal/model"
)

func TestLeaseReviewAPIExactRevisionAndClosedFields(t *testing.T) {
	m, err := manager.Open(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	c, err := m.RegisterContact(agents.Contact{ID: "review-api", Session: uuid.NewString(), Adapter: "generic", CWD: t.TempDir(), Delivery: "poll"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.Acquire(model.Request{Reason: "API review fixture", Agent: c.ID, TTL: time.Hour, Resources: model.Resources{CPU: .1}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Manager: m}
	call := func(op string, q any) (any, error) {
		raw, _ := json.Marshal(q)
		return s.dispatch(context.Background(), model.WireRequest{Operation: op, Params: raw})
	}
	generation := int64(0)
	q := model.LeaseReviewConfigure{Generation: &generation, Lease: l.ID, Revision: l.Revision, Enabled: true, Interval: time.Minute}
	if _, err = call("lease_review_configure", q); err != nil {
		t.Fatal(err)
	}
	if _, err = call("lease_review_policy", map[string]string{"lease": l.ID}); err != nil {
		t.Fatal(err)
	}
	q.Revision++
	if _, err = call("lease_review_configure", q); err == nil {
		t.Fatal("stale revision accepted")
	}
	if _, err = call("lease_review_list", map[string]any{"pending": true, "unknown": true}); err == nil {
		t.Fatal("unknown field accepted")
	}
	if !browserAllowedOperation("lease_review_list") || !browserAllowedOperation("lease_review_configure") || browserAllowedOperation("lease_review_reply") {
		t.Fatal("operator dashboard acquired agent reply authority")
	}
	after, err := m.Inspect(l.ID)
	if err != nil || after.Revision != l.Revision || !after.Expires.Equal(*l.Expires) {
		t.Fatal("configure altered entitlement")
	}
}
