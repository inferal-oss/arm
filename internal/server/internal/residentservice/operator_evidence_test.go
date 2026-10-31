package residentservice

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// All values are synthetic parser DATA. None enter Plan/Apply, a native issuer,
// OriginalDebit, inherited child or ManagerCharge. A successful data validator
// is not evidence of real measurement or approval.
func evidenceData(t *testing.T) (MeasuredProfile, EvidenceBundle) {
	t.Helper()
	profile := dataRecords(t)[0].Grant.Intent.Profile
	raw := []byte("synthetic parser data, never a measurement receipt")
	evidence := EvidenceBundle{Schema: 1, Build: profile.Build, Fixture: "resident-root-standing-v1", FixtureSourceSHA256: strings.Repeat("a", 64), Started: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Finished: time.Date(2000, 1, 1, 0, 0, 1, 0, time.UTC), ObservedResources: profile.Resources, ObservedBounds: profile.Bounds, Unknowns: []string{"synthetic DATA ONLY; measurement truth is not asserted"}, Artifacts: []MeasurementArtifact{{Name: "data", Kind: "fixture-transcript", SHA256: digest(raw), Size: int64(len(raw)), Raw: raw}}}
	for _, name := range requiredStandingWorkloads {
		evidence.Coverage = append(evidence.Coverage, MeasurementCoverage{Workload: name, Method: "synthetic parser data only", Artifacts: []string{"data"}})
	}
	for _, name := range requiredStandingCostScopes {
		evidence.CostScopes = append(evidence.CostScopes, MeasurementCostScope{Scope: name, Method: "synthetic parser data only", Artifacts: []string{"data"}})
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	profile.EvidenceSHA256 = digest(encoded)
	return profile, evidence
}
func TestResidentOperatorEvidenceCanonicalData(t *testing.T) {
	profile, evidence := evidenceData(t)
	if err := evidence.Validate(profile); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var parsed EvidenceBundle
	if err := decodeOperatorDocument(raw, MaxEvidenceBytes, &parsed); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unknown-field", "duplicate-field", "trailing-object", "noncanonical", "oversize"} {
		t.Run(name, func(t *testing.T) {
			mutated := append([]byte(nil), raw...)
			switch name {
			case "unknown-field":
				mutated = append([]byte(`{"unknown":1,`), raw[1:]...)
			case "duplicate-field":
				mutated = append([]byte(`{"schema":1,`), raw[1:]...)
			case "trailing-object":
				mutated = append(mutated, []byte(`{}`)...)
			case "noncanonical":
				mutated = append(mutated, '\n')
			case "oversize":
				mutated = make([]byte, MaxEvidenceBytes+1)
			}
			if err := decodeOperatorDocument(mutated, MaxEvidenceBytes, &EvidenceBundle{}); err == nil {
				t.Fatal("invalid document accepted")
			}
		})
	}
}
func TestResidentOperatorEvidenceRefusesIncompleteData(t *testing.T) {
	for _, name := range []string{"missing-cost-scope", "missing-workload", "artifact-hash", "missing-raw", "unreferenced-artifact", "claim-too-small", "no-unknowns", "source", "fixture", "runtime", "duplicate-artifact", "zero-bound", "nonfinite", "empty-interval"} {
		t.Run(name, func(t *testing.T) {
			profile, evidence := evidenceData(t)
			switch name {
			case "missing-cost-scope":
				evidence.CostScopes = evidence.CostScopes[:len(evidence.CostScopes)-1]
			case "missing-workload":
				evidence.Coverage = evidence.Coverage[:len(evidence.Coverage)-1]
			case "artifact-hash":
				evidence.Artifacts[0].SHA256 = strings.Repeat("c", 64)
			case "missing-raw":
				evidence.Artifacts[0].Raw = nil
			case "unreferenced-artifact":
				a := evidence.Artifacts[0]
				a.Name = "unused"
				evidence.Artifacts = append(evidence.Artifacts, a)
			case "claim-too-small":
				evidence.ObservedResources.Memory = profile.Resources.Memory + 1
			case "no-unknowns":
				evidence.Unknowns = nil
			case "source":
				evidence.FixtureSourceSHA256 = "unknown"
			case "fixture":
				evidence.Fixture = "control-only"
			case "runtime":
				evidence.Build.GoBuild = "another-runtime"
			case "duplicate-artifact":
				evidence.Artifacts = append(evidence.Artifacts, evidence.Artifacts[0])
			case "zero-bound":
				evidence.ObservedBounds.Descriptors = 0
			case "nonfinite":
				evidence.ObservedResources.CPU = math.Inf(1)
			case "empty-interval":
				evidence.Finished = evidence.Started
			}
			if err := evidence.Validate(profile); err == nil {
				t.Fatal("incomplete evidence data accepted")
			}
		})
	}
}
func TestResidentOperatorCopiedZeroAndCancelledPendingRefuse(t *testing.T) {
	if _, err := (&OperatorPending{}).PlanOriginal(t.Context()); err == nil {
		t.Fatal("zero holder accepted")
	}
	pending, err := NewOperatorPending("/synthetic/home", "/synthetic/profile", "/synthetic/evidence", "synthetic-negative", "negative pre-effect refusal")
	if err != nil {
		t.Fatal(err)
	}
	copy := *pending
	if _, err := copy.PlanOriginal(t.Context()); err == nil {
		t.Fatal("copied holder accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := pending.PlanOriginal(ctx); err != context.Canceled {
		t.Fatal(err)
	}
	seen := pending.InspectOriginal()
	if !seen.PlanAttempted || seen.PlanCompleted || seen.ApplyAttempted || seen.Original.OpenedFDs != 0 || seen.Original.FirstError != context.Canceled || seen.Original.NativeOutcome != "unknown" {
		t.Fatal(seen)
	}
	if _, err := pending.ApplyOriginal(t.Context(), strings.Repeat("a", 64)); err != context.Canceled {
		t.Fatal("lost first failure", err)
	}
}
