package daemonconfig

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"getarm.org/internal/model"
)

// guardianDocument is the optional [guardian] table. Enabled records the
// operator's explicit choice; absence means nobody has decided yet.
type guardianDocument struct {
	Enabled              *bool                  `toml:"enabled"`
	Mode                 *string                `toml:"mode"`
	Role                 *string                `toml:"role"`
	Interval             *string                `toml:"interval"`
	Recover              *string                `toml:"recover"`
	Cooldown             *string                `toml:"cooldown"`
	FreezeMax            *string                `toml:"freeze_max"`
	TerminateGrace       *string                `toml:"terminate_grace"`
	MaxActionsPerHour    *int                   `toml:"max_actions_per_hour"`
	DesktopNotifications *bool                  `toml:"desktop_notifications"`
	Protect              []string               `toml:"protect"`
	Ladder               []guardianStepDocument `toml:"ladder"`
}
type guardianStepDocument struct {
	Step  string `toml:"step"`
	Level string `toml:"level"`
	After string `toml:"after"`
}

var guardianKeys = []string{"enabled", "mode", "role", "interval", "recover", "cooldown", "freeze_max", "terminate_grace", "max_actions_per_hour", "desktop_notifications"}

func guardianLiteral(key, value string) (string, error) {
	switch key {
	case "enabled", "desktop_notifications":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return "", fmt.Errorf("guardian.%s requires true or false", key)
		}
		return strconv.FormatBool(b), nil
	case "max_actions_per_hour":
		n, err := strconv.Atoi(value)
		if err != nil {
			return "", fmt.Errorf("guardian.%s requires an integer", key)
		}
		return strconv.Itoa(n), nil
	}
	if !slices.Contains(guardianKeys, key) {
		return "", fmt.Errorf("supported guardian keys: %v", guardianKeys)
	}
	return quoted(value), nil
}

func guardianSettings(doc *guardianDocument) (*model.GuardianSettings, error) {
	if doc == nil {
		return nil, nil
	}
	g := &model.GuardianSettings{Enabled: doc.Enabled, Protect: doc.Protect, DesktopNotifications: doc.DesktopNotifications}
	if doc.Mode != nil {
		g.Mode = *doc.Mode
	}
	if doc.Role != nil {
		g.Role = *doc.Role
	}
	if doc.MaxActionsPerHour != nil {
		g.MaxActionsPerHour = *doc.MaxActionsPerHour
	}
	for _, d := range []struct {
		raw  *string
		into *time.Duration
		name string
	}{{doc.Interval, &g.Interval, "interval"}, {doc.Recover, &g.Recover, "recover"}, {doc.Cooldown, &g.Cooldown, "cooldown"}, {doc.FreezeMax, &g.FreezeMax, "freeze_max"}, {doc.TerminateGrace, &g.TerminateGrace, "terminate_grace"}} {
		if d.raw == nil {
			continue
		}
		v, err := time.ParseDuration(*d.raw)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("guardian.%s requires a positive duration such as 30s", d.name)
		}
		*d.into = v
	}
	if doc.Ladder != nil {
		g.Ladder = []model.GuardianStepRule{}
		for _, s := range doc.Ladder {
			after, err := time.ParseDuration(s.After)
			if s.After == "" {
				after, err = 0, nil
			}
			if err != nil {
				return nil, fmt.Errorf("guardian ladder step %s: invalid after duration", s.Step)
			}
			g.Ladder = append(g.Ladder, model.GuardianStepRule{Step: s.Step, Level: s.Level, After: after})
		}
	}
	return g, nil
}
