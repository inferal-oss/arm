package database_test

import (
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/model"
)

func TestWorkspaceReviewResetSchemaVersionAndLocatorRetention(t *testing.T) {
	for _, kind := range []string{"fresh2", "legacy1", "legacy-used1", "missing-table2", "missing-unique2", "marker1-with-table2", "orphan-locator2", "used2"} {
		t.Run(kind, func(t *testing.T) {
			home := proofFixtureResources(t, false, model.Resources{})
			switch kind {
			case "legacy1", "legacy-used1":
				edit(t, home, `DROP TABLE sync_workspaces`)
				edit(t, home, `UPDATE metadata SET value='1' WHERE key='sync_schema'`)
				if kind == "legacy-used1" {
					edit(t, home, `INSERT INTO sync_operations(key,side,pair,generation,record,record_sha256,manifest,state) VALUES('sync-key-12345678','local','pair',1,x'00','digest',x'00','reserved')`)
					edit(t, home, `INSERT INTO metadata(key,value) VALUES('sync_used','1')`)
				}
			case "missing-table2":
				edit(t, home, `DROP TABLE sync_workspaces`)
			case "missing-unique2":
				edit(t, home, `DROP TABLE sync_workspaces`)
				edit(t, home, `CREATE TABLE sync_workspaces (key TEXT NOT NULL,side TEXT NOT NULL,role TEXT NOT NULL,path TEXT NOT NULL,root_id TEXT NOT NULL,parent_id TEXT NOT NULL,base_id TEXT NOT NULL,disk_bytes INTEGER NOT NULL,PRIMARY KEY(key,side,role))`)
			case "marker1-with-table2":
				edit(t, home, `UPDATE metadata SET value='1' WHERE key='sync_schema'`)
			case "orphan-locator2":
				edit(t, home, `INSERT INTO sync_workspaces VALUES('orphan','local','receiver','private path','root','parent','base',1)`)
			case "used2":
				edit(t, home, `INSERT INTO sync_operations(key,side,pair,generation,record,record_sha256,manifest,state) VALUES('sync-key-12345678','local','pair',1,x'00','digest',x'00','reserved')`)
				edit(t, home, `INSERT INTO metadata(key,value) VALUES('sync_used','1')`)
			}
			proof, err := prove(t, home)
			switch kind {
			case "fresh2", "legacy1":
				if err != nil || !proof.Complete || !proof.Settled {
					t.Fatalf("exact settled schema refused: %+v %v", proof, err)
				}
			case "legacy-used1", "used2":
				if err != nil || proof.Settled || len(proof.Blockers) == 0 {
					t.Fatalf("used sync obligation lost: %+v %v", proof, err)
				}
			case "orphan-locator2":
				if err == nil || !strings.Contains(err.Error(), "use marker") {
					t.Fatalf("orphan native locator accepted: %+v %v", proof, err)
				}
			default:
				if err == nil {
					t.Fatalf("incompatible or partial sync shape accepted: %+v", proof)
				}
			}
		})
	}
}
