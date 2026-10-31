// Package residentprotocol handles bounded handoff DATA only. It issues no
// original owner, authenticated phase view, native authority or readiness.
package residentprotocol

import (
	"crypto/hmac"
	"encoding/hex"
	"errors"
)

const MaxWireBytes = 16 << 10
const WireHeaderBytes = 36
const MaxPayloadBytes = MaxWireBytes - WireHeaderBytes
const MaxIntentBytes = 16 << 10
const MaxFooterBytes = 4 << 10
const FooterFramingBytes = 52
const MaxLaunchJournalBytes = 20 << 10
const MaxServiceJournalBytes = 1 << 20
const MaxImageBytes = 1 << 30

const BoundAckDomain = "arm-resident-bound-ack-v1"
const CurrentChallengeDomain = "arm-resident-current-challenge-v1"
const CurrentProofDomain = "arm-resident-current-proof-v1"
const PendingPhase = "prepared-unstarted-worker-pending"

var ErrInvalid = errors.New("invalid resident handoff payload data")

// Hashes link the exact original native/layout/Env/A/settings/key and journals.
// These declarations are DATA; actual owners must separately check real bytes,
// original operation identity, current native state and once-only protocol phase.
type ContextData struct {
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

func lowerHexData(value string, byteCount int) bool {
	if len(value) != byteCount*2 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (c ContextData) Validate() error {
	if !lowerHexData(c.Generation, 16) {
		return ErrInvalid
	}
	for _, h := range []string{c.DebitID, c.LaunchKeySHA256, c.ProfileSHA256, c.ImageSHA256, c.SettingsSHA256, c.EnvironmentSHA256, c.IntentSHA256, c.PreparedCommitmentSHA256, c.ServiceJournalSHA256, c.FooterSHA256, c.LaunchJournalSHA256} {
		if !lowerHexData(h, 32) {
			return ErrInvalid
		}
	}
	// A service3 journal contains three nonempty frames (88-byte overhead each).
	// Actual canonical records and footer contents are checked by their owners.
	if c.ImageBytes <= 0 || c.ImageBytes > MaxImageBytes || c.ServiceJournalBytes < 3*(88+1) || c.ServiceJournalBytes > MaxServiceJournalBytes || c.FooterBytes <= FooterFramingBytes || c.FooterBytes > MaxFooterBytes || c.LaunchJournalBytes <= 0 || c.LaunchJournalBytes > MaxLaunchJournalBytes {
		return ErrInvalid
	}
	if c.LaunchJournalBytes <= c.FooterBytes {
		return ErrInvalid
	}
	intentBytes := c.LaunchJournalBytes - c.FooterBytes // bounded before subtraction
	if intentBytes <= 0 || intentBytes > MaxIntentBytes {
		return ErrInvalid
	}
	return nil
}
func MatchContextData(actual, expected ContextData) error {
	if actual.Validate() != nil || expected.Validate() != nil || actual != expected {
		return ErrInvalid
	}
	return nil
}
func MatchNonceData(encoded string, expected [32]byte) error {
	if !lowerHexData(encoded, 32) {
		return ErrInvalid
	}
	var actual [32]byte
	if _, err := hex.Decode(actual[:], []byte(encoded)); err != nil || !hmac.Equal(actual[:], expected[:]) {
		return ErrInvalid
	}
	return nil
}

type BoundAckBodyData struct {
	Schema   uint32      `json:"schema"`
	Domain   string      `json:"domain"`
	Kind     string      `json:"kind"`
	Sequence uint32      `json:"sequence"`
	Phase    string      `json:"phase"`
	Context  ContextData `json:"context"`
}
type CurrentChallengeBodyData struct {
	Schema   uint32      `json:"schema"`
	Domain   string      `json:"domain"`
	Kind     string      `json:"kind"`
	Sequence uint32      `json:"sequence"`
	Phase    string      `json:"phase"`
	Context  ContextData `json:"context"`
	Nonce    string      `json:"nonce"`
}
type CurrentProofBodyData struct {
	Schema   uint32      `json:"schema"`
	Domain   string      `json:"domain"`
	Kind     string      `json:"kind"`
	Sequence uint32      `json:"sequence"`
	Phase    string      `json:"phase"`
	Context  ContextData `json:"context"`
	Nonce    string      `json:"nonce"`
}
type boundAckPayloadData struct {
	Body BoundAckBodyData `json:"body"`
	MAC  string           `json:"mac"`
}
type currentChallengePayloadData struct {
	Body CurrentChallengeBodyData `json:"body"`
	MAC  string                   `json:"mac"`
}
type currentProofPayloadData struct {
	Body CurrentProofBodyData `json:"body"`
	MAC  string               `json:"mac"`
}

func boundAckBodyData(c ContextData) BoundAckBodyData {
	return BoundAckBodyData{1, BoundAckDomain, "bound_ack", 1, PendingPhase, c}
}
func currentChallengeBodyData(c ContextData, n [32]byte) CurrentChallengeBodyData {
	return CurrentChallengeBodyData{1, CurrentChallengeDomain, "current_challenge", 2, PendingPhase, c, hex.EncodeToString(n[:])}
}
func currentProofBodyData(c ContextData, n [32]byte) CurrentProofBodyData {
	return CurrentProofBodyData{1, CurrentProofDomain, "current_proof", 2, PendingPhase, c, hex.EncodeToString(n[:])}
}
func validHeaderData(schema uint32, domain, kind string, seq uint32, phase, wantDomain, wantKind string, wantSeq uint32) bool {
	return schema == 1 && domain == wantDomain && kind == wantKind && seq == wantSeq && phase == PendingPhase
}
