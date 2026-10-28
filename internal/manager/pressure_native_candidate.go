package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/pressure"
)

// pressureNativeCandidate is a process-local proposal, not signal authority.
// The controller must re-read the lease, recheck this exact job pointer and
// original owner under Manager.mu, then fence the effect against the durable
// pressure action and current elapsed entitlement. Never persist job or infer
// it from a recovered PID.
type pressureNativeCandidate struct {
	id            pressure.JobID
	job           *job
	leaseRevision int
	remaining     time.Duration
	policyDigest  [32]byte
}

// pressureNativeGrant is minted only for an exact current original job and
// resolved CPU policy. It lives on that job, never in a lease row or journal.
// A later policy/revision/epoch change invalidates it; no recovered process
// can reconstruct it. The policy source and durable grant decision still need
// central integration before this can be used by the runtime.
type pressureNativeGrant struct {
	leaseID       string
	leaseRevision int
	owner         *job
	policyDigest  [32]byte
	epoch         string
}

func pressurePolicyHash(p pressure.Policy) ([32]byte, bool) {
	b, err := json.Marshal(p)
	if err != nil {
		return [32]byte{}, false
	}
	return sha256.Sum256(b), true
}

// bindPressureNativeGrant is an unused source cut. Its future caller must
// supply a resolved, authorized profile policy, not a request-controlled flag.
// Caller holds Manager.mu.
func (m *Manager) bindPressureNativeGrant(id string, policy pressure.Policy) bool {
	threshold, cpuEnabled := policy.Resources[pressure.CPU]
	if id == "" || !cpuEnabled || !threshold.AllowPause || policy.MaxPauseJobs != 1 || m.closing || m.draining {
		return false
	}
	l, err := m.get(id)
	if err != nil || !m.pressureGenericExecution(l) {
		return false
	}
	j := m.jobs[id]
	if j == nil || j.sshRuntime != nil || j.originalCancellationRequested || m.sshTransports[id] != nil ||
		m.checkpointExpiryJobBinding(id, l, j) != nil || !j.childOwner.OwnsUnreaped(j.cmd) {
		return false
	}
	remaining, err := m.leaseRemaining(l)
	if err != nil || remaining <= 0 {
		return false
	}
	digest, ok := pressurePolicyHash(policy)
	if !ok || m.pressureEpoch == "" {
		return false
	}
	j.pressureGrant = &pressureNativeGrant{leaseID: id, leaseRevision: l.Revision, owner: j, policyDigest: digest, epoch: m.pressureEpoch}
	return true
}

func (m *Manager) pressureGenericExecution(l model.Lease) bool {
	id := l.ID
	return id != "" && l.State == "active" && l.Host == m.host && l.Owner == m.owner &&
		(l.Request.Kind == "" || l.Request.Kind == "execution") && l.Request.Profile == "" &&
		l.Request.ServiceLease == "" && l.Request.StorageLease == "" && l.Request.PortLease == "" &&
		l.Request.ForwardSource == nil && len(l.Request.Ports) == 0 && len(l.Request.Pools) == 0 &&
		l.Cargo == nil && l.Workload == nil && m.cargoWorkers[id] == nil && m.syncWorkers[id] == nil &&
		m.syncSourceWorkers[id] == nil && m.relayTransits[id] == nil && m.forwardListeners[id] == nil
}

// pressureOriginalNativeCandidate selects only a current generic execution
// whose original child owner is still registered. The caller supplies a
// resolved pressure policy and the process-local grant for this execution;
// neither a lease's requested resources nor its observed PID grants permission.
// This gate is unused while MaxPauseJobs remains zero.
// Caller holds Manager.mu.
func (m *Manager) pressureOriginalNativeCandidate(id string, policy pressure.Policy) (pressureNativeCandidate, bool) {
	var none pressureNativeCandidate
	threshold, cpuEnabled := policy.Resources[pressure.CPU]
	if id == "" || !cpuEnabled || !threshold.AllowPause || policy.MaxPauseJobs != 1 ||
		m.closing || m.draining || m.pressureEpoch == "" {
		return none, false
	}
	l, err := m.get(id)
	if err != nil || l.ID != id || !m.pressureGenericExecution(l) {
		return none, false
	}
	j := m.jobs[id]
	if j == nil || j.sshRuntime != nil || j.originalCancellationRequested || m.sshTransports[id] != nil ||
		m.checkpointExpiryJobBinding(id, l, j) != nil || !j.childOwner.OwnsUnreaped(j.cmd) {
		return none, false
	}
	digest, ok := pressurePolicyHash(policy)
	grant := j.pressureGrant
	if !ok || grant == nil || grant.owner != j || grant.leaseID != id || grant.leaseRevision != l.Revision ||
		grant.epoch != m.pressureEpoch || grant.policyDigest != digest {
		return none, false
	}
	remaining, err := m.leaseRemaining(l)
	if err != nil || remaining <= 0 {
		return none, false
	}
	// The digest is only a bounded governor identity. Its source is the exact
	// captured process identity already matched to the live original owner.
	identity, err := json.Marshal(j.identity)
	if err != nil {
		return none, false
	}
	sum := sha256.Sum256(identity)
	return pressureNativeCandidate{
		id:  pressure.JobID{LeaseID: id, ExecutionID: id, Generation: hex.EncodeToString(sum[:]), OwnershipID: m.pressureEpoch},
		job: j, leaseRevision: l.Revision, remaining: remaining, policyDigest: digest,
	}, true
}

// pressureCandidateStillCurrent closes a stale-policy, renewal, revocation,
// expiry or owner-replacement window before any future native effect. This
// does not assert that a governor action or signal is authorized.
// Caller holds Manager.mu.
func (m *Manager) pressureCandidateStillCurrent(candidate pressureNativeCandidate, policy pressure.Policy) bool {
	if candidate.job == nil || candidate.id.LeaseID == "" || m.jobs[candidate.id.LeaseID] != candidate.job {
		return false
	}
	current, ok := m.pressureOriginalNativeCandidate(candidate.id.LeaseID, policy)
	return ok && current.id == candidate.id && current.job == candidate.job && current.leaseRevision == candidate.leaseRevision &&
		current.policyDigest == candidate.policyDigest
}
