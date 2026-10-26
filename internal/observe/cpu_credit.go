package observe

import (
	"math"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/usage"
)

// These are private backend evidence, never a wire authority or process-control
// capability. Manager must revalidate the exact original job and current lease,
// then cap EACH owner by its own current promise before adding candidates.
type CPUOwnerCredit struct {
	Token            string
	Root             model.ProcessIdentity
	Cookie           string
	Cores            float64
	MatchedProcesses int
}
type CPUOwnerCreditTerm struct {
	Owner            usage.CPUOwner
	RawDelta, Ticks  uint64
	MatchedProcesses int
	Members          []usage.CPUProcessDelta
}
type CPUCreditEvidence struct {
	PreviousHostStarted, PreviousHostFinished, HostStarted, HostFinished             time.Time
	PreviousPostStarted, PreviousPostFinished, CurrentPreStarted, CurrentPreFinished time.Time
	MaxTotalDelta, BusyTicks, Quantum, CorrectionTicks                               uint64
	Terms                                                                            []CPUOwnerCreditTerm
}
type CPUCreditFrame struct {
	Credits  []CPUOwnerCredit
	Eligible bool
	Reason   string
	Evidence CPUCreditEvidence
}

func cpuCreditFrame(before, after cpuAvailabilityRaw, post, pre usage.CPUCut, reading model.CPUAvailability) CPUCreditFrame {
	f := CPUCreditFrame{Evidence: CPUCreditEvidence{PreviousHostStarted: before.started, PreviousHostFinished: before.finished, HostStarted: after.started, HostFinished: after.finished, PreviousPostStarted: post.Started, PreviousPostFinished: post.Finished, CurrentPreStarted: pre.Started, CurrentPreFinished: pre.Finished}}
	if !reading.Known {
		f.Reason = "native CPU interval is unavailable"
		return f
	}
	if !before.fullHost || !after.fullHost {
		f.Reason = "managed CPU credit requires complete host CPU scope"
		return f
	}
	if len(before.quotas) != 0 || len(after.quotas) != 0 {
		f.Reason = "managed CPU credit for finite cgroup quotas is not implemented"
		return f
	}
	if !containedCPUWindows(before, after, post, pre) {
		f.Reason = "managed CPU cuts are missing, stale or not contained in the host interval"
		return f
	}
	q, _, err := usage.CPUClockQuantum()
	if err != nil {
		f.Reason = err.Error()
		return f
	}
	maxTotal, busy := uint64(0), uint64(0)
	for i, current := range after.rows {
		old := before.rows[i]
		used, idle := current.busy-old.busy, current.idle-old.idle
		total, e := checkedAdd(used, idle)
		if e != nil {
			f.Reason = "CPU host delta overflow"
			return f
		}
		busy, e = checkedAdd(busy, used)
		if e != nil {
			f.Reason = "CPU busy sum overflow"
			return f
		}
		maxTotal = max(maxTotal, total)
	}
	if maxTotal == 0 || maxTotal > 1<<53 || busy > 1<<53 || q == 0 {
		f.Reason = "CPU normalization range is unsupported"
		return f
	}
	f.Eligible = true
	f.Evidence.MaxTotalDelta, f.Evidence.BusyTicks, f.Evidence.Quantum, f.Evidence.CorrectionTicks = maxTotal, busy, q, uint64(2*len(after.rows))
	credits, terms, consistent := normalizeCPUOwners(usage.ContainedCPUDeltas(post, pre), q, f.Evidence.CorrectionTicks, maxTotal, busy)
	f.Evidence.Terms = terms
	if !consistent {
		f.Eligible = false
		f.Reason = "managed CPU increments contradict host busy accounting"
		return f
	}
	f.Credits = credits
	if len(f.Credits) == 0 {
		f.Reason = "no verified managed CPU increment exceeded the conservative accounting margin"
	} else {
		f.Reason = "contained full-host Darwin recount increments; each owner still requires current-job and promise validation"
	}
	return f
}

func containedCPUWindows(before, after cpuAvailabilityRaw, post, pre usage.CPUCut) bool {
	points := []time.Time{before.started, before.finished, post.Started, post.Finished, pre.Started, pre.Finished, after.started, after.finished}
	for i, p := range points {
		if p.IsZero() {
			return false
		}
		if i > 0 && (p.Before(points[i-1]) || p.UTC().Before(points[i-1].UTC())) {
			return false
		}
	}
	if post.Finished.Sub(post.Started) > usage.CPUCutBudget || pre.Finished.Sub(pre.Started) > usage.CPUCutBudget {
		return false
	}
	return cpuIntervalReliable(after.finished.Sub(before.started), after.finished.UTC().Sub(before.started.UTC()), cpuObservationMinInterval)
}

func conservativeCPUOwnerRatio(raw, q, correction, denominator, remaining uint64) (uint64, float64) {
	if q == 0 || denominator == 0 || denominator > 1<<53 || remaining > 1<<53 {
		return 0, 0
	}
	ticks := raw / q
	if ticks <= correction {
		return 0, 0
	}
	ticks = min(ticks-correction, remaining)
	if ticks == 0 {
		return 0, 0
	}
	// Both integer operands are exactly representable. Step below the rounded
	// quotient so the floating candidate remains a strict lower bound.
	return ticks, math.Nextafter(float64(ticks)/float64(denominator), 0)
}

func normalizeCPUOwners(deltas []usage.CPUOwnerDelta, q, correction, maxTotal, busy uint64) ([]CPUOwnerCredit, []CPUOwnerCreditTerm, bool) {
	credits := []CPUOwnerCredit{}
	terms := []CPUOwnerCreditTerm{}
	if q == 0 || maxTotal == 0 || maxTotal > 1<<53 || busy > 1<<53 {
		return nil, nil, false
	}
	remaining := busy
	for _, d := range deltas {
		raw, err := checkedAdd(d.User, d.System)
		if err != nil {
			continue
		}
		uncapped := raw / q
		if uncapped > correction {
			uncapped -= correction
		} else {
			uncapped = 0
		}
		if uncapped > remaining {
			terms = append(terms, CPUOwnerCreditTerm{Owner: d.Owner, RawDelta: raw, Ticks: uncapped, MatchedProcesses: d.MatchedProcesses, Members: append([]usage.CPUProcessDelta(nil), d.Members...)})
			return nil, terms, false
		}
		ticks, cores := conservativeCPUOwnerRatio(raw, q, correction, maxTotal, remaining)
		term := CPUOwnerCreditTerm{Owner: d.Owner, RawDelta: raw, Ticks: ticks, MatchedProcesses: d.MatchedProcesses, Members: append([]usage.CPUProcessDelta(nil), d.Members...)}
		terms = append(terms, term)
		if ticks == 0 {
			continue
		}
		remaining -= ticks
		credits = append(credits, CPUOwnerCredit{Token: d.Owner.Token, Root: d.Owner.Root, Cookie: d.Owner.Cookie, Cores: cores, MatchedProcesses: d.MatchedProcesses})
	}
	return credits, terms, true
}
