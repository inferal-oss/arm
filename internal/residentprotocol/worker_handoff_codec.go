package residentprotocol

import (
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Returned payloads require exactly one existing 36-byte outer frame. The
// actual sender must retain its original phase/owner gate before writing bytes.
func EncodeWorkerBoundAckPayloadData(secret [32]byte, c WorkerContextData) ([]byte, error) {
	if c.Validate() != nil {
		return nil, ErrInvalid
	}
	body := workerBoundAckBodyData(c)
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, ErrInvalid
	}
	return encodePayloadData(workerBoundAckPayloadData{body, hex.EncodeToString(macData(secret, WorkerBoundAckDomain, raw))})
}
func DecodeWorkerBoundAckPayloadData(secret [32]byte, payload []byte) (WorkerBoundAckBodyData, error) {
	var data workerBoundAckPayloadData
	if decodeCanonicalPayloadData(payload, &data) != nil {
		return WorkerBoundAckBodyData{}, ErrInvalid
	}
	b := data.Body
	if !workerValidHeaderData(b.Schema, b.Domain, b.Kind, b.Sequence, b.Phase, WorkerBoundAckDomain, "bound_ack", 1) || b.Context.Validate() != nil {
		return WorkerBoundAckBodyData{}, ErrInvalid
	}
	raw, err := json.Marshal(b)
	if err != nil || !validMACData(secret, WorkerBoundAckDomain, raw, data.MAC) {
		return WorkerBoundAckBodyData{}, ErrInvalid
	}
	return b, nil
}
func EncodeWorkerCurrentChallengePayloadData(secret [32]byte, c WorkerContextData, nonce [32]byte) ([]byte, error) {
	if c.Validate() != nil {
		return nil, ErrInvalid
	}
	body := workerCurrentChallengeBodyData(c, nonce)
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, ErrInvalid
	}
	return encodePayloadData(workerCurrentChallengePayloadData{body, hex.EncodeToString(macData(secret, WorkerCurrentChallengeDomain, raw))})
}
func DecodeWorkerCurrentChallengePayloadData(secret [32]byte, payload []byte) (WorkerCurrentChallengeBodyData, error) {
	var data workerCurrentChallengePayloadData
	if decodeCanonicalPayloadData(payload, &data) != nil {
		return WorkerCurrentChallengeBodyData{}, ErrInvalid
	}
	b := data.Body
	if !workerValidHeaderData(b.Schema, b.Domain, b.Kind, b.Sequence, b.Phase, WorkerCurrentChallengeDomain, "current_challenge", 2) || b.Context.Validate() != nil || !lowerHexData(b.Nonce, 32) {
		return WorkerCurrentChallengeBodyData{}, ErrInvalid
	}
	raw, err := json.Marshal(b)
	if err != nil || !validMACData(secret, WorkerCurrentChallengeDomain, raw, data.MAC) {
		return WorkerCurrentChallengeBodyData{}, ErrInvalid
	}
	return b, nil
}
func EncodeWorkerCurrentProofPayloadData(secret [32]byte, c WorkerContextData, nonce [32]byte) ([]byte, error) {
	if c.Validate() != nil {
		return nil, ErrInvalid
	}
	body := workerCurrentProofBodyData(c, nonce)
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, ErrInvalid
	}
	return encodePayloadData(workerCurrentProofPayloadData{body, hex.EncodeToString(macData(secret, WorkerCurrentProofDomain, raw))})
}
func DecodeWorkerCurrentProofPayloadData(secret [32]byte, payload []byte) (WorkerCurrentProofBodyData, error) {
	var data workerCurrentProofPayloadData
	if decodeCanonicalPayloadData(payload, &data) != nil {
		return WorkerCurrentProofBodyData{}, ErrInvalid
	}
	b := data.Body
	if !workerValidHeaderData(b.Schema, b.Domain, b.Kind, b.Sequence, b.Phase, WorkerCurrentProofDomain, "current_proof", 2) || b.Context.Validate() != nil || !lowerHexData(b.Nonce, 32) {
		return WorkerCurrentProofBodyData{}, ErrInvalid
	}
	raw, err := json.Marshal(b)
	if err != nil || !validMACData(secret, WorkerCurrentProofDomain, raw, data.MAC) {
		return WorkerCurrentProofBodyData{}, ErrInvalid
	}
	return b, nil
}

type WorkerCapacityPayloadsData struct{ BoundAck, CurrentChallenge, CurrentProof []byte }

// Capacity bytes use a fixed placeholder MAC/nonce, NOT an actual secret or
// entropy request. They are never authority or frames to send. The server
// preflight wraps these exact typed schemas once and uses only their lengths.
func EncodeWorkerCapacityPayloadsData(c WorkerContextData) (WorkerCapacityPayloadsData, error) {
	var result WorkerCapacityPayloadsData
	if c.Validate() != nil {
		return result, ErrInvalid
	}
	placeholder := strings.Repeat("f", 64)
	var nonce [32]byte
	for i := range nonce {
		nonce[i] = 255
	}
	var err error
	result.BoundAck, err = encodePayloadData(workerBoundAckPayloadData{workerBoundAckBodyData(c), placeholder})
	if err != nil {
		return WorkerCapacityPayloadsData{}, err
	}
	result.CurrentChallenge, err = encodePayloadData(workerCurrentChallengePayloadData{workerCurrentChallengeBodyData(c, nonce), placeholder})
	if err != nil {
		return WorkerCapacityPayloadsData{}, err
	}
	result.CurrentProof, err = encodePayloadData(workerCurrentProofPayloadData{workerCurrentProofBodyData(c, nonce), placeholder})
	if err != nil {
		return WorkerCapacityPayloadsData{}, err
	}
	return result, nil
}
