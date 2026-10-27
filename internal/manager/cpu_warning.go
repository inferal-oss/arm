package manager

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/observe"
	"getarm.org/internal/pressure"
)

// A possible-use bound needs the original owner observed before the native
// interval read and the same current owner at the warning decision. It earns
// no usage credit. Linux intentionally has no compatible CPU credit cookie.
type cpuWarningOwner struct {
	job           *job
	identity      model.ProcessIdentity
	cookie        string
	transit       *RelayTransit
	inline        *SyncWorkerHold
	runtime       *sshRuntime
	ctx           context.Context
	done          <-chan struct{}
	leaseDigest   [32]byte
	transitRecord string
}

func (m *Manager) captureCPUWarningOwners() map[string]cpuWarningOwner {
	m.mu.Lock()
	defer m.mu.Unlock()
	owners := map[string]cpuWarningOwner{}
	if m.closing {
		return owners
	}
	for id, j := range m.jobs {
		if len(owners) == 256 {
			break
		}
		if j == nil || j.identity == nil || j.cpuExited.Load() {
			continue
		}
		select {
		case <-j.done:
			continue
		default:
		}
		owners[id] = cpuWarningOwner{job: j, identity: *j.identity, cookie: j.cpuAnchor}
	}
	for id, h := range m.relayTransits {
		if len(owners) == 256 {
			break
		}
		if h == nil || h.settled || h.ctx.Err() != nil {
			continue
		}
		select {
		case <-h.joined:
			continue
		default:
		}
		record, err := json.Marshal(h.result)
		if err != nil {
			continue
		}
		owners[id] = cpuWarningOwner{transit: h, ctx: h.ctx, done: h.joined, transitRecord: string(record)}
	}
	for id, h := range m.syncWorkers {
		if len(owners) == 256 {
			break
		}
		if h == nil || h.manager != m || h.runtime == nil || h.runtime.ctx == nil || h.runtime.ctx.Err() != nil || h.done == nil || len(h.leaseRecord) == 0 {
			continue
		}
		// Same order as Start: Manager.mu, then hold.mu. Never wait for done.
		h.mu.Lock()
		started := h.started
		h.mu.Unlock()
		if !started {
			continue
		}
		select {
		case <-h.done:
			continue
		default:
		}
		owners[id] = cpuWarningOwner{inline: h, runtime: h.runtime, ctx: h.runtime.ctx, done: h.done, leaseDigest: sha256.Sum256(h.leaseRecord)}
	}
	return owners
}

// Manager.mu is held. A missing/replaced original owner makes this interval
// ambiguous; it supplies no managed coverage. Independent physical idle can
// still establish recovery in the enclosing classifier.
func (m *Manager) currentCPUWarningOwner(l model.Lease, saved cpuWarningOwner) bool {
	// A lease cannot simultaneously borrow different original owner kinds.
	kinds := 0
	if m.jobs[l.ID] != nil {
		kinds++
	}
	if m.relayTransits[l.ID] != nil {
		kinds++
	}
	if m.syncWorkers[l.ID] != nil {
		kinds++
	}
	if kinds != 1 {
		return false
	}
	if saved.job != nil {
		j := m.jobs[l.ID]
		if j != saved.job || j.identity == nil || l.Process == nil || *j.identity != saved.identity || *l.Process != saved.identity || j.cpuAnchor != saved.cookie || j.cpuExited.Load() {
			return false
		}
		select {
		case <-j.done:
			return false
		default:
			return true
		}
	}
	if saved.transit != nil {
		h := m.relayTransits[l.ID]
		if h != saved.transit || h.ctx != saved.ctx || h.joined != saved.done || h.settled || h.ctx.Err() != nil || h.result.ID != l.ID || h.result.Revision != l.Revision || h.key != l.Request.Key {
			return false
		}
		listed, listedErr := json.Marshal(l)
		current, currentErr := json.Marshal(h.result)
		if listedErr != nil || currentErr != nil || string(listed) != saved.transitRecord || string(current) != saved.transitRecord {
			return false
		}
		select {
		case <-h.joined:
			return false
		default:
			return true
		}
	}
	if saved.inline != nil {
		h := m.syncWorkers[l.ID]
		if h != saved.inline || h.manager != m || h.lease != l.ID || h.runtime != saved.runtime || h.runtime == nil || h.runtime.ctx != saved.ctx || h.runtime.ctx == nil || h.runtime.ctx.Err() != nil || h.done != saved.done || sha256.Sum256(h.leaseRecord) != saved.leaseDigest {
			return false
		}
		listed, err := json.Marshal(l)
		if err != nil || sha256.Sum256(listed) != saved.leaseDigest {
			return false
		}
		h.mu.Lock()
		started := h.started
		h.mu.Unlock()
		if !started {
			return false
		}
		select {
		case <-h.done:
			return false
		default:
		}
		// This checks the complete original row and volume binding using the
		// current original context; it lends only possible-compliant use.
		return h.checkBindingLocked(h.runtime.ctx) == nil
	}
	return false
}

// The collector already proves compatibility/contained windows. These checks
// bind the private frame to THIS cached observation, not a different cycle.
// Process inventory completeness and an upper usage bound are not inferred.
func cpuWarningFrameMatches(frame observe.CPUCreditFrame, cpu model.CPUAvailability) bool {
	if !frame.Eligible {
		return false
	}
	e := frame.Evidence
	points := []time.Time{e.PreviousHostStarted, e.PreviousHostFinished, e.PreviousPostStarted, e.PreviousPostFinished, e.CurrentPreStarted, e.CurrentPreFinished, e.HostStarted, e.HostFinished}
	for i, t := range points {
		if t.IsZero() || i > 0 && t.Before(points[i-1]) {
			return false
		}
	}
	// Compare monotonic interval bounds, not At.Add(-Interval): a small
	// permitted wall-clock adjustment must not misidentify the previous read.
	return !cpu.At.Before(e.HostStarted) && !cpu.At.After(e.HostFinished) &&
		cpu.Interval >= e.HostStarted.Sub(e.PreviousHostFinished) &&
		cpu.Interval <= e.HostFinished.Sub(e.PreviousHostStarted)
}

// The enclosing host-pressure policy and CPU sample must use one immutable
// observation, even if a native sampler publishes while SQL rows are read.
type cpuWarningSnapshot struct {
	cpu      model.CPUAvailability
	observed time.Time
	tick     time.Duration
	credits  []cpuCreditSample
	frame    observe.CPUCreditFrame
	owners   map[string]cpuWarningOwner
}

// Caller holds observationMu.
func (m *Manager) cpuWarningSnapshotLocked() cpuWarningSnapshot {
	return cpuWarningSnapshot{m.cpuAvailability, m.cpuObserved, m.cpuPressureTick, m.cpuCredits, m.cpuCreditFrame, m.cpuWarningOwners}
}

// Caller holds Manager.mu. The result is warning evidence, never admission or
// signal authority. "Possible" assumes compliance with promises and is NOT a
// physical usage upper bound. Missing/churning members can leave it uncertain.
func (m *Manager) cpuWarningSample(now time.Time, tick time.Duration, leases []model.Lease) (pressure.Sample, model.CPUWarningEvidence) {
	m.observationMu.RLock()
	snapshot := m.cpuWarningSnapshotLocked()
	m.observationMu.RUnlock()
	return m.cpuWarningSampleFrom(snapshot, now, tick, leases)
}

func (m *Manager) cpuWarningSampleFrom(snapshot cpuWarningSnapshot, now time.Time, tick time.Duration, leases []model.Lease) (pressure.Sample, model.CPUWarningEvidence) {
	elapsed := m.pressureElapsedWindows(leases)
	return m.cpuWarningSampleFromElapsed(snapshot, now, tick, leases, elapsed)
}

// Caller holds Manager.mu. The roster and CPU attribution use the same native
// entitlement sample for one host-pressure decision.
func (m *Manager) cpuWarningSampleFromElapsed(snapshot cpuWarningSnapshot, now time.Time, tick time.Duration, leases []model.Lease, elapsed pressureElapsedView) (pressure.Sample, model.CPUWarningEvidence) {
	traceCPUWarningEnter(m, snapshot, now, tick, leases, elapsed)
	cpu, observed, sampleTick := snapshot.cpu, snapshot.observed, snapshot.tick
	credits, frame, owners := snapshot.credits, snapshot.frame, snapshot.owners
	policy := hostPressurePolicy(cpu, model.MemoryAvailability{}, model.DiskAvailability{})
	evidence := model.CPUWarningEvidence{State: "unavailable", At: cpu.At, Interval: cpu.Interval, Detail: "Fresh native CPU headroom is unavailable"}
	sample := hostSample(cpu.At, sampleTick, tick, now, m.pressureEpoch, 0, false)
	if !elapsed.CPUAttributionKnown {
		evidence.Detail = "Elapsed lease provenance is unavailable; CPU warning attribution is unknown"
		return traceCPUWarningReturn(m, snapshot, now, tick, leases, elapsed, sample, evidence)
	}
	if !cpu.Known || cpu.Available == nil || cpu.Capacity == nil || cpu.Busy == nil || cpu.EligibleCPUs == nil || *cpu.EligibleCPUs <= 0 || !finiteCPU(*cpu.Available) || !finiteCPU(*cpu.Capacity) || !finiteCPU(*cpu.Busy) || *cpu.Capacity < 0.002 || *cpu.Capacity > 65536 || *cpu.Capacity > float64(*cpu.EligibleCPUs) || *cpu.Available < 0 || *cpu.Available > *cpu.Capacity || *cpu.Busy < 0 || *cpu.Busy > float64(*cpu.EligibleCPUs) || cpu.Interval < 100*time.Millisecond || cpu.Interval > 6*time.Second || observed.IsZero() || observed.After(now) || now.Sub(observed) > policy.MaxAge || cpu.At.IsZero() || cpu.At.After(now) || now.Sub(cpu.At) > policy.MaxAge || sampleTick < 0 || sampleTick > tick || tick-sampleTick > policy.MaxAge {
		return traceCPUWarningReturn(m, snapshot, now, tick, leases, elapsed, sample, evidence)
	}
	evidence.NativeKnown = true
	evidence.Idle = *cpu.Available
	reserved, possible := 0.0, 0.0
	active := map[string]bool{}
	ownerChanged := false
	for _, l := range leases {
		// Retained execution/transport obligations are not bare reservations.
		// A lost original owner cannot lend usage or a compliance bound, but
		// its absence also cannot identify unexplained busy time. This runs
		// before elapsed eligibility filtering because uncertain work stays charged.
		if consuming(l.State) && l.ID != "" && l.Request.Resources.CPU > 0 && m.jobs[l.ID] == nil && m.relayTransits[l.ID] == nil && m.syncWorkers[l.ID] == nil {
			missing, err := m.missingCPUWarningOwner(l)
			if err != nil {
				evidence.Detail = "CPU warning owner accounting is unavailable"
				return traceCPUWarningReturn(m, snapshot, now, tick, leases, elapsed, sample, evidence)
			}
			ownerChanged = ownerChanged || missing
		}
		if elapsed.Live[l.ID] <= 0 {
			continue
		}
		n := l.Request.Resources.CPU
		if !finiteCPU(n) || n < 0 || n > 65536 {
			evidence.Detail = "CPU promise accounting is invalid; no warning decision is established"
			return traceCPUWarningReturn(m, snapshot, now, tick, leases, elapsed, sample, evidence)
		}
		reserved += n
		if !finiteCPU(reserved) {
			return traceCPUWarningReturn(m, snapshot, now, tick, leases, elapsed, sample, evidence)
		}
		if n == 0 {
			continue
		}
		saved, wasOwned := owners[l.ID]
		currentOwned := m.jobs[l.ID] != nil || m.relayTransits[l.ID] != nil || m.syncWorkers[l.ID] != nil
		if !wasOwned && !currentOwned {
			continue
		} // Bare claims are not observed work.
		if l.Host != m.host || l.Owner != m.owner || !wasOwned || !m.currentCPUWarningOwner(l, saved) {
			ownerChanged = true
			continue
		}
		possible = cpuWarningSumUp(possible, n)
		active[l.ID] = true
	}
	// Owners present at either sampling or decision time must all be
	// accounted for. A retired, expired, omitted or newly joined member can
	// explain the sampled busy interval; losing it cannot create shortfall.
	for id := range owners {
		if !active[id] {
			ownerChanged = true
		}
	}
	for id := range m.jobs {
		if !active[id] {
			ownerChanged = true
		}
	}
	for id := range m.relayTransits {
		if !active[id] {
			ownerChanged = true
		}
	}
	for id := range m.syncWorkers {
		if !active[id] {
			ownerChanged = true
		}
	}
	if !finiteCPU(possible) {
		return traceCPUWarningReturn(m, snapshot, now, tick, leases, elapsed, sample, evidence)
	}
	credited := 0.0
	if cpuWarningFrameMatches(frame, cpu) {
		eligible := make([]cpuCreditSample, 0, len(credits))
		for _, c := range credits {
			if active[c.id] && owners[c.id].job != nil {
				eligible = append(eligible, c)
			}
		}
		credited = m.currentCPUCredit(eligible, reserved, *cpu.Busy)
	}
	evidence.VerifiedManaged = credited
	evidence.PossibleCompliant = possible
	minimum, possibleAmount := *cpu.Available+credited, max(possible, credited)
	if credited > 0 {
		minimum = math.Nextafter(minimum, 0)
	}
	maximum := cpuWarningSumUp(*cpu.Available, possibleAmount)
	evidence.MinimumCoverage = min(*cpu.Capacity, minimum)
	evidence.PossibleCoverage = min(*cpu.Capacity, maximum)
	evidence.State = "uncertain"
	evidence.Detail = "Some supervised CPU use may be unobserved; compliant-use bounds are not physical measurements or proof of an external culprit"

	threshold := policy.Resources[pressure.CPU]
	low, high := uint64(math.Floor(cpuWarningProduct(evidence.MinimumCoverage, 1000, false))), uint64(math.Ceil(cpuWarningProduct(evidence.PossibleCoverage, 1000, true)))
	switch {
	case low >= threshold.ExitAbove:
		sample.Available, sample.Known = low, true
		evidence.State = "recovery"
		evidence.Detail = "Fresh idle plus verified current managed use supports a recovery observation; incident recovery still requires hysteresis, and bare promises add no measured coverage"
	case ownerChanged:
		evidence.Detail = "Original CPU work ownership changed across this observation; warning confidence must be re-established"
	case high <= threshold.EnterBelow:
		sample.Available, sample.Known = high, true
		evidence.State = "shortfall"
		evidence.Detail = "Even possible compliant supervised use does not cover this busy interval; bare/untracked work may be legitimate, so this is a cooperative warning without blame"
	case low > threshold.EnterBelow && high < threshold.ExitAbove:
		sample.Available, sample.Known = low, true
		evidence.State = "neutral"
		evidence.Detail = "The complete compliant-use coverage range lies between pressure and recovery thresholds"
	}
	return traceCPUWarningReturn(m, snapshot, now, tick, leases, elapsed, sample, evidence)
}

// A persisted descriptor supplies only uncertainty, never executable ownership
// or credit. Ordinary bare claims have no such provider/operation evidence.
func (m *Manager) missingCPUWarningOwner(l model.Lease) (bool, error) {
	if l.Process != nil || l.PID > 0 || l.Request.Kind == relayTransitKind || l.Request.Kind == syncPreparationKind {
		return true, nil
	}
	if l.State != "needs_attention" && l.Request.OriginKey == "" {
		return false, nil
	}
	var hadRun, syncBound bool
	err := m.db.QueryRow(`SELECT run IS NOT NULL,EXISTS(SELECT 1 FROM sync_lease_bindings WHERE lease=?) FROM leases WHERE id=?`, l.ID, l.ID).Scan(&hadRun, &syncBound)
	return hadRun || syncBound, err
}

// TwoSum retains the rounding residual. Step upward only when the nearest
// sum rounded below the exact sum of the representable policy quantities.
// Applying this per owner prevents many lost ulps from shrinking the upper
// bound; it does not turn a promise into measured usage.
func cpuWarningSumUp(a, b float64) float64 {
	sum := a + b
	virtualB := sum - a
	residual := (a - (sum - virtualB)) + (b - virtualB)
	if residual > 0 {
		return math.Nextafter(sum, math.Inf(1))
	}
	return sum
}

// FMA exposes the product residual without a separately rounded multiply.
// Threshold and coverage conversion must round in their own safe direction.
func cpuWarningProduct(a, scale float64, up bool) float64 {
	product := a * scale
	residual := math.FMA(a, scale, -product)
	if up && residual > 0 {
		return math.Nextafter(product, math.Inf(1))
	}
	if !up && residual < 0 {
		return math.Nextafter(product, 0)
	}
	return product
}
