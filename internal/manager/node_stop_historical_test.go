package manager

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
)

func stopLedgerText(t *testing.T, m *Manager) []string {
	t.Helper()
	rows, err := m.db.Query(`SELECT data FROM node_stop_state UNION ALL SELECT data FROM node_stop_operations ORDER BY data`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var text string
		if err = rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		out = append(out, text)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func historicalRunningManager(t *testing.T) (*Manager, nodestop.Record) {
	t.Helper()
	m, actor, external := newStopManagerFixture(t)
	oldCloseAttempted := false
	closeOld := func() error {
		if oldCloseAttempted {
			return nil
		}
		oldCloseAttempted = true
		return m.Close()
	}
	t.Cleanup(func() {
		if err := closeOld(); err != nil {
			t.Error("original historical Manager close failed", err)
		}
	})
	q := stopApplication(t, m, actor, external, model.ShutdownRequest{})
	if _, err := m.BeginNodeStop(q, actor, true, external); err != nil {
		t.Fatal(err)
	}
	report, err := m.AdvanceShutdown()
	if err != nil {
		t.Fatal(err)
	}
	record, present, err := m.NodeStopDrainReady(report)
	if err != nil || !present {
		t.Fatal(present, err)
	}
	s, present, err := m.NodeStopDesiredState()
	if err != nil || !present {
		t.Fatal(present, err)
	}
	// Controlled valid post-review row for Manager branch tests. The actual
	// retained native lock/review/CAS and Serve sequence is a separate server test.
	s.Running = true
	s.Revision++
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`UPDATE node_stop_state SET data=? WHERE singleton=1`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := nodestop.ReadState(m.db); err != nil || !exists {
		t.Fatal(exists, err)
	}
	home := m.home
	if err = closeOld(); err != nil {
		t.Fatal(err)
	}
	fresh, err := Open(home, model.Resources{CPU: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	return fresh, record
}

func TestNodeStopHistoricalRunningAllowsOnlyOrdinaryShutdown(t *testing.T) {
	m, old := historicalRunningManager(t)
	before := stopLedgerText(t, m)
	report, err := m.BeginShutdown(model.ShutdownRequest{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	report, err = m.AdvanceShutdown()
	if err != nil || report.Phase != "ready" || report.ID == old.Shutdown {
		t.Fatal(report, err)
	}
	if _, present, err := m.NodeStopDrainReady(report); err != nil || present {
		t.Fatal("historical intent blocked ordinary fresh-lifetime shutdown", present, err)
	}
	after := stopLedgerText(t, m)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatal("ordinary shutdown rewrote retained stop history")
	}
}

func TestNodeStopReadinessHistoricalBypassPreservesRefusals(t *testing.T) {
	for _, kind := range []string{"wrong-shutdown", "current-running", "current-generation", "current-interrupted", "missing-current-ledger", "historical-corrupt-schema", "historical-foreign-authority", "historical-unconfirmed-accepted", "historical-unknown"} {
		t.Run(kind, func(t *testing.T) {
			var m *Manager
			var report model.ShutdownReport
			if strings.HasPrefix(kind, "historical-") {
				m, _ = historicalRunningManager(t)
				report = model.ShutdownReport{ID: "ordinary", Phase: "ready"}
			} else {
				var actor nodestop.Actor
				var external nodestop.Impact
				m, actor, external = stopManagerFixture(t)
				q := stopApplication(t, m, actor, external, model.ShutdownRequest{})
				if _, err := m.BeginNodeStop(q, actor, true, external); err != nil {
					t.Fatal(err)
				}
				var err error
				report, err = m.AdvanceShutdown()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "wrong-shutdown":
				report.ID = "not-the-admitted-shutdown"
			case "current-running":
				s, present, err := m.NodeStopDesiredState()
				if err != nil || !present {
					t.Fatal(present, err)
				}
				s.Running = true
				s.Revision++
				raw, err := json.Marshal(s)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = m.db.Exec(`UPDATE node_stop_state SET data=? WHERE singleton=1`, string(raw)); err != nil {
					t.Fatal(err)
				}
			case "current-generation":
				m.nodeStopAcceptedTarget.Generation = strings.Repeat("6", 32)
			case "current-interrupted", "historical-unconfirmed-accepted":
				state, present, err := m.NodeStopDesiredState()
				if err != nil || !present {
					t.Fatal(present, err)
				}
				record, err := nodestop.Lookup(m.db, state.Key)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "current-interrupted" {
					record.Phase = "interrupted"
				} else {
					record.Phase = "accepted"
				}
				raw, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = m.db.Exec(`UPDATE node_stop_operations SET data=? WHERE key=?`, string(raw), record.Plan.Key); err != nil {
					t.Fatal(err)
				}
			case "missing-current-ledger":
				for _, name := range []string{"node_stop_operations", "node_stop_state", "node_stop_schema"} {
					if _, err := m.db.Exec(`DROP TABLE ` + name); err != nil {
						t.Fatal(err)
					}
				}
			case "historical-corrupt-schema":
				// Build one genuinely stored invalid marker on the SAME retained
				// SQLite connection, then restore enforcement before the reader.
				if err := func() (resultErr error) {
					connection, err := m.db.Conn(t.Context())
					if err != nil {
						return err
					}
					defer func() {
						restore, cancel := context.WithTimeout(context.Background(), time.Second)
						defer cancel()
						_, restoreErr := connection.ExecContext(restore, `PRAGMA ignore_check_constraints=OFF`)
						resultErr = errors.Join(resultErr, restoreErr, connection.Close())
					}()
					if _, err = connection.ExecContext(t.Context(), `PRAGMA ignore_check_constraints=ON`); err != nil {
						return err
					}
					_, err = connection.ExecContext(t.Context(), `UPDATE node_stop_schema SET version=9`)
					return err
				}(); err != nil {
					t.Fatal("constructing retained unknown-version marker failed", err)
				}
				var version int
				if err := m.db.QueryRow(`SELECT version FROM node_stop_schema`).Scan(&version); err != nil || version != 9 {
					t.Fatal("negative control did not store the intended corruption", version, err)
				}
			case "historical-foreign-authority":
				state, present, err := m.NodeStopDesiredState()
				if err != nil || !present {
					t.Fatal(present, err)
				}
				record, err := nodestop.Lookup(m.db, state.Key)
				if err != nil {
					t.Fatal(err)
				}
				record.Plan.Target.Host = strings.Repeat("e", 32)
				record.Digest, err = record.Plan.Digest()
				if err != nil {
					t.Fatal(err)
				}
				state.Target = record.Plan.Target
				state.Digest = record.Digest
				recordRaw, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				stateRaw, err := json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = m.db.Exec(`UPDATE node_stop_operations SET data=? WHERE key=?`, string(recordRaw), record.Plan.Key); err != nil {
					t.Fatal(err)
				}
				if _, err = m.db.Exec(`UPDATE node_stop_state SET data=? WHERE singleton=1`, string(stateRaw)); err != nil {
					t.Fatal(err)
				}
				if _, present, err = nodestop.ReadState(m.db); err != nil || !present {
					t.Fatal("foreign fixture must remain structurally closed", present, err)
				}
			case "historical-unknown":
				m.nodeStopUnknown = true
			}
			var before []string
			if kind != "missing-current-ledger" {
				before = stopLedgerText(t, m)
			}
			if _, _, err := m.NodeStopDrainReady(report); err == nil {
				t.Fatal("readiness normalized current/unknown/malformed scope", kind)
			}
			if kind != "missing-current-ledger" {
				if after := stopLedgerText(t, m); strings.Join(before, "\n") != strings.Join(after, "\n") {
					t.Fatal("refusal rewrote retained operation/state")
				}
			} else {
				var count int
				if err := m.db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name GLOB 'node_stop_*'`).Scan(&count); err != nil || count != 0 {
					t.Fatal("refusal fabricated missing ledger", count, err)
				}
			}
		})
	}
}
