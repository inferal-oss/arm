package kubejournal

import (
	"fmt"
	"math"
	"slices"
	"time"

	"getarm.org/internal/kubesource"
)

func (r History) stage(name string) (int, error) {
	for n, stage := range r.Stages {
		if stage.Name == name {
			return n, nil
		}
	}
	return 0, fmt.Errorf("unknown V2 workflow stage")
}
func firstPositive(stage HistoryStage) *EffectResult {
	for _, observation := range stage.Observations {
		if !observation.Result.Unknown {
			result := observation.Result
			return &result
		}
	}
	return nil
}
func verificationAction(stage string) (string, bool) {
	actions := map[string]string{
		"create":                 "lookup_original_pod",
		"local-listener-prepare": "inspect_original_local_listener_preparation",
		"pod-listener-prepare":   "inspect_original_pod_listener_preparation",
		"local-listener-apply":   "inspect_original_local_listener",
		"pod-listener-apply":     "inspect_original_pod_listener",
		"local-pair":             "inspect_original_local_pair",
		"pod-pair":               "inspect_original_pod_pair",
		"local-activate":         "inspect_original_local_activation",
		"pod-activate":           "inspect_original_pod_activation",
	}
	v, ok := actions[stage]
	return v, ok
}

// CaptureEvidence is a data reference, never proof of a live native owner.
// The first positive result is historical only. Current eligibility always
// names the latest exact prefix and refuses a newer unknown observation.
func (r History) CaptureEvidence(name string) (StageEvidenceRef, error) {
	n, err := r.stage(name)
	if err != nil {
		return StageEvidenceRef{}, err
	}
	s := r.Stages[n]
	if len(s.Observations) == 0 || len(s.Observations) > MaxStageObservations || s.Observations[len(s.Observations)-1].Result.Unknown {
		return StageEvidenceRef{}, fmt.Errorf("current stage evidence is unavailable")
	}
	latest := s.Observations[len(s.Observations)-1]
	// A connection's reviewed original identities cannot be silently replaced
	// by another positive read. Installation runtime refresh is instead bound
	// into a new connection's explicit immutable input and review.
	if r.Connection != nil && !same(firstPositive(s), &latest.Result) {
		return StageEvidenceRef{}, fmt.Errorf("current connection identity/evidence changed")
	}
	return StageEvidenceRef{Observed: latest.Observed, Stage: name, Count: uint8(len(s.Observations)), PrefixSHA256: historyHash(s.Observations), ObservationSHA256: historyHash(latest)}, nil
}
func (r History) CheckEvidence(ref StageEvidenceRef) error {
	current, err := r.CaptureEvidence(ref.Stage)
	if err != nil || current != ref {
		return fmt.Errorf("current stage observation differs from captured review input")
	}
	return nil
}
func (r History) CheckInstallation(installation History) error {
	if r.Connection == nil || r.Installation == nil || installation.Validate() != nil || installation.Kind != "kubernetes_install" || installation.Phase != "completed" || installation.SourceUnresolved() || installation.Key != r.Installation.Key || installation.Plan.SHA256 != r.Plan.SHA256 || installation.Owner != r.Owner || installation.SourceHost != r.SourceHost {
		return fmt.Errorf("current original installation is unavailable or changed")
	}
	if _, err := installation.CaptureEvidence("start"); err != nil {
		return err
	}
	n, _ := installation.stage("start")
	if !same(installation.Stages[n].Observations[len(installation.Stages[n].Observations)-1].Result.Runtime, &r.Connection.Runtime) {
		return fmt.Errorf("current runtime differs from pinned connection generation")
	}
	return nil
}
func (r History) currentInstallationInput(installation []History) (*InstallationEvidence, error) {
	if err := r.checkInstallationInput(installation); err != nil {
		return nil, err
	}
	if r.Connection == nil {
		return nil, nil
	}
	ref, err := installation[0].CaptureEvidence("start")
	if err != nil {
		return nil, err
	}
	return &InstallationEvidence{Key: installation[0].Key, PlanSHA256: installation[0].Plan.SHA256, SourceHost: installation[0].SourceHost, Owner: installation[0].Owner, Observation: ref}, nil
}
func (r History) CheckReviewInstallation(name, hash string, installation History) error {
	if err := r.CheckInstallation(installation); err != nil {
		return err
	}
	n, err := r.stage(name)
	if err != nil {
		return err
	}
	review, ok := reviewByHash(r.Stages[n], hash)
	if !ok || review.Installation == nil {
		return fmt.Errorf("exact review installation input missing")
	}
	current, err := r.currentInstallationInput([]History{installation})
	if err != nil || !same(current, review.Installation) {
		return fmt.Errorf("current installation prefix differs from exact review")
	}
	return nil
}
func (r History) checkReviewInstallationInput(name, hash string, installation []History) error {
	if r.Connection == nil {
		return r.checkInstallationInput(installation)
	}
	if len(installation) != 1 {
		return fmt.Errorf("current installation snapshot required")
	}
	return r.CheckReviewInstallation(name, hash, installation[0])
}
func (r History) checkInstallationInput(installation []History) error {
	if r.Connection == nil {
		if len(installation) != 0 {
			return fmt.Errorf("unexpected installation input")
		}
		return nil
	}
	if len(installation) != 1 {
		return fmt.Errorf("exact current installation snapshot required")
	}
	return r.CheckInstallation(installation[0])
}
func (r History) CheckReviewEvidence(name, hash string) error {
	n, err := r.stage(name)
	if err != nil {
		return err
	}
	review, ok := reviewByHash(r.Stages[n], hash)
	if !ok {
		return fmt.Errorf("original exact review missing")
	}
	for _, ref := range review.ProofInputs {
		if err := r.CheckEvidence(ref); err != nil {
			return err
		}
	}
	return nil
}
func (r History) priorHash(n int) string {
	if n == 0 {
		return ""
	}
	if prior := firstPositive(r.Stages[n-1]); prior != nil {
		return historyHash(prior)
	}
	return ""
}
func reviewByHash(stage HistoryStage, hash string) (StageReview, bool) {
	for _, review := range stage.Reviews {
		if review.SHA256 == hash {
			return review, true
		}
	}
	return StageReview{}, false
}
func stopped(phase string) bool {
	return slices.Contains([]string{"cancelled", "declined", "revoked", "disconnected", "completed"}, phase)
}
func sourceClosed(source HistorySource) bool {
	v := source.Result
	return v != nil && v.SourceClosed && v.SourceSettled && (source.Scope.Stage != "f" || v.PortClosed && v.PortSettled)
}
func (r History) SourceUnresolved() bool {
	for _, source := range r.Sources {
		if !sourceClosed(source) {
			return true
		}
	}
	return false
}
func (r History) reviewClosed(review StageReview, revision int64, at time.Time) bool {
	for n := range review.Steps {
		found := false
		for _, source := range r.Sources {
			if source.ReviewSHA256 == review.SHA256 && int(source.Step) == n {
				found = sourceClosed(source) && source.FinishedRevision < revision && !source.Finished.After(at)
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func reviewEffects(stage, role string) []string {
	if role == VerificationRole {
		return []string{"Read only the original Pod and exact-key receipts", "Never resend payload, start marker or a trust mutation"}
	}
	if role == CarrierRole {
		return []string{"Allow bounded original hello/prove transport only", "Close and join this original carrier before recording its proof"}
	}
	if stage == "start" {
		return []string{"Write the original start marker once", "Retire t before separately admitting i for fresh exact runtime proof"}
	}
	return effects(stage)
}

func (r History) Validate() error {
	if r.Schema != HistorySchema || r.Revision < 1 || r.Revision == math.MaxInt64 || r.Updated.Before(r.Created) || !slices.Contains([]string{"pending", "review", "executing", "unknown", "declined", "cancelled", "completed", "disconnected", "revoked"}, r.Phase) {
		return fmt.Errorf("invalid V2 workflow identity or phase")
	}
	if _, err := New(r.Kind, r.Key, r.Owner, r.SourceHost, r.Plan, r.Connection, r.Created); err != nil {
		return err
	}
	if r.Connection == nil && r.Installation != nil || r.Connection != nil && (r.Installation == nil || r.Installation.Key != r.Plan.Review.Key || r.Installation.PlanSHA256 != r.Plan.SHA256 || r.Installation.SourceHost != r.SourceHost || r.Installation.Owner != r.Owner || r.Installation.Observation.Stage != "start" || r.Installation.Observation.Observed.IsZero() || r.Installation.Observation.Observed.After(r.Created) || r.Installation.Observation.Count < 1 || r.Installation.Observation.Count > MaxStageObservations || !digestPattern.MatchString(r.Installation.Observation.PrefixSHA256) || !digestPattern.MatchString(r.Installation.Observation.ObservationSHA256)) {
		return fmt.Errorf("connection lacks exact installation observation reference")
	}
	roster := stageRoster(r.Kind)
	if r.Stages == nil || len(r.Stages) != len(roster) || r.Sources == nil || len(r.Sources) > MaxSourceHistory {
		return fmt.Errorf("invalid bounded V2 history")
	}
	prior, incomplete := "", false
	reviewHashes := map[string]bool{}
	for n, stage := range r.Stages {
		if stage.Name != roster[n] || stage.Key != historyStageKey(r.Key, stage.Name) || stage.InputSHA256 != r.InputHash(stage.Name) || stage.ExpectedPrior != prior || stage.Reviews == nil || len(stage.Reviews) > MaxStageReviews || stage.Observations == nil || len(stage.Observations) > MaxStageObservations {
			return fmt.Errorf("invalid V2 stage identity or history bound")
		}
		if incomplete && (len(stage.Reviews) != 0 || stage.Effect != nil || len(stage.Observations) != 0) {
			return fmt.Errorf("V2 stage passed an unconfirmed predecessor")
		}
		if err := r.validateHistoryStage(n, reviewHashes); err != nil {
			return err
		}
		incomplete = incomplete || firstPositive(stage) == nil
		prior = stage.Name
	}
	if r.Phase == "completed" && incomplete {
		return fmt.Errorf("completed V2 workflow lacks confirmed stages")
	}
	if err := r.validateHistorySources(); err != nil {
		return err
	}
	return nil
}

func (r History) validateHistoryStage(n int, hashes map[string]bool) error {
	stage := r.Stages[n]
	for i, review := range stage.Reviews {
		steps, err := sourceSequence(stage.Name, review.Role, r.Plan.Review.BinaryBytes)
		if err != nil || review.Stage != stage.Name || review.StageKey != stage.Key || review.InputSHA256 != stage.InputSHA256 || review.Revision < 2 || review.Revision > r.Revision || !digestPattern.MatchString(review.SHA256) || hashes[review.SHA256] || stageReviewHash(review) != review.SHA256 || review.Created.Before(r.Created) || review.Created.After(r.Updated) || !review.Expires.After(review.Created) || review.Expires.Sub(review.Created) != 2*time.Minute || !same(review.Steps, steps) || !same(review.Effects, reviewEffects(stage.Name, review.Role)) || review.ExpectedGeneration != "" && !idPattern.MatchString(review.ExpectedGeneration) {
			return fmt.Errorf("invalid exact V2 stage review")
		}
		hashes[review.SHA256] = true
		if i > 0 && (review.Revision <= stage.Reviews[i-1].Revision || review.Created.Before(stage.Reviews[i-1].Created)) {
			return fmt.Errorf("V2 review order changed")
		}
		if r.Connection != nil && review.ExpectedGeneration != r.Connection.Runtime.Generation {
			return fmt.Errorf("review generation differs from pinned connection")
		}
		if r.Connection == nil && review.Installation != nil || r.Connection != nil && (review.Installation == nil || review.Installation.Key != r.Installation.Key || review.Installation.PlanSHA256 != r.Plan.SHA256 || review.Installation.SourceHost != r.SourceHost || review.Installation.Owner != r.Owner || review.Installation.Observation.Stage != "start" || review.Installation.Observation.Count < 1 || review.Installation.Observation.Count > MaxStageObservations || review.Installation.Observation.Observed.IsZero() || review.Installation.Observation.Observed.After(review.Created) || !digestPattern.MatchString(review.Installation.Observation.PrefixSHA256) || !digestPattern.MatchString(review.Installation.Observation.ObservationSHA256)) {
			return fmt.Errorf("review lacks a bounded exact installation prefix")
		}
		if review.Role == EffectRole {
			if review.PriorObservation != 0 || review.ProofInputs == nil || len(review.ProofInputs) != n {
				return fmt.Errorf("effect review predecessor changed")
			}
			prior := ""
			for i, ref := range review.ProofInputs {
				input := r.Stages[i]
				if ref.Stage != input.Name || ref.Count < 1 || int(ref.Count) > len(input.Observations) || ref.PrefixSHA256 != historyHash(input.Observations[:ref.Count]) || ref.ObservationSHA256 != historyHash(input.Observations[ref.Count-1]) || !ref.Observed.Equal(input.Observations[ref.Count-1].Observed) || input.Observations[ref.Count-1].Result.Unknown || input.Observations[ref.Count-1].Revision >= review.Revision {
					return fmt.Errorf("captured review prefix is not historical positive evidence")
				}
				prior = ref.ObservationSHA256
			}
			if review.PriorSHA256 != prior {
				return fmt.Errorf("review predecessor observation hash differs")
			}
		} else if stage.Effect == nil || review.ProofInputs == nil || len(review.ProofInputs) != 0 || review.Created.Before(stage.Effect.Attempted) || int(review.PriorObservation) > len(stage.Observations) || review.PriorSHA256 != historyHash(stage.Observations[:review.PriorObservation]) {
			return fmt.Errorf("read-only review lost original effect/history binding")
		}
		if !slices.Contains([]string{"pending", "approved", "declined"}, review.Decision) || review.Decision == "pending" && (!review.Decided.IsZero() || review.DecisionRevision != 0) || review.Decision != "pending" && (review.DecisionRevision <= review.Revision || review.DecisionRevision > r.Revision || review.Decided.Before(review.Created) || !review.Decided.Before(review.Expires) || review.Decided.After(r.Updated)) {
			return fmt.Errorf("V2 decision is missing, stale or malformed")
		}
		if review.Role == EffectRole && stage.Effect != nil && review.Revision > stage.Effect.Revision {
			return fmt.Errorf("new effect review follows original effect attempt")
		}
	}
	if stage.Effect != nil {
		review, ok := reviewByHash(stage, stage.Effect.ReviewSHA256)
		if !ok || review.Role != EffectRole || review.Decision != "approved" || stage.Effect.Revision <= review.DecisionRevision || stage.Effect.Revision > r.Revision || stage.Effect.Attempted.Before(review.Decided) || !stage.Effect.Attempted.Before(review.Expires) || stage.Effect.Attempted.After(r.Updated) {
			return fmt.Errorf("effect lacks original exact live approval")
		}
		for _, later := range stage.Reviews {
			if later.Revision > review.Revision && later.Revision < stage.Effect.Revision {
				return fmt.Errorf("effect used a superseded review")
			}
		}
		bound := false
		for _, source := range r.Sources {
			if source.ReviewSHA256 == review.SHA256 && source.Step == 0 && source.Binding != nil && source.BoundRevision < stage.Effect.Revision && (source.Result == nil || source.FinishedRevision > stage.Effect.Revision) && !source.Intent.After(stage.Effect.Attempted) {
				bound = true
			}
		}
		if !bound {
			return fmt.Errorf("effect lacks acknowledged original source")
		}
	}
	seen := map[string]bool{}
	for i, observation := range stage.Observations {
		review, ok := reviewByHash(stage, observation.ReviewSHA256)
		if !ok || review.Decision != "approved" || observation.Role != review.Role || seen[review.SHA256] || stage.Effect == nil || observation.Revision <= stage.Effect.Revision || observation.Revision <= review.DecisionRevision || observation.Revision > r.Revision || observation.Observed.Before(stage.Effect.Attempted) || observation.Observed.Before(review.Decided) || observation.Observed.After(r.Updated) || i > 0 && (observation.Revision <= stage.Observations[i-1].Revision || observation.Observed.Before(stage.Observations[i-1].Observed)) {
			return fmt.Errorf("invalid original V2 observation order/binding")
		}
		seen[review.SHA256] = true
		if observation.Role != EffectRole && int(review.PriorObservation) > i {
			return fmt.Errorf("verification observes before its captured history")
		}
		if observation.Result.Unknown {
			if !same(observation.Result, EffectResult{Unknown: true}) {
				return fmt.Errorf("uncertain observation carries mixed positive proof")
			}
		} else if err := validateEffect(stage.Name, observation.Result); err != nil || !r.reviewClosed(review, observation.Revision, observation.Observed) {
			return fmt.Errorf("positive observation lacks exact result and original settlement")
		}
		if n > 0 && r.Kind == "kubernetes_install" && !observation.Result.Unknown {
			pod := firstPositive(r.Stages[0]).Pod
			if observation.Result.Pod != nil && observation.Result.Pod.UID != pod.UID || observation.Result.Runtime != nil && observation.Result.Runtime.PodUID != pod.UID {
				return fmt.Errorf("later observation substitutes original Pod UID")
			}
		}
	}
	return nil
}

func (r History) validateHistorySources() error {
	attempts, bindings, steps := map[string]bool{}, map[string]bool{}, map[string]bool{}
	unresolved := false
	for _, source := range r.Sources {
		scope, err := source.Scope.Native()
		n, stageErr := r.stage(source.Stage)
		if err != nil || stageErr != nil || scope.Operation != r.Plan.Review.Key || scope.B != r.Plan.Review.BinaryBytes || attempts[scope.Attempt] || unresolved || source.IntentRevision < 2 || source.IntentRevision > r.Revision || source.Intent.Before(r.Created) || source.Intent.After(r.Updated) {
			return fmt.Errorf("invalid, overlapping or repeated V2 source")
		}
		attempts[scope.Attempt] = true
		review, ok := reviewByHash(r.Stages[n], source.ReviewSHA256)
		key := fmt.Sprintf("%s/%d", review.SHA256, source.Step)
		if !ok || review.Decision != "approved" || int(source.Step) >= len(review.Steps) || review.Steps[source.Step].Profile != source.Scope.Stage || source.IntentRevision <= review.DecisionRevision || source.Intent.Before(review.Decided) || !source.Intent.Before(review.Expires) || steps[key] {
			return fmt.Errorf("source lacks exact once-only reviewed sequence")
		}
		if source.Step > 0 && (!steps[fmt.Sprintf("%s/%d", review.SHA256, source.Step-1)] || r.Stages[n].Effect == nil || source.IntentRevision <= r.Stages[n].Effect.Revision) {
			return fmt.Errorf("source sequence skipped its predecessor")
		}
		steps[key] = true
		if b := source.Binding; b != nil {
			if source.BoundRevision <= source.IntentRevision || source.BoundRevision > r.Revision || b.Operation != scope.Operation || b.Attempt != scope.Attempt || b.Stage != string(scope.Stage) || b.BinaryBytes != scope.B || !idPattern.MatchString(b.SourceLeaseID) || !idPattern.MatchString(b.StorageLeaseID) || (scope.Stage == kubesource.Forward) != (b.PortLeaseID != "") {
				return fmt.Errorf("source binding differs from exact original scope")
			}
			for _, id := range []string{b.SourceLeaseID, b.StorageLeaseID, b.PortLeaseID} {
				if id == "" {
					continue
				}
				if !idPattern.MatchString(id) || bindings[id] {
					return fmt.Errorf("aliased original source/parent binding")
				}
				bindings[id] = true
			}
		}
		if source.Binding == nil && source.BoundRevision != 0 {
			return fmt.Errorf("source binding revision has no acknowledged binding")
		}
		if v := source.Result; v != nil {
			b := source.Binding
			if b == nil || source.FinishedRevision <= source.BoundRevision || source.FinishedRevision > r.Revision || v.SourceLeaseID != b.SourceLeaseID || v.StorageLeaseID != b.StorageLeaseID || v.PromisedDisk != scope.DiskBytes() || v.SourceSettled && !v.SourceClosed || !slices.Contains([]string{"active", "ending", "needs_attention", "terminal"}, v.StorageState) || v.StorageRetained != (v.StorageState != "terminal") || source.Finished.Before(source.Intent) || source.Finished.After(r.Updated) {
				return fmt.Errorf("source result differs from original pair")
			}
			if scope.Stage == kubesource.Forward {
				if v.PortLeaseID != b.PortLeaseID || v.PortSettled && (!v.PortClosed || !v.SourceClosed) || !slices.Contains([]string{"active", "ending", "needs_attention", "terminal"}, v.PortState) || v.PortSettled != (v.PortState == "terminal") {
					return fmt.Errorf("forward source lacks independent port result")
				}
			} else if v.PortLeaseID != "" || v.PortClosed || v.PortSettled || v.PortState != "" {
				return fmt.Errorf("non-forward source carries port proof")
			}
		} else if !source.Finished.IsZero() || source.FinishedRevision != 0 {
			return fmt.Errorf("source finish lacks result")
		}
		unresolved = !sourceClosed(source)
	}
	return nil
}
