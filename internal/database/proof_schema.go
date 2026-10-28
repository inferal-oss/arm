package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode"

	"getarm.org/internal/model"
	"getarm.org/internal/syncservice"
)

// Reset proof supports exactly this writer schema. An unfamiliar table, trigger,
// view, index or altered constraint is not evidence that obligations disappeared.
// This catalog never migrates; supported subpool evidence is verified separately.
var resetSchema = []string{
	`CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	`CREATE TABLE leases (id TEXT PRIMARY KEY, request_key TEXT UNIQUE, data TEXT NOT NULL, run TEXT)`,
	`CREATE TABLE events (sequence INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT NOT NULL, entity TEXT NOT NULL, at TEXT NOT NULL, data TEXT)`,
	`CREATE TABLE agents (id TEXT PRIMARY KEY, data TEXT NOT NULL)`,
	`CREATE TABLE messages (id TEXT PRIMARY KEY, data TEXT NOT NULL)`,
	`CREATE TABLE message_operations (key TEXT PRIMARY KEY, request TEXT NOT NULL, message TEXT NOT NULL)`,
	`CREATE TABLE usage_incidents (id TEXT PRIMARY KEY, lease TEXT NOT NULL, resource TEXT NOT NULL, data TEXT NOT NULL)`,
	`CREATE UNIQUE INDEX usage_incidents_open ON usage_incidents(lease,resource) WHERE json_extract(data,'$.state')<>'closed'`,
	`CREATE TABLE policy_outbox (message TEXT PRIMARY KEY, state TEXT NOT NULL, data TEXT NOT NULL)`,
	`CREATE TABLE host_pressure_state (id TEXT PRIMARY KEY, data TEXT NOT NULL)`,
	`CREATE TABLE host_pressure_outbox (message TEXT PRIMARY KEY, state TEXT NOT NULL, data TEXT NOT NULL)`,
	`CREATE TABLE agent_inbox (sequence INTEGER PRIMARY KEY AUTOINCREMENT, recipient TEXT NOT NULL, registration TEXT NOT NULL, message TEXT NOT NULL, receipt TEXT NOT NULL, UNIQUE(recipient,registration,message))`,
	`CREATE INDEX agent_inbox_recipient ON agent_inbox(recipient,registration,sequence)`,
	`CREATE TABLE pools (id TEXT PRIMARY KEY, scope TEXT NOT NULL, host TEXT NOT NULL, name TEXT NOT NULL, data TEXT NOT NULL)`,
	`CREATE UNIQUE INDEX pools_name ON pools(scope,host,name) WHERE json_extract(data,'$.state')<>'deleted'`,
	`CREATE TABLE pool_changes (sequence INTEGER PRIMARY KEY AUTOINCREMENT, pool TEXT NOT NULL, data TEXT NOT NULL)`,
	`CREATE INDEX pool_changes_pool ON pool_changes(pool,sequence)`,
	`CREATE TABLE pool_operations (key TEXT PRIMARY KEY, request TEXT NOT NULL, result TEXT NOT NULL)`,
	`CREATE TABLE pool_lineage (pool TEXT PRIMARY KEY,data TEXT NOT NULL)`,
	`CREATE TABLE pool_claim_bindings (lease TEXT PRIMARY KEY,data TEXT NOT NULL)`,
	`CREATE TABLE cancelled_requests (key TEXT PRIMARY KEY)`,
	`CREATE TABLE budgets (id TEXT PRIMARY KEY, owner TEXT NOT NULL, parent TEXT, data TEXT NOT NULL)`,
	`CREATE UNIQUE INDEX budgets_root ON budgets(owner) WHERE parent IS NULL`,
	`CREATE INDEX budgets_parent ON budgets(parent)`,
	`CREATE TABLE budget_changes (sequence INTEGER PRIMARY KEY AUTOINCREMENT,budget TEXT NOT NULL,data TEXT NOT NULL)`,
	`CREATE INDEX budget_changes_budget ON budget_changes(budget,sequence)`,
	`CREATE TABLE budget_operations (key TEXT PRIMARY KEY,request TEXT NOT NULL,result TEXT NOT NULL)`,
	`CREATE TABLE lease_budget_bindings (lease TEXT PRIMARY KEY,data TEXT NOT NULL)`,
	`CREATE TABLE gradle_workloads (lease TEXT PRIMARY KEY, data TEXT NOT NULL)`,
	`CREATE TABLE storage_exports (id TEXT PRIMARY KEY, request_key TEXT UNIQUE NOT NULL, lease TEXT NOT NULL, state TEXT NOT NULL, request TEXT NOT NULL, data TEXT NOT NULL)`,
	`CREATE INDEX storage_exports_lease ON storage_exports(lease,state)`,
	`CREATE INDEX storage_exports_state ON storage_exports(state)`,
}

// A later Manager may install this optional pre-bind retention group. All
// objects must match exactly; any row remains an unsettled obligation until a
// pair-aware reset reader is reviewed. Older databases without the group stay
// readable for their original obligations.
var forwardUnixGuardResetSchema = []struct{ kind, name, table, ddl string }{
	{"table", "forward_unix_pairs", "forward_unix_pairs", `CREATE TABLE forward_unix_pairs (
	source_id TEXT PRIMARY KEY,
	storage_id TEXT NOT NULL UNIQUE,
	phase TEXT NOT NULL CHECK (phase IN ('planned','observed','bind_intent','bound','active','closing','uncertain','terminal')),
	source_key TEXT NOT NULL,
	storage_key TEXT NOT NULL,
	socket_name TEXT NOT NULL,
	disk_bytes INTEGER NOT NULL CHECK (disk_bytes > 0),
	data TEXT NOT NULL CHECK (json_valid(data))
)`},
	{"trigger", "forward_unix_source_update_guard", "leases", `CREATE TRIGGER forward_unix_source_update_guard
BEFORE UPDATE ON leases
WHEN CASE WHEN EXISTS (SELECT 1 FROM forward_unix_pairs p WHERE p.source_id=OLD.id AND p.phase IS NOT 'terminal')
THEN (NEW.id IS NOT OLD.id
     OR NEW.request_key IS NOT OLD.request_key
     OR NEW.request_key IS NOT (SELECT source_key FROM forward_unix_pairs WHERE source_id=OLD.id)
     OR json_extract(NEW.data,'$.id') IS NOT OLD.id
     OR json_extract(NEW.data,'$.host') IS NULL
     OR json_extract(NEW.data,'$.owner') IS NULL
     OR json_extract(NEW.data,'$.host') IS NOT json_extract(OLD.data,'$.host')
     OR json_extract(NEW.data,'$.owner') IS NOT json_extract(OLD.data,'$.owner')
     OR (json_extract(NEW.data,'$.state') IS NOT 'active'
         AND json_extract(NEW.data,'$.state') IS NOT 'provisioning'
         AND json_extract(NEW.data,'$.state') IS NOT 'ending'
         AND json_extract(NEW.data,'$.state') IS NOT 'needs_attention')
     OR json_extract(NEW.data,'$.request.storage_lease') IS NOT (SELECT storage_id FROM forward_unix_pairs WHERE source_id=OLD.id)
     OR json_extract(NEW.data,'$.request') IS NOT json_extract(OLD.data,'$.request')
     OR json_extract(NEW.data,'$.ports') IS NOT json_extract(OLD.data,'$.ports')) ELSE 0 END
BEGIN SELECT RAISE(ABORT,'unfinished forward Unix source pair retains charge'); END`},
	{"trigger", "forward_unix_storage_update_guard", "leases", `CREATE TRIGGER forward_unix_storage_update_guard
BEFORE UPDATE ON leases
WHEN CASE WHEN EXISTS (SELECT 1 FROM forward_unix_pairs p WHERE p.storage_id=OLD.id AND p.phase IS NOT 'terminal')
THEN (NEW.id IS NOT OLD.id
     OR NEW.request_key IS NOT OLD.request_key
     OR NEW.request_key IS NOT (SELECT storage_key FROM forward_unix_pairs WHERE storage_id=OLD.id)
     OR json_extract(NEW.data,'$.id') IS NOT OLD.id
     OR json_extract(NEW.data,'$.host') IS NULL
     OR json_extract(NEW.data,'$.owner') IS NULL
     OR json_extract(NEW.data,'$.host') IS NOT json_extract(OLD.data,'$.host')
     OR json_extract(NEW.data,'$.owner') IS NOT json_extract(OLD.data,'$.owner')
     OR (json_extract(NEW.data,'$.state') IS NOT 'active'
         AND json_extract(NEW.data,'$.state') IS NOT 'needs_attention')
     OR json_extract(NEW.data,'$.request') IS NOT json_extract(OLD.data,'$.request')
     OR json_extract(NEW.data,'$.artifact.identity') IS NOT json_extract(OLD.data,'$.artifact.identity')) ELSE 0 END
BEGIN SELECT RAISE(ABORT,'unfinished forward Unix storage pair retains charge'); END`},
	{"trigger", "forward_unix_source_delete_guard", "leases", `CREATE TRIGGER forward_unix_source_delete_guard
BEFORE DELETE ON leases
WHEN EXISTS (SELECT 1 FROM forward_unix_pairs p WHERE p.source_id=OLD.id AND p.phase IS NOT 'terminal')
BEGIN SELECT RAISE(ABORT,'unfinished forward Unix source pair retains charge'); END`},
	{"trigger", "forward_unix_storage_delete_guard", "leases", `CREATE TRIGGER forward_unix_storage_delete_guard
BEFORE DELETE ON leases
WHEN EXISTS (SELECT 1 FROM forward_unix_pairs p WHERE p.storage_id=OLD.id AND p.phase IS NOT 'terminal')
BEGIN SELECT RAISE(ABORT,'unfinished forward Unix storage pair retains charge'); END`},
}

const meshResetSchema = `CREATE TABLE mesh_operations (principal TEXT,key TEXT,operation TEXT,request TEXT,result TEXT,admission TEXT NOT NULL,PRIMARY KEY(principal,key))`
const syncLeaseBindingResetSchema = `CREATE TABLE sync_lease_bindings (lease TEXT PRIMARY KEY,key TEXT NOT NULL,side TEXT NOT NULL,volume TEXT NOT NULL,disk_bytes INTEGER NOT NULL)`

var syncResetSchema = map[string]struct{ kind, table, ddl string }{
	"sync_operations":      {"table", "sync_operations", syncservice.OperationsSchema},
	"sync_open_generation": {"index", "sync_operations", syncservice.OpenGenerationSchema},
	"sync_effects":         {"table", "sync_effects", syncservice.EffectsSchema},
	"sync_baselines":       {"table", "sync_baselines", syncservice.BaselinesSchema},
	"sync_workspaces":      {"table", "sync_workspaces", syncservice.WorkspacesSchema},
	"sync_lease_bindings":  {"table", "sync_lease_bindings", syncLeaseBindingResetSchema},
}

// Retain token boundaries: TEXT UNIQUE is two SQL tokens and must never
// compare equal to the permissive SQLite type name TEXTUNIQUE. Quoted tokens
// preserve their literal spelling (including escaped quote pairs). Comments
// remain tokens and cannot normalize into a reviewed schema.
func normalizedSchema(raw string) string {
	runes := []rune(raw)
	tokens := []string{}
	for i := 0; i < len(runes); {
		c := runes[i]
		if unicode.IsSpace(c) {
			i++
			continue
		}
		start := i
		if c == '\'' || c == '"' || c == '`' || c == '[' {
			close := c
			if c == '[' {
				close = ']'
			}
			i++
			for i < len(runes) {
				if runes[i] == close {
					i++
					if close != ']' && i < len(runes) && runes[i] == close {
						i++
						continue
					}
					break
				}
				i++
			}
			tokens = append(tokens, string(runes[start:i]))
			continue
		}
		if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '$' {
			i++
			for i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || runes[i] == '_' || runes[i] == '$') {
				i++
			}
			tokens = append(tokens, strings.ToLower(string(runes[start:i])))
			continue
		}
		tokens = append(tokens, string(c))
		i++
	}
	return strings.Join(tokens, "\x1f")
}

var resetAutoIndexes = map[string]int{
	"published_agents": 2, "published_agent_changes": 1, "routed_agent_links": 2,
	"routing_roots": 1, "routing_outbox": 2, "routing_inbox": 2,
	"routing_graph_issuance": 1, "routing_graph_reports": 1, "routing_graph_observations": 1,
	"proxy_runs": 2, "proxy_run_chunks": 1,
	"cargo_workloads": 1, "lease_reviews": 1, "lease_review_policies": 1,
	"pool_lineage": 1, "pool_claim_bindings": 1,
	"workspace_instances": 1, "workspace_directory_members": 1, "workspace_trusted_members": 1, "workspace_observations": 1, "workspace_operations": 1, "workspace_denies": 1, "workspace_clocks": 1,
	"metadata": 1, "leases": 2, "agents": 1, "messages": 1, "message_operations": 1, "usage_incidents": 1, "policy_outbox": 1, "host_pressure_state": 1, "host_pressure_outbox": 1, "agent_inbox": 1, "pools": 1, "pool_operations": 1, "cancelled_requests": 1, "budgets": 1, "budget_operations": 1, "lease_budget_bindings": 1, "gradle_workloads": 1, "storage_exports": 2, "mesh_operations": 1, "ssh_operations": 2, "ssh_key_fences": 2,
	"forward_unix_pairs": 2,
	"sync_operations":    1, "sync_effects": 1, "sync_baselines": 1, "sync_lease_bindings": 1, "sync_workspaces": 2,
}

func verifyResetSchema(ctx context.Context, tx *sql.Tx) (map[string][]string, error) {
	expected := map[string]bool{}
	for _, query := range resetSchema {
		expected[normalizedSchema(query)] = false
	}
	reviewObjects := map[string][3]string{}
	reviewSeen := map[string]bool{}
	forwardUnixObjects := map[string][3]string{}
	forwardUnixSeen := map[string]bool{}
	for _, object := range forwardUnixGuardResetSchema {
		forwardUnixObjects[normalizedSchema(object.ddl)] = [3]string{object.kind, object.name, object.table}
	}
	for i, q := range leaseReviewResetSchema {
		reviewObjects[normalizedSchema(q)] = leaseReviewObjectIdentity[i]
	}
	workspaceObjects := map[string]string{}
	for i, q := range workspaceResetSchema {
		workspaceObjects[normalizedSchema(q)] = workspaceResetTables[i]
	}
	routingObjects := map[string]string{}
	for i, q := range routingResetSchema {
		routingObjects[normalizedSchema(q)] = routingResetTables[i]
	}
	for i, q := range routingGraphResetSchema {
		routingObjects[normalizedSchema(q)] = routingGraphResetTables[i]
	}
	for i, q := range publishedResetSchema {
		routingObjects[normalizedSchema(q)] = publishedResetTables[i]
	}
	mesh := normalizedSchema(meshResetSchema)
	ssh := normalizedSchema(sshResetSchema)
	sshFence := normalizedSchema(sshFenceResetSchema)
	syncObjects := map[string]struct{ name, kind, table string }{}
	for name, object := range syncResetSchema {
		syncObjects[normalizedSchema(object.ddl)] = struct{ name, kind, table string }{name, object.kind, object.table}
	}
	syncSeen := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT substr(type,1,33),substr(name,1,129),substr(tbl_name,1,129),substr(sql,1,4097) FROM sqlite_schema ORDER BY name LIMIT 160`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	catalog := map[string][]string{}
	count := 0
	autoIndexes := map[string]bool{}
	for rows.Next() {
		count++
		var kind, name, table string
		var raw sql.NullString
		if err := rows.Scan(&kind, &name, &table, &raw); err != nil {
			return nil, err
		}
		if count > 159 || len(name) > 128 || len(table) > 128 || len(raw.String) > 4096 {
			return nil, resetSchemaError("reset schema catalog exceeds bound")
		}
		if !raw.Valid && kind == "index" {
			allowed := false
			for i := 1; i <= resetAutoIndexes[table]; i++ {
				if name == fmt.Sprintf("sqlite_autoindex_%s_%d", table, i) {
					allowed = true
				}
			}
			if !allowed || autoIndexes[name] {
				return nil, resetSchemaError("unknown/duplicate internal reset index %q", name)
			}
			autoIndexes[name] = true
			continue
		}

		if kind == "table" && name == "sqlite_sequence" && normalizedSchema(raw.String) == normalizedSchema(`CREATE TABLE sqlite_sequence(name,seq)`) {
			catalog[table] = nil
			continue
		}
		normal := normalizedSchema(raw.String)
		if identity, ok := forwardUnixObjects[normal]; ok {
			if kind != identity[0] || name != identity[1] || table != identity[2] || forwardUnixSeen[name] {
				return nil, resetSchemaError("forward Unix guard schema object identity differs")
			}
			forwardUnixSeen[name] = true
			if kind == "table" {
				catalog[table] = nil
			}
			continue
		}
		if object, ok := syncObjects[normal]; ok {
			if kind != object.kind || name != object.name || table != object.table || syncSeen[name] {
				return nil, resetSchemaError("sync schema object identity differs")
			}
			syncSeen[name] = true
			if kind == "table" {
				catalog[table] = nil
			}
			continue
		}
		if identity, ok := reviewObjects[normal]; ok {
			if kind != identity[0] || name != identity[1] || table != identity[2] {
				return nil, resetSchemaError("lease review schema object identity differs")
			}
			if reviewSeen[normal] {
				return nil, resetSchemaError("duplicate lease review schema object")
			}
			reviewSeen[normal] = true
			if kind == "table" {
				catalog[table] = nil
			}
			continue
		}
		if expectedTable, ok := routingObjects[normal]; ok {
			if kind != "table" || name != expectedTable || table != expectedTable {
				return nil, resetSchemaError("routing schema object identity differs")
			}
			catalog[expectedTable] = nil
			continue
		}
		if expectedTable, ok := workspaceObjects[normal]; ok {
			if kind != "table" || name != expectedTable || table != expectedTable {
				return nil, resetSchemaError("workspace schema object identity differs")
			}
			catalog[expectedTable] = nil
			continue
		}
		if normal == sshFence {
			if kind != "table" || name != "ssh_key_fences" || table != name {
				return nil, resetSchemaError("SSH fence schema identity differs")
			}
			continue
		}

		proxyObject := false
		for object, ddl := range proxyRunResetSchema {
			if normal == normalizedSchema(ddl) {
				if kind != "table" || name != object || table != object {
					return nil, resetSchemaError("proxy run schema object identity differs")
				}
				catalog[table] = nil
				proxyObject = true
				break
			}
		}
		if proxyObject {
			continue
		}
		if normal == normalizedSchema(cargoResetSchema) {
			if kind != "table" || name != "cargo_workloads" || table != "cargo_workloads" {
				return nil, resetSchemaError("Cargo optional schema identity differs")
			}
			catalog[table] = nil
			continue
		}
		if normal == mesh || normal == ssh {
			continue
		}
		if _, ok := expected[normal]; !ok {
			return nil, resetSchemaError("unsupported reset schema object %q (%s); no migration or destructive proof inferred", name, kind)
		}
		expected[normal] = true
		if kind == "table" {
			catalog[table] = nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Report trusted object identities in declaration order, never the internal
	// token-comparison keys (which contain control-character separators).
	missing := []string{}
	for _, query := range resetSchema {
		if !expected[normalizedSchema(query)] {
			fields := strings.Fields(query) // only the static reviewed DDL above
			if fields[1] == "UNIQUE" {
				fields = fields[1:]
			}
			missing = append(missing, fmt.Sprintf("%s %q", strings.ToLower(fields[1]), fields[2]))
		}
	}
	if len(missing) > 0 {
		return nil, resetSchemaError("required schema objects missing: %s", strings.Join(missing, ", "))
	}
	if len(reviewSeen) != 0 && len(reviewSeen) != len(leaseReviewResetSchema) {
		return nil, resetSchemaError("partial lease review schema group")
	}
	if len(forwardUnixSeen) != 0 && len(forwardUnixSeen) != len(forwardUnixGuardResetSchema) {
		return nil, resetSchemaError("partial forward Unix guard schema group")
	}
	// Version 1 predates workspace locators. Exact shape/version pairing is
	// checked by readSync after metadata; neither supported shape is migrated.
	legacySync := len(syncSeen) == len(syncResetSchema)-1 && !syncSeen["sync_workspaces"]
	if len(syncSeen) != 0 && len(syncSeen) != len(syncResetSchema) && !legacySync {
		return nil, resetSchemaError("partial sync reset schema group")
	}
	_, proxyRuns := catalog["proxy_runs"]
	_, proxyChunks := catalog["proxy_run_chunks"]
	if proxyRuns != proxyChunks {
		return nil, resetSchemaError("partial proxy run schema group")
	}
	workspaceCount := 0
	for _, table := range workspaceResetTables {
		if _, ok := catalog[table]; ok {
			workspaceCount++
		}
	}
	if workspaceCount != 0 && workspaceCount != len(workspaceResetTables) {
		return nil, resetSchemaError("partial workspace reset schema group")
	}
	routingCount := 0
	for _, table := range routingResetTables {
		if _, ok := catalog[table]; ok {
			routingCount++
		}
	}
	if routingCount != 0 && routingCount != len(routingResetTables) {
		return nil, resetSchemaError("partial routing reset schema group")
	}
	publicationCount := 0
	for _, table := range publishedResetTables {
		if _, ok := catalog[table]; ok {
			publicationCount++
		}
	}
	if publicationCount != 0 && (publicationCount != 3 || workspaceCount != len(workspaceResetTables)) {
		return nil, resetSchemaError("partial published agent reset schema group")
	}
	graphCount := 0
	for _, table := range routingGraphResetTables {
		if _, ok := catalog[table]; ok {
			graphCount++
		}
	}
	if graphCount != 0 && (graphCount != len(routingGraphResetTables) || routingCount != len(routingResetTables)) {
		return nil, resetSchemaError("partial routing graph reset schema group")
	}
	// Retrieve columns only after the exact schema/constraints have been checked.
	rows.Close()
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='mesh_operations'`).Scan(&count); err != nil {
		return nil, err
	}
	if count == 1 {
		catalog["mesh_operations"] = nil
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='ssh_operations'`).Scan(&count); err != nil {
		return nil, err
	}
	if count == 1 {
		catalog["ssh_operations"] = nil
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='ssh_key_fences'`).Scan(&count); err != nil {
		return nil, err
	}
	if count == 1 {
		catalog["ssh_key_fences"] = nil
	}
	for table := range catalog {
		for i := 1; i <= resetAutoIndexes[table]; i++ {
			name := fmt.Sprintf("sqlite_autoindex_%s_%d", table, i)
			if !autoIndexes[name] {
				return nil, resetSchemaError("required implicit reset constraint index missing: %s", name)
			}
		}
	}

	for table := range catalog {
		cols, err := tx.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
		if err != nil {
			return nil, err
		}
		for cols.Next() {
			var seq, notnull, pk int
			var name, kind string
			var value any
			if err = cols.Scan(&seq, &name, &kind, &notnull, &value, &pk); err != nil {
				cols.Close()
				return nil, err
			}
			catalog[table] = append(catalog[table], name)
		}
		err = cols.Err()
		cols.Close()
		if err != nil {
			return nil, err
		}
	}
	return catalog, nil
}

// Schema refusal is a supported outcome with a stable machine code. The proof
// never upgrades a ledger, and missing objects alone cannot identify its origin
// or establish that older native obligations are settled.
func resetSchemaError(format string, args ...any) error {
	return model.Err("database_schema_unsupported", fmt.Sprintf(format, args...)+
		". The ledger schema is incomplete or incompatible with this ARM build; removal is not authorized and no schema repair was attempted."+
		" Preserve the state directory; use arm database check and arm database backup DESTINATION for diagnosis and a consistent backup."+
		" Use the ARM build that created the ledger to inspect and resolve obligations, or choose a separate empty --home for a fresh installation."+
		" Ordinary removal cannot migrate or remove incompatible schemas. To intentionally lose this ledger without proving cleanup, review arm database remove --forget --dry-run.")
}
