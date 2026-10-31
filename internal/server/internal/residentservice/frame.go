package residentservice

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
)

const MaxFrames = 32
const MaxPayloadBytes = 16 << 10
const MaxJournalBytes = 1 << 20
const frameHeaderBytes = 80

var frameMagic = [8]byte{'A', 'R', 'M', 'S', 'V', 'C', '1', '\n'}
var frameEnd = [8]byte{'E', 'N', 'D', 'S', 'V', 'C', '1', '\n'}

// Encoded frames and decoded records are data ONLY; no native/service capability
// is manufactured by checksums, marker presence or an intact serialized record.
func encodeFrame(record Record, previous [sha256.Size]byte) ([]byte, error) {
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if len(payload) == 0 || len(payload) > MaxPayloadBytes || record.Schema != 1 || record.Sequence == 0 || record.Sequence > MaxFrames {
		return nil, ErrInvalid
	}
	frame := make([]byte, frameHeaderBytes+len(payload)+len(frameEnd))
	copy(frame[:8], frameMagic[:])
	binary.BigEndian.PutUint32(frame[8:12], record.Sequence)
	binary.BigEndian.PutUint32(frame[12:16], uint32(len(payload)))
	copy(frame[16:48], previous[:])
	sum := sha256.Sum256(payload)
	copy(frame[48:80], sum[:])
	copy(frame[80:], payload)
	copy(frame[80+len(payload):], frameEnd[:])
	return frame, nil
}

type decodedJournal struct {
	records []Record
	bytes   int64
	last    [sha256.Size]byte
}

func decodeJournal(data []byte) (decodedJournal, error) {
	var result decodedJournal
	if len(data) == 0 || len(data) > MaxJournalBytes {
		return result, ErrInvalid
	}
	for position := 0; position < len(data); {
		if len(result.records) >= MaxFrames || len(data)-position < frameHeaderBytes+len(frameEnd) {
			return decodedJournal{}, ErrInvalid
		}
		header := data[position : position+frameHeaderBytes]
		length := binary.BigEndian.Uint32(header[12:16])
		sequence := binary.BigEndian.Uint32(header[8:12])
		if !bytes.Equal(header[:8], frameMagic[:]) || length == 0 || length > MaxPayloadBytes || sequence != uint32(len(result.records)+1) || !bytes.Equal(header[16:48], result.last[:]) {
			return decodedJournal{}, ErrInvalid
		}
		end := position + frameHeaderBytes + int(length) + len(frameEnd)
		if end > len(data) || !bytes.Equal(data[end-len(frameEnd):end], frameEnd[:]) {
			return decodedJournal{}, ErrInvalid
		}
		payload := data[position+frameHeaderBytes : end-len(frameEnd)]
		sum := sha256.Sum256(payload)
		if !bytes.Equal(sum[:], header[48:80]) {
			return decodedJournal{}, ErrInvalid
		}
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		var record Record
		if err := decoder.Decode(&record); err != nil {
			return decodedJournal{}, ErrInvalid
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return decodedJournal{}, ErrInvalid
		}
		canonical, err := json.Marshal(record)
		if err != nil || !bytes.Equal(canonical, payload) || record.Sequence != sequence {
			return decodedJournal{}, ErrInvalid
		}
		result.records = append(result.records, record)
		result.last = sha256.Sum256(data[position:end])
		position = end
		result.bytes = int64(end)
	}
	if err := ValidateLedger(result.records); err != nil {
		return decodedJournal{}, err
	}
	return result, nil
}
