package manager

// Exact existing base schema; only the original fresh owner calls this list.
var residentManagerBaseSchema = []string{`CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS leases (id TEXT PRIMARY KEY, request_key TEXT UNIQUE, data TEXT NOT NULL, run TEXT)`,
	`CREATE TABLE IF NOT EXISTS events (sequence INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT NOT NULL, entity TEXT NOT NULL, at TEXT NOT NULL, data TEXT)`,
	`CREATE TABLE IF NOT EXISTS agents (id TEXT PRIMARY KEY, data TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS messages (id TEXT PRIMARY KEY, data TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS message_operations (key TEXT PRIMARY KEY, request TEXT NOT NULL, message TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS usage_incidents (id TEXT PRIMARY KEY, lease TEXT NOT NULL, resource TEXT NOT NULL, data TEXT NOT NULL)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS usage_incidents_open ON usage_incidents(lease,resource) WHERE json_extract(data,'$.state')<>'closed'`,
	`CREATE TABLE IF NOT EXISTS policy_outbox (message TEXT PRIMARY KEY, state TEXT NOT NULL, data TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS host_pressure_state (id TEXT PRIMARY KEY, data TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS host_pressure_outbox (message TEXT PRIMARY KEY, state TEXT NOT NULL, data TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS agent_inbox (sequence INTEGER PRIMARY KEY AUTOINCREMENT, recipient TEXT NOT NULL, registration TEXT NOT NULL, message TEXT NOT NULL, receipt TEXT NOT NULL, UNIQUE(recipient,registration,message))`,
	`CREATE INDEX IF NOT EXISTS agent_inbox_recipient ON agent_inbox(recipient,registration,sequence)`,
	`CREATE TABLE IF NOT EXISTS pools (id TEXT PRIMARY KEY, scope TEXT NOT NULL, host TEXT NOT NULL, name TEXT NOT NULL, data TEXT NOT NULL)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS pools_name ON pools(scope,host,name) WHERE json_extract(data,'$.state')<>'deleted'`,
	`CREATE TABLE IF NOT EXISTS pool_changes (sequence INTEGER PRIMARY KEY AUTOINCREMENT, pool TEXT NOT NULL, data TEXT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS pool_changes_pool ON pool_changes(pool,sequence)`,
	`CREATE TABLE IF NOT EXISTS pool_operations (key TEXT PRIMARY KEY, request TEXT NOT NULL, result TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS cancelled_requests (key TEXT PRIMARY KEY)`}
