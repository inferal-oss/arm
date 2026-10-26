package database_test

import (
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/model"
)

func TestSyncResetProofRetainsUsedOrPartialNamespace(t *testing.T) {
	for _, damage := range []string{"used", "missing-use-marker", "missing-index"} {
		t.Run(damage, func(t *testing.T) {
			// These cases inspect metadata, not CPU admission; the fixture still
			// acquires and releases its counted pool claim.
			home := proofFixtureResources(t, false, model.Resources{})
			if damage != "missing-index" {
				edit(t, home, `INSERT INTO sync_operations(key,side,pair,generation,record,record_sha256,manifest,state) VALUES('sync-key-12345678','local','pair',1,x'00','digest',x'00','reserved')`)
				if damage == "used" {
					edit(t, home, `INSERT INTO metadata(key,value) VALUES('sync_used','1')`)
				}
			} else {
				edit(t, home, `DROP INDEX sync_open_generation`)
			}
			proof, err := prove(t, home)
			switch damage {
			case "used":
				if err != nil || proof.Settled || len(proof.Blockers) == 0 {
					t.Fatalf("retained sync history accepted: %+v %v", proof, err)
				}
			case "missing-use-marker":
				if err == nil || !strings.Contains(err.Error(), "use marker") {
					t.Fatalf("unmarked sync record accepted: %v", err)
				}
			case "missing-index":
				if err == nil {
					t.Fatal("partial sync schema accepted")
				}
			}
		})
	}
}
