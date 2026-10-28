package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"getarm.org/internal/kubesource"
	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
)

func sourceCoreManager(t *testing.T) *Manager {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), "state"), model.Resources{CPU: 1, Memory: 512 << 20, Disk: 512 << 20})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 64, 64, 0, 64, 0)
	t.Cleanup(func() { _ = m.Close() })
	return m
}
func sourceCoreScope(stage byte) kubesource.Scope {
	return kubesource.Scope{Operation: strings.Repeat("a", 64), Attempt: strings.Repeat("b", 32), Stage: stage, B: 1}
}
func sourceCoreOwner(t *testing.T, m *Manager, stage byte) (*KubernetesSourcePreparation, func()) {
	t.Helper()
	joined := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(joined) }) }
	owner, err := m.PrepareKubernetesSource(t.Context(), sourceCoreScope(stage), joined)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { finish(); _, _ = owner.FinishAfterJoin() })
	return owner, finish
}
func sourceCoreWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("original core worker did not finish")
	}
}
func sourceCoreDisk(t *testing.T, m *Manager, id string) model.Lease {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		l, err := m.Inspect(id)
		m.mu.Lock()
		busy := m.storageJobs[id]
		m.mu.Unlock()
		if err == nil && !busy && (l.State == "needs_attention" || l.State == "terminal") {
			return l
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ordinary storage End did not finish")
	return model.Lease{}
}
func TestKubernetesSourceReservedRequestsRequirePrivatePermit(t *testing.T) {
	m := sourceCoreManager(t)
	scope := sourceCoreScope(kubesource.Inspect)
	for _, r := range []model.Request{scope.Request(m.host, "", true), scope.Request(m.host, "", false), {Key: scope.Key(false), Reason: "forged source namespace", Resources: model.Resources{Memory: 1}, TTL: time.Minute}} {
		if _, err := m.Acquire(r, nil); err == nil {
			t.Fatal("ordinary Acquire minted reserved role")
		}
		if _, err := m.AcquireRelayTransit(t.Context(), r, make(chan struct{})); err == nil {
			t.Fatal("generic transit minted reserved role")
		}
	}
	rows, err := m.list()
	if err != nil || len(rows) != 0 {
		t.Fatal("refused forgery created claims", rows, err)
	}
	ordinary := model.Request{Key: "ordinary", Project: "ordinary", Reason: "unrelated policy preserved", Resources: model.Resources{Memory: 1}, TTL: time.Minute}
	l, err := m.Acquire(ordinary, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = m.End(l.ID, "released")
}
func TestKubernetesSourceSameOperationAdmissionIsExclusive(t *testing.T) {
	m := sourceCoreManager(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	m.sourceBeforeAdmission = func() { close(entered); <-release }
	joined := make(chan struct{})
	var joinedOnce sync.Once
	done := make(chan struct{})
	var first *KubernetesSourcePreparation
	var firstErr error
	go func() {
		defer close(done)
		first, firstErr = m.PrepareKubernetesSource(t.Context(), sourceCoreScope(kubesource.Inspect), joined)
	}()
	sourceCoreWait(t, entered)
	other := sourceCoreScope(kubesource.Inspect)
	other.Attempt = strings.Repeat("c", 32)
	if owner, err := m.PrepareKubernetesSource(t.Context(), other, make(chan struct{})); err == nil || owner != nil {
		t.Fatal("second source published")
	}
	if rows, err := m.list(); err != nil || len(rows) != 0 {
		t.Fatal("losing admission produced rows/files", rows, err)
	}
	releaseOnce.Do(func() { close(release) })
	sourceCoreWait(t, done)
	if firstErr != nil || first == nil {
		t.Fatal(firstErr)
	}
	t.Cleanup(func() { joinedOnce.Do(func() { close(joined) }); _, _ = first.FinishAfterJoin() })
	rows, err := m.list()
	if err != nil || len(rows) != 2 {
		t.Fatal("not exactly one original pair", len(rows), err)
	}
	joinedOnce.Do(func() { close(joined) })
	if _, err = first.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
}
func TestKubernetesSourcePartialAdmissionNeverWaitsUnpublishedCaller(t *testing.T) {
	m := sourceCoreManager(t)
	m.capacity.Disk = 1
	done := make(chan struct{})
	var owner *KubernetesSourcePreparation
	var err error
	go func() {
		defer close(done)
		owner, err = m.PrepareKubernetesSource(t.Context(), sourceCoreScope(kubesource.Ship), make(chan struct{}))
	}()
	sourceCoreWait(t, done)
	if err == nil || owner != nil {
		t.Fatal("disk refusal exposed factory")
	}
	rows, e := m.list()
	if e != nil || len(rows) != 1 || rows[0].State != "terminal" || rows[0].Request.StorageLease != "" {
		t.Fatal("partial source hold leaked", rows, e)
	}
	if m.storage != nil {
		t.Fatal("disk refusal created storage")
	}
}
func TestKubernetesSourceDebtPairAndCumulativePayload(t *testing.T) {
	m := sourceCoreManager(t)
	owner, finish := sourceCoreOwner(t, m, kubesource.Ship)
	payload, err := owner.Files().NewPayload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = payload.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	} // B+1 maximum, including oversize sentinel
	if _, err = payload.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err = payload.Write([]byte("x")); err == nil {
		t.Fatal("seek replenished cumulative budget")
	}
	if err = payload.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Files().NewPayload(t.Context()); err == nil {
		t.Fatal("positive close replenished purpose")
	}
	m.mu.Lock()
	parent, err := m.get(owner.parent.ID)
	m.mu.Unlock()
	if err != nil || parent.Artifact.AllocatedBefore == nil || !parent.Artifact.ContentObserved {
		t.Fatal("debt pair not durable", parent, err)
	}
	entries, err := os.ReadDir(parent.ManagedDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("temporary entry not actually unlinked", entries, err)
	}
	finish()
	result, err := owner.FinishAfterJoin()
	if err != nil || !result.SourceClosed || !result.SourceSettled || !result.StorageRetained {
		t.Fatal(result, err)
	}
	final := sourceCoreDisk(t, m, parent.ID)
	if final.State != "needs_attention" || !final.Artifact.ContentObserved || final.Artifact.AllocatedBefore == nil {
		t.Fatal("empty final namespace erased debt", final)
	}
}
func TestKubernetesSourceLostDebtAckDoesNotExposeOrUnlink(t *testing.T) {
	m := sourceCoreManager(t)
	owner, finish := sourceCoreOwner(t, m, kubesource.Ship)
	owner.lostDebtAck = true
	file, err := owner.Files().NewPayload(t.Context())
	if err == nil || file != nil {
		t.Fatal("unacknowledged observation exposed file")
	}
	entries, err := os.ReadDir(owner.parent.ManagedDir)
	if err != nil || len(entries) != 1 {
		t.Fatal("unacknowledged entry was unlinked", entries, err)
	}
	finish()
	result, err := owner.FinishAfterJoin()
	if err == nil || result.SourceSettled {
		t.Fatal(result, err)
	}
	status, err := m.Status()
	if err != nil || status.Reserved.CPU != kubesource.SourceCPU || status.Reserved.Disk != owner.scope.DiskBytes() {
		t.Fatal("unknown ACK released pair", status.Reserved, err)
	}
}
func TestKubernetesSourceRequestLaneReservedBeforeOpen(t *testing.T) {
	m := sourceCoreManager(t)
	owner, finish := sourceCoreOwner(t, m, kubesource.Inspect)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	owner.files.beforeOpen = func(string) { close(entered); <-release }
	done := make(chan struct{})
	var first *kubernetesHeldFile
	var firstErr error
	go func() { defer close(done); first, firstErr = owner.files.create(t.Context(), "lookup_binary") }()
	sourceCoreWait(t, entered)
	if second, err := owner.files.create(t.Context(), "catalog_proof"); err == nil || second != nil {
		t.Fatal("different purpose passed in-flight lane")
	}
	once.Do(func() { close(release) })
	sourceCoreWait(t, done)
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	owner.files.beforeOpen = nil
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := owner.files.create(t.Context(), "catalog_proof")
	if err != nil {
		t.Fatal("original positive close failed to release coexistence lane", err)
	}
	_ = next.Close()
	finish()
	_, _ = owner.FinishAfterJoin()
}
func TestKubernetesSourceQueuedIORechecksAfterGate(t *testing.T) {
	m := sourceCoreManager(t)
	owner, finish := sourceCoreOwner(t, m, kubesource.Ship)
	file, err := owner.files.create(t.Context(), "payload")
	if err != nil {
		t.Fatal(err)
	}
	file.ioGate <- struct{}{}
	done := make(chan struct{})
	var writeErr error
	go func() { defer close(done); _, writeErr = file.Write([]byte("x")) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		file.mu.Lock()
		waiting := file.ioPending == 1
		file.mu.Unlock()
		if waiting {
			break
		}
		time.Sleep(time.Millisecond)
	}
	file.mu.Lock()
	queued := file.ioPending == 1
	file.mu.Unlock()
	if !queued {
		<-file.ioGate
		t.Fatal("fixture did not queue the original I/O before cancellation")
	}
	owner.Cancel()
	<-file.ioGate
	sourceCoreWait(t, done)
	if writeErr == nil {
		t.Fatal("queued cancelled write reached native I/O")
	}
	info, err := file.raw.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatal("late queued write changed bytes", info, err)
	}
	finish()
	_, _ = owner.FinishAfterJoin()
}
func TestKubernetesSourceNativeClosureSeparateFromSQLSettlement(t *testing.T) {
	m := sourceCoreManager(t)
	owner, finish := sourceCoreOwner(t, m, kubesource.Inspect)
	trigger := fmt.Sprintf(`CREATE TRIGGER refuse_source_terminal BEFORE UPDATE ON leases WHEN NEW.id='%s' AND json_extract(NEW.data,'$.state')='terminal' BEGIN SELECT RAISE(ABORT,'expected terminal refusal'); END`, owner.transit.id)
	if _, err := m.db.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	finish()
	result, err := owner.FinishAfterJoin()
	if err == nil || !result.SourceClosed || result.SourceSettled {
		t.Fatal("native closure or acknowledged settlement lied", result, err)
	}
	source, err := m.Inspect(owner.transit.id)
	if err != nil || source.State == "terminal" {
		t.Fatal("terminal failure released durable source", source, err)
	}
	status, err := m.Status()
	if err != nil || status.Reserved.CPU != kubesource.SourceCPU || status.Reserved.Disk != owner.scope.DiskBytes() {
		t.Fatal(status.Reserved, err)
	}
}
func TestKubernetesSourceNativeCloseFailureIsSticky(t *testing.T) {
	m := sourceCoreManager(t)
	owner, finish := sourceCoreOwner(t, m, kubesource.Ship)
	payload, err := owner.files.create(t.Context(), "payload")
	if err != nil {
		t.Fatal(err)
	}
	owner.files.closeFile = func(f *os.File) error { _ = f.Close(); return syscall.EIO }
	if err = payload.Close(); !errors.Is(err, syscall.EIO) {
		t.Fatal(err)
	}
	finish()
	result, err := owner.FinishAfterJoin()
	if err == nil || result.SourceClosed || result.SourceSettled {
		t.Fatal(result, err)
	}
	status, e := m.Status()
	if e != nil || status.Reserved.CPU != kubesource.SourceCPU || status.Reserved.Disk != owner.scope.DiskBytes() {
		t.Fatal("negative original close freed pair", status.Reserved, e)
	}
}
func TestKubernetesSourceEndCancelsBeforeFailedRead(t *testing.T) {
	for _, role := range []string{"source", "parent"} {
		t.Run(role, func(t *testing.T) {
			m := sourceCoreManager(t)
			owner, finish := sourceCoreOwner(t, m, kubesource.Inspect)
			id := owner.transit.id
			if role == "parent" {
				id = owner.parent.ID
			}
			m.mu.Lock()
			_, err := m.db.Exec(`ALTER TABLE leases RENAME TO temporarily_unreadable`)
			if err == nil {
				_, err = m.end(id, "cancelled")
			}
			cancelled := owner.ctx.Err() != nil
			_, restore := m.db.Exec(`ALTER TABLE temporarily_unreadable RENAME TO leases`)
			m.mu.Unlock()
			if err == nil || restore != nil || !cancelled {
				t.Fatal("original cancellation depended on SQL get", err, restore, cancelled)
			}
			finish()
			_, _ = owner.FinishAfterJoin()
		})
	}
}

func TestKubernetesSourceCreateRechecksAfterGate(t *testing.T) {
	for _, mode := range []string{"cancel", "source-end", "parent-end", "elapsed-expiry", "directory-replaced"} {
		t.Run(mode, func(t *testing.T) {
			m := sourceCoreManager(t)
			owner, finish := sourceCoreOwner(t, m, kubesource.Ship)
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			owner.files.beforeOpen = func(string) { close(entered); <-release }
			var createErr error
			go func() { defer close(done); _, createErr = owner.Files().NewPayload(t.Context()) }()
			sourceCoreWait(t, entered)
			switch mode {
			case "cancel":
				owner.Cancel()
			case "source-end":
				if _, err := m.End(owner.transit.id, "cancelled"); err != nil {
					t.Fatal(err)
				}
			case "parent-end":
				if _, err := m.End(owner.parent.ID, "cancelled"); err != nil {
					t.Fatal(err)
				}
			case "elapsed-expiry":
				m.mu.Lock()
				sample := m.leaseClockLast
				sample.Tick += int64(7 * time.Minute)
				m.leaseClockRead = func() (leaseclock.Sample, error) { return sample, nil }
				m.mu.Unlock()
			case "directory-replaced":
				if err := os.Rename(owner.parent.ManagedDir, owner.parent.ManagedDir+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(owner.parent.ManagedDir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			once.Do(func() { close(release) })
			sourceCoreWait(t, done)
			owner.files.mu.Lock()
			created := len(owner.files.files)
			owner.files.mu.Unlock()
			if createErr == nil || created != 0 {
				t.Fatal("revoked creation opened a file", created, createErr)
			}
			finish()
			_, _ = owner.FinishAfterJoin()
		})
	}
}

func TestKubernetesSourceFailedUnlinkRetainsLinkedEntryAndPair(t *testing.T) {
	m := sourceCoreManager(t)
	owner, finish := sourceCoreOwner(t, m, kubesource.Ship)
	owner.files.unlinkFile = func(*os.File, string) error { return syscall.EIO }
	if file, err := owner.Files().NewPayload(t.Context()); err == nil || file != nil {
		t.Fatal(file, err)
	}
	entries, err := os.ReadDir(owner.parent.ManagedDir)
	if err != nil || len(entries) != 1 {
		t.Fatal("failed unlink erased linked entry", entries, err)
	}
	finish()
	result, err := owner.FinishAfterJoin()
	if err == nil || result.SourceSettled {
		t.Fatal(result, err)
	}
	status, err := m.Status()
	if err != nil || status.Reserved.CPU != kubesource.SourceCPU || status.Reserved.Disk != owner.scope.DiskBytes() {
		t.Fatal(status.Reserved, err)
	}
}

func TestKubernetesSourceManagerCloseCancelsThenJoinsOutsideLocks(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "state"), model.Resources{CPU: 1, Memory: 512 << 20, Disk: 512 << 20})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 64, 64, 0, 64, 0)
	joined := make(chan struct{})
	owner, err := m.PrepareKubernetesSource(t.Context(), sourceCoreScope(kubesource.Ship), joined)
	if err != nil {
		_ = m.Close()
		t.Fatal(err)
	}
	if _, err = owner.Files().NewPayload(t.Context()); err != nil {
		close(joined)
		_ = m.Close()
		t.Fatal(err)
	}
	closed := make(chan struct{})
	owner.files.closeFile = func(f *os.File) error {
		m.mu.Lock()
		m.mu.Unlock()
		err := f.Close()
		close(closed)
		return err
	}
	done := make(chan struct{})
	var closeErr error
	go func() { defer close(done); closeErr = m.Close() }()
	sourceCoreWait(t, owner.ctx.Done())
	select {
	case <-done:
		t.Fatal("Manager closed before original caller joined")
	default:
	}
	close(joined)
	sourceCoreWait(t, closed)
	sourceCoreWait(t, done)
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func TestKubernetesSourceOperationScanRefusesCorruptOrBoundedHistory(t *testing.T) {
	for _, mode := range []string{"unknown-state", "wrong-role", "column-key-mismatch", "blob", "oversize", "row-bound"} {
		t.Run(mode, func(t *testing.T) {
			m := sourceCoreManager(t)
			scope := sourceCoreScope(kubesource.Inspect)
			now := time.Now().UTC()
			request := scope.Request(m.host, m.canonicalBudgetSelector(""), false)
			request.Kind = "relay-transit"
			l := model.Lease{ID: strings.Repeat("c", 32), Host: m.host, Owner: m.owner, Request: request, State: "terminal", Outcome: "relay_closed", Ended: &now}
			key := l.Request.Key
			switch mode {
			case "unknown-state":
				l.State = "future-state"
			case "wrong-role":
				l.Request.Resources.Disk = 1
			case "column-key-mismatch":
				key = strings.Replace(key, scope.Attempt, strings.Repeat("d", 32), 1)
			}
			raw, err := json.Marshal(l)
			if err != nil {
				t.Fatal(err)
			}
			var data any = string(raw)
			if mode == "blob" {
				data = raw
			}
			if mode == "oversize" {
				data = strings.Repeat("x", 1048577)
			}
			if mode == "row-bound" {
				tx, err := m.db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 129; i++ {
					prior := scope
					prior.Attempt = fmt.Sprintf("%032x", i)
					l.ID = fmt.Sprintf("%032x", i+1)
					l.Request = prior.Request(m.host, m.canonicalBudgetSelector(""), false)
					l.Request.Kind = "relay-transit"
					raw, _ = json.Marshal(l)
					if _, err = tx.Exec(`INSERT INTO leases VALUES(?,?,?,NULL)`, l.ID, l.Request.Key, string(raw)); err != nil {
						_ = tx.Rollback()
						t.Fatal(err)
					}
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			} else if _, err = m.db.Exec(`INSERT INTO leases VALUES(?,?,?,NULL)`, l.ID, key, data); err != nil {
				t.Fatal(err)
			}
			next := scope
			next.Attempt = strings.Repeat("e", 32)
			if owner, err := m.PrepareKubernetesSource(t.Context(), next, make(chan struct{})); err == nil || owner != nil {
				t.Fatal("corrupt history admitted another original source", owner, err)
			}
			var n int
			if err = m.db.QueryRow(`SELECT count(*) FROM leases`).Scan(&n); err != nil || n != map[bool]int{false: 1, true: 129}[mode == "row-bound"] {
				t.Fatal("refusal created new rows", n, err)
			}
		})
	}
}
