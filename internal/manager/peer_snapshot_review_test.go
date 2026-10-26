package manager

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestReviewPeerSnapshotDoesNotWaitForManagerStatusLock(t *testing.T) {
	m := newManager(t)
	m.mu.Lock()
	done := make(chan error, 1)
	go func() { _, err := m.PeerSnapshot(); done <- err }()
	blocked := false
	select {
	case err := <-done:
		if err != nil {
			m.mu.Unlock()
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		blocked = true
	}
	m.mu.Unlock()
	if blocked {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("snapshot failed to join after own lock released")
		}
		t.Fatal("peer summary waited behind full Manager.Status work")
	}
}

func TestPeerProjectionHonorsDatabaseWaitCancellation(t *testing.T) {
	m := newManager(t)
	tx, err := m.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err = m.PeerSnapshotContext(ctx); err == nil {
		t.Fatal("cancelled database wait succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("projection ignored context deadline")
	}
}
func TestPeerProjectionRefusesOversizedRecordWithoutPartialPicture(t *testing.T) {
	m := newManager(t)
	l, err := m.Acquire(request("private"), nil)
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err = json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}
	data["private_padding"] = strings.Repeat("p", 65536)
	changed, _ := json.Marshal(data)
	if _, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(changed), l.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.PeerSnapshot()
	if err == nil || snapshot.Version != 0 {
		t.Fatal("oversized inventory produced a partial authoritative-looking picture")
	}
}

func TestPeerProjectionRefusesUnclassifiableLeaseState(t *testing.T) {
	for _, state := range []string{"missing", "null"} {
		t.Run(state, func(t *testing.T) {
			m := newManager(t)
			row := map[string]any{"id": "unclassifiable-peer-fixture", "request": map[string]any{"resources": map[string]any{"cpu": 1}}}
			if state == "null" {
				row["state"] = nil
			}
			raw, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.db.Exec(`INSERT INTO leases(id,data) VALUES(?,?)`, "unclassifiable-peer-fixture", string(raw)); err != nil {
				t.Fatal(err)
			}
			snapshot, err := m.PeerSnapshot()
			if err == nil {
				t.Fatalf("unclassifiable state silently reported held=%+v queued=%+v", snapshot.Held, snapshot.Queued)
			}
		})
	}
}
