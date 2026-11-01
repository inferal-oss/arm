package residentbirth

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"

	"getarm.org/internal/server/internal/residentservice"
)

const MaxWorkerRootBoundFooterBytes = 4 << 10
const MaxWorkerLaunchJournalBytes = 20 << 10
const workerFooterHeaderBytes = 44

var workerFooterMagic = [8]byte{'A', 'R', 'M', 'R', 'B', 'F', '2', '\n'}
var workerFooterEnd = [8]byte{'E', 'N', 'D', 'R', 'B', 'F', '2', '\n'}

// Closed canonical DATA. The variable original key is committed with its own
// domain; original intent/prepared/service records retain the exact key link.
// No home path, settings object, secret, success bit or authority constructor.
type WorkerRootBoundFooterData struct {
	Schema                   uint32 `json:"schema"`
	Domain                   string `json:"domain"`
	Generation               string `json:"generation"`
	DebitID                  string `json:"debit_id"`
	LaunchKeySHA256          string `json:"launch_key_sha256"`
	PreparedCommitmentSHA256 string `json:"prepared_commitment_sha256"`
	IntentSHA256             string `json:"intent_sha256"`
	ProfileSHA256            string `json:"profile_sha256"`
	ImageSHA256              string `json:"image_sha256"`
	SettingsSHA256           string `json:"settings_sha256"`
	ContractSHA256           string `json:"contract_sha256"`
	InitialPackageSHA256     string `json:"initial_package_sha256"`
	InitialManifestSHA256    string `json:"initial_manifest_sha256"`
	ServiceJournalBytes      int64  `json:"service_journal_bytes"`
	ServiceJournalSHA256     string `json:"service_journal_sha256"`
}

func WorkerLaunchKeyCommitmentData(key string) (string, error) {
	return residentservice.WorkerLaunchKeyCommitmentData(key)
}
func WorkerFooterForServiceData(service residentservice.WorkerRootBoundData, intentSHA, settingsSHA string) (WorkerRootBoundFooterData, error) {
	key, err := WorkerLaunchKeyCommitmentData(service.Record.LaunchKey)
	if err != nil {
		return WorkerRootBoundFooterData{}, err
	}
	return WorkerRootBoundFooterData{Schema: 2, Domain: "arm-resident-worker-root-bound-footer-v1", Generation: service.Record.Generation, DebitID: service.Record.DebitID, LaunchKeySHA256: key, PreparedCommitmentSHA256: service.Record.CommitmentSHA256, IntentSHA256: intentSHA, ProfileSHA256: service.ProfileSHA256, ImageSHA256: service.ImageSHA256, SettingsSHA256: settingsSHA, ContractSHA256: service.Record.ContractSHA256, InitialPackageSHA256: service.InitialPackageSHA256, InitialManifestSHA256: service.InitialManifestSHA256, ServiceJournalBytes: service.JournalBytes, ServiceJournalSHA256: service.JournalSHA256}, nil
}
func EncodeWorkerRootBoundFooterData(data WorkerRootBoundFooterData) ([]byte, error) {
	if data.Schema != 2 || data.Domain != "arm-resident-worker-root-bound-footer-v1" || !footerDigestData(data.Generation, 16) || data.ServiceJournalBytes < 3*(88+1) || data.ServiceJournalBytes > 3*(16<<10) {
		return nil, errOriginalWorkerFence
	}
	for _, h := range []string{data.DebitID, data.LaunchKeySHA256, data.PreparedCommitmentSHA256, data.IntentSHA256, data.ProfileSHA256, data.ImageSHA256, data.SettingsSHA256, data.ContractSHA256, data.InitialPackageSHA256, data.InitialManifestSHA256, data.ServiceJournalSHA256} {
		if !footerDigestData(h, sha256.Size) {
			return nil, errOriginalWorkerFence
		}
	}
	payload, err := json.Marshal(data)
	if err != nil || len(payload) == 0 || workerFooterHeaderBytes+len(payload)+len(workerFooterEnd) > MaxWorkerRootBoundFooterBytes {
		return nil, errOriginalWorkerFence
	}
	frame := make([]byte, workerFooterHeaderBytes+len(payload)+len(workerFooterEnd))
	copy(frame[:8], workerFooterMagic[:])
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(payload)))
	h := sha256.Sum256(payload)
	copy(frame[12:44], h[:])
	copy(frame[44:], payload)
	copy(frame[44+len(payload):], workerFooterEnd[:])
	return frame, nil
}
func DecodeWorkerRootBoundFooterData(frame []byte) (WorkerRootBoundFooterData, error) {
	var data WorkerRootBoundFooterData
	if len(frame) <= workerFooterHeaderBytes+len(workerFooterEnd) || len(frame) > MaxWorkerRootBoundFooterBytes || !bytes.Equal(frame[:8], workerFooterMagic[:]) {
		return data, errOriginalWorkerFence
	}
	encodedLength := binary.BigEndian.Uint32(frame[8:12])
	if uint64(encodedLength) != uint64(len(frame)-workerFooterHeaderBytes-len(workerFooterEnd)) || !bytes.Equal(frame[len(frame)-len(workerFooterEnd):], workerFooterEnd[:]) {
		return data, errOriginalWorkerFence
	}
	length := int(encodedLength) // equality above bounds this conversion by the <=4KiB frame
	payload := frame[workerFooterHeaderBytes : workerFooterHeaderBytes+length]
	h := sha256.Sum256(payload)
	if !bytes.Equal(h[:], frame[12:44]) {
		return data, errOriginalWorkerFence
	}
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if d.Decode(&data) != nil || d.Decode(new(any)) != io.EOF {
		return WorkerRootBoundFooterData{}, errOriginalWorkerFence
	}
	canonical, err := EncodeWorkerRootBoundFooterData(data)
	if err != nil || !bytes.Equal(frame, canonical) {
		return WorkerRootBoundFooterData{}, errOriginalWorkerFence
	}
	return data, nil
}
func WorkerLaunchJournalCapacityData(intentBytes, footerBytes int) (int, error) {
	if intentBytes <= 0 || intentBytes > maxWorkerIntentBytes || footerBytes <= 0 || footerBytes > MaxWorkerRootBoundFooterBytes || footerBytes > MaxWorkerLaunchJournalBytes-intentBytes {
		return 0, errOriginalWorkerFence
	}
	return intentBytes + footerBytes, nil
}
