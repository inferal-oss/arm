package manager

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"getarm.org/internal/customnames"
	"getarm.org/internal/model"
)

func nameTestLease(t *testing.T, m *Manager, key string) model.Lease {
	t.Helper()
	lease, err := m.Acquire(model.Request{Resources: model.Resources{CPU: .1}, TTL: time.Hour, Reason: "custom naming transaction fixture", Key: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestCustomNameManagerCASAuditAndLeaseIdentity(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	lease := nameTestLease(t, m, "name-cas")
	var before string
	if err := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, lease.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	doc, err := m.CustomNames(ctx)
	if err != nil || doc.Revision != 0 || doc.Entries == nil {
		t.Fatal(doc, err)
	}
	var markers int
	if err = m.db.QueryRow(`SELECT count(*) FROM metadata WHERE key='custom_names_v1'`).Scan(&markers); err != nil || markers != 0 {
		t.Fatal("read created optional metadata", err, markers)
	}
	target := customnames.Entry{Kind: "lease", AuthorityHost: lease.Host, ID: lease.ID, Name: "build-cache"}
	doc, err = m.MutateCustomName(ctx, "set", target, 0, NameObservation{})
	if err != nil || doc.Revision != 1 || customnames.ActiveName(doc, target) != target.Name {
		t.Fatal(doc, err)
	}
	if _, err = m.MutateCustomName(ctx, "set", target, 1, NameObservation{}); err != nil {
		t.Fatal(err)
	}
	var events int
	if err = m.db.QueryRow(`SELECT count(*) FROM events WHERE type='custom_name_set'`).Scan(&events); err != nil || events != 1 {
		t.Fatal("no-op wrote event", err, events)
	}
	var winners atomic.Int32
	var failures atomic.Int32
	var wg sync.WaitGroup
	for _, name := range []string{"cache-a", "cache-b", "cache-c", "cache-d"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			candidate := target
			candidate.Name = name
			_, err := m.MutateCustomName(ctx, "set", candidate, 1, NameObservation{})
			if err == nil {
				winners.Add(1)
				return
			}
			var api *model.Error
			if !errors.As(err, &api) || api.Code != "name_revision_conflict" {
				failures.Add(1)
			}
		}(name)
	}
	wg.Wait()
	if winners.Load() != 1 || failures.Load() != 0 {
		t.Fatal("CAS did not select one writer", winners.Load(), failures.Load())
	}
	var after string
	if err = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, lease.ID).Scan(&after); err != nil || before != after {
		t.Fatal("cosmetic mutation changed lease entitlement/receipt", err)
	}
	doc, err = m.CustomNames(ctx)
	if err != nil || doc.Revision != 2 || len(doc.Entries) != 2 {
		t.Fatal("rename lost retained reservation", doc, err)
	}
	doc, err = m.MutateCustomName(ctx, "clear", target, 2, NameObservation{})
	if err != nil || doc.Revision != 3 || len(doc.Entries) != 1 {
		t.Fatal("exact clear failed", doc, err)
	}
	other := nameTestLease(t, m, "name-reuse")
	target.ID = other.ID
	if _, err = m.MutateCustomName(ctx, "set", target, 3, NameObservation{}); err != nil {
		t.Fatal("explicit clear did not release deliberate reuse", err)
	}
	t.Run("canonical-local-identities", func(t *testing.T) {
		for _, mode := range []string{"ghost-id", "key-payload", "wrong-host", "wrong-owner"} {
			t.Run(mode, func(t *testing.T) {
				m := newManager(t)
				lease := nameTestLease(t, m, "canonical-local-name")
				var original string
				if err := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, lease.ID).Scan(&original); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, original, lease.ID); err != nil {
						t.Error(err)
					}
				}()
				other := strings.Repeat("f", 32)
				if other == lease.ID || other == m.host {
					other = strings.Repeat("e", 32)
				}
				changed := lease
				id := lease.ID
				switch mode {
				case "ghost-id", "key-payload":
					changed.ID = other
					if mode == "ghost-id" {
						id = other
					}
				case "wrong-host":
					changed.Host = other
				case "wrong-owner":
					changed.Owner = "0"
					if m.owner == "0" {
						changed.Owner = "1"
					}
				}
				raw, _ := json.Marshal(changed)
				if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), lease.ID); err != nil {
					t.Fatal(err)
				}
				entry := customnames.Entry{Kind: "lease", AuthorityHost: m.host, ID: id, Name: "canonical-only"}
				if _, err := m.MutateCustomName(t.Context(), "set", entry, 0, NameObservation{}); err == nil {
					t.Fatal("list payload established a nonexistent or foreign exact local reference")
				}
				doc, err := m.CustomNames(t.Context())
				if err != nil || doc.Revision != 0 || len(doc.Entries) != 0 {
					t.Fatal("invalid canonical target changed optional metadata", doc, err)
				}
				var preserved string
				if err := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, lease.ID).Scan(&preserved); err != nil || preserved != string(raw) {
					t.Fatal("naming repaired or rewrote the malformed lease", err)
				}
			})
		}
		for _, explicit := range []bool{false, true} {
			name := "port-derived"
			if explicit {
				name = "port-asserted"
			}
			t.Run(name, func(t *testing.T) {
				m := newManager(t)
				owner := nameTestLease(t, m, "canonical-portless-owner")
				ghost := owner
				ghost.Ports = []model.PortBinding{{PortClaim: model.PortClaim{Network: "tcp4", Address: "127.0.0.1", Port: 12345, Namespace: "local", Mode: "hold"}, State: "held"}}
				raw, _ := json.Marshal(ghost)
				key := strings.Repeat("f", 32)
				if key == owner.ID {
					key = strings.Repeat("e", 32)
				}
				if _, err := m.db.Exec(`INSERT INTO leases(id,data) VALUES(?,?)`, key, string(raw)); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := m.db.Exec(`DELETE FROM leases WHERE id=?`, key); err != nil {
						t.Error(err)
					}
				}()
				entry := customnames.Entry{Kind: "resource", AuthorityHost: m.host, ID: m.host + "/ports/local/tcp4/127.0.0.1:12345", Name: "canonical-listener"}
				if explicit {
					entry.BindingLease = owner.ID
				}
				if _, err := m.MutateCustomName(t.Context(), "set", entry, 0, NameObservation{}); err == nil {
					t.Fatal("ghost port payload borrowed a real portless lease identity")
				}
				doc, err := m.CustomNames(t.Context())
				if err != nil || doc.Revision != 0 || len(doc.Entries) != 0 {
					t.Fatal("invented reservation changed optional metadata", doc, err)
				}
				var preserved string
				if err := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, key).Scan(&preserved); err != nil || preserved != string(raw) {
					t.Fatal("naming repaired or rewrote the ghost port lease", err)
				}
			})
		}
	})
}

func TestCustomNameManagerAuditFailureRollsBackWholeMap(t *testing.T) {
	m := newManager(t)
	lease := nameTestLease(t, m, "name-audit")
	_, err := m.db.Exec(`CREATE TRIGGER reject_custom_name BEFORE INSERT ON events WHEN NEW.type LIKE 'custom_name_%' BEGIN SELECT RAISE(ABORT,'test custom name audit failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	entry := customnames.Entry{Kind: "lease", AuthorityHost: lease.Host, ID: lease.ID, Name: "atomic-label"}
	if _, err = m.MutateCustomName(t.Context(), "set", entry, 0, NameObservation{}); err == nil {
		t.Fatal("audit failure accepted")
	}
	doc, err := m.CustomNames(t.Context())
	if err != nil || doc.Revision != 0 || len(doc.Entries) != 0 {
		t.Fatal("partial metadata commit survived failed audit", doc, err)
	}
}

func TestCustomNameManagerCorruptionNeverBlocksExactIDRelease(t *testing.T) {
	for _, corruption := range []string{"invalid-json", "blob-key", "duplicate-physical-key"} {
		t.Run(corruption, func(t *testing.T) {
			m := newManager(t)
			lease := nameTestLease(t, m, "name-corrupt")
			doc, err := m.CustomNames(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			doc.Revision = 1
			raw, _ := json.Marshal(doc)
			switch corruption {
			case "invalid-json":
				_, err = m.db.Exec(`INSERT INTO metadata VALUES('custom_names_v1','{')`)
			case "blob-key":
				_, err = m.db.Exec(`INSERT INTO metadata VALUES(CAST('custom_names_v1' AS BLOB),?)`, string(raw))
			default:
				_, err = m.db.Exec(`INSERT INTO metadata VALUES('custom_names_v1',?)`, string(raw))
				if err == nil {
					_, err = m.db.Exec(`INSERT INTO metadata VALUES(CAST('custom_names_v1' AS BLOB),?)`, string(raw))
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.CustomNames(t.Context()); err == nil {
				t.Fatal("corrupt optional map accepted")
			}
			entry := customnames.Entry{Kind: "lease", AuthorityHost: lease.Host, ID: lease.ID, Name: "corrupt-entry"}
			if _, err = m.MutateCustomName(t.Context(), "set", entry, 0, NameObservation{}); err == nil {
				t.Fatal("corrupt map was silently repaired")
			}
			if result, err := m.End(lease.ID, "released"); err != nil || result.ID != lease.ID || result.State != "terminal" {
				t.Fatal("cosmetic corruption blocked cleanup", result, err)
			}
		})
	}
}

func TestCustomNamePortReferenceUsesOnlyUniqueReservationOwner(t *testing.T) {
	host := strings.Repeat("a", 32)
	owner, attached := strings.Repeat("b", 32), strings.Repeat("c", 32)
	resource := model.ResourceView{ID: host + "/ports/local/tcp4/127.0.0.1:12345", Host: host, Kind: "port", Users: []model.ResourceUse{{Lease: attached, Relationship: "activation"}, {Lease: owner, Relationship: "reservation"}}}
	ref, err := CustomNameResourceReference(host, resource)
	if err != nil || ref.BindingLease != owner {
		t.Fatal("slice order or attached consumer supplied incarnation", ref, err)
	}
	observation, err := ObserveNameStatus(host, model.Status{Host: host, Resources: []model.ResourceView{resource}})
	if err != nil {
		t.Fatal(err)
	}
	withoutAssertion := ref
	withoutAssertion.BindingLease = ""
	if ValidateCustomNameTarget("set", withoutAssertion) != nil || ValidateCustomNameTarget("clear", withoutAssertion) == nil {
		t.Fatal("set syntax assertion became clear authority")
	}
	derived, err := deriveNameBinding(withoutAssertion, observation)
	if err != nil || derived.BindingLease != owner {
		t.Fatal("empty assertion was not derived from current reservation", derived, err)
	}
	otherRow := resource
	otherRow.Users = []model.ResourceUse{{Lease: attached, Relationship: "reservation"}}
	if owner := CustomNamePortReservations(host, []model.ResourceView{resource, otherRow})[resource.ID]; owner != "" {
		t.Fatal("conflicting duplicate locator rows supplied an incarnation", owner)
	}
	resource.Users = append(resource.Users, model.ResourceUse{Lease: attached, Relationship: "reservation"})
	if _, err = CustomNameResourceReference(host, resource); err == nil {
		t.Fatal("ambiguous reservation accepted")
	}
	resource.Users = []model.ResourceUse{{Lease: attached, Relationship: "activation"}}
	if _, err = CustomNameResourceReference(host, resource); err == nil {
		t.Fatal("infrastructure/attached-only locator acquired a custom incarnation")
	}
}
