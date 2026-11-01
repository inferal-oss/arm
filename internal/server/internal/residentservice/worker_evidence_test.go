package residentservice

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkerEvidenceBindsBothEvidenceDigestsAndContractData(t *testing.T) {
	p, e := workerEvidenceData(t)
	if err := e.Validate(p); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(e)
	if _, err := decodeWorkerEvidenceData(raw, p); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"outer-evidence", "nested-core-evidence", "contract", "outer-schema", "core-schema"} {
		t.Run(name, func(t *testing.T) {
			p, e := workerEvidenceData(t)
			switch name {
			case "outer-evidence":
				p.EvidenceSHA256 = strings.Repeat("f", 64)
			case "nested-core-evidence":
				p.Core.EvidenceSHA256 = strings.Repeat("f", 64)
				if e.Core.Validate(p.Core) != nil {
					t.Fatal("control must remain shape-valid to test missing old digest binding")
				}
			case "contract":
				e.ContractSHA256 = strings.Repeat("f", 64)
				workerOuterDigestData(t, &p, e)
			case "outer-schema":
				e.Schema = 2
				workerOuterDigestData(t, &p, e)
			case "core-schema":
				e.Core.Schema = 1
				workerOuterDigestData(t, &p, e)
			}
			if e.Validate(p) == nil {
				t.Fatal("digest or version mismatch accepted")
			}
		})
	}
}

func TestWorkerEvidenceOrderedCoverageAndArtifactReferencesData(t *testing.T) {
	for _, name := range []string{"missing", "extra", "duplicate", "unknown", "reordered", "empty-method", "long-method", "missing-reference", "unknown-reference", "duplicate-reference", "too-many-references", "long-reference"} {
		t.Run(name, func(t *testing.T) {
			p, e := workerEvidenceData(t)
			switch name {
			case "missing":
				e.ContractMeasurements = e.ContractMeasurements[:7]
			case "extra":
				e.ContractMeasurements = append(e.ContractMeasurements, e.ContractMeasurements[0])
			case "duplicate":
				e.ContractMeasurements[1] = e.ContractMeasurements[0]
			case "unknown":
				e.ContractMeasurements[0].Scope = "unreviewed"
			case "reordered":
				e.ContractMeasurements[0], e.ContractMeasurements[1] = e.ContractMeasurements[1], e.ContractMeasurements[0]
			case "empty-method":
				e.ContractMeasurements[0].Method = ""
			case "long-method":
				e.ContractMeasurements[0].Method = strings.Repeat("x", 1025)
			case "missing-reference":
				e.ContractMeasurements[0].Artifacts = nil
			case "unknown-reference":
				e.ContractMeasurements[0].Artifacts = []string{"missing"}
			case "duplicate-reference":
				e.ContractMeasurements[0].Artifacts = []string{"data", "data"}
			case "too-many-references":
				e.ContractMeasurements[0].Artifacts = make([]string, MaxEvidenceArtifacts+1)
			case "long-reference":
				e.ContractMeasurements[0].Artifacts = []string{strings.Repeat("x", 129)}
			}
			workerOuterDigestData(t, &p, e)
			if e.Validate(p) == nil {
				t.Fatal("invalid worker coverage accepted")
			}
		})
	}
	copy := WorkerMeasurementScopesData()
	copy[0] = "changed"
	if WorkerMeasurementScopesData()[0] == "changed" {
		t.Fatal("coverage roster alias escaped")
	}
	known := map[string]bool{"a": true, "b": true}
	if workerArtifactReferencesData([]string{"a", "b"}, known) != nil || workerArtifactReferencesData([]string{"b", "a"}, known) == nil {
		t.Fatal("artifact references must use canonical sorted order")
	}
}

func TestWorkerEvidencePreservesCoreCoverageRawAndUnknownChecksData(t *testing.T) {
	for _, name := range []string{"workload", "cost-scope", "raw", "raw-size", "raw-total", "artifact-hash", "no-unknowns", "resources", "bounds", "environment", "interval"} {
		t.Run(name, func(t *testing.T) {
			p, e := workerEvidenceData(t)
			switch name {
			case "workload":
				e.Core.Coverage = e.Core.Coverage[:8]
			case "cost-scope":
				e.Core.CostScopes = e.Core.CostScopes[:5]
			case "raw":
				e.Core.Artifacts[0].Raw = nil
			case "raw-size":
				e.Core.Artifacts[0].Raw = make([]byte, MaxEvidenceArtifactBytes+1)
			case "raw-total":
				a := e.Core.Artifacts[0]
				a.Raw = make([]byte, MaxEvidenceArtifactBytes)
				a.Size = int64(len(a.Raw))
				a.SHA256 = digest(a.Raw)
				e.Core.Artifacts = nil
				for i := 0; i < 9; i++ {
					value := a
					value.Name = string(rune('a' + i))
					e.Core.Artifacts = append(e.Core.Artifacts, value)
				}
			case "artifact-hash":
				e.Core.Artifacts[0].SHA256 = strings.Repeat("f", 64)
			case "no-unknowns":
				e.Core.Unknowns = nil
			case "resources":
				e.Core.ObservedResources.Memory = p.Core.Resources.Memory + 1
			case "bounds":
				e.Core.ObservedBounds.Tasks = p.Core.Bounds.Tasks + 1
			case "environment":
				e.Core.RuntimeEnvironment.Entries[0] = "GOMAXPROCS=2"
			case "interval":
				e.Core.Finished = e.Core.Started
			}
			coreRaw, _ := json.Marshal(e.Core)
			p.Core.EvidenceSHA256 = digest(coreRaw)
			workerOuterDigestData(t, &p, e)
			if e.Validate(p) == nil {
				t.Fatal("old Core invariant dropped by worker wrapper")
			}
		})
	}
}

func TestWorkerEvidenceCanonicalDecoderData(t *testing.T) {
	for _, name := range []string{"unknown", "duplicate", "trailing", "whitespace", "oversize"} {
		t.Run(name, func(t *testing.T) {
			p, e := workerEvidenceData(t)
			raw, _ := json.Marshal(e)
			switch name {
			case "unknown":
				raw = append([]byte(`{"extra":1,`), raw[1:]...)
			case "duplicate":
				raw = append([]byte(`{"schema":3,`), raw[1:]...)
			case "trailing":
				raw = append(raw, []byte("{}")...)
			case "whitespace":
				raw = append(raw, '\n')
			case "oversize":
				raw = make([]byte, MaxEvidenceBytes+1)
			}
			if _, err := decodeWorkerEvidenceData(raw, p); err == nil {
				t.Fatal("noncanonical worker evidence accepted")
			}
		})
	}
}
