//go:build arm_devtrace

package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/yrashk/arm/internal/sharelease"
)

// shareFormalState is the same closed record emitted by TLC. Durable/cache
// fields are observed from real SQL/Manager objects, not advanced by a Go Next
// function. Runtime facts are counted only at actual native entry/join/no-start
// checkpoints. They never authorize production behavior.
type shareFormalState struct {
	Schema        string `json:"schema"`
	Action        string `json:"action"`
	Phase         string `json:"phase"`
	CachePhase    string `json:"cache_phase"`
	CacheRevision int64  `json:"cache_revision"`
	Boundary      string `json:"boundary"`
	Worker        string `json:"worker"`
	Lease         string `json:"lease"`
	Charged       bool   `json:"charged"`
	Pin           bool   `json:"pin"`
	Native        string `json:"native"`
	Launches      int    `json:"launches"`
	JoinKnown     bool   `json:"join_known"`
	AbsenceProved bool   `json:"absence_proved"`
	SkippedProved bool   `json:"skipped_proved"`
	Revision      int64  `json:"revision"`
	Slots         int    `json:"slots"`
	Outcome       string `json:"outcome"`
	Manifest      bool   `json:"manifest"`
}

type shareFormalObserver struct {
	mu                      sync.Mutex
	key                     string
	storage                 string
	records                 []shareFormalState
	events                  chan shareFormalState
	gates                   map[string]chan struct{}
	native                  string
	launches                int
	joined, absent, skipped bool
	rollbackBeforeCommit    bool
	lastRollback            bool
	loseNextAck             bool
	offline, recovering     bool
	err                     error
}

var shareFormalObservers sync.Map // home -> private dev-test observer, never env activated

func shareFormalPhase(p sharelease.Publication) string {
	switch p.State {
	case "preparing":
		if p.WorkerState == "reserved" {
			return "reserved"
		}
		if p.WorkerState == "running" {
			return "running"
		}
	case "needs_attention":
		return "unknown"
	default:
		return p.State
	}
	return "invalid"
}
func (m *Manager) shareFormalObserver() *shareFormalObserver {
	v, ok := shareFormalObservers.Load(m.home)
	if !ok {
		return nil
	}
	return v.(*shareFormalObserver)
}

// Caller holds Mu. Trace append is bounded memory only: no log/file writes,
// user callback, native filesystem inspection or barrier while holding Mu.
func (m *Manager) traceShareLocked(action, id string) {
	m.tracePublicationLocked(action, id)
	o := m.shareFormalObserver()
	if o == nil {
		return
	}
	if action == "reserved" || action == "needs_attention" {
		return
	} // admission registered below; startup emits one final snapshot
	o.mu.Lock()
	defer o.mu.Unlock()
	if action == "restart" && o.recovering && len(o.records) > 0 && (o.records[len(o.records)-1].Action == "restart_unknown" || o.records[len(o.records)-1].Action == "restart_rollback_unknown") {
		return
	}
	if o.err != nil {
		return
	}
	s := shareFormalState{Schema: "arm.share-preparation/v1", Action: action, Phase: "absent", CachePhase: "absent", Boundary: "healthy", Worker: "absent", Lease: "absent", Native: o.native, Launches: o.launches, JoinKnown: o.joined, AbsenceProved: o.absent, SkippedProved: o.skipped, Outcome: "none"}
	if s.Native == "" {
		s.Native = "never"
	}
	if m.sharePolicyError() != nil {
		s.Boundary = "unavailable"
	}
	var table int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name='share_publications'`).Scan(&table); err != nil {
		o.err = err
		return
	}
	if table != 0 {
		var raw []byte
		err := m.db.QueryRow(`SELECT CASE WHEN length(CAST(data AS BLOB))<=? THEN data END FROM share_publications WHERE json_extract(data,'$.request.key')=?`, sharelease.MaxRecordBytes, o.key).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			o.err = err
			return
		}
		if errors.Is(err, sql.ErrNoRows) && id != "" {
			o.err = fmt.Errorf("trace key does not identify requested publication")
			return
		}
		if err == nil {
			p, e := sharelease.DecodePublication(raw)
			if e != nil {
				o.err = e
				return
			}
			if id != "" && p.ID != id {
				o.err = fmt.Errorf("trace exact publication identity differs")
				return
			}
			l, e := m.get(p.Lease)
			if e != nil {
				o.err = e
				return
			}
			// Correlate immutable identity/control only. Do not pre-validate the
			// phase/lease safety invariant: an actual early-credit-release defect
			// must be emitted for TLC to reject, rather than hidden by the observer.
			if l.ID != p.Lease || l.Host != p.Authority || l.Owner != p.Owner || l.Request.Key != shareLeaseKey(l.Host, p.Request.Key) || l.Request.Resources != p.Request.Resources || l.Request.StorageLease != p.Request.Storage || l.Request.Reason != p.Request.Reason || l.Request.TTL != p.Request.TTL || l.Request.Budget != p.Request.Budget {
				o.err = fmt.Errorf("trace immutable lease identity/control differs")
				return
			}
			s.Phase = shareFormalPhase(p)
			s.Worker = p.WorkerState
			s.Lease = l.State
			s.Charged = consuming(l.State)
			s.Revision = p.Revision
			s.Slots = p.JournalSlots
			s.Manifest = p.Summary != nil
			if l.Outcome != "" {
				s.Outcome = l.Outcome
			}
			if cached, ok := m.shares.state.Publications[p.ID]; ok {
				s.CachePhase = shareFormalPhase(cached)
				s.CacheRevision = cached.Revision
			}
			o.storage = p.Request.Storage
		}
	}
	if o.storage != "" {
		busy, e := m.shareStorageBusy(o.storage)
		s.Pin = e != nil || busy != ""
	} else {
		s.Pin = s.Boundary == "unavailable"
	}
	if o.offline {
		s.Boundary = "offline"
		s.CachePhase, s.CacheRevision = "absent", 0
		s.Pin = s.Charged
	}
	switch action {
	case "worker_started":
		s.Action = "worker_start"
	case "commit_unknown":
		if o.recovering {
			s.Action = "restart_unknown"
			if o.lastRollback {
				s.Action = "restart_rollback_unknown"
				o.lastRollback = false
			}
			break
		}
		if o.lastRollback {
			s.Action = "commit_unavailable"
			o.lastRollback = false
			break
		}
		switch s.Phase {
		case "reserved":
			s.Action = "admit_unknown"
		case "running":
			s.Action = "worker_start_unknown"
		case "ending":
			s.Action = "ending_unknown"
		case "prepared":
			s.Action = "prepared_unknown"
		case "cancelled":
			s.Action = "cancelled_unknown"
		case "failed":
			s.Action = "failed_unknown"
		default:
			s.Action = "commit_unavailable"
		}
	case "restart":
		if len(o.records) == 0 && s.Phase == "absent" {
			s.Action = "init"
		}
	}
	if len(o.records) >= 256 {
		o.err = fmt.Errorf("dev trace exceeded256 events")
		return
	}
	o.records = append(o.records, s)
	select {
	case o.events <- s:
	default:
		o.err = fmt.Errorf("dev trace receiver did not drain bounded events")
	}
}

func (m *Manager) traceShareNative(id, event string) {
	m.tracePublicationNative(id, event)
	o := m.shareFormalObserver()
	if o == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o.mu.Lock()
	switch event {
	case "native_begin":
		o.launches++
		o.native = "running"
	case "native_join":
		o.native = "joined"
		o.joined = true
	case "native_skipped":
		if m.sharePolicyError() != nil {
			o.mu.Unlock()
			return
		}
		o.native = "skipped"
		o.skipped = true
	case "observed_absent":
		o.absent = true
	default:
		o.err = fmt.Errorf("unknown native trace checkpoint")
	}
	o.mu.Unlock()
	m.traceShareLocked(event, id)
}
func (m *Manager) traceShareBarrier(ctx context.Context, id, point string) {
	m.tracePublicationBarrier(ctx, id, point)
	o := m.shareFormalObserver()
	if o == nil {
		return
	}
	o.mu.Lock()
	gate := o.gates[point]
	o.mu.Unlock()
	if gate == nil {
		return
	}
	select {
	case <-gate:
	case <-time.After(10 * time.Second):
		o.mu.Lock()
		o.err = fmt.Errorf("dev scheduling barrier timed out: %s", point)
		o.mu.Unlock()
	}
}
func (m *Manager) traceShareBeforeCommit(tx *sql.Tx) {
	m.tracePublicationBeforeCommit(tx)
	o := m.shareFormalObserver()
	if o == nil {
		return
	}
	o.mu.Lock()
	rollback := o.rollbackBeforeCommit
	lose := o.loseNextAck
	o.rollbackBeforeCommit = false
	o.loseNextAck = false
	o.lastRollback = rollback
	o.mu.Unlock()
	if rollback {
		_ = tx.Rollback()
	} // actual no-durability Commit error, distinct from lost durable acknowledgment
	if lose {
		m.shares.commitAckFailure = errors.New("dev trace: lost durable acknowledgment")
	}
}
