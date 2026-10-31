package residentprotocol

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
)

// These private JSON helpers accept only the three typed envelope DATA shapes.
// They perform no owner registration, callbacks, native work or phase admission.
func encodePayloadData(value any) ([]byte, error) {
	b, err := json.Marshal(value)
	if err != nil || len(b) == 0 || len(b) > MaxPayloadBytes {
		return nil, ErrInvalid
	}
	return b, nil
}
func decodeCanonicalPayloadData(payload []byte, target any) error {
	if len(payload) == 0 || len(payload) > MaxPayloadBytes {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	canonical, err := json.Marshal(target)
	if err != nil || !bytes.Equal(canonical, payload) {
		return ErrInvalid
	}
	return nil
}
func macData(secret [32]byte, domain string, body []byte) []byte {
	m := hmac.New(sha256.New, secret[:])
	m.Write([]byte(domain))
	m.Write([]byte{0})
	m.Write(body)
	return m.Sum(nil)
}
func validMACData(secret [32]byte, domain string, body []byte, encoded string) bool {
	if !lowerHexData(encoded, 32) {
		return false
	}
	var actual [32]byte
	_, err := hex.Decode(actual[:], []byte(encoded))
	return err == nil && hmac.Equal(actual[:], macData(secret, domain, body))
}

// Returned payloads require exactly one existing 36-byte outer frame. The
// actual sender must retain its original phase/owner gate before writing bytes.
func EncodeBoundAckPayloadData(secret [32]byte, c ContextData) ([]byte, error) {
	if c.Validate() != nil {
		return nil, ErrInvalid
	}
	body := boundAckBodyData(c)
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, ErrInvalid
	}
	return encodePayloadData(boundAckPayloadData{body, hex.EncodeToString(macData(secret, BoundAckDomain, raw))})
}
func DecodeBoundAckPayloadData(secret [32]byte, payload []byte) (BoundAckBodyData, error) {
	var data boundAckPayloadData
	if decodeCanonicalPayloadData(payload, &data) != nil {
		return BoundAckBodyData{}, ErrInvalid
	}
	b := data.Body
	if !validHeaderData(b.Schema, b.Domain, b.Kind, b.Sequence, b.Phase, BoundAckDomain, "bound_ack", 1) || b.Context.Validate() != nil {
		return BoundAckBodyData{}, ErrInvalid
	}
	raw, err := json.Marshal(b)
	if err != nil || !validMACData(secret, BoundAckDomain, raw, data.MAC) {
		return BoundAckBodyData{}, ErrInvalid
	}
	return b, nil
}
func EncodeCurrentChallengePayloadData(secret [32]byte, c ContextData, nonce [32]byte) ([]byte, error) {
	if c.Validate() != nil {
		return nil, ErrInvalid
	}
	body := currentChallengeBodyData(c, nonce)
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, ErrInvalid
	}
	return encodePayloadData(currentChallengePayloadData{body, hex.EncodeToString(macData(secret, CurrentChallengeDomain, raw))})
}
func DecodeCurrentChallengePayloadData(secret [32]byte, payload []byte) (CurrentChallengeBodyData, error) {
	var data currentChallengePayloadData
	if decodeCanonicalPayloadData(payload, &data) != nil {
		return CurrentChallengeBodyData{}, ErrInvalid
	}
	b := data.Body
	if !validHeaderData(b.Schema, b.Domain, b.Kind, b.Sequence, b.Phase, CurrentChallengeDomain, "current_challenge", 2) || b.Context.Validate() != nil || !lowerHexData(b.Nonce, 32) {
		return CurrentChallengeBodyData{}, ErrInvalid
	}
	raw, err := json.Marshal(b)
	if err != nil || !validMACData(secret, CurrentChallengeDomain, raw, data.MAC) {
		return CurrentChallengeBodyData{}, ErrInvalid
	}
	return b, nil
}
func EncodeCurrentProofPayloadData(secret [32]byte, c ContextData, nonce [32]byte) ([]byte, error) {
	if c.Validate() != nil {
		return nil, ErrInvalid
	}
	body := currentProofBodyData(c, nonce)
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, ErrInvalid
	}
	return encodePayloadData(currentProofPayloadData{body, hex.EncodeToString(macData(secret, CurrentProofDomain, raw))})
}
func DecodeCurrentProofPayloadData(secret [32]byte, payload []byte) (CurrentProofBodyData, error) {
	var data currentProofPayloadData
	if decodeCanonicalPayloadData(payload, &data) != nil {
		return CurrentProofBodyData{}, ErrInvalid
	}
	b := data.Body
	if !validHeaderData(b.Schema, b.Domain, b.Kind, b.Sequence, b.Phase, CurrentProofDomain, "current_proof", 2) || b.Context.Validate() != nil || !lowerHexData(b.Nonce, 32) {
		return CurrentProofBodyData{}, ErrInvalid
	}
	raw, err := json.Marshal(b)
	if err != nil || !validMACData(secret, CurrentProofDomain, raw, data.MAC) {
		return CurrentProofBodyData{}, ErrInvalid
	}
	return b, nil
}

type CapacityPayloadsData struct{ BoundAck, CurrentChallenge, CurrentProof []byte }

// Capacity bytes use a fixed placeholder MAC/nonce, NOT an actual secret or
// entropy request. They are never authority or frames to send. The server
// preflight wraps these exact typed schemas once and uses only their lengths.
func EncodeCapacityPayloadsData(c ContextData) (CapacityPayloadsData, error) {
	var result CapacityPayloadsData
	if c.Validate() != nil {
		return result, ErrInvalid
	}
	placeholder := strings.Repeat("f", 64)
	var nonce [32]byte
	for i := range nonce {
		nonce[i] = 255
	}
	var err error
	result.BoundAck, err = encodePayloadData(boundAckPayloadData{boundAckBodyData(c), placeholder})
	if err != nil {
		return CapacityPayloadsData{}, err
	}
	result.CurrentChallenge, err = encodePayloadData(currentChallengePayloadData{currentChallengeBodyData(c, nonce), placeholder})
	if err != nil {
		return CapacityPayloadsData{}, err
	}
	result.CurrentProof, err = encodePayloadData(currentProofPayloadData{currentProofBodyData(c, nonce), placeholder})
	if err != nil {
		return CapacityPayloadsData{}, err
	}
	return result, nil
}
