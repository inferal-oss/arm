package sshservice

import (
	"context"
	"fmt"
	"os"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/usage"
	"golang.org/x/sys/unix"
)

// RecoverArtifacts is an explicit exact-evidence cleanup, never startup work.
// A recovered PID is only a liveness veto, never authority to signal/adopt it.
// No socket runtime ownership is inferred from its persisted pathname/inode.
func RecoverArtifacts(ctx context.Context, home string, b Binding, intent SourceIntent, status model.SSHKeyStatus) error {
	if e := ValidateKeyStatus(intent, status); e != nil {
		return e
	}
	if status.State == "sealed" && status.Fence != nil {
		if e := MatchSourceSeal(intent, *status.Fence); e != nil {
			return e
		}
	} else if status.Execution != nil {
		if e := MatchTerminalReceipt(intent, *status.Execution); e != nil {
			return e
		}
	} else {
		return fmt.Errorf("unknown/absent status cannot authorize artifact cleanup")
	}
	return withStateMutation(ctx, home, b, true, func(s *State, _ *os.File) error {
		found := false
		for _, r := range s.Intents {
			if exactIntent(r, intent) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("exact retained pending intention required")
		}
		current, e := usage.Capture(os.Getpid())
		if e != nil || current.Namespace != intent.Worker.Namespace {
			return fmt.Errorf("original worker namespace unavailable; preserve artifacts for manual review")
		}
		// ESRCH is the only accepted failure; inaccessible/reused/live workers veto.
		if e = unix.Kill(intent.Worker.PID, 0); e != unix.ESRCH {
			return fmt.Errorf("original worker PID still exists or cannot be proved absent; no cleanup")
		}
		if e = s.guard(); e != nil {
			return e
		}
		return cleanupArtifactDirectory(ctx, intent, nil)
	})
}
