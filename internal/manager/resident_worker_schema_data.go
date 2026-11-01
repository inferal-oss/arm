package manager

// Exact reviewed format5 DATA. Selection is only through an actual worker-native
// original Manager claim, never a workspace marker or legacy owner conversion.
const residentWorkerManagerWorkspaceFormat = "5"

var residentWorkerManagerSchema = [...]string{
	`CREATE TABLE resident_worker_core_state (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  generation TEXT NOT NULL CHECK(typeof(generation)='text' AND length(generation)=32),
  revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision>=0),
  phase TEXT NOT NULL CHECK(phase IN ('preparing','worker_pending','active','sealed','unknown')),
  active_epoch INTEGER CHECK(active_epoch IS NULL OR (typeof(active_epoch)='integer' AND active_epoch BETWEEN 1 AND 128)),
  activation_slot INTEGER CHECK(activation_slot IS NULL OR (typeof(activation_slot)='integer' AND activation_slot BETWEEN 1 AND 128))
)`,
	`CREATE TABLE resident_worker_packages (
  slot INTEGER PRIMARY KEY CHECK(slot BETWEEN 1 AND 2),
  package_generation TEXT NOT NULL UNIQUE CHECK(typeof(package_generation)='text' AND length(package_generation)=32),
  package_sha256 TEXT NOT NULL CHECK(typeof(package_sha256)='text' AND length(package_sha256)=64),
  manifest BLOB NOT NULL CHECK(typeof(manifest)='blob' AND length(manifest) BETWEEN 1 AND 65536),
  source_commitment BLOB NOT NULL CHECK(typeof(source_commitment)='blob' AND length(source_commitment) BETWEEN 1 AND 4096),
  phase TEXT NOT NULL CHECK(phase IN ('inspection_pending','staged','pinned','retained_assets','retirement_pending','unknown')),
  revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision>=0)
)`,
	`CREATE TABLE resident_worker_operations (
  slot INTEGER PRIMARY KEY CHECK(slot BETWEEN 1 AND 128),
  operation_key TEXT NOT NULL UNIQUE CHECK(typeof(operation_key)='text' AND length(CAST(operation_key AS BLOB)) BETWEEN 1 AND 128),
  request_sha256 TEXT NOT NULL CHECK(typeof(request_sha256)='text' AND length(request_sha256)=64),
  operation TEXT NOT NULL CHECK(operation IN ('launch','replace','stop')),
  resident_generation TEXT NOT NULL CHECK(typeof(resident_generation)='text' AND length(resident_generation)=32),
  phase TEXT NOT NULL CHECK(phase IN ('prepared','committed','effect_pending','completed','failed','unknown')),
  reserved_bytes INTEGER NOT NULL CHECK(typeof(reserved_bytes)='integer' AND reserved_bytes=266240),
  public_request BLOB NOT NULL CHECK(typeof(public_request)='blob' AND length(public_request)<=65536),
  typed_operation BLOB NOT NULL CHECK(typeof(typed_operation)='blob' AND length(typed_operation)<=65536),
  result BLOB CHECK(result IS NULL OR (typeof(result)='blob' AND length(result)<=131072)),
  metadata BLOB NOT NULL CHECK(typeof(metadata)='blob' AND length(metadata)<=4096)
)`,
	`CREATE TABLE resident_worker_attempts (
  attempt INTEGER PRIMARY KEY CHECK(attempt BETWEEN 1 AND 128),
  operation_slot INTEGER NOT NULL UNIQUE CHECK(operation_slot BETWEEN 1 AND 128),
  epoch INTEGER NOT NULL UNIQUE CHECK(epoch BETWEEN 1 AND 128),
  resident_generation TEXT NOT NULL CHECK(typeof(resident_generation)='text' AND length(resident_generation)=32),
  package_generation TEXT NOT NULL CHECK(typeof(package_generation)='text' AND length(package_generation)=32),
  package_sha256 TEXT NOT NULL CHECK(typeof(package_sha256)='text' AND length(package_sha256)=64),
  lease_id TEXT NOT NULL UNIQUE CHECK(typeof(lease_id)='text' AND length(lease_id) BETWEEN 1 AND 128),
  demand BLOB NOT NULL CHECK(typeof(demand)='blob' AND length(demand) BETWEEN 1 AND 65536),
  phase TEXT NOT NULL CHECK(phase IN ('reserved','start_pending','inactive','active','sealed','join_pending','settled','failed','unknown')),
  native_observation BLOB NOT NULL CHECK(typeof(native_observation)='blob' AND length(native_observation)<=4096),
  renewal_revision INTEGER NOT NULL CHECK(typeof(renewal_revision)='integer' AND renewal_revision>=0),
  renewal_state BLOB NOT NULL CHECK(typeof(renewal_state)='blob' AND length(renewal_state)<=4096),
  revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision>=0)
)`,
	`CREATE TABLE resident_worker_activations (
  operation_slot INTEGER PRIMARY KEY CHECK(operation_slot BETWEEN 1 AND 128),
  from_epoch INTEGER CHECK(from_epoch IS NULL OR from_epoch BETWEEN 1 AND 128),
  to_epoch INTEGER NOT NULL CHECK(to_epoch BETWEEN 1 AND 128),
  stop_revision INTEGER NOT NULL CHECK(typeof(stop_revision)='integer' AND stop_revision>=0),
  phase TEXT NOT NULL CHECK(phase IN ('prepared','old_sealed','old_joined','commit_pending','committed','published','probed','failed','unknown')),
  observation BLOB NOT NULL CHECK(typeof(observation)='blob' AND length(observation)<=4096)
)`,
	`CREATE TABLE resident_worker_asset_sessions (
  slot INTEGER PRIMARY KEY CHECK(slot BETWEEN 1 AND 32),
  session_commitment BLOB NOT NULL UNIQUE CHECK(typeof(session_commitment)='blob' AND length(session_commitment)=32),
  package_generation TEXT NOT NULL CHECK(typeof(package_generation)='text' AND length(package_generation)=32),
  resource_lease_id TEXT NOT NULL CHECK(typeof(resource_lease_id)='text' AND length(resource_lease_id) BETWEEN 1 AND 128),
  entitlement BLOB NOT NULL CHECK(typeof(entitlement)='blob' AND length(entitlement) BETWEEN 1 AND 4096),
  phase TEXT NOT NULL CHECK(phase IN ('active','expired','retirement_pending','retired','unknown'))
)`,
}

type workerReservedObjectData struct {
	kind, table, sql string
	nullSQL          bool
}

var workerReservedObjectsData = map[string]workerReservedObjectData{
	"resident_worker_core_state": {kind: "table", table: "resident_worker_core_state", sql: `CREATE TABLE resident_worker_core_state (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  generation TEXT NOT NULL CHECK(typeof(generation)='text' AND length(generation)=32),
  revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision>=0),
  phase TEXT NOT NULL CHECK(phase IN ('preparing','worker_pending','active','sealed','unknown')),
  active_epoch INTEGER CHECK(active_epoch IS NULL OR (typeof(active_epoch)='integer' AND active_epoch BETWEEN 1 AND 128)),
  activation_slot INTEGER CHECK(activation_slot IS NULL OR (typeof(activation_slot)='integer' AND activation_slot BETWEEN 1 AND 128))
)`},
	"resident_worker_packages": {kind: "table", table: "resident_worker_packages", sql: `CREATE TABLE resident_worker_packages (
  slot INTEGER PRIMARY KEY CHECK(slot BETWEEN 1 AND 2),
  package_generation TEXT NOT NULL UNIQUE CHECK(typeof(package_generation)='text' AND length(package_generation)=32),
  package_sha256 TEXT NOT NULL CHECK(typeof(package_sha256)='text' AND length(package_sha256)=64),
  manifest BLOB NOT NULL CHECK(typeof(manifest)='blob' AND length(manifest) BETWEEN 1 AND 65536),
  source_commitment BLOB NOT NULL CHECK(typeof(source_commitment)='blob' AND length(source_commitment) BETWEEN 1 AND 4096),
  phase TEXT NOT NULL CHECK(phase IN ('inspection_pending','staged','pinned','retained_assets','retirement_pending','unknown')),
  revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision>=0)
)`},
	"resident_worker_operations": {kind: "table", table: "resident_worker_operations", sql: `CREATE TABLE resident_worker_operations (
  slot INTEGER PRIMARY KEY CHECK(slot BETWEEN 1 AND 128),
  operation_key TEXT NOT NULL UNIQUE CHECK(typeof(operation_key)='text' AND length(CAST(operation_key AS BLOB)) BETWEEN 1 AND 128),
  request_sha256 TEXT NOT NULL CHECK(typeof(request_sha256)='text' AND length(request_sha256)=64),
  operation TEXT NOT NULL CHECK(operation IN ('launch','replace','stop')),
  resident_generation TEXT NOT NULL CHECK(typeof(resident_generation)='text' AND length(resident_generation)=32),
  phase TEXT NOT NULL CHECK(phase IN ('prepared','committed','effect_pending','completed','failed','unknown')),
  reserved_bytes INTEGER NOT NULL CHECK(typeof(reserved_bytes)='integer' AND reserved_bytes=266240),
  public_request BLOB NOT NULL CHECK(typeof(public_request)='blob' AND length(public_request)<=65536),
  typed_operation BLOB NOT NULL CHECK(typeof(typed_operation)='blob' AND length(typed_operation)<=65536),
  result BLOB CHECK(result IS NULL OR (typeof(result)='blob' AND length(result)<=131072)),
  metadata BLOB NOT NULL CHECK(typeof(metadata)='blob' AND length(metadata)<=4096)
)`},
	"resident_worker_attempts": {kind: "table", table: "resident_worker_attempts", sql: `CREATE TABLE resident_worker_attempts (
  attempt INTEGER PRIMARY KEY CHECK(attempt BETWEEN 1 AND 128),
  operation_slot INTEGER NOT NULL UNIQUE CHECK(operation_slot BETWEEN 1 AND 128),
  epoch INTEGER NOT NULL UNIQUE CHECK(epoch BETWEEN 1 AND 128),
  resident_generation TEXT NOT NULL CHECK(typeof(resident_generation)='text' AND length(resident_generation)=32),
  package_generation TEXT NOT NULL CHECK(typeof(package_generation)='text' AND length(package_generation)=32),
  package_sha256 TEXT NOT NULL CHECK(typeof(package_sha256)='text' AND length(package_sha256)=64),
  lease_id TEXT NOT NULL UNIQUE CHECK(typeof(lease_id)='text' AND length(lease_id) BETWEEN 1 AND 128),
  demand BLOB NOT NULL CHECK(typeof(demand)='blob' AND length(demand) BETWEEN 1 AND 65536),
  phase TEXT NOT NULL CHECK(phase IN ('reserved','start_pending','inactive','active','sealed','join_pending','settled','failed','unknown')),
  native_observation BLOB NOT NULL CHECK(typeof(native_observation)='blob' AND length(native_observation)<=4096),
  renewal_revision INTEGER NOT NULL CHECK(typeof(renewal_revision)='integer' AND renewal_revision>=0),
  renewal_state BLOB NOT NULL CHECK(typeof(renewal_state)='blob' AND length(renewal_state)<=4096),
  revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision>=0)
)`},
	"resident_worker_activations": {kind: "table", table: "resident_worker_activations", sql: `CREATE TABLE resident_worker_activations (
  operation_slot INTEGER PRIMARY KEY CHECK(operation_slot BETWEEN 1 AND 128),
  from_epoch INTEGER CHECK(from_epoch IS NULL OR from_epoch BETWEEN 1 AND 128),
  to_epoch INTEGER NOT NULL CHECK(to_epoch BETWEEN 1 AND 128),
  stop_revision INTEGER NOT NULL CHECK(typeof(stop_revision)='integer' AND stop_revision>=0),
  phase TEXT NOT NULL CHECK(phase IN ('prepared','old_sealed','old_joined','commit_pending','committed','published','probed','failed','unknown')),
  observation BLOB NOT NULL CHECK(typeof(observation)='blob' AND length(observation)<=4096)
)`},
	"resident_worker_asset_sessions": {kind: "table", table: "resident_worker_asset_sessions", sql: `CREATE TABLE resident_worker_asset_sessions (
  slot INTEGER PRIMARY KEY CHECK(slot BETWEEN 1 AND 32),
  session_commitment BLOB NOT NULL UNIQUE CHECK(typeof(session_commitment)='blob' AND length(session_commitment)=32),
  package_generation TEXT NOT NULL CHECK(typeof(package_generation)='text' AND length(package_generation)=32),
  resource_lease_id TEXT NOT NULL CHECK(typeof(resource_lease_id)='text' AND length(resource_lease_id) BETWEEN 1 AND 128),
  entitlement BLOB NOT NULL CHECK(typeof(entitlement)='blob' AND length(entitlement) BETWEEN 1 AND 4096),
  phase TEXT NOT NULL CHECK(phase IN ('active','expired','retirement_pending','retired','unknown'))
)`},
	"sqlite_autoindex_resident_worker_packages_1":       {kind: "index", table: "resident_worker_packages", nullSQL: true},
	"sqlite_autoindex_resident_worker_operations_1":     {kind: "index", table: "resident_worker_operations", nullSQL: true},
	"sqlite_autoindex_resident_worker_attempts_1":       {kind: "index", table: "resident_worker_attempts", nullSQL: true},
	"sqlite_autoindex_resident_worker_attempts_2":       {kind: "index", table: "resident_worker_attempts", nullSQL: true},
	"sqlite_autoindex_resident_worker_attempts_3":       {kind: "index", table: "resident_worker_attempts", nullSQL: true},
	"sqlite_autoindex_resident_worker_asset_sessions_1": {kind: "index", table: "resident_worker_asset_sessions", nullSQL: true},
}
