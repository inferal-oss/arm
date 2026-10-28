package database_test

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// The prior catalog ceiling was 147 objects. Run history contributes two
// tables and three implicit indexes (5); the forwarding guard contributes one
// table, two implicit indexes and four triggers (7). The composed ceiling is
// therefore 159 objects, with LIMIT 160 to detect one excess object.
func TestForwardUnixGuardAndRunProofCatalogCensus(t *testing.T) {
	home := proofFixture(t, false)
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var runs, guard, total int
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name IN ('proxy_runs','proxy_run_chunks') OR name GLOB 'sqlite_autoindex_proxy_runs_*' OR name GLOB 'sqlite_autoindex_proxy_run_chunks_*'`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='forward_unix_pairs' OR name GLOB 'forward_unix_*_guard' OR name GLOB 'sqlite_autoindex_forward_unix_pairs_*'`).Scan(&guard); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_schema`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if runs != 5 || guard != 7 || total > 159 {
		t.Fatalf("composed schema object census differs: run=%d guard=%d total=%d", runs, guard, total)
	}
	proof, err := prove(t, home)
	if err != nil || !proof.Complete || !proof.Settled {
		t.Fatalf("empty pair guard changed settled proof: %+v %v", proof, err)
	}
}

func TestForwardUnixGuardAndRunProofRejectPartialSchema(t *testing.T) {
	for _, tc := range []struct{ name, change string }{
		{"missing guard trigger", `DROP TRIGGER forward_unix_source_update_guard`},
		{"altered guard trigger", `DROP TRIGGER forward_unix_source_update_guard; CREATE TRIGGER forward_unix_source_update_guard BEFORE UPDATE OF data ON leases BEGIN SELECT 1; END`},
		{"missing guard table", `DROP TABLE forward_unix_pairs`},
		{"missing run table", `DROP TABLE proxy_run_chunks`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := proofFixture(t, false)
			edit(t, home, tc.change)
			if proof, err := prove(t, home); err == nil || proof.Settled {
				t.Fatalf("partial or altered composed schema was accepted: %+v %v", proof, err)
			}
		})
	}
}
