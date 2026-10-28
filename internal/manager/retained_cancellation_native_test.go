package manager

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
	"getarm.org/internal/ports"
)

func TestOriginalFailedSettlementRetriesOnlyExactPredecessor(t *testing.T) {
	for _, mode := range []string{"compare-and-put", "observe-committed"} {
		t.Run(mode, func(t *testing.T) {
			m := newManager(t)
			payload, command := checkpointNativePayload(t, t.TempDir())
			r := request("original-settlement-retry")
			r.Resources.CPU = .01
			r.Resources.Memory = 64
			l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sh", "-c", command}, CWD: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if l.Process == nil {
				t.Fatal("required original native identity unavailable")
			}
			payload.awaitReady(t)
			t.Cleanup(func() {
				if payload.writer != nil {
					_ = payload.writer.Close()
				}
			})
			m.mu.Lock()
			j := m.jobs[l.ID]
			row, err := readOriginalSettlementTuple(m.db, l.ID)
			if err != nil || j == nil {
				m.mu.Unlock()
				t.Fatal("original row unavailable", err)
			}
			expected := row.data
			_, err = m.db.Exec(`CREATE TRIGGER original_completion_refusal BEFORE UPDATE OF data ON leases WHEN NEW.id='` + l.ID + `' BEGIN SELECT RAISE(ABORT,'original completion journal refusal'); END`)
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				m.mu.Lock()
				_, _ = m.db.Exec(`DROP TRIGGER IF EXISTS original_completion_refusal`)
				if m.jobs[l.ID] == j && j.failedSettlement != nil {
					_, _ = m.db.Exec(`UPDATE leases SET data=?,request_key=?,run=? WHERE id=?`, expected, settlementSQLText(row.key), settlementSQLText(row.run), l.ID)
				}
				m.mu.Unlock()
			})
			payload.finish(t)
			awaitTransportSignal(t, j.done, "original native settlement attempt did not return")
			m.mu.Lock()
			d := j.failedSettlement
			if d == nil || d.failure == nil || m.jobs[l.ID] != j || !j.cpuExited.Load() || !d.reaped || !d.nativeSafe || !strings.Contains(d.failure.Error(), "original completion journal refusal") || d.expected != expected {
				m.mu.Unlock()
				t.Fatal("failed ACK erased original completion evidence")
			}
			_, err = m.db.Exec(`DROP TRIGGER original_completion_refusal`)
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			foreign, finishForeign := endingFailureForeignGroup(t)
			// All controls run while an unrelated original group stays live. The
			// exact predecessor is restored after every refusal; no new job is run.
			physicalRefusals := func(phase string) {
				t.Helper()
				originalData := expected
				if phase == "candidate" {
					originalData = d.candidate
				}
				for _, change := range []struct {
					name, sql string
					value     any
				}{
					{"request-key", `UPDATE leases SET request_key=? WHERE id=?`, "different-original-key"},
					{"request-key-blob", `UPDATE leases SET request_key=? WHERE id=?`, []byte(row.key.String)},
					{"request-key-null", `UPDATE leases SET request_key=? WHERE id=?`, nil},
					{"run-bytes", `UPDATE leases SET run=? WHERE id=?`, row.run.String + " "},
					{"run-blob", `UPDATE leases SET run=? WHERE id=?`, []byte(row.run.String)},
					{"run-null", `UPDATE leases SET run=? WHERE id=?`, nil},
					{"data-blob", `UPDATE leases SET data=? WHERE id=?`, []byte(originalData)},
					{"oversized-key", `UPDATE leases SET request_key=CAST(zeroblob(?) AS TEXT) WHERE id=?`, int64(129)},
					{"oversized-run", `UPDATE leases SET run=CAST(zeroblob(?) AS TEXT) WHERE id=?`, int64(originalSettlementRecordBytes + 1)},
					{"oversized-data", `UPDATE leases SET data=CAST(zeroblob(?) AS TEXT) WHERE id=?`, int64(originalSettlementRecordBytes + 1)},
				} {
					t.Run(phase+"/"+change.name, func(t *testing.T) {
						m.mu.Lock()
						_, mutateErr := m.db.Exec(change.sql, change.value, l.ID)
						var before, after int64
						_ = m.db.QueryRow(`SELECT total_changes()`).Scan(&before)
						retryErr := m.commitOriginalJobSettlement(l.ID, d)
						_ = m.db.QueryRow(`SELECT total_changes()`).Scan(&after)
						retained := m.jobs[l.ID] == j && j.failedSettlement == d
						data := expected
						if phase == "candidate" {
							data = d.candidate
						}
						_, restoreErr := m.db.Exec(`UPDATE leases SET data=?,request_key=?,run=? WHERE id=?`, data, settlementSQLText(row.key), settlementSQLText(row.run), l.ID)
						m.mu.Unlock()
						if mutateErr != nil || retryErr == nil || restoreErr != nil || before != after || !retained {
							t.Fatal("physical tuple refusal changed original debt", mutateErr, retryErr, restoreErr, before, after, retained)
						}
					})
				}
			}
			physicalRefusals("predecessor")
			changed := l
			changed.Request.Reason = "changed after native completion"
			changed.PID = foreign
			identity := *l.Process
			identity.PID, identity.PGID = foreign, foreign
			changed.Process = &identity
			altered, _ := json.Marshal(changed)
			m.mu.Lock()
			_, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(altered), l.ID)
			if err == nil {
				err = m.reconcileLeaseExpiries()
			}
			var after string
			_ = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&after)
			m.mu.Unlock()
			if err == nil || after != string(altered) {
				t.Fatal("failed completion adopted a changed predecessor", err)
			}
			s, err := m.Status()
			if err != nil || s.Reserved.CPU != r.Resources.CPU || s.Reserved.Memory != r.Resources.Memory {
				t.Fatal("altered predecessor received capacity credit", s.Reserved, err)
			}
			m.mu.Lock()
			_, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, expected, l.ID)
			if err == nil && mode == "observe-committed" {
				// Commit through the real exact-compare writer but leave the old
				// failure on the original job, modelling a lost success ACK.
				err = m.commitOriginalJobSettlement(l.ID, d)
			}
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "observe-committed" {
				physicalRefusals("candidate")
				// Earlier unrelated lifecycle events do not stand in for, or
				// conflict with, the original completion transition's event.
				m.mu.Lock()
				_, err = m.db.Exec(`INSERT INTO events(type,entity,at,data) VALUES('lease.notice',?,?,?)`, l.ID, d.eventAt, `{}`)
				m.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				for _, change := range []string{"missing", "payload", "time", "blob", "duplicate", "window"} {
					t.Run("event/"+change, func(t *testing.T) {
						m.mu.Lock()
						var extra int64
						switch change {
						case "missing":
							_, err = m.db.Exec(`DELETE FROM events WHERE sequence=?`, d.eventSequence)
						case "payload":
							_, err = m.db.Exec(`UPDATE events SET data='{}' WHERE sequence=?`, d.eventSequence)
						case "time":
							_, err = m.db.Exec(`UPDATE events SET at='different' WHERE sequence=?`, d.eventSequence)
						case "blob":
							_, err = m.db.Exec(`UPDATE events SET data=CAST(data AS BLOB) WHERE sequence=?`, d.eventSequence)
						case "duplicate":
							result, e := m.db.Exec(`INSERT INTO events(type,entity,at,data) VALUES(?,?,?,?)`, d.kind, l.ID, d.eventAt, d.candidate)
							err = e
							if err == nil {
								extra, err = result.LastInsertId()
							}
						case "window":
							extra = d.eventSequence + originalSettlementEventSpan
							_, err = m.db.Exec(`INSERT INTO events(sequence,type,entity,at,data) VALUES(?,'lease.notice',?,?,?)`, extra, l.ID, d.eventAt, `{}`)
						}
						retryErr := m.commitOriginalJobSettlement(l.ID, d)
						retained := m.jobs[l.ID] == j && j.failedSettlement == d
						if extra > 0 {
							_, _ = m.db.Exec(`DELETE FROM events WHERE sequence=?`, extra)
						}
						_, restoreErr := m.db.Exec(`INSERT INTO events(sequence,type,entity,at,data) VALUES(?,?,?,?,?) ON CONFLICT(sequence) DO UPDATE SET type=excluded.type,entity=excluded.entity,at=excluded.at,data=excluded.data`, d.eventSequence, d.kind, l.ID, d.eventAt, d.candidate)
						m.mu.Unlock()
						if err != nil || retryErr == nil || restoreErr != nil || !retained {
							t.Fatal("lost ACK accepted missing/changed/duplicate evidence", err, retryErr, restoreErr, retained)
						}
					})
				}
			}
			finishForeign() // Own successful exit rules out stored-PID signals.
			m.mu.Lock()
			if err == nil {
				err = m.reconcileLeaseExpiries()
			}
			final, getErr := m.get(l.ID)
			retained := m.jobs[l.ID] != nil || j.failedSettlement != nil
			m.mu.Unlock()
			if err != nil || getErr != nil || retained || final.State != "terminal" || final.Ended == nil || final.ExitCode == nil || *final.ExitCode != 0 ||
				!reflect.DeepEqual(final.Clock, l.Clock) || !reflect.DeepEqual(final.Expires, l.Expires) {
				t.Fatal("exact original completion did not recover without rebasing", err, getErr, final)
			}
			if j.childOwner.Active() || j.cmd.ProcessState == nil || j.cmd.ProcessState.ExitCode() != 0 {
				t.Fatal("completion retry replaced original native join")
			}
			if data, e := os.ReadFile(payload.ready); e != nil || string(data) != "ready\n" {
				t.Fatal("completion retry replayed native work", e)
			}
			events, e := m.Events(0)
			count := 0
			for _, event := range events {
				if event.Entity == l.ID && event.Type == d.kind {
					count++
				}
			}
			if e != nil || count != 1 {
				t.Fatal("completion ACK recovery duplicated final event", e, count)
			}
		})
	}
}

func TestCloseReportsOriginalSettlementFailureCreatedDuringJoin(t *testing.T) {
	for _, mode := range []string{"terminal-row", "completion-event"} {
		t.Run(mode, func(t *testing.T) {
			m, err := Open(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
			if err != nil {
				t.Fatal(err)
			}
			closed := false
			t.Cleanup(func() {
				if !closed {
					_ = m.Close()
				}
			})
			payload, command := checkpointNativePayload(t, t.TempDir())
			r := request("close-original-settlement")
			r.Resources.CPU = .01
			r.Resources.Memory = 64
			awaitNativeFixtureHeadroom(t, m, r.Resources)
			l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sh", "-c", command}, CWD: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			payload.awaitReady(t)
			t.Cleanup(func() {
				if payload.writer != nil {
					_ = payload.writer.Close()
				}
			})
			reader, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(m.home, "state.db"))+"?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reader.Close() })
			m.mu.Lock()
			j := m.jobs[l.ID]
			if j == nil || j.failedSettlement != nil || !j.childOwner.Active() {
				m.mu.Unlock()
				t.Fatal("Close fixture did not start with a live original and no debt")
			}
			if mode == "terminal-row" {
				_, err = m.db.Exec(`CREATE TRIGGER close_original_terminal_refusal BEFORE UPDATE OF data ON leases WHEN NEW.id='` + l.ID + `' AND json_extract(NEW.data,'$.state')='terminal' BEGIN SELECT RAISE(ABORT,'close original terminal ACK refusal'); END`)
			} else {
				_, err = m.db.Exec(`CREATE TRIGGER close_original_event_refusal BEFORE INSERT ON events WHEN NEW.entity='` + l.ID + `' AND NEW.type='lease.cancelled' BEGIN SELECT RAISE(ABORT,'close original terminal ACK refusal'); END`)
			}
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			closeErr := m.Close()
			closed = true
			if closeErr == nil || !strings.Contains(closeErr.Error(), "close original terminal ACK refusal") {
				t.Fatal("Close hid a final ACK failure first recorded during its join", closeErr)
			}
			checkpointNativeJoined(t, j, true)
			awaitTransportSignal(t, j.done, "Close returned before original finalization joined")
			m.mu.Lock()
			d := j.failedSettlement
			retained := m.jobs[l.ID] == j && d != nil && d.failure != nil
			m.mu.Unlock()
			if !retained {
				t.Fatal("Close erased original failed settlement evidence")
			}
			row, err := readOriginalSettlementTuple(reader, l.ID)
			var final model.Lease
			if err == nil {
				err = json.Unmarshal([]byte(row.data), &final)
			}
			if err != nil || row.data != d.expected || final.State != "ending" || final.Ended != nil || !consuming(final.State) || final.Request.Resources != r.Resources || !reflect.DeepEqual(final.Clock, l.Clock) || !reflect.DeepEqual(final.Expires, l.Expires) {
				t.Fatal("failed Close ACK lost the original durable charge/deadline", err, final)
			}
			var ending, completed int
			if err = reader.QueryRow(`SELECT count(*) FROM events WHERE entity=? AND type='lease.ending'`, l.ID).Scan(&ending); err != nil {
				t.Fatal(err)
			}
			if err = reader.QueryRow(`SELECT count(*) FROM events WHERE entity=? AND type='lease.cancelled'`, l.ID).Scan(&completed); err != nil {
				t.Fatal(err)
			}
			if ending != 1 || completed != 0 {
				t.Fatal("fixture did not ACK ending before refusing terminal completion", ending, completed)
			}
			if data, e := os.ReadFile(payload.ready); e != nil || string(data) != "ready\n" {
				t.Fatal("Close replayed original work", e)
			}
		})
	}
}

func TestOriginalNativeFinalizerRejectsChangedTupleBeforeEffects(t *testing.T) {
	for _, mode := range []string{"physical-id", "body-id", "host", "owner", "request", "native", "deadline", "ports", "request-key", "run"} {
		t.Run(mode, func(t *testing.T) {
			m := newManager(t)
			foreign, err := m.Acquire(clockRequest("foreign-finalizer-obligation", time.Minute), nil)
			if err != nil {
				t.Fatal(err)
			}
			var foreignRaw string
			if err = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, foreign.ID).Scan(&foreignRaw); err != nil {
				t.Fatal(err)
			}
			foreignPID, finishForeign := endingFailureForeignGroup(t)
			payload, command := checkpointNativePayload(t, t.TempDir())
			r := request("finalizer-original")
			r.Resources.CPU = .01
			r.Resources.Memory = 64
			l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sh", "-c", command}, CWD: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			payload.awaitReady(t)
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			j := m.jobs[l.ID]
			original, err := readOriginalSettlementTuple(m.db, l.ID)
			m.portSockets[l.ID] = []*ports.Socket{{File: writer}}
			m.mu.Unlock()
			if err != nil || j == nil || l.Process == nil {
				t.Fatal("original native tuple unavailable", err)
			}
			physicalID := l.ID
			t.Cleanup(func() {
				m.mu.Lock()
				if physicalID != l.ID {
					_, _ = m.db.Exec(`UPDATE leases SET id=? WHERE id=?`, l.ID, physicalID)
				}
				_, _ = m.db.Exec(`UPDATE leases SET data=?,request_key=?,run=? WHERE id=?`, original.data, settlementSQLText(original.key), settlementSQLText(original.run), l.ID)
				delete(m.portSockets, l.ID)
				m.mu.Unlock()
				_ = reader.Close()
				_ = writer.Close()
				_ = payload.writer.Close()
			})
			changed := l
			switch mode {
			case "body-id":
				changed.ID = foreign.ID
			case "host":
				changed.Host = "foreign-host"
			case "owner":
				changed.Owner = "foreign-owner"
			case "request":
				changed.Request.Reason = "different original request"
			case "native":
				identity := *l.Process
				identity.PID = foreignPID
				identity.PGID = foreignPID
				changed.Process = &identity
				changed.PID = foreignPID
			case "deadline":
				deadline := *l.Clock
				deadline.End++
				changed.Clock = &deadline
			case "ports":
				changed.Ports = []model.PortBinding{{PortClaim: model.PortClaim{Network: "invalid"}}}
			}
			changedRaw, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			_, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(changedRaw), l.ID)
			if err == nil {
				switch mode {
				case "physical-id":
					physicalID = "different-physical-original"
					_, err = m.db.Exec(`UPDATE leases SET id=? WHERE id=?`, physicalID, l.ID)
				case "request-key":
					_, err = m.db.Exec(`UPDATE leases SET request_key='different-key' WHERE id=?`, l.ID)
				case "run":
					_, err = m.db.Exec(`UPDATE leases SET run=run||' ' WHERE id=?`, l.ID)
				}
			}
			var before int64
			_ = m.db.QueryRow(`SELECT total_changes()`).Scan(&before)
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			payload.finish(t)
			awaitTransportSignal(t, j.done, "original finalizer did not return")
			checkpointNativeJoined(t, j, false)
			m.mu.Lock()
			var after int64
			var observed, foreignAfter string
			_ = m.db.QueryRow(`SELECT total_changes()`).Scan(&after)
			readErr := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, physicalID).Scan(&observed)
			foreignErr := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, foreign.ID).Scan(&foreignAfter)
			retained := m.jobs[l.ID] == j && j.failedSettlement == nil
			m.mu.Unlock()
			if before != after || readErr != nil || foreignErr != nil || observed != string(changedRaw) || foreignAfter != foreignRaw || !retained {
				t.Fatal("changed tuple redirected initial finalization", before, after, readErr, foreignErr, retained)
			}
			if _, err = writer.Write([]byte("x")); err != nil {
				t.Fatal("changed port metadata closed an original FD before binding", err)
			}
			finishForeign()
		})
	}
}

// These are bounded SQL reader controls, not fabricated native completion.
func TestOriginalSettlementTupleReadBounds(t *testing.T) {
	m := newManager(t)
	r := RunRequest{Request: request("bounded-original-run"), Command: []string{""}, CWD: t.TempDir()}
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Command[0] = strings.Repeat("x", originalSettlementRecordBytes-len(encoded))
	encoded, err = json.Marshal(r)
	if err != nil || len(encoded) != originalSettlementRecordBytes {
		t.Fatal("exact run boundary unavailable", err, len(encoded))
	}
	r.originalRun = string(encoded)
	id := "bounded-settlement-tuple"
	l := model.Lease{ID: id, Request: r.Request}
	data := strings.Repeat("x", originalSettlementRecordBytes)
	_, err = m.db.Exec(`INSERT INTO leases(id,request_key,data,run) VALUES(?,?,?,?)`, id, r.Key, data, string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.db.Exec(`DELETE FROM leases WHERE id=?`, id) })
	row, err := readOriginalSettlementTuple(m.db, id)
	if err != nil || row.data != data || row.run.String != string(encoded) {
		t.Fatal("supported exact tuple boundary refused", err)
	}
	if _, err = m.captureOriginalSettlementLaunch(l, r); err != nil {
		t.Fatal("exact original run boundary refused", err)
	}
	for _, c := range []struct {
		name, query string
		value       any
	}{
		{"data-overflow", `UPDATE leases SET data=CAST(zeroblob(?) AS TEXT) WHERE id=?`, originalSettlementRecordBytes + 1},
		{"run-overflow", `UPDATE leases SET run=CAST(zeroblob(?) AS TEXT) WHERE id=?`, originalSettlementRecordBytes + 1},
		{"key-overflow", `UPDATE leases SET request_key=CAST(zeroblob(?) AS TEXT) WHERE id=?`, 129},
		{"data-blob", `UPDATE leases SET data=? WHERE id=?`, []byte("{}")},
		{"run-blob", `UPDATE leases SET run=? WHERE id=?`, []byte("{}")},
		{"key-blob", `UPDATE leases SET request_key=? WHERE id=?`, []byte(r.Key)},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, e := m.db.Exec(c.query, c.value, id)
			if e != nil {
				t.Fatal(e)
			}
			got, readErr := readOriginalSettlementTuple(m.db, id)
			_, launchErr := m.captureOriginalSettlementLaunch(l, r)
			_, restoreErr := m.db.Exec(`UPDATE leases SET request_key=?,data=?,run=? WHERE id=?`, r.Key, data, string(encoded), id)
			if readErr == nil || launchErr == nil || got != (originalSettlementTuple{}) || restoreErr != nil {
				t.Fatal("unsupported tuple became retained evidence", readErr, launchErr, restoreErr)
			}
		})
	}
	// SQL NULL is distinct from an empty TEXT key. Saved native runs cannot
	// be NULL, while a truly absent request key is an original supported tuple.
	_, err = m.db.Exec(`UPDATE leases SET request_key=NULL WHERE id=?`, id)
	if err != nil {
		t.Fatal(err)
	}
	r.Key = ""
	l.Request = r.Request
	encoded, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	r.originalRun = string(encoded)
	_, err = m.db.Exec(`UPDATE leases SET run=? WHERE id=?`, string(encoded), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.captureOriginalSettlementLaunch(l, r); err != nil {
		t.Fatal("original NULL key refused", err)
	}
	_, err = m.db.Exec(`UPDATE leases SET request_key='' WHERE id=?`, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.captureOriginalSettlementLaunch(l, r); err == nil {
		t.Fatal("empty TEXT key adopted as original NULL")
	}
}

func TestOriginalSettlementRefusalDoesNotStarveExpiredNativeSibling(t *testing.T) {
	x := newCheckpointNativePair(t)
	m := x.m
	m.mu.Lock()
	_, err := m.db.Exec(`CREATE TRIGGER original_completion_refusal BEFORE UPDATE OF data ON leases WHEN NEW.id='` + x.generic.ID + `' BEGIN SELECT RAISE(ABORT,'original completion journal refusal'); END`)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		m.mu.Lock()
		_, _ = m.db.Exec(`DROP TRIGGER IF EXISTS original_completion_refusal`)
		m.mu.Unlock()
	})
	x.genericCmd.finish(t)
	awaitTransportSignal(t, x.genericJob.done, "original generic completion attempt did not return")
	m.mu.Lock()
	d := x.genericJob.failedSettlement
	m.mu.Unlock()
	if d == nil || d.failure == nil {
		t.Fatal("refused original settlement was not retained")
	}
	x.expire(t)
	err = x.reconcile() // Debt CAS fails, but must still cancel the original SSH sibling.
	if err == nil || !strings.Contains(err.Error(), "original completion journal refusal") {
		t.Fatal("original debt failure hidden", err)
	}
	awaitTransportSignal(t, x.runtime.ctx.Done(), "one failed debt starved proven-expired original sibling")
	checkpointNativeJoined(t, x.runtime.leaseHold.job, true)
	x.joinTransport(t)
	m.mu.Lock()
	retained := m.jobs[x.generic.ID] == x.genericJob && x.genericJob.failedSettlement == d
	_, err = m.db.Exec(`DROP TRIGGER original_completion_refusal`)
	m.mu.Unlock()
	if !retained || err != nil {
		t.Fatal("sibling cleanup erased original debt", retained, err)
	}
	if err = x.reconcile(); err != nil {
		t.Fatal("restored original settlement failed", err)
	}
	x.finishForeign()
}

// Exercise the profile cancellation selector with an actual original native
// group and Wait. This is not a Java/Gradle-provider acceptance fixture: only
// its immutable profile selector is supplied privately after generic admission.
func TestRetainedGradleSelectorReadFailureJoinsOriginalNativeChild(t *testing.T) {
	m := newManager(t)
	command, payload := checkpointNativePayload(t, t.TempDir())
	r := request("retained-gradle-original")
	r.Resources.CPU = .01
	l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sh", "-c", payload}, CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	command.awaitReady(t)
	t.Cleanup(func() {
		if command.writer != nil {
			_ = command.writer.Close()
		}
	})
	sentinel, sentinelPayload := checkpointNativePayload(t, t.TempDir())
	sentinelRequest := r
	sentinelRequest.Key = "retained-unrelated-original"
	sentinelLease, sentinelErr := m.Acquire(sentinelRequest, &RunRequest{Request: sentinelRequest, Command: []string{"/bin/sh", "-c", sentinelPayload}, CWD: t.TempDir()})
	if sentinelErr != nil {
		t.Fatal(sentinelErr)
	}
	sentinel.awaitReady(t)
	t.Cleanup(func() {
		if sentinel.writer != nil {
			_ = sentinel.writer.Close()
		}
	})
	m.mu.Lock()
	j := m.jobs[l.ID]
	if j == nil || j.childOwner == nil {
		m.mu.Unlock()
		t.Fatal("missing original native owner")
	}
	sentinelJob := m.jobs[sentinelLease.ID]
	original := l
	raw, _ := json.Marshal(original)
	l.Request.Profile = "gradle-service"
	j.leaseRequest = l.Request
	if err = m.put(l, "fixture.retained_gradle_selector"); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	_, err = m.db.Exec(`UPDATE leases SET data='{' WHERE id=?`, l.ID)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.End(l.ID, "cancelled")
	if err == nil {
		t.Fatal("failed read unexpectedly mutated lease")
	}
	// Restore only the private fixture's original row so its sole waiter can
	// retain/settle through ordinary code; cancellation was already requested.
	m.mu.Lock()
	j.leaseRequest = original.Request
	_, restoreErr := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), l.ID)
	sentinelCancelled := sentinelJob == nil || sentinelJob.originalCancellationRequested
	m.mu.Unlock()
	if restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if sentinelCancelled {
		t.Fatal("unrelated original received cancellation")
	}
	select {
	case <-j.nativeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("original cancelled profile did not reach native Wait")
	}
	if j.childOwner.Active() {
		t.Fatal("native Wait failed to retire original controls")
	}
	if data, e := os.ReadFile(command.ready); e != nil || string(data) != "ready\n" {
		t.Fatal("original workload was replayed", e)
	}
	sentinel.finish(t)
	select {
	case <-sentinelJob.nativeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("unrelated original did not join its normal finish")
	}
}

func TestCargoIndependentElapsedWatcherCancelsCompilingOriginalWithoutCheckpoint(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil && os.Getenv("ARM_TEST_TOOLCHAIN_BIN") == "" {
		t.Fatal("required native Cargo fixture tool unavailable", err)
	}
	m := newManager(t)
	t.Setenv("ARM_CARGO_TEST_DROP", "started")
	r, run, ready, release := realCargoTestProject(t, m, true)
	run.Request = r
	l, err := m.Acquire(r, &run)
	if err != nil {
		t.Fatal(err)
	}
	// A failed test may release its own build-script gate, never substitute a
	// PID kill or second Wait for the original Cargo worker's cleanup.
	t.Cleanup(func() { _ = os.WriteFile(release, []byte("release"), 0600) })
	cargoWait(t, func() bool { _, e := os.Stat(ready); return e == nil })
	m.mu.Lock()
	w := m.cargoWorkers[l.ID]
	live, err := m.get(l.ID)
	if err != nil || w == nil || live.Process == nil {
		m.mu.Unlock()
		t.Fatal("compiling original not proven", err)
	}
	j := m.jobs[l.ID]
	if j == nil || j.childOwner == nil {
		m.mu.Unlock()
		t.Fatal("original helper not registered")
	}
	var before string
	_ = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&before)
	sample, err := m.readLeaseClock()
	if err == nil {
		_, err = m.db.Exec(`CREATE TRIGGER retained_cargo_checkpoint_refusal BEFORE UPDATE ON metadata WHEN NEW.key='lease_clock' BEGIN SELECT RAISE(ABORT,'retained Cargo checkpoint refusal'); END`)
	}
	if err == nil {
		_, err = m.db.Exec(`CREATE TRIGGER retained_cargo_final_refusal BEFORE INSERT ON events WHEN NEW.type LIKE 'cargo.%' BEGIN SELECT RAISE(ABORT,'retained Cargo final refusal'); END`)
	}
	if err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	sample.Tick = live.Clock.End // Native elapsed model advances; wall and Go timers do not.
	m.leaseClockRead = func() (leaseclock.Sample, error) { return sample, nil }
	m.mu.Unlock()
	t.Cleanup(func() {
		m.mu.Lock()
		_, _ = m.db.Exec(`DROP TRIGGER IF EXISTS retained_cargo_checkpoint_refusal`)
		_, _ = m.db.Exec(`DROP TRIGGER IF EXISTS retained_cargo_final_refusal`)
		m.mu.Unlock()
	})
	// Do not call tick/reconcile or start Manager sampling here. Only the
	// independent watcher can cancel the blocked original helper protocol.
	select {
	case <-w.done:
	case <-time.After(8 * time.Second):
		t.Fatal("independent elapsed watcher left original compilation alive")
	}
	if j.childOwner.Active() || !j.cpuExited.Load() {
		t.Fatal("worker return lacks original native retirement/Wait")
	}
	m.mu.Lock()
	var after string
	_ = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&after)
	m.mu.Unlock()
	if before != after {
		t.Fatal("failed final journal credited cancelled compilation")
	}
	if data, e := os.ReadFile(ready); e != nil || strings.TrimSpace(string(data)) != "ready" {
		t.Fatal("compile fixture lost its build-script marker", e)
	}
}
