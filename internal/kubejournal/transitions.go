package kubejournal

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"getarm.org/internal/kubeprovision"
	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
)

func New(kind, key, owner, host string, plan kubeprovision.Plan, connection *ConnectionDraft, now time.Time) (Record, error) {
	r := Record{Schema: Schema, Kind: kind, Key: key, Owner: owner, SourceHost: host, Revision: 1, Created: now.UTC(), Updated: now.UTC(), Plan: plan, Phase: "pending", Connection: connection, Stages: []Stage{}, Sources: []SourceAttempt{}}
	prior := ""
	for _, name := range stageRoster(kind) {
		r.Stages = append(r.Stages, Stage{Name: name, Key: StageKey(key, name), InputSHA256: r.InputHash(name), State: "pending", ExpectedPrior: prior})
		prior = name
	}
	// The returned record has no aliases to the caller's mutable Plan/grants.
	raw, err := json.Marshal(r)
	if err == nil {
		err = Decode(raw, &r)
	}
	if err == nil {
		err = r.Validate()
	}
	return r, err
}
func (r *Record) change(revision int64, now time.Time, fn func(*Record) error) error {
	if r == nil || r.Validate() != nil || revision != r.Revision || now.Before(r.Updated) {
		return fmt.Errorf("Kubernetes workflow revision or clock changed")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	var next Record
	if err = Decode(raw, &next); err != nil {
		return err
	}
	next.Revision++
	next.Updated = now.UTC()
	if err = fn(&next); err != nil {
		return err
	}
	if err = r.ValidateSuccessor(next); err != nil {
		return err
	}
	*r = next
	return nil
}
func (r Record) stage(name string) (int, error) {
	for n, s := range r.Stages {
		if s.Name == name {
			return n, nil
		}
	}
	return 0, fmt.Errorf("unknown stage for this Kubernetes workflow")
}
func (r Record) priorHash(name string) string {
	n, err := r.stage(name)
	if err != nil || n == 0 {
		return ""
	}
	if r.Stages[n-1].Result == nil {
		return ""
	}
	return Hash(*r.Stages[n-1].Result)
}
func (r Record) next(name string) (int, error) {
	n, err := r.stage(name)
	if err != nil {
		return 0, err
	}
	for _, s := range r.Stages[:n] {
		if s.State != "confirmed" {
			return 0, fmt.Errorf("previous Kubernetes stage unconfirmed")
		}
	}
	if r.SourceUnresolved() {
		return 0, fmt.Errorf("original source closure/settlement unresolved")
	}
	if slices.Contains([]string{"declined", "cancelled", "completed", "revoked", "disconnected"}, r.Phase) {
		return 0, fmt.Errorf("Kubernetes workflow is lookup-only")
	}
	return n, nil
}
func effects(stage string) []string {
	switch stage {
	case "create":
		return []string{"Create one exact reviewed Pod"}
	case "ship":
		return []string{"Ship the reviewed native carrier into the original Pod"}
	case "start":
		return []string{"Write the original start marker; verify the running backend separately"}
	case "local-listener-prepare", "pod-listener-prepare":
		return []string{"Reserve one inactive native listener for two minutes", "Authorize cleanup of this exact uncommitted reservation"}
	case "local-listener-apply", "pod-listener-apply":
		return []string{"Transfer the same original socket to a pending-proof listener"}
	case "local-pair", "pod-pair":
		return []string{"Write the exact pending directional pair"}
	case "prove":
		return []string{"Allow bounded hello/prove only", "Close and join the original forward before activation"}
	case "local-activate", "pod-activate":
		return []string{"Activate the exact proven pair once"}
	}
	return nil
}
func (r *Record) ReviewStage(revision int64, name, generation string, now time.Time) error {
	return r.change(revision, now, func(next *Record) error {
		n, err := next.next(name)
		if err != nil {
			return err
		}
		s := &next.Stages[n]
		if !slices.Contains([]string{"pending", "review"}, s.State) || s.Approved {
			return fmt.Errorf("stage has an immutable prior decision")
		}
		if generation != "" && !idPattern.MatchString(generation) {
			return fmt.Errorf("invalid expected backend generation")
		}
		s.ExpectedGeneration = generation
		scope := kubesource.Scope{Stage: profile(name), B: next.Plan.Review.BinaryBytes}
		review := Review{Stage: name, StageKey: s.Key, InputSHA256: s.InputSHA256, Revision: next.Revision, Created: now.UTC(), Expires: now.UTC().Add(2 * time.Minute), ExpectedGeneration: generation, ExpectedPrior: s.ExpectedPrior, PriorSHA256: next.priorHash(name), SourceProfile: string(scope.Stage), SourceResources: model.Resources{CPU: kubesource.SourceCPU, Memory: kubesource.SourceMemory}, SourceTTL: kubesource.SourceTTL, StorageBytes: scope.DiskBytes(), StorageTTL: kubesource.ParentTTL, Effects: effects(name)}
		review.SHA256 = reviewHash(review)
		s.Review = &review
		s.State = "review"
		next.Phase = "review"
		return nil
	})
}
func (r *Record) Decide(revision int64, name string, reviewRevision int64, hash string, approve bool, now time.Time) error {
	return r.change(revision, now, func(next *Record) error {
		n, err := next.next(name)
		if err != nil {
			return err
		}
		s := &next.Stages[n]
		if s.State != "review" || s.Review == nil || s.Review.Revision != reviewRevision || s.Review.SHA256 != hash || !now.Before(s.Review.Expires) {
			return fmt.Errorf("stage review changed or expired")
		}
		if !approve {
			s.State = "declined"
			next.Phase = "declined"
			return nil
		}
		s.Approved = true
		s.ApprovedRevision = reviewRevision
		s.ApprovedSHA256 = hash
		s.State = "approved"
		next.Phase = "pending"
		return nil
	})
}
func (r *Record) Intent(revision int64, name string, scope kubesource.Scope, now time.Time) error {
	return r.change(revision, now, func(next *Record) error {
		n, err := next.next(name)
		if err != nil {
			return err
		}
		s := next.Stages[n]
		if scope.Validate() != nil || scope.Operation != next.Plan.Review.Key || scope.Stage != profile(name) || scope.B != next.Plan.Review.BinaryBytes || s.State != "approved" || !now.Before(s.Review.Expires) || len(next.Sources) >= 128 {
			return fmt.Errorf("source intent lacks approved finite stage scope")
		}
		for _, a := range next.Sources {
			if a.Scope.Attempt == scope.Attempt || a.Stage == name {
				return fmt.Errorf("original stage source is lookup-only")
			}
		}
		next.Sources = append(next.Sources, SourceAttempt{Scope: Scope(scope), Stage: name, Intent: now.UTC()})
		next.Phase = "executing"
		return nil
	})
}
func (r *Record) Bind(revision int64, attempt string, binding kubesource.Binding, now time.Time) error {
	return r.change(revision, now, func(next *Record) error {
		for n := range next.Sources {
			a := &next.Sources[n]
			if a.Scope.Attempt != attempt {
				continue
			}
			if a.Binding != nil || a.Result != nil {
				return fmt.Errorf("source binding already recorded")
			}
			a.Binding = &binding
			return nil
		}
		return fmt.Errorf("source intent missing")
	})
}
func (r *Record) Attempt(revision int64, name, generation string, now time.Time) error {
	return r.change(revision, now, func(next *Record) error {
		if slices.Contains([]string{"cancelled", "declined", "revoked", "completed", "disconnected"}, next.Phase) {
			return fmt.Errorf("stopped Kubernetes workflow cannot attempt an effect")
		}
		n, err := next.stage(name)
		if err != nil {
			return err
		}
		s := &next.Stages[n]
		if s.State != "approved" || s.ExpectedGeneration != generation || !now.Before(s.Review.Expires) {
			return fmt.Errorf("stage approval or generation changed")
		}
		for _, a := range next.Sources {
			if a.Stage == name && a.Binding != nil && a.Result == nil {
				s.State = "attempted"
				s.Attempted = now.UTC()
				return nil
			}
		}
		return fmt.Errorf("stage has no acknowledged original source binding")
	})
}
func (r *Record) FinishSource(revision int64, attempt string, result kubesource.Result, now time.Time) error {
	return r.change(revision, now, func(next *Record) error {
		for n := range next.Sources {
			a := &next.Sources[n]
			if a.Scope.Attempt != attempt {
				continue
			}
			if a.Result != nil {
				return fmt.Errorf("original source finish is immutable")
			}
			a.Result = &result
			a.Finished = now.UTC()
			return nil
		}
		return fmt.Errorf("original source intent missing")
	})
}
func (r *Record) Observe(revision int64, name, key string, result EffectResult, now time.Time) error {
	return r.change(revision, now, func(next *Record) error {
		n, err := next.stage(name)
		if err != nil {
			return err
		}
		s := &next.Stages[n]
		if key != s.Key || !slices.Contains([]string{"attempted", "unknown"}, s.State) {
			return fmt.Errorf("stage permits exact attempted-key lookup only")
		}
		if s.State == "unknown" && !same(*s.Result, result) && result.Unknown {
			return fmt.Errorf("uncertain result cannot replace earlier outcome")
		}
		s.Result = &result
		if result.Unknown {
			s.State = "unknown"
			if next.Phase != "cancelled" {
				next.Phase = "unknown"
			}
			return nil
		}
		if next.SourceUnresolved() {
			return fmt.Errorf("source must retire before confirming a stage")
		}
		s.State = "confirmed"
		s.Confirmed = now.UTC()
		if next.Phase == "cancelled" {
			return nil
		}
		next.Phase = "pending"
		if n == len(next.Stages)-1 {
			next.Phase = "completed"
		}
		return nil
	})
}
func (r *Record) Cancel(revision int64, now time.Time) error {
	return r.change(revision, now, func(next *Record) error {
		if next.Phase == "completed" || next.Phase == "revoked" {
			return fmt.Errorf("completed workflow cannot be cancelled")
		}
		// Prior approvals, attempted effects and source debt remain immutable.
		next.Phase = "cancelled"
		return nil
	})
}
