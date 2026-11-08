package guardian

import "getarm.org/internal/model"

// FromSettings resolves configuration. Unconfigured or disabled settings
// resolve to mode off; enabling without a mode selects act.
func FromSettings(s *model.GuardianSettings) Config {
	if s == nil || s.Enabled == nil || !*s.Enabled {
		return Config{Mode: model.GuardianOff}.Defaults()
	}
	c := Config{Mode: s.Mode, Role: s.Role, Interval: s.Interval, Recover: s.Recover, Cooldown: s.Cooldown, FreezeMax: s.FreezeMax, TerminateGrace: s.TerminateGrace, Budget: s.MaxActionsPerHour, Protect: s.Protect, Ladder: s.Ladder, Desktop: true}
	if c.Mode == "" {
		c.Mode = model.GuardianAct
	}
	if s.DesktopNotifications != nil {
		c.Desktop = *s.DesktopNotifications
	}
	return c.Defaults()
}

// ValidateSettings checks a configured table, even when disabled, so a
// later enable cannot reveal a broken policy.
func ValidateSettings(s *model.GuardianSettings) error {
	if s == nil {
		return nil
	}
	on := true
	copy := *s
	copy.Enabled = &on
	if copy.Mode == model.GuardianOff {
		copy.Mode = ""
	}
	return FromSettings(&copy).Validate()
}
