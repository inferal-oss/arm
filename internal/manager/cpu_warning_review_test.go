package manager

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func TestCPUWarningReviewRetiredSampleOwnerIsUncertain(t *testing.T) {
	for _, change := range []string{"terminal", "expired", "absent"} {
		t.Run(change, func(t *testing.T) {
			m, l, _ := cpuCreditFixture(t)
			now, tick := time.Now().UTC(), 100*time.Second
			setHostPressureCPU(m, now, tick, 0)
			changed := l
			if change == "terminal" {
				changed.State = "terminal"
			}
			if change == "expired" {
				expired := now.Add(-time.Second)
				changed.Expires = &expired
			}
			rows := []model.Lease{changed}
			if change == "absent" {
				rows = nil
			}
			m.mu.Lock()
			sample, e := m.cpuWarningSample(now, tick, rows)
			m.mu.Unlock()
			if sample.Known || e.State != "uncertain" {
				t.Errorf("retired sampled owner manufactured shortfall: %+v %+v", sample, e)
			}
		})
	}
}

func TestCPUWarningReviewFractionalRecoveryThresholdRoundsUp(t *testing.T) {
	m := newManager(t)
	now, tick := time.Now().UTC(), 100*time.Second
	setHostPressureCPU(m, now, tick, .002)
	m.observationMu.Lock()
	capacity, busy := .0026, .0006
	m.cpuAvailability.Capacity = &capacity
	m.cpuAvailability.Busy = &busy
	m.observationMu.Unlock()
	sample, e := readCPUWarning(t, m, now, tick)
	if e.State == "recovery" {
		t.Errorf("77%% measured coverage crossed 90%% recovery threshold: %+v %+v", sample, e)
	}
}

func TestCPUWarningReviewPossibleSumRoundsOutwardPerOwner(t *testing.T) {
	m := newManager(t)
	memoryReading(m, 1024, 1024, 0)
	for index := range 17 {
		joined := make(chan struct{})
		h, err := m.AcquireRelayTransit(t.Context(), model.Request{Key: fmt.Sprintf("warning-rounded-owner-%d", index), Reason: "controlled outward-bound owner", TTL: time.Minute, Resources: model.Resources{CPU: .75 / 17, Memory: 1}}, joined)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			close(joined)
			if _, err := h.FinishAfterJoin(); err != nil {
				t.Error(err)
			}
		})
	}
	now, tick := time.Now().UTC(), 100*time.Second
	setHostPressureCPU(m, now, tick, 0)
	sample, e := readCPUWarning(t, m, now, tick)
	// The exact sum of these 17 representable promises is slightly greater
	// than .75. Repeated nearest additions lose several ulps before the final
	// outward step; that must not manufacture the <=750m warning crossing.
	if sample.Known || e.State != "uncertain" || e.PossibleCoverage <= .75 {
		t.Errorf("possible-use sum rounded into shortfall: %+v %+v", sample, e)
	}
}

// Check against exact rational values, independently of the float residual
// implementation, including thresholds, lost small terms and large promises.
func TestCPUWarningReviewOutwardArithmeticExactBounds(t *testing.T) {
	values := []float64{0, math.SmallestNonzeroFloat64, math.Nextafter(.001, 0), .0026, .75 / 17, .75, math.Nextafter(.75, math.Inf(1)), .9, 1, 65536}
	for _, a := range values {
		for _, b := range values {
			exact := new(big.Rat).Add(new(big.Rat).SetFloat64(a), new(big.Rat).SetFloat64(b))
			got := new(big.Rat).SetFloat64(cpuWarningSumUp(a, b))
			if got.Cmp(exact) < 0 {
				t.Fatalf("possible sum rounded down: %.17g + %.17g", a, b)
			}
		}
		for _, scale := range []float64{750, 900, 1000} {
			exact := new(big.Rat).Mul(new(big.Rat).SetFloat64(a), new(big.Rat).SetFloat64(scale))
			down := new(big.Rat).SetFloat64(cpuWarningProduct(a, scale, false))
			up := new(big.Rat).SetFloat64(cpuWarningProduct(a, scale, true))
			if down.Cmp(exact) > 0 || up.Cmp(exact) < 0 {
				t.Fatalf("threshold product rounded inward: %.17g * %.17g", a, scale)
			}
		}
	}
}

func TestCPUWarningReviewMissingNativeOwnerUsesOnlyPhysicalIdle(t *testing.T) {
	m, l, j := cpuCreditFixture(t)
	m.mu.Lock()
	delete(m.jobs, l.ID)
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.jobs[l.ID] = j; m.mu.Unlock() }()
	for _, idle := range []float64{0, 1} {
		now, tick := time.Now().UTC(), 100*time.Second
		setHostPressureCPU(m, now, tick, idle)
		sample, e := readCPUWarning(t, m, now, tick)
		if e.VerifiedManaged != 0 || e.PossibleCompliant != 0 {
			t.Error("lost owner lent managed coverage", e)
		}
		if idle == 0 && (sample.Known || e.State != "uncertain") {
			t.Error("lost native owner created shortfall", sample, e)
		}
		if idle == 1 && (!sample.Known || e.State != "recovery" || e.MinimumCoverage != 1) {
			t.Error("lost owner suppressed independent physical recovery", sample, e)
		}
	}
}

func TestCPUWarningReviewMissingInlineOwnerUsesOnlyPhysicalIdle(t *testing.T) {
	m, h := reviewSyncHold(t)
	m.mu.Lock()
	delete(m.syncWorkers, h.lease)
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.syncWorkers[h.lease] = h; m.mu.Unlock() }()
	for _, idle := range []float64{0, 1} {
		now, tick := time.Now().UTC(), 100*time.Second
		setHostPressureCPU(m, now, tick, idle)
		sample, e := readCPUWarning(t, m, now, tick)
		if e.VerifiedManaged != 0 || e.PossibleCompliant != 0 {
			t.Error("lost inline owner lent managed coverage", e)
		}
		if idle == 0 && (sample.Known || e.State != "uncertain") {
			t.Error("lost inline owner created shortfall", sample, e)
		}
		if idle == 1 && (!sample.Known || e.State != "recovery" || e.MinimumCoverage != 1) {
			t.Error("lost inline owner suppressed physical recovery", sample, e)
		}
	}
}

func TestCPUWarningReviewRestartedExpiredInlineObligationIsUncertain(t *testing.T) {
	m, h := reviewSyncHold(t)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(m.home, m.capacity)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.mu.Lock()
	l, err := reopened.get(h.lease)
	if err == nil {
		expired := time.Now().Add(-time.Minute)
		l.Expires = &expired
		err = reopened.put(l, "review.expired-retained-owner")
	}
	reopened.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if l.State != "needs_attention" || len(reopened.syncWorkers) != 0 {
		t.Fatal("restart reconstructed worker ownership", l)
	}
	for _, idle := range []float64{0, 1} {
		now, tick := time.Now().UTC(), 100*time.Second
		setHostPressureCPU(reopened, now, tick, idle)
		sample, e := readCPUWarning(t, reopened, now, tick)
		if e.PossibleCompliant != 0 || e.VerifiedManaged != 0 {
			t.Error("restart reconstructed use", e)
		}
		if idle == 0 && (sample.Known || e.State != "uncertain") {
			t.Error("expired retained owner manufactured shortfall", sample, e)
		}
		if idle == 1 && (!sample.Known || e.State != "recovery") {
			t.Error("expired retained owner blocked physical recovery", sample, e)
		}
	}
}

func TestCPUWarningReviewTransitRequiresOriginalContextAndJoin(t *testing.T) {
	for _, change := range []string{"context", "join"} {
		t.Run(change, func(t *testing.T) {
			m := newManager(t)
			memoryReading(m, 1024, 1024, 0)
			joined := make(chan struct{})
			h, err := m.AcquireRelayTransit(t.Context(), model.Request{Key: "warning-original-transit-" + change, Reason: "controlled original transit", TTL: time.Minute, Resources: model.Resources{CPU: .5, Memory: 1}}, joined)
			if err != nil {
				t.Fatal(err)
			}
			originalContext, originalJoined := h.ctx, h.joined
			t.Cleanup(func() {
				m.mu.Lock()
				h.ctx, h.joined = originalContext, originalJoined
				m.mu.Unlock()
				close(joined)
				if _, err := h.FinishAfterJoin(); err != nil {
					t.Error(err)
				}
			})
			now, tick := time.Now().UTC(), 100*time.Second
			setHostPressureCPU(m, now, tick, .5)
			replacement, cancel := context.WithCancel(context.Background())
			defer cancel()
			m.mu.Lock()
			if change == "context" {
				h.ctx = replacement
			} else {
				h.joined = make(chan struct{})
			}
			m.mu.Unlock()
			sample, e := readCPUWarning(t, m, now, tick)
			if sample.Known || e.State != "uncertain" || e.PossibleCompliant != 0 || e.VerifiedManaged != 0 {
				t.Error("transit adopted replacement context/join", sample, e)
			}
		})
	}
}

func TestCPUWarningReviewTransitRejectsSameRevisionRowReplacement(t *testing.T) {
	m := newManager(t)
	memoryReading(m, 1024, 1024, 0)
	joined := make(chan struct{})
	h, err := m.AcquireRelayTransit(t.Context(), model.Request{Key: "warning-original-transit-row", Reason: "controlled original transit row", TTL: time.Minute, Resources: model.Resources{CPU: .5, Memory: 1}}, joined)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(joined)
		if _, err := h.FinishAfterJoin(); err != nil {
			t.Error(err)
		}
	})
	owners := m.captureCPUWarningOwners()
	m.mu.Lock()
	defer m.mu.Unlock()
	l, err := m.get(h.id)
	if err != nil {
		t.Fatal(err)
	}
	if !m.currentCPUWarningOwner(l, owners[h.id]) {
		t.Fatal("original transit owner unavailable")
	}
	l.Request.Resources.CPU = .25
	if m.currentCPUWarningOwner(l, owners[h.id]) {
		t.Error("transit accepted different promise at same revision")
	}
}
