package residentservice

import (
	"encoding/json"

	"getarm.org/internal/residentcost"
)

const workerPendingLedgerArtifactName = "worker-pending-startup-ledger-v1"

// Each constituent is bounded by its own canonical ENCODED limit. A summary
// name can expand sixfold under JSON escaping; kind/digest are fixed ASCII and
// 20 bytes cover any positive int64 size. The remaining 4096 bounds the fixed
// outer keys, totals, unsupported literals and fingerprint. Check the actual
// whole encoded projection too; a future schema expansion must revise this.
const workerPendingArtifactSummaryBytes = 6*128 + len("owner-high-watermark") + 64 + 20 + len(`{"name":"","kind":"","sha256":"","size":}`)
const MaxWorkerPendingStartupReviewBytes = 2*residentcost.MaxLedgerBytes + MaxEvidenceArtifacts*(workerPendingArtifactSummaryBytes+1) + 4096
const workerPendingStartupReviewFramingBytes = len(",\"pending_startup_review\":")
const MaxWorkerOperatorPlanBytes = MaxProfileBytes + MaxWorkerPendingStartupReviewBytes + workerPendingStartupReviewFramingBytes

// This is a complete private operator-review projection, not an issuer, native
// charge, empirical truth assertion or runtime authorization. Artifact bytes,
// filesystem paths, runtime credentials and native FD capabilities stay private
// to their original evidence input owners.
type WorkerPendingStartupReview struct {
	Stage                string                                 `json:"stage"`
	SourceContractSHA256 string                                 `json:"source_contract_sha256"`
	SourceContract       residentcost.PendingSourceContractData `json:"source_contract"`
	Ledger               residentcost.PendingLedgerData         `json:"ledger"`
	CheckedTotals        residentcost.PendingTotalsData         `json:"checked_totals"`
	Artifacts            []OperatorArtifactSummary              `json:"artifacts"`
	Unsupported          []string                               `json:"unsupported"`
	Limit                string                                 `json:"limit"`
}

// Check only bounded committed DATA. The actual gate separately chooses its
// own compiled source contract and verifies genuine parent/child/charge/birth/
// runtime lineage. A caller-provided description is never native authority.
func workerPendingStartupReviewData(profile WorkerMeasuredProfile, evidence WorkerEvidenceBundle, required residentcost.PendingSourceContractData) (review WorkerPendingStartupReview, resultErr error) {
	if profile.Validate() != nil || evidence.Validate(profile) != nil {
		return review, ErrInvalid
	}
	fingerprint, err := required.FingerprintData()
	if err != nil {
		return review, ErrInvalid
	}
	var selected *MeasurementArtifact
	for i := range evidence.Core.Artifacts {
		a := &evidence.Core.Artifacts[i]
		if a.Name == workerPendingLedgerArtifactName {
			if selected != nil || a.Kind != "fixture-transcript" || a.Size != int64(len(a.Raw)) || a.SHA256 != digest(a.Raw) {
				return review, ErrInvalid
			}
			selected = a
		}
	}
	if selected == nil {
		return review, ErrInvalid
	}
	ledger, err := residentcost.DecodePendingLedgerData(selected.Raw)
	if err != nil {
		return review, ErrInvalid
	}
	environment, err := p1EnvironmentSHA256Data(profile.Core.RuntimeEnvironment)
	if err != nil {
		return review, ErrInvalid
	}
	contract, err := profile.Contract.SHA256()
	if err != nil || ledger.BuildSHA256 != profile.Core.Build.SHA256 || ledger.EnvironmentSHA256 != environment || ledger.ContractSHA256 != contract || ledger.InitialPackageSHA256 != profile.Contract.InitialPackageSHA256 || ledger.InitialManifestSHA256 != profile.Contract.InitialManifestSHA256 || ledger.SourceContractSHA256 != fingerprint {
		return review, ErrInvalid
	}
	totals, err := residentcost.CheckPendingLedgerData(required, ledger)
	if err != nil {
		return review, ErrInvalid
	}
	resources, bounds := profile.Core.Resources, profile.Core.Bounds
	if residentcost.CheckBudgetData(totals, residentcost.ResourcesData{CPU: resources.CPU, Memory: resources.Memory, Disk: resources.Disk}, residentcost.BoundsData{Tasks: uint64(bounds.Tasks), Descriptors: uint64(bounds.Descriptors), Children: uint64(bounds.Children), Sockets: uint64(bounds.Sockets), NativePending: uint64(bounds.NativePending), IOBytes: bounds.IOBytes, RetainedReceiptBytes: bounds.RetainedReceiptBytes}) != nil {
		return review, ErrInvalid
	}
	// Resolve every named observation/assumption reference against exact bound
	// evidence artifacts. The ledger cannot serve as its own measurement, hide
	// a reference or form an evidence cycle through another ledger record.
	names := make(map[string]bool, MaxEvidenceArtifacts)
	for _, name := range ledger.EnvelopeEvidence {
		names[name] = true
	}
	for _, unknown := range ledger.Unknowns {
		for _, name := range unknown.Evidence {
			names[name] = true
		}
	}
	if names[workerPendingLedgerArtifactName] || len(names) == 0 || len(names) > MaxEvidenceArtifacts-1 {
		return review, ErrInvalid
	}
	for _, artifact := range evidence.Core.Artifacts {
		if !names[artifact.Name] {
			continue
		}
		switch artifact.Kind {
		case "owner-high-watermark", "native-observation", "fixture-transcript":
		default:
			return WorkerPendingStartupReview{}, ErrInvalid
		}
		if artifact.Name == workerPendingLedgerArtifactName || artifact.Size != int64(len(artifact.Raw)) || artifact.SHA256 != digest(artifact.Raw) {
			return WorkerPendingStartupReview{}, ErrInvalid
		}
		if nested, decodeErr := residentcost.DecodePendingLedgerData(artifact.Raw); decodeErr == nil && nested.Stage == residentcost.PendingStage {
			return WorkerPendingStartupReview{}, ErrInvalid
		}
		review.Artifacts = append(review.Artifacts, OperatorArtifactSummary{Name: artifact.Name, Kind: artifact.Kind, SHA256: artifact.SHA256, Size: artifact.Size})
		delete(names, artifact.Name)
	}
	if len(names) != 0 {
		return WorkerPendingStartupReview{}, ErrInvalid
	}
	review.Stage = residentcost.PendingStage
	review.SourceContractSHA256, review.SourceContract, review.Ledger, review.CheckedTotals = fingerprint, required, ledger, totals
	review.Unsupported = []string{"nonempty-worker-ledger", "manager-runtime-loops", "API-UI-worker-controller", "public-ingress-readiness", "active-job-upgrade-continuity"}
	review.Limit = "Pending startup resource comparison only. Recorded component observations and explicit unknown allowances are not whole-root measurement truth, hard future RSS or runtime/worker activation authority."
	encoded, err := json.Marshal(review)
	if err != nil || len(encoded) > MaxWorkerPendingStartupReviewBytes {
		return WorkerPendingStartupReview{}, ErrInvalid
	}
	return review, nil
}
