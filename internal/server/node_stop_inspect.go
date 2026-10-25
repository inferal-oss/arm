package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/nodestop"
	"github.com/yrashk/arm/internal/storage"
	"golang.org/x/sys/unix"
)

type NodeStopObservation = nodestop.Observation

// InspectNodeStop is the fixed read-only native helper implementation. It does
// not create home/locks/schema, start a backend, signal a PID or retry a stop.
// A busy/new authority is conservatively unknown here; a future live inspector
// may add positive replacement-generation evidence without changing this rule.
func InspectNodeStop(ctx context.Context, home string, target nodestop.Target, key string) (out NodeStopObservation, resultErr error) {
	return inspectNodeStopLocked(ctx, home, target, key, nil)
}

// WithInspectedNodeStop retains the actual original daemon lock, directory and
// committed read source through a narrowly scoped native maintenance action.
// It confers no process or file authority: the action must prove its own scope.
func WithInspectedNodeStop(ctx context.Context, home string, target nodestop.Target, key string, action func(NodeStopObservation) error) (NodeStopObservation, error) {
	if action == nil {
		return NodeStopObservation{}, fmt.Errorf("scoped maintenance action required")
	}
	return inspectNodeStopLocked(ctx, home, target, key, action)
}

func inspectNodeStopLocked(ctx context.Context, home string, target nodestop.Target, key string, action func(NodeStopObservation) error) (out NodeStopObservation, resultErr error) {
	dir, err := database.OpenDirectory(home)
	if err != nil {
		return out, err
	}
	defer func() { resultErr = errors.Join(resultErr, dir.Close()) }()
	id, err := storage.DirectoryIdentity(int(dir.FD.Fd()))
	if err != nil || id != target.Directory || dir.Path != target.Home {
		return out, fmt.Errorf("original private authority directory differs")
	}
	lock, _, err := dir.Open("daemon.lock", unix.O_RDONLY)
	if err != nil {
		return out, err
	}
	if lock == nil {
		return out, fmt.Errorf("original authority lock missing")
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return out, fmt.Errorf("exact authority absence not established: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, unix.Flock(int(lock.Fd()), unix.LOCK_UN)) }()
	lockID, err := storage.DirectoryIdentity(int(lock.Fd()))
	if err != nil {
		return out, err
	}
	source, exists, err := database.OpenStartupRead(ctx, home, lock)
	if err != nil {
		return out, err
	}
	defer func() { resultErr = errors.Join(resultErr, source.Close()) }()
	if !exists {
		return out, fmt.Errorf("original database absent; no completion verdict inferred")
	}
	state, present, err := nodestop.ReadState(source)
	if err != nil {
		return out, err
	}
	if !present {
		return out, fmt.Errorf("stop intent absent; no nonexecution or absence verdict inferred")
	}
	var ledgerHost string
	if e := source.QueryRow(`SELECT value FROM metadata WHERE key='host'`).Scan(&ledgerHost); e != nil || ledgerHost != target.Host {
		return out, fmt.Errorf("original ledger authority changed")
	}
	record, err := nodestop.Lookup(source, key)
	if err != nil {
		return out, err
	}
	if record.Plan.Target != target || record.Phase != "drain-ready" {
		return out, fmt.Errorf("original stop generation has no complete drain record")
	}
	if err = readStopMarker(dir, record, lockID); err != nil {
		return out, err
	}
	endpoint, err := dir.Observe("endpoint.json", false)
	if err != nil {
		return out, err
	}
	if endpoint.Present {
		return out, fmt.Errorf("endpoint still present; exact authority absence not certified")
	}
	traceRows := traceStopReadRows(home, source)
	if err = source.Validate(ctx); err != nil {
		return out, err
	}
	if err = dir.Validate(); err != nil {
		return out, err
	}
	// The matched original native lock is still held exclusively. These flags
	// describe this generation only, never all hosts/services or old PIDs.
	observation := NodeStopObservation{Record: record, Desired: state, ServerJoined: true, OriginalGenerationAbsent: true}
	traceStopObserved(home, observation, traceRows)
	if action != nil {
		traceUninstallNativeBoundary("native-enter", home, observation, nil)
		// End of the maintenance-action authority, not an invented FD/process join.
		defer func() { traceUninstallNativeBoundary("native-leave", home, observation, resultErr) }()
		if err = action(observation); err != nil {
			return out, err
		}
		if err = source.Validate(ctx); err != nil {
			return out, err
		}
		if err = dir.Validate(); err != nil {
			return out, err
		}
	}
	return observation, nil
}
