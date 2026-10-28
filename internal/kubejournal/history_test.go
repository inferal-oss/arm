package kubejournal

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/kubeprovision"
	"getarm.org/internal/kubesource"
)

func historyFixture(t *testing.T) History {
	t.Helper()
	old, now := fixture(t)
	r, err := NewHistory(old.Kind, old.Key, old.Owner, old.SourceHost, old.Plan, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func historyTime(r History) time.Time { return r.Updated.Add(time.Millisecond) }
func historyApproved(t *testing.T, r *History, stage, role string, installation ...History) StageReview {
	t.Helper()
	generation := ""
	if r.Connection != nil {
		generation = r.Connection.Runtime.Generation
	}
	if err := r.ReviewStage(r.Revision, stage, role, generation, historyTime(*r), installation...); err != nil {
		t.Fatal(err)
	}
	n, _ := r.stage(stage)
	review := r.Stages[n].Reviews[len(r.Stages[n].Reviews)-1]
	if err := r.Decide(r.Revision, stage, review.Revision, review.SHA256, true, historyTime(*r)); err != nil {
		t.Fatal(err)
	}
	return r.Stages[n].Reviews[len(r.Stages[n].Reviews)-1]
}
func historyBound(t *testing.T, r *History, review StageReview, step uint8, installation ...History) (kubesource.Scope, kubesource.Binding) {
	t.Helper()
	scope := kubesource.Scope{Operation: r.Plan.Review.Key, Attempt: fmt.Sprintf("%032x", r.Revision+1), Stage: review.Steps[step].Profile[0], B: r.Plan.Review.BinaryBytes}
	if err := r.Intent(r.Revision, review.Stage, review.SHA256, step, scope, historyTime(*r), installation...); err != nil {
		t.Fatal(err)
	}
	binding := kubesource.Binding{Operation: scope.Operation, Attempt: scope.Attempt, Stage: string(scope.Stage), BinaryBytes: scope.B, SourceLeaseID: fmt.Sprintf("%032x", r.Revision+1000), StorageLeaseID: fmt.Sprintf("%032x", r.Revision+2000)}
	if scope.Stage == kubesource.Forward {
		binding.PortLeaseID = fmt.Sprintf("%032x", r.Revision+3000)
	}
	if err := r.Bind(r.Revision, scope.Attempt, binding, historyTime(*r)); err != nil {
		t.Fatal(err)
	}
	return scope, binding
}
func historyFinish(t *testing.T, r *History, scope kubesource.Scope, binding kubesource.Binding) {
	t.Helper()
	v := result(scope, binding)
	if scope.Stage == kubesource.Forward {
		v.PortLeaseID, v.PortClosed, v.PortSettled, v.PortState = binding.PortLeaseID, true, true, "terminal"
	}
	if err := r.FinishSource(r.Revision, scope.Attempt, v, historyTime(*r)); err != nil {
		t.Fatal(err)
	}
}
func historyPrerequisites(t *testing.T, r *History) {
	t.Helper()
	for _, name := range []string{"create", "ship"} {
		review := historyApproved(t, r, name, EffectRole)
		scope, binding := historyBound(t, r, review, 0)
		if err := r.Attempt(r.Revision, name, review.SHA256, "", historyTime(*r)); err != nil {
			t.Fatal(err)
		}
		historyFinish(t, r, scope, binding)
		if err := r.Observe(r.Revision, name, review.SHA256, EffectResult{Pod: &kubeprovision.Pod{UID: "original-pod"}}, historyTime(*r)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHistoryStartRequiresSerialFreshRuntimeScope(t *testing.T) {
	r := historyFixture(t)
	historyPrerequisites(t, &r)
	review := historyApproved(t, &r, "start", EffectRole)
	if len(review.Steps) != 2 || review.Steps[0].Profile != "t" || review.Steps[1].Profile != "i" || review.Steps[1].Role != VerificationRole || review.Steps[0].StorageBytes != 262144 || review.Steps[1].StorageBytes != 327680 {
		t.Fatal("start review does not disclose distinct t/i promises", review.Steps)
	}
	scope, binding := historyBound(t, &r, review, 0)
	if err := r.Attempt(r.Revision, "start", review.SHA256, "", historyTime(r)); err != nil {
		t.Fatal(err)
	}
	i := kubesource.Scope{Operation: r.Plan.Review.Key, Attempt: strings.Repeat("7", 32), Stage: kubesource.Inspect, B: r.Plan.Review.BinaryBytes}
	before := Hash(r)
	if err := r.Intent(r.Revision, "start", review.SHA256, 1, i, historyTime(r)); err == nil || Hash(r) != before {
		t.Fatal("i overlapped original t")
	}
	// Positive native closure alone must not promote uncertain SQL settlement.
	partial, err := r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	v := result(scope, binding)
	v.SourceSettled = false
	if err = partial.FinishSource(partial.Revision, scope.Attempt, v, historyTime(partial)); err != nil {
		t.Fatal(err)
	}
	if err = partial.Intent(partial.Revision, "start", review.SHA256, 1, i, historyTime(partial)); err == nil {
		t.Fatal("native closure concealed unresolved source settlement")
	}
	historyFinish(t, &r, scope, binding)
	runtime := &kubeprovision.RuntimeProof{PodUID: "original-pod", Host: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32)}
	for _, outcome := range []EffectResult{{Pod: &kubeprovision.Pod{UID: "original-pod"}}, {Runtime: runtime}} {
		before = Hash(r)
		if err := r.Observe(r.Revision, "start", review.SHA256, outcome, historyTime(r)); err == nil || Hash(r) != before {
			t.Fatal("marker or unexecuted runtime proof confirmed start")
		}
	}
	i, ib := historyBound(t, &r, review, 1)
	if err := r.Attempt(r.Revision, "start", review.SHA256, "", historyTime(r)); err == nil {
		t.Fatal("second source granted a second effect attempt")
	}
	historyFinish(t, &r, i, ib)
	if err := r.Observe(r.Revision, "start", review.SHA256, EffectResult{Runtime: runtime}, historyTime(r)); err != nil || r.Phase != "completed" {
		t.Fatal("serial fresh runtime could not complete installation", r.Phase, err)
	}
	for _, source := range r.Sources {
		if !source.Result.StorageRetained || source.Result.StorageState != "needs_attention" {
			t.Fatal("source settlement erased scratch debt")
		}
	}
}

func TestHistoryRecoveryRetainsUnknownAndCannotReplayEffect(t *testing.T) {
	r := historyFixture(t)
	create := historyApproved(t, &r, "create", EffectRole)
	s, b := historyBound(t, &r, create, 0)
	if err := r.Attempt(r.Revision, "create", create.SHA256, "", historyTime(r)); err != nil {
		t.Fatal(err)
	}
	historyFinish(t, &r, s, b)
	if err := r.Observe(r.Revision, "create", create.SHA256, EffectResult{Pod: &kubeprovision.Pod{UID: "original-pod"}}, historyTime(r)); err != nil {
		t.Fatal(err)
	}
	ship := historyApproved(t, &r, "ship", EffectRole)
	s, b = historyBound(t, &r, ship, 0)
	if err := r.Attempt(r.Revision, "ship", ship.SHA256, "", historyTime(r)); err != nil {
		t.Fatal(err)
	}
	historyFinish(t, &r, s, b)
	if err := r.Observe(r.Revision, "ship", ship.SHA256, EffectResult{Unknown: true}, historyTime(r)); err != nil {
		t.Fatal(err)
	}
	originalReview, originalEffect, originalUnknown := Hash(r.Stages[1].Reviews[0]), Hash(r.Stages[1].Effect), Hash(r.Stages[1].Observations[0])
	if err := r.Cancel(r.Revision, historyTime(r)); err != nil {
		t.Fatal(err)
	}
	recovery := historyApproved(t, &r, "ship", VerificationRole)
	if len(recovery.Steps) != 1 || recovery.Steps[0].Profile != "i" || recovery.Steps[0].Role != VerificationRole {
		t.Fatal("read-only recovery carries effect or payload authority")
	}
	s, b = historyBound(t, &r, recovery, 0)
	before := Hash(r)
	if err := r.Attempt(r.Revision, "ship", recovery.SHA256, "", historyTime(r)); err == nil || Hash(r) != before {
		t.Fatal("verification approval dispatched an effect")
	}
	historyFinish(t, &r, s, b)
	if err := r.Observe(r.Revision, "ship", recovery.SHA256, EffectResult{Pod: &kubeprovision.Pod{UID: "original-pod"}}, historyTime(r)); err != nil {
		t.Fatal(err)
	}
	if r.Phase != "cancelled" || Hash(r.Stages[1].Reviews[0]) != originalReview || Hash(r.Stages[1].Effect) != originalEffect || Hash(r.Stages[1].Observations[0]) != originalUnknown || len(r.Stages[1].Observations) != 2 {
		t.Fatal("fresh observation erased original effects/uncertainty/stop decision")
	}
	if err := r.ReviewStage(r.Revision, "start", EffectRole, "", historyTime(r)); err == nil {
		t.Fatal("later verification resurrected a stopped workflow")
	}
}

func TestHistoryClosedVersionRefusesPriorReaderAndEncodedOverflow(t *testing.T) {
	r := historyFixture(t)
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var old Record
	if err = json.Unmarshal(raw, &old); err == nil {
		t.Fatal("V1 reader accepted protected V2 data")
	}
	var restored History
	if err = json.Unmarshal(raw, &restored); err != nil || Hash(restored) != Hash(r) {
		t.Fatal("closed V2 roundtrip differs", err)
	}
	// Saved approval remains evidence only: no live worker is constructed by
	// any decoder. A raw unknown member or alias cannot widen this closed DTO.
	for _, wire := range []string{strings.Replace(string(raw), `"schema":`, `"Schema":`, 1), strings.Replace(string(raw), `"sources":[]`, `"sources":null`, 1), strings.TrimSuffix(string(raw), "}") + `,"future_effect":true}`} {
		if err := json.Unmarshal([]byte(wire), &restored); err == nil {
			t.Fatal("closed V2 reader accepted alias/null/unknown field")
		}
	}
	review := historyApproved(t, &r, "create", EffectRole)
	s, b := historyBound(t, &r, review, 0)
	if err := r.Attempt(r.Revision, "create", review.SHA256, "", historyTime(r)); err != nil {
		t.Fatal(err)
	}
	historyFinish(t, &r, s, b)
	before := Hash(r)
	if err := r.Observe(r.Revision, "create", review.SHA256, EffectResult{Pod: &kubeprovision.Pod{UID: "original-pod", Name: strings.Repeat("x", 65537)}}, historyTime(r)); err == nil || Hash(r) != before {
		t.Fatal("oversized final encoded observation replaced original bytes/revision")
	}
	if err := r.Observe(r.Revision, "create", review.SHA256, EffectResult{Pod: &kubeprovision.Pod{UID: "original-pod"}}, historyTime(r)); err != nil {
		t.Fatal("ordinary original observation refused after oversized input", err)
	}
}

func TestHistoryAcknowledgementsAndPrefixCannotBeCollapsed(t *testing.T) {
	r := historyFixture(t)
	review := historyApproved(t, &r, "create", EffectRole)
	scope := kubesource.Scope{Operation: r.Plan.Review.Key, Attempt: strings.Repeat("3", 32), Stage: kubesource.Bootstrap, B: r.Plan.Review.BinaryBytes}
	if err := r.Intent(r.Revision, "create", review.SHA256, 0, scope, historyTime(r)); err != nil {
		t.Fatal(err)
	}
	if err := r.Attempt(r.Revision, "create", review.SHA256, "", historyTime(r)); err == nil {
		t.Fatal("unacknowledged binding allowed effect")
	}
	before := Hash(r)
	for _, mutate := range []func(*History){
		func(v *History) { v.Stages[0].Reviews[0].Steps[0].StorageBytes = 0 },
		func(v *History) {
			v.Stages[0].Reviews[0].Decision = "pending"
			v.Stages[0].Reviews[0].Decided = time.Time{}
			v.Stages[0].Reviews[0].DecisionRevision = 0
		},
		func(v *History) { v.Sources = []HistorySource{} },
	} {
		next, err := r.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		next.Revision++
		next.Updated = historyTime(r)
		mutate(&next)
		if err := r.ValidateSuccessor(next); err == nil || Hash(r) != before {
			t.Fatal("generic successor erased approval/custody or changed its resource promise")
		}
	}
}

func TestHistoryExpiredReviewsRemainBoundedAndImmutable(t *testing.T) {
	r := historyFixture(t)
	for range MaxStageReviews {
		if err := r.ReviewStage(r.Revision, "create", EffectRole, "", r.Updated.Add(2*time.Minute+time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	before := Hash(r)
	if err := r.ReviewStage(r.Revision, "create", EffectRole, "", r.Updated.Add(2*time.Minute+time.Millisecond)); err == nil || Hash(r) != before {
		t.Fatal("history exhaustion pruned original reviews or allowed unbounded growth")
	}
	first := r.Stages[0].Reviews[0]
	if err := r.Decide(r.Revision, "create", first.Revision, first.SHA256, true, historyTime(r)); err == nil {
		t.Fatal("old exact review became reusable")
	}
}

func TestHistoryForwardRequiresIndependentOriginalPortRetirement(t *testing.T) {
	for _, mode := range []string{"unknown-close", "unsettled-port", "positive"} {
		t.Run(mode, func(t *testing.T) {
			install := historyCompleted(t)
			draft := &ConnectionDraft{SourceIdentity: Identity{Host: install.SourceHost, Fingerprint: strings.Repeat("b", 64)}, Runtime: kubeprovision.RuntimeProof{PodUID: "original-pod", Host: strings.Repeat("c", 32), Generation: strings.Repeat("d", 32)}, SSHHostKey: "fixed fixture SSH pin", DestinationUID: "65532"}
			r, err := NewConnectionHistory(strings.Repeat("9", 64), install, *draft, historyTime(install))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range stageNames[3:10] {
				review := historyApproved(t, &r, name, EffectRole, install)
				scope, binding := historyBound(t, &r, review, 0, install)
				if err := r.Attempt(r.Revision, name, review.SHA256, draft.Runtime.Generation, historyTime(r), install); err != nil {
					t.Fatal(err)
				}
				v := result(scope, binding)
				effect := EffectResult{}
				switch name {
				case "local-listener-prepare", "pod-listener-prepare":
					effect.Prepared = &PreparedSetup{Key: historyStageKey(r.Key, name), Token: "original fixture token", Identity: Identity{Host: draft.Runtime.Host}, Expires: historyTime(r).Add(time.Minute)}
				case "local-listener-apply", "pod-listener-apply":
					effect.Identity = &Identity{Host: draft.Runtime.Host}
				case "local-pair", "pod-pair":
					effect.Peer = &Peer{Pair: strings.Repeat("e", 64), Revision: 1, State: "pending"}
				case "prove":
					if review.Steps[0].PortCount != 1 || review.Steps[0].PortTTL != kubesource.ParentTTL {
						t.Fatal("forward review omitted independent held port")
					}
					v.PortLeaseID, v.PortClosed, v.PortSettled, v.PortState = binding.PortLeaseID, mode != "unknown-close", mode == "positive", "needs_attention"
					if v.PortSettled {
						v.PortState = "terminal"
					}
					effect.Proven, effect.OwnerClosed = true, true
				}
				if err := r.FinishSource(r.Revision, scope.Attempt, v, historyTime(r)); err != nil {
					t.Fatal(err)
				}
				err = r.Observe(r.Revision, name, review.SHA256, effect, historyTime(r), install)
				if name == "prove" && mode != "positive" {
					if err == nil || !r.SourceUnresolved() {
						t.Fatal("proof concealed unretired original port", mode, err)
					}
					if err := r.ReviewStage(r.Revision, "local-activate", EffectRole, draft.Runtime.Generation, historyTime(r), install); err == nil {
						t.Fatal("activation advanced past original port uncertainty")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := r.ReviewStage(r.Revision, "local-activate", EffectRole, draft.Runtime.Generation, historyTime(r), install); err != nil {
				t.Fatal("positive original port retirement refused next review", err)
			}
		})
	}
}

func historyCompleted(t *testing.T) History {
	t.Helper()
	r := historyFixture(t)
	historyPrerequisites(t, &r)
	review := historyApproved(t, &r, "start", EffectRole)
	for step := range review.Steps {
		scope, binding := historyBound(t, &r, review, uint8(step))
		if step == 0 {
			if err := r.Attempt(r.Revision, "start", review.SHA256, "", historyTime(r)); err != nil {
				t.Fatal(err)
			}
		}
		historyFinish(t, &r, scope, binding)
	}
	proof := &kubeprovision.RuntimeProof{PodUID: "original-pod", Host: strings.Repeat("c", 32), Generation: strings.Repeat("d", 32)}
	if err := r.Observe(r.Revision, "start", review.SHA256, EffectResult{Runtime: proof}, historyTime(r)); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestHistoryCreateUsesClosedOneRequestAndLostAckLookup(t *testing.T) {
	r := historyFixture(t)
	review := historyApproved(t, &r, "create", EffectRole)
	step := review.Steps[0]
	if len(review.Steps) != 1 || step.Profile != "e" || !same(step.Purposes, []string{"mesh_bootstrap"}) || !same(step.Actions, []string{"create_original_pod"}) || step.RequestCount != 1 || step.StorageBytes != 131072 {
		t.Fatal("create review widened or mislabeled the one-request helper", step)
	}
	s, b := historyBound(t, &r, review, 0)
	if err := r.Attempt(r.Revision, "create", review.SHA256, "", historyTime(r)); err != nil {
		t.Fatal(err)
	}
	historyFinish(t, &r, s, b)
	if err := r.Observe(r.Revision, "create", review.SHA256, EffectResult{Unknown: true}, historyTime(r)); err != nil {
		t.Fatal(err)
	}
	old := Hash(r.Stages[0].Effect)
	lookup := historyApproved(t, &r, "create", VerificationRole)
	if len(lookup.Steps) != 1 || lookup.Steps[0].Profile != "e" || !same(lookup.Steps[0].Actions, []string{"lookup_original_pod"}) || lookup.Steps[0].RequestCount != 1 {
		t.Fatal("lost ACK lookup grants another create", lookup)
	}
	s, b = historyBound(t, &r, lookup, 0)
	before := Hash(r)
	if err := r.Attempt(r.Revision, "create", lookup.SHA256, "", historyTime(r)); err == nil || Hash(r) != before {
		t.Fatal("lookup replayed create")
	}
	historyFinish(t, &r, s, b)
	if err := r.Observe(r.Revision, "create", lookup.SHA256, EffectResult{Pod: &kubeprovision.Pod{UID: "original-pod"}}, historyTime(r)); err != nil {
		t.Fatal(err)
	}
	if Hash(r.Stages[0].Effect) != old || len(r.Stages[0].Observations) != 2 || !r.Stages[0].Observations[0].Result.Unknown {
		t.Fatal("lookup erased original create/unknown")
	}
}

func historyConnectionThroughPairs(t *testing.T, install History) History {
	t.Helper()
	n, _ := install.stage("start")
	runtime := *install.Stages[n].Observations[len(install.Stages[n].Observations)-1].Result.Runtime
	draft := ConnectionDraft{SourceIdentity: Identity{Host: install.SourceHost, Fingerprint: strings.Repeat("b", 64)}, Runtime: runtime, SSHHostKey: "fixed fixture SSH pin", DestinationUID: "65532"}
	r, err := NewConnectionHistory(strings.Repeat("9", 64), install, draft, historyTime(install))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range stageNames[3:9] {
		review := historyApproved(t, &r, name, EffectRole, install)
		s, b := historyBound(t, &r, review, 0, install)
		if err := r.Attempt(r.Revision, name, review.SHA256, draft.Runtime.Generation, historyTime(r), install); err != nil {
			t.Fatal(err)
		}
		historyFinish(t, &r, s, b)
		effect := EffectResult{}
		switch name {
		case "local-listener-prepare", "pod-listener-prepare":
			effect.Prepared = &PreparedSetup{Key: historyStageKey(r.Key, name), Token: "original fixture token", Identity: Identity{Host: draft.Runtime.Host}, Expires: historyTime(r).Add(time.Minute)}
		case "local-listener-apply", "pod-listener-apply":
			effect.Identity = &Identity{Host: draft.Runtime.Host}
		case "local-pair", "pod-pair":
			effect.Peer = &Peer{Pair: strings.Repeat("e", 64), Revision: 1, State: "pending"}
		}
		if err := r.Observe(r.Revision, name, review.SHA256, effect, historyTime(r), install); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestHistoryCurrentPrefixInvalidatesDownstreamWithoutErasingDecision(t *testing.T) {
	for _, mode := range []string{"unknown", "changed-positive"} {
		t.Run(mode, func(t *testing.T) {
			install := historyCompleted(t)
			r := historyConnectionThroughPairs(t, install)
			approved := historyApproved(t, &r, "prove", EffectRole, install)
			approvedBytes := Hash(approved)
			prior := r.Stages[5].Observations[0]
			refresh := historyApproved(t, &r, "pod-pair", VerificationRole, install)
			s, b := historyBound(t, &r, refresh, 0, install)
			historyFinish(t, &r, s, b)
			effect := EffectResult{Unknown: true}
			if mode == "changed-positive" {
				effect = EffectResult{Peer: &Peer{Pair: strings.Repeat("7", 64), Revision: 2, State: "pending"}}
			}
			if err := r.Observe(r.Revision, "pod-pair", refresh.SHA256, effect, historyTime(r), install); err != nil {
				t.Fatal(err)
			}
			if err := r.Validate(); err != nil {
				t.Fatal("old decisions no longer inspectable", err)
			}
			n, _ := r.stage("prove")
			if Hash(r.Stages[n].Reviews[0]) != approvedBytes || !same(r.Stages[5].Observations[0], prior) {
				t.Fatal("new observation rewrote historical proof/decision")
			}
			if err := r.CheckReviewEvidence("prove", approved.SHA256); err == nil {
				t.Fatal("approved downstream review silently used old positive")
			}
			before := Hash(r)
			if err := r.ReviewStage(r.Revision, "prove", EffectRole, install.Stages[2].Observations[0].Result.Runtime.Generation, historyTime(r), install); err == nil || Hash(r) != before {
				t.Fatal("new downstream review accepted unknown/changed current identity")
			}
			scope := kubesource.Scope{Operation: r.Plan.Review.Key, Attempt: strings.Repeat("8", 32), Stage: kubesource.Forward, B: r.Plan.Review.BinaryBytes}
			if err := r.Intent(r.Revision, "prove", approved.SHA256, 0, scope, historyTime(r), install); err == nil || Hash(r) != before {
				t.Fatal("old approved digest admitted a new carrier after current evidence changed")
			}
			if mode == "unknown" {
				known := historyApproved(t, &r, "pod-pair", VerificationRole, install)
				s, b := historyBound(t, &r, known, 0, install)
				historyFinish(t, &r, s, b)
				if err := r.Observe(r.Revision, "pod-pair", known.SHA256, prior.Result, historyTime(r), install); err != nil {
					t.Fatal(err)
				}
				if err := r.ReviewStage(r.Revision, "prove", EffectRole, install.Stages[2].Observations[0].Result.Runtime.Generation, historyTime(r), install); err != nil {
					t.Fatal("fresh identical evidence could not get a separate review", err)
				}
				latest := r.Stages[6].Reviews[len(r.Stages[6].Reviews)-1]
				if latest.SHA256 == approved.SHA256 || latest.ProofInputs[5].Count != 3 || Hash(r.Stages[6].Reviews[0]) != approvedBytes || r.Validate() != nil {
					t.Fatal("new review did not preserve old decision and capture current exact prefix")
				}
			}
		})
	}
}

func TestHistoryInstallationRefreshFencesOldRuntimeAndGeneration(t *testing.T) {
	for _, mode := range []string{"unknown", "generation", "host", "same"} {
		t.Run(mode, func(t *testing.T) {
			install := historyCompleted(t)
			connection := historyConnectionThroughPairs(t, install)
			downstream := historyApproved(t, &connection, "prove", EffectRole, install)
			decision := Hash(downstream)
			oldDraft := *connection.Connection
			review := historyApproved(t, &install, "start", VerificationRole)
			s, b := historyBound(t, &install, review, 0)
			historyFinish(t, &install, s, b)
			result := EffectResult{Unknown: true}
			if mode != "unknown" {
				current := oldDraft.Runtime
				if mode == "generation" {
					current.Generation = strings.Repeat("3", 32)
				} else if mode == "host" {
					current.Host = strings.Repeat("4", 32)
				}
				result = EffectResult{Runtime: &current}
			}
			if err := install.Observe(install.Revision, "start", review.SHA256, result, historyTime(install)); err != nil {
				t.Fatal(err)
			}
			if err := connection.Validate(); err != nil || Hash(connection.Stages[6].Reviews[0]) != decision {
				t.Fatal("current refresh erased old connection decision", err)
			}
			if err := connection.CheckReviewInstallation("prove", downstream.SHA256, install); err == nil {
				t.Fatal("old review silently used an appended installation observation")
			}
			if mode == "same" {
				if err := connection.CheckInstallation(install); err != nil {
					t.Fatal("identical current identity was permanently fenced", err)
				}
				if err := connection.ReviewStage(connection.Revision, "prove", EffectRole, oldDraft.Runtime.Generation, historyTime(connection), install); err != nil {
					t.Fatal("identical refresh could not get a new explicit review", err)
				}
				latest := connection.Stages[6].Reviews[len(connection.Stages[6].Reviews)-1]
				if latest.Installation.Observation.Count != 2 || latest.SHA256 == downstream.SHA256 || Hash(connection.Stages[6].Reviews[0]) != decision {
					t.Fatal("new review did not preserve old decision/current installation prefix")
				}
				return
			}
			if err := connection.CheckInstallation(install); err == nil {
				t.Fatal("old connection used changed/unknown runtime")
			}
			before := Hash(connection)
			if err := connection.ReviewStage(connection.Revision, "prove", EffectRole, oldDraft.Runtime.Generation, historyTime(connection), install); err == nil || Hash(connection) != before {
				t.Fatal("downstream review ignored changed current installation")
			}
			if _, err := NewConnectionHistory(strings.Repeat("6", 64), install, oldDraft, historyTime(install)); err == nil {
				t.Fatal("new connection accepted stale runtime draft")
			}
			if mode != "unknown" {
				fresh := oldDraft
				fresh.Runtime = *result.Runtime
				next, err := NewConnectionHistory(strings.Repeat("6", 64), install, fresh, historyTime(install))
				if err != nil || next.Installation.Observation.Count != 2 {
					t.Fatal("explicit new connection could not capture current changed runtime", err)
				}
			}
		})
	}
}
