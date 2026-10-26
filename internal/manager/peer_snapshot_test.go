package manager

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPeerSnapshotHeldQueuedTimingAndPrivacy(t *testing.T) {
	m := newManager(t)
	holder, err := m.Acquire(request("private holder reason"), nil)
	if err != nil {
		t.Fatal(err)
	}
	queued := request("private queued reason")
	queued.Queue = true
	queued.StartDeadline = deadlineAfter(time.Minute)
	q, err := m.Acquire(queued, nil)
	if err != nil || q.State != "queued" {
		t.Fatalf("queue: %+v %v", q, err)
	}
	s, err := m.PeerSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if s.HeldCount != 1 || s.QueuedCount != 1 || s.Held.CPU != holder.Request.Resources.CPU || s.Queued.CPU != q.Request.Resources.CPU || len(s.Items) != 2 {
		t.Fatalf("wrong summary: %+v", s)
	}
	if s.Items[0].Expires == nil || !s.Items[0].Expires.Equal(*holder.Expires) || s.Items[1].QueuePosition != 1 || s.Items[1].StartDeadline == nil {
		t.Fatalf("timing lost: %+v", s.Items)
	}
	raw, _ := json.Marshal(s)
	for _, private := range []string{holder.ID, q.ID, "private holder reason", "private queued reason", holder.Request.Key} {
		if private != "" && strings.Contains(string(raw), private) {
			t.Fatalf("private metadata shared: %q", private)
		}
	}
	// A numeric UID can occur in public quantities or timestamps (notably root's
	// "0"). Check private field names recursively instead of searching for that
	// ambiguous scalar in the serialized report.
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	var checkFields func(any)
	checkFields = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				switch key {
				case "id", "lease_id", "owner", "owner_uid", "uid", "key", "request_key", "reason", "project", "session", "agent", "contact", "recipient", "path", "command":
					t.Fatalf("private field shared: %s", key)
				}
				checkFields(child)
			}
		case []any:
			for _, child := range value {
				checkFields(child)
			}
		}
	}
	checkFields(decoded)
}
