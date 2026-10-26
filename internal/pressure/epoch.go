package pressure

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
)

// changeEpoch keeps obligations for independent provider reconciliation. It
// drops all signal authority and pressure evidence from the old epoch.
func changeEpoch(old State, in Input, p Policy) (State, []Action, error) {
	s := State{Epoch: in.Epoch, LastTick: in.Tick, LastNow: in.Now,
		Resources: make(map[Resource]Episode), Pauses: make(map[JobID]PauseRecord),
		JobWarnings:     make(map[Resource]map[JobID]JobWarning),
		Unresolved:      make(map[string]UnresolvedControl, len(old.Unresolved)+len(old.Pauses)),
		ControlRevision: old.ControlRevision}
	for key, obligation := range old.Unresolved {
		s.Unresolved[key] = obligation
	}
	settled := make(map[string]bool, len(in.Reconciled))
	for _, key := range in.Reconciled {
		settled[key] = true
		delete(s.Unresolved, key)
	}
	ids := make([]JobID, 0, len(old.Pauses))
	for jid := range old.Pauses {
		ids = append(ids, jid)
	}
	slices.SortFunc(ids, compareJob)
	var actions []Action
	for _, jid := range ids {
		record := old.Pauses[jid]
		if settled[record.PauseKey] {
			continue
		}
		if record.PauseKey == "" || record.Job != jid {
			return old, nil, errors.New("pressure: invalid old-epoch control record")
		}
		if _, exists := s.Unresolved[record.PauseKey]; exists {
			return old, nil, errors.New("pressure: duplicate old-epoch obligation")
		}
		s.Unresolved[record.PauseKey] = UnresolvedControl{Key: record.PauseKey, SourceEpoch: old.Epoch, Record: record}
		a := action(RequestReconciliation, record.Reason, record.Incident, 0, jid, "", in.Epoch, record.Revision)
		sum := sha256.Sum256([]byte(record.PauseKey))
		a.Key = "reconcile/" + hex.EncodeToString(sum[:])
		a.OriginalEpoch, a.At, a.Tick = old.Epoch, in.Now, in.Tick
		actions = append(actions, a)
	}
	if len(s.Unresolved) > p.MaxJobs {
		return old, nil, errors.New("pressure: unresolved obligations exceed policy bound")
	}
	return s, actions, nil
}
