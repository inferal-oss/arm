package manager

import (
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yrashk/arm/internal/model"
)

// The initial adapter uses an installed Gradle executable, not a project
// wrapper or a shell command that could change the selected distribution/JVM.
// Validation does not execute Gradle or read/evaluate project build code.
func validateGradleExecutable(command []string) error {
	if len(command) == 0 || filepath.Base(command[0]) != "gradle" {
		return model.Err("profile_conflict", "Gradle profiles require an installed gradle executable; project wrappers and shell commands are unsupported")
	}
	return nil
}

func validateGradleService(command []string, cpu float64) error {
	if err := validateGradleExecutable(command); err != nil {
		return err
	}
	if len(command) != 1 {
		return model.Err("profile_conflict", "gradle-service supplies foreground daemon, JVM and home settings; pass only the gradle executable")
	}
	if math.IsNaN(cpu) || math.IsInf(cpu, 0) || cpu < 1 || cpu > math.MaxInt32 {
		return model.Err("profile_conflict", "gradle-service requires at least one CPU for its daemon/worker budget")
	}
	return nil
}

var gradleTask = regexp.MustCompile(`^:?[A-Za-z0-9_][A-Za-z0-9_:.-]*$`)

func validateGradleBuild(command []string, cpu float64) error {
	if err := validateGradleExecutable(command); err != nil {
		return err
	}
	if math.IsNaN(cpu) || math.IsInf(cpu, 0) || cpu <= 0 {
		return model.Err("profile_conflict", "gradle-build requires a positive CPU claim for its separate client process")
	}
	tasks := 0
	for _, arg := range command[1:] {
		switch arg {
		case "--offline", "--stacktrace", "--full-stacktrace", "--info", "--quiet", "--warn":
			continue
		}
		if !strings.HasPrefix(arg, "-") && gradleTask.MatchString(arg) {
			tasks++
			continue
		}
		return model.Err("profile_conflict", fmt.Sprintf("unsupported Gradle argument %q; the initial adapter accepts task names and offline/logging flags, and supplies daemon, JVM, worker and path settings", arg))
	}
	if tasks == 0 {
		return model.Err("profile_conflict", "gradle-build requires at least one task")
	}
	return nil
}
