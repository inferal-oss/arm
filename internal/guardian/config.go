package guardian

import (
	"fmt"
	"slices"
	"time"

	"getarm.org/internal/model"
)

// Config is the resolved guardian policy. The zero value is disabled.
type Config struct {
	Mode           string
	Role           string
	Interval       time.Duration
	Ladder         []model.GuardianStepRule
	Recover        time.Duration // pressure at or below elevated this long closes an incident
	Cooldown       time.Duration // minimum spacing between applied steps of one incident
	FreezeMax      time.Duration // frozen groups resume after this long
	TerminateGrace time.Duration // forced termination follows graceful termination
	Budget         int           // applied process actions per hour
	Desktop        bool          // desktop notifications
	Protect        []string
}

var steps = []string{model.GuardianNotify, model.GuardianDeprioritize, model.GuardianAsk, model.GuardianShed, model.GuardianFreeze, model.GuardianTerminate}

func rule(step, level string, after time.Duration) model.GuardianStepRule {
	return model.GuardianStepRule{Step: step, Level: level, After: after}
}

// DefaultLadder prefers notification and reversible steps on a manned host.
// An unmanned host freezes and terminates, because nobody will intervene.
func DefaultLadder(role string) []model.GuardianStepRule {
	if role == model.GuardianUnmanned {
		return []model.GuardianStepRule{
			rule(model.GuardianNotify, model.PressureHigh, 15*time.Second),
			rule(model.GuardianDeprioritize, model.PressureHigh, 30*time.Second),
			rule(model.GuardianAsk, model.PressureHigh, 45*time.Second),
			rule(model.GuardianShed, model.PressureHigh, 90*time.Second),
			rule(model.GuardianFreeze, model.PressureCritical, 120*time.Second),
			rule(model.GuardianTerminate, model.PressureCritical, 240*time.Second),
		}
	}
	return []model.GuardianStepRule{
		rule(model.GuardianNotify, model.PressureHigh, 15*time.Second),
		rule(model.GuardianDeprioritize, model.PressureHigh, 30*time.Second),
		rule(model.GuardianAsk, model.PressureHigh, 45*time.Second),
		rule(model.GuardianShed, model.PressureCritical, 60*time.Second),
	}
}

// Defaults fills unset fields for the role.
func (c Config) Defaults() Config {
	if c.Mode == "" {
		c.Mode = model.GuardianOff
	}
	if c.Role == "" {
		c.Role = model.GuardianManned
	}
	if c.Interval == 0 {
		c.Interval = 4 * time.Second
	}
	if c.Ladder == nil {
		c.Ladder = DefaultLadder(c.Role)
	}
	if c.Recover == 0 {
		c.Recover = 60 * time.Second
	}
	if c.Cooldown == 0 {
		c.Cooldown = 15 * time.Second
	}
	if c.FreezeMax == 0 {
		c.FreezeMax = 5 * time.Minute
	}
	if c.TerminateGrace == 0 {
		c.TerminateGrace = 10 * time.Second
	}
	if c.Budget == 0 {
		c.Budget = 12
		if c.Role == model.GuardianUnmanned {
			c.Budget = 24
		}
	}
	return c
}

func (c Config) Validate() error {
	if !slices.Contains([]string{model.GuardianOff, model.GuardianObserve, model.GuardianAdvise, model.GuardianAct}, c.Mode) {
		return fmt.Errorf("guardian mode must be off, observe, advise or act")
	}
	if c.Role != model.GuardianManned && c.Role != model.GuardianUnmanned {
		return fmt.Errorf("guardian role must be manned or unmanned")
	}
	if c.Interval < time.Second || c.Interval > time.Minute {
		return fmt.Errorf("guardian interval must be between 1s and 1m")
	}
	for _, d := range []time.Duration{c.Recover, c.Cooldown, c.FreezeMax, c.TerminateGrace} {
		if d <= 0 || d > 24*time.Hour {
			return fmt.Errorf("guardian durations must be positive and at most 24h")
		}
	}
	if c.Budget < 1 || c.Budget > 1000 {
		return fmt.Errorf("guardian budget must be between 1 and 1000 actions per hour")
	}
	last := -1
	for _, r := range c.Ladder {
		i := slices.Index(steps, r.Step)
		if i < 0 {
			return fmt.Errorf("unknown guardian step %q", r.Step)
		}
		if i <= last {
			return fmt.Errorf("guardian steps must follow the order %v without repeats", steps)
		}
		last = i
		if rank, ok := model.PressureRank(r.Level); !ok || rank < 1 {
			return fmt.Errorf("guardian step %s needs level elevated, high or critical", r.Step)
		}
		if r.After < 0 {
			return fmt.Errorf("guardian step %s has a negative delay", r.Step)
		}
	}
	return nil
}

// executes reports whether mode permits a step to take effect; otherwise it
// is recorded as planned.
func (c Config) executes(step string) bool {
	switch c.Mode {
	case model.GuardianAct:
		return true
	case model.GuardianAdvise:
		return step == model.GuardianNotify || step == model.GuardianAsk
	}
	return false
}
