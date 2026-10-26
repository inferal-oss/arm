package manager

import (
	"encoding/json"
	"testing"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/syncreconcile"
)

func TestReviewSyncKeyRefusesChangedChargedEvidence(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	lease, err := m.ReserveSyncSource(t.Context(), i, rows, root, parent, control)
	if err != nil {
		t.Fatal(err)
	}
	var original string
	if err := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, lease.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*model.Lease)
	}{
		{"cpu", func(l *model.Lease) { l.Request.Resources.CPU /= 2 }},
		{"memory", func(l *model.Lease) { l.Request.Resources.Memory /= 2 }},
		{"owner", func(l *model.Lease) { l.Owner += "-other" }},
		{"terminal", func(l *model.Lease) { l.State = "terminal" }},
		{"queued", func(l *model.Lease) { l.State = "queued" }},
		{"request-key", func(l *model.Lease) { l.Request.Key = "replacement-request-key" }},
		{"reason", func(l *model.Lease) { l.Request.Reason = "different reviewed reason" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := lease
			tc.change(&changed)
			raw, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, raw, lease.ID); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, original, lease.ID); err != nil {
					t.Error(err)
				}
			}()
			if _, err := m.InspectSyncKey(t.Context(), i.Key, syncreconcile.Local); err == nil {
				t.Fatal("changed charged evidence reported as original sync reservation")
			}
		})
	}
}
