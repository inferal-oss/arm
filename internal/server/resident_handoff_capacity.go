package server

import (
	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/residentrefs"
)

// Capacity DATA only, no original endpoint or secret/entropy. Full wire sizes
// use the ACTUAL existing outer encoder exactly once, including its36-byte header.
// This does not change any control phase or authorize transmission of fake MACs.
func p1HandoffCapacityData(context residentprotocol.ContextData) ([3]int, error) {
	var sizes [3]int
	if residentprotocol.MaxWireBytes != residentrefs.MaxWireBytes {
		return sizes, errResidentChildOriginal
	}
	payloads, err := residentprotocol.EncodeCapacityPayloadsData(context)
	if err != nil {
		return sizes, err
	}
	for i, payload := range [][]byte{payloads.BoundAck, payloads.CurrentChallenge, payloads.CurrentProof} {
		frame, err := residentrefs.EncodeFrameData(payload)
		if err != nil {
			return [3]int{}, err
		}
		if len(frame) != len(payload)+residentprotocol.WireHeaderBytes || len(frame) > residentrefs.MaxWireBytes {
			return [3]int{}, errResidentChildOriginal
		}
		sizes[i] = len(frame)
	}
	return sizes, nil
}
