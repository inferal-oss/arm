package manager

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"getarm.org/internal/nodeavailability"
)

func TestNodeAvailabilityOptionalCosmeticCAS(t *testing.T) {
	m := newManager(t)
	missing, err := m.NodeAvailability()
	if err != nil || missing.OwnerDeclared.Profile != "unspecified" || missing.OwnerDeclared.Revision != 0 {
		t.Fatal(missing, err)
	}
	var count int
	if err := m.db.QueryRow(`SELECT count(*) FROM metadata WHERE key=?`, nodeavailability.MetadataKey).Scan(&count); err != nil || count != 0 {
		t.Fatal("read backfilled metadata", count, err)
	}
	before, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	update := nodeavailability.Update{Key: "owner-change", Profile: "intermittent", Reason: "private owner reason"}
	first, err := m.SetNodeAvailability(update)
	if err != nil || first.Revision != 1 {
		t.Fatal(first, err)
	}
	again, err := m.SetNodeAvailability(update)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("current-key retry changed document", again, err)
	}
	conflict := update
	conflict.Profile = "stable"
	if _, err := m.SetNodeAvailability(conflict); errorCode(err) != "idempotency_conflict" {
		t.Fatal("same key changed request", err)
	}
	second := nodeavailability.Update{Key: "later-change", ExpectedRevision: 1, Profile: "stable", Reason: "explicit later declaration"}
	if _, err := m.SetNodeAvailability(second); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetNodeAvailability(update); errorCode(err) != "revision_conflict" || !strings.Contains(err.Error(), "may have applied") {
		t.Fatal("old revision reapplied or claimed not applied", err)
	}
	current, err := m.NodeAvailability()
	if err != nil || current.OwnerDeclared.Profile != "stable" || current.OwnerDeclared.Revision != 2 {
		t.Fatal(current, err)
	}
	raw, _ := json.Marshal(current)
	if strings.Contains(string(raw), "private owner reason") || strings.Contains(string(raw), update.Key) {
		t.Fatal("peer report leaked private metadata")
	}
	after, err := m.Status()
	if err != nil || !reflect.DeepEqual(before.Leases, after.Leases) || before.Reserved != after.Reserved || before.Capacity != after.Capacity {
		t.Fatal("cosmetic metadata altered resource authority", err)
	}
	if _, err := m.db.Exec(`UPDATE metadata SET value='{}' WHERE key=?`, nodeavailability.MetadataKey); err != nil {
		t.Fatal(err)
	}
	if _, err := m.NodeAvailability(); err == nil {
		t.Fatal("corrupt profile silently became stable/unspecified")
	}
	if _, err := m.SetNodeAvailability(second); err == nil {
		t.Fatal("setter overwrote corrupt evidence")
	}
	for _, test := range []struct {
		name  string
		value any
	}{{"blob-read", []byte(`{}`)}, {"oversized-read", strings.Repeat("x", nodeavailability.MaxDocumentBytes+1)}} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := m.db.Exec(`UPDATE metadata SET value=? WHERE key=?`, test.value, nodeavailability.MetadataKey); err != nil {
				t.Fatal(err)
			}
			if _, err := m.NodeAvailability(); errorCode(err) != "availability_metadata_invalid" {
				t.Fatal("unbounded or non-TEXT metadata was decoded", err)
			}
			var kind, projected string
			var size int64
			if err := m.db.QueryRow(nodeAvailabilityReadSQL, nodeavailability.MaxDocumentBytes, nodeavailability.MetadataKey).Scan(&kind, &size, &projected); err != nil {
				t.Fatal(err)
			}
			if projected != "" {
				t.Fatal("unbounded metadata value scanned before type/size refusal", len(projected))
			}
		})
	}
	if _, err := m.db.Exec(`UPDATE metadata SET value=NULL WHERE key=?`, nodeavailability.MetadataKey); err == nil {
		t.Fatal("existing metadata NOT NULL constraint was not preserved")
	}
}
