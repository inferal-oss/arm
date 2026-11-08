package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/guardian"
	"getarm.org/internal/model"
)

// guardianLadderReport is the saved ladder that the next backend start uses.
type guardianLadderReport struct {
	Path    string                   `json:"path"`
	Enabled *bool                    `json:"enabled"`
	Mode    string                   `json:"mode"`
	Role    string                   `json:"role"`
	Source  string                   `json:"source"` // config, or role-default
	Ladder  []model.GuardianStepRule `json:"ladder"`
	Unused  []string                 `json:"unused"`
	Edit    *daemonconfig.EditResult `json:"edit,omitempty"`
	digest  string                   // config.toml SHA256 of the bytes read
}

var ladderEffects = map[string]string{
	model.GuardianNotify:       "Inbox message and desktop notification",
	model.GuardianDeprioritize: "Lower CPU and I/O priority (not used for memory)",
	model.GuardianAsk:          "Ask the responsible registered agent to back off",
	model.GuardianShed:         "Close up to three of the busiest browser tabs",
	model.GuardianFreeze:       "Suspend the program (SIGSTOP); resumes after freeze_max",
	model.GuardianTerminate:    "SIGTERM, then SIGKILL after terminate_grace",
}

var ladderLevels = []string{model.PressureElevated, model.PressureHigh, model.PressureCritical}

const ladderMaxDelay = 24 * time.Hour

// readGuardianLadder also records the digest of the file it read, so a
// later save refuses a config.toml that changed in between.
func (a *app) readGuardianLadder() (guardianLadderReport, error) {
	r, err := daemonconfig.Resolve(a.home, daemonDefaults(), daemonconfig.Overrides{})
	if err != nil {
		return guardianLadderReport{}, fmt.Errorf("configuration refused; nothing applied: %w", err)
	}
	before, err := configDigest(r.Path)
	if err != nil {
		return guardianLadderReport{}, err
	}
	if again, err := daemonconfig.Resolve(a.home, daemonDefaults(), daemonconfig.Overrides{}); err != nil || !reflect.DeepEqual(again, r) {
		return guardianLadderReport{}, fmt.Errorf("configuration changed while it was read; retry")
	}
	g := r.Settings.Guardian
	rep := guardianLadderReport{digest: before, Path: r.Path, Role: model.GuardianManned, Source: "role-default", Mode: guardian.FromSettings(g).Mode}
	if g != nil {
		rep.Enabled = g.Enabled
		if g.Role != "" {
			rep.Role = g.Role
		}
	}
	rep.Ladder = guardian.DefaultLadder(rep.Role)
	if g != nil && g.Ladder != nil {
		rep.Ladder, rep.Source = slices.Clone(g.Ladder), "config"
	}
	return rep.withUnused(), nil
}

func (r guardianLadderReport) withUnused() guardianLadderReport {
	r.Unused = []string{}
	for _, s := range guardian.Steps() {
		if ladderIndex(r.Ladder, s) < 0 {
			r.Unused = append(r.Unused, s)
		}
	}
	return r
}

func ladderIndex(ladder []model.GuardianStepRule, step string) int {
	return slices.IndexFunc(ladder, func(r model.GuardianStepRule) bool { return r.Step == step })
}

// ladderDefault is a step's role default, or the unmanned default for a step
// the role does not use.
func ladderDefault(role, step string) model.GuardianStepRule {
	for _, l := range [][]model.GuardianStepRule{guardian.DefaultLadder(role), guardian.DefaultLadder(model.GuardianUnmanned)} {
		if i := ladderIndex(l, step); i >= 0 {
			return l[i]
		}
	}
	return model.GuardianStepRule{Step: step, Level: model.PressureCritical}
}

// withStep inserts or replaces a step at its required position.
func withStep(ladder []model.GuardianStepRule, rule model.GuardianStepRule) []model.GuardianStepRule {
	out := slices.DeleteFunc(slices.Clone(ladder), func(r model.GuardianStepRule) bool { return r.Step == rule.Step })
	order := guardian.Steps()
	at := slices.IndexFunc(out, func(r model.GuardianStepRule) bool {
		return slices.Index(order, r.Step) > slices.Index(order, rule.Step)
	})
	if at < 0 {
		at = len(out)
	}
	return slices.Insert(out, at, rule)
}

func parseLadderStep(step string) error {
	if !slices.Contains(guardian.Steps(), step) {
		return fmt.Errorf("unknown step %q; use one of %s", step, strings.Join(guardian.Steps(), ", "))
	}
	return nil
}
func parseLadderLevel(level string) error {
	if !slices.Contains(ladderLevels, level) {
		return fmt.Errorf("level must be elevated, high or critical")
	}
	return nil
}
func parseLadderDelay(raw string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || d < 0 || d > ladderMaxDelay || d%time.Second != 0 {
		return 0, fmt.Errorf("delay must be whole seconds from 0s to 24h, such as 45s or 2m")
	}
	return d, nil
}

func (a *app) guardianLadder(args []string) (int, error) {
	action := "show"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	} else if len(args) == 1 && a.group("guardian ladder", args) {
		return 0, nil
	}
	if !slices.Contains([]string{"show", "set", "remove", "reset", "preset", "edit"}, action) {
		return 2, fmt.Errorf("unknown guardian ladder action %q; use show, set, remove, reset, preset or edit", action)
	}
	f := a.flags("guardian ladder " + action)
	f.Bool("json", false, "JSON ladder report")
	var level, after, expect string
	var dryRun bool
	if action == "set" {
		f.StringVar(&level, "level", "", "minimum pressure level: elevated, high or critical")
		f.StringVar(&after, "after", "", "sustained pressure before the step, such as 45s")
	}
	if action != "show" {
		f.BoolVar(&dryRun, "dry-run", false, "show the validated proposed TOML without writing")
		f.StringVar(&expect, "expect-file", "", "expected existing config.toml SHA256 (optional concurrency guard)")
	}
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	rep, err := a.readGuardianLadder()
	if err != nil {
		return 2, err
	}
	if action == "show" {
		return 0, a.print(rep)
	}
	if action == "edit" {
		return a.guardianLadderEdit(rep, dryRun, expect)
	}
	ladder, remove := rep.Ladder, false
	switch action {
	case "set":
		step := f.Arg(0)
		if err := parseLadderStep(step); err != nil {
			return 2, err
		}
		rule := ladderDefault(rep.Role, step)
		if i := ladderIndex(ladder, step); i >= 0 {
			rule = ladder[i]
		}
		if level != "" {
			if err := parseLadderLevel(level); err != nil {
				return 2, err
			}
			rule.Level = level
		}
		if after != "" {
			if rule.After, err = parseLadderDelay(after); err != nil {
				return 2, err
			}
		}
		ladder = withStep(ladder, rule)
	case "remove":
		step := f.Arg(0)
		if err := parseLadderStep(step); err != nil {
			return 2, err
		}
		if ladderIndex(ladder, step) < 0 {
			return 2, fmt.Errorf("step %s is not in the ladder", step)
		}
		ladder = slices.DeleteFunc(slices.Clone(ladder), func(r model.GuardianStepRule) bool { return r.Step == step })
		if len(ladder) == 0 {
			return 2, fmt.Errorf("the ladder needs at least one step; set guardian.mode observe to record without acting")
		}
	case "preset":
		role := f.Arg(0)
		if role != model.GuardianManned && role != model.GuardianUnmanned {
			return 2, fmt.Errorf("preset must be manned or unmanned")
		}
		ladder = guardian.DefaultLadder(role)
	case "reset":
		remove = true
	}
	return a.saveGuardianLadder(rep, ladder, remove, dryRun, expect)
}

// saveGuardianLadder writes a custom ladder, or removes it so the role
// default applies, and reports the proposed result.
func (a *app) saveGuardianLadder(rep guardianLadderReport, ladder []model.GuardianStepRule, remove, dryRun bool, expect string) (int, error) {
	if expect == "" {
		expect = rep.digest
	}
	if remove && rep.Source != "config" {
		// Nothing to remove; a missing file stays missing.
		if expect != rep.digest {
			return 2, fmt.Errorf("configuration changed; inspect config show before retrying")
		}
		rep.Edit = &daemonconfig.EditResult{Path: rep.Path, BeforeSHA256: rep.digest, AfterSHA256: rep.digest, Scope: "Saved configuration for next ordinary daemon startup; running daemon unchanged"}
		return 0, a.print(rep.withUnused())
	}
	edit := daemonconfig.Edit{Key: "guardian.ladder", Ladder: ladder, Remove: remove, DryRun: dryRun, ExpectFile: expect}
	result, err := daemonconfig.EditFile(a.home, daemonDefaults(), edit)
	if err != nil {
		return 2, err
	}
	rep.digest = result.AfterSHA256
	rep.Ladder, rep.Source = ladder, "config"
	if remove {
		rep.Ladder, rep.Source = guardian.DefaultLadder(rep.Role), "role-default"
	}
	rep.Edit = &result
	return 0, a.print(rep.withUnused())
}

func (a *app) guardianLadderHuman(r guardianLadderReport) error {
	var b bytes.Buffer
	fmt.Fprintln(&b, a.paint("1;36", "ARM · host guardian ladder"))
	source := "custom ladder in " + r.Path
	if r.Source != "config" {
		source = "default ladder for " + r.Role + " hosts"
	}
	fmt.Fprintf(&b, "Role %s · mode %s · %s\n\n", r.Role, r.Mode, source)
	fmt.Fprintf(&b, "  %-13s %-9s %6s  %s\n", "STEP", "LEVEL", "AFTER", "EFFECT")
	for _, s := range r.Ladder {
		fmt.Fprintf(&b, "  %-13s %-9s %6s  %s\n", s.Step, s.Level, daemonconfig.DurationText(s.After), ladderEffects[s.Step])
	}
	if len(r.Unused) > 0 {
		fmt.Fprintf(&b, "\nNot used: %s\n", strings.Join(r.Unused, ", "))
	}
	for _, w := range ladderWarnings(r.Mode, r.Role, r.Enabled, r.Ladder) {
		fmt.Fprintln(&b, a.paint("33", w))
	}
	if e := r.Edit; e != nil {
		switch {
		case !e.Written && e.BeforeSHA256 != e.AfterSHA256:
			fmt.Fprintln(&b, "\nDry run; nothing written. Proposed config.toml:")
			fmt.Fprint(&b, e.TOML)
		case !e.Written:
			fmt.Fprintln(&b, "\nUnchanged; config.toml already says this.")
		default:
			fmt.Fprintln(&b, a.paint("32", "\nSaved to "+e.Path+"."))
			fmt.Fprintln(&b, "Applies at the next backend start: arm stop, then arm start.")
		}
	}
	_, err := a.out.Write(b.Bytes())
	return err
}

func ladderWarnings(mode, role string, enabled *bool, ladder []model.GuardianStepRule) []string {
	var w []string
	if enabled == nil || !*enabled {
		w = append(w, "The guardian is off; enable it with: arm config set guardian.enabled true")
	}
	switch mode {
	case model.GuardianOff:
		if enabled != nil && *enabled {
			w = append(w, "Mode off: the guardian is enabled but does nothing; set guardian.mode act.")
		}
	case model.GuardianObserve:
		w = append(w, "Mode observe records every step as planned without acting.")
	case model.GuardianAdvise:
		w = append(w, "Mode advise runs only notify and ask; other steps are recorded as planned.")
	}
	if role == model.GuardianManned && (ladderIndex(ladder, model.GuardianFreeze) >= 0 || ladderIndex(ladder, model.GuardianTerminate) >= 0) {
		w = append(w, "This ladder can suspend or terminate programs on a host someone is using.")
	}
	return w
}

// configDigest is the SHA256 that EditFile compares against, empty file
// included, so an interactive session refuses concurrent edits.
func configDigest(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
