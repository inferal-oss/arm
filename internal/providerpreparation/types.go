package providerpreparation

const (
	SchemaVersion = 1
	SchemaKey     = "provider_preparation_schema"
	SchemaValue   = "1"
	InertProfile  = "inert-pair-v1"

	MaxLivePairs        = 4
	MaxJournalRows      = 128
	MaxMembers          = 2 * MaxJournalRows
	MaxParentCharges    = 16
	MaxLeaseBytes       = 12 << 10
	MaxBudgetBytes      = 4 << 10
	MaxInitialBytes     = 56 << 10
	MaxProgressBytes    = 4 << 10
	RecordReserveBytes  = 104 << 10
	MaxAggregateBytes   = 4 << 20
	MaxProgressRevision = 3
	MaxPairEvents       = 3
	MaxEventBytes       = 512
	MaxEnvelopeDepth    = 8
	MaxLeaseDepth       = 16

	WorkerCPUMillis   = 1000
	WorkerMemoryBytes = 64 << 20
	TempDiskBytes     = 64 << 20
	LifetimeNS        = 300_000_000_000
)

// Scalar domains: Hex32 is exactly 32 lowercase hex bytes; Digest is 64;
// Decimal is canonical nonnegative ASCII decimal <= MaxInt64 with <=19 bytes;
// UID is canonical decimal <= MaxUint32 with <=10 bytes; Key is 1..128 ASCII
// bytes from [A-Za-z0-9._:-]; Stamp is canonical UTC RFC3339Nano, <=40 bytes.
// Fields called revision/generation must be positive. None is an authority seal.
type Hex32 string
type Digest string
type Decimal string
type UID string
type Key string
type Stamp string

type Role string

const (
	Worker Role = "worker"
	Temp   Role = "temp"
)

// LeaseRef is the proposed exact Lease-node field. Omission is distinct from
// null/false/malformed presence. A default reset/SSH proof must refuse ANY raw
// presence before decoding. This data can never authorize cleanup or execution.
type LeaseRef struct {
	Schema     int     `json:"schema"`
	Pair       Hex32   `json:"pair"`
	Role       Role    `json:"role"`
	Generation Decimal `json:"generation"`
}

type Amounts struct {
	CPUMillis   Decimal `json:"cpu_millis"`
	MemoryBytes Decimal `json:"memory_bytes"`
	DiskBytes   Decimal `json:"disk_bytes"`
}

// RowRef always requires SQL run IS NULL. SQL id/request_key must equal these
// values AND the embedded Lease.ID/Request.Key. DataSHA256 hashes exact stored
// bytes, never a get/list result with transient usage attached.
type RowRef struct {
	ID           Hex32   `json:"id"`
	RequestKey   Key     `json:"request_key"`
	RunKind      string  `json:"run_kind"` // literal "sql-null"
	DataSHA256   Digest  `json:"data_sha256"`
	Revision     Decimal `json:"revision"`
	ClockSHA256  Digest  `json:"clock_sha256"`
	BudgetSHA256 Digest  `json:"budget_sha256"`
}

// Byte slices encode as standard padded base64, preserving protected raw bytes
// exactly. Embedding RawMessage would not preserve their whitespace/escaping.
// LeaseBytes <=12288 decoded bytes; BudgetBytes <=4096. Decode limits apply
// before allocation. Budget row SQL lease key must equal Row.ID.
type OriginalMember struct {
	Role        Role   `json:"role"`
	Row         RowRef `json:"row"`
	LeaseBytes  []byte `json:"lease_bytes"`
	BudgetBytes []byte `json:"budget_bytes"`
}

// ParentCharge is a bounded snapshot of the already resolved budget ancestry,
// not a new budget/cost policy. The inert profile admits no custom pool claims.
// At most 16 unique budget IDs, exact path/revision and sidecar-byte bindings.
type ParentCharge struct {
	Budget   Key     `json:"budget"` // includes the existing budget_owner_ digest namespace
	Revision Decimal `json:"revision"`
	Binding  Digest  `json:"binding_sha256"`
	Amounts  Amounts `json:"amounts"`
}

// Initial is immutable. Members[0] is worker and Members[1] is temp. They have
// different IDs, the same owner/authority/pair/epoch and exact role quantities.
// There is no registration, executable, path, credentials, RunRequest or native
// result. Full Deadline/Expires values are protected by the raw lease snapshots
// and ClockSHA256; no new clock parser or serialized remaining duration exists.
type Initial struct {
	Schema       int               `json:"schema"`
	Pair         Hex32             `json:"pair"`
	OperationKey Key               `json:"operation_key"`
	Authority    Hex32             `json:"authority"`
	Owner        UID               `json:"owner"`
	OpenEpoch    Hex32             `json:"open_epoch"`
	Profile      string            `json:"profile"` // literal InertProfile
	Created      Stamp             `json:"created_at"`
	LifetimeNS   Decimal           `json:"lifetime_ns"`
	DiskScope    string            `json:"disk_scope"` // literal "state-filesystem"; no custom volume/artifact
	Members      [2]OriginalMember `json:"members"`
	Parents      [16]*ParentCharge `json:"parents"` // trailing nil slots only; duplicate IDs refused
}

type Phase string

const (
	Preparing Phase = "preparing"
	Revoked   Phase = "revoked"
	Unknown   Phase = "unknown"
)

// Progress has only monotone retaining states. All three consume both members.
// Marker advancement does not change Initial or either member's stored bytes.
// Allowed: preparing/1 -> revoked/2 or unknown/2; revoked/2 -> unknown/3.
// Unknown is absorbing; repeated requests inspect without a write/event.
// No finalized/startable/settled state is supported in this version.
type Progress struct {
	Schema     int     `json:"schema"`
	Pair       Hex32   `json:"pair"`
	Generation Decimal `json:"generation"`
	Revision   Decimal `json:"revision"`
	Phase      Phase   `json:"phase"`
	Reason     string  `json:"reason"` // "none", "cancelled", "expired", "clock", "close", "persistence", "binding"
	At         Stamp   `json:"at"`
}

// Accounting is proposed display data, not admission or settlement authority.
// State is "exact" or "unavailable". RetainedFloor contains only bounded,
// readable stated promises; it is never usable as available capacity when the
// state is unavailable. Global headroom/admission must be fenced in that case.
type Accounting struct {
	Schema        int     `json:"schema"`
	State         string  `json:"state"`
	RetainedFloor Amounts `json:"retained_floor"`
	PairCount     int     `json:"pair_count"`
	ErrorCode     string  `json:"error_code"` // "none" or "provider_preparation_unavailable"
}
