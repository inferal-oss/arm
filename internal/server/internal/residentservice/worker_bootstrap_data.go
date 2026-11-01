package residentservice

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"

	"getarm.org/internal/mesh"
)

const WorkerCoreRole = "--arm-resident-worker-core-v1"
const workerBootstrapDomain = "arm-resident-worker-bootstrap-v1"
const workerBootstrapMaxWireBytes = 16 << 10

// Canonical private-channel DATA. This is neither a native descriptor registry
// nor a charge/birth. The outer composed worker-root bootstrap is also schema2;
// it must independently preflight its COMPLETE frame before launch effects.
type WorkerBootstrapData struct {
	Schema                uint32   `json:"schema"`
	Domain                string   `json:"domain"`
	Role                  string   `json:"role"`
	ChannelID             NativeID `json:"channel_id"`
	Home                  string   `json:"home"`
	HomeID                NativeID `json:"home_id"`
	Build                 Build    `json:"build"`
	DebitID               string   `json:"debit_id"`
	LaunchKey             string   `json:"launch_key"`
	ProfileSHA256         string   `json:"profile_sha256"`
	ContractSHA256        string   `json:"contract_sha256"`
	InitialPackageSHA256  string   `json:"initial_package_sha256"`
	InitialManifestSHA256 string   `json:"initial_manifest_sha256"`
	InitialManifestBytes  uint32   `json:"initial_manifest_bytes"`
	JournalBytes          int64    `json:"journal_bytes"`
	JournalSHA256         string   `json:"journal_sha256"`
	Secret                [32]byte `json:"secret"`
}

func validWorkerBuildData(b Build) bool {
	return validDigest(b.SHA256) && b.Size > 0 && b.Size <= mesh.MaxArtifactBytes &&
		(b.GOOS == "linux" || b.GOOS == "darwin") && (b.GOARCH == "arm64" || b.GOARCH == "amd64") &&
		boundedText(b.GoBuild, 256) && b.Protocol == 1
}

func (b WorkerBootstrapData) Validate() error {
	if b.Schema != 2 || b.Domain != workerBootstrapDomain || b.Role != WorkerCoreRole || !validInputPath(b.Home) || b.HomeID.Inode == 0 || b.ChannelID.Inode == 0 || b.ChannelID.UID != b.HomeID.UID || !validWorkerBuildData(b.Build) || !boundedText(b.LaunchKey, 128) || b.Secret == ([32]byte{}) {
		return ErrInvalid
	}
	for _, hash := range []string{b.DebitID, b.ProfileSHA256, b.ContractSHA256, b.InitialPackageSHA256, b.InitialManifestSHA256, b.JournalSHA256} {
		if !validDigest(hash) {
			return ErrInvalid
		}
	}
	if b.InitialManifestBytes == 0 || b.InitialManifestBytes > workerManifestBytes || b.JournalBytes < int64(2*(workerJournalHeaderBytes+len(workerJournalEnd)+1)) || b.JournalBytes > 2*workerJournalFrameWireBytes {
		return ErrInvalid
	}
	raw, err := json.Marshal(b)
	if err != nil || len(raw) > workerBootstrapMaxWireBytes-workerTransportHeaderBytes {
		return ErrInvalid
	}
	return nil
}

func encodeWorkerBootstrapFrameData(b WorkerBootstrapData) ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(b)
	if err != nil {
		return nil, ErrInvalid
	}
	frame := make([]byte, workerTransportHeaderBytes+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	sum := sha256.Sum256(payload)
	copy(frame[4:workerTransportHeaderBytes], sum[:])
	copy(frame[workerTransportHeaderBytes:], payload)
	return frame, nil
}

func decodeWorkerBootstrapFrameData(frame []byte) (WorkerBootstrapData, error) {
	var b WorkerBootstrapData
	if len(frame) <= workerTransportHeaderBytes || len(frame) > workerBootstrapMaxWireBytes {
		return b, ErrInvalid
	}
	length := binary.BigEndian.Uint32(frame[:4])
	if int(length) != len(frame)-workerTransportHeaderBytes {
		return b, ErrInvalid
	}
	payload := frame[workerTransportHeaderBytes:]
	sum := sha256.Sum256(payload)
	if !bytes.Equal(frame[4:workerTransportHeaderBytes], sum[:]) || decodeOperatorDocument(payload, workerBootstrapMaxWireBytes-workerTransportHeaderBytes, &b) != nil || b.Validate() != nil {
		return WorkerBootstrapData{}, ErrInvalid
	}
	return b, nil
}

// Pure consistency check; only actual inherited native read lineage can turn
// this DATA into a worker child. Secret/hash values never issue that lineage.
func validateWorkerBootstrapJournalData(b WorkerBootstrapData, journal workerJournalData) error {
	if b.Validate() != nil || ValidateWorkerLedgerData(journal.records) != nil || len(journal.records) != 2 || journal.bytes != b.JournalBytes || hex.EncodeToString(journal.last[:]) != b.JournalSHA256 {
		return ErrInvalid
	}
	g, d := journal.records[0].Grant, journal.records[1].Debit
	if g.Intent.Home != b.Home || g.Intent.HomeID != b.HomeID || g.Intent.Profile.Core.Build != b.Build || d.ID != b.DebitID || d.LaunchKey != b.LaunchKey || d.ProfileSHA256 != b.ProfileSHA256 || d.ContractSHA256 != b.ContractSHA256 || d.InitialPackageSHA256 != b.InitialPackageSHA256 || d.InitialManifestSHA256 != b.InitialManifestSHA256 || g.Intent.Profile.Contract.InitialManifestBytes != b.InitialManifestBytes || d.SecretCommitment != digest(b.Secret[:]) {
		return ErrInvalid
	}
	return nil
}
