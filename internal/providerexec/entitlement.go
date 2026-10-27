package providerexec

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

// HeldBinding identifies the original ordinary admissions. It is not a wire
// claim or proof of current eligibility. A trusted checker must compare every
// bound value against its current owner records and authoritative native clock.
type HeldBinding struct {
	Authority, Owner, Worker, Temp, Boot string
	WorkerRevision, TempRevision         int
	TempPath                             string
	TempIdentity                         model.FileIdentity
	CPU                                  float64
	Memory, Disk                         int64
}

// EntitlementCheck can only withhold eligibility. It neither creates durable
// start/dispatch markers nor certifies process closure, cleanup or settlement.
// The trusted implementation must obey ctx and must not infer elapsed lifetime
// from wall time. It runs outside Owner and Handle bookkeeping locks.
type EntitlementCheck func(ctx context.Context, original HeldBinding) (time.Duration, error)

type Entitlement struct {
	lifetime context.Context
	boot     string
	current  EntitlementCheck
}

// EntitlementFromTrusted validates/copies ALREADY TRUSTED coordinator inputs.
// It cannot authenticate them. Never construct it from provider/protocol claims.
// The context belongs to the original retained worker/temp ownership scope.
func EntitlementFromTrusted(lifetime context.Context, boot string, current EntitlementCheck) (Entitlement, error) {
	if lifetime == nil || boot == "" || len(boot) > 256 || current == nil {
		return Entitlement{}, fmt.Errorf("trusted original lifetime, native boot and current-pair checker required")
	}
	if err := lifetime.Err(); err != nil {
		return Entitlement{}, err
	}
	return Entitlement{lifetime: lifetime, boot: strings.Clone(boot), current: current}, nil
}

func (h Held) Binding() HeldBinding {
	return HeldBinding{Authority: h.authority, Owner: h.owner, Worker: h.worker, Temp: h.temp, Boot: h.entitlement.boot, WorkerRevision: h.workerRevision, TempRevision: h.tempRevision, TempPath: h.path, TempIdentity: h.identity, CPU: h.cpu, Memory: h.memory, Disk: h.disk}
}

func (h Held) remaining(ctx context.Context) (time.Duration, error) {
	if ctx == nil || h.entitlement.lifetime == nil || h.entitlement.current == nil || h.entitlement.boot == "" {
		return 0, fmt.Errorf("current trusted entitlement unavailable")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := h.entitlement.lifetime.Err(); err != nil {
		return 0, err
	}
	checkContext, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(h.entitlement.lifetime, cancel)
	defer func() { stop(); cancel() }()
	remaining, err := h.entitlement.current(checkContext, h.Binding())
	if err != nil {
		return 0, err
	}
	// Cancellation during a read cannot be converted back into eligibility.
	if err = ctx.Err(); err != nil {
		return 0, err
	}
	if err = h.entitlement.lifetime.Err(); err != nil {
		return 0, err
	}
	if remaining <= 0 {
		return 0, fmt.Errorf("original elapsed entitlement exhausted")
	}
	return remaining, nil
}

// currentRemaining binds an individual call to the original native duty as well
// as the retained admissions. Cancellation of any of them withholds eligibility.
func (h *Handle) currentRemaining(ctx context.Context) (time.Duration, error) {
	if ctx == nil || h.context == nil {
		return 0, fmt.Errorf("original native lifetime unavailable")
	}
	if err := h.context.Err(); err != nil {
		return 0, err
	}
	current, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(h.context, cancel)
	defer func() { stop(); cancel() }()
	remaining, err := h.prepared.held.remaining(current)
	if err != nil {
		return 0, err
	}
	if err = h.context.Err(); err != nil {
		return 0, err
	}
	return remaining, nil
}
