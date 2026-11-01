package residentprotocol

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// Synthetic DATA only. No actual secret/entropy/owner/endpoint is constructed.
func workerContextDataOnly() WorkerContextData {
	h := strings.Repeat("a", 64)
	return WorkerContextData{WorkspaceFormat: 5, ContractSHA256: h, InitialPackageSHA256: h, InitialManifestSHA256: h, Generation: strings.Repeat("b", 32), DebitID: h, LaunchKeySHA256: h, ProfileSHA256: h, ImageSHA256: h, ImageBytes: 1, SettingsSHA256: h, EnvironmentSHA256: h, IntentSHA256: h, PreparedCommitmentSHA256: h, ServiceJournalBytes: 1024, ServiceJournalSHA256: h, FooterBytes: 1024, FooterSHA256: h, LaunchJournalBytes: 1056, LaunchJournalSHA256: h}
}
func TestWorkerHandoffTypedPayloadDataRoundTripAndCapacity(t *testing.T) {
	c := workerContextDataOnly()
	var key, nonce [32]byte
	key[0] = 7
	nonce[31] = 9
	capacity, err := EncodeWorkerCapacityPayloadsData(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("bound-ack", func(t *testing.T) {
		p, err := EncodeWorkerBoundAckPayloadData(key, c)
		if err != nil {
			t.Fatal(err)
		}
		b, err := DecodeWorkerBoundAckPayloadData(key, p)
		if err != nil || b.Context != c || b.Sequence != 1 || len(p) != len(capacity.BoundAck) {
			t.Fatal(err)
		}
	})
	t.Run("challenge", func(t *testing.T) {
		p, err := EncodeWorkerCurrentChallengePayloadData(key, c, nonce)
		if err != nil {
			t.Fatal(err)
		}
		b, err := DecodeWorkerCurrentChallengePayloadData(key, p)
		if err != nil || b.Context != c || MatchNonceData(b.Nonce, nonce) != nil || len(p) != len(capacity.CurrentChallenge) {
			t.Fatal(err)
		}
	})
	t.Run("proof", func(t *testing.T) {
		p, err := EncodeWorkerCurrentProofPayloadData(key, c, nonce)
		if err != nil {
			t.Fatal(err)
		}
		b, err := DecodeWorkerCurrentProofPayloadData(key, p)
		if err != nil || b.Context != c || MatchNonceData(b.Nonce, nonce) != nil || len(p) != len(capacity.CurrentProof) {
			t.Fatal(err)
		}
	})
}
func TestWorkerHandoffContextDataRejectsCrossFieldBounds(t *testing.T) {
	c := workerContextDataOnly()
	c.ImageBytes = 256 << 20
	c.ServiceJournalBytes = 3 * (16 << 10)
	c.FooterBytes = MaxFooterBytes
	c.LaunchJournalBytes = MaxWireBytes - WireHeaderBytes + c.FooterBytes
	if c.Validate() != nil {
		t.Fatal("valid maximum DATA")
	}
	for _, name := range []string{"generation", "digest-case", "digest-hex", "image-zero", "image-overflow", "service-short", "service-overflow", "footer-header-only", "footer-overflow", "launch-no-intent", "launch-overflow", "intent-overflow"} {
		t.Run(name, func(t *testing.T) {
			bad := c
			switch name {
			case "generation":
				bad.Generation = "short"
			case "digest-case":
				bad.ImageSHA256 = strings.Repeat("A", 64)
			case "digest-hex":
				bad.EnvironmentSHA256 = strings.Repeat("g", 64)
			case "image-zero":
				bad.ImageBytes = 0
			case "image-overflow":
				bad.ImageBytes = math.MaxInt64
			case "service-short":
				bad.ServiceJournalBytes = 3*(88+1) - 1
			case "service-overflow":
				bad.ServiceJournalBytes = math.MaxInt64
			case "footer-header-only":
				bad.FooterBytes = FooterFramingBytes
			case "footer-overflow":
				bad.FooterBytes = math.MaxInt64
			case "launch-no-intent":
				bad.LaunchJournalBytes = bad.FooterBytes
			case "launch-overflow":
				bad.LaunchJournalBytes = math.MaxInt64
			case "intent-overflow":
				bad.FooterBytes = 64
				bad.LaunchJournalBytes = MaxIntentBytes + 65
			}
			if bad.Validate() != ErrInvalid {
				t.Fatal("invalid declaration accepted")
			}
		})
	}
}
func TestWorkerHandoffCanonicalPayloadDataRejectsAliasesAndTrailing(t *testing.T) {
	var key, nonce [32]byte
	payload, err := EncodeWorkerCurrentChallengePayloadData(key, workerContextDataOnly(), nonce)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unknown-envelope", "unknown-body", "duplicate", "alias", "leading-space", "trailing-space", "trailing-json", "truncated", "oversize"} {
		t.Run(name, func(t *testing.T) {
			bad := append([]byte(nil), payload...)
			switch name {
			case "unknown-envelope":
				bad = append([]byte(`{"foreign":1,`), bad[1:]...)
			case "unknown-body":
				bad = bytes.Replace(bad, []byte(`"body":{`), []byte(`"body":{"foreign":1,`), 1)
			case "duplicate":
				bad = bytes.Replace(bad, []byte(`"schema":2`), []byte(`"schema":2,"schema":2`), 1)
			case "alias":
				bad = bytes.Replace(bad, []byte(`"schema":2`), []byte(`"Schema":2`), 1)
			case "leading-space":
				bad = append([]byte{' '}, bad...)
			case "trailing-space":
				bad = append(bad, '\n')
			case "trailing-json":
				bad = append(bad, []byte(`{}`)...)
			case "truncated":
				bad = bad[:len(bad)-1]
			case "oversize":
				bad = make([]byte, MaxPayloadBytes+1)
			}
			if _, err := DecodeWorkerCurrentChallengePayloadData(key, bad); err != ErrInvalid {
				t.Fatal(err)
			}
		})
	}
}
func TestWorkerHandoffAuthenticatedBodyDataRejectsSemanticMisbinding(t *testing.T) {
	var key, nonce [32]byte
	for _, name := range []string{"schema", "domain", "kind", "sequence", "phase", "nonce", "context"} {
		t.Run(name, func(t *testing.T) {
			body := workerCurrentChallengeBodyData(workerContextDataOnly(), nonce)
			switch name {
			case "schema":
				body.Schema = 1
			case "domain":
				body.Domain = WorkerCurrentProofDomain
			case "kind":
				body.Kind = "current_proof"
			case "sequence":
				body.Sequence = 1
			case "phase":
				body.Phase = "ready"
			case "nonce":
				body.Nonce = strings.Repeat("A", 64)
			case "context":
				body.Context.LaunchJournalBytes = body.Context.FooterBytes
			}
			// Independently sign deliberately invalid typed DATA to ensure semantic
			// refusal is not merely a mismatched MAC after mutation.
			raw, _ := json.Marshal(body)
			m := hmac.New(sha256.New, key[:])
			m.Write([]byte(WorkerCurrentChallengeDomain + "\x00"))
			m.Write(raw)
			payload, _ := json.Marshal(workerCurrentChallengePayloadData{body, hex.EncodeToString(m.Sum(nil))})
			if _, err := DecodeWorkerCurrentChallengePayloadData(key, payload); err != ErrInvalid {
				t.Fatal(err)
			}
		})
	}
}
func TestWorkerHandoffMACTypeAndExpectedNonceContextData(t *testing.T) {
	c := workerContextDataOnly()
	var key, wrongKey, nonce, otherNonce [32]byte
	wrongKey[0] = 1
	otherNonce[0] = 1
	ack, _ := EncodeWorkerBoundAckPayloadData(key, c)
	challenge, _ := EncodeWorkerCurrentChallengePayloadData(key, c, nonce)
	proof, _ := EncodeWorkerCurrentProofPayloadData(key, c, nonce)
	if _, err := DecodeWorkerBoundAckPayloadData(wrongKey, ack); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err := DecodeWorkerCurrentChallengePayloadData(wrongKey, challenge); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err := DecodeWorkerCurrentProofPayloadData(wrongKey, proof); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err := DecodeWorkerCurrentProofPayloadData(key, challenge); err != ErrInvalid {
		t.Fatal("type reflection accepted")
	}
	if _, err := DecodeWorkerCurrentChallengePayloadData(key, ack); err != ErrInvalid {
		t.Fatal("type reflection accepted")
	}
	var domainRecord workerCurrentChallengePayloadData
	if json.Unmarshal(challenge, &domainRecord) != nil {
		t.Fatal("DATA")
	}
	raw, _ := json.Marshal(domainRecord.Body)
	wrongDomainMAC := hmac.New(sha256.New, key[:])
	wrongDomainMAC.Write([]byte(WorkerCurrentProofDomain + "\x00"))
	wrongDomainMAC.Write(raw)
	domainRecord.MAC = hex.EncodeToString(wrongDomainMAC.Sum(nil))
	wrongDomainPayload, _ := json.Marshal(domainRecord)
	if _, err := DecodeWorkerCurrentChallengePayloadData(key, wrongDomainPayload); err != ErrInvalid {
		t.Fatal("HMAC domain prefix not checked")
	}
	var record workerCurrentProofPayloadData
	if json.Unmarshal(proof, &record) != nil {
		t.Fatal("DATA")
	}
	record.MAC = strings.Repeat("A", 64)
	bad, _ := json.Marshal(record)
	if _, err := DecodeWorkerCurrentProofPayloadData(key, bad); err != ErrInvalid {
		t.Fatal("MAC case accepted")
	}
	body, err := DecodeWorkerCurrentProofPayloadData(key, proof)
	if err != nil {
		t.Fatal(err)
	}
	if MatchNonceData(body.Nonce, otherNonce) != ErrInvalid || MatchNonceData(body.Nonce, nonce) != nil {
		t.Fatal("nonce comparison")
	}
	other := c
	other.Generation = strings.Repeat("c", 32)
	if MatchWorkerContextData(body.Context, other) != ErrInvalid || MatchWorkerContextData(body.Context, c) != nil {
		t.Fatal("context comparison")
	}
}
func TestStatelessWorkerHandoffCodecAcceptsExactReplayAsData(t *testing.T) {
	var key, nonce [32]byte
	payload, err := EncodeWorkerCurrentProofPayloadData(key, workerContextDataOnly(), nonce)
	if err != nil {
		t.Fatal(err)
	}
	first, err := DecodeWorkerCurrentProofPayloadData(key, payload)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DecodeWorkerCurrentProofPayloadData(key, payload)
	if err != nil || first != second {
		t.Fatal("codec must not invent phase/replay authority", err)
	}
}

func TestWorkerHandoffRejectsLegacyMessagesAndContextRebindingData(t *testing.T) {
	var key, nonce [32]byte
	legacy, _ := EncodeBoundAckPayloadData(key, contextDataOnly())
	worker, _ := EncodeWorkerBoundAckPayloadData(key, workerContextDataOnly())
	if _, err := DecodeWorkerBoundAckPayloadData(key, legacy); err == nil {
		t.Fatal("legacy ACK entered worker protocol")
	}
	if _, err := DecodeBoundAckPayloadData(key, worker); err == nil {
		t.Fatal("worker ACK entered legacy protocol")
	}
	legacyChallenge, _ := EncodeCurrentChallengePayloadData(key, contextDataOnly(), nonce)
	if _, err := DecodeWorkerCurrentChallengePayloadData(key, legacyChallenge); err == nil {
		t.Fatal("legacy challenge entered worker protocol")
	}
	for _, field := range []string{"format", "contract", "package", "manifest"} {
		t.Run(field, func(t *testing.T) {
			original := workerContextDataOnly()
			other := original
			switch field {
			case "format":
				other.WorkspaceFormat = 4
			case "contract":
				other.ContractSHA256 = strings.Repeat("c", 64)
			case "package":
				other.InitialPackageSHA256 = strings.Repeat("c", 64)
			case "manifest":
				other.InitialManifestSHA256 = strings.Repeat("c", 64)
			}
			if MatchWorkerContextData(original, other) == nil {
				t.Fatal("different worker context accepted")
			}
		})
	}
}
