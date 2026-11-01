package residentservice

import "encoding/json"

// Coverage assertions and artifact references are DATA for explicit review.
// Validation does not establish their truth or issue funding/native authority.
type WorkerMeasurement struct {
	Scope     string   `json:"scope"`
	Method    string   `json:"method"`
	Artifacts []string `json:"artifacts"`
}

var requiredWorkerMeasurementScopes = [...]string{
	"worker-native-launch-and-fixed-layout",
	"worker-private-protocol-buffers-and-unknowns",
	"worker-original-dispatch-and-response-settlement",
	"worker-lifetime-controller-receipts",
	"worker-automatic-renewal-cas",
	"worker-two-live-replacement-and-original-joins",
	"worker-package-assets-and-read-retention",
	"worker-public-start-and-native-reporting-continuity",
}

func WorkerMeasurementScopesData() []string {
	return append([]string(nil), requiredWorkerMeasurementScopes[:]...)
}

type WorkerEvidenceBundle struct {
	Schema               uint32              `json:"schema"`
	Core                 EvidenceBundle      `json:"core"`
	ContractSHA256       string              `json:"contract_sha256"`
	ContractMeasurements []WorkerMeasurement `json:"contract_measurements"`
}

// Bound allocations before invoking the existing Core validator's marshaling.
// All its semantic nine-workload/six-cost-scope/raw/digest checks still run.
func workerCoreEvidenceShapeData(e EvidenceBundle) error {
	if len(e.Artifacts) == 0 || len(e.Artifacts) > MaxEvidenceArtifacts || len(e.Unknowns) == 0 || len(e.Unknowns) > 16 || len(e.Coverage) != len(requiredStandingWorkloads) || len(e.CostScopes) != len(requiredStandingCostScopes) || !boundedText(e.Fixture, 128) || !validDigest(e.FixtureSourceSHA256) {
		return ErrInvalid
	}
	var total uint64
	for _, a := range e.Artifacts {
		if !boundedText(a.Name, 128) || !boundedText(a.Kind, 128) || !validDigest(a.SHA256) || len(a.Raw) > MaxEvidenceArtifactBytes || a.Size <= 0 || a.Size > MaxEvidenceArtifactBytes {
			return ErrInvalid
		}
		var err error
		total, err = workerAddData(total, uint64(len(a.Raw)))
		if err != nil || total > MaxEvidenceRawBytes {
			return ErrInvalid
		}
	}
	for _, value := range e.Unknowns {
		if !boundedText(value, 1024) {
			return ErrInvalid
		}
	}
	for _, row := range e.Coverage {
		if !boundedText(row.Workload, 128) || !boundedText(row.Method, 1024) || workerArtifactReferencesData(row.Artifacts, nil) != nil {
			return ErrInvalid
		}
	}
	for _, row := range e.CostScopes {
		if !boundedText(row.Scope, 128) || !boundedText(row.Method, 1024) || workerArtifactReferencesData(row.Artifacts, nil) != nil {
			return ErrInvalid
		}
	}
	return nil
}

func workerArtifactReferencesData(names []string, known map[string]bool) error {
	if len(names) == 0 || len(names) > MaxEvidenceArtifacts {
		return ErrInvalid
	}
	previous := ""
	for _, name := range names {
		if !boundedText(name, 128) || name <= previous || known != nil && !known[name] {
			return ErrInvalid
		}
		previous = name
	}
	return nil
}

func (e WorkerEvidenceBundle) Validate(p WorkerMeasuredProfile) error {
	if p.Validate() != nil || e.Schema != 3 || e.Core.Schema != 2 || workerCoreEvidenceShapeData(e.Core) != nil || e.Core.Validate(p.Core) != nil || len(e.ContractMeasurements) != len(requiredWorkerMeasurementScopes) {
		return ErrInvalid
	}
	coreRaw, err := json.Marshal(e.Core)
	if err != nil || digest(coreRaw) != p.Core.EvidenceSHA256 {
		return ErrInvalid // Core.Validate alone does not bind the supplied digest.
	}
	contractSHA, err := p.Contract.SHA256()
	if err != nil || contractSHA != e.ContractSHA256 {
		return ErrInvalid
	}
	known := make(map[string]bool, len(e.Core.Artifacts))
	for _, artifact := range e.Core.Artifacts {
		known[artifact.Name] = true
	}
	for i, row := range e.ContractMeasurements {
		if row.Scope != requiredWorkerMeasurementScopes[i] || !boundedText(row.Method, 1024) || workerArtifactReferencesData(row.Artifacts, known) != nil {
			return ErrInvalid
		}
	}
	raw, err := json.Marshal(e)
	if err != nil || len(raw) > MaxEvidenceBytes || digest(raw) != p.EvidenceSHA256 {
		return ErrInvalid
	}
	return nil
}

func decodeWorkerEvidenceData(raw []byte, profile WorkerMeasuredProfile) (WorkerEvidenceBundle, error) {
	var value WorkerEvidenceBundle
	if decodeOperatorDocument(raw, MaxEvidenceBytes, &value) != nil || value.Validate(profile) != nil {
		return WorkerEvidenceBundle{}, ErrInvalid
	}
	return value, nil
}
