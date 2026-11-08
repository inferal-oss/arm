package guardian

import (
	"syscall"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

const (
	prioDarwinProcess = 4
	prioDarwinBG      = 0x1000
	lowNiceValue      = 0 // Darwin deprioritization does not change nice
)

// lower applies Darwin background policy: low CPU priority, efficiency
// cores where available and throttled storage I/O. The owner can clear it.
func lower(id model.ProcessIdentity) (Priority, error) {
	if err := revalidate(id); err != nil {
		return Priority{}, err
	}
	if err := unix.Setpriority(prioDarwinProcess, id.PID, prioDarwinBG); err != nil {
		return Priority{}, err
	}
	return Priority{Background: true}, nil
}

// inferredPriority is what a lowered Darwin process carries.
func inferredPriority() Priority { return Priority{Background: true} }

func restore(id model.ProcessIdentity, p Priority) error {
	if !p.Background {
		return nil
	}
	if err := sameProcess(id); err != nil {
		return err
	}
	return unix.Setpriority(prioDarwinProcess, id.PID, 0)
}

// signal has a short window between revalidation and delivery; Darwin has
// no process descriptors to close it.
func signal(id model.ProcessIdentity, sig syscall.Signal) error {
	if err := revalidate(id); err != nil {
		return err
	}
	return unix.Kill(id.PID, sig)
}

// reclaim is unsupported: Darwin offers no interface to page out another
// process. A frozen process's idle pages are compressed or swapped over time.
func reclaim(*Group) (string, error) { return "", errUnsupported }
