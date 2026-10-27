package manager

import (
	"context"
	"math"
	"sort"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/observe"
	"github.com/inferal-oss/arm/internal/usage"
)

type cpuManagedObserver interface {
	SampleManaged(context.Context, func(context.Context) usage.CPUCut) (model.CPUAvailability, observe.CPUCreditFrame)
}

// This binding exists only in the original Manager process. A stored PID or
// wire observation cannot recreate the job pointer or its native launch anchor.
type cpuCreditSample struct {
	id       string
	job      *job
	identity model.ProcessIdentity
	cookie   string
	cores    float64
}

func (m *Manager) cpuOwners() ([]usage.CPUOwner, map[string]*job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owners, jobs := []usage.CPUOwner{}, map[string]*job{}
	if m.closing {
		return owners, jobs
	}
	ids := make([]string, 0, len(m.jobs))
	for id := range m.jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		j := m.jobs[id]
		if j == nil || j.identity == nil || j.cpuAnchor == "" || j.cpuExited.Load() {
			continue
		}
		select {
		case <-j.done:
			continue
		default:
		}
		owners = append(owners, usage.CPUOwner{Token: id, Root: *j.identity, Cookie: j.cpuAnchor})
		jobs[id] = j
		// Native collection has an independent global process/time bound.
		// Unobserved jobs keep their full promises and earn no guessed credit.
		if len(owners) == 32 {
			break
		}
	}
	return owners, jobs
}

func bindCPUCredits(frame observe.CPUCreditFrame, jobs map[string]*job) []cpuCreditSample {
	if !frame.Eligible {
		return nil
	}
	counts := map[string]int{}
	for _, c := range frame.Credits {
		counts[c.Token]++
	}
	var result []cpuCreditSample
	for _, c := range frame.Credits {
		j := jobs[c.Token]
		if counts[c.Token] != 1 || j == nil || j.identity == nil || *j.identity != c.Root || j.cpuAnchor == "" || j.cpuAnchor != c.Cookie || !finiteCPU(c.Cores) || c.Cores <= 0 || c.MatchedProcesses <= 0 {
			continue
		}
		result = append(result, cpuCreditSample{id: c.Token, job: j, identity: c.Root, cookie: c.Cookie, cores: c.Cores})
	}
	return result
}

// Caller holds Manager.mu. Revalidate current ownership and the actual durable
// consuming claim at every decision, rather than trusting a sampled promise.
// No process/native work runs here. Each job is capped separately so an
// over-budget job cannot borrow credit from another job's idle promise.
func (m *Manager) currentCPUCredit(samples []cpuCreditSample, reserved, busy float64) float64 {
	if reserved <= 0 || busy <= 0 {
		return 0
	}
	seen := map[model.ProcessIdentity]bool{}
	credit := 0.0
	for _, sample := range samples {
		j := m.jobs[sample.id]
		if j == nil || j != sample.job || j.identity == nil || *j.identity != sample.identity || j.cpuAnchor == "" || j.cpuAnchor != sample.cookie || j.cpuExited.Load() || seen[sample.identity] || !finiteCPU(sample.cores) || sample.cores <= 0 {
			continue
		}
		select {
		case <-j.done:
			continue
		default:
		}
		l, err := m.get(sample.id)
		if err != nil || l.ID != sample.id || l.Host != m.host || l.Owner != m.owner || !consuming(l.State) || l.Process == nil || *l.Process != sample.identity || !finiteCPU(l.Request.Resources.CPU) || l.Request.Resources.CPU <= 0 {
			continue
		}
		seen[sample.identity] = true
		amount := min(sample.cores, l.Request.Resources.CPU)
		credit = math.Nextafter(credit+amount, 0)
	}
	return min(credit, reserved, busy)
}
