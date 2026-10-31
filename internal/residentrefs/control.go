package residentrefs

import (
	"crypto/sha256"
	"encoding/binary"
)

// Includes all header/checksum bytes. A 16KiB payload plus header is refused.
const MaxWireBytes = 16 << 10
const wireHeaderBytes = 36

// DATA encoding only. It issues no admission, birth, Ready, ACK or closure.
func EncodeFrameData(payload []byte) ([]byte, error) {
	if len(payload) == 0 || len(payload) > MaxWireBytes-wireHeaderBytes {
		return nil, ErrOriginal
	}
	frame := make([]byte, wireHeaderBytes+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	digest := sha256.Sum256(payload)
	copy(frame[4:36], digest[:])
	copy(frame[36:], payload)
	return frame, nil
}
