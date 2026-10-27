package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yrashk/arm/internal/leaseclock"
	"github.com/yrashk/arm/internal/model"
)

// All methods below require m.mu (or unpublished Open). Failure is sticky for
// this Manager. A successful later sample cannot restore old clock authority.
func (m *Manager) readLeaseClock() (leaseclock.Sample, error) {
	if m.leaseClockError != nil {
		return leaseclock.Sample{}, m.leaseClockError
	}
	read := m.leaseClockRead
	if read == nil {
		read = leaseclock.Read
	}
	s, err := read()
	if err == nil && (!s.Valid() || (m.leaseClockLast.Valid() && (s.Boot != m.leaseClockLast.Boot || s.Tick < m.leaseClockLast.Tick))) {
		err = fmt.Errorf("elapsed clock changed scope or regressed")
	}
	if err != nil {
		m.leaseClockError = model.Err("lease_clock_unavailable", "Active lease clock is uncertain; new grants and renewals are refused. Existing obligations remain charged: "+err.Error())
		return leaseclock.Sample{}, m.leaseClockError
	}
	m.leaseClockLast = s
	return s, nil
}

func (m *Manager) initLeaseClock() error {
	var raw string
	err := m.db.QueryRow(`SELECT value FROM metadata WHERE key='lease_clock'`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var previous leaseclock.Sample
	if err == nil && (json.Unmarshal([]byte(raw), &previous) != nil || !previous.Valid()) {
		m.leaseClockError = model.Err("lease_clock_unavailable", "Persisted lease clock checkpoint is invalid; obligations remain charged")
		return nil
	}
	read := m.leaseClockRead
	if read == nil {
		read = leaseclock.Read
	}
	current, e := read()
	if e != nil || !current.Valid() {
		m.leaseClockError = model.Err("lease_clock_unavailable", "Native suspend-inclusive lease clock is unavailable")
		return nil
	}
	if previous.Valid() && previous.Boot == current.Boot && previous.Tick > current.Tick {
		m.leaseClockError = model.Err("lease_clock_unavailable", "Native lease clock regressed behind its durable checkpoint")
		return nil
	}
	m.leaseClockLast = current
	m.leaseClockCheckpoint = previous
	return nil
}

func (m *Manager) checkpointLeaseClock(s leaseclock.Sample) error {
	if m.leaseClockCheckpoint.Valid() && m.leaseClockCheckpoint.Boot == s.Boot && s.Tick >= m.leaseClockCheckpoint.Tick && s.Tick-m.leaseClockCheckpoint.Tick < int64(time.Second) {
		return nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = m.db.Exec(`INSERT INTO metadata(key,value) VALUES('lease_clock',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(b))
	if err == nil {
		m.leaseClockCheckpoint = s
	}
	return err
}

func (m *Manager) leaseRemaining(l model.Lease) (time.Duration, error) {
	s, err := m.readLeaseClock()
	if err != nil {
		return 0, err
	}
	return l.Clock.Remaining(s, l.Expires)
}
func (m *Manager) leaseLive(l model.Lease) bool {
	n, err := m.leaseRemaining(l)
	return err == nil && n > 0 && (l.State == "active" || l.State == "provisioning")
}
func (m *Manager) leaseCovers(l model.Lease, ttl time.Duration) bool {
	n, err := m.leaseRemaining(l)
	return err == nil && n > 0 && ttl > 0 && n >= ttl
}

// No wall discontinuity can expire or extend a boot-scoped lease. Unknown
// provenance withdraws automatic authority without asserting reclamation.
func (m *Manager) retainLeaseClock(l model.Lease) error {
	if l.State == "needs_attention" && l.RecoveryReason == "lease_clock_uncertain" {
		return nil
	}
	if h := m.relayTransits[l.ID]; h != nil {
		h.cancel()
	}
	if h := m.forwardListeners[l.ID]; h != nil {
		h.CloseOriginal()
	}
	if h := m.syncWorkers[l.ID]; h != nil {
		h.cancel()
	}
	if h := m.syncSourceWorkers[l.ID]; h != nil {
		h.cancel()
	}
	if cancel := m.storageExportCancels[l.ID]; cancel != nil {
		cancel()
	}

	l.State = "needs_attention"
	l.RecoveryReason = "lease_clock_uncertain"
	l.Failure = l.Failure + "; Elapsed deadline provenance is unavailable; entitlement is uncertain and all obligations remain charged pending explicit reconciliation"
	l.Revision++
	return m.put(l, "lease.clock_uncertain")
}
func (m *Manager) reconcileLeaseExpiries() error {
	s, err := m.readLeaseClock()
	if err != nil {
		return err
	}
	ls, err := m.list()
	if err != nil {
		return err
	}
	if err = m.checkpointLeaseClock(s); err != nil {
		return err
	}
	for _, l := range ls {
		if l.State != "active" && l.State != "provisioning" {
			continue
		}
		remaining, e := l.Clock.Remaining(s, l.Expires)
		if e != nil {
			if e = m.retainLeaseClock(l); e != nil {
				return e
			}
		} else if remaining == 0 {
			if _, e = m.end(l.ID, "expired"); e != nil {
				return e
			}
		}
	}
	return nil
}

// Original provider/process recovery runs first. Clock uncertainty is additive:
// it must not erase the outcome that explicit resource recovery requires.
func (m *Manager) retainRecoveredLeaseClocks() error {
	leases, err := m.list()
	if err != nil {
		return err
	}
	for _, l := range leases {
		if l.State != "active" && l.State != "provisioning" && l.State != "needs_attention" {
			continue
		}
		if _, err := m.leaseRemaining(l); err != nil {
			if err = m.retainLeaseClock(l); err != nil {
				return err
			}
		}
	}
	return nil
}
