//go:build arm_devtrace

package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
)

// Clock input is controlled. Every emitted lease, journal event and checkpoint
// below is read from actual physical SQLite after a real Manager operation.
// This observes bare memory entitlement, not native clocks/processes/providers.
func TestLeaseClockActualTrace(t *testing.T) {
	for _, scenario := range []string{"wall-rollback", "wall-forward", "suspend-admission", "restart-live", "restart-expired", "restart-other-boot", "restart-regressed", "malformed-deadline", "sticky-read-failure", "journal-refusal"} {
		t.Run(scenario, func(t *testing.T) {
			origin := int64(time.Hour)
			wallOrigin := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
			sample := leaseclock.Sample{Boot: "a", Tick: origin + int64(time.Second), Wall: wallOrigin.Add(time.Hour)}
			var readErr error
			read := func() (leaseclock.Sample, error) { return sample, readErr }
			home := filepath.Join(t.TempDir(), "state")
			m, err := openWithLeaseClock(home, model.Resources{Memory: 1}, read)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if m != nil {
					if err := m.Close(); err != nil {
						t.Error(err)
					}
				}
			}()
			id, nextID := "", ""
			index := 0
			capture := func(op string, returned error) {
				t.Helper()
				m.mu.Lock()
				defer m.mu.Unlock()
				row := struct {
					Schema        int               `json:"schema"`
					Scenario      string            `json:"scenario"`
					Index         int               `json:"index"`
					Operation     string            `json:"operation"`
					Input         leaseclock.Sample `json:"input"`
					InputError    bool              `json:"input_error"`
					ClockError    string            `json:"clock_error"`
					ReturnedError string            `json:"returned_error"`
					ID            string            `json:"lease_id"`
					NextID        string            `json:"next_id"`
					Leases        []json.RawMessage `json:"leases"`
					Events        []json.RawMessage `json:"events"`
					Checkpoint    json.RawMessage   `json:"checkpoint"`
				}{Schema: 1, Scenario: scenario, Index: index, Operation: op, Input: sample, InputError: readErr != nil, ID: id, NextID: nextID, Leases: []json.RawMessage{}, Events: []json.RawMessage{}}
				if m.leaseClockError != nil {
					row.ClockError = m.leaseClockError.Code
				}
				if returned != nil {
					var api *model.Error
					if errors.As(returned, &api) {
						row.ReturnedError = api.Code
					} else {
						row.ReturnedError = "database_error"
					}
				}
				rows, e := m.db.Query(`SELECT data FROM leases ORDER BY rowid LIMIT 3`)
				if e != nil {
					t.Fatal(e)
				}
				for rows.Next() {
					var raw []byte
					if e = rows.Scan(&raw); e != nil {
						rows.Close()
						t.Fatal(e)
					}
					row.Leases = append(row.Leases, append(json.RawMessage(nil), raw...))
				}
				if e = rows.Err(); e != nil {
					rows.Close()
					t.Fatal(e)
				}
				if e = rows.Close(); e != nil {
					t.Fatal(e)
				}
				if len(row.Leases) > 2 {
					t.Fatal("unexpected lease roster")
				}
				rows, e = m.db.Query(`SELECT sequence,type,entity FROM events WHERE type GLOB 'lease.*' ORDER BY sequence LIMIT 33`)
				if e != nil {
					t.Fatal(e)
				}
				for rows.Next() {
					var seq int64
					var kind, entity string
					if e = rows.Scan(&seq, &kind, &entity); e != nil {
						rows.Close()
						t.Fatal(e)
					}
					raw, e := json.Marshal(struct {
						Sequence int64  `json:"sequence"`
						Type     string `json:"type"`
						Entity   string `json:"entity"`
					}{seq, kind, entity})
					if e != nil {
						rows.Close()
						t.Fatal(e)
					}
					row.Events = append(row.Events, raw)
				}
				if e = rows.Err(); e != nil {
					rows.Close()
					t.Fatal(e)
				}
				if e = rows.Close(); e != nil {
					t.Fatal(e)
				}
				if len(row.Events) > 32 {
					t.Fatal("unexpected event roster")
				}
				var raw string
				if e = m.db.QueryRow(`SELECT value FROM metadata WHERE key='lease_clock'`).Scan(&raw); e != nil && !errors.Is(e, sql.ErrNoRows) {
					t.Fatal(e)
				}
				row.Checkpoint = json.RawMessage(raw)
				b, e := json.Marshal(row)
				if e != nil {
					t.Fatal(e)
				}
				t.Log("LEASE_CLOCK_ACTUAL " + string(b))
				index++
			}
			set := func(tick, wall int64) {
				sample.Tick = origin + tick*int64(time.Second)
				sample.Wall = wallOrigin.Add(time.Duration(wall) * time.Hour)
			}
			capture("initial", nil)
			first, err := m.Acquire(model.Request{Key: "clock-first", Reason: "formal bare-memory lease clock fixture", Resources: model.Resources{Memory: 1}, TTL: 2 * time.Second}, nil)
			if err != nil || first.State != "active" {
				t.Fatal(first, err)
			}
			id = first.ID
			capture("grant", err)
			renew := func(ttl time.Duration, op string) { _, err = m.Renew(id, ttl); capture(op, err) }
			reopen := func() {
				if err = m.Close(); err != nil {
					t.Fatal(err)
				}
				m = nil
				m, err = openWithLeaseClock(home, model.Resources{Memory: 1}, read)
				if err != nil {
					t.Fatal(err)
				}
				capture("restart", nil)
			}
			switch scenario {
			case "wall-rollback", "wall-forward":
				w := int64(0)
				if scenario == "wall-forward" {
					w = 2
				}
				set(2, w)
				m.tick()
				capture("poll", nil)
				renew(3*time.Second, "renew")
				set(5, w)
				renew(3*time.Second, "renew-refused")
				renew(3*time.Second, "renew-refused")
			case "suspend-admission":
				set(3, 1)
				second, e := m.Acquire(model.Request{Key: "clock-next", Reason: "admission after suspend fixture", Resources: model.Resources{Memory: 1}, TTL: 2 * time.Second}, nil)
				if e != nil || second.State != "active" {
					t.Fatal(second, e)
				}
				nextID = second.ID
				capture("admit", e)
				renew(3*time.Second, "renew-refused")
			case "restart-live":
				set(2, 0)
				reopen()
			case "restart-expired":
				set(3, 1)
				reopen()
			case "restart-other-boot":
				sample.Boot = "b"
				set(1, 1)
				reopen()
			case "restart-regressed":
				set(0, 1)
				reopen()
			case "malformed-deadline":
				m.mu.Lock()
				changed, e := m.get(id)
				if e == nil {
					changed.Clock.Version = 2
					e = m.put(changed, "fixture.corrupt")
				}
				m.mu.Unlock()
				if e != nil {
					t.Fatal(e)
				}
				capture("corrupt", nil)
				set(2, 1)
				m.tick()
				capture("poll", nil)
				renew(3*time.Second, "renew-refused")
			case "sticky-read-failure":
				readErr = errors.New("controlled clock read failure")
				renew(3*time.Second, "clock-refused")
				readErr = nil
				set(2, 1)
				renew(3*time.Second, "sticky-refused")
			case "journal-refusal":
				_, err = m.db.Exec(`CREATE TRIGGER fixture_clock_checkpoint_failure BEFORE UPDATE ON metadata WHEN NEW.key='lease_clock' BEGIN SELECT RAISE(ABORT,'controlled clock checkpoint failure'); END`)
				if err != nil {
					t.Fatal(err)
				}
				set(3, 1)
				renew(3*time.Second, "journal-refused")
				_, err = m.db.Exec(`DROP TRIGGER fixture_clock_checkpoint_failure`)
				if err != nil {
					t.Fatal(err)
				}
				m.tick()
				capture("poll", nil)
			}
		})
	}
}
