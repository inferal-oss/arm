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
func contextDataOnly() ContextData {
	h := strings.Repeat("a", 64)
	return ContextData{Generation: strings.Repeat("b", 32), DebitID: h, LaunchKeySHA256: h, ProfileSHA256: h, ImageSHA256: h, ImageBytes: 1, SettingsSHA256: h, EnvironmentSHA256: h, IntentSHA256: h, PreparedCommitmentSHA256: h, ServiceJournalBytes: 1024, ServiceJournalSHA256: h, FooterBytes: 1024, FooterSHA256: h, LaunchJournalBytes: 1056, LaunchJournalSHA256: h}
}
func TestHandoffTypedPayloadDataRoundTripAndCapacity(t *testing.T) {
	c := contextDataOnly()
	var key, nonce [32]byte
	key[0] = 7
	nonce[31] = 9
	capacity, err := EncodeCapacityPayloadsData(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("bound-ack", func(t *testing.T) {
		p, err := EncodeBoundAckPayloadData(key, c)
		if err != nil {
			t.Fatal(err)
		}
		b, err := DecodeBoundAckPayloadData(key, p)
		if err != nil || b.Context != c || b.Sequence != 1 || len(p) != len(capacity.BoundAck) {
			t.Fatal(err)
		}
	})
	t.Run("challenge", func(t *testing.T) {
		p, err := EncodeCurrentChallengePayloadData(key, c, nonce)
		if err != nil {
			t.Fatal(err)
		}
		b, err := DecodeCurrentChallengePayloadData(key, p)
		if err != nil || b.Context != c || MatchNonceData(b.Nonce, nonce) != nil || len(p) != len(capacity.CurrentChallenge) {
			t.Fatal(err)
		}
	})
	t.Run("proof", func(t *testing.T) {
		p, err := EncodeCurrentProofPayloadData(key, c, nonce)
		if err != nil {
			t.Fatal(err)
		}
		b, err := DecodeCurrentProofPayloadData(key, p)
		if err != nil || b.Context != c || MatchNonceData(b.Nonce, nonce) != nil || len(p) != len(capacity.CurrentProof) {
			t.Fatal(err)
		}
	})
}
func TestHandoffContextDataRejectsCrossFieldBounds(t *testing.T) {
	c := contextDataOnly()
	c.ImageBytes = MaxImageBytes
	c.ServiceJournalBytes = MaxServiceJournalBytes
	c.FooterBytes = MaxFooterBytes
	c.LaunchJournalBytes = MaxLaunchJournalBytes
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
func TestHandoffCanonicalPayloadDataRejectsAliasesAndTrailing(t *testing.T) {
	var key, nonce [32]byte
	payload, err := EncodeCurrentChallengePayloadData(key, contextDataOnly(), nonce)
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
				bad = bytes.Replace(bad, []byte(`"schema":1`), []byte(`"schema":1,"schema":1`), 1)
			case "alias":
				bad = bytes.Replace(bad, []byte(`"schema":1`), []byte(`"Schema":1`), 1)
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
			if _, err := DecodeCurrentChallengePayloadData(key, bad); err != ErrInvalid {
				t.Fatal(err)
			}
		})
	}
}
func TestHandoffAuthenticatedBodyDataRejectsSemanticMisbinding(t *testing.T) {
	var key, nonce [32]byte
	for _, name := range []string{"schema", "domain", "kind", "sequence", "phase", "nonce", "context"} {
		t.Run(name, func(t *testing.T) {
			body := currentChallengeBodyData(contextDataOnly(), nonce)
			switch name {
			case "schema":
				body.Schema = 2
			case "domain":
				body.Domain = CurrentProofDomain
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
			m.Write([]byte(CurrentChallengeDomain + "\x00"))
			m.Write(raw)
			payload, _ := json.Marshal(currentChallengePayloadData{body, hex.EncodeToString(m.Sum(nil))})
			if _, err := DecodeCurrentChallengePayloadData(key, payload); err != ErrInvalid {
				t.Fatal(err)
			}
		})
	}
}
func TestHandoffMACTypeAndExpectedNonceContextData(t *testing.T) {
	c := contextDataOnly()
	var key, wrongKey, nonce, otherNonce [32]byte
	wrongKey[0] = 1
	otherNonce[0] = 1
	ack, _ := EncodeBoundAckPayloadData(key, c)
	challenge, _ := EncodeCurrentChallengePayloadData(key, c, nonce)
	proof, _ := EncodeCurrentProofPayloadData(key, c, nonce)
	if _, err := DecodeBoundAckPayloadData(wrongKey, ack); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err := DecodeCurrentChallengePayloadData(wrongKey, challenge); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err := DecodeCurrentProofPayloadData(wrongKey, proof); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err := DecodeCurrentProofPayloadData(key, challenge); err != ErrInvalid {
		t.Fatal("type reflection accepted")
	}
	if _, err := DecodeCurrentChallengePayloadData(key, ack); err != ErrInvalid {
		t.Fatal("type reflection accepted")
	}
	var domainRecord currentChallengePayloadData
	if json.Unmarshal(challenge, &domainRecord) != nil {
		t.Fatal("DATA")
	}
	raw, _ := json.Marshal(domainRecord.Body)
	wrongDomainMAC := hmac.New(sha256.New, key[:])
	wrongDomainMAC.Write([]byte(CurrentProofDomain + "\x00"))
	wrongDomainMAC.Write(raw)
	domainRecord.MAC = hex.EncodeToString(wrongDomainMAC.Sum(nil))
	wrongDomainPayload, _ := json.Marshal(domainRecord)
	if _, err := DecodeCurrentChallengePayloadData(key, wrongDomainPayload); err != ErrInvalid {
		t.Fatal("HMAC domain prefix not checked")
	}
	var record currentProofPayloadData
	if json.Unmarshal(proof, &record) != nil {
		t.Fatal("DATA")
	}
	record.MAC = strings.Repeat("A", 64)
	bad, _ := json.Marshal(record)
	if _, err := DecodeCurrentProofPayloadData(key, bad); err != ErrInvalid {
		t.Fatal("MAC case accepted")
	}
	body, err := DecodeCurrentProofPayloadData(key, proof)
	if err != nil {
		t.Fatal(err)
	}
	if MatchNonceData(body.Nonce, otherNonce) != ErrInvalid || MatchNonceData(body.Nonce, nonce) != nil {
		t.Fatal("nonce comparison")
	}
	other := c
	other.Generation = strings.Repeat("c", 32)
	if MatchContextData(body.Context, other) != ErrInvalid || MatchContextData(body.Context, c) != nil {
		t.Fatal("context comparison")
	}
}
func TestStatelessHandoffCodecAcceptsExactReplayAsData(t *testing.T) {
	var key, nonce [32]byte
	payload, err := EncodeCurrentProofPayloadData(key, contextDataOnly(), nonce)
	if err != nil {
		t.Fatal(err)
	}
	first, err := DecodeCurrentProofPayloadData(key, payload)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DecodeCurrentProofPayloadData(key, payload)
	if err != nil || first != second {
		t.Fatal("codec must not invent phase/replay authority", err)
	}
}
