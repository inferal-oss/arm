package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/model"
)

// The destructive reader sees records written by the public manager and actual
// Cargo helper, never a hand-assembled successful Cargo persistence fixture.
func TestCargoSettledProofActualProducerAndCorruptHistory(t *testing.T) {
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(base, "arm")
	m, e := Open(home, model.Resources{CPU: 1, Memory: 1024, Disk: 64 << 20})
	if e != nil {
		t.Fatal(e)
	}
	closed := false
	defer func() {
		if !closed {
			_ = m.Close()
		}
	}()
	r, run, _, _ := realCargoTestProject(t, m, false)
	lease, e := m.Acquire(r, &run)
	if e != nil {
		t.Fatal(e)
	}
	cargoWait(t, func() bool { l, _ := m.Inspect(lease.ID); return l.State == "terminal" })
	lease, e = m.Inspect(lease.ID)
	if e != nil || lease.Outcome != "completed" {
		out, _ := m.Output(lease.ID, 0)
		t.Fatalf("actual compile: %+v %v %s", lease, e, out)
	}
	rejectedRequest := r
	rejectedRequest.Key = "cargo-proof-already-late"
	past := time.Now().Add(-time.Second).UTC()
	rejectedRequest.StartDeadline = &past
	rejectedRun := run
	rejectedRun.Request = rejectedRequest
	rejected, e := m.Acquire(rejectedRequest, &rejectedRun)
	if e != nil || rejected.State != "terminal" || rejected.Outcome != "rejected" || rejected.PID != 0 {
		t.Fatal("past-deadline admission", rejected, e)
	}
	if _, e = m.StorageHandoff(r.StorageLease); e != nil {
		t.Fatal(e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	closed = true
	if e = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	check := func() (database.SettledProof, error) {
		s, e := database.Offline(context.Background(), home)
		if e != nil {
			return database.SettledProof{}, e
		}
		defer s.Close()
		return database.ProveDatabaseSettled(context.Background(), s, strconv.Itoa(os.Geteuid()))
	}
	p, e := check()
	if e != nil || !p.Complete || !p.Settled || p.Tables["cargo_workloads"] != 2 {
		t.Fatalf("actual Cargo proof: %+v %v", p, e)
	}
	db, e := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	var original string
	if e = db.QueryRow(`SELECT data FROM cargo_workloads WHERE lease=?`, lease.ID).Scan(&original); e != nil {
		t.Fatal(e)
	}
	_ = db.Close()
	cases := map[string]string{
		"case-alias":                  `json_set(data,'$.Phase','finished')`,
		"optional-null":               `json_set(data,'$.worker',null)`,
		"required-null":               `json_set(data,'$.group_absent',null)`,
		"worker":                      `json_set(data,'$.worker','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`,
		"uncertain":                   `json_set(data,'$.uncertain',json('true'))`,
		"unknown-phase":               `json_set(data,'$.phase','future')`,
		"no-group-absence":            `json_set(data,'$.group_absent',json('false'))`,
		"no-authorize":                `json_remove(data,'$.authorized_at')`,
		"no-start":                    `json_remove(data,'$.started')`,
		"false-start-receipt":         `json_set(data,'$.finished.command_started',json('false'))`,
		"success-with-failed-receipt": `json_set(data,'$.finished.code',1)`,
		"missing-receipt":             `json_remove(data,'$.finished')`,
		"missing-preflight":           `json_remove(data,'$.preflight_at')`,
		"forged-no-spawn":             `json_set(data,'$.no_spawn',json('true'))`,
		"run-digest":                  `json_set(data,'$.run_sha256','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`,
		"command-digest":              `json_set(data,'$.command_sha256','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`,
		"prepared-digest":             `json_set(data,'$.prepared_sha256','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`,
		"birth-inode":                 `json_set(data,'$.births[1].born.inode',0)`,
		"wrong-parent":                `json_set(data,'$.storage','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`,
		"future-field":                `json_set(data,'$.future_obligation',json('true'))`,
	}
	for name, expression := range cases {
		t.Run(name, func(t *testing.T) {
			db, e := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec(`UPDATE cargo_workloads SET data=`+expression+` WHERE lease=?`, lease.ID); e != nil {
				_ = db.Close()
				t.Fatal(e)
			}
			_ = db.Close()
			p, e := check()
			db, restore := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if restore != nil {
				t.Fatal(restore)
			}
			_, restore = db.Exec(`UPDATE cargo_workloads SET data=? WHERE lease=?`, original, lease.ID)
			_ = db.Close()
			if restore != nil {
				t.Fatal(restore)
			}
			if e == nil && p.Complete && p.Settled {
				raw, _ := json.Marshal(p)
				t.Fatalf("corrupt Cargo accepted %s", raw)
			}
		})
	}
	// Optional namespace must have its marker, table and exact lease row together.
	for _, kind := range []string{"marker", "row"} {
		t.Run("missing-"+kind, func(t *testing.T) {
			db, e := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if e != nil {
				t.Fatal(e)
			}
			if kind == "marker" {
				_, e = db.Exec(`DELETE FROM metadata WHERE key='cargo_schema'`)
			} else {
				_, e = db.Exec(`DELETE FROM cargo_workloads WHERE lease=?`, lease.ID)
			}
			_ = db.Close()
			if e != nil {
				t.Fatal(e)
			}
			p, e := check()
			db, restore := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if restore != nil {
				t.Fatal(restore)
			}
			if kind == "marker" {
				_, restore = db.Exec(`INSERT INTO metadata(key,value) VALUES('cargo_schema','1')`)
			} else {
				_, restore = db.Exec(`INSERT INTO cargo_workloads(lease,data) VALUES(?,?)`, lease.ID, original)
			}
			_ = db.Close()
			if restore != nil {
				t.Fatal(restore)
			}
			if e == nil && p.Complete && p.Settled {
				t.Fatal("incomplete Cargo ledger accepted")
			}
		})
	}
}
