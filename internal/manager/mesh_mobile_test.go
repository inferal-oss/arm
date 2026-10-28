package manager

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/nodeavailability"
)

func TestMobilePeerRestartRetainsOriginalIntentAndObligation(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		name := "completed-result"
		if uncertain {
			name = "uncertain-result"
		}
		t.Run(name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "state")
			capacity := model.Resources{CPU: 1}
			m, err := Open(home, capacity)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if m != nil {
					if err := m.Close(); err != nil {
						t.Error("original Manager cleanup failed", err)
					}
				}
			})
			cpuReading(m, 64, 64, 0, 64, 0)
			// Initialize the existing journal before installing the controlled
			// result-write fault. This is fixture DDL, not a product migration.
			if _, err = m.PeerOnce("mobile-peer", "setup", "cancel", json.RawMessage(`{}`), func() (any, error) { return true, nil }); err != nil {
				t.Fatal(err)
			}
			if uncertain {
				if _, err = m.db.Exec(`CREATE TRIGGER mobile_result_fault BEFORE UPDATE ON mesh_operations BEGIN SELECT RAISE(ABORT,'fixture result unavailable'); END`); err != nil {
					t.Fatal(err)
				}
			}
			q := model.Request{Host: "localhost", Key: "original-key", Reason: "mobile recovery fixture", Resources: model.Resources{CPU: .5}, TTL: time.Hour, PeerCertificate: "original-pin", PeerGrantRevision: 2}
			params, err := json.Marshal(q)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			var original model.Lease
			fn := func() (any, error) {
				calls++
				var err error
				original, err = m.AcquirePeer("mobile-peer", q, nil, capacity, time.Hour)
				return original, err
			}
			_, err = m.PeerOnceAuthenticated("mobile-peer", q.Key, "acquire", params, "original-pin", 2, fn)
			if uncertain && errorCode(err) != "remote_outcome_unknown" || !uncertain && err != nil {
				t.Fatal("wrong initial result classification", err)
			}
			if calls != 1 || original.ID == "" {
				t.Fatal("original admission did not execute exactly once", calls)
			}
			if _, err := m.SetNodeAvailability(nodeavailability.Update{Key: "persistent-profile", Profile: "intermittent", Reason: "owner fixture declaration"}); err != nil {
				t.Fatal(err)
			}
			originalManager := m
			m = nil
			if err = originalManager.Close(); err != nil {
				t.Fatal(err)
			}
			m, err = Open(home, capacity)
			if err != nil {
				t.Fatal(err)
			}
			profile, err := m.NodeAvailability()
			if err != nil || profile.OwnerDeclared.Profile != "intermittent" || profile.OwnerDeclared.Revision != 1 {
				t.Fatal("owner declaration did not persist across original Manager restart", profile, err)
			}
			_, err = m.PeerOnceAuthenticated("mobile-peer", q.Key, "acquire", params, "original-pin", 2, fn)
			if uncertain && errorCode(err) != "remote_outcome_unknown" || !uncertain && err != nil || calls != 1 {
				t.Fatal("restart replayed or discarded original outcome", calls, err)
			}
			retained, err := m.PeerLease("mobile-peer", original.ID)
			if err != nil || retained.ID != original.ID || retained.Request.OriginKey != q.Key || retained.Request.PeerCertificate != "original-pin" || retained.Request.PeerGrantRevision != 2 || retained.Request.Key != original.Request.Key || !consuming(retained.State) {
				t.Fatal("restart discarded original ownership/obligation", retained, err)
			}
			status, err := m.Status()
			if err != nil || status.Reserved.CPU < .5 {
				t.Fatal("retained obligation was credited free", status.Reserved, err)
			}
		})
	}
}
