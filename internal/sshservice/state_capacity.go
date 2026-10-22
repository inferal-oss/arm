package sshservice

import (
	"errors"
	"fmt"
)

// ErrSourceCapacity is a definite refusal before source intent publication.
// It never describes a lost commit acknowledgement or uncertain execution.
var ErrSourceCapacity = errors.New("SSH source receipt capacity exhausted")

// CheckSourceCapacity is a read-only conservative preflight. ReserveSource
// repeats this exact check under source locks after existing-key fences.
func (s *State) CheckSourceCapacity() error {
	if s.History != nil && len(s.History.Busy) > 0 {
		return ErrHistoryBusy
	}
	if len(s.Intents) >= MaxSourceIntents {
		return fmt.Errorf("%w: intention roster full; no unresolved record removed", ErrSourceCapacity)
	}
	// Each pending invocation reserves a future completion. An interrupted
	// matching pending/completed pair counts once, not as two obligations.
	slots := map[string]bool{}
	for _, v := range s.Completions {
		slots[intentName(v.Intent)] = true
	}
	for _, v := range s.Seals {
		slots[intentName(v.Intent)] = true
	}
	for _, v := range s.Intents {
		slots[intentName(v)] = true
	}
	if len(slots) >= MaxCompletions {
		return fmt.Errorf("%w: completed history full; archive verified settled receipts explicitly before new execution", ErrSourceCapacity)
	}
	return nil
}
