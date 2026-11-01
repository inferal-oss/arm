// Package residentprotocol handles bounded handoff DATA only. It issues no
// original owner, authenticated phase view, native authority or readiness.
package residentprotocol

import (
	"encoding/hex"
)

const WorkerBoundAckDomain = "arm-resident-worker-bound-ack-v1"
const WorkerCurrentChallengeDomain = "arm-resident-worker-current-challenge-v1"
const WorkerCurrentProofDomain = "arm-resident-worker-current-proof-v1"
const WorkerPendingPhase = "worker-prepared-native-pending"

// Hashes link the exact original native/layout/Env/A/settings/key and journals.
// These declarations are DATA; actual owners must separately check real bytes,
// original operation identity, current native state and once-only protocol phase.
type WorkerContextData struct {
	WorkspaceFormat          uint32 `json:"workspace_format"`
	ContractSHA256           string `json:"contract_sha256"`
	InitialPackageSHA256     string `json:"initial_package_sha256"`
	InitialManifestSHA256    string `json:"initial_manifest_sha256"`
	Generation               string `json:"generation"`
	DebitID                  string `json:"debit_id"`
	LaunchKeySHA256          string `json:"launch_key_sha256"`
	ProfileSHA256            string `json:"profile_sha256"`
	ImageSHA256              string `json:"image_sha256"`
	ImageBytes               int64  `json:"image_bytes"`
	SettingsSHA256           string `json:"settings_sha256"`
	EnvironmentSHA256        string `json:"environment_sha256"`
	IntentSHA256             string `json:"intent_sha256"`
	PreparedCommitmentSHA256 string `json:"prepared_commitment_sha256"`
	ServiceJournalBytes      int64  `json:"service_journal_bytes"`
	ServiceJournalSHA256     string `json:"service_journal_sha256"`
	FooterBytes              int64  `json:"footer_bytes"`
	FooterSHA256             string `json:"footer_sha256"`
	LaunchJournalBytes       int64  `json:"launch_journal_bytes"`
	LaunchJournalSHA256      string `json:"launch_journal_sha256"`
}

func (c WorkerContextData) Validate() error {
	if c.WorkspaceFormat != 5 || !lowerHexData(c.Generation, 16) {
		return ErrInvalid
	}
	for _, h := range []string{c.ContractSHA256, c.InitialPackageSHA256, c.InitialManifestSHA256, c.DebitID, c.LaunchKeySHA256, c.ProfileSHA256, c.ImageSHA256, c.SettingsSHA256, c.EnvironmentSHA256, c.IntentSHA256, c.PreparedCommitmentSHA256, c.ServiceJournalSHA256, c.FooterSHA256, c.LaunchJournalSHA256} {
		if !lowerHexData(h, 32) {
			return ErrInvalid
		}
	}
	// A service3 journal contains three nonempty frames (88-byte overhead each).
	// Actual canonical records and footer contents are checked by their owners.
	if c.ImageBytes <= 0 || c.ImageBytes > 256<<20 || c.ServiceJournalBytes < 3*(88+1) || c.ServiceJournalBytes > 3*(16<<10) || c.FooterBytes <= FooterFramingBytes || c.FooterBytes > MaxFooterBytes || c.LaunchJournalBytes <= 0 || c.LaunchJournalBytes > MaxLaunchJournalBytes {
		return ErrInvalid
	}
	if c.LaunchJournalBytes <= c.FooterBytes {
		return ErrInvalid
	}
	intentBytes := c.LaunchJournalBytes - c.FooterBytes // bounded before subtraction
	if intentBytes <= 0 || intentBytes > MaxWireBytes-WireHeaderBytes {
		return ErrInvalid
	}
	return nil
}
func MatchWorkerContextData(actual, expected WorkerContextData) error {
	if actual.Validate() != nil || expected.Validate() != nil || actual != expected {
		return ErrInvalid
	}
	return nil
}

type WorkerBoundAckBodyData struct {
	Schema   uint32            `json:"schema"`
	Domain   string            `json:"domain"`
	Kind     string            `json:"kind"`
	Sequence uint32            `json:"sequence"`
	Phase    string            `json:"phase"`
	Context  WorkerContextData `json:"context"`
}
type WorkerCurrentChallengeBodyData struct {
	Schema   uint32            `json:"schema"`
	Domain   string            `json:"domain"`
	Kind     string            `json:"kind"`
	Sequence uint32            `json:"sequence"`
	Phase    string            `json:"phase"`
	Context  WorkerContextData `json:"context"`
	Nonce    string            `json:"nonce"`
}
type WorkerCurrentProofBodyData struct {
	Schema   uint32            `json:"schema"`
	Domain   string            `json:"domain"`
	Kind     string            `json:"kind"`
	Sequence uint32            `json:"sequence"`
	Phase    string            `json:"phase"`
	Context  WorkerContextData `json:"context"`
	Nonce    string            `json:"nonce"`
}
type workerBoundAckPayloadData struct {
	Body WorkerBoundAckBodyData `json:"body"`
	MAC  string                 `json:"mac"`
}
type workerCurrentChallengePayloadData struct {
	Body WorkerCurrentChallengeBodyData `json:"body"`
	MAC  string                         `json:"mac"`
}
type workerCurrentProofPayloadData struct {
	Body WorkerCurrentProofBodyData `json:"body"`
	MAC  string                     `json:"mac"`
}

func workerBoundAckBodyData(c WorkerContextData) WorkerBoundAckBodyData {
	return WorkerBoundAckBodyData{2, WorkerBoundAckDomain, "bound_ack", 1, WorkerPendingPhase, c}
}
func workerCurrentChallengeBodyData(c WorkerContextData, n [32]byte) WorkerCurrentChallengeBodyData {
	return WorkerCurrentChallengeBodyData{2, WorkerCurrentChallengeDomain, "current_challenge", 2, WorkerPendingPhase, c, hex.EncodeToString(n[:])}
}
func workerCurrentProofBodyData(c WorkerContextData, n [32]byte) WorkerCurrentProofBodyData {
	return WorkerCurrentProofBodyData{2, WorkerCurrentProofDomain, "current_proof", 2, WorkerPendingPhase, c, hex.EncodeToString(n[:])}
}
func workerValidHeaderData(schema uint32, domain, kind string, seq uint32, phase, wantDomain, wantKind string, wantSeq uint32) bool {
	return schema == 2 && domain == wantDomain && kind == wantKind && seq == wantSeq && phase == WorkerPendingPhase
}
