package server

import (
	"fmt"
	"reflect"
	"runtime"
	"unsafe"

	"getarm.org/internal/database"
	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/residentcost"
	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/nativeowner"
	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
)

// compiledWorkerPendingSourceData is the trusted assembly's pure source census.
// It never reads a filesystem, invokes an initializer, issues a funding view or
// creates authority from its descriptions. The real original funding owner
// binds this DATA to the same profile/build/contract and its native evidence.
// Linux is the only pending constructor census currently covered; another OS
// must return its own complete reviewed census before this stage can proceed.
func compiledWorkerPendingSourceData() (residentcost.PendingSourceContractData, error) {
	var c residentcost.PendingSourceContractData
	if runtime.GOOS != "linux" {
		return c, errResidentWorkerChildOriginal
	}
	headers, err := residentcost.OwnHeaderReservationData([][]residentcost.OwnHeaderData{
		residentrefs.WorkerOwnHeadersData(), mesh.WorkerOwnHeadersData(),
		residentbirth.WorkerOwnHeadersData(), residentservice.WorkerOwnHeadersData(),
		nativeowner.WorkerOwnHeadersData(), database.WorkerOwnHeadersData(),
		manager.WorkerOwnHeadersData(), WorkerOwnHeadersData(),
	}, 1, 16<<20)
	if err != nil {
		return c, err
	}
	// Inspect the same Server's declared AgentStatus field TYPE, without opening
	// or constructing that service. Missing/incompatible composition refuses.
	status, ok := reflect.TypeOf(Server{}).FieldByName("status")
	if !ok || status.Type.Kind() != reflect.Pointer || status.Type.Elem().Kind() != reflect.Struct {
		return c, errResidentWorkerChildOriginal
	}
	statusHeader := uint64(status.Type.Elem().Size())
	if statusHeader > 1<<20 || headers+18*statusHeader > 16<<20 {
		return c, errResidentWorkerChildOriginal
	}
	calls, observerIO := residentcost.PendingLinuxObserverReadBoundsData()
	const MiB = 1 << 20
	// Source-known capacities are reservations, not observed or exclusive RSS.
	// Each row is counted ONCE at cardinality1; its backing field contains the
	// explicit finite multiplicity described in its derivation.
	c = residentcost.PendingSourceContractData{Schema: 1, Stage: residentcost.PendingStage}
	add := func(scope, owner, derivation string, memory, disk uint64, b residentcost.BoundsData) {
		c.Terms = append(c.Terms, residentcost.SourceTermData{Scope: scope, Owner: owner, Derivation: derivation, Lifetime: "original-core", Cardinality: 1, BackingMemoryBytes: memory, RetainedDiskBytes: disk, PerInstance: b})
	}
	add("original-custody-and-entry", "original-main-parent-child-custody", fmt.Sprintf("Exact ONE-of-each compiled ARM header roster:%dbytes; fixed arrays included.16MiB source header reservation/guard, not a live collection cardinality bound. Additional header multiplicity/unknown collection backing is explicitly UNOBSERVED in retained-partial-abnormal-startup-operations, proposed240MiB including112MiB moved out of this source row. Fresh SQL rows<=312 and preflight256objects/256arrays each are separate concrete bounds; no unsafe.Sizeof inference for dynamic/helper/native internals.", headers), 16*MiB, 0, residentcost.BoundsData{Tasks: 256, Descriptors: 13, Children: 1, NativePending: 64, RetainedReceiptBytes: MiB})
	add("bounded-funding-input", "original-parent-child-evidence-readers", "Two129-open retained readers; each evidence<=1MiB,32public ReadAt calls. Streaming preflight precedes typed decode:4096tokens/256objects/256arrays/depth16/64elements per array. Reserve32MiB per process for raw/token/typed/base64/canonical-sixfold escaping/old-new/private copies, including malformed decode tails; not a syscall-count bound.", 64*MiB, 2*MiB, residentcost.BoundsData{Descriptors: 258, NativePending: 258, IOBytes: 64 * MiB, RetainedReceiptBytes: 2 * MiB})
	add("service-birth-and-rootbound", "original-worker-service-and-footer", "Immutable birth2 plus separate service3/current-read2, bounded48KiB each and32public IO attempts per owner; retain canonical/decoded/old-new buffers. Max service journal1MiB disk.128MiB IO reserves four32-call1MiB service/journal source histories. No close/timeout reuse or release credit.", MiB, MiB, residentcost.BoundsData{Descriptors: 16, NativePending: 32, IOBytes: 128 * MiB, RetainedReceiptBytes: MiB})
	add("inherited-image-and-fence", "original-capture-private-pin-fence-and-inherited-image", "Two captured/mapped image reserves256MiB each plus4MiB buffers/intent/footer/source backing.512MiB image disk plus2MiB intent/launch metadata.16GiB source IO reserve covers up to four separately retained8-proof image owners*256MiB plus original capture/copy/hash/read/write histories; fixed proof/read attempts remain enforced by actual owners. Partial/unreturned internals separately allowed.", 516*MiB, 514*MiB, residentcost.BoundsData{Descriptors: 40, NativePending: 64, IOBytes: 16 << 30, RetainedReceiptBytes: MiB})
	add("root-four-phase-protocol", "original-parent-child-control", "Two roles*four phases*16KiB wire+canonical/defensive copies and32 bounded IO/timer records per phase inside1MiB memory. One actual core child/control pair; sixteen tasks include bounded timer/wait/drain attempts. These records are not joins or future worker funding.", MiB, 0, residentcost.BoundsData{Tasks: 16, Descriptors: 2, Sockets: 2, NativePending: 32, IOBytes: 8 * MiB, RetainedReceiptBytes: MiB})
	add("worker-native-issuer", "original-worker-native-owner", "Explicit own header roster plus actual native maxOwnedFDs4 and validationWaiters64.1MiB reserves fixed native/canonical/identity record backing; driver/runtime/poller/lock internals remain quantified unknowns. No legacy issuer or reconstructed closing witness.", MiB, 0, residentcost.BoundsData{Descriptors: 4, NativePending: 64, IOBytes: MiB, RetainedReceiptBytes: MiB})
	const sqlCells = 3 * 8 * 13 * 12 * 4097
	if sqlCells+3*8*13*12*uint64(unsafe.Sizeof(""))+3*8*13*uint64(unsafe.Sizeof([]string{})) > 17*MiB {
		return residentcost.PendingSourceContractData{}, errResidentWorkerChildOriginal
	}
	add("official-one-connection-sql", "original-official-sqlite-connector", "Three original startup/prepared/current slots*8queries*13rows*12columns*4097bytes per bounded cell plus explicit string/row headers fit17MiB. One unwrapped official Connector/physical Connect and pool1/idle1, no replacement.64MiB logical SQL IO reserve; driver native/opaque IO is separately quantified rather than inferred from query counts.", 17*MiB, 0, residentcost.BoundsData{Tasks: 8, Descriptors: 4, NativePending: 8, IOBytes: 64 * MiB, RetainedReceiptBytes: MiB})
	add("database-wal-and-publication", "original-fresh-format5-database", "Fresh28DDL (22base+6worker), singleton/revision0/original generation and five empty worker tables.8MiB source metadata/backing plus64MiB provisional initial DB/WAL/SHM logical disk. Extra opaque growth separately charged. No loops/nonempty worker activation in this stage.", 8*MiB, 64*MiB, residentcost.BoundsData{Descriptors: 4, NativePending: 8, IOBytes: 64 * MiB, RetainedReceiptBytes: MiB})
	add("manager-initialization-and-observers", "original-manager-constructor", fmt.Sprintf("18initializers:sync/subpools/budgets/runs/cargo/gradle/lease_reviews/storage_exports/contacts/storage_pools/lease_clock/deliveries/policy_outbox/host_pressure/shutdown/budget_bindings/scheduling/workspaces. Then Snapshot,initializeCPU<=2samples,sampleMemory,sampleDisk<=1pending worker. Source monitor read cap:%dpublic calls/%dbytes;8configured storage pools max.32MiB source dynamic backing; additional helper/native/observer allocation and partial tasks separately quantified unknowns, no claim32MiB bounds allocator behavior.", calls, observerIO), 32*MiB, 0, residentcost.BoundsData{Tasks: 64, Descriptors: 64, NativePending: 64, IOBytes: observerIO, RetainedReceiptBytes: MiB})
	add("same-server-auth-and-status", "same-original-server-and-agent-status", fmt.Sprintf("Same original Server/Manager/pool/root context. AgentStatus declared own header%dbytes,18job slots;2providers/16queue,128KiB response/16KiB headers/12KiB prompt/2048tokens/32KiB fullHTML+JSON-finalization. Four8MiB input/decoded/store/publication buffers plus32MiB source expansion/metadata reserve. Disk EXPLICIT8MiB serialized state+8MiB simultaneous atomic temporary publication. Disabled default is nonzero. Provider/directory/auth/runtime native tails separately retained.", statusHeader), 64*MiB, 16*MiB, residentcost.BoundsData{Tasks: 32, Descriptors: 32, NativePending: 32, IOBytes: 64 * MiB, RetainedReceiptBytes: MiB})
	floor := residentservice.WorkerPendingProspectiveFloorData()
	add("partial-unknown-retention", "original-pending-and-prospective-contract-floor", "Complete fixed prospective v1 lifetime floor:128attempts/receipts,32response owners,2generations/32asset sessions; no recycling.64MiB backing also retains original failures/once histories and old-new copies.3MiB auxiliary source/native publication disk; input2MiB+service1MiB+launch2MiB+auxiliary3MiB metadata total8MiB. Actual runtime gate remains separate and unsupported; these numbers never activate a worker.", 64*MiB, 3*MiB, floor)
	for _, scope := range residentcost.PendingUnknownScopesData() {
		c.UnknownCardinalities = append(c.UnknownCardinalities, residentcost.UnknownCardinalityData{Scope: scope, RetainedOriginalAttempts: 1, Derivation: "One original startup, both parent/child overlap and all abnormal/unreturned tails retained; no retry, native closure or disappearance credit. Scope requires explicit numeric evidence-bound opaque allowance in the full original ledger; retained-partial scope also includes UNOBSERVED header/collection multiplicity beyond exact one-of-each compiled type sizes, not an asserted512/header reachable maximum."})
	}
	if _, err = c.FingerprintData(); err != nil {
		return residentcost.PendingSourceContractData{}, err
	}
	return c, nil
}
