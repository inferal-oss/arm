package kubejournal

import (
	"fmt"
	"time"

	"getarm.org/internal/kubesource"
)

// Every mutation returns a detached, closed-codec successor before replacing
// the caller's value. These methods record evidence; none can dispatch native
// work, reconstruct a lease, or mint a live review deadline from saved fields.
func (r *History) change(revision int64, now time.Time, change func(*History) error) error {
	if r == nil || r.Validate() != nil || revision != r.Revision || now.Before(r.Updated) {
		return fmt.Errorf("V2 workflow revision or observation clock changed")
	}
	next, err := r.Snapshot()
	if err != nil {
		return err
	}
	next.Revision++
	next.Updated = now.UTC()
	if err = change(&next); err != nil {
		return err
	}
	if err = r.ValidateSuccessor(next); err != nil {
		return err
	}
	if next, err = next.Snapshot(); err != nil {
		return err
	}
	*r = next
	return nil
}

func (r *History) ReviewStage(revision int64, name, role, generation string, now time.Time, installation ...History) error {
	return r.change(revision, now, func(next *History) error {
		n, err := next.stage(name)
		if err != nil {
			return err
		}
		s := &next.Stages[n]
		input, err := next.currentInstallationInput(installation)
		if err != nil {
			return err
		}
		if len(s.Reviews) >= MaxStageReviews || next.SourceUnresolved() {
			return fmt.Errorf("review history exhausted or original source unresolved")
		}
		inputs := []StageEvidenceRef{}
		if role == EffectRole {
			for _, prior := range next.Stages[:n] {
				ref, err := next.CaptureEvidence(prior.Name)
				if err != nil {
					return fmt.Errorf("predecessor stage has no current eligible evidence")
				}
				inputs = append(inputs, ref)
			}
		}
		if role == EffectRole {
			if stopped(next.Phase) || s.Effect != nil {
				return fmt.Errorf("original effect is stopped or already attempted")
			}
			for _, prior := range s.Reviews {
				if prior.Role == EffectRole && prior.Decision == "approved" && now.Before(prior.Expires) && next.CheckReviewEvidence(name, prior.SHA256) == nil && next.checkReviewInstallationInput(name, prior.SHA256, installation) == nil {
					return fmt.Errorf("original current effect approval remains live")
				}
			}
		} else if s.Effect == nil {
			return fmt.Errorf("read-only recovery requires the original effect attempt")
		}
		if len(s.Reviews) != 0 {
			last := s.Reviews[len(s.Reviews)-1]
			if last.Decision == "pending" && now.Before(last.Expires) {
				return fmt.Errorf("existing exact review remains undecided")
			}
		}
		steps, err := sourceSequence(name, role, next.Plan.Review.BinaryBytes)
		if err != nil {
			return err
		}
		review := StageReview{Role: role, Stage: name, StageKey: s.Key, InputSHA256: s.InputSHA256, Revision: next.Revision, Created: now.UTC(), Expires: now.UTC().Add(2 * time.Minute), ExpectedGeneration: generation, PriorSHA256: "", ProofInputs: inputs, Installation: input, Steps: steps, Effects: reviewEffects(name, role), Decision: "pending"}
		if len(inputs) > 0 {
			review.PriorSHA256 = inputs[len(inputs)-1].ObservationSHA256
		}
		if role != EffectRole {
			review.PriorObservation = uint8(len(s.Observations))
			review.PriorSHA256 = historyHash(s.Observations)
		}
		review.SHA256 = stageReviewHash(review)
		s.Reviews = append(s.Reviews, review)
		if !stopped(next.Phase) {
			next.Phase = "review"
		}
		return nil
	})
}

func (r *History) Decide(revision int64, name string, reviewRevision int64, hash string, approve bool, now time.Time) error {
	return r.change(revision, now, func(next *History) error {
		n, err := next.stage(name)
		if err != nil {
			return err
		}
		s := &next.Stages[n]
		if len(s.Reviews) == 0 {
			return fmt.Errorf("exact stage review missing")
		}
		review := &s.Reviews[len(s.Reviews)-1]
		if review.SHA256 != hash || review.Revision != reviewRevision || review.Decision != "pending" || !now.Before(review.Expires) || review.Role == EffectRole && stopped(next.Phase) {
			return fmt.Errorf("exact stage review changed, decided or expired")
		}
		review.Decided, review.DecisionRevision = now.UTC(), next.Revision
		review.Decision = "declined"
		if approve {
			review.Decision = "approved"
		}
		if !approve && review.Role == EffectRole {
			next.Phase = "declined"
		} else if !stopped(next.Phase) {
			next.Phase = "pending"
		}
		return nil
	})
}

func (r *History) Intent(revision int64, name, reviewSHA string, step uint8, scope kubesource.Scope, now time.Time, installation ...History) error {
	return r.change(revision, now, func(next *History) error {
		n, err := next.stage(name)
		if err != nil {
			return err
		}
		s := next.Stages[n]
		if err := next.checkReviewInstallationInput(name, reviewSHA, installation); err != nil {
			return err
		}
		if err := next.CheckReviewEvidence(name, reviewSHA); err != nil {
			return err
		}
		if len(s.Reviews) == 0 || s.Reviews[len(s.Reviews)-1].SHA256 != reviewSHA || next.SourceUnresolved() || len(next.Sources) >= MaxSourceHistory || scope.Validate() != nil || scope.Operation != next.Plan.Review.Key || scope.B != next.Plan.Review.BinaryBytes {
			return fmt.Errorf("source intent lacks current finite original review")
		}
		review := s.Reviews[len(s.Reviews)-1]
		if review.Decision != "approved" || !now.Before(review.Expires) || int(step) >= len(review.Steps) || review.Steps[step].Profile != string(scope.Stage) || review.Role == EffectRole && stopped(next.Phase) {
			return fmt.Errorf("source intent differs from approved source sequence")
		}
		if review.Role == EffectRole && step == 0 && s.Effect != nil || step > 0 && (s.Effect == nil || s.Effect.ReviewSHA256 != reviewSHA) {
			return fmt.Errorf("effect source cannot be replayed or verification reordered")
		}
		previous := step == 0
		for _, source := range next.Sources {
			if source.Scope.Attempt == scope.Attempt || source.ReviewSHA256 == reviewSHA && source.Step == step {
				return fmt.Errorf("source attempt is once-only")
			}
			if source.ReviewSHA256 == reviewSHA && source.Step+1 == step && sourceClosed(source) {
				previous = true
			}
		}
		if !previous {
			return fmt.Errorf("previous original source has not settled")
		}
		next.Sources = append(next.Sources, HistorySource{Stage: name, ReviewSHA256: reviewSHA, Step: step, Scope: Scope(scope), Intent: now.UTC(), IntentRevision: next.Revision})
		if !stopped(next.Phase) {
			next.Phase = "executing"
		}
		return nil
	})
}

func (r *History) Bind(revision int64, attempt string, binding kubesource.Binding, now time.Time) error {
	return r.change(revision, now, func(next *History) error {
		for n := range next.Sources {
			s := &next.Sources[n]
			if s.Scope.Attempt == attempt {
				if s.Binding != nil || s.Result != nil {
					return fmt.Errorf("original binding already acknowledged")
				}
				s.Binding, s.BoundRevision = &binding, next.Revision
				return nil
			}
		}
		return fmt.Errorf("original source intent missing")
	})
}

func (r *History) Attempt(revision int64, name, reviewSHA, generation string, now time.Time, installation ...History) error {
	return r.change(revision, now, func(next *History) error {
		n, err := next.stage(name)
		if err != nil {
			return err
		}
		s := &next.Stages[n]
		if err := next.checkReviewInstallationInput(name, reviewSHA, installation); err != nil {
			return err
		}
		if err := next.CheckReviewEvidence(name, reviewSHA); err != nil {
			return err
		}
		review, ok := reviewByHash(*s, reviewSHA)
		if !ok || review.Role != EffectRole || review.Decision != "approved" || !now.Before(review.Expires) || review.ExpectedGeneration != generation || s.Effect != nil || stopped(next.Phase) || len(s.Reviews) == 0 || s.Reviews[len(s.Reviews)-1].SHA256 != reviewSHA {
			return fmt.Errorf("effect lacks original current effect-role approval")
		}
		for _, source := range next.Sources {
			if source.ReviewSHA256 == reviewSHA && source.Step == 0 && source.Binding != nil && source.Result == nil {
				s.Effect = &HistoryEffect{Revision: next.Revision, ReviewSHA256: reviewSHA, Attempted: now.UTC()}
				next.Phase = "executing"
				return nil
			}
		}
		return fmt.Errorf("effect requires the acknowledged still-live original binding")
	})
}

func (r *History) FinishSource(revision int64, attempt string, result kubesource.Result, now time.Time) error {
	return r.change(revision, now, func(next *History) error {
		for n := range next.Sources {
			s := &next.Sources[n]
			if s.Scope.Attempt == attempt {
				if s.Binding == nil || s.Result != nil {
					return fmt.Errorf("source finish requires original binding and is immutable")
				}
				s.Result, s.Finished, s.FinishedRevision = &result, now.UTC(), next.Revision
				return nil
			}
		}
		return fmt.Errorf("original source intent missing")
	})
}

func (r *History) Observe(revision int64, name, reviewSHA string, result EffectResult, now time.Time, installation ...History) error {
	return r.change(revision, now, func(next *History) error {
		n, err := next.stage(name)
		if err != nil {
			return err
		}
		s := &next.Stages[n]
		if !result.Unknown {
			if err := next.checkReviewInstallationInput(name, reviewSHA, installation); err != nil {
				return err
			}
		}
		review, ok := reviewByHash(*s, reviewSHA)
		if !ok || review.Decision != "approved" || s.Effect == nil || len(s.Observations) >= MaxStageObservations {
			return fmt.Errorf("observation lacks bounded original attempt/review")
		}
		if review.Role == EffectRole && s.Effect.ReviewSHA256 != reviewSHA {
			return fmt.Errorf("observation substitutes original effect approval")
		}
		for _, prior := range s.Observations {
			if prior.ReviewSHA256 == reviewSHA {
				return fmt.Errorf("original observation is immutable; recovery needs a fresh read-only review")
			}
		}
		s.Observations = append(s.Observations, StageObservation{Revision: next.Revision, ReviewSHA256: reviewSHA, Role: review.Role, Observed: now.UTC(), Result: result})
		if !stopped(next.Phase) {
			if result.Unknown {
				next.Phase = "unknown"
			} else {
				next.Phase = "pending"
				complete := true
				for _, stage := range next.Stages {
					complete = complete && firstPositive(stage) != nil
				}
				if complete {
					next.Phase = "completed"
				}
			}
		}
		return nil
	})
}

func (r *History) Cancel(revision int64, now time.Time) error {
	return r.change(revision, now, func(next *History) error {
		if next.Phase == "completed" || next.Phase == "revoked" || next.Phase == "cancelled" {
			return fmt.Errorf("workflow already stopped or completed")
		}
		next.Phase = "cancelled"
		return nil
	})
}
