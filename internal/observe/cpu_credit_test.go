package observe

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/usage"
)

func TestManagedCPUNormalizationIsConservativePerOwner(t *testing.T) {
	ticks, cores := conservativeCPUOwnerRatio(1250, 10, 4, 100, 100)
	if ticks != 100 || cores >= 1 || cores <= .999999999 {
		t.Fatal("busy bound/downward conversion lost", ticks, cores)
	}
	if ticks, cores := conservativeCPUOwnerRatio(450, 10, 4, 100, 100); ticks != 41 || cores >= .41 || cores <= .409999999 {
		t.Fatal(ticks, cores)
	}
	for _, args := range [][5]uint64{{40, 10, 4, 100, 100}, {1, 0, 0, 1, 1}, {100, 1, 0, 0, 100}, {100, 1, 0, 1 << 54, 100}, {100, 1, 0, 100, 1 << 54}} {
		if ticks, cores := conservativeCPUOwnerRatio(args[0], args[1], args[2], args[3], args[4]); ticks != 0 || cores != 0 {
			t.Fatal("invalid interval created credit", args, ticks, cores)
		}
	}
	// Every job pays the entire host quantization margin, even if two jobs
	// would have produced a larger pooled candidate.
	_, a := conservativeCPUOwnerRatio(55, 10, 4, 100, 100)
	_, b := conservativeCPUOwnerRatio(55, 10, 4, 100, 100)
	_, pooled := conservativeCPUOwnerRatio(110, 10, 4, 100, 100)
	if a+b >= pooled {
		t.Fatal("per-job margin pooled unexpectedly", a, b, pooled)
	}
}
func TestManagedCPUWindowContainment(t *testing.T) {
	at := time.Now()
	old := cpuAvailabilityRaw{started: at, finished: at.Add(time.Millisecond)}
	now := cpuAvailabilityRaw{started: at.Add(time.Second), finished: at.Add(time.Second + time.Millisecond)}
	post := usage.CPUCut{Started: at.Add(2 * time.Millisecond), Finished: at.Add(3 * time.Millisecond)}
	pre := usage.CPUCut{Started: at.Add(990 * time.Millisecond), Finished: at.Add(991 * time.Millisecond)}
	if !containedCPUWindows(old, now, post, pre) {
		t.Fatal("contained inner work interval rejected")
	}
	for _, name := range []string{"outside-before", "outside-after", "overlap", "too-long", "missing", "stale"} {
		t.Run(name, func(t *testing.T) {
			a, b, p, q := old, now, post, pre
			switch name {
			case "outside-before":
				p.Started = at.Add(-time.Millisecond)
			case "outside-after":
				q.Finished = b.finished.Add(time.Millisecond)
			case "overlap":
				q.Started = p.Started
			case "too-long":
				p.Finished = p.Started.Add(usage.CPUCutBudget + time.Nanosecond)
			case "missing":
				p.Started = time.Time{}
			case "stale":
				b.finished = at.Add(time.Hour)
			}
			if containedCPUWindows(a, b, p, q) {
				t.Fatal("bad alignment accepted", name)
			}
		})
	}
}
func TestManagedCPUSamplerOrdersActualCallbacksAndCloses(t *testing.T) {
	order := []string{}
	at := time.Now()
	step := 0
	s := &CPUSampler{collect: func(context.Context) (cpuAvailabilityRaw, error) {
		order = append(order, "host")
		step++
		return cpuFixture(at.Add(time.Duration(step)*time.Second), uint64(step), uint64(step*10)), nil
	}}
	callback := func(context.Context) usage.CPUCut {
		order = append(order, "managed")
		return usage.CPUCut{Started: time.Now(), Finished: time.Now()}
	}
	s.SampleManaged(t.Context(), callback)
	if len(order) != 3 || order[0] != "managed" || order[1] != "host" || order[2] != "managed" {
		t.Fatal("native collection bracket was inverted", order)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before := len(order)
	r, f := s.SampleManaged(t.Context(), callback)
	if r.Known || len(f.Credits) != 0 || len(order) != before {
		t.Fatal("closed sampler ran native work or revived credit", r, f, order)
	}
}
func TestManagedCPUUnknownRestrictedOrFiniteQuotaZero(t *testing.T) {
	at := time.Now()
	before := cpuFixture(at, 1, 9)
	after := cpuFixture(at.Add(time.Second), 2, 18)
	for _, name := range []string{"unknown", "restricted", "finite-quota"} {
		t.Run(name, func(t *testing.T) {
			a, b := before, after
			a.fullHost, b.fullHost = true, true
			r := cpuAvailabilityBetween(a, b)
			switch name {
			case "unknown":
				r.Known = false
			case "restricted":
				b.fullHost = false
			case "finite-quota":
				b.quotas = []cpuQuotaCounter{{id: "parent"}}
			}
			f := cpuCreditFrame(a, b, usage.CPUCut{}, usage.CPUCut{}, r)
			if f.Eligible || len(f.Credits) != 0 {
				t.Fatal("unsupported scope generated managed credit", name, f)
			}
		})
	}
	s := &CPUSampler{collect: func(context.Context) (cpuAvailabilityRaw, error) {
		return cpuAvailabilityRaw{}, errors.New("native read failed")
	}}
	r, f := s.SampleManaged(t.Context(), nil)
	if r.Known || len(f.Credits) != 0 {
		t.Fatal("failed native input manufactured credit", r, f)
	}
}

func TestManagedCPUAccountingContradictionZerosWholeFrame(t *testing.T) {
	deltas := []usage.CPUOwnerDelta{{Owner: usage.CPUOwner{Token: "first"}, User: 500, MatchedProcesses: 1}, {Owner: usage.CPUOwner{Token: "second"}, User: 900, MatchedProcesses: 1}}
	credits, terms, ok := normalizeCPUOwners(deltas, 10, 4, 100, 100)
	if ok || len(credits) != 0 || len(terms) != 2 {
		t.Fatal("inconsistent raw accounting was silently capped into credit", credits, terms, ok)
	}
	// Valid distinct owners each pay the complete host margin.
	deltas[1].User = 500
	credits, terms, ok = normalizeCPUOwners(deltas, 10, 4, 100, 100)
	if !ok || len(credits) != 2 || terms[0].Ticks != 46 || terms[1].Ticks != 46 {
		t.Fatal("consistent per-owner normalization lost", credits, terms, ok)
	}
}
