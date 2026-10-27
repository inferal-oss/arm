package providerpreparation

// Proposed writer declarations only. Nothing executes these statements or adds
// them to Manager.Open. The central independent literal catalog is an un-applied
// review patch. All four named objects + their two exact PK autoindexes form one
// optional group, with metadata provider_preparation_schema="1".
const TableDDL = `CREATE TABLE provider_preparations (
id TEXT PRIMARY KEY NOT NULL CHECK(typeof(id)='text' AND length(CAST(id AS BLOB))=32),
operation_key TEXT NOT NULL CHECK(typeof(operation_key)='text' AND length(CAST(operation_key AS BLOB)) BETWEEN 1 AND 128),
authority TEXT NOT NULL CHECK(typeof(authority)='text' AND length(CAST(authority AS BLOB))=32),
owner TEXT NOT NULL CHECK(typeof(owner)='text' AND length(CAST(owner AS BLOB)) BETWEEN 1 AND 10),
profile TEXT NOT NULL CHECK(profile='inert-pair-v1'),
generation INTEGER NOT NULL CHECK(typeof(generation)='integer' AND generation=1),
state TEXT NOT NULL CHECK(state IN ('preparing','revoked','unknown')),
worker TEXT NOT NULL CHECK(typeof(worker)='text' AND length(CAST(worker AS BLOB))=32),
temp TEXT NOT NULL CHECK(typeof(temp)='text' AND length(CAST(temp AS BLOB))=32 AND worker<>temp),
cpu_millis INTEGER NOT NULL CHECK(typeof(cpu_millis)='integer' AND cpu_millis=1000),
memory_bytes INTEGER NOT NULL CHECK(typeof(memory_bytes)='integer' AND memory_bytes=67108864),
disk_bytes INTEGER NOT NULL CHECK(typeof(disk_bytes)='integer' AND disk_bytes=67108864),
initial_data TEXT NOT NULL CHECK(typeof(initial_data)='text' AND length(CAST(initial_data AS BLOB)) BETWEEN 1 AND 57344),
initial_sha256 TEXT NOT NULL CHECK(typeof(initial_sha256)='text' AND length(CAST(initial_sha256 AS BLOB))=64),
progress_data TEXT NOT NULL CHECK(typeof(progress_data)='text' AND length(CAST(progress_data AS BLOB)) BETWEEN 1 AND 4096),
revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision BETWEEN 1 AND 3),
record_bytes INTEGER NOT NULL CHECK(typeof(record_bytes)='integer' AND record_bytes=106496)
)`

const OperationIndexDDL = `CREATE UNIQUE INDEX provider_preparations_operation_key ON provider_preparations(operation_key)`

const MembersDDL = `CREATE TABLE provider_preparation_members (
lease TEXT PRIMARY KEY NOT NULL CHECK(typeof(lease)='text' AND length(CAST(lease AS BLOB))=32),
preparation TEXT NOT NULL CHECK(typeof(preparation)='text' AND length(CAST(preparation AS BLOB))=32),
role TEXT NOT NULL CHECK(role IN ('worker','temp')),
generation INTEGER NOT NULL CHECK(typeof(generation)='integer' AND generation=1),
FOREIGN KEY(preparation) REFERENCES provider_preparations(id) ON DELETE RESTRICT
)`

const RolesIndexDDL = `CREATE UNIQUE INDEX provider_preparation_roles ON provider_preparation_members(preparation,role)`

// Foreign-key activation is not assumed: exact reciprocal membership is always
// validated in the same transaction. There are no triggers/views/cascades or
// supported row-deletion/settlement statements.
