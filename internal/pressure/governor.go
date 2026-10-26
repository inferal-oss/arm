// Package pressure decides host-pressure responses without observing processes or
// performing effects. The caller must persist the returned state and actions in
// one transaction, then deliver actions through its ordinary guarded outbox.
package pressure

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

type Resource string

const (
	CPU    Resource = "cpu"
	Memory Resource = "memory"
	Disk   Resource = "disk"
)

type Threshold struct {
	// Available and thresholds use the same caller-defined units (for example,
	// millicores or bytes). ExitAbove must exceed EnterBelow.
	EnterBelow uint64
	ExitAbove  uint64
	// Pausing on memory or disk pressure is opt-in and never credits capacity.
	AllowPause bool
}

type Policy struct {
	Resources                       map[Resource]Threshold
	MaxAge, MaxSampleGap            time.Duration
	SustainFor, Grace, WarningEvery time.Duration
	RecoverFor, MinPause, MaxPause  time.Duration
	MaxPauseJobs                    int
	MaxJobs                         int
	PendingTimeout                  time.Duration
}

type Sample struct {
	Known        bool
	ObservedAt   time.Time
	ObservedTick time.Duration
	Epoch        string
	Available    uint64
}

// Job is an authority snapshot supplied by the owning provider, not a PID.
// All identity fields must refer to the original captured execution. The
// provider must recheck these fields atomically when applying an action.
type Job struct {
	LeaseID, ExecutionID, Generation, OwnershipID, AgentID string
	Supervised, StillOwned, PauseCapability, PauseAllowed  bool
	Paused, Revoked, Stopping, Acknowledged                bool
	ExpiresAt                                              time.Time
}

type Input struct {
	// Epoch identifies this governor process lifetime. A new epoch discards
	// old process control authority and requires external reconciliation.
	Epoch string
	// Tick is an elapsed, monotonic duration within Epoch and must include
	// suspend time. Now is wall time for freshness and lease expiry only.
	Tick     time.Duration
	Now      time.Time
	Samples  map[Resource]Sample
	Jobs     []Job
	Stopping bool
	// Results are exact provider completions. An ACK from an agent is not a
	// control result. Missing results leave control requests unresolved.
	Results []ControlResult
	// Reconciled names old-epoch obligations independently settled by the
	// owning provider. The caller authenticates these results.
	Reconciled []string
}

type ControlResult struct {
	Key     string
	Applied bool
}

type JobID struct {
	LeaseID, ExecutionID, Generation, OwnershipID string
}

type ActionKind string

const (
	WarnOperator          ActionKind = "warn_operator"
	WarnAgent             ActionKind = "warn_agent"
	RequestCooperation    ActionKind = "request_cooperation"
	RequestPause          ActionKind = "request_pause"
	CancelPause           ActionKind = "cancel_pause"
	RequestResume         ActionKind = "request_resume"
	PauseLimitReached     ActionKind = "pause_limit_reached"
	ControlFailed         ActionKind = "control_failed"
	RequestReconciliation ActionKind = "request_reconciliation"
)

type Action struct {
	// Key is a stable outbox deduplication key within an epoch.
	Key           string
	Kind          ActionKind
	Reason        Resource
	Incident      uint64
	Warning       uint64
	Job           JobID
	AgentID       string
	At            time.Time
	Tick          time.Duration
	Sample        Sample
	Fresh         bool
	Epoch         string
	Revision      uint64
	PolicyDigest  string
	OriginalEpoch string
}

type Episode struct {
	Incident                                                 uint64
	Active, Pressuring, Recovering, Warned                   bool
	PressureSince, LastPressure, RecoverySince, LastRecovery time.Duration
	WarnedAt, LastWarning                                    time.Duration
	LastWarningSample                                        time.Duration
	WarningCount                                             uint64
}

type PauseRecord struct {
	Job                     JobID
	Reason                  Resource
	Incident                uint64
	RequestedAt             time.Duration
	AppliedAt               time.Duration
	PendingSince            time.Duration
	LimitNotified           bool
	FailureNotifiedRevision uint64
	Phase                   PausePhase
	Revision                uint64
	PauseKey                string
	ResumeKey               string
	PolicyDigest            string
}

type UnresolvedControl struct {
	Key         string
	SourceEpoch string
	Record      PauseRecord
}

type PausePhase string

const (
	PausePending   PausePhase = "pause_pending"
	CancelPending  PausePhase = "cancel_pending"
	PauseApplied   PausePhase = "pause_applied"
	ResumePending  PausePhase = "resume_pending"
	ResumeFailed   PausePhase = "resume_failed"
	NeedsAttention PausePhase = "needs_attention"
)

type JobWarning struct {
	Incident uint64
	FirstAt  time.Duration
	Count    uint64
}

type State struct {
	Epoch           string
	LastTick        time.Duration
	LastNow         time.Time
	Resources       map[Resource]Episode
	Pauses          map[JobID]PauseRecord
	JobWarnings     map[Resource]map[JobID]JobWarning
	ControlRevision uint64
	Unresolved      map[string]UnresolvedControl
}

// Advance is pure. Repeat calls with the same state and input return the same
// result. No output action grants authority by itself: the receiver must
// validate current lease, ownership, generation, expiry, and stop state.
func Advance(old State, in Input, p Policy) (State, []Action, error) {
	if err := validate(in, p); err != nil {
		return old, nil, err
	}
	if err := validateStoredState(old, p.MaxJobs); err != nil {
		return old, nil, err
	}
	if old.Epoch != "" && old.Epoch != in.Epoch {
		return changeEpoch(old, in, p)
	}
	s := clone(old)
	for _, key := range in.Reconciled {
		delete(s.Unresolved, key)
	}
	if s.Epoch == "" {
		s.Epoch = in.Epoch
	}
	if in.Tick < s.LastTick || (!s.LastNow.IsZero() && in.Now.Before(s.LastNow)) {
		// A regressed clock cannot prove duration, freshness, or recovery.
		return s, nil, nil
	}
	gap := in.Tick - s.LastTick
	s.LastTick, s.LastNow = in.Tick, in.Now
	jobs := append([]Job(nil), in.Jobs...)
	slices.SortFunc(jobs, func(a, b Job) int { return compareJob(id(a), id(b)) })
	current := make(map[JobID]Job, len(jobs))
	for _, j := range jobs {
		current[id(j)] = j
	}
	for _, warned := range s.JobWarnings {
		for jid := range warned {
			if _, present := current[jid]; !present {
				delete(warned, jid)
			}
		}
	}
	var actions []Action
	digest := policyDigest(p)
	allRecovered := true
	for _, r := range []Resource{CPU, Memory, Disk} {
		threshold, enabled := p.Resources[r]
		if !enabled {
			continue
		}
		e := s.Resources[r]
		sample, supplied := in.Samples[r]
		fresh := supplied && freshSample(sample, in, p.MaxAge)
		if !fresh {
			e.Pressuring, e.Recovering = false, false
			s.Resources[r] = e
			allRecovered = false
			continue
		}
		if gap > p.MaxSampleGap {
			e.Pressuring, e.Recovering = false, false
		}
		if e.Pressuring && (sample.ObservedTick < e.LastPressure || sample.ObservedTick-e.LastPressure > p.MaxSampleGap) {
			e.Pressuring = false
		}
		if e.Recovering && (sample.ObservedTick < e.LastRecovery || sample.ObservedTick-e.LastRecovery > p.MaxSampleGap) {
			e.Recovering = false
		}
		if sample.Available <= threshold.EnterBelow {
			e.Recovering = false
			if !e.Active {
				e.Active = true
				e.Incident++
				e.Warned = false
				e.WarningCount = 0
			}
			if !e.Pressuring {
				e.PressureSince = sample.ObservedTick
				e.Pressuring = true
			}
			e.LastPressure = sample.ObservedTick
			if sample.ObservedTick-e.PressureSince >= p.SustainFor {
				priorWarnings := e.WarningCount
				priorWarningSample := e.LastWarningSample
				priorJobWarnings := make(map[JobID]JobWarning, len(s.JobWarnings[r]))
				for jid, w := range s.JobWarnings[r] {
					priorJobWarnings[jid] = w
				}
				if !e.Warned || (in.Tick-e.LastWarning >= p.WarningEvery && sample.ObservedTick > e.LastWarningSample) {
					e.Warned = true
					if e.WarningCount == 0 {
						e.WarnedAt = in.Tick
					}
					e.LastWarning = in.Tick
					e.LastWarningSample = sample.ObservedTick
					e.WarningCount++
					actions = append(actions, warnings(r, e, jobs, in.Epoch, in.Now)...)
					if s.JobWarnings[r] == nil {
						s.JobWarnings[r] = make(map[JobID]JobWarning)
					}
					for _, j := range jobs {
						if j.Revoked || j.Stopping || !j.StillOwned || !j.ExpiresAt.After(in.Now) {
							continue
						}
						jid := id(j)
						w := s.JobWarnings[r][jid]
						if w.Incident != e.Incident {
							w = JobWarning{Incident: e.Incident, FirstAt: in.Tick}
						}
						w.Count++
						s.JobWarnings[r][jid] = w
					}
				}
				if e.Warned && priorWarnings >= 2 && sample.ObservedTick > priorWarningSample && in.Tick-e.WarnedAt >= p.Grace && threshold.AllowPause && !in.Stopping {
					remaining := p.MaxPauseJobs - len(s.Pauses) - len(s.Unresolved)
					for _, j := range jobs {
						if remaining <= 0 {
							break
						}
						jid := id(j)
						if _, exists := s.Pauses[jid]; exists || j.Paused || !eligible(j, in.Now) {
							continue
						}
						w := priorJobWarnings[jid]
						if w.Incident != e.Incident || w.Count < 2 || in.Tick-w.FirstAt < p.Grace {
							continue
						}
						s.ControlRevision++
						a := action(RequestPause, r, e.Incident, 0, jid, j.AgentID, in.Epoch, s.ControlRevision)
						a.PolicyDigest = digest
						s.Pauses[jid] = PauseRecord{Job: jid, Reason: r, Incident: e.Incident, RequestedAt: in.Tick, PendingSince: in.Tick, Phase: PausePending, Revision: a.Revision, PauseKey: a.Key, PolicyDigest: digest}
						actions = append(actions, a)
						remaining--
					}
				}
			}
		} else if sample.Available >= threshold.ExitAbove {
			e.Pressuring = false
			if !e.Recovering {
				e.RecoverySince = sample.ObservedTick
				e.Recovering = true
			}
			e.LastRecovery = sample.ObservedTick
			if sample.ObservedTick-e.RecoverySince >= p.RecoverFor {
				e.Active, e.Warned, e.Recovering = false, false, false
				e.WarningCount = 0
				delete(s.JobWarnings, r)
			}
		} else {
			// The hysteresis band neither proves ongoing low headroom nor recovery.
			e.Pressuring, e.Recovering = false, false
		}
		if e.Active {
			allRecovered = false
		}
		s.Resources[r] = e
	}
	for r, e := range s.Resources {
		if _, enabled := p.Resources[r]; !enabled && e.Active {
			allRecovered = false
		}
	}
	for _, record := range s.Pauses {
		if _, enabled := p.Resources[record.Reason]; !enabled {
			allRecovered = false
		}
	}
	pausedIDs := make([]JobID, 0, len(s.Pauses))
	for jid := range s.Pauses {
		pausedIDs = append(pausedIDs, jid)
	}
	slices.SortFunc(pausedIDs, compareJob)
	results := make(map[string]ControlResult, len(in.Results))
	for _, result := range in.Results {
		results[result.Key] = result
	}
	for _, jid := range pausedIDs {
		record := s.Pauses[jid]
		j, present := current[jid]
		valid := present && eligible(j, in.Now) && !in.Stopping
		if result, ok := results[record.PauseKey]; ok && (record.Phase == PausePending || record.Phase == CancelPending || (record.Phase == NeedsAttention && record.ResumeKey == "" && record.AppliedAt == 0)) {
			if result.Applied {
				record.Phase = PauseApplied
				record.AppliedAt = in.Tick
			} else if !present || !j.Paused {
				delete(s.Pauses, jid)
				continue
			} else {
				record.Phase = NeedsAttention
			}
		}
		if result, ok := results[record.ResumeKey]; ok && record.ResumeKey != "" && (record.Phase == ResumePending || record.Phase == ResumeFailed || record.Phase == NeedsAttention) {
			if result.Applied && present && !j.Paused {
				delete(s.Pauses, jid)
				continue
			}
			if !result.Applied {
				record.Phase = ResumeFailed
			}
		}
		switch record.Phase {
		case PausePending:
			timedOut := in.Tick-record.PendingSince >= p.PendingTimeout
			if !valid || allRecovered || timedOut || record.PolicyDigest != digest {
				s.ControlRevision++
				record.Phase, record.Revision, record.PendingSince = CancelPending, s.ControlRevision, in.Tick
				actions = append(actions, action(CancelPause, record.Reason, record.Incident, 0, jid, j.AgentID, in.Epoch, record.Revision))
				if timedOut {
					record.Phase = NeedsAttention
				}
			}
		case CancelPending:
			if in.Tick-record.PendingSince >= p.PendingTimeout {
				record.Phase = NeedsAttention
			}
		case PauseApplied:
			if !valid {
				record.Phase = NeedsAttention
				break
			}
			if allRecovered && j.Paused && in.Tick-record.AppliedAt >= p.MinPause {
				s.ControlRevision++
				record.Phase, record.Revision, record.PendingSince = ResumePending, s.ControlRevision, in.Tick
				a := action(RequestResume, record.Reason, record.Incident, 0, jid, j.AgentID, in.Epoch, record.Revision)
				a.PolicyDigest = digest
				record.PolicyDigest = digest
				record.ResumeKey = a.Key
				actions = append(actions, a)
			}
		case ResumePending:
			if !valid {
				record.Phase = NeedsAttention
			}
			if record.PolicyDigest != digest {
				s.ControlRevision++
				record.Revision = s.ControlRevision
				record.Phase = ResumeFailed
			} else if in.Tick-record.PendingSince >= p.PendingTimeout {
				s.ControlRevision++
				record.Revision = s.ControlRevision
				record.Phase = ResumeFailed
			}
		}
		if (record.Phase == NeedsAttention || record.Phase == ResumeFailed) && record.FailureNotifiedRevision != record.Revision {
			actions = append(actions, action(ControlFailed, record.Reason, record.Incident, 0, jid, j.AgentID, in.Epoch, record.Revision))
			record.FailureNotifiedRevision = record.Revision
		}
		if record.AppliedAt > 0 && in.Tick-record.AppliedAt >= p.MaxPause && !record.LimitNotified && record.Phase != ResumePending {
			actions = append(actions, action(PauseLimitReached, record.Reason, record.Incident, 0, jid, j.AgentID, in.Epoch, record.Revision))
			record.LimitNotified = true
		}
		s.Pauses[jid] = record
	}
	for i := range actions {
		actions[i].At, actions[i].Tick = in.Now, in.Tick
		actions[i].Sample = in.Samples[actions[i].Reason]
		actions[i].Fresh = freshSample(actions[i].Sample, in, p.MaxAge)
	}
	return s, actions, nil
}

func freshSample(sample Sample, in Input, maxAge time.Duration) bool {
	return sample.Known && sample.Epoch == in.Epoch && sample.ObservedTick >= 0 && sample.ObservedTick <= in.Tick && in.Tick-sample.ObservedTick <= maxAge && !sample.ObservedAt.IsZero() && !sample.ObservedAt.After(in.Now) && in.Now.Sub(sample.ObservedAt) <= maxAge
}

// ControlAllowed is the last pure fence before a native effect. The caller
// must serialize this check and the effect with Advance, then return an exact
// ControlResult. A stored action or an outbox retry is never sufficient alone.
func ControlAllowed(s State, a Action, in Input, p Policy) bool {
	if validate(in, p) != nil || validateStoredState(s, p.MaxJobs) != nil || (a.Kind != RequestPause && a.Kind != RequestResume) || a.Revision == 0 || a.Epoch != s.Epoch || in.Epoch != s.Epoch || in.Stopping || in.Tick < s.LastTick || in.Tick-s.LastTick > p.MaxSampleGap || in.Now.Before(s.LastNow) {
		return false
	}
	record, present := s.Pauses[a.Job]
	if !present || record.Revision != a.Revision || record.Job != a.Job || record.Reason != a.Reason || record.Incident != a.Incident || a.PolicyDigest == "" || a.PolicyDigest != record.PolicyDigest || a.PolicyDigest != policyDigest(p) || a.Key != action(a.Kind, a.Reason, a.Incident, 0, a.Job, a.AgentID, a.Epoch, a.Revision).Key {
		return false
	}
	var job Job
	found := false
	for _, candidate := range in.Jobs {
		if id(candidate) == a.Job {
			job, found = candidate, true
			break
		}
	}
	if !found || !eligible(job, in.Now) || job.AgentID != a.AgentID {
		return false
	}
	if a.Kind == RequestPause {
		threshold, enabled := p.Resources[a.Reason]
		sample := in.Samples[a.Reason]
		return enabled && threshold.AllowPause && record.Phase == PausePending && record.PauseKey == a.Key && !job.Paused && s.Resources[a.Reason].Active && s.Resources[a.Reason].Pressuring && s.Resources[a.Reason].Warned && freshSample(sample, in, p.MaxAge) && sample.Available <= threshold.EnterBelow
	}
	if record.Phase != ResumePending || record.ResumeKey != a.Key || !job.Paused {
		return false
	}
	if _, enabled := p.Resources[record.Reason]; !enabled {
		return false
	}
	for r, threshold := range p.Resources {
		sample := in.Samples[r]
		if !freshSample(sample, in, p.MaxAge) || sample.Available < threshold.ExitAbove || s.Resources[r].Active {
			return false
		}
	}
	for r, episode := range s.Resources {
		if _, enabled := p.Resources[r]; !enabled && episode.Active {
			return false
		}
	}
	return true
}

func validate(in Input, p Policy) error {
	if in.Epoch == "" || !boundedUTF8(in.Epoch, maxEpochBytes) || in.Now.IsZero() || in.Tick < 0 {
		return errors.New("pressure: epoch, wall time, and nonnegative tick required")
	}
	if p.MaxAge <= 0 || p.MaxSampleGap <= 0 || p.SustainFor <= 0 || p.Grace <= 0 || p.WarningEvery <= 0 || p.WarningEvery >= p.Grace || p.RecoverFor <= 0 || p.MinPause < 0 || p.MaxPause <= 0 || p.MaxPauseJobs < 0 || p.MaxJobs <= 0 || p.MaxJobs > 256 || p.MaxPauseJobs > p.MaxJobs || p.PendingTimeout <= 0 {
		return errors.New("pressure: invalid timing or pause policy")
	}
	if len(in.Jobs) > p.MaxJobs {
		return errors.New("pressure: job roster exceeds policy bound")
	}
	if len(in.Results) > 2*p.MaxJobs || len(in.Samples) > 3 {
		return errors.New("pressure: input exceeds policy bound")
	}
	for r, sample := range in.Samples {
		if !validResource(r) || !boundedUTF8(sample.Epoch, maxEpochBytes) {
			return errors.New("pressure: invalid sample identity")
		}
	}
	if len(p.Resources) == 0 {
		return errors.New("pressure: no resources configured")
	}
	for r, t := range p.Resources {
		if r != CPU && r != Memory && r != Disk {
			return fmt.Errorf("pressure: unsupported resource %q", r)
		}
		if t.ExitAbove <= t.EnterBelow {
			return fmt.Errorf("pressure: invalid %s hysteresis", r)
		}
	}
	seen := make(map[JobID]bool, len(in.Jobs))
	for _, j := range in.Jobs {
		jid := id(j)
		if !validJobID(jid) || !boundedUTF8(j.AgentID, maxAgentBytes) {
			return errors.New("pressure: invalid or oversized job identity")
		}
		if seen[jid] {
			return fmt.Errorf("pressure: duplicate job identity %+v", jid)
		}
		seen[jid] = true
	}
	results := make(map[string]bool, len(in.Results))
	for _, result := range in.Results {
		if result.Key == "" || !boundedUTF8(result.Key, maxKeyBytes) || results[result.Key] {
			return errors.New("pressure: empty or duplicate control result key")
		}
		results[result.Key] = true
	}
	seenReconciled := make(map[string]bool, len(in.Reconciled))
	if len(in.Reconciled) > p.MaxJobs {
		return errors.New("pressure: reconciliation list exceeds policy bound")
	}
	for _, key := range in.Reconciled {
		if key == "" || !boundedUTF8(key, maxKeyBytes) || seenReconciled[key] {
			return errors.New("pressure: invalid reconciliation key")
		}
		seenReconciled[key] = true
	}
	return nil
}

func clone(old State) State {
	s := old
	s.Resources = make(map[Resource]Episode, len(old.Resources))
	for k, v := range old.Resources {
		s.Resources[k] = v
	}
	s.Pauses = make(map[JobID]PauseRecord, len(old.Pauses))
	for k, v := range old.Pauses {
		s.Pauses[k] = v
	}
	s.JobWarnings = make(map[Resource]map[JobID]JobWarning, len(old.JobWarnings))
	for r, warned := range old.JobWarnings {
		s.JobWarnings[r] = make(map[JobID]JobWarning, len(warned))
		for jid, w := range warned {
			s.JobWarnings[r][jid] = w
		}
	}
	s.Unresolved = make(map[string]UnresolvedControl, len(old.Unresolved))
	for key, obligation := range old.Unresolved {
		s.Unresolved[key] = obligation
	}
	return s
}

func id(j Job) JobID { return JobID{j.LeaseID, j.ExecutionID, j.Generation, j.OwnershipID} }

func compareJob(a, b JobID) int {
	aa := [...]string{a.LeaseID, a.ExecutionID, a.Generation, a.OwnershipID}
	bb := [...]string{b.LeaseID, b.ExecutionID, b.Generation, b.OwnershipID}
	for i, av := range aa {
		bv := bb[i]
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

func eligible(j Job, now time.Time) bool {
	return j.LeaseID != "" && j.ExecutionID != "" && j.Generation != "" && j.OwnershipID != "" && j.Supervised && j.StillOwned && j.PauseCapability && j.PauseAllowed && !j.Revoked && !j.Stopping && !j.ExpiresAt.IsZero() && now.Before(j.ExpiresAt)
}

func warnings(r Resource, e Episode, jobs []Job, epoch string, now time.Time) []Action {
	a := []Action{action(WarnOperator, r, e.Incident, e.WarningCount, JobID{}, "", epoch, 0)}
	for _, j := range jobs {
		if j.Revoked || j.Stopping || !j.StillOwned || !j.ExpiresAt.After(now) || j.AgentID == "" {
			continue
		}
		jid := id(j)
		a = append(a, action(WarnAgent, r, e.Incident, e.WarningCount, jid, j.AgentID, epoch, 0))
		if !j.Supervised || !j.PauseCapability || !j.PauseAllowed {
			a = append(a, action(RequestCooperation, r, e.Incident, e.WarningCount, jid, j.AgentID, epoch, 0))
		}
	}
	return a
}

func action(kind ActionKind, r Resource, incident, warning uint64, jid JobID, agent, epoch string, revision uint64) Action {
	return Action{Key: fmt.Sprintf("%q/%q/%q/%d/%d/%d/%q/%q/%q/%q", epoch, kind, r, incident, warning, revision, jid.LeaseID, jid.ExecutionID, jid.Generation, jid.OwnershipID), Kind: kind, Reason: r, Incident: incident, Warning: warning, Job: jid, AgentID: agent, Epoch: epoch, Revision: revision}
}
