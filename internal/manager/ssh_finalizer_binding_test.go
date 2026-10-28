package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/model"
)

type sshFinalizerPhysicalRow struct {
	data, key, run   []byte
	keyType, runType string
}

// These fixtures contain only small owned rows. Preserve SQL NULL/TEXT/BLOB
// distinctions so a byte-identical type replacement cannot pass as unchanged.
// Manager.mu is held; this helper supplies no production completion authority.
func sshFinalizerRow(m *Manager, id string) (sshFinalizerPhysicalRow, error) {
	var row sshFinalizerPhysicalRow
	err := m.db.QueryRow(`SELECT CAST(data AS BLOB),typeof(request_key),CAST(request_key AS BLOB),typeof(run),CAST(run AS BLOB) FROM leases WHERE id=?`, id).Scan(&row.data, &row.keyType, &row.key, &row.runType, &row.run)
	return row, err
}

func TestSSHNativeFinalizerRejectsChangedTupleBeforeEffects(t *testing.T) {
	for _, mode := range []string{"foreign-body", "request-key", "run-bytes", "run-blob", "ports", "deadline", "native-record-erased-after-ack"} {
		t.Run(mode, func(t *testing.T) {
			x := newCheckpointNativePair(t)
			foreign, err := x.m.Acquire(portRequest("foreign-held-finalizer-port"), nil)
			if err != nil || foreign.State != "active" || len(foreign.Ports) != 1 || foreign.Ports[0].State != "held" {
				t.Fatal("real foreign held port unavailable", foreign, err)
			}
			x.m.mu.Lock()
			original, err := readOriginalSettlementTuple(x.m.db, x.ssh.ID)
			foreignBefore, foreignErr := sshFinalizerRow(x.m, foreign.ID)
			sockets := x.m.portSockets[foreign.ID]
			x.m.mu.Unlock()
			if err != nil || foreignErr != nil || len(sockets) != 1 || sockets[0].File == nil {
				t.Fatal("original tuples or real foreign descriptor unavailable", err, foreignErr)
			}
			foreignSocket, foreignFile := sockets[0], sockets[0].File
			// Restoration is failure cleanup, after all acceptance observations and real
			// original joins. It is never a successful settlement witness.
			t.Cleanup(func() {
				x.m.mu.Lock()
				_, _ = x.m.db.Exec(`UPDATE leases SET data=?,request_key=?,run=? WHERE id=?`, original.data, settlementSQLText(original.key), settlementSQLText(original.run), x.ssh.ID)
				x.m.mu.Unlock()
				_, _ = x.m.End(foreign.ID, "released")
			})
			var changed model.Lease
			if err = json.Unmarshal([]byte(original.data), &changed); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "foreign-body":
				changed.ID = foreign.ID
				changed.Request = foreign.Request
				// Keep the original CPU/memory promise charged in the deliberately corrupt
				// row while redirecting its body identity/request/ports to the foreign hold.
				changed.Request.Resources = x.ssh.Request.Resources
				changed.Ports = append([]model.PortBinding(nil), foreign.Ports...)
			case "ports":
				changed.Ports = append([]model.PortBinding(nil), foreign.Ports...)
			case "deadline":
				d := *changed.Clock
				d.End++
				d.Wall = d.Wall.Add(time.Nanosecond)
				changed.Clock = &d
				expiry := changed.Expires.Add(time.Nanosecond)
				changed.Expires = &expiry
			case "native-record-erased-after-ack":
				changed.PID, changed.Process = 0, nil
			}
			raw, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			x.m.mu.Lock()
			_, err = x.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), x.ssh.ID)
			if err == nil {
				switch mode {
				case "request-key":
					_, err = x.m.db.Exec(`UPDATE leases SET request_key='replaced-physical-ssh-key' WHERE id=?`, x.ssh.ID)
				case "run-bytes":
					_, err = x.m.db.Exec(`UPDATE leases SET run=run||' ' WHERE id=?`, x.ssh.ID)
				case "run-blob":
					_, err = x.m.db.Exec(`UPDATE leases SET run=CAST(run AS BLOB) WHERE id=?`, x.ssh.ID)
				}
			}
			before, readErr := sshFinalizerRow(x.m, x.ssh.ID)
			var eventsBefore int
			eventErr := x.m.db.QueryRow(`SELECT count(*) FROM events WHERE entity IN (?,?) AND type LIKE 'lease.%'`, x.ssh.ID, foreign.ID).Scan(&eventsBefore)
			x.m.mu.Unlock()
			if err != nil || readErr != nil || eventErr != nil {
				t.Fatal(err, readErr, eventErr)
			}
			x.sshCmd.finish(t) // Actual original normal exit, never a signal from row PID.
			j := x.runtime.leaseHold.job
			checkpointNativeJoined(t, j, false)
			awaitTransportSignal(t, x.runtime.leaseHold.ready, "SSH finalizer did not reach its real ready barrier")
			if b, e := os.ReadFile(x.sshCmd.terms); !errors.Is(e, os.ErrNotExist) || len(b) != 0 {
				t.Fatal("normal original exit received an unsolicited signal", e, string(b))
			}
			assertUnchanged := func() {
				t.Helper()
				x.m.mu.Lock()
				after, e := sshFinalizerRow(x.m, x.ssh.ID)
				foreignAfter, fe := sshFinalizerRow(x.m, foreign.ID)
				current := x.m.portSockets[foreign.ID]
				retained := x.m.jobs[x.ssh.ID] == j && x.m.sshTransports[x.ssh.ID] == x.runtime.leaseHold && j.failedSettlement == nil
				var eventCount int
				ee := x.m.db.QueryRow(`SELECT count(*) FROM events WHERE entity IN (?,?) AND type LIKE 'lease.%'`, x.ssh.ID, foreign.ID).Scan(&eventCount)
				holdErr := x.runtime.leaseHold.err
				noCandidate := x.runtime.leaseHold.candidate.ID == ""
				x.m.mu.Unlock()
				if e != nil || fe != nil || ee != nil || !reflect.DeepEqual(after, before) || !reflect.DeepEqual(foreignAfter, foreignBefore) || eventCount != eventsBefore || !retained || !noCandidate || errorCode(holdErr) != "ssh_owner_changed" {
					t.Fatal("SSH completion changed a replaced or foreign obligation", e, fe, ee, eventCount, eventsBefore, retained, noCandidate, holdErr)
				}
				if len(current) != 1 || current[0] != foreignSocket || current[0].File != foreignFile {
					t.Fatal("SSH finalizer redirected the foreign held socket map")
				}
				if _, e = foreignFile.Stat(); e != nil {
					t.Fatal("SSH finalizer closed the real foreign held descriptor", e)
				}
				var charged model.Lease
				if e = json.Unmarshal(after.data, &charged); e != nil || !consuming(charged.State) || charged.Ended != nil || charged.Request.Resources != x.ssh.Request.Resources {
					t.Fatal("refused original SSH lost its resource promise", e, charged)
				}
				var held model.Lease
				if e = json.Unmarshal(foreignAfter.data, &held); e != nil || !consuming(held.State) || held.Ended != nil || len(held.Ports) != 1 || held.Ports[0].State != "held" {
					t.Fatal("foreign port obligation was freed", e, held)
				}
			}
			assertUnchanged()
			select {
			case <-j.done:
				t.Fatal("native ready forged actual original IO join")
			default:
			}
			x.joinTransport(t)
			assertUnchanged()
			x.genericCmd.finish(t)
			awaitTransportSignal(t, x.genericJob.done, "unrelated original generic completion did not return")
			checkpointNativeJoined(t, x.genericJob, false)
			x.finishForeign()
		})
	}
}

func TestSSHNativeFinalizerAcceptsLatestAcknowledgedRenewal(t *testing.T) {
	x := newCheckpointNativePair(t)
	limit, ok := x.runtime.ctx.Deadline()
	room := limit.Sub(*x.ssh.Expires)
	if !ok || room <= time.Nanosecond {
		t.Fatal("live authorization room required for valid renewal control")
	}
	advance := min(time.Millisecond, room/2)
	x.m.mu.Lock()
	x.clock.sample.Tick += int64(time.Second)
	x.clock.sample.Wall = x.clock.sample.Wall.Add(advance)
	x.m.mu.Unlock()
	awaitNativeFixtureHeadroom(t, x.m, model.Resources{CPU: .01, Memory: 1})
	renewed, err := x.m.Renew(x.ssh.ID, time.Minute)
	if err != nil || renewed.Expires == nil || reflect.DeepEqual(renewed.Clock, x.runtime.leaseHold.original.Clock) || renewed.Expires.Equal(*x.runtime.leaseHold.original.Expires) || !renewed.Expires.Before(limit) {
		t.Fatal("valid acknowledged renewal did not establish a changed original deadline", renewed, err)
	}
	x.sshCmd.finish(t)
	j := x.runtime.leaseHold.job
	checkpointNativeJoined(t, j, false)
	awaitTransportSignal(t, x.runtime.leaseHold.ready, "renewed original did not stage its native result")
	x.m.mu.Lock()
	staged, e := x.m.get(x.ssh.ID)
	holdErr := x.runtime.leaseHold.err
	x.m.mu.Unlock()
	if e != nil || holdErr != nil || staged.State != "ending" || staged.Ended != nil || staged.Expires == nil || !reflect.DeepEqual(staged.Clock, renewed.Clock) || !staged.Expires.Equal(*renewed.Expires) {
		t.Fatal("renewed original was refused or settled before real IO join", staged, e, holdErr)
	}
	select {
	case <-j.done:
		t.Fatal("renewed native exit forged original IO join")
	default:
	}
	x.joinTransport(t)
	x.m.mu.Lock()
	final, e := x.m.get(x.ssh.ID)
	registered := x.m.jobs[x.ssh.ID] != nil || x.m.sshTransports[x.ssh.ID] != nil
	x.m.mu.Unlock()
	if e != nil || final.State != "terminal" || final.Ended == nil || final.Expires == nil || registered || !reflect.DeepEqual(final.Clock, renewed.Clock) || !final.Expires.Equal(*renewed.Expires) {
		t.Fatal("positive original native/transport joins did not settle renewed lease", final, e, registered)
	}
	if b, e := os.ReadFile(x.sshCmd.ready); e != nil || string(b) != "ready\n" {
		t.Fatal("renewal replayed original work", e, string(b))
	}
	x.genericCmd.finish(t)
	awaitTransportSignal(t, x.genericJob.done, "unrelated generic did not complete normally")
	checkpointNativeJoined(t, x.genericJob, false)
	x.finishForeign()
}

// The pause follows the actual sole Wait, outside Manager.mu. It establishes
// the causal ordering without replacing any native or transport join witness.
func TestSSHFailedNativeGateAcceptsAcknowledgedCancellationBeforeFinalizer(t *testing.T) {
	for _, mode := range []string{"end", "cancel-request", "close"} {
		t.Run(mode, func(t *testing.T) {
			m, err := Open(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
			if err != nil {
				t.Fatal(err)
			}
			closed, closeStarted := false, false
			closeResult := make(chan error, 1)
			t.Cleanup(func() {
				if !closed && closeStarted {
					select {
					case <-closeResult:
					case <-time.After(5 * time.Second):
						t.Error("original Close did not finish during cleanup")
					}
				} else if !closed {
					_ = m.Close()
				}
			})
			entered, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			releaseFinalizer := func() { once.Do(func() { close(resume) }) }
			defer releaseFinalizer()
			m.sshBeforeFinalizer = func() { close(entered); <-resume }
			awaitNativeFixtureHeadroom(t, m, model.Resources{CPU: .5, Memory: 64})
			f, actual, inputs, releaseInput := sshSettlementFixtureWithManager(t, m, true)
			defer releaseInput()
			session, err := f.client.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			marker := filepath.Join(f.q.CWD, "never-executed")
			request := make(chan error, 1)
			go func() { request <- session.Start("touch " + sshTestQuote(marker)) }()
			r := awaitSSHRuntime(t, actual)
			held := awaitSSHInput(t, inputs)
			j, id := r.leaseHold.job, r.leaseHold.original.ID
			awaitTransportSignal(t, entered, "original sole Wait did not reach pre-finalizer barrier")
			checkpointNativeJoined(t, j, true)
			awaitTransportSignal(t, held.entered, "real failed-start input did not reach EOF")
			reader, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(m.home, "state.db"))+"?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			switch mode {
			case "end":
				_, err = m.End(id, "cancelled")
			case "cancel-request":
				err = m.CancelRequest(r.leaseHold.original.Request.Key)
			case "close":
				closeStarted = true
				go func() { closeResult <- m.Close() }()
			}
			if err != nil {
				t.Fatal("original lifecycle ending ACK failed", err)
			}
			deadline := time.Now().Add(5 * time.Second)
			var ending model.Lease
			var row originalSettlementTuple
			for {
				row, err = readOriginalSettlementTuple(reader, id)
				if err == nil {
					err = json.Unmarshal([]byte(row.data), &ending)
				}
				if err != nil {
					t.Fatal(err)
				}
				if ending.State == "ending" {
					break
				}
				if !time.Now().Before(deadline) {
					t.Fatal("lifecycle did not acknowledge ending before finalizer", ending)
				}
				time.Sleep(time.Millisecond)
			}
			m.mu.Lock()
			known := j.sshNoNative != nil && *j.sshNoNative == row
			retained := m.jobs[id] == j && m.sshTransports[id] == r.leaseHold && !r.leaseHold.staged && r.leaseHold.err == nil
			m.mu.Unlock()
			var started, endings int
			err = reader.QueryRow(`SELECT count(*) FILTER (WHERE type='lease.started'),count(*) FILTER (WHERE type='lease.ending') FROM events WHERE entity=?`, id).Scan(&started, &endings)
			if err != nil || !known || !retained || started != 0 || endings != 1 || ending.PID != 0 || ending.Process != nil || ending.Outcome != "cancelled" || ending.Ended != nil || !reflect.DeepEqual(ending.Clock, r.leaseHold.original.Clock) || !reflect.DeepEqual(ending.Expires, r.leaseHold.original.Expires) || ending.Request.Resources != r.leaseHold.original.Request.Resources {
				t.Fatal("ending did not preserve exact known no-native predecessor and charge", err, known, retained, started, endings, ending)
			}
			if _, err = os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("refused start ran requested work", err)
			}
			releaseFinalizer()
			awaitTransportSignal(t, r.leaseHold.ready, "real finalizer did not stage after ending ACK")
			assertSSHSettlementCharged(t, m, r)
			m.mu.Lock()
			holdErr := r.leaseHold.err
			m.mu.Unlock()
			if holdErr != nil {
				t.Fatal("acknowledged lifecycle predecessor was refused", holdErr)
			}
			select {
			case <-j.done:
				t.Fatal("ending plus native reap forged original IO join")
			default:
			}
			if mode == "close" {
				select {
				case err = <-closeResult:
					closed = true
					t.Fatal("Close returned before held original input joined", err)
				default:
				}
			}
			releaseInput()
			awaitTransportSignal(t, f.serverDone, "original server did not join")
			awaitTransportSignal(t, r.transportJoined, "actual original transport did not join")
			awaitTransportSignal(t, r.leaseHold.done, "original SSH obligation did not finish")
			awaitTransportSignal(t, j.done, "original job did not join")
			if mode == "close" {
				select {
				case err = <-closeResult:
					closed = true
				case <-time.After(5 * time.Second):
					t.Fatal("Close did not join original finalization")
				}
				if err != nil {
					t.Fatal("Close refused known ending predecessor", err)
				}
			}
			row, err = readOriginalSettlementTuple(reader, id)
			var final model.Lease
			if err == nil {
				err = json.Unmarshal([]byte(row.data), &final)
			}
			m.mu.Lock()
			registered := m.jobs[id] != nil || m.sshTransports[id] != nil
			holdErr = r.leaseHold.err
			m.mu.Unlock()
			if err != nil || holdErr != nil || registered || final.State != "terminal" || final.Ended == nil || !reflect.DeepEqual(final.Clock, ending.Clock) || !reflect.DeepEqual(final.Expires, ending.Expires) {
				t.Fatal("positive native and actual IO joins failed to settle", err, holdErr, registered, final)
			}
			var joined int
			err = reader.QueryRow(`SELECT count(*) FROM events WHERE entity=? AND type='lease.ssh_transport_joined'`, id).Scan(&joined)
			if err != nil || joined != 1 {
				t.Fatal("original final ACK duplicated or missing", err, joined)
			}
			if _, err = os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("settlement replayed refused work", err)
			}
			select {
			case err = <-request:
				if err == nil {
					t.Fatal("refused native gate accepted request")
				}
			case <-time.After(time.Second):
				t.Fatal("original refused request did not return")
			}
		})
	}
}
