package residentservice

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// Entirely synthetic DATA. This never reads native/environment values or calls
// any operator, inherited issuer, charge, Manager, process or database factory.
func workerEvidenceData(t *testing.T) (WorkerMeasuredProfile, WorkerEvidenceBundle) {
	t.Helper()
	core, evidence := executionProfileData(t)
	core.Resources = Resources{CPU: 4, Memory: 1 << 30, Disk: 1 << 34}
	core.Bounds = Bounds{Tasks: 512, Descriptors: 512, Children: 512, Sockets: 512, NativePending: 512, IOBytes: 1 << 34, RetainedReceiptBytes: 1 << 30}
	evidence.ObservedResources, evidence.ObservedBounds = core.Resources, core.Bounds
	coreRaw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	core.EvidenceSHA256 = digest(coreRaw)
	contract := WorkerStandingContract{
		WorkspaceFormat: 5, CoreProtocol: 1, WorkerProtocol: 1,
		InitialPackageSHA256: strings.Repeat("c", 64), InitialManifestSHA256: strings.Repeat("d", 64), InitialManifestBytes: 1,
		MaxLiveWorkers: 2, MaxInspectors: 1, MaxActivations: 1, MaxQueuedControl: 16, MaxInFlightPerWorker: 4,
		MaxResponseOwners: 32, MaxLifetimeAttempts: 128, MaxLifecycleReceipts: 128, MaxPackageGenerations: 2, MaxAssetSessions: 32,
		LifecycleReceiptBytes: 266240, DomainResultWireBytes: 131072, CurrentLifecycleResultWireBytes: 16384, ResponseWireBytes: 262144, RenewalRecordBytes: 4096,
	}
	contractSHA, err := contract.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	p := WorkerMeasuredProfile{Schema: 3, Core: core, Purpose: WorkerStandingPurpose, Contract: contract}
	e := WorkerEvidenceBundle{Schema: 3, Core: evidence, ContractSHA256: contractSHA}
	for _, scope := range WorkerMeasurementScopesData() {
		e.ContractMeasurements = append(e.ContractMeasurements, WorkerMeasurement{Scope: scope, Method: "synthetic parser DATA only, never actual measurement", Artifacts: []string{"data"}})
	}
	workerOuterDigestData(t, &p, e)
	return p, e
}

func workerOuterDigestData(t *testing.T, p *WorkerMeasuredProfile, e WorkerEvidenceBundle) {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	p.EvidenceSHA256 = digest(raw)
}

func TestWorkerProfileCanonicalSeparateSchemaData(t *testing.T) {
	p, _ := workerEvidenceData(t)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := decodeWorkerProfileData(raw)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := p.SHA256()
	after, _ := parsed.SHA256()
	if before != after || before != digest(raw) {
		t.Fatal("canonical worker DATA hash differs")
	}
	var legacy MeasuredProfile
	if decodeOperatorDocument(raw, MaxProfileBytes, &legacy) == nil {
		t.Fatal("old profile document accepted worker grammar")
	}
	legacy = p.Core
	legacy.Schema = 3
	if legacy.Validate() == nil || legacy.RequireP1Environment() == nil {
		t.Fatal("old schema/role validator widened")
	}
}

func TestWorkerProfileRefusesCanonicalAndVersionDriftData(t *testing.T) {
	for _, name := range []string{"outer-schema", "core-schema", "purpose", "environment", "digest", "unknown", "duplicate", "trailing", "whitespace", "oversize"} {
		t.Run(name, func(t *testing.T) {
			p, _ := workerEvidenceData(t)
			switch name {
			case "outer-schema":
				p.Schema = 2
			case "core-schema":
				p.Core.Schema, p.Core.RuntimeEnvironment = 1, nil
			case "purpose":
				p.Purpose = "resident-root-standing-p1-v2"
			case "environment":
				p.Core.RuntimeEnvironment.Entries = append(p.Core.RuntimeEnvironment.Entries, "SECRET=synthetic")
			case "digest":
				p.EvidenceSHA256 = "missing"
			}
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "unknown":
				raw = append([]byte(`{"extra":1,`), raw[1:]...)
			case "duplicate":
				raw = append([]byte(`{"schema":3,`), raw[1:]...)
			case "trailing":
				raw = append(raw, []byte("{}")...)
			case "whitespace":
				raw = append(raw, '\n')
			case "oversize":
				raw = make([]byte, MaxProfileBytes+1)
			}
			if _, err = decodeWorkerProfileData(raw); err == nil {
				t.Fatal("invalid worker document accepted")
			}
		})
	}
}

func TestWorkerContractEveryFixedLimitAndCommitmentData(t *testing.T) {
	for _, name := range []string{"format", "core-protocol", "worker-protocol", "package", "manifest", "manifest-zero", "manifest-large", "live", "inspector", "activation", "queue", "inflight", "responses", "attempts", "receipts", "packages", "sessions", "receipt-bytes", "domain-bytes", "current-result-bytes", "response-bytes", "renewal-bytes", "overflow"} {
		t.Run(name, func(t *testing.T) {
			p, _ := workerEvidenceData(t)
			c := &p.Contract
			switch name {
			case "format":
				c.WorkspaceFormat = 4
			case "core-protocol":
				c.CoreProtocol++
			case "worker-protocol":
				c.WorkerProtocol++
			case "package":
				c.InitialPackageSHA256 = strings.Repeat("A", 64)
			case "manifest":
				c.InitialManifestSHA256 = "missing"
			case "manifest-zero":
				c.InitialManifestBytes = 0
			case "manifest-large":
				c.InitialManifestBytes = 65537
			case "live":
				c.MaxLiveWorkers++
			case "inspector":
				c.MaxInspectors++
			case "activation":
				c.MaxActivations++
			case "queue":
				c.MaxQueuedControl++
			case "inflight":
				c.MaxInFlightPerWorker++
			case "responses":
				c.MaxResponseOwners++
			case "attempts":
				c.MaxLifetimeAttempts++
			case "receipts":
				c.MaxLifecycleReceipts++
			case "packages":
				c.MaxPackageGenerations++
			case "sessions":
				c.MaxAssetSessions++
			case "receipt-bytes":
				c.LifecycleReceiptBytes--
			case "domain-bytes":
				c.DomainResultWireBytes++
			case "current-result-bytes":
				c.CurrentLifecycleResultWireBytes++
			case "response-bytes":
				c.ResponseWireBytes++
			case "renewal-bytes":
				c.RenewalRecordBytes++
			case "overflow":
				c.LifecycleReceiptBytes, c.MaxLifetimeAttempts = math.MaxUint64, math.MaxUint32
			}
			if c.Validate() == nil || p.Validate() == nil {
				t.Fatal("nonfixed contract accepted")
			}
			if _, err := c.LogicalFloorData(); err == nil {
				t.Fatal("invalid contract produced a floor")
			}
		})
	}
}

func TestWorkerLogicalReservationsAndOverflowData(t *testing.T) {
	p, _ := workerEvidenceData(t)
	b, err := p.Contract.LogicalFloorData()
	if err != nil || b.RetainedReceiptBytes != 60694528 || b.IOBytes != 2208178176 {
		t.Fatalf("logical wire/storage floor = %+v, %v", b, err)
	}
	for _, field := range []string{"tasks", "descriptors", "children", "sockets", "pending", "IO", "receipts"} {
		t.Run(field, func(t *testing.T) {
			copy := p
			copy.Core.Bounds = b
			switch field {
			case "tasks":
				copy.Core.Bounds.Tasks--
			case "descriptors":
				copy.Core.Bounds.Descriptors--
			case "children":
				copy.Core.Bounds.Children--
			case "sockets":
				copy.Core.Bounds.Sockets--
			case "pending":
				copy.Core.Bounds.NativePending--
			case "IO":
				copy.Core.Bounds.IOBytes--
			case "receipts":
				copy.Core.Bounds.RetainedReceiptBytes--
			}
			if copy.Validate() == nil {
				t.Fatal("under-reserved DATA accepted")
			}
		})
	}
	if _, err = workerAddData(math.MaxUint64, 1); err == nil {
		t.Fatal("add wrapped")
	}
	if _, err = workerMultiplyData(math.MaxUint64, 2); err == nil {
		t.Fatal("multiply wrapped")
	}
	if value, err := workerMultiplyData(math.MaxUint64, 0); err != nil || value != 0 {
		t.Fatal(value, err)
	}
}

func TestWorkerWholeFrameVersusPayloadData(t *testing.T) {
	for _, limit := range []uint64{workerControlWireBytes, workerRequestWireBytes, workerResponseWireBytes} {
		if workerPayloadFitsData(limit, limit-workerTransportHeaderBytes) != nil || workerPayloadFitsData(limit, limit-workerTransportHeaderBytes+1) == nil || workerPayloadFitsData(limit, limit) == nil || workerPayloadFitsData(limit, 0) == nil {
			t.Fatal("full encoded frame and payload ceilings conflated", limit)
		}
	}
	if workerPayloadFitsData(math.MaxUint64, 1) == nil {
		t.Fatal("unreviewed wire limit accepted")
	}
}
