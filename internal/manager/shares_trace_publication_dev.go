//go:build arm_devtrace

package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/yrashk/arm/internal/sharelease"
)

type publicationNative struct {
	Status  string `json:"status"`
	Count   int    `json:"count"`
	Joined  bool   `json:"joined"`
	Skipped bool   `json:"skipped"`
}
type publicationFormalState struct {
	Schema        string                       `json:"schema"`
	Action        string                       `json:"action"`
	Phase         string                       `json:"phase"`
	Worker        string                       `json:"worker"`
	Scope         string                       `json:"scope"`
	Revision      int64                        `json:"revision"`
	Slots         int                          `json:"slots"`
	Original      bool                         `json:"original"`
	Intent        bool                         `json:"intent"`
	Published     bool                         `json:"published"`
	Retired       bool                         `json:"retired"`
	CachePhase    string                       `json:"cache_phase"`
	CacheRevision int64                        `json:"cache_revision"`
	Boundary      string                       `json:"boundary"`
	Lease         string                       `json:"lease"`
	Charged       bool                         `json:"charged"`
	Pin           bool                         `json:"pin"`
	Handle        string                       `json:"handle"`
	Native        map[string]publicationNative `json:"native"`
	Comparison    string                       `json:"comparison"`
	Absence       bool                         `json:"absence"`
	Stop          bool                         `json:"stop"`
	Evidence      int                          `json:"evidence"`
	EndDecision   string                       `json:"end_decision"`
	Outcome       string                       `json:"outcome"`
	IdentityOK    bool                         `json:"identity_ok"`
	OriginalOK    bool                         `json:"original_ok"`
	IntentOK      bool                         `json:"intent_ok"`
}
type publicationFormalObserver struct {
	preparedRevision                                int64
	verificationWorker                              *sharelease.Worker
	faultAfter                                      string
	mu                                              sync.Mutex
	key, storage                                    string
	records                                         []publicationFormalState
	events                                          chan publicationFormalState
	gates                                           map[string]chan struct{}
	native                                          map[string]publicationNative
	comparison                                      string
	absent, stopped, offline, recovering            bool
	rollbackBeforeCommit, loseNextAck, lastRollback bool
	first                                           *sharelease.Publication
	original                                        *sharelease.Publication
	intent                                          *sharelease.PublishRequest
	endDecision                                     string
	err                                             error
}

var publicationFormalObservers sync.Map // private dev tests only, never env activated
func (m *Manager) publicationFormalObserver() *publicationFormalObserver {
	v, ok := publicationFormalObservers.Load(m.home)
	if !ok {
		return nil
	}
	return v.(*publicationFormalObserver)
}
func publicationFormalPhase(p sharelease.Publication) string {
	if p.State == "verifying" {
		return "verify_" + p.WorkerState
	}
	return shareFormalPhase(p)
}

// Caller holds Manager.mu. SQL records and fixed-size controls only: no native
// traversal, manifest-body decode/hash, callback, output or scheduling barrier.
func (m *Manager) tracePublicationLocked(action, id string) {
	o := m.publicationFormalObserver()
	if o == nil {
		return
	}
	if action == "reserved" || action == "needs_attention" || action == "verify_reserved" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err != nil {
		return
	}
	if action == "restart" && o.recovering && len(o.records) > 0 {
		a := o.records[len(o.records)-1].Action
		if a == "restart_unknown" || a == "restart_rollback_unknown" {
			return
		}
	}
	s := publicationFormalState{Schema: "arm.share-publication/v1", Action: action, Phase: "absent", Worker: "absent", Scope: "preparation", CachePhase: "absent", Boundary: "healthy", Lease: "absent", Handle: "none", Comparison: o.comparison, Absence: o.absent, Stop: o.stopped, EndDecision: o.endDecision, Outcome: "none", IdentityOK: true, OriginalOK: true, IntentOK: true, Native: map[string]publicationNative{}}
	if s.Comparison == "" {
		s.Comparison = "none"
	}
	if s.EndDecision == "" {
		s.EndDecision = "none"
	}
	for _, scope := range []string{"preparation", "verification"} {
		n := o.native[scope]
		if n.Status == "" {
			n.Status = "never"
		}
		s.Native[scope] = n
	}
	if m.sharePolicyError() != nil {
		s.Boundary = "unavailable"
		o.stopped = true
		s.Stop = true
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
			o.err = fmt.Errorf("publication trace key/identity mismatch")
			return
		}
		if err == nil {
			var p sharelease.Publication
			// Decode observed bounded bytes without Validate: invalid safety fields must
			// reach TLC rather than being repaired or rejected by a duplicate invariant.
			if err = json.Unmarshal(raw, &p); err != nil {
				o.err = err
				return
			}
			if id != "" && p.ID != id {
				o.err = fmt.Errorf("publication trace identity differs")
				return
			}
			l, err := m.get(p.Lease)
			if err != nil {
				o.err = err
				return
			}
			s.Phase = publicationFormalPhase(p)
			s.Worker = p.WorkerState
			s.Scope = p.WorkerPhase
			s.Revision = p.Revision
			s.Slots = p.JournalSlots
			s.Original = p.Summary != nil
			s.Intent = p.Publish != nil
			s.Published = p.Published != nil
			s.Retired = p.Retired != nil
			s.Lease = l.State
			s.Charged = consuming(l.State)
			if l.Outcome != "" {
				s.Outcome = l.Outcome
			}
			sourceOK := o.storage == "" || o.storage == p.Request.Storage
			if o.storage == "" {
				o.storage = p.Request.Storage
			}
			if cached, ok := m.shares.state.Publications[p.ID]; ok {
				s.CachePhase = publicationFormalPhase(cached)
				s.CacheRevision = cached.Revision
			}
			if m.shares.workers[p.ID] != nil {
				s.Handle = p.WorkerPhase
			}
			if o.first == nil {
				q := clonePublication(p)
				o.first = &q
			}
			first := o.first
			s.IdentityOK = sourceOK && p.PreparationWorker.Validate(p.Owner) == nil && p.Worker.Validate(p.Owner) == nil && (m.host == "" || p.Authority == m.host) && (m.owner == "" || p.Owner == m.owner) && p.Schema == 2 && p.Authority == first.Authority && p.Owner == first.Owner && p.ID == first.ID && p.Generation == first.Generation && p.Request == first.Request && p.Lease == first.Lease && p.StorageIdentity == first.StorageIdentity && p.PreparationWorker == first.PreparationWorker && p.Created.Equal(first.Created) && p.Expires.Equal(first.Expires)
			s.IdentityOK = s.IdentityOK && p.Request.Resources.CPU >= 1 && p.Request.Resources.Memory >= sharelease.PreparationMemory().Total && l.Expires != nil && l.Expires.Equal(p.Expires) && l.ID == p.Lease && l.Host == p.Authority && l.Owner == p.Owner && l.Request.Key == shareLeaseKey(l.Host, p.Request.Key) && l.Request.Resources == p.Request.Resources && l.Request.StorageLease == p.Request.Storage && l.Request.Reason == p.Request.Reason && l.Request.TTL == p.Request.TTL && l.Request.Budget == p.Request.Budget
			if p.Summary != nil && o.original == nil {
				q := clonePublication(p)
				o.original = &q
				if o.preparedRevision == 0 {
					o.preparedRevision = p.Revision
				}
			}
			if o.original != nil {
				s.OriginalOK = p.Summary != nil && reflect.DeepEqual(p.Summary, o.original.Summary) && reflect.DeepEqual(p.Prepared, o.original.Prepared)
			}
			if p.Publish != nil && o.verificationWorker == nil {
				w := p.Worker
				o.verificationWorker = &w
			}
			if p.WorkerPhase == "preparation" {
				s.IdentityOK = s.IdentityOK && p.Worker == p.PreparationWorker
			} else if o.verificationWorker != nil {
				s.IdentityOK = s.IdentityOK && p.Worker == *o.verificationWorker
			}
			if p.Publish != nil && o.intent == nil {
				q := *p.Publish
				o.intent = &q
			}
			if o.intent != nil {
				s.IntentOK = p.Publish != nil && *p.Publish == *o.intent
				if p.Publish != nil {
					s.IntentOK = s.IntentOK && p.Publish.Schema == 1 && p.Publish.Share == p.ID && p.Publish.Generation == p.Generation && p.Publish.Revision == o.preparedRevision && p.Summary != nil && p.Publish.PreparedSHA256 == p.Summary.SHA256
				}
			}
			if p.State == "retiring" && o.endDecision == "" {
				o.endDecision = s.Outcome
				s.EndDecision = s.Outcome
			}
			if p.State == "ending" {
				o.stopped = true
				s.Stop = true
			}
			for _, c := range m.shares.state.Consumers {
				if c.Admission.Control.Share == p.ID {
					s.Evidence++
				}
			}
			for _, f := range m.shares.state.Fences {
				if f.Control.Share == p.ID {
					s.Evidence++
				}
			}
			if s.Evidence > 2 {
				s.Evidence = 2
			} // finite 0 / one / at-least-two history classes
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
		s.CachePhase = "absent"
		s.CacheRevision = 0
		s.Pin = s.Charged
		s.Handle = "none"
	}
	switch action {
	case "worker_started", "verify_started":
		s.Action = "worker_start"
	case "commit_unknown":
		if o.recovering {
			s.Action = "restart_unknown"
			if o.lastRollback {
				s.Action = "restart_rollback_unknown"
			}
		} else if o.lastRollback {
			s.Action = "commit_unavailable"
		} else {
			label := map[string]string{"reserved": "admit", "running": "worker_start", "verify_reserved": "verify_intent", "verify_running": "worker_start", "prepared": "prepared", "published": "published", "ending": "ending", "cancelled": "cancelled", "failed": "failed", "retiring": "retiring", "retired": "retired"}[s.Phase]
			if label == "" {
				o.err = fmt.Errorf("unknown publication commit phase %s", s.Phase)
				return
			}
			s.Action = label + "_unknown"
		}
		o.lastRollback = false
	case "restart":
		if len(o.records) == 0 && s.Phase == "absent" {
			s.Action = "init"
		}
	}
	if len(o.records) >= 256 {
		o.err = fmt.Errorf("publication trace exceeds256 records")
		return
	}
	o.records = append(o.records, s)
	if o.faultAfter == action {
		o.loseNextAck = true
		o.faultAfter = ""
	}
	select {
	case o.events <- s:
	default:
		o.err = fmt.Errorf("publication trace receiver did not drain bounded events")
	}
}
func (m *Manager) tracePublicationNative(id, event string) {
	o := m.publicationFormalObserver()
	if o == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.shares.state.Publications[id]
	if !ok {
		return
	}
	o.mu.Lock()
	if event == "native_skipped" && m.sharePolicyError() != nil {
		o.mu.Unlock()
		return
	}
	n := o.native[p.WorkerPhase]
	if n.Status == "" {
		n.Status = "never"
	}
	if o.native == nil {
		o.native = map[string]publicationNative{}
	}
	switch event {
	case "native_begin":
		n.Count++
		n.Status = "running"
	case "native_join":
		n.Joined = true
		n.Status = "joined"
	case "native_skipped":
		n.Skipped = true
		n.Status = "skipped"
	case "observed_absent":
		o.absent = true
	default:
		o.err = fmt.Errorf("unknown publication native checkpoint")
	}
	o.native[p.WorkerPhase] = n
	o.mu.Unlock()
	m.tracePublicationLocked(event, id)
}
func (m *Manager) tracePublicationCompare(id, result string) {
	o := m.publicationFormalObserver()
	if o == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o.mu.Lock()
	o.comparison = result
	o.mu.Unlock()
	m.tracePublicationLocked("compare", id)
}
func (m *Manager) tracePublicationBarrier(ctx context.Context, id, point string) {
	o := m.publicationFormalObserver()
	if o == nil {
		return
	}
	m.mu.Lock()
	p := m.shares.state.Publications[id]
	m.mu.Unlock()
	o.mu.Lock()
	gate := o.gates[p.WorkerPhase+":"+point]
	o.mu.Unlock()
	if gate == nil {
		return
	}
	select {
	case <-gate:
	case <-time.After(10 * time.Second):
		o.mu.Lock()
		o.err = fmt.Errorf("publication barrier timed out: %s/%s", p.WorkerPhase, point)
		o.mu.Unlock()
	}
}
func (m *Manager) tracePublicationBeforeCommit(tx *sql.Tx) {
	o := m.publicationFormalObserver()
	if o == nil {
		return
	}
	o.mu.Lock()
	rollback, lose := o.rollbackBeforeCommit, o.loseNextAck
	o.rollbackBeforeCommit = false
	o.loseNextAck = false
	o.lastRollback = rollback
	o.mu.Unlock()
	if rollback {
		_ = tx.Rollback()
	}
	if lose {
		m.shares.commitAckFailure = errors.New("dev publication lost durable acknowledgment")
	}
}
