package manager

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
)

// These contexts isolate the cancellation authority from settlement. They do
// not impersonate a helper exit, a completed export or an original native join.
func retainedCargoContext(t *testing.T, m *Manager, l model.Lease) (model.Lease, context.Context) {
	t.Helper()
	l.Request.Profile = "rust-build"
	l.Cargo = &model.CargoBinding{Schema: 1}
	if err := m.put(l, "fixture.retained_cargo"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &cargoWorker{leaseID: l.ID, lease: l, leaseHost: l.Host, leaseOwner: l.Owner, leaseRequest: l.Request,
		id: "original-fixture-worker", done: make(chan struct{}), cancel: cancel}
	rec := cargoNewRecord(l, &RunRequest{Request: l.Request, Command: []string{"cargo", "build", "--offline"}})
	rec.Worker, rec.Phase = w.id, "spawn_intent"
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`CREATE TABLE IF NOT EXISTS cargo_workloads (lease TEXT PRIMARY KEY,data TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`INSERT INTO cargo_workloads(lease,data) VALUES(?,?)`, l.ID, string(b)); err != nil {
		t.Fatal(err)
	}
	if m.cargoWorkers == nil {
		m.cargoWorkers = map[string]*cargoWorker{}
	}
	m.cargoWorkers[l.ID] = w
	t.Cleanup(func() { cancel(); m.mu.Lock(); delete(m.cargoWorkers, l.ID); m.mu.Unlock() })
	return l, ctx
}

func TestRetainedProfileEndReadFailureCancelsOnlyCapturedContext(t *testing.T) {
	for _, mode := range []string{"unreadable", "changed-request"} {
		t.Run(mode, func(t *testing.T) {
			m, c := clockManager(t)
			l := clockGrant(t, m, "original", time.Minute)
			sentinel := clockGrant(t, m, "sentinel", time.Minute)
			m.mu.Lock()
			l, ctx := retainedCargoContext(t, m, l)
			_, other := retainedCargoContext(t, m, sentinel)
			raw, _ := json.Marshal(l)
			if mode == "unreadable" {
				_, _ = m.db.Exec(`UPDATE leases SET data='{' WHERE id=?`, l.ID)
			} else {
				changed := l
				changed.Host = "foreign-host"
				if err := m.put(changed, "fixture.changed"); err != nil {
					m.mu.Unlock()
					t.Fatal(err)
				}
			}
			m.mu.Unlock()
			_, err := m.End(l.ID, "cancelled")
			if err == nil || ctx.Err() == nil || other.Err() != nil {
				t.Fatal("End lost original cancellation/refusal/sentinel", err, ctx.Err(), other.Err())
			}
			m.mu.Lock()
			_, restoreErr := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), l.ID)
			if restoreErr == nil {
				got, e := m.get(l.ID)
				if e != nil || got.State != l.State || !reflect.DeepEqual(got.Clock, l.Clock) {
					restoreErr = errors.New("cancellation rewrote original entitlement")
				}
			}
			m.mu.Unlock()
			if restoreErr != nil {
				t.Fatal(restoreErr)
			}
			_ = c
		})
	}
}

func TestRetainedCheckpointExpiryRequiresCompleteRosterAndExactDeadline(t *testing.T) {
	m, c := clockManager(t)
	a := clockGrant(t, m, "changed", time.Minute)
	b := clockGrant(t, m, "valid-sibling", time.Minute)
	m.mu.Lock()
	a, first := retainedCargoContext(t, m, a)
	b, second := retainedCargoContext(t, m, b)
	c.sample.Tick = b.Clock.End
	rows := []checkpointExpiryLease{{id: a.ID, lease: a}, {id: b.ID, lease: b}}
	rows[0].lease.Request.Reason = "replaced tuple"
	err := m.cancelCheckpointExpiredHolders(c.sample, rows)
	if err == nil || first.Err() != nil || second.Err() == nil {
		m.mu.Unlock()
		t.Fatal("binding mismatch starved valid sibling or cancelled replacement", err)
	}
	m.mu.Unlock()

	// A malformed later row prevents every cancellation, even an earlier valid
	// expired original. No partially decoded roster may grant authority.
	m2, c2 := clockManager(t)
	x := clockGrant(t, m2, "complete-only", time.Minute)
	y := clockGrant(t, m2, "corrupt-later", time.Minute)
	m2.mu.Lock()
	x, held := retainedCargoContext(t, m2, x)
	c2.sample.Tick = x.Clock.End
	_, err = m2.db.Exec(`UPDATE leases SET data='{' WHERE id=?`, y.ID)
	if err == nil {
		_, err = m2.db.Exec(`CREATE TRIGGER retained_clock_refusal BEFORE UPDATE ON metadata WHEN NEW.key='lease_clock' BEGIN SELECT RAISE(ABORT,'retained checkpoint refusal'); END`)
	}
	if err != nil {
		m2.mu.Unlock()
		t.Fatal(err)
	}
	err = m2.reconcileLeaseExpiries()
	if err == nil || held.Err() != nil {
		m2.mu.Unlock()
		t.Fatal("partial physical roster authorized cancellation", err)
	}
	raw, _ := json.Marshal(y)
	_, _ = m2.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), y.ID)
	_, _ = m2.db.Exec(`DROP TRIGGER retained_clock_refusal`)
	m2.mu.Unlock()

	// Matching lease bytes do not authorize a worker whose durable Cargo
	// sidecar now names another original; the valid sibling still cancels.
	m3, c3 := clockManager(t)
	p := clockGrant(t, m3, "worker-replaced", time.Minute)
	q := clockGrant(t, m3, "worker-valid", time.Minute)
	m3.mu.Lock()
	p, replaced := retainedCargoContext(t, m3, p)
	q, valid := retainedCargoContext(t, m3, q)
	rec, recordErr := m3.getCargo(p.ID)
	if recordErr == nil {
		rec.Worker = "different-original-worker"
		b, _ := json.Marshal(rec)
		_, recordErr = m3.db.Exec(`UPDATE cargo_workloads SET data=? WHERE lease=?`, string(b), p.ID)
	}
	if recordErr != nil {
		m3.mu.Unlock()
		t.Fatal(recordErr)
	}
	c3.sample.Tick = q.Clock.End
	err = m3.cancelCheckpointExpiredHolders(c3.sample, []checkpointExpiryLease{{id: p.ID, lease: p}, {id: q.ID, lease: q}})
	m3.mu.Unlock()
	if err == nil || replaced.Err() != nil || valid.Err() == nil {
		t.Fatal("durable worker replacement inherited authority or starved sibling", err)
	}
}

func TestRetainedDeadlineChangesOnlyAfterAcknowledgedRenewal(t *testing.T) {
	m, c := clockManager(t)
	l := clockGrant(t, m, "deadline", time.Minute)
	m.mu.Lock()
	l, ctx := retainedCargoContext(t, m, l)
	w := m.cargoWorkers[l.ID]
	changed := l
	d := *l.Clock
	d.End += int64(time.Minute)
	d.Wall = d.Wall.Add(time.Minute)
	changed.Clock, changed.Expires = &d, &d.Wall
	if err := m.cancelBoundRetained(checkpointExpiryLease{id: l.ID, lease: changed}); err == nil || ctx.Err() != nil {
		m.mu.Unlock()
		t.Fatal("unacknowledged deadline adopted")
	}
	_, err := m.db.Exec(`CREATE TRIGGER retained_renew_refusal BEFORE INSERT ON events WHEN NEW.type='lease.renewed' BEGIN SELECT RAISE(ABORT,'retained renewal refusal'); END`)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Renew(l.ID, 2*time.Minute); err == nil {
		t.Fatal("failed renewal unexpectedly acknowledged")
	}
	m.mu.Lock()
	if !retainedDeadlineBinding(w.lease, l) {
		m.mu.Unlock()
		t.Fatal("failed renewal changed original entitlement")
	}
	_, err = m.db.Exec(`DROP TRIGGER retained_renew_refusal`)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := m.Renew(l.ID, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	if !retainedDeadlineBinding(w.lease, renewed) || !reflect.DeepEqual(w.lease.Request, l.Request) {
		m.mu.Unlock()
		t.Fatal("acknowledged renewal lost or changed immutable binding")
	}
	c.sample.Tick = renewed.Clock.End
	if err = m.cancelCheckpointExpiredHolders(c.sample, []checkpointExpiryLease{{id: l.ID, lease: renewed}}); err != nil || ctx.Err() == nil {
		m.mu.Unlock()
		t.Fatal("acknowledged current deadline lost cancellation", err)
	}
	m.mu.Unlock()
}

func TestExpiredRetainedStorageParentCancelsOnlyOriginalDependent(t *testing.T) {
	m := newManager(t)
	parent, err := m.Acquire(storageRequest("retained-parent"), nil)
	if err != nil {
		t.Fatal(err)
	}
	child := clockGrant(t, m, "retained-dependent", 2*time.Minute)
	sentinel := clockGrant(t, m, "retained-other", 2*time.Minute)
	m.mu.Lock()
	child.Request.StorageLease = parent.ID
	child, ctx := retainedCargoContext(t, m, child)
	_, other := retainedCargoContext(t, m, sentinel)
	m.cargoWorkers[child.ID].storageParent = parent
	sample, err := m.readLeaseClock()
	if err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	sample.Tick = parent.Clock.End
	wrong := parent
	wrong.ManagedDir = parent.ManagedDir + "-replacement"
	if err = m.cancelCheckpointExpiredHolders(sample, []checkpointExpiryLease{{id: parent.ID, lease: wrong}, {id: child.ID, lease: child}}); err != nil || ctx.Err() != nil {
		m.mu.Unlock()
		t.Fatal("replacement parent granted cancellation", err)
	}
	if err = m.cancelCheckpointExpiredHolders(sample, []checkpointExpiryLease{{id: parent.ID, lease: parent}, {id: child.ID, lease: child}}); err != nil || ctx.Err() == nil || other.Err() != nil {
		m.mu.Unlock()
		t.Fatal("parent expiry lost original-dependent/sentinel boundary", err)
	}
	got, err := m.get(parent.ID)
	m.mu.Unlock()
	if err != nil || got.State != parent.State || got.Revision != parent.Revision || !retainedDeadlineBinding(got, parent) {
		t.Fatal("context cancellation settled or rewrote parent", err)
	}
}

func TestNormalCheckpointExpiryUsesOriginalPhysicalAuthority(t *testing.T) {
	for _, mode := range []string{"valid", "valid-usage", "changed-deadline", "physical-id"} {
		t.Run(mode, func(t *testing.T) {
			m, c := clockManager(t)
			m.capacity.CPU = 1
			cpuReading(m, 64, 64, 0, 64, 0)
			h, _ := acquireTestRelay(t, m, "normal-physical-original")
			sibling, _ := acquireTestRelay(t, m, "normal-physical-sibling")
			m.mu.Lock()
			original, err := m.get(h.id)
			if err != nil {
				m.mu.Unlock()
				t.Fatal(err)
			}
			raw, _ := json.Marshal(original)
			if mode == "valid-usage" {
				m.usages[h.id] = workloadSample{view: model.WorkloadUsage{Observed: time.Now().UTC(), Complete: true, Detail: "controlled transient observation"}, observed: time.Now()}
				view, e := m.get(h.id)
				if e != nil || view.Usage == nil || reflect.DeepEqual(view, original) {
					m.mu.Unlock()
					t.Fatal("usage decoration control was vacuous", e)
				}
			}
			changed := original
			if mode == "changed-deadline" {
				d := *changed.Clock
				d.End -= int64(time.Second)
				d.Wall = d.Wall.Add(-time.Second)
				changed.Clock, changed.Expires = &d, &d.Wall
			} else if mode == "physical-id" {
				changed.ID = "foreign-physical-body"
			}
			if mode != "valid" && mode != "valid-usage" {
				b, _ := json.Marshal(changed)
				_, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(b), h.id)
			}
			if err != nil {
				m.mu.Unlock()
				t.Fatal(err)
			}
			c.sample.Tick = sibling.result.Clock.End
			err = m.reconcileLeaseExpiries() // Checkpoint succeeds; no explicit End is called.
			cancelled, siblingCancelled := h.Context().Err() != nil, sibling.Context().Err() != nil
			var after string
			_ = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, h.id).Scan(&after)
			if mode != "valid" && mode != "valid-usage" {
				_, _ = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), h.id)
			}
			m.mu.Unlock()
			if !siblingCancelled {
				t.Fatal("invalid row starved valid normal-expiry original", err)
			}
			if mode == "valid" || mode == "valid-usage" {
				if err != nil || !cancelled {
					t.Fatal("normal expiry lost valid original", err)
				}
			} else {
				b, _ := json.Marshal(changed)
				if err == nil || cancelled || after != string(b) {
					t.Fatal("automatic path acquired explicit authority from changed row", err, cancelled)
				}
			}
		})
	}
}

func TestStorageExportCancellationPreservesBusyFenceAndFailedEndingCharge(t *testing.T) {
	for _, mode := range []string{"ending-write", "checkpoint-write"} {
		t.Run(mode, func(t *testing.T) {
			m := newManager(t)
			l, err := m.Acquire(storageRequest("held-export"), nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op := model.StorageExport{ID: "original-export", Request: model.StorageExportRequest{Lease: l.ID, Key: "held-export", Reason: "held cancellation fixture"}, State: "copying", Host: m.host, Owner: m.owner}
			m.mu.Lock()
			if err = m.saveStorageExport(&op, "fixture.export", true); err == nil {
				m.storageJobs[exportJobKey(l.ID)], m.storageJobs[exportOperationJobKey(op.ID)] = true, true
				m.storageExportCancels = map[string]*storageExportCancellation{l.ID: {lease: l, operation: op.ID, cancel: cancel}}
			}
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				m.mu.Lock()
				delete(m.storageExportCancels, l.ID)
				delete(m.storageJobs, exportJobKey(l.ID))
				delete(m.storageJobs, exportOperationJobKey(op.ID))
				_, _ = m.db.Exec(`DELETE FROM storage_exports WHERE id=?`, op.ID)
				_, _ = m.db.Exec(`DROP TRIGGER IF EXISTS retained_export_refusal`)
				m.mu.Unlock()
			})
			if _, err = m.End(l.ID, "released"); err == nil || ctx.Err() != nil {
				t.Fatal("ordinary busy export release became cancellation", err)
			}
			m.mu.Lock()
			var before string
			_ = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&before)
			s, readErr := m.readLeaseClock()
			if readErr != nil {
				m.mu.Unlock()
				t.Fatal(readErr)
			}
			s.Tick = l.Clock.End
			m.leaseClockRead = func() (leaseclock.Sample, error) { return s, nil }
			if mode == "ending-write" {
				_, err = m.db.Exec(`CREATE TRIGGER retained_export_refusal BEFORE INSERT ON events WHEN NEW.type='storage.ending' BEGIN SELECT RAISE(ABORT,'retained export refusal'); END`)
			} else {
				_, err = m.db.Exec(`CREATE TRIGGER retained_export_refusal BEFORE UPDATE ON metadata WHEN NEW.key='lease_clock' BEGIN SELECT RAISE(ABORT,'retained export refusal'); END`)
			}
			if err == nil {
				err = m.reconcileLeaseExpiries()
			}
			var after string
			_ = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&after)
			m.mu.Unlock()
			if err == nil || !strings.Contains(err.Error(), "retained export refusal") || ctx.Err() == nil || before != after {
				t.Fatal("failed journal retained live export or rewrote charge", err, ctx.Err(), before == after)
			}
		})
	}
}
