package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/yrashk/arm/internal/model"
)

// An sshLeaseHold is a current in-process obligation, never recovered from a
// descriptor. Native readiness lets Serve finish; settlement additionally needs
// its positive connection/copy/callback JoinTransport witness.
type sshLeaseHold struct {
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
	h := &sshLeaseHold{manager: m, runtime: r, original: original, ready: make(chan struct{}), done: make(chan struct{})}
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

// stageSSHResult never stores terminal while the original transport is live.
// The exact charged intermediate row fences later join settlement.
func (m *Manager) stageSSHResult(l model.Lease) (model.Lease, error) {
	h := m.sshTransports[l.ID]
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
