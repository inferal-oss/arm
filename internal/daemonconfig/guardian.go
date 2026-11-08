package daemonconfig

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2/unstable"
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
	if key == "ladder" || strings.HasPrefix(key, "ladder.") {
		return "", fmt.Errorf("change the guardian ladder with arm guardian ladder")
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

// editLadder replaces every [[guardian.ladder]] table with edit.Ladder, placed
// at the end of the [guardian] table, or removes them all when edit.Remove so
// the role default applies again. Other settings, tables and comments are
// preserved, and the file's line ending is kept. Inline and dotted ladder
// forms are refused because they cannot be rewritten without restructuring
// the operator's file.
func editLadder(raw []byte, sections []configSection, edit Edit) ([]byte, error) {
	if !edit.Remove && len(edit.Ladder) == 0 {
		return nil, fmt.Errorf("a guardian ladder needs at least one step; set guardian.mode observe to record without acting")
	}
	for _, s := range sections {
		for _, r := range s.values {
			if s.name == "guardian" && r.key == "ladder" || s.name == "" && (r.key == "guardian" || strings.HasPrefix(r.key, "guardian.")) {
				return nil, fmt.Errorf("guardian settings use a dotted or inline form; edit the [guardian] table manually")
			}
		}
	}
	eol := "\n"
	if bytes.Contains(raw, []byte("\r\n")) {
		eol = "\r\n"
	}
	lineEnd := func(raw []byte, pos int) int {
		if end := bytes.IndexByte(raw[pos:], '\n'); end >= 0 {
			return pos + end + 1
		}
		return len(raw)
	}
	// Remove from the last table so earlier offsets stay valid. A ladder table
	// holds only scalar step, level and after values, so each removal covers
	// the header and value lines, plus one preceding blank line.
	for i := len(sections) - 1; i >= 0; i-- {
		s := sections[i]
		if s.name != "guardian.ladder" {
			continue
		}
		start, end := s.start, lineEnd(raw, s.start)
		for _, v := range s.values {
			end = max(end, lineEnd(raw, v.end))
		}
		for _, blank := range []string{"\r\n\r\n", "\n\n"} {
			if bytes.HasSuffix(raw[:start], []byte(blank)) {
				start -= len(blank) / 2
				break
			}
		}
		raw = append(append([]byte{}, raw[:start]...), raw[end:]...)
	}
	if edit.Remove {
		return raw, nil
	}
	var b strings.Builder
	for _, r := range edit.Ladder {
		b.WriteString(eol + "[[guardian.ladder]]" + eol + "step = " + quoted(r.Step) + eol + "level = " + quoted(r.Level) + eol + "after = " + quoted(DurationText(r.After)) + eol)
	}
	sections, err := sourceSections(raw)
	if err != nil {
		return nil, err
	}
	// Insert where the [guardian] table ends, before any blank or comment
	// lines that lead into the next table. Array values report no source
	// range, so the table end, not the last value, marks the position. Only
	// lines the parser reports as comments count: a line inside a multi-line
	// string can start with # too.
	comments := map[int]bool{}
	parser := unstable.Parser{KeepComments: true}
	parser.Reset(raw)
	for parser.NextExpression() {
		if n := parser.Expression(); n.Kind == unstable.Comment {
			comments[int(n.Raw.Offset)] = true
		}
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	at := len(raw)
	for _, s := range sections {
		if s.name != "guardian" {
			continue
		}
		at = s.end
		for floor := lineEnd(raw, s.start); at > floor; {
			prev := bytes.LastIndexByte(raw[:at-1], '\n') + 1
			line := bytes.TrimLeft(raw[prev:at], " \t")
			start := at - len(line)
			if len(bytes.TrimSpace(line)) > 0 && !comments[start] {
				break
			}
			at = prev
		}
		break
	}
	out := append([]byte{}, raw[:at]...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, eol...)
	}
	out = append(out, b.String()...)
	if at < len(raw) && raw[at] != '\n' && raw[at] != '\r' {
		out = append(out, eol...)
	}
	return append(out, raw[at:]...), nil
}

// DurationText writes a duration without zero trailing units: 1m, not 1m0s.
func DurationText(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
