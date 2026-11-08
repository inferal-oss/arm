package guardian

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"

	"getarm.org/internal/model"
	"getarm.org/internal/usage"
)

var (
	errIdentity = errors.New("process identity changed or exited")
	// errNiceKept reports a restore that succeeded except for a nice value
	// an unprivileged owner cannot lower again.
	errNiceKept = errors.New("nice value kept without privilege")
)

// revalidate confirms that the PID still names the observed process. A
// stored identity is never trusted without this check.
func revalidate(id model.ProcessIdentity) error {
	current, err := usage.Capture(id.PID)
	if err != nil || current != id {
		return errIdentity
	}
	return nil
}

// sameProcess is the looser check for undoing the guardian's own actions:
// a process that changed its group or session is still the same process,
// and resuming or restoring its priority is safe.
func sameProcess(id model.ProcessIdentity) error {
	current, err := usage.Capture(id.PID)
	if err != nil || current.Start != id.Start || current.Namespace != id.Namespace {
		return errIdentity
	}
	return nil
}

// Thaw resumes the exact process, matched by PID, start time and namespace.
func Thaw(id model.ProcessIdentity) error {
	if err := sameProcess(id); err != nil {
		return err
	}
	return unix.Kill(id.PID, syscall.SIGCONT)
}

// Priority records what deprioritizing changed, so restore can undo only
// that. Nice values can be raised back only with privilege.
type Priority struct {
	Background bool `json:"background,omitempty"` // Darwin PRIO_DARWIN_BG applied
	Nice       *int `json:"nice,omitempty"`       // previous nice, when changed
	IOPrio     *int `json:"ioprio,omitempty"`     // previous Linux I/O priority, when changed
}

// Signal delivers sig to the exact identity.
func Signal(id model.ProcessIdentity, sig syscall.Signal) error { return signal(id, sig) }

var errUnsupported = errors.New("unsupported on this platform")

func splitLines(b []byte) []string {
	var out []string
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				out = append(out, string(b[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, string(b[start:]))
	}
	return out
}
