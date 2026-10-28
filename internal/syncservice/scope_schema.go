package syncservice

// ChargedScopeSchema is the immutable version-3 original-scope row shape.
// It is installed only in a fresh, dedicated sync namespace. A row is
// evidence for recovery, never a reconstructed execution capability.
const ChargedScopeSchema = `CREATE TABLE sync_charged_scopes (key TEXT NOT NULL,side TEXT NOT NULL CHECK(side IN ('local','remote')),preparation_lease TEXT NOT NULL,preparation_revision INTEGER NOT NULL CHECK(preparation_revision>0),preparation_data TEXT NOT NULL,host TEXT NOT NULL,owner TEXT NOT NULL,provider TEXT NOT NULL CHECK(provider='xfs-project-quota'),scope_id TEXT NOT NULL,mount_id TEXT NOT NULL,project_id INTEGER NOT NULL CHECK(project_id>0),project_root_id TEXT NOT NULL,selected_root_id TEXT NOT NULL,home_root_id TEXT NOT NULL,owner_epoch INTEGER NOT NULL CHECK(owner_epoch>0),enrollment_digest TEXT NOT NULL,ownership_digest TEXT NOT NULL,charged_hard_bytes INTEGER NOT NULL CHECK(charged_hard_bytes>0),initial_used_bytes INTEGER NOT NULL CHECK(initial_used_bytes>=0),state TEXT NOT NULL CHECK(state IN ('preparing','admitted','settling','settled','unresolved')),row_revision INTEGER NOT NULL CHECK(row_revision>0),final_lease TEXT,PRIMARY KEY(key,side))`

const ChargedScopePreparationIndex = `CREATE UNIQUE INDEX sync_charged_scopes_preparation ON sync_charged_scopes(preparation_lease)`
const ChargedScopeProjectIndex = `CREATE UNIQUE INDEX sync_charged_scopes_project ON sync_charged_scopes(provider,mount_id,project_id)`
const ChargedScopeFinalIndex = `CREATE UNIQUE INDEX sync_charged_scopes_final ON sync_charged_scopes(final_lease)`
