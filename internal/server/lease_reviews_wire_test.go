package server_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/client"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/manager"
	"github.com/inferal-oss/arm/internal/model"
)

// Construct maximal typed historical records in the private fixture, then use
// the actual websocket client (its ordinary 4MiB limit remains unchanged).
func TestLeaseReviewWirePagesWholeWorstShapeRecords(t *testing.T) {
	home := backend(t)
	ctx := context.Background()
	contact := agents.Contact{ID: strings.Repeat("a", 128), Session: "11111111-1111-4111-8111-111111111111", Adapter: "generic", CWD: home, Delivery: "poll"}
	if err := client.Call(ctx, home, "register", contact, &contact); err != nil {
		t.Fatal(err)
	}
	var status model.Status
	if err := client.Call(ctx, home, "status", nil, &status); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	contexts := []model.LeaseReviewContext{}
	for i := 0; i < 16; i++ {
		var l model.Lease
		if err := client.Call(ctx, home, "acquire", model.Request{Resources: model.Resources{CPU: .001}, Agent: contact.ID, Reason: strings.Repeat("r", 4096), TTL: time.Hour, Key: fmt.Sprint("wire-", i)}, &l); err != nil {
			t.Fatal(err)
		}
		c := model.LeaseReviewContext{Lease: l.ID, Revision: l.Revision, PolicyGeneration: 1, Owner: l.Owner, Purpose: l.Request.Reason, Resources: l.Request.Resources, Expires: *l.Expires, Remaining: l.Expires.Sub(now), Usage: model.LeaseReviewUsage{Observed: &now, CPUQuality: "unavailable", MemoryQuality: "unavailable", Stale: true, Detail: strings.Repeat("d", 512)}}
		trial := append(append([]model.LeaseReviewContext{}, contexts...), c)
		raw, _ := json.Marshal(trial)
		if len(raw) <= 64<<10 {
			contexts = trial
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	entire := model.LeaseReviewPage{Items: []model.LeaseReview{}, MaxPending: 64, RetainedClosed: 1024}
	for i := 0; i < 64; i++ {
		id, msgID := local.ID(), local.ID()
		closed := now.Add(time.Nanosecond)
		reply := &model.LeaseReviewReply{ID: id, Agent: contact.ID, Session: contact.Session, Registration: contact.Registration, Intent: "needed", Explanation: strings.Repeat("<", 4096)}
		x := model.LeaseReview{ID: id, Host: status.Host, Agent: contact.ID, Session: contact.Session, Registration: contact.Registration, Message: msgID, Created: now, Deadline: now.Add(time.Minute), Closed: &closed, State: "replied", Context: contexts, Reply: reply}
		raw, _ := json.Marshal(x)
		res, err := tx.Exec(`INSERT INTO lease_reviews(id,agent,registration,state,data) VALUES(?,?,?,'replied',?)`, id, contact.ID, contact.Registration, string(raw))
		if err != nil {
			t.Fatal(err)
		}
		x.Sequence, err = res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		entire.Items = append(entire.Items, x)
		msg := manager.Message{ID: msgID, Owner: status.Owner, Sender: "arm-lease-review", Audience: contact.ID, Body: "Exact typed historical paging fixture", Created: now, Deliveries: map[string]agents.Delivery{contact.ID: {Mode: "poll", State: "available", Registration: contact.Registration}}}
		raw, _ = json.Marshal(msg)
		if _, err = tx.Exec(`INSERT INTO messages(id,data) VALUES(?,?)`, msgID, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(entire)
	if len(raw) <= 4<<20 {
		t.Fatalf("fixture did not reproduce old wire overflow: %d bytes / %d contexts", len(raw), len(contexts))
	}
	t.Logf("Unpaged valid typed review payload: %d bytes; %d full lease contexts per record", len(raw), len(contexts))
	seen := map[string]bool{}
	after := int64(0)
	pages := 0
	maxPageBytes := 0
	for {
		var page model.LeaseReviewPage
		if err = client.Call(ctx, home, "lease_review_list", model.LeaseReviewQuery{After: after}, &page); err != nil {
			t.Fatal("actual websocket limit rejected bounded page", err)
		}
		encoded, _ := json.Marshal(page)
		if len(encoded) > maxPageBytes {
			maxPageBytes = len(encoded)
		}
		if len(encoded) > 1<<20 || len(page.Items) == 0 {
			t.Fatal("not a bounded whole-record page", len(encoded), len(page.Items))
		}
		for _, x := range page.Items {
			if seen[x.ID] || len(x.Context) != len(contexts) || x.Reply == nil || len(x.Reply.Explanation) != 4096 {
				t.Fatal("record split/lost/repeated", x.ID)
			}
			seen[x.ID] = true
		}
		if page.Cursor <= after {
			t.Fatal("cursor did not advance")
		}
		after = page.Cursor
		pages++
		if !page.More {
			break
		}
		if pages > 64 {
			t.Fatal("pagination did not terminate")
		}
	}
	t.Logf("Actual websocket returned all64 complete records over%d pages; largest payload%d bytes", pages, maxPageBytes)
	if len(seen) != 64 || pages < 2 {
		t.Fatal("whole records missing", len(seen), pages)
	}
}

func TestLeaseReviewWireBoundsCorruptSchedulerDiagnostic(t *testing.T) {
	home := backend(t)
	ctx := context.Background()
	contact := agents.Contact{ID: "corrupt-time-review", Session: "11111111-1111-4111-8111-111111111111", Adapter: "generic", CWD: home, Delivery: "poll"}
	if err := client.Call(ctx, home, "register", contact, &contact); err != nil {
		t.Fatal(err)
	}
	var l model.Lease
	if err := client.Call(ctx, home, "acquire", model.Request{Agent: contact.ID, Reason: "private oversized scheduler diagnostic regression", TTL: time.Hour, Resources: model.Resources{CPU: .01}}, &l); err != nil {
		t.Fatal(err)
	}
	g := int64(0)
	var policy model.LeaseReviewPolicy
	if err := client.Call(ctx, home, "lease_review_configure", model.LeaseReviewConfigure{Lease: l.ID, Revision: l.Revision, Generation: &g, Enabled: true, Interval: 15 * time.Second}, &policy); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		t.Fatal(err)
	}
	corrupt := strings.Repeat("<", 400000)
	if _, err = db.Exec(`UPDATE lease_review_policies SET data=json_set(data,'$.updated_at',?,'$.next_at',?) WHERE lease=?`, corrupt, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), l.ID); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err = db.QueryRow(`SELECT data FROM lease_review_policies WHERE lease=?`, l.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var invalid model.LeaseReviewPolicy
	parseErr := json.Unmarshal(raw, &invalid)
	if parseErr == nil {
		t.Fatal("invalid stored timestamp accepted")
	}
	oldPayload, _ := json.Marshal(model.LeaseReviewPage{Items: []model.LeaseReview{}, SchedulerError: parseErr.Error(), MaxPending: 64, RetainedClosed: 1024})
	if len(oldPayload) <= 4<<20 {
		t.Fatal("fixture did not reproduce unbounded old metadata", len(oldPayload))
	}
	t.Logf("Prior raw scheduler diagnostic would encode %d bytes", len(oldPayload))
	deadline := time.Now().Add(5 * time.Second)
	for {
		var page model.LeaseReviewPage
		if err = client.Call(ctx, home, "lease_review_list", model.LeaseReviewQuery{}, &page); err != nil {
			t.Fatal("actual normal client rejected scheduler page", err)
		}
		if page.SchedulerError != "" {
			encoded, _ := json.Marshal(page)
			if len(encoded) > 1<<20 || len(page.SchedulerError) > 256 || strings.Contains(page.SchedulerError, "<") {
				t.Fatal("diagnostic leaked raw persisted content or escaped bound", len(encoded), len(page.SchedulerError))
			}
			t.Logf("Actual unchanged websocket client received bounded diagnostic page: %d bytes", len(encoded))
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scheduler did not observe invalid due policy")
		}
		time.Sleep(25 * time.Millisecond)
	}
	var after model.Lease
	if err = client.Call(ctx, home, "inspect", map[string]string{"id": l.ID}, &after); err != nil || after.State != "active" || after.Revision != l.Revision || !after.Expires.Equal(*l.Expires) {
		t.Fatal("optional scheduler diagnostic changed entitlement", after, err)
	}
}
