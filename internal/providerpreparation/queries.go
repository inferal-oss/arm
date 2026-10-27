package providerpreparation

// Proposed context-bound raw reads. Every actual call must use QueryContext or
// QueryRowContext with the original caller bound and a bounded read deadline.
// The fresh Manager clock sample follows these reads under the Manager fence.
// The +1 sentinel and exact byte length detect truncation; SQL run must be NULL.
const ReadLeaseSQL = `SELECT id,typeof(id),substr(CAST(request_key AS BLOB),1,129),typeof(request_key),run IS NULL,typeof(data),length(CAST(data AS BLOB)),substr(CAST(data AS BLOB),1,12289) FROM leases WHERE id=?`

const ReadBudgetSQL = `SELECT lease,typeof(lease),typeof(data),length(CAST(data AS BLOB)),substr(CAST(data AS BLOB),1,4097) FROM lease_budget_bindings WHERE lease=?`

// Header scan precedes body allocation. A 129th record or aggregate >4MiB makes
// accounting unavailable. Numeric projections return only an INTEGER or a
// NULL sentinel, plus the original typeof flag: never pass a corrupt TEXT/BLOB
// numeric value to the driver. Future readers require type "integer", a valid
// nullable int64 and the exact profile/range; NULL is not a zero reservation.
// Readers still retain bounded readable reservation hints, but must not derive
// availability from an incomplete scan.
const ReadHeadersSQL = `SELECT substr(CAST(id AS BLOB),1,33),typeof(id),substr(CAST(operation_key AS BLOB),1,129),typeof(operation_key),substr(CAST(authority AS BLOB),1,33),typeof(authority),substr(CAST(owner AS BLOB),1,11),typeof(owner),substr(CAST(profile AS BLOB),1,33),typeof(profile),CASE WHEN typeof(generation)='integer' THEN generation ELSE NULL END,typeof(generation),substr(CAST(state AS BLOB),1,17),typeof(state),substr(CAST(worker AS BLOB),1,33),typeof(worker),substr(CAST(temp AS BLOB),1,33),typeof(temp),CASE WHEN typeof(cpu_millis)='integer' THEN cpu_millis ELSE NULL END,typeof(cpu_millis),CASE WHEN typeof(memory_bytes)='integer' THEN memory_bytes ELSE NULL END,typeof(memory_bytes),CASE WHEN typeof(disk_bytes)='integer' THEN disk_bytes ELSE NULL END,typeof(disk_bytes),CASE WHEN typeof(revision)='integer' THEN revision ELSE NULL END,typeof(revision),CASE WHEN typeof(record_bytes)='integer' THEN record_bytes ELSE NULL END,typeof(record_bytes),length(CAST(initial_data AS BLOB)),length(CAST(progress_data AS BLOB)) FROM provider_preparations ORDER BY id LIMIT 129`

const ReadBodySQL = `SELECT substr(CAST(initial_sha256 AS BLOB),1,65),typeof(initial_sha256),typeof(initial_data),length(CAST(initial_data AS BLOB)),substr(CAST(initial_data AS BLOB),1,57345),typeof(progress_data),length(CAST(progress_data AS BLOB)),substr(CAST(progress_data AS BLOB),1,4097) FROM provider_preparations WHERE id=?`

// Member generation uses the same integer/NULL and original-type contract.
const ReadMembersSQL = `SELECT substr(CAST(lease AS BLOB),1,33),typeof(lease),substr(CAST(preparation AS BLOB),1,33),typeof(preparation),substr(CAST(role AS BLOB),1,17),typeof(role),CASE WHEN typeof(generation)='integer' THEN generation ELSE NULL END,typeof(generation) FROM provider_preparation_members ORDER BY lease LIMIT 257`

// Marker-only progression leaves both lease rows immutable. ExecuteContext must
// bind every expected field, require RowsAffected==1 and commit event+marker in
// the same transaction. Publish the in-memory successor only on a positive ACK;
// uncertainty revokes eligibility and does not accept a new raw row as expected.
const ProgressCASSQL = `UPDATE provider_preparations SET state=?,progress_data=?,revision=? WHERE id=? AND operation_key=? AND authority=? AND owner=? AND profile=? AND generation=? AND state=? AND worker=? AND temp=? AND cpu_millis=? AND memory_bytes=? AND disk_bytes=? AND initial_data=? AND initial_sha256=? AND progress_data=? AND revision=? AND record_bytes=?`
