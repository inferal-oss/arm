package nodestop

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	_ "modernc.org/sqlite"
)

func recordFixture(t *testing.T) (Record, State) {
	t.Helper()
	_, p, _, now := contractFixture(t)
	p.DesiredRevision = 1
	r := Record{Schema: RecordSchema, Plan: p, Shutdown: strings.Repeat("7", 32), DesiredRevision: p.DesiredRevision + 1, Phase: "accepted", Created: now, Updated: now}
	h, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	r.Digest = h
	s := State{Schema: StateSchema, Revision: r.DesiredRevision, Running: false, Target: p.Target, Key: p.Key, Digest: h}
	if err = s.Validate(r); err != nil {
		t.Fatal(err)
	}
	return r, s
}

func TestClosedWireRejectsAliasesDuplicatesNullAndPartialNestedFrames(t *testing.T) {
	_, p, _, _ := contractFixture(t)
	q := application(t, p)
	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	var positive Apply
	if err = Decode(raw, &positive); err != nil {
		t.Fatal("valid typed application", err)
	}
	for name, altered := range map[string]string{
		"duplicate":         strings.Replace(string(raw), `"key":"stop-one"`, `"key":"stop-one","key":"stop-one"`, 1),
		"case-alias":        strings.Replace(string(raw), `"reason":`, `"Reason":`, 1),
		"nested-missing":    strings.Replace(string(raw), `"force":false,`, "", 1),
		"null-array":        strings.Replace(string(raw), `"blockers":[]`, `"blockers":null`, 1),
		"float-revision":    strings.Replace(string(raw), `"desired_state_revision":7`, `"desired_state_revision":7.0`, 1),
		"overflow-revision": strings.Replace(string(raw), `"desired_state_revision":7`, `"desired_state_revision":18446744073709551616`, 1),
		"extra-value":       string(raw) + ` {}`,
		"unknown":           strings.Replace(string(raw), `"policy":{`, `"policy":{"unused":false,`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			var out Apply
			if Decode([]byte(altered), &out) == nil {
				t.Fatal("altered closed frame accepted")
			}
		})
	}
	if Decode([]byte(strings.Repeat(" ", 64<<10+1)), new(Apply)) == nil {
		t.Fatal("oversized frame accepted")
	}
}

func stateDB(t *testing.T) (*sql.DB, Record, State) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, q := range Tables {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO node_stop_schema VALUES(1,1)`); err != nil {
		t.Fatal(err)
	}
	r, s := recordFixture(t)
	rawR, _ := json.Marshal(r)
	rawS, _ := json.Marshal(s)
	if _, err = db.Exec(`INSERT INTO node_stop_operations VALUES(?,?)`, s.Key, string(rawR)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO node_stop_state VALUES(1,?)`, string(rawS)); err != nil {
		t.Fatal(err)
	}
	return db, r, s
}

func TestDurableStateRejectsImpossibleRestartPhaseCombinations(t *testing.T) {
	for _, row := range []struct {
		phase          string
		running, valid bool
	}{{"accepted", false, true}, {"drain-ready", false, true}, {"drain-ready", true, true}, {"interrupted", true, true}, {"accepted", true, false}, {"interrupted", false, false}} {
		name := row.phase + "-stopped"
		if row.running {
			name = row.phase + "-running"
		}
		t.Run(name, func(t *testing.T) {
			db, record, state := stateDB(t)
			record.Phase = row.phase
			state.Running = row.running
			if row.running {
				state.Revision++
			}
			if err := state.Validate(record); err != nil {
				t.Fatal("generic transaction construction changed", err)
			}
			recordRaw, _ := json.Marshal(record)
			stateRaw, _ := json.Marshal(state)
			if _, err := db.Exec(`UPDATE node_stop_operations SET data=? WHERE key=?`, string(recordRaw), record.Plan.Key); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE node_stop_state SET data=? WHERE singleton=1`, string(stateRaw)); err != nil {
				t.Fatal(err)
			}
			got, present, err := ReadState(db)
			if row.valid {
				if err != nil || !present || got != state {
					t.Fatal(got, present, err)
				}
			} else if err == nil || present {
				t.Fatal("impossible durable phase was normalized", got, present, err)
			}
			var recordAfter, stateAfter string
			if err = db.QueryRow(`SELECT data FROM node_stop_operations WHERE key=?`, record.Plan.Key).Scan(&recordAfter); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow(`SELECT data FROM node_stop_state WHERE singleton=1`).Scan(&stateAfter); err != nil {
				t.Fatal(err)
			}
			if recordAfter != string(recordRaw) || stateAfter != string(stateRaw) {
				t.Fatal("reader rewrote retained evidence")
			}
		})
	}
}

func TestOptionalSchemaIsLegacyAbsentOrCompleteAndClosed(t *testing.T) {
	db, _, want := stateDB(t)
	got, present, err := ReadState(db)
	if err != nil || !present || got != want {
		t.Fatal(got, present, err)
	}
	for _, name := range []string{"partial", "unknown-version", "unknown-table", "trigger", "erased-operation", "fractional-revision", "unsigned-overflow", "oversized-row", "blob-row"} {
		t.Run(name, func(t *testing.T) {
			db, _, state := stateDB(t)
			var query string
			switch name {
			case "partial":
				query = `DROP TABLE node_stop_operations`
			case "unknown-version":
				query = `PRAGMA ignore_check_constraints=ON`
				if _, e := db.Exec(query); e != nil {
					t.Fatal(e)
				}
				query = `UPDATE node_stop_schema SET version=2`
			case "unknown-table":
				query = `CREATE TABLE node_stop_surprise(x TEXT)`
			case "trigger":
				query = `CREATE TRIGGER surprise AFTER UPDATE ON node_stop_state BEGIN SELECT 1; END`
			case "erased-operation":
				query = `DELETE FROM node_stop_operations`
			case "unsigned-overflow":
				state.Revision = MaxRevision + 1
				raw, _ := json.Marshal(state)
				if _, e := db.Exec(`UPDATE node_stop_state SET data=?`, string(raw)); e != nil {
					t.Fatal(e)
				}
			case "oversized-row":
				if _, e := db.Exec(`UPDATE node_stop_state SET data=?`, strings.Repeat("x", 65537)); e != nil {
					t.Fatal(e)
				}
			case "blob-row":
				raw, _ := json.Marshal(state)
				if _, e := db.Exec(`UPDATE node_stop_state SET data=CAST(? AS BLOB)`, string(raw)); e != nil {
					t.Fatal(e)
				}
			case "fractional-revision":
				raw, _ := json.Marshal(state)
				bad := strings.Replace(string(raw), `"revision":2`, `"revision":2.0`, 1)
				if _, e := db.Exec(`UPDATE node_stop_state SET data=?`, bad); e != nil {
					t.Fatal(e)
				}
			}
			if query != "" {
				if _, e := db.Exec(query); e != nil {
					t.Fatal(e)
				}
			}
			if _, _, e := ReadState(db); e == nil {
				t.Fatal("partial/unknown durable schema treated as safe running intent")
			}
		})
	}
	legacy, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, present, e := ReadState(legacy); e != nil || present {
		t.Fatal("absent optional schema is not legacy", present, e)
	}
}

func TestSignedRevisionBoundsAndExactReviewedRestart(t *testing.T) {
	r, s := recordFixture(t)
	now := r.Created
	q, err := ReviewRestart(s, r, "explicit private restart", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Validate(s, r, s.Target.Owner, now); err != nil {
		t.Fatal(err)
	}
	other := s
	other.Revision++
	if q.Validate(other, r, s.Target.Owner, now) == nil || q.Validate(s, r, "999", now) == nil || q.Validate(s, r, s.Target.Owner, q.Expires) == nil {
		t.Fatal("restart review borrowed across revision/owner/time")
	}
	r.Plan.DesiredRevision = MaxRevision - 1
	r.DesiredRevision = MaxRevision
	r.Digest, err = r.Plan.Digest()
	if err != nil {
		t.Fatal(err)
	}
	s.Revision = MaxRevision
	s.Digest = r.Digest
	if err = s.Validate(r); err != nil {
		t.Fatal("last representable stopped revision", err)
	}
	if _, err = ReviewRestart(s, r, "overflow", now); err == nil {
		t.Fatal("restart advanced beyond signed SQLite domain")
	}
	r.Plan.DesiredRevision = MaxRevision
	if _, err = r.Plan.Digest(); err == nil {
		t.Fatal("stop revision overflow permitted")
	}
}

func TestJoinedClosureRetainsFailureAndRequiresEveryExactStage(t *testing.T) {
	r, _ := recordFixture(t)
	r.Phase = "drain-ready"
	c, err := NewClosure(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range closureStages {
		c.Stage(stage, nil)
	}
	if c.Error() != nil {
		t.Fatal(c.Error())
	}
	c.Stage("manager", nil)
	if c.Error() == nil {
		t.Fatal("duplicate stage normalized")
	}
	c, err = NewClosure(r)
	if err != nil {
		t.Fatal(err)
	}
	denial := errors.New("owned component close failed")
	for i, stage := range closureStages {
		var e error
		if i == 2 {
			e = denial
		}
		c.Stage(stage, e)
	}
	if !errors.Is(c.Error(), denial) {
		t.Fatal("later joins erased earlier failure")
	}
	lock := model.FileIdentity{Device: 7, Inode: 99, Owner: 501, BirthKnown: true, BornSeconds: 10}
	closed := Closed{Schema: "arm.node-stop.closed/v1", Lock: lock, Record: r, Stages: append([]string{}, closureStages[:]...), At: r.Updated.Add(time.Second)}
	if err = closed.Validate(r, lock); err != nil {
		t.Fatal(err)
	}
	foreign := lock
	foreign.Inode++
	if closed.Validate(r, foreign) == nil {
		t.Fatal("marker borrowed a replaced lock")
	}
	closed.Stages = closed.Stages[:len(closed.Stages)-1]
	if closed.Validate(r, lock) == nil {
		t.Fatal("partial joins certified completion")
	}
}
