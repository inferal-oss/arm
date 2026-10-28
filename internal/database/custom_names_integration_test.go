package database_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/nodeavailability"
)

func TestCustomNamesSettledProofNeverSettlesActiveObligations(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(strconv.FormatBool(active), func(t *testing.T) {
			home := proofFixture(t, active)
			db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			var host string
			err = db.QueryRow(`SELECT value FROM metadata WHERE key='host'`).Scan(&host)
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{"version": 1, "owner": strconv.Itoa(os.Geteuid()), "host": host, "revision": 1, "entries": []any{map[string]any{"kind": "lease", "authority_host": host, "id": strings.Repeat("a", 32), "binding_lease": "", "name": "dangling-history", "active": false}}})
			edit(t, home, `INSERT INTO metadata VALUES('custom_names_v1',?)`, string(raw))
			update := nodeavailability.Update{Key: "combined-profile", Profile: "intermittent", Reason: "Cosmetic declaration"}
			availability, err := json.Marshal(nodeavailability.Document{Schema: 1, Host: host, Owner: strconv.Itoa(os.Geteuid()), Revision: 1, Profile: update.Profile, Reason: update.Reason, UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), LastUpdate: update})
			if err != nil {
				t.Fatal(err)
			}
			edit(t, home, `INSERT INTO metadata VALUES('node_availability_v1',?)`, string(availability))
			p, err := prove(t, home)
			if err != nil || !p.Complete || p.Settled == active {
				t.Fatal("cosmetic metadata changed settlement", p, err)
			}
			edit(t, home, `UPDATE metadata SET value=replace(value,'"active":false','"Active":false') WHERE key='custom_names_v1'`)
			if p, err = prove(t, home); err == nil || p.Complete || p.Settled {
				t.Fatal("corrupt names accepted by strict proof", p, err)
			}
			edit(t, home, `UPDATE metadata SET value=? WHERE key='custom_names_v1'`, string(raw))
			edit(t, home, `UPDATE metadata SET value=replace(value,'"profile":"intermittent"','"profile":"guaranteed"') WHERE key='node_availability_v1'`)
			if p, err = prove(t, home); err == nil || p.Complete || p.Settled {
				t.Fatal("corrupt availability accepted alongside valid names", p, err)
			}
		})
	}
}
