package kubejournal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/kubeprovision"
	"getarm.org/internal/kubesource"
)

func fixture(t *testing.T) (Record, time.Time) {
	t.Helper()
	plan, err := kubeprovision.NewPlan(kubeprovision.Review{Key: strings.Repeat("a", 64), Owner: "501", Reason: "closed journal fixture", Cluster: kubeprovision.Cluster{Context: "fixture", Server: "https://fixture.invalid", CAHash: strings.Repeat("b", 64), AuthSubject: "operator", AuthGroups: []string{"group"}}, Namespace: kubeprovision.Namespace{Name: "default", UID: "ns-uid"}, Node: kubeprovision.Node{Name: "worker", UID: "node-uid", OS: "linux", Arch: "arm64", Ready: true}, Image: "registry.invalid/base@sha256:" + strings.Repeat("c", 64), BinarySHA256: strings.Repeat("d", 64), BinaryBytes: 1024, CatalogSHA256: strings.Repeat("e", 64), Resources: kubeprovision.Resources{CPURequest: "100m", CPULimit: "500m", MemoryRequest: "128Mi", MemoryLimit: "512Mi", EphemeralRequest: "256Mi", EphemeralLimit: "1Gi"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	r, err := New("kubernetes_install", plan.Review.Key, "501", strings.Repeat("f", 32), plan, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	return r, now
}
func approved(t *testing.T, r *Record, name string, now time.Time) {
	t.Helper()
	generation := ""
	if r.Connection != nil {
		generation = r.Connection.Runtime.Generation
	}
	if err := r.ReviewStage(r.Revision, name, generation, now); err != nil {
		t.Fatal(err)
	}
	n, _ := r.stage(name)
	v := r.Stages[n].Review
	if err := r.Decide(r.Revision, name, v.Revision, v.SHA256, true, now); err != nil {
		t.Fatal(err)
	}
}
func bound(t *testing.T, r *Record, name string, now time.Time) (kubesource.Scope, kubesource.Binding) {
	t.Helper()
	scope := kubesource.Scope{Operation: r.Plan.Review.Key, Attempt: strings.Repeat("1", 32), Stage: profile(name), B: r.Plan.Review.BinaryBytes}
	if err := r.Intent(r.Revision, name, scope, now); err != nil {
		t.Fatal(err)
	}
	binding := kubesource.Binding{Operation: scope.Operation, Attempt: scope.Attempt, Stage: string(scope.Stage), BinaryBytes: scope.B, SourceLeaseID: strings.Repeat("2", 32), StorageLeaseID: strings.Repeat("3", 32)}
	if err := r.Bind(r.Revision, scope.Attempt, binding, now); err != nil {
		t.Fatal(err)
	}
	return scope, binding
}
func result(scope kubesource.Scope, b kubesource.Binding) kubesource.Result {
	return kubesource.Result{SourceLeaseID: b.SourceLeaseID, StorageLeaseID: b.StorageLeaseID, SourceClosed: true, SourceSettled: true, StorageState: "needs_attention", StorageRetained: true, PromisedDisk: scope.DiskBytes()}
}
func TestStageAttemptRequiresExactApprovalAndAcknowledgedBinding(t *testing.T) {
	r, now := fixture(t)
	if err := r.Attempt(r.Revision, "create", "", now); err == nil {
		t.Fatal("unapproved attempt")
	}
	approved(t, &r, "create", now)
	if err := r.Attempt(r.Revision, "create", "", now); err == nil {
		t.Fatal("unbound effect")
	}
	scope, b := bound(t, &r, "create", now)
	prior := Hash(r)
	if err := r.Attempt(r.Revision, "create", strings.Repeat("8", 32), now); err == nil || Hash(r) != prior {
		t.Fatal("changed generation mutated record")
	}
	if err := r.Attempt(r.Revision, "create", "", now); err != nil {
		t.Fatal(err)
	}
	if err := r.Observe(r.Revision, "create", StageKey(r.Key, "create"), EffectResult{Pod: &kubeprovision.Pod{UID: "pod-uid"}}, now); err == nil {
		t.Fatal("remote success settled original source")
	}
	if err := r.FinishSource(r.Revision, scope.Attempt, result(scope, b), now); err != nil {
		t.Fatal(err)
	}
	if err := r.Observe(r.Revision, "create", StageKey(r.Key, "create"), EffectResult{Pod: &kubeprovision.Pod{UID: "pod-uid"}}, now); err != nil {
		t.Fatal(err)
	}
	if !r.Sources[0].Result.StorageRetained || r.Phase != "pending" {
		t.Fatal("retained source debt erased")
	}
	if err := r.ReviewStage(r.Revision, "ship", "", now); err != nil {
		t.Fatal(err)
	}
	if err := r.ReviewStage(r.Revision, "create", "", now); err == nil {
		t.Fatal("confirmed stage replayed")
	}
}
func TestStageLostAckAndCancellationPreservePriorAuthorization(t *testing.T) {
	r, now := fixture(t)
	approved(t, &r, "create", now)
	_, _ = bound(t, &r, "create", now)
	if err := r.Attempt(r.Revision, "create", "", now); err != nil {
		t.Fatal(err)
	}
	if err := r.Observe(r.Revision, "create", StageKey(r.Key, "create"), EffectResult{Unknown: true}, now); err != nil {
		t.Fatal(err)
	}
	prior := r.Stages[0]
	if err := r.ReviewStage(r.Revision, "create", "", now); err == nil {
		t.Fatal("unknown stage replayed")
	}
	if err := r.Decide(r.Revision, "create", prior.ApprovedRevision, prior.ApprovedSHA256, false, now); err == nil {
		t.Fatal("later decline replaced prior approval")
	}
	if err := r.Cancel(r.Revision, now); err != nil {
		t.Fatal(err)
	}
	if !same(prior, r.Stages[0]) || !r.SourceUnresolved() {
		t.Fatal("cancellation erased prior authorization or debt")
	}
	if err := r.Observe(r.Revision, "create", strings.Repeat("9", 64), EffectResult{Unknown: true}, now); err == nil {
		t.Fatal("foreign lookup key accepted")
	}
}
func TestStageStrictCodecRejectsAliasesDuplicatesAndMissingFields(t *testing.T) {
	r, _ := fixture(t)
	raw, _ := json.Marshal(r)
	for _, mode := range []string{"valid", "duplicate", "alias", "unknown", "missing", "null-stages", "null-review", "tail", "huge"} {
		t.Run(mode, func(t *testing.T) {
			input := bytes.Clone(raw)
			switch mode {
			case "duplicate":
				input = bytes.Replace(input, []byte(`"revision":1`), []byte(`"revision":1,"revision":1`), 1)
			case "alias":
				input = bytes.Replace(input, []byte(`"revision":1`), []byte(`"Revision":1`), 1)
			case "unknown":
				input = bytes.Replace(input, []byte(`"revision":1`), []byte(`"revision":1,"ambient_authority":true`), 1)
			case "missing":
				input = bytes.Replace(input, []byte(`"revision":1,`), nil, 1)
			case "null-stages":
				var bad = r
				bad.Stages = nil
				input, _ = json.Marshal(bad)
			case "null-review":
				input = bytes.Replace(input, []byte(`"approved":false`), []byte(`"review":null,"approved":false`), 1)
			case "tail":
				input = append(input, []byte(` {}`)...)
			case "huge":
				input = append(input, bytes.Repeat([]byte(" "), MaxRecordBytes)...)
			}
			var got Record
			err := Decode(input, &got)
			if err == nil {
				err = got.Validate()
			}
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
		})
	}
}
func TestStageReviewHashIncludesGenerationAndDeclineIsFinal(t *testing.T) {
	r, now := fixture(t)
	if err := r.ReviewStage(r.Revision, "create", strings.Repeat("1", 32), now); err != nil {
		t.Fatal(err)
	}
	review := *r.Stages[0].Review
	changed := review
	changed.ExpectedGeneration = strings.Repeat("2", 32)
	if reviewHash(changed) == review.SHA256 {
		t.Fatal("generation absent from reviewed digest")
	}
	before := Hash(r)
	if err := r.Decide(r.Revision, "create", review.Revision, review.SHA256, true, review.Expires); err == nil || Hash(r) != before {
		t.Fatal("expired approval mutated record")
	}
	if err := r.Decide(r.Revision, "create", review.Revision, review.SHA256, false, now); err != nil {
		t.Fatal(err)
	}
	if err := r.ReviewStage(r.Revision, "create", "", now); err == nil {
		t.Fatal("declined stage replaced")
	}
	if r.Stages[0].Approved {
		t.Fatal("decline authorized effect")
	}
}

func TestStageProveRequiresIndependentForwardPortRetirement(t *testing.T) {
	for _, mode := range []string{"unknown-close", "unsettled-port", "positive"} {
		t.Run(mode, func(t *testing.T) {
			install, now := fixture(t)
			draft := &ConnectionDraft{SourceIdentity: Identity{Host: install.SourceHost, Fingerprint: strings.Repeat("b", 64)}, Runtime: kubeprovision.RuntimeProof{PodUID: "pod-uid", Host: strings.Repeat("c", 32), Generation: strings.Repeat("d", 32)}, SSHHostKey: "fixed fixture SSH pin", DestinationUID: "65532"}
			r, err := New("kubernetes_connect", strings.Repeat("9", 64), install.Owner, install.SourceHost, install.Plan, draft, now)
			if err != nil {
				t.Fatal(err)
			}
			if err = r.ReviewStage(r.Revision, "local-listener-prepare", "", now); err == nil {
				t.Fatal("connection review omitted the exact known backend generation")
			}
			for n, name := range stageNames[3:10] {
				approved(t, &r, name, now)
				scope := kubesource.Scope{Operation: r.Plan.Review.Key, Attempt: fmt.Sprintf("%032x", n+1), Stage: profile(name), B: r.Plan.Review.BinaryBytes}
				if err = r.Intent(r.Revision, name, scope, now); err != nil {
					t.Fatal(name, err)
				}
				b := kubesource.Binding{Operation: scope.Operation, Attempt: scope.Attempt, Stage: string(scope.Stage), BinaryBytes: scope.B, SourceLeaseID: fmt.Sprintf("%032x", 3*n+100), StorageLeaseID: fmt.Sprintf("%032x", 3*n+101)}
				if name == "prove" {
					b.PortLeaseID = fmt.Sprintf("%032x", 3*n+102)
				}
				if err = r.Bind(r.Revision, scope.Attempt, b, now); err != nil {
					t.Fatal(name, err)
				}
				if err = r.Attempt(r.Revision, name, draft.Runtime.Generation, now); err != nil {
					t.Fatal(name, err)
				}
				outcome := result(scope, b)
				effect := EffectResult{}
				switch name {
				case "local-listener-prepare", "pod-listener-prepare":
					effect.Prepared = &PreparedSetup{Key: StageKey(r.Key, name), Token: "original fixture token", Identity: Identity{Host: draft.Runtime.Host}, Expires: now.Add(time.Minute)}
				case "local-listener-apply", "pod-listener-apply":
					effect.Identity = &Identity{Host: draft.Runtime.Host}
				case "local-pair", "pod-pair":
					effect.Peer = &Peer{Pair: strings.Repeat("e", 64), Revision: 1, State: "pending"}
				case "prove":
					outcome.PortLeaseID = b.PortLeaseID
					outcome.PortClosed = mode != "unknown-close"
					outcome.PortSettled = mode == "positive"
					outcome.PortState = "needs_attention"
					if outcome.PortSettled {
						outcome.PortState = "terminal"
					}
					effect.Proven = true
					effect.OwnerClosed = true
				}
				if err = r.FinishSource(r.Revision, scope.Attempt, outcome, now); err != nil {
					t.Fatal(name, err)
				}
				err = r.Observe(r.Revision, name, StageKey(r.Key, name), effect, now)
				if name == "prove" && mode != "positive" {
					if err == nil || !r.SourceUnresolved() {
						t.Fatal("proof bypassed original port retirement", mode, err)
					}
					if err = r.ReviewStage(r.Revision, "local-activate", draft.Runtime.Generation, now); err == nil {
						t.Fatal("activation advanced past unretired forward")
					}
					return
				}
				if err != nil {
					t.Fatal(name, err)
				}
			}
			if err = r.ReviewStage(r.Revision, "local-activate", draft.Runtime.Generation, now); err != nil {
				t.Fatal("positive forward retirement failed to allow next exact review", err)
			}
		})
	}
}

func TestStageConnectorCannotSubstituteOriginalDraftPins(t *testing.T) {
	install, now := fixture(t)
	draft := &ConnectionDraft{SourceIdentity: Identity{Host: install.SourceHost, Fingerprint: strings.Repeat("b", 64)}, Runtime: kubeprovision.RuntimeProof{PodUID: "pod-uid", Host: strings.Repeat("c", 32), Generation: strings.Repeat("d", 32)}, SSHHostKey: "fixed fixture SSH pin", DestinationUID: "65532"}
	record, err := New("kubernetes_connect", strings.Repeat("9", 64), install.Owner, install.SourceHost, install.Plan, draft, now)
	if err != nil {
		t.Fatal(err)
	}
	base := Connector{ConnectionKey: record.Key, InstallationKey: record.Plan.Review.Key, ScopeSHA256: strings.Repeat("a", 64), Phase: "bootstrap_only", PodUID: draft.Runtime.PodUID, BackendHost: draft.Runtime.Host, BackendGeneration: draft.Runtime.Generation, DestinationUID: draft.DestinationUID, MeshIdentity: Identity{Host: draft.Runtime.Host, SPKI: strings.Repeat("e", 64), Fingerprint: strings.Repeat("f", 64), Certificate: "typed fixture observation"}, SSHHostKey: draft.SSHHostKey, SourceIdentity: draft.SourceIdentity}
	for _, mode := range []string{"same", "pod", "host", "generation", "ssh", "uid", "source-pin", "grant"} {
		t.Run(mode, func(t *testing.T) {
			c := base
			switch mode {
			case "pod":
				c.PodUID = "other-pod"
			case "host":
				c.BackendHost = strings.Repeat("2", 32)
				c.MeshIdentity.Host = c.BackendHost
			case "generation":
				c.BackendGeneration = strings.Repeat("2", 32)
			case "ssh":
				c.SSHHostKey = "substitute SSH pin"
			case "uid":
				c.DestinationUID = "65533"
			case "source-pin":
				c.SourceIdentity.Fingerprint = strings.Repeat("2", 64)
			case "grant":
				c.Forward.Permissions = []string{"observe-host"}
			}
			r := record
			r.Connector = &c
			if err := r.Validate(); (err == nil) != (mode == "same") {
				t.Fatal("connector pin scope", mode, err)
			}
		})
	}
}

func TestStageCancellationAfterBindingRefusesAttemptButRecordsCleanup(t *testing.T) {
	r, now := fixture(t)
	approved(t, &r, "create", now)
	scope, b := bound(t, &r, "create", now)
	if err := r.Cancel(r.Revision, now); err != nil {
		t.Fatal(err)
	}
	before := Hash(r)
	if err := r.Attempt(r.Revision, "create", "", now); err == nil || Hash(r) != before {
		t.Fatal("cancelled bound stage acquired effect intent")
	}
	if err := r.FinishSource(r.Revision, scope.Attempt, result(scope, b), now); err != nil {
		t.Fatal("cancellation blocked original cleanup observation", err)
	}
	if r.Phase != "cancelled" || !r.Stages[0].Approved || !r.Stages[0].Attempted.IsZero() {
		t.Fatal("cleanup erased stop/approval or invented an effect")
	}
}
