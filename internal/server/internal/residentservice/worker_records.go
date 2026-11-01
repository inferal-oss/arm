package residentservice

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strings"
)

const workerJournalFrameWireBytes = 16 << 10
const workerJournalHeaderBytes = 80
const workerJournalFrames = 3
const workerJournalMaxBytes = workerJournalFrames * workerJournalFrameWireBytes

var workerJournalMagic = [8]byte{'A', 'R', 'M', 'S', 'V', 'C', '3', '\n'}
var workerJournalEnd = [8]byte{'E', 'N', 'D', 'S', 'V', 'C', '3', '\n'}

// Every type in this file is DATA. No decoder, valid hash, marker, native-ID
// number or record pointer constructs an original operator/debit/charge/birth.
type WorkerOperatorIntent struct {
	Key              string                `json:"key"`
	Home             string                `json:"home"`
	HomeID           NativeID              `json:"home_id"`
	ExpectedRevision uint64                `json:"expected_revision"`
	Profile          WorkerMeasuredProfile `json:"profile"`
	Reason           string                `json:"reason"`
	Inputs           OperatorInputs        `json:"inputs"`
}
type WorkerGrantRecord struct {
	ID       string               `json:"id"`
	Revision uint64               `json:"revision"`
	PlanSHA  string               `json:"plan_sha256"`
	Intent   WorkerOperatorIntent `json:"intent"`
}
type WorkerDebitRecord struct {
	ID                    string   `json:"id"`
	LaunchKey             string   `json:"launch_key"`
	GrantID               string   `json:"grant_id"`
	GrantRevision         uint64   `json:"grant_revision"`
	ProfileSHA256         string   `json:"profile_sha256"`
	ContractSHA256        string   `json:"contract_sha256"`
	InitialPackageSHA256  string   `json:"initial_package_sha256"`
	InitialManifestSHA256 string   `json:"initial_manifest_sha256"`
	SecretCommitment      string   `json:"secret_commitment"`
	LockID                NativeID `json:"lock_id"`
	JournalID             NativeID `json:"journal_id"`
}
type WorkerRootBoundRecord struct {
	DebitID          string `json:"debit_id"`
	LaunchKey        string `json:"launch_key"`
	Generation       string `json:"generation"`
	CommitmentSHA256 string `json:"commitment_sha256"`
	ContractSHA256   string `json:"contract_sha256"`
}
type WorkerRecordData struct {
	Schema   uint32                 `json:"schema"`
	Sequence uint32                 `json:"sequence"`
	Kind     string                 `json:"kind"`
	Grant    *WorkerGrantRecord     `json:"grant,omitempty"`
	Debit    *WorkerDebitRecord     `json:"debit,omitempty"`
	Root     *WorkerRootBoundRecord `json:"root,omitempty"`
}

func (i WorkerOperatorIntent) Validate() error {
	if !boundedText(i.Key, 128) || !boundedText(i.Reason, 1024) || !validInputPath(i.Home) || i.HomeID.Inode == 0 || i.Inputs.Validate() != nil {
		return ErrInvalid
	}
	profileSHA, err := i.Profile.SHA256()
	if err != nil || i.Inputs.Profile.SHA256 != profileSHA || i.Inputs.Evidence.SHA256 != i.Profile.EvidenceSHA256 || i.Inputs.Profile.ID.UID != i.HomeID.UID || i.Inputs.Evidence.ID.UID != i.HomeID.UID || i.Inputs.Profile.Path == i.Inputs.Evidence.Path {
		return ErrInvalid
	}
	raw, err := json.Marshal(i.Profile)
	if err != nil || int64(len(raw)) != i.Inputs.Profile.Size {
		return ErrInvalid
	}
	// Exact size-only skeleton: every valid grant ID/plan digest is 64 hex bytes.
	// It is not an original grant and is never handed to a native owner.
	zeroDigest := strings.Repeat("0", 64)
	skeleton := WorkerRecordData{Schema: 3, Sequence: 1, Kind: "worker-operator-authorized", Grant: &WorkerGrantRecord{ID: zeroDigest, Revision: 1, PlanSHA: zeroDigest, Intent: i}}
	if _, err = workerRecordPayloadData(skeleton); err != nil {
		return err
	}
	return nil
}

func (i WorkerOperatorIntent) SHA256() (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(i)
	if err != nil {
		return "", ErrInvalid
	}
	return digest(raw), nil
}

func validWorkerGenerationData(value string) bool {
	if len(value) != 32 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func ValidateWorkerLedgerData(records []WorkerRecordData) error {
	if len(records) == 0 || len(records) > workerJournalFrames {
		return ErrInvalid
	}
	first := records[0]
	if first.Schema != 3 || first.Sequence != 1 || first.Kind != "worker-operator-authorized" || first.Grant == nil || first.Debit != nil || first.Root != nil {
		return ErrInvalid
	}
	g := first.Grant
	if !validDigest(g.ID) || g.Revision != 1 || g.Intent.ExpectedRevision != 0 {
		return ErrInvalid
	}
	plan, err := g.Intent.SHA256()
	if err != nil || plan != g.PlanSHA {
		return ErrInvalid
	}
	for _, record := range records {
		if _, err = workerRecordPayloadData(record); err != nil {
			return err
		}
	}
	if len(records) == 1 {
		return nil
	}
	second := records[1]
	if second.Schema != 3 || second.Sequence != 2 || second.Kind != "worker-debit-prepared" || second.Debit == nil || second.Grant != nil || second.Root != nil {
		return ErrInvalid
	}
	d := second.Debit
	profileSHA, err := g.Intent.Profile.SHA256()
	if err != nil {
		return ErrInvalid
	}
	contractSHA, err := g.Intent.Profile.Contract.SHA256()
	if err != nil || !validDigest(d.ID) || !boundedText(d.LaunchKey, 128) || d.GrantID != g.ID || d.GrantRevision != g.Revision || d.ProfileSHA256 != profileSHA || d.ContractSHA256 != contractSHA || d.InitialPackageSHA256 != g.Intent.Profile.Contract.InitialPackageSHA256 || d.InitialManifestSHA256 != g.Intent.Profile.Contract.InitialManifestSHA256 || !validDigest(d.SecretCommitment) || d.LockID.Inode == 0 || d.JournalID.Inode == 0 || d.LockID.UID != g.Intent.HomeID.UID || d.JournalID.UID != g.Intent.HomeID.UID {
		return ErrInvalid
	}
	if len(records) == 2 {
		return nil
	}
	third := records[2]
	if third.Schema != 3 || third.Sequence != 3 || third.Kind != "worker-root-bound" || third.Root == nil || third.Grant != nil || third.Debit != nil {
		return ErrInvalid
	}
	r := third.Root
	if r.DebitID != d.ID || r.LaunchKey != d.LaunchKey || !validWorkerGenerationData(r.Generation) || !validDigest(r.CommitmentSHA256) || r.ContractSHA256 != contractSHA {
		return ErrInvalid
	}
	return nil
}

// Bound every variable-sized member before general encoding, including calls
// made by DATA preflight/negative controls. Cross-record semantics are separate.
func workerRecordShapeBoundsData(record WorkerRecordData) error {
	if len(record.Kind) > 32 {
		return ErrInvalid
	}
	if g := record.Grant; g != nil {
		i := g.Intent
		if len(g.ID) > 64 || len(g.PlanSHA) > 64 || len(i.Key) > 128 || len(i.Home) > 4096 || len(i.Reason) > 1024 || i.Profile.Validate() != nil || i.Inputs.Validate() != nil {
			return ErrInvalid
		}
	}
	if d := record.Debit; d != nil {
		for _, value := range []string{d.ID, d.GrantID, d.ProfileSHA256, d.ContractSHA256, d.InitialPackageSHA256, d.InitialManifestSHA256, d.SecretCommitment} {
			if len(value) > 64 {
				return ErrInvalid
			}
		}
		if len(d.LaunchKey) > 128 {
			return ErrInvalid
		}
	}
	if r := record.Root; r != nil {
		if len(r.DebitID) > 64 || len(r.LaunchKey) > 128 || len(r.Generation) > 32 || len(r.CommitmentSHA256) > 64 || len(r.ContractSHA256) > 64 {
			return ErrInvalid
		}
	}
	return nil
}

func workerRecordPayloadData(record WorkerRecordData) ([]byte, error) {
	if record.Schema != 3 || record.Sequence == 0 || record.Sequence > workerJournalFrames {
		return nil, ErrInvalid
	}
	if workerRecordShapeBoundsData(record) != nil {
		return nil, ErrInvalid
	}
	raw, err := json.Marshal(record)
	if err != nil || len(raw) == 0 || len(raw) > workerJournalFrameWireBytes-workerJournalHeaderBytes-len(workerJournalEnd) {
		return nil, ErrInvalid
	}
	return raw, nil
}

// New envelope, separate typed records and magic. Existing schema1/format4
// journal readers/encoders remain unchanged and cannot consume this journal.
func encodeWorkerRecordFrameData(record WorkerRecordData, previous [sha256.Size]byte) ([]byte, error) {
	payload, err := workerRecordPayloadData(record)
	if err != nil {
		return nil, err
	}
	frame := make([]byte, workerJournalHeaderBytes+len(payload)+len(workerJournalEnd))
	copy(frame[:8], workerJournalMagic[:])
	binary.BigEndian.PutUint32(frame[8:12], record.Sequence)
	binary.BigEndian.PutUint32(frame[12:16], uint32(len(payload)))
	copy(frame[16:48], previous[:])
	sum := sha256.Sum256(payload)
	copy(frame[48:80], sum[:])
	copy(frame[workerJournalHeaderBytes:], payload)
	copy(frame[workerJournalHeaderBytes+len(payload):], workerJournalEnd[:])
	return frame, nil
}

func encodeWorkerJournalData(records []WorkerRecordData) ([]byte, error) {
	if ValidateWorkerLedgerData(records) != nil {
		return nil, ErrInvalid
	}
	var result []byte
	var previous [sha256.Size]byte
	for _, record := range records {
		frame, err := encodeWorkerRecordFrameData(record, previous)
		if err != nil {
			return nil, err
		}
		result = append(result, frame...)
		previous = sha256.Sum256(frame)
	}
	return result, nil
}

type workerJournalData struct {
	records []WorkerRecordData
	bytes   int64
	last    [sha256.Size]byte
}

func decodeWorkerJournalData(raw []byte) (workerJournalData, error) {
	var result workerJournalData
	if len(raw) == 0 || len(raw) > workerJournalMaxBytes {
		return result, ErrInvalid
	}
	for position := 0; position < len(raw); {
		remaining := len(raw) - position
		if len(result.records) >= workerJournalFrames || remaining < workerJournalHeaderBytes+len(workerJournalEnd) {
			return workerJournalData{}, ErrInvalid
		}
		header := raw[position : position+workerJournalHeaderBytes]
		length := binary.BigEndian.Uint32(header[12:16])
		sequence := binary.BigEndian.Uint32(header[8:12])
		if !bytes.Equal(header[:8], workerJournalMagic[:]) || length == 0 || length > workerJournalFrameWireBytes-workerJournalHeaderBytes-uint32(len(workerJournalEnd)) || sequence != uint32(len(result.records)+1) || !bytes.Equal(header[16:48], result.last[:]) || int(length) > remaining-workerJournalHeaderBytes-len(workerJournalEnd) {
			return workerJournalData{}, ErrInvalid
		}
		end := position + workerJournalHeaderBytes + int(length) + len(workerJournalEnd)
		payload := raw[position+workerJournalHeaderBytes : end-len(workerJournalEnd)]
		if !bytes.Equal(raw[end-len(workerJournalEnd):end], workerJournalEnd[:]) {
			return workerJournalData{}, ErrInvalid
		}
		sum := sha256.Sum256(payload)
		if !bytes.Equal(sum[:], header[48:80]) {
			return workerJournalData{}, ErrInvalid
		}
		var record WorkerRecordData
		if decodeOperatorDocument(payload, workerJournalFrameWireBytes-workerJournalHeaderBytes-len(workerJournalEnd), &record) != nil || record.Sequence != sequence {
			return workerJournalData{}, ErrInvalid
		}
		result.records = append(result.records, record)
		result.last = sha256.Sum256(raw[position:end])
		position = end
		result.bytes = int64(end)
	}
	if ValidateWorkerLedgerData(result.records) != nil {
		return workerJournalData{}, ErrInvalid
	}
	return result, nil
}
