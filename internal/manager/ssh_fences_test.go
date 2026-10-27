package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

func fenceControl(m *Manager, key string) model.SSHControlCommitment {
	return model.SSHControlCommitment{Schema: 1, Source: strings.Repeat("a", 32), Target: m.host, Pair: strings.Repeat("b", 64), Revision: 2, Certificate: strings.Repeat("c", 64), Owner: m.owner, Account: m.ownerUsername, HostKeySHA256: strings.Repeat("d", 64), Key: key, PublicKeySHA256: strings.Repeat("e", 64), Reason: "synthetic destination-fence fixture", TTL: time.Minute, Resources: model.Resources{CPU: .5}, CWD: "/tmp", Kind: "exec", CommandSHA256: sshDigest([]byte("private command"))}
}
func fenceOperation(m *Manager, c model.SSHControlCommitment) (model.SSHOperation, model.Lease, RunRequest) {
	now := time.Now().UTC()
	q := model.SSHExecutionRequest{Source: c.Source, Target: c.Target, Pair: c.Pair, Revision: c.Revision, Certificate: c.Certificate, Account: c.Account, Key: c.Key, PublicKeySHA256: c.PublicKeySHA256, Reason: c.Reason, TTL: c.TTL, Resources: c.Resources, CWD: c.CWD, Kind: c.Kind, CommandSHA256: c.CommandSHA256}
	if c.PTY {
		q.TerminalSHA256 = strings.Repeat("f", 64)
	}
	r := model.Request{Host: m.host, Reason: q.Reason, TTL: q.TTL, Resources: q.Resources, Key: sshLeaseKey(q), Peer: q.Source, OriginKey: q.Key, PeerCertificate: q.Certificate, PeerGrantRevision: q.Revision}
	l := model.Lease{ID: local.ID(), Host: m.host, Owner: m.owner, OwnerUsername: m.ownerUsername, Request: r, State: "terminal", Outcome: "completed", Created: now, Ended: &now, Revision: 1, Capabilities: []string{}}
	return model.SSHOperation{Schema: 1, Request: q, Lease: l.ID, AdmittedAt: now}, l, RunRequest{Request: r, Command: []string{sshRedactedCommand}, CWD: q.CWD, SSH: sshDescriptor(q)}
}
func fenceTestAdmit(m *Manager, c model.SSHControlCommitment, rollback bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx := context.Background()
	if e := m.ensureSSHKeyFences(ctx); e != nil {
		return e
	}
	op, l, run := fenceOperation(m, c)
	tx, e := m.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = insertSSHAdmittedFence(ctx, tx, c, op, l); e != nil {
		return e
	}
	lb, _ := json.Marshal(l)
	rb, _ := json.Marshal(run)
	ob, _ := json.Marshal(op)
	if _, e = tx.Exec(`INSERT INTO leases(id,request_key,data,run) VALUES(?,?,?,?)`, l.ID, l.Request.Key, string(lb), string(rb)); e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO ssh_operations(principal,key,lease,data) VALUES(?,?,?,?)`, c.Source, c.Key, l.ID, string(ob)); e != nil {
		return e
	}
	if rollback {
		return errors.New("fixture rolls back after all related inserts")
	}
	return tx.Commit()
}
func fenceRows(t *testing.T, m *Manager, c model.SSHControlCommitment) (int, int) {
	t.Helper()
	var leases, ops int
	if e := m.db.QueryRow(`SELECT COUNT(*) FROM leases WHERE request_key=?`, sshLeaseKey(model.SSHExecutionRequest{Source: c.Source, Key: c.Key})).Scan(&leases); e != nil {
		t.Fatal(e)
	}
	if e := m.db.QueryRow(`SELECT COUNT(*) FROM ssh_operations WHERE principal=? AND key=?`, c.Source, c.Key).Scan(&ops); e != nil {
		t.Fatal(e)
	}
	return leases, ops
}
func TestSSHKeyLookupReadOnlyAndSealSurvivesReplyLoss(t *testing.T) {
	m := newManager(t)
	c := fenceControl(m, "first-seal")
	status, e := m.LookupSSHKey(context.Background(), c.Source, c.Key)
	if e != nil || status.State != "absent" {
		t.Fatal(status, e)
	}
	var tables, markers int
	if e = m.db.QueryRow(`SELECT (SELECT COUNT(*) FROM sqlite_schema WHERE name IN('ssh_operations','ssh_key_fences')),(SELECT COUNT(*) FROM metadata WHERE key IN('ssh_schema','ssh_fence_schema'))`).Scan(&tables, &markers); e != nil || tables != 0 || markers != 0 {
		t.Fatal("read-only lookup initialized namespace", tables, markers, e)
	}
	if _, e = m.SealSSHKey(context.Background(), c, "Prevent this original invocation from ever executing"); e != nil {
		t.Fatal(e)
	} // Simulate reply lost: discard result.
	status, e = m.LookupSSHKey(context.Background(), c.Source, c.Key)
	if e != nil || status.State != "sealed" || status.Fence == nil || status.Execution != nil {
		t.Fatal("lost seal response not recoverable by read-only lookup", status, e)
	}
	original, _ := json.Marshal(status.Fence)
	replay, e := m.SealSSHKey(context.Background(), c, "another wording cannot replace the immutable receipt")
	if e != nil {
		t.Fatal(e)
	}
	actual, _ := json.Marshal(replay.Fence)
	if string(original) != string(actual) {
		t.Fatal("existing sealed receipt replaced")
	}
	if e = fenceTestAdmit(m, c, false); e == nil {
		t.Fatal("late authenticated exec bypassed durable seal")
	}
	if leases, ops := fenceRows(t, m, c); leases != 0 || ops != 0 {
		t.Fatal("sealed key admitted work", leases, ops)
	}
	wrong := c
	wrong.CommandSHA256 = strings.Repeat("1", 64)
	if _, e = m.SealSSHKey(context.Background(), wrong, "different exact control"); e == nil {
		t.Fatal("conflicting control accepted")
	}
	hidden, e := m.LookupSSHKey(context.Background(), strings.Repeat("9", 32), c.Key)
	if e != nil || hidden.State != "absent" || hidden.Fence != nil {
		t.Fatal("other principal saw seal", e)
	}
}
func TestSSHKeySealAndAdmissionShareAtomicTransaction(t *testing.T) {
	m := newManager(t)
	c := fenceControl(m, "rolled-back-admission")
	if e := fenceTestAdmit(m, c, true); e == nil {
		t.Fatal("rollback fixture did not fail")
	}
	status, e := m.LookupSSHKey(context.Background(), c.Source, c.Key)
	if e != nil || status.State != "absent" {
		t.Fatal("rollback leaked fence or work", status, e)
	}
	if leases, ops := fenceRows(t, m, c); leases != 0 || ops != 0 {
		t.Fatal("rollback leaked related rows")
	}
	for i := 0; i < 20; i++ {
		c := fenceControl(m, fmt.Sprintf("concurrent-%d", i))
		start := make(chan struct{})
		var wg sync.WaitGroup
		var admission error
		var seal model.SSHKeyStatus
		var sealError error
		wg.Add(2)
		go func() { defer wg.Done(); <-start; admission = fenceTestAdmit(m, c, false) }()
		go func() {
			defer wg.Done()
			<-start
			seal, sealError = m.SealSSHKey(context.Background(), c, "Explicit competing nonexecution review")
		}()
		close(start)
		wg.Wait()
		if sealError != nil {
			t.Fatal(sealError)
		}
		state, e := m.LookupSSHKey(context.Background(), c.Source, c.Key)
		if e != nil {
			t.Fatal(e)
		}
		leases, ops := fenceRows(t, m, c)
		switch state.State {
		case "sealed":
			if admission == nil || leases != 0 || ops != 0 || seal.State != "sealed" {
				t.Fatal("seal winner still admitted", admission, leases, ops)
			}
		case "admitted":
			if admission != nil || leases != 1 || ops != 1 || seal.State != "admitted" {
				t.Fatal("admission winner lost atomic operation", admission, leases, ops)
			}
		default:
			t.Fatal("race produced no decisive durable outcome", state.State)
		}
	}
}
func TestSSHKeyFenceLegacyTransitionPreservesActualExecution(t *testing.T) {
	f := newSSHManagerFixture(t, nil, time.Minute)
	session, e := f.client.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	if out, e := session.CombinedOutput("printf legacy-fence"); e != nil || string(out) != "legacy-fence" {
		t.Fatal(string(out), e)
	}
	q, _, l := f.snapshot()
	eventually(t, func() bool { v, e := f.m.Inspect(l.ID); return e == nil && v.State == "terminal" })
	var original string
	if e = f.m.db.QueryRow(`SELECT data FROM ssh_operations WHERE principal=? AND key=?`, q.Source, q.Key).Scan(&original); e != nil {
		t.Fatal(e)
	}
	before, e := f.m.LookupSSHKey(context.Background(), q.Source, q.Key)
	if e != nil || before.State != "legacy-admitted" || before.Fence != nil {
		t.Fatal("read-only legacy projection changed evidence", before, e)
	}
	f.m.mu.Lock()
	e = f.m.ensureSSHKeyFences(context.Background())
	f.m.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	after, e := f.m.LookupSSHKey(context.Background(), q.Source, q.Key)
	if e != nil || after.State != "legacy-admitted" || after.Fence == nil || after.Fence.Control != nil || after.Fence.ControlSHA256 != "" || after.Fence.OperationSHA256 != sshDigest([]byte(original)) || after.Execution == nil {
		t.Fatal("invented legacy control or lost exact operation", after, e)
	}
	var unchanged string
	f.m.db.QueryRow(`SELECT data FROM ssh_operations WHERE principal=? AND key=?`, q.Source, q.Key).Scan(&unchanged)
	if unchanged != original {
		t.Fatal("migration rewrote historical operation")
	}
	c := fenceControl(f.m, q.Key)
	c.Source = q.Source
	result, e := f.m.SealSSHKey(context.Background(), c, "cannot erase an existing actual execution")
	if e != nil || result.State != "legacy-admitted" {
		t.Fatal("legacy work replaced by nonexecution proof", result, e)
	}
	if e = fenceTestAdmit(f.m, c, false); e == nil {
		t.Fatal("legacy admitted key reexecuted")
	}
}
func TestSSHKeyFenceSchemaLossAndRowTamperingFailClosed(t *testing.T) {
	for _, mode := range []string{"missing-table", "missing-marker", "inplace-data", "orphan-lease"} {
		t.Run(mode, func(t *testing.T) {
			m := newManager(t)
			c := fenceControl(m, "tamper-key")
			if mode == "orphan-lease" {
				m.mu.Lock()
				e := m.ensureSSHKeyFences(context.Background())
				m.mu.Unlock()
				if e != nil {
					t.Fatal(e)
				}
				_, l, run := fenceOperation(m, c)
				lb, _ := json.Marshal(l)
				rb, _ := json.Marshal(run)
				if _, e = m.db.Exec(`INSERT INTO leases(id,request_key,data,run) VALUES(?,?,?,?)`, l.ID, l.Request.Key, string(lb), string(rb)); e != nil {
					t.Fatal(e)
				}
				if _, e = m.SealSSHKey(context.Background(), c, "orphan must not become nonexecution proof"); e == nil {
					t.Fatal("orphan admitted key sealed")
				}
				return
			}
			if _, e := m.SealSSHKey(context.Background(), c, "preserve initial exact seal"); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "missing-table":
				m.db.Exec(`DROP TABLE ssh_key_fences`)
			case "missing-marker":
				m.db.Exec(`DELETE FROM metadata WHERE key='ssh_fence_schema'`)
			case "inplace-data":
				m.db.Exec(`UPDATE ssh_key_fences SET data=json_set(data,'$.control.PTY',false)`)
			}
			if _, e := m.LookupSSHKey(context.Background(), c.Source, c.Key); e == nil {
				t.Fatal("corrupt namespace accepted")
			}
			c.Key = "new-key"
			if _, e := m.SealSSHKey(context.Background(), c, "do not repair missing evidence"); e == nil && mode != "inplace-data" {
				t.Fatal("partial namespace repaired")
			}
		})
	}
}
