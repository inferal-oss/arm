package kubejournal

import "fmt"

func (r History) identityHash() string {
	return historyHash(struct {
		Kind, Key, Owner, Host, Plan string
		Connection                   *ConnectionDraft
		Installation                 *InstallationEvidence
	}{r.Kind, r.Key, r.Owner, r.SourceHost, r.Plan.SHA256, r.Connection, r.Installation})
}

// ValidateSuccessor protects append-only custody and decisions when the future
// protected store applies a typed transition. A valid record is never itself
// a native dispatch capability. At most one durable history event is ACKed per
// revision; binding, attempt, closure and observation therefore cannot share
// one claimed acknowledgement.
func (r History) ValidateSuccessor(next History) error {
	if r.Validate() != nil || next.Validate() != nil || r.identityHash() != next.identityHash() || !r.Created.Equal(next.Created) || next.Revision != r.Revision+1 || next.Updated.Before(r.Updated) || len(next.Sources) < len(r.Sources) {
		return fmt.Errorf("V2 immutable identity, revision or history changed")
	}
	if stopped(r.Phase) && next.Phase != r.Phase {
		return fmt.Errorf("prior workflow stop decision erased")
	}
	events := 0
	for n, old := range r.Stages {
		v := next.Stages[n]
		if old.Name != v.Name || old.Key != v.Key || old.InputSHA256 != v.InputSHA256 || old.ExpectedPrior != v.ExpectedPrior || len(v.Reviews) < len(old.Reviews) || len(v.Observations) < len(old.Observations) || old.Effect != nil && !same(old.Effect, v.Effect) {
			return fmt.Errorf("V2 prior stage identity/effect/history erased")
		}
		if old.Effect == nil && v.Effect != nil {
			if v.Effect.Revision != next.Revision {
				return fmt.Errorf("effect attempt lacks this revision ACK")
			}
			events++
		}
		for i, prior := range old.Reviews {
			current := v.Reviews[i]
			if prior.Decision == "pending" && current.Decision != "pending" {
				if current.DecisionRevision != next.Revision {
					return fmt.Errorf("decision lacks this revision ACK")
				}
				current.Decision, current.Decided, current.DecisionRevision = "pending", prior.Decided, 0
				events++
			}
			if !same(prior, current) {
				return fmt.Errorf("prior exact review/decision replaced")
			}
		}
		for _, review := range v.Reviews[len(old.Reviews):] {
			if review.Revision != next.Revision || review.Decision != "pending" {
				return fmt.Errorf("new review bypasses separate decision ACK")
			}
			events++
		}
		for i, prior := range old.Observations {
			if !same(prior, v.Observations[i]) {
				return fmt.Errorf("prior positive or uncertain observation replaced")
			}
		}
		for _, observation := range v.Observations[len(old.Observations):] {
			if observation.Revision != next.Revision {
				return fmt.Errorf("observation lacks this revision ACK")
			}
			events++
		}
	}
	for n, old := range r.Sources {
		v := next.Sources[n]
		if old.Stage != v.Stage || old.Scope != v.Scope || old.ReviewSHA256 != v.ReviewSHA256 || old.Step != v.Step || old.IntentRevision != v.IntentRevision || !old.Intent.Equal(v.Intent) {
			return fmt.Errorf("prior original source intent replaced")
		}
		if old.Binding == nil && v.Binding != nil {
			if v.BoundRevision != next.Revision {
				return fmt.Errorf("source binding lacks this revision ACK")
			}
			events++
		} else if !same(old.Binding, v.Binding) || old.BoundRevision != v.BoundRevision {
			return fmt.Errorf("original source binding replaced")
		}
		if old.Result == nil && v.Result != nil {
			if v.FinishedRevision != next.Revision {
				return fmt.Errorf("source closure lacks this revision ACK")
			}
			events++
		} else if !same(old.Result, v.Result) || old.FinishedRevision != v.FinishedRevision || !old.Finished.Equal(v.Finished) {
			return fmt.Errorf("original source closure/settlement replaced")
		}
	}
	for _, source := range next.Sources[len(r.Sources):] {
		if source.IntentRevision != next.Revision || source.Binding != nil || source.Result != nil {
			return fmt.Errorf("new source skips its separate original intent ACK")
		}
		events++
	}
	if events > 1 || events == 0 && (next.Phase != "cancelled" || r.Phase == next.Phase) {
		return fmt.Errorf("V2 successor must acknowledge one exact event")
	}
	return nil
}
