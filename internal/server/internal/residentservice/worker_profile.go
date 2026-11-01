package residentservice

import (
	"encoding/json"
	"math"
)

const WorkerStandingPurpose = "resident-workers-format5-v1"
const workerControlWireBytes = 16 << 10
const workerRequestWireBytes = 64 << 10
const workerResponseWireBytes = 256 << 10
const workerTransportHeaderBytes = 36
const workerLogTailBytes = 64 << 10
const workerLogInputBytes = 16 << 20
const workerManifestBytes = 64 << 10

// WorkerStandingContract is bounded DATA, not a worker reservation, original
// birth binding, measured maximum or native authority. All v1 limits are fixed.
type WorkerStandingContract struct {
	WorkspaceFormat                 uint32 `json:"workspace_format"`
	CoreProtocol                    uint32 `json:"core_protocol"`
	WorkerProtocol                  uint32 `json:"worker_protocol"`
	InitialPackageSHA256            string `json:"initial_package_sha256"`
	InitialManifestSHA256           string `json:"initial_manifest_sha256"`
	InitialManifestBytes            uint32 `json:"initial_manifest_bytes"`
	MaxLiveWorkers                  uint32 `json:"max_live_workers"`
	MaxInspectors                   uint32 `json:"max_inspectors"`
	MaxActivations                  uint32 `json:"max_activations"`
	MaxQueuedControl                uint32 `json:"max_queued_control"`
	MaxInFlightPerWorker            uint32 `json:"max_in_flight_per_worker"`
	MaxResponseOwners               uint32 `json:"max_response_owners"`
	MaxLifetimeAttempts             uint32 `json:"max_lifetime_attempts"`
	MaxLifecycleReceipts            uint32 `json:"max_lifecycle_receipts"`
	MaxPackageGenerations           uint32 `json:"max_package_generations"`
	MaxAssetSessions                uint32 `json:"max_asset_sessions"`
	LifecycleReceiptBytes           uint64 `json:"lifecycle_receipt_bytes"`
	DomainResultWireBytes           uint32 `json:"domain_result_wire_bytes"`
	CurrentLifecycleResultWireBytes uint32 `json:"current_lifecycle_result_wire_bytes"`
	ResponseWireBytes               uint32 `json:"response_wire_bytes"`
	RenewalRecordBytes              uint32 `json:"renewal_record_bytes"`
}

// The nested schema2 document stays DATA. No method here converts it into an
// old GrantRecord, OriginalDebit, ManagerCharge, claim or inherited capability.
type WorkerMeasuredProfile struct {
	Schema         uint32                 `json:"schema"`
	Core           MeasuredProfile        `json:"core"`
	Purpose        string                 `json:"purpose"`
	Contract       WorkerStandingContract `json:"contract"`
	EvidenceSHA256 string                 `json:"evidence_sha256"`
}

func (c WorkerStandingContract) Validate() error {
	if c.WorkspaceFormat != 5 || c.CoreProtocol != 1 || c.WorkerProtocol != 1 || !validDigest(c.InitialPackageSHA256) || !validDigest(c.InitialManifestSHA256) || c.InitialManifestBytes == 0 || c.InitialManifestBytes > workerManifestBytes {
		return ErrInvalid
	}
	if c.MaxLiveWorkers != 2 || c.MaxInspectors != 1 || c.MaxActivations != 1 || c.MaxQueuedControl != 16 || c.MaxInFlightPerWorker != 4 || c.MaxResponseOwners != 32 || c.MaxLifetimeAttempts != 128 || c.MaxLifecycleReceipts != 128 || c.MaxPackageGenerations != 2 || c.MaxAssetSessions != 32 {
		return ErrInvalid
	}
	if c.LifecycleReceiptBytes != 266240 || c.DomainResultWireBytes != 131072 || c.CurrentLifecycleResultWireBytes != 16384 || c.ResponseWireBytes != workerResponseWireBytes || c.RenewalRecordBytes != 4096 {
		return ErrInvalid
	}
	return nil
}

func (c WorkerStandingContract) SHA256() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", ErrInvalid
	}
	return digest(raw), nil
}

func workerAddData(a, b uint64) (uint64, error) {
	if a > math.MaxUint64-b {
		return 0, ErrInvalid
	}
	return a + b, nil
}
func workerMultiplyData(a, b uint64) (uint64, error) {
	if b != 0 && a > math.MaxUint64/b {
		return 0, ErrInvalid
	}
	return a * b, nil
}

// LogicalFloorData accounts for the fixed contract's known reservations only.
// Lifetime tails are conservatively retained for every attempt; no recycling or
// native settlement is assumed. IO also reserves every attempt's log-input cap.
// Additional core/runtime/native/driver/parser/SQL/package/asset costs require
// genuine measured coverage and original admission, never this arithmetic.
func (c WorkerStandingContract) LogicalFloorData() (Bounds, error) {
	if err := c.Validate(); err != nil {
		return Bounds{}, err
	}
	var retained uint64
	for _, term := range [][2]uint64{
		{uint64(c.MaxLifecycleReceipts), c.LifecycleReceiptBytes},
		{uint64(c.MaxResponseOwners), uint64(c.ResponseWireBytes)},
		{uint64(c.MaxLiveWorkers), uint64(c.RenewalRecordBytes)},
		{uint64(c.MaxLiveWorkers) * uint64(c.MaxInFlightPerWorker), 2 * workerRequestWireBytes},
		{uint64(c.MaxQueuedControl), workerControlWireBytes},
		{uint64(c.MaxLifetimeAttempts), 2 * workerLogTailBytes},
		{uint64(c.MaxPackageGenerations), workerManifestBytes},
	} {
		part, err := workerMultiplyData(term[0], term[1])
		if err != nil {
			return Bounds{}, err
		}
		retained, err = workerAddData(retained, part)
		if err != nil {
			return Bounds{}, err
		}
	}
	logs, err := workerMultiplyData(uint64(c.MaxLifetimeAttempts), workerLogInputBytes)
	if err != nil {
		return Bounds{}, err
	}
	ioBytes, err := workerAddData(retained, logs)
	if err != nil {
		return Bounds{}, err
	}
	// These intentionally weak count minima follow only from fixed participants;
	// they are not a complete native descriptor/task/process census.
	return Bounds{Tasks: 5, Descriptors: 8, Children: 2, Sockets: 2, NativePending: 2, IOBytes: ioBytes, RetainedReceiptBytes: retained}, nil
}

// The limit includes the transport header; payload cannot claim the whole wire.
func workerPayloadFitsData(wireLimit, payloadBytes uint64) error {
	if wireLimit != workerControlWireBytes && wireLimit != workerRequestWireBytes && wireLimit != workerResponseWireBytes || payloadBytes == 0 || payloadBytes > wireLimit-workerTransportHeaderBytes {
		return ErrInvalid
	}
	return nil
}

func (p WorkerMeasuredProfile) Validate() error {
	if p.Schema != 3 || p.Purpose != WorkerStandingPurpose || p.Core.Schema != 2 || p.Core.RequireP1Environment() != nil || !validDigest(p.EvidenceSHA256) || p.Contract.Validate() != nil {
		return ErrInvalid
	}
	floor, err := p.Contract.LogicalFloorData()
	if err != nil || runtimeBoundsSufficientData(p.Core.Bounds, floor) != nil {
		return ErrInvalid
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > MaxProfileBytes {
		return ErrInvalid
	}
	return nil
}
func (p WorkerMeasuredProfile) SHA256() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", ErrInvalid
	}
	return digest(raw), nil
}

func decodeWorkerProfileData(raw []byte) (WorkerMeasuredProfile, error) {
	var value WorkerMeasuredProfile
	if decodeOperatorDocument(raw, MaxProfileBytes, &value) != nil || value.Validate() != nil {
		return WorkerMeasuredProfile{}, ErrInvalid
	}
	return value, nil
}
