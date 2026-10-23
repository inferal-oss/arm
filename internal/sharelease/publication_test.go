package sharelease

import (
	"bytes"
	"strings"
	"testing"
)

func TestPublicationFinalizationCapacityCoversEveryVerificationPath(t *testing.T) {
	// Saturate actual byte capacity with maximum-size future rows. Each path is
	// real admission plus exact reserved future-slot counts, including recovery.
	for name, path := range map[string][]int{
		"publish-retire":          {8, 7, 6, 5, 4, 3, 2, 0},
		"verify-reserved-restart": {8, 7, 6, 5, 4, 0},
		"verify-running-restart":  {8, 7, 6, 5, 4, 3, 0},
		"verify-running-release":  {8, 7, 6, 5, 4, 3, 0},
		"verify-failure":          {8, 7, 6, 5, 4, 0},
		"preparation-restart":     {8, 7, 6, 0},
	} {
		t.Run(name, func(t *testing.T) {
			c := JournalCapacity{Bytes: MaxJournalBytes - 9*MaxJournalRecordBytes}
			old := 0
			for _, slots := range path {
				var err error
				c, err = c.Change(MaxJournalRecordBytes, old, slots)
				if err != nil {
					t.Fatalf("reserved path cannot finalize %v at %d: %v", path, slots, err)
				}
				old = slots
			}
			if c.ReservedSlots != 0 {
				t.Fatal(c)
			}
		})
	}
	c := JournalCapacity{Bytes: MaxJournalBytes - 9*MaxJournalRecordBytes + 1}
	if _, err := c.Change(MaxJournalRecordBytes, 0, PublicationFinalSlots); err == nil {
		t.Fatal("admitted work missing one finalization byte")
	}
	c = JournalCapacity{Rows: MaxJournalRows - PublicationFinalSlots}
	if _, err := c.Change(1, 0, PublicationFinalSlots); err == nil {
		t.Fatal("admitted work missing one finalization row")
	}
}

func TestPublicationSchemaAndIntentAreClosedAndPhaseBound(t *testing.T) {
	p, _, _ := evidenceFixture()
	raw, _ := Canonical(p)
	// Old unpublished private evidence is preserved/refused, never adopted.
	old := bytes.Replace(raw, []byte(`"schema":2`), []byte(`"schema":1`), 1)
	if _, err := DecodePublication(old); err == nil {
		t.Fatal("adopted old publication schema")
	}
	for name, mutate := range map[string]func(*Publication){
		"phase":           func(p *Publication) { p.WorkerPhase = "verification" },
		"original-worker": func(p *Publication) { p.PreparationWorker.Start = "changed" },
		"premature-intent": func(p *Publication) {
			p.Publish = &PublishRequest{Schema: 1, Key: "verify", Share: p.ID, Generation: p.Generation, Revision: 1, PreparedSHA256: strings.Repeat("a", 64), Reason: "approve"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			next := p
			mutate(&next)
			if next.Validate() == nil {
				t.Fatal("accepted contradictory phase evidence")
			}
		})
	}
}
