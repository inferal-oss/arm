package manager

import (
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func TestLeaseActionReviewsExactAttachedConsumers(t *testing.T) {
	for _, kind := range []string{"storage", "ports"} {
		t.Run(kind, func(t *testing.T) {
			m := newManager(t)
			parentRequest := model.Request{Reason: "reviewed parent", Kind: kind, TTL: 5 * time.Minute}
			if kind == "storage" {
				parentRequest.Resources.Disk = 512
			} else {
				parentRequest.Ports = []model.PortClaim{{Network: "tcp4", Address: "127.0.0.1", Port: 0, Mode: "hold"}}
			}
			parent, err := m.Acquire(parentRequest, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Use a genuinely supervised queued consumer: a run=NULL storage
			// dependency is deliberately an unresolved share/orphan hold.
			if _, err = m.Acquire(request("occupy-consumer-capacity"), nil); err != nil {
				t.Fatal(err)
			}
			q := request("consumer-review")
			q.Queue = true
			if kind == "storage" {
				q.StorageLease = parent.ID
			} else {
				q.PortLease = parent.ID
			}
			consumer, err := m.Acquire(q, &RunRequest{Request: q, CWD: t.TempDir(), Command: []string{"/bin/true"}})
			if err != nil || consumer.State != "queued" {
				t.Fatal(consumer, err)
			}
			for _, reviewed := range [][]model.LeaseRevision{{}, {{ID: consumer.ID, Revision: consumer.Revision + 1}}, {{ID: "unrelated", Revision: 1}}} {
				_, err = m.LeaseAction(model.LeaseAction{ID: parent.ID, Revision: parent.Revision, Action: "release", Consumers: reviewed})
				if errorCode(err) != "revision_conflict" {
					t.Fatalf("changed consumer review accepted: %v", err)
				}
				current, _ := m.Inspect(parent.ID)
				if current.Revision != parent.Revision || current.State != "active" {
					t.Fatalf("failed review mutated parent: %+v", current)
				}
			}
			for _, reviewed := range [][]model.LeaseRevision{{{ID: consumer.ID, Revision: 0}}, {{ID: consumer.ID, Revision: consumer.Revision}, {ID: consumer.ID, Revision: consumer.Revision}}} {
				_, err = m.LeaseAction(model.LeaseAction{ID: parent.ID, Revision: parent.Revision, Action: "cancel", Consumers: reviewed})
				if errorCode(err) != "invalid_request" {
					t.Fatal("invalid consumer review accepted", err)
				}
			}
			_, err = m.LeaseAction(model.LeaseAction{ID: parent.ID, Revision: parent.Revision, Action: "cancel", Consumers: []model.LeaseRevision{{ID: consumer.ID, Revision: consumer.Revision}}})
			if err != nil {
				t.Fatal("current consumer review refused", err)
			}
			child, _ := m.Inspect(consumer.ID)
			if child.State != "terminal" || child.Outcome != "revoked" {
				t.Fatalf("parent did not end reviewed consumer: %+v", child)
			}
		})
	}
}
