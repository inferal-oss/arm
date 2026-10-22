// Package cargoprofile prepares a closed cooperative Cargo workflow. It does
// not run tools, grant resources or confer cleanup ownership. Callers must do
// preflight inside the admitted owned workload scope before compilation.
package cargoprofile

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Command preserves caller input separately from a normalized builtin command.
// Executable remains a selector until an admitted helper verifies real tools.
type Command struct {
	Original   []string `json:"original"`
	Executable string   `json:"executable_selector"`
	Subcommand string   `json:"subcommand"`
	Jobs       int      `json:"jobs"`
	Arguments  []string `json:"arguments"`
	Manifest   string   `json:"manifest_path,omitempty"`
}

// Parse accepts only literal build/check/test --no-run. Aliases, custom commands, test execution,
// caller config files and unmodeled output controls remain explicitly refused.
func Parse(argv []string, cpu float64) (Command, error) {
	var c Command
	if math.IsNaN(cpu) || math.IsInf(cpu, 0) || cpu < 1 || cpu > 65536 {
		return c, fmt.Errorf("managed Cargo requires a finite CPU claim between 1 and 65536")
	}
	if len(argv) < 2 || len(argv) > 256 || filepath.Base(argv[0]) != "cargo" || strings.ContainsRune(argv[0], 0) {
		return c, fmt.Errorf("use an installed cargo executable with literal build/check/test --no-run; other programs need generic supervision")
	}
	totalBytes := 0
	for _, arg := range argv {
		totalBytes += len(arg)
		if len(arg) > 8192 || !validText(arg) || totalBytes > 65536 {
			return Command{}, fmt.Errorf("Cargo arguments exceed bounds or contain control characters")
		}
	}
	c = Command{Original: append([]string{}, argv...), Executable: argv[0], Subcommand: argv[1], Jobs: int(math.Floor(cpu)), Arguments: []string{argv[1]}}
	if c.Subcommand != "build" && c.Subcommand != "check" && c.Subcommand != "test" {
		return Command{}, fmt.Errorf("managed Cargo supports literal build/check/test --no-run; aliases, toolchain prefixes, custom commands and test execution are not yet regulated")
	}
	jobsSeen, noRun := false, false
	for i := 2; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--no-run" {
			if c.Subcommand != "test" || noRun {
				return Command{}, fmt.Errorf("--no-run is accepted exactly once for test")
			}
			noRun = true
			c.Arguments = append(c.Arguments, arg)
			continue
		}
		if !validText(arg) || len(arg) > 8192 {
			return Command{}, fmt.Errorf("Cargo argument exceeds bounds or contains control characters")
		}
		flag, value, inline := strings.Cut(arg, "=")
		if strings.HasPrefix(arg, "-j") && arg != "-j" {
			flag = "-j"
			value = arg[2:]
			inline = true
		}
		if flag == "--jobs" || flag == "-j" {
			if jobsSeen {
				return Command{}, fmt.Errorf("specify jobs once; ARM appends the authoritative positive jobs value")
			}
			jobsSeen = true
			if !inline {
				i++
				if i >= len(argv) {
					return Command{}, fmt.Errorf("jobs needs a positive integer")
				}
				value = argv[i]
			}
			n, e := strconv.Atoi(value)
			if e != nil || n < 1 || n > int(math.Floor(cpu)) || strconv.Itoa(n) != value {
				return Command{}, fmt.Errorf("jobs must be a positive integer no greater than the whole-core CPU claim")
			}
			c.Jobs = n
			continue
		}
		if booleanFlags[flag] && !inline {
			c.Arguments = append(c.Arguments, flag)
			continue
		}
		if flag == "-vv" || flag == "-vvv" {
			if inline {
				return Command{}, fmt.Errorf("invalid verbosity value")
			}
			for range flag[1:] {
				c.Arguments = append(c.Arguments, "-v")
			}
			continue
		}
		if canonical, ok := valueFlags[flag]; ok {
			if !inline {
				i++
				if i >= len(argv) {
					return Command{}, fmt.Errorf("%s requires one value", flag)
				}
				value = argv[i]
			}
			if !validText(value) || len(value) > 8192 || strings.HasPrefix(value, "-") {
				return Command{}, fmt.Errorf("%s requires a bounded non-option value", flag)
			}
			if canonical == "--target" && !targetTriple(value) {
				return Command{}, fmt.Errorf("use a target-like selector; custom JSON/paths are refused and built-in membership requires admitted tool verification")
			}
			if canonical == "--manifest-path" {
				if c.Manifest != "" {
					return Command{}, fmt.Errorf("specify manifest-path once")
				}
				c.Manifest = value
			}
			c.Arguments = append(c.Arguments, canonical, value)
			continue
		}
		return Command{}, fmt.Errorf("Cargo option/operand is not regulated; use literal build/check/test --no-run options, omit aliases/config/output overrides, or use generic supervision")
	}
	if c.Subcommand == "test" && !noRun {
		return Command{}, fmt.Errorf("test requires --no-run; execution/test threads need a separately regulated workflow")
	}
	return c, nil
}

var booleanFlags = map[string]bool{"--release": true, "-r": true, "--workspace": true, "--lib": true, "--bins": true, "--examples": true, "--tests": true, "--benches": true, "--all-targets": true, "--all-features": true, "--no-default-features": true, "--offline": true, "--frozen": true, "--locked": true, "--keep-going": true, "--quiet": true, "-q": true, "--verbose": true, "-v": true, "--future-incompat-report": true}
var valueFlags = map[string]string{"--package": "--package", "-p": "--package", "--exclude": "--exclude", "--bin": "--bin", "--example": "--example", "--test": "--test", "--bench": "--bench", "--features": "--features", "-F": "--features", "--profile": "--profile", "--target": "--target", "--manifest-path": "--manifest-path"}

func validText(s string) bool {
	if s == "" || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// targetTriple checks only conservative selector syntax. It is not proof of
// a built-in target; an admitted resolver must bind rustc --print target-list.
func targetTriple(v string) bool {
	if len(v) > 256 || !strings.Contains(v, "-") || strings.HasSuffix(v, ".json") {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// MetadataArguments preserves the discovery cwd; manifest-path does not change
// config discovery. Package/target selection is compilation-specific; metadata
// reports the entire workspace with matching manifest/features and preserves the
// caller's offline/frozen controls; admission must precede any resolution work.
func (c Command) MetadataArguments() []string {
	out := []string{"metadata", "--format-version", "1", "--no-deps"}
	for i := 1; i < len(c.Arguments); i++ {
		a := c.Arguments[i]
		if a == "--manifest-path" || a == "--features" {
			out = append(out, a, c.Arguments[i+1])
			i++
		} else if a == "--all-features" || a == "--no-default-features" || a == "--locked" || a == "--frozen" || a == "--offline" {
			out = append(out, a)
		} else if _, ok := valueFlags[a]; ok {
			i++
		}
	}
	return out
}
