package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/usage"
)

func leaseDependencyRank(l model.Lease) int {
	if l.Request.Kind == "storage" || l.Request.Kind == "ports" {
		return 2
	}
	if l.Request.Profile == "gradle-service" {
		return 1
	}
	return 0
}

func (m *Manager) endGradleConsumers(parent model.Lease, outcome string) error {
	return m.endGradleConsumersForCause(parent, outcome, nil)
}
func (m *Manager) endGradleConsumersForCause(parent model.Lease, outcome string, expiry *automaticExpiryEnding) error {
	if parent.Request.Profile != "gradle-service" {
		return nil
	}
	leases, err := m.list()
	if err != nil {
		return err
	}
	for _, child := range leases {
		if child.Request.ServiceLease != parent.ID || (!consuming(child.State) && child.State != "queued") {
			continue
		}
		var err error
		if expiry == nil {
			_, err = m.end(child.ID, outcome)
		} else {
			_, err = m.endAutomaticExpiry(child.ID, outcome, expiry.dependent(parent.ID))
		}
		if err != nil {
			var api *model.Error
			if !errors.As(err, &api) || api.Code != "needs_attention" {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) gradleRenew(l model.Lease, ttl time.Duration) error {
	if l.Request.ServiceLease != "" {
		r := l.Request
		r.TTL = ttl
		_, err := m.gradleParent(r, l.ID)
		return err
	}
	if l.Request.Profile == "gradle-service" && (l.Workload == nil || l.Workload.State != "ready") {
		return model.Err("service_unavailable", "only a verified ready Gradle service can be renewed")
	}
	if l.Request.Profile == "gradle-service" {
		leases, err := m.list()
		if err != nil {
			return err
		}
		sample, err := m.readLeaseClock()
		if err != nil {
			return err
		}
		until := sample.Tick + int64(ttl)
		for _, child := range leases {
			if child.Request.ServiceLease != l.ID || !consuming(child.State) {
				continue
			}
			_, childClockErr := child.Clock.Remaining(sample, child.Expires)
			if childClockErr != nil || child.Clock.End > until {
				return model.Err("service_in_use", "service renewal cannot shorten its lifetime across a consuming lease's deadline or uncertain clock")
			}
		}
	}
	return nil
}

func (m *Manager) blockGradleParent(consumer model.Lease, detail string) error {
	if consumer.Request.ServiceLease == "" {
		return nil
	}
	parent, err := m.get(consumer.Request.ServiceLease)
	if err != nil {
		return err
	}
	r, err := m.gradleRecord(parent.ID)
	if err != nil {
		return err
	}
	r.Uncertain, r.BlockedBy = true, consumer.ID
	return m.blockGradleService(&parent, r, detail)
}

func (m *Manager) restoreGradleParent(consumer model.Lease) error {
	parent, err := m.get(consumer.Request.ServiceLease)
	if err != nil || parent.State != "active" {
		return err
	}
	r, err := m.gradleRecord(parent.ID)
	if err != nil || r.BlockedBy != consumer.ID {
		return err
	}
	j := m.jobs[parent.ID]
	if j == nil || j.identity == nil {
		return nil
	}
	current, err := usage.Capture(j.identity.PID)
	if err != nil || current != *j.identity {
		return nil
	}
	if _, err := m.gradleFinishedEvidence(r); err != nil {
		return nil
	}
	r.BlockedBy, r.Uncertain, r.Phase = "", false, "ready"
	r.Binding.State = "ready"
	r.Binding.Detail = "Owned daemon verified ready; previous consumer completion evidence reconciled. Service/worker promise remains charged between clients."
	parent.Failure = ""
	return m.saveGradleRecord(&parent, r, "workload.service_recovered")
}

func gradleGroupGone(pid int) bool {
	return pid <= 0 || errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH)
}

func (m *Manager) finishGradleJob(l *model.Lease, groupGone bool) error {
	if !m.gradleObligation(*l) {
		return nil
	}
	r, err := m.gradleRecord(l.ID)
	if err != nil {
		l.State, l.Outcome, l.Ended = "needs_attention", "gradle_work_unreconciled", nil
		l.Failure = "Gradle workload record unavailable; obligations remain charged"
		return err
	}
	if r.FinalOutcome == "" {
		r.FinalOutcome = l.Outcome
	}
	if r.Binding.Role == "consumer" {
		_, proofErr := m.gradleFinishedEvidence(r)
		if groupGone && proofErr == nil {
			r.Phase, r.Binding.State, r.Uncertain = "finished", "settled", false
			if err := m.saveGradleRecord(l, r, "workload.consumer_settled"); err != nil {
				l.State, l.Outcome, l.Ended = "needs_attention", "gradle_work_unreconciled", nil
				return err
			}
			return m.restoreGradleParent(*l)
		}
		r.Uncertain, r.Phase, r.Binding.State = true, "unresolved", "unresolved"
		l.State, l.Outcome, l.Ended = "needs_attention", "gradle_work_unreconciled", nil
		l.Failure = fmt.Sprintf("Client exit did not prove Gradle work stopped (client group absent: %t; completion: %v); service and storage remain protected", groupGone, proofErr)
		r.Binding.Detail = l.Failure
		if err := m.saveGradleRecord(l, r, "workload.consumer_unresolved"); err != nil {
			return err
		}
		return m.blockGradleParent(*l, "Consumer "+l.ID+" has unresolved Gradle work; reuse is blocked")
	}
	if err := m.endGradleConsumers(*l, "revoked"); err != nil {
		l.State, l.Outcome, l.Ended = "needs_attention", "gradle_service_unreconciled", nil
		l.Failure = "Service exited but dependent consumer termination did not persist: " + err.Error()
		return err
	}
	if r.Phase == "probing" {
		if _, err := m.gradleFinishedEvidence(r); err != nil {
			r.Uncertain = true
		}
	}
	leases, err := m.list()
	if err != nil {
		return err
	}
	children := false
	for _, child := range leases {
		if child.Request.ServiceLease == l.ID && consuming(child.State) {
			children = true
		}
	}
	if !groupGone || r.Uncertain || children {
		l.State, l.Outcome, l.Ended = "needs_attention", "gradle_service_unreconciled", nil
		l.Failure = "Owned service group or dependent work remains unresolved; no daemon PID was adopted or signalled from a log/receipt"
		r.Binding.State, r.Binding.Detail, r.Phase = "unresolved", l.Failure, "unresolved"
	} else {
		r.Binding.State, r.Phase = "stopped", "finished"
	}
	return m.saveGradleRecord(l, r, "workload.service_settled")
}

func (m *Manager) gradleStorageIdle(id string) error {
	store, err := m.get(id)
	if err != nil {
		return err
	}
	if m.providerFor(store) == nil || store.Artifact == nil || store.Artifact.Phase != "owned" {
		return model.Err("needs_attention", "managed Gradle workspace ownership is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	survey, err := m.providerFor(store).Survey(ctx, *store.Artifact)
	if err != nil || len(survey.Active) != 0 {
		return model.Err("needs_attention", fmt.Sprintf("managed Gradle workspace remains active or uncertain (observation: %v, active PIDs: %v)", err, survey.Active))
	}
	return nil
}

// Reconciliation observes only. Saved native identities, receipt PIDs and paths
// never authorize a signal after supervision has been lost.
func (m *Manager) reconcileGradle(l model.Lease, outcome string) (model.Lease, error) {
	if m.jobs[l.ID] != nil || !gradleGroupGone(l.PID) {
		return l, model.Err("needs_attention", "Gradle client/service group remains supervised, present or unavailable; no signal was sent")
	}
	r, exists, err := m.gradleRecordOptional(l.ID)
	if err != nil {
		return l, err
	}
	if !exists && l.PID != 0 {
		return l, model.Err("needs_attention", "Gradle workload evidence is missing")
	}
	if exists && r.Binding.Role == "consumer" {
		if _, err := m.gradleFinishedEvidence(r); err != nil {
			parent, pe := m.get(l.Request.ServiceLease)
			if pe != nil || m.jobs[parent.ID] != nil || !gradleGroupGone(parent.PID) {
				return l, model.Err("needs_attention", "Gradle completion is unverified and its service may still execute work")
			}
			if err := m.gradleStorageIdle(parent.Request.StorageLease); err != nil {
				return l, err
			}
			if err := m.gradleStorageIdle(l.Request.StorageLease); err != nil {
				return l, err
			}
		}
	} else if exists && r.Binding.Role == "service" {
		leases, err := m.list()
		if err != nil {
			return l, err
		}
		for _, child := range leases {
			if child.Request.ServiceLease == l.ID && consuming(child.State) {
				if _, err := m.reconcileGradle(child, "reconciled"); err != nil {
					return l, err
				}
			}
		}
		if r.Uncertain || l.PID != 0 {
			if err := m.gradleStorageIdle(l.Request.StorageLease); err != nil {
				return l, err
			}
		}
	}
	if outcome == "" {
		outcome = "reconciled"
	}
	now := time.Now().UTC()
	l.State, l.Outcome, l.Ended, l.Failure = "terminal", outcome, &now, ""
	if exists {
		r.Uncertain, r.Phase, r.Binding.State = false, "finished", "settled"
		r.Binding.Detail = "Verified idle/completion evidence reconciled; execution obligation settled. No exclusive physical storage reclamation is inferred."
		if err := m.saveGradleRecord(&l, r, "workload.reconciled"); err != nil {
			return l, err
		}
		if r.Binding.Role == "consumer" {
			_ = m.restoreGradleParent(l)
		}
		return l, nil
	}
	l.Revision++
	return l, m.put(l, "lease."+outcome)
}

func (m *Manager) tickGradleWorkloads(leases []model.Lease) {
	for _, l := range leases {
		if !isGradleProfile(l.Request.Profile) || l.Workload == nil {
			continue
		}
		if l.Request.Profile == "gradle-service" && l.State == "active" && l.Workload.State == "starting" {
			r, err := m.gradleRecord(l.ID)
			if err != nil {
				continue
			}
			if l.Started != nil && time.Since(*l.Started) > 30*time.Second {
				_ = m.blockGradleService(&l, r, "Gradle foreground daemon did not reach readiness within 30 seconds; no application consumer authorized")
				continue
			}
			if ready, err := m.gradleServerAnnounced(l); err == nil && ready {
				_ = m.startGradleProbe(l, r, m.jobs[l.ID])
			}
		} else if l.Request.Profile == "gradle-build" && l.State == "needs_attention" && m.jobs[l.ID] == nil && gradleGroupGone(l.PID) {
			r, err := m.gradleRecord(l.ID)
			if err != nil {
				continue
			}
			if _, err := m.gradleFinishedEvidence(r); err == nil {
				_, _ = m.reconcileGradle(l, r.FinalOutcome)
			} else if !os.IsNotExist(err) {
				// Malformed/mismatched evidence never becomes zero use or retries.
				continue
			}
		}
	}
}
