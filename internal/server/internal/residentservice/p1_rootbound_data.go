package residentservice

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
)

const MaxP1RootBoundFrameBytes = 4 << 10

func P1RootBoundJournalBytesData(birthBytes int64, frameBytes int) (int64, error) {
	if birthBytes <= 0 || birthBytes > MaxJournalBytes || frameBytes <= 0 || frameBytes > MaxP1RootBoundFrameBytes || int64(frameBytes) > MaxJournalBytes-birthBytes {
		return 0, ErrInvalid
	}
	return birthBytes + int64(frameBytes), nil
}

// Canonical DATA only, with full existing service frame overhead included.
// No returned bytes or digest can issue an original writer or prepared Root.
func EncodeP1RootBoundFrameData(record RootBoundRecord, previousSHA string) ([]byte, error) {
	if !validDigest(record.DebitID) || !boundedText(record.LaunchKey, 128) || !validDigest(record.CommitmentSHA256) || len(record.Generation) != 32 || strings.ToLower(record.Generation) != record.Generation || !validDigest(previousSHA) {
		return nil, ErrInvalid
	}
	if _, err := hex.DecodeString(record.Generation); err != nil {
		return nil, ErrInvalid
	}
	previousBytes, err := hex.DecodeString(previousSHA)
	if err != nil {
		return nil, ErrInvalid
	}
	var previous [sha256.Size]byte
	copy(previous[:], previousBytes)
	frame, err := encodeFrame(Record{Schema: 1, Sequence: 3, Kind: "root-bound", Root: &record}, previous)
	if err != nil || len(frame) > MaxP1RootBoundFrameBytes {
		return nil, ErrInvalid
	}
	return frame, nil
}

// Before debit: actual unused-grant journal size and exact known launch key,
// maximal not-yet-known fixed fields. Numeric sentinels are DATA, not native IDs.
func P1RootBoundJournalCapacityData(grantBytes int64, launchKey string) (int64, error) {
	if grantBytes <= 0 || grantBytes > MaxJournalBytes || !boundedText(launchKey, 128) {
		return 0, ErrInvalid
	}
	h := strings.Repeat("f", 64)
	id := NativeID{Device: math.MaxUint64, Inode: math.MaxUint64, UID: math.MaxUint32}
	debit := DebitRecord{ID: h, LaunchKey: launchKey, GrantID: h, GrantRevision: math.MaxUint64, ProfileSHA256: h, SecretCommitment: h, LockID: id, JournalID: id}
	frame2, err := encodeFrame(Record{Schema: 1, Sequence: 2, Kind: "debit-prepared", Debit: &debit}, [sha256.Size]byte{})
	if err != nil {
		return 0, err
	}
	frame3, err := EncodeP1RootBoundFrameData(RootBoundRecord{DebitID: h, LaunchKey: launchKey, Generation: strings.Repeat("f", 32), CommitmentSHA256: h}, h)
	if err != nil {
		return 0, err
	}
	if int64(len(frame2)) > MaxJournalBytes-grantBytes {
		return 0, ErrInvalid
	}
	return P1RootBoundJournalBytesData(grantBytes+int64(len(frame2)), len(frame3))
}
