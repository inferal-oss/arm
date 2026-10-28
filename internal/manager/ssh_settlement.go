package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"getarm.org/internal/model"
)

// An sshLeaseHold is a current in-process obligation, never recovered from a
// descriptor. Native readiness lets Serve finish; settlement additionally needs
// its positive connection/copy/callback JoinTransport witness.
type sshLeaseHold struct {
	expectedPID                             int
	expectedProcess                         *model.ProcessIdentity
	manager                                 *Manager
	runtime                                 *sshRuntime
	original                                model.Lease
	job                                     *job
	nativeStarted, nativeReaped, nativeSafe bool
	staged                                  bool
	ready                                   chan struct{}
	done                                    chan struct{}
	candidate                               model.Lease
	expected                                string
	err                                     error // Manager.mu; retained when final persistence/ownership is uncertain
}

// Manager.mu held. Publish before any fallible launch preparation owns IO.
func (m *Manager) bindSSHTransport(l model.Lease, r *sshRuntime) error {
	if m.sshTransports == nil {
		m.sshTransports = map[string]*sshLeaseHold{}
	}
	if m.sshTransports[l.ID] != nil || r.leaseHold != nil {
		return model.Err("ssh_owner_changed", "SSH runtime cannot be rebound or replayed")
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return err
	}
	var original model.Lease
	if err = json.Unmarshal(raw, &original); err != nil {
		return err
	}
	h := &sshLeaseHold{manager: m, runtime: r, original: original, expectedPID: original.PID, expectedProcess: original.Process, ready: make(chan struct{}), done: make(chan struct{})}
	r.leaseHold = h
	m.sshTransports[l.ID] = h
	m.wg.Go(func() {
		defer close(h.done)
		<-h.ready
		<-r.transportJoined
		m.mu.Lock()
		defer m.mu.Unlock()
		defer func() {
			if h.job != nil {
				close(h.job.done)
			}
		}()
		if h.err != nil {
			return
		}
		if m.sshTransports[l.ID] != h || r.leaseHold != h {
			h.err = model.Err("ssh_owner_changed", "original SSH transport hold changed")
			return
		}
		var current string
		if err := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&current); err != nil {
			h.err = err
			return
		}
		if current != h.expected {
			h.err = model.Err("ssh_owner_changed", "SSH staged lease changed before original transport joined")
			return
		}
		if h.job != nil && m.jobs[l.ID] != h.job {
			h.err = model.Err("ssh_owner_changed", "original SSH native job changed before transport joined")
			return
		}
		final := h.candidate
		if final.State == "terminal" {
			now := time.Now().UTC()
			final.Ended = &now
		}
		final.Revision++
		if err := m.put(final, "lease.ssh_transport_joined"); err != nil {
			h.err = err
			return
		}
		if h.job != nil {
			if h.nativeReaped {
				delete(m.jobs, l.ID)
			}
		}
		delete(m.sshTransports, l.ID)
	})
	return nil
}

// putOriginalSSHNoNativeEnding advances only the privately known predecessor of
// the original gated child whose start ACK failed. Manager.mu is held. The exact
// writer bytes become authority only after a successful comparison and Commit;
// an arbitrary PID-zero row or an unknown ACK can never create that authority.
func (m *Manager) putOriginalSSHNoNativeEnding(l model.Lease, data string, j *job) error {
	refuse := func() error {
		return model.Err("ssh_owner_changed", "original no-native SSH ending predecessor differs")
	}
	if j == nil || j.sshNoNative == nil || j.sshRuntime == nil || j.cmd == nil || j.cmd.Process == nil || j.cmd.Process.Pid <= 0 || j.childOwner == nil || m.jobs[l.ID] != j {
		return refuse()
	}
	h := j.sshRuntime.leaseHold
	prior := *j.sshNoNative
	if h == nil || h.manager != m || h.job != j || h.runtime != j.sshRuntime || m.sshTransports[l.ID] != h || !h.nativeStarted || h.nativeReaped || h.staged || h.err != nil || h.expectedPID != 0 || h.expectedProcess != nil || prior.id != l.ID || !originalSettlementLaunchMatches(prior, j.settlementLaunch) {
		return refuse()
	}
	var expected model.Lease
	if json.Unmarshal([]byte(prior.data), &expected) != nil || expected.State != "provisioning" || expected.PID != 0 || expected.Process != nil || expected.ID != h.original.ID || expected.Host != j.leaseHost || expected.Owner != j.leaseOwner || !reflect.DeepEqual(expected.Request, j.leaseRequest) || !reflect.DeepEqual(expected.Clock, j.leaseDeadline) || !reflect.DeepEqual(expected.Expires, j.leaseExpires) || l.State != "ending" || l.Revision != expected.Revision+1 {
		return refuse()
	}
	// get may attach an observation; only lifecycle fields and that observation
	// may differ. In particular ports, request, native identity and deadline stay.
	expected.State, expected.Outcome, expected.Revision, expected.Usage = l.State, l.Outcome, l.Revision, l.Usage
	if !reflect.DeepEqual(expected, l) || len(data) < 1 || len(data) > originalSettlementRecordBytes || len(prior.id)+len(data)+len(prior.key.String)+len(prior.run.String) > originalSettlementTupleBytes {
		return refuse()
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := readOriginalSettlementTuple(tx, l.ID)
	if err != nil {
		return err
	}
	if current != prior {
		return refuse()
	}
	result, err := tx.Exec(`UPDATE leases SET data=? WHERE id=? AND typeof(id)='text' AND data=? AND typeof(data)='text' AND request_key IS ? AND typeof(request_key)=? AND run IS ? AND typeof(run)=?`, data, l.ID, prior.data, settlementSQLText(prior.key), settlementSQLType(prior.key), settlementSQLText(prior.run), settlementSQLType(prior.run))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return refuse()
	}
	if _, err = tx.Exec(`INSERT INTO events(type,entity,at,data) VALUES(?,?,?,?)`, "lease.ending", l.ID, time.Now().UTC().Format(time.RFC3339Nano), data); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	prior.data = data
	j.sshNoNative = &prior
	return nil
}

// originalSSHCompletionBinding is a pre-effect check, before classification or
// port/profile cleanup. It neither consumes ready/staged nor invokes native
// controls. The original sole waiter has returned; optional identity observation
// may be unavailable, but the captured command and process-local owner remain.
// Manager.mu is held. Renew changes only the job's acknowledged deadline.
func (m *Manager) originalSSHCompletionBinding(id string, row originalSettlementTuple, l model.Lease, j *job, r *sshRuntime, originalPorts []model.PortBinding, nativeIdentityAcknowledged bool, preNative *originalSettlementTuple) bool {
	if id == "" || j == nil || r == nil || m.jobs[id] != j || j.sshRuntime != r || j.childOwner == nil || j.cmd == nil || j.cmd.Process == nil || j.cmd.Process.Pid <= 0 ||
		j.done == nil || j.nativeDone == nil || row.id != id || !originalSettlementLaunchMatches(row, j.settlementLaunch) {
		return false
	}
	h := r.leaseHold
	if h == nil {
		return false
	}
	nativeMatches := nativeIdentityAcknowledged && l.PID > 0 && l.PID == j.cmd.Process.Pid && h.expectedPID == l.PID &&
		reflect.DeepEqual(l.Process, j.identity) && reflect.DeepEqual(l.Process, h.expectedProcess)
	if !nativeIdentityAcknowledged && preNative != nil {
		// The original gated shell was really owned and waited, but the failed
		// lease.started ACK prevented the requested workload gate from opening.
		// Only its privately known, ACKed no-native predecessor is eligible here.
		nativeMatches = preNative.id == id && preNative.data == row.data && preNative.key == row.key && preNative.run == row.run &&
			l.PID == 0 && l.Process == nil && h.expectedPID == 0 && h.expectedProcess == nil && j.cpuExited.Load() && j.cmd.ProcessState != nil
	}
	return h != nil && h.manager == m && h.runtime == r && h.job == j && m.sshTransports[id] == h &&
		h.nativeStarted && !h.nativeReaped && !h.staged && h.err == nil && h.original.ID == id &&
		l.ID == id && l.Host == m.host && l.Owner == m.owner && l.Host == j.leaseHost && l.Owner == j.leaseOwner &&
		h.original.Host == l.Host && h.original.Owner == l.Owner && reflect.DeepEqual(l.Request, j.leaseRequest) && reflect.DeepEqual(l.Request, h.original.Request) &&
		nativeMatches &&
		reflect.DeepEqual(l.Clock, j.leaseDeadline) && reflect.DeepEqual(l.Expires, j.leaseExpires) && reflect.DeepEqual(l.Ports, originalPorts)
}

// stageSSHResult never stores terminal while the original transport is live.
// The exact charged intermediate row fences later join settlement.
func (m *Manager) stageSSHResult(l model.Lease) (model.Lease, error) {
	return m.stageSSHResultFor(l.ID, l)
}

// The original map key is captured before native waiting. Never select the
// obligation to release from a subsequently decoded candidate's JSON identity.
func (m *Manager) stageSSHResultFor(originalID string, l model.Lease) (model.Lease, error) {
	h := m.sshTransports[originalID]
	if h == nil {
		return l, model.Err("ssh_owner_changed", "original SSH transport hold is absent")
	}
	if h.staged {
		return l, model.Err("ssh_owner_changed", "original SSH settlement already staged")
	}
	h.staged = true
	defer close(h.ready)
	if l.ID != h.original.ID || l.Host != h.original.Host || l.Owner != h.original.Owner || !reflect.DeepEqual(l.Request, h.original.Request) {
		h.err = model.Err("ssh_owner_changed", "SSH lease intent/owner changed before settlement")
		return l, h.err
	}
	var rawCurrent string
	if err := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, originalID).Scan(&rawCurrent); err != nil {
		h.err = err
		return l, err
	}
	var stored model.Lease
	if err := json.Unmarshal([]byte(rawCurrent), &stored); err != nil {
		h.err = err
		return l, err
	}
	if stored.ID != h.original.ID || stored.PID != h.expectedPID || !reflect.DeepEqual(stored.Process, h.expectedProcess) || stored.Host != h.original.Host || stored.Owner != h.original.Owner || !reflect.DeepEqual(stored.Request, h.original.Request) {
		h.err = model.Err("ssh_owner_changed", "SSH native row binding changed before staging")
		return l, h.err
	}
	if h.nativeStarted && !h.nativeSafe {
		l.State = "needs_attention"
		l.Ended = nil
		if l.Failure == "" {
			l.Failure = "Original native cleanup is uncertain; SSH obligations remain charged"
		}
	}
	if h.job != nil && m.jobs[l.ID] != h.job {
		h.err = model.Err("ssh_owner_changed", "original SSH job changed before staging")
		return l, h.err
	}
	h.candidate = l
	interim := l
	if interim.State == "terminal" {
		interim.State = "ending"
		interim.Ended = nil
	}
	if !consuming(interim.State) {
		h.err = fmt.Errorf("SSH settlement must retain a consuming intermediate state")
		return l, h.err
	}
	if err := m.put(interim, "lease.awaiting_ssh_transport"); err != nil {
		h.err = err
		return interim, err
	}
	raw, err := json.Marshal(interim)
	if err != nil {
		h.err = err
		return interim, err
	}
	h.expected = string(raw)
	return interim, nil
}

func (m *Manager) failSSHSettlement(id string, err error) {
	if h := m.sshTransports[id]; h != nil && !h.staged {
		h.staged = true
		h.err = err
		close(h.ready)
	}
}
func (m *Manager) sshSettlementErrors() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	for _, h := range m.sshTransports {
		err = errors.Join(err, h.err, model.Err("needs_attention", "SSH settlement remains charged pending exact reconciliation"))
	}
	return err
}

// Saved descriptors classify lost obligations; they confer no runtime authority.
func recordedSSHRun(raw string) (bool, error) {
	var saved RunRequest
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		return false, err
	}
	return saved.SSH != nil, nil
}
