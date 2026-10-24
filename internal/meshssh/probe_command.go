package meshssh

import (
	"fmt"
	"regexp"
	"strings"
)

// ProbeInvocation is the existing fixed read-only native probe, in both the
// OpenSSH remote argument representation and its actual native argv. These are
// detached values; callers cannot change another invocation or the fixed script.
// It grants no permission to execute a received shell command.
type ProbeInvocation struct {
	Remote []string
	Argv   []string
	Input  string
}

// FixedReadOnlyProbe builds only the existing, unmodified read-only probe. The
// developer SSH fixture uses the exact selected image/home command as an
// allowlist, then executes Argv rather than evaluating any received command.
func FixedReadOnlyProbe(binary, home string) (ProbeInvocation, error) {
	if home != "" && !ValidStateHome(home) {
		return ProbeInvocation{}, fmt.Errorf("canonical bounded remote state home required")
	}
	if !regexp.MustCompile(`^(arm|/[A-Za-z0-9_./-]+)$`).MatchString(binary) {
		return ProbeInvocation{}, fmt.Errorf("safe direct destination/installed binary required")
	}
	binary, home = strings.Clone(binary), strings.Clone(home)
	return ProbeInvocation{
		Remote: []string{"/bin/sh", "-c", quote(probeScript), "arm-probe", quote(binary), quote(home)},
		Argv:   []string{"/bin/sh", "-c", probeScript, "arm-probe", binary, home},
		Input:  "ARM_READONLY_PROBE\n",
	}, nil
}
