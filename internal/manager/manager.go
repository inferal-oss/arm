package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/observe"
	"github.com/yrashk/arm/internal/ports"
	"github.com/yrashk/arm/internal/storage"
	"github.com/yrashk/arm/internal/usage"
	_ "modernc.org/sqlite"
)

type RunRequest struct {
	model.Request
	Command    []string                `json:"command"`
	CWD        string                  `json:"cwd"`
	SSH        *model.SSHRunDescriptor `json:"ssh,omitempty"`
	sshRuntime *sshRuntime
}
type job struct {
	cmd              *exec.Cmd
	done             chan struct{}
	identity         *model.ProcessIdentity
	observationError string
	lastUsage        *model.WorkloadUsage
}
type Manager struct {
	mu                 sync.Mutex
	db                 *sql.DB
	databaseSlot       chan struct{}
	home, host, owner  string
	budgetStartupError *model.Error
	poolStartupError   *model.Error
	ownerUsername      string
	capacity           model.Resources
	jobs               map[string]*job
	stop               chan struct{}
	wg                 sync.WaitGroup
	closing            bool
	draining           bool
	shutdownDeadline   time.Time
	observationMu      sync.RWMutex
	observations       map[string]model.ResourceMeasurement
	memoryAvailability model.MemoryAvailability
	memoryObserved     time.Time
	diskAvailability   model.DiskAvailability
	diskObserved       time.Time
	diskScopeVolume    string
	diskSampleSlots    chan struct{}
	diskCollector      func(context.Context, string) model.DiskAvailability
	storage            *storage.Provider
	storageErr         error
	storageJobs        map[string]bool
	storageExportSlots chan struct{}
	portSockets        map[string][]*ports.Socket
	portNamespace      string
	portErr            error
	infrastructure     []model.PortBinding
	inboxChanged       chan struct{}
	inboxSlots         chan struct{}
	usages             map[string]workloadSample
	policyClocks       map[string]incidentClock
	startClocks        map[string]time.Time
	scheduleHighwater  time.Time
	scheduleCheckpoint time.Time
	scheduleRollback   bool
	scheduleWallNow    func() time.Time
	scheduleElapsedNow func() time.Time
}

var capabilities = []string{"cooperative-reservation", "process-group-supervision", "durable-events", "host-observation", "custom-resource-pools", "pool-capacity-journal", "local-global-pool-authority", "bulk-lease-actions", "draining-shutdown", "local-owner-policy-budgets", "counted-resource-subpools"}

func Open(home string, capacity model.Resources) (*Manager, error) {
	if capacity.Memory < 0 || capacity.Disk < 0 {
		return nil, model.Err("invalid_config", "memory/disk ceilings must be nonnegative; zero selects live availability")
	}
	if e := local.Prepare(home); e != nil {
		return nil, e
	}
	fresh, e := prepareDatabaseFile(home)
	if e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	if !fresh {
		if _, e = database.StartupAuthority(context.Background(), home, db); e != nil {
			db.Close()
			return nil, e
		}
	}
	for _, q := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS leases (id TEXT PRIMARY KEY, request_key TEXT UNIQUE, data TEXT NOT NULL, run TEXT)`,
		`CREATE TABLE IF NOT EXISTS events (sequence INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT NOT NULL, entity TEXT NOT NULL, at TEXT NOT NULL, data TEXT)`,
		`CREATE TABLE IF NOT EXISTS agents (id TEXT PRIMARY KEY, data TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS messages (id TEXT PRIMARY KEY, data TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS message_operations (key TEXT PRIMARY KEY, request TEXT NOT NULL, message TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS usage_incidents (id TEXT PRIMARY KEY, lease TEXT NOT NULL, resource TEXT NOT NULL, data TEXT NOT NULL)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS usage_incidents_open ON usage_incidents(lease,resource) WHERE json_extract(data,'$.state')<>'closed'`,
		`CREATE TABLE IF NOT EXISTS policy_outbox (message TEXT PRIMARY KEY, state TEXT NOT NULL, data TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS agent_inbox (sequence INTEGER PRIMARY KEY AUTOINCREMENT, recipient TEXT NOT NULL, registration TEXT NOT NULL, message TEXT NOT NULL, receipt TEXT NOT NULL, UNIQUE(recipient,registration,message))`,
		`CREATE INDEX IF NOT EXISTS agent_inbox_recipient ON agent_inbox(recipient,registration,sequence)`,
		`CREATE TABLE IF NOT EXISTS pools (id TEXT PRIMARY KEY, scope TEXT NOT NULL, host TEXT NOT NULL, name TEXT NOT NULL, data TEXT NOT NULL)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS pools_name ON pools(scope,host,name) WHERE json_extract(data,'$.state')<>'deleted'`,
		`CREATE TABLE IF NOT EXISTS pool_changes (sequence INTEGER PRIMARY KEY AUTOINCREMENT, pool TEXT NOT NULL, data TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS pool_changes_pool ON pool_changes(pool,sequence)`,
		`CREATE TABLE IF NOT EXISTS pool_operations (key TEXT PRIMARY KEY, request TEXT NOT NULL, result TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS cancelled_requests (key TEXT PRIMARY KEY)`} {
		if _, e = db.Exec(q); e != nil {
			db.Close()
			return nil, e
		}
	}
	if fresh {
		if _, e = db.Exec(`INSERT INTO metadata VALUES ('host',?)`, local.ID()); e != nil {
			db.Close()
			return nil, e
		}
	}
	m := &Manager{db: db, home: home, capacity: capacity, jobs: map[string]*job{}, stop: make(chan struct{}), storageJobs: map[string]bool{}, portSockets: map[string][]*ports.Socket{}}
	m.diskSampleSlots = make(chan struct{}, 1)
	m.usages = map[string]workloadSample{}
	m.policyClocks = map[string]incidentClock{}
	m.inboxChanged = make(chan struct{})
	m.inboxSlots = make(chan struct{}, 64)
	m.portNamespace, m.portErr = ports.Namespace()
	if e = db.QueryRow(`SELECT value FROM metadata WHERE key='host'`).Scan(&m.host); e != nil {
		db.Close()
		return nil, e
	}
	identity := local.CurrentIdentity()
	m.owner, m.ownerUsername = identity.UID, identity.Username
	if e = m.initSubpoolRecords(); e != nil && !m.degradePoolStartup(e) {
		db.Close()
		return nil, e
	}
	if e = m.initBudgetRecords(); e != nil && !m.degradeBudgetStartup(e) {
		db.Close()
		return nil, e
	}
	if e = m.initGradleRecords(); e != nil {
		db.Close()
		return nil, e
	}
	if e = m.initStorageExports(); e != nil {
		db.Close()
		return nil, e
	}
	if e = m.migrateContacts(); e != nil {
		db.Close()
		return nil, e
	}
	m.loadStorage()
	opened := false
	defer func() {
		if !opened && m.storage != nil {
			m.storage.Close()
		}
	}()
	// A recovered PID alone never authorizes signalling. Keep uncertain jobs charged.
	leaves, e := m.list()
	if e != nil {
		db.Close()
		return nil, e
	}
	for _, l := range leaves {
		if l.Usage != nil {
			l.Usage.Live = false
			l.Usage.Stale = true
		}
		var run sql.NullString
		if e = db.QueryRow(`SELECT run FROM leases WHERE id=?`, l.ID).Scan(&run); e != nil {
			db.Close()
			return nil, e
		}
		if consuming(l.State) && len(l.Request.Ports) > 0 {
			l.State = "needs_attention"
			if l.Outcome == "" {
				l.Outcome = "port_supervision_lost"
			}
			l.Failure = "Socket descriptors did not survive restart; reconcile actual availability before returning this allocation"
			for i := range l.Ports {
				l.Ports[i].State = "unknown"
				l.Ports[i].SocketID = ""
				l.Ports[i].Detail = l.Failure
			}
			l.Revision++
			if e = m.put(l, "ports.recovery_required"); e != nil {
				db.Close()
				return nil, e
			}
		} else if run.Valid && consuming(l.State) {
			l.State = "needs_attention"
			l.Outcome = "supervision_lost"
			if !m.gradleObligation(l) && (l.PID == 0 || errors.Is(syscall.Kill(-l.PID, 0), syscall.ESRCH)) {
				now := time.Now().UTC()
				l.State = "terminal"
				l.Outcome = "interrupted"
				l.Ended = &now
			}
			l.Revision++
			if e = m.put(l, "lease.recovery_required"); e != nil {
				db.Close()
				return nil, e
			}
		}
	}
	if e = m.recoverDeliveries(); e != nil {
		db.Close()
		return nil, e
	}
	if e = m.recoverPolicyOutbox(); e != nil {
		db.Close()
		return nil, e
	}
	m.sample()
	m.sampleMemory()
	m.sampleDisk()
	if e = m.recoverShutdown(); e != nil {
		db.Close()
		return nil, e
	}
	if m.budgetStartupError == nil {
		if e = m.migrateBudgetBindings(); e != nil && !m.degradeBudgetStartup(e) {
			db.Close()
			return nil, e
		}
	}
	if e = m.initScheduling(); e != nil {
		db.Close()
		return nil, e
	}
	opened = true
	return m, nil
}

func (m *Manager) Start() {
	m.wg.Go(func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-ticker.C:
				m.sampleDisk()
			}
		}
	})
	m.wg.Go(func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-ticker.C:
				m.sampleMemory()
			}
		}
	})
	m.wg.Go(m.sampleUsageLoop)
	m.wg.Go(m.policyDeliveryLoop)
	m.wg.Go(func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-t.C:
				m.sample()
			}
		}
	})
	m.wg.Go(func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-t.C:
				m.tick()
			}
		}
	})
}

func (m *Manager) sample() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	observations := observe.Snapshot(ctx, m.home)
	m.observationMu.Lock()
	m.observations = observations
	m.observationMu.Unlock()
}

// Observation returns an immutable cached physical reading without admission
// or database work. The monitor reuses filesystem topology/capacity probes.
func (m *Manager) Observation(kind string) model.ResourceMeasurement {
	m.observationMu.RLock()
	defer m.observationMu.RUnlock()
	return m.observations[kind]
}
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closing = true
	for id := range m.jobs {
		_, _ = m.end(id, "cancelled")
	}
	m.mu.Unlock()
	close(m.stop)
	m.wg.Wait()
	for _, sockets := range m.portSockets {
		ports.Close(sockets)
	}
	if m.storage != nil {
		m.storage.Close()
	}
	return m.db.Close()
}
func consuming(s string) bool {
	return s == "active" || s == "provisioning" || s == "ending" || s == "needs_attention"
}
func (m *Manager) list() ([]model.Lease, error) {
	rows, e := m.db.Query(`SELECT data FROM leases ORDER BY rowid`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Lease{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var l model.Lease
		if e = json.Unmarshal(b, &l); e != nil {
			return nil, e
		}
		m.attachUsage(&l)
		out = append(out, l)
	}
	return out, rows.Err()
}
func (m *Manager) get(id string) (model.Lease, error) {
	var l model.Lease
	var b []byte
	e := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, id).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return l, model.Err("not_found", "lease does not exist")
	}
	if e != nil {
		return l, e
	}
	e = json.Unmarshal(b, &l)
	if e == nil {
		m.attachUsage(&l)
	}
	return l, e
}
func event(tx *sql.Tx, kind, id string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO events(type,entity,at,data) VALUES(?,?,?,?)`, kind, id, time.Now().UTC().Format(time.RFC3339Nano), string(b))
	return e
}
func (m *Manager) put(l model.Lease, kind string) error {
	b, e := json.Marshal(l)
	if e != nil {
		return e
	}
	tx, e := m.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`UPDATE leases SET data=? WHERE id=?`, string(b), l.ID); e != nil {
		return e
	}
	if e = event(tx, kind, l.ID, l); e != nil {
		return e
	}
	return tx.Commit()
}

func (m *Manager) Status() (model.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, e := m.list()
	s := model.Status{PoolError: m.poolPolicyError(), BudgetError: m.budgetPolicyError(), Host: m.host, Owner: m.owner, OwnerUsername: m.ownerUsername, Draining: m.draining, Capacity: m.capacity, Leases: ls, Capabilities: append(append([]string{}, capabilities...), "observed-usage-policy", "sender-aware-agent-messaging", "owned-gradle-services"), Infrastructure: append([]model.PortBinding{}, m.infrastructure...)}
	if m.portErr == nil {
		s.Capabilities = append(append([]string{}, s.Capabilities...), "port-reservation", "socket-activation", "native-port-inventory")
	}
	if e != nil {
		return s, e
	}
	if e = m.queueViews(ls); e != nil {
		return s, e
	}
	for i := range ls {
		m.attachBudgetEvidence(&ls[i])
		m.attachPoolEvidence(&ls[i])
	}
	for _, l := range ls {
		if consuming(l.State) {
			s.Reserved = s.Reserved.Add(l.Request.Resources)
		}
	}
	diskReserved, err := diskPromises(ls)
	if err != nil {
		return s, err
	}
	s.Reserved.Disk = diskReserved
	memory := m.memoryBudget(s.Reserved.Memory)
	s.Memory = &memory
	s.Capacity.Memory = memory.Capacity
	disk := m.diskBudget(s.Reserved.Disk)
	s.Disk = &disk
	s.Capacity.Disk = disk.Capacity
	s.Capabilities = append(s.Capabilities, "live-memory-admission", "live-disk-admission")
	for _, kind := range []string{"cpu", "memory", "disk"} {
		total, reserved, unit := s.Capacity.CPU, s.Reserved.CPU, "logical-core-equivalents"
		if kind == "memory" {
			total, reserved, unit = float64(s.Capacity.Memory), float64(s.Reserved.Memory), "bytes"
		}
		if kind == "disk" {
			total, reserved, unit = float64(s.Capacity.Disk), float64(s.Reserved.Disk), "bytes"
		}
		v := model.ResourceView{ID: m.host + "/" + kind, Host: m.host, Pool: m.host + "/" + kind + "-budget", Kind: kind, Unit: unit, BudgetTotal: model.FloatQuantity(total), Reserved: model.FloatQuantity(reserved), Available: model.FloatQuantity(max(0, total-reserved)), Accounting: "cooperative-reservation-budget", Users: []model.ResourceUse{}}
		if kind == "memory" {
			v.BudgetTotal = model.CountQuantity(s.Capacity.Memory)
			v.Reserved = model.CountQuantity(s.Reserved.Memory)
			v.Available = model.CountQuantity(max(0, s.Capacity.Memory-s.Reserved.Memory))
			v.Deficit = model.CountQuantity(memory.Deficit)
			v.Accounting = "live-host-memory-headroom; full outstanding promises charged conservatively"
		}
		if kind == "disk" {
			v.BudgetTotal = model.CountQuantity(s.Capacity.Disk)
			v.Reserved = model.CountQuantity(s.Reserved.Disk)
			v.Available = model.CountQuantity(disk.Available)
			v.Deficit = model.CountQuantity(disk.Deficit)
			v.Accounting = disk.Accounting
		}
		m.observationMu.RLock()
		measurement, exists := m.observations[kind]
		m.observationMu.RUnlock()
		if exists {
			v.Measurement = &measurement
			v.Observed = measurement.Used.Value
		}
		for _, l := range ls {
			if !consuming(l.State) && !(kind == "disk" && l.State == "reclaiming") {
				continue
			}
			q := l.Request.Resources.CPU
			if kind == "memory" {
				q = float64(l.Request.Resources.Memory)
			}
			if kind == "disk" {
				q = float64(l.Request.Resources.Disk)
			}
			if q > 0 {
				quantity := model.FloatQuantity(q)
				if kind == "memory" {
					quantity = model.CountQuantity(l.Request.Resources.Memory)
				}
				if kind == "disk" {
					quantity = model.CountQuantity(l.Request.Resources.Disk)
				}
				v.Users = append(v.Users, model.ResourceUse{Lease: l.ID, Owner: l.Owner, OwnerUsername: l.OwnerUsername, Agent: l.Request.Agent, Project: l.Request.Project, Reason: l.Request.Reason, Quantity: quantity, State: l.State, Expires: l.Expires})
			}
		}
		s.Resources = append(s.Resources, v)
	}
	m.portResources(&s, ls)
	views, err := m.poolViews(ls, false)
	if err != nil {
		if s.PoolError == nil {
			return s, err
		}
		views = nil
	}
	for _, v := range views {
		view := v
		s.Resources = append(s.Resources, model.ResourceView{ID: v.Pool.ID, Host: v.Pool.Host, Pool: v.Pool.ID, Kind: "custom", Unit: v.Pool.Unit, BudgetTotal: model.CountQuantity(v.Pool.Capacity), Reserved: model.CountQuantity(v.Reserved), Available: model.CountQuantity(v.Available), Accounting: "cooperative-counted-permits", Users: v.Users, Custom: &view})
	}
	e = m.db.QueryRow(`SELECT COALESCE(MAX(sequence),0) FROM events`).Scan(&s.Cursor)
	return s, e
}
func (m *Manager) Inspect(id string) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, err := m.get(id)
	if err == nil {
		m.attachBudgetEvidence(&l)
		m.attachPoolEvidence(&l)
	}
	if err != nil || l.State != "queued" {
		return l, err
	}
	ls, err := m.list()
	if err != nil {
		return l, err
	}
	if err = m.queueViews(ls); err != nil {
		return l, err
	}
	for _, current := range ls {
		if current.ID == id {
			m.attachBudgetEvidence(&current)
			m.attachPoolEvidence(&current)
			return current, nil
		}
	}
	return l, nil
}

func (m *Manager) Acquire(r model.Request, run *RunRequest) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.acquire(r, run)
}

func (m *Manager) acquire(r model.Request, run *RunRequest) (model.Lease, error) {
	var empty model.Lease
	if run != nil && (run.SSH != nil || run.sshRuntime != nil) && (run.SSH == nil || run.sshRuntime == nil || !reflect.DeepEqual(run.SSH, sshDescriptor(run.sshRuntime.request)) || run.sshRuntime.check() != nil) {
		return empty, model.Err("invalid_request", "a persisted/wire SSH descriptor cannot authorize execution")
	}
	if m.closing || m.draining {
		return empty, model.Err("backend_stopping", "backend is shutting down")
	}
	if err := canonicalStartDeadline(&r); err != nil {
		return empty, err
	}
	if err := m.reconcileQueued(); err != nil {
		return empty, err
	}
	if len(r.Key) > 128 {
		return empty, model.Err("invalid_request", "idempotency key must be at most 128 bytes")
	}
	if r.Host != "" && r.Host != "localhost" && r.Host != m.host {
		return empty, model.Err("host_unavailable", "only the local host is registered")
	}
	if r.Budget != "" && !budgetText(r.Budget, 128, true) {
		return empty, model.Err("invalid_request", "policy selector must be a bounded ID or self")
	}
	if strings.TrimSpace(r.Reason) == "" || len(r.Reason) > 4096 {
		return empty, model.Err("invalid_request", "reason is required and must be at most 4096 bytes")
	}
	if r.TTL <= 0 || r.TTL > 7*24*time.Hour {
		return empty, model.Err("invalid_request", "ttl must be positive and no greater than 7 days")
	}
	var portErr error
	r.Pools, portErr = m.normalizePoolClaims(r.Pools)
	if portErr != nil {
		return empty, portErr
	}
	if len(r.Ports) > 0 {
		if m.portErr != nil {
			return empty, model.Err("namespace_unavailable", m.portErr.Error())
		}
		r.Ports, portErr = ports.Normalize(r.Ports, m.portNamespace, run != nil)
		if portErr != nil {
			return empty, portErr
		}
	} else {
		r.Ports = nil
	}
	if math.IsNaN(r.Resources.CPU) || math.IsInf(r.Resources.CPU, 0) || r.Resources.CPU < 0 || r.Resources.Memory < 0 || r.Resources.Disk < 0 || (r.Resources == (model.Resources{}) && len(r.Ports) == 0 && r.PortLease == "" && len(r.Pools) == 0) {
		return empty, model.Err("invalid_request", "request positive CPU/memory/disk, at least one port, an attached port lease, or counted pool claims")
	}
	if r.Kind != "" && r.Kind != "execution" && r.Kind != "storage" && r.Kind != "ports" {
		return empty, model.Err("invalid_request", "kind must be execution, storage or ports")
	}
	if r.Kind == "storage" && (len(r.Pools) > 0 || run != nil || r.Resources.CPU != 0 || r.Resources.Memory != 0 || r.Resources.Disk <= 0 || r.Profile != "" || r.StorageLease != "" || len(r.Ports) > 0 || r.PortLease != "") {
		return empty, model.Err("invalid_request", "a storage lease requires only a disk claim and no command, profile or parent storage lease")
	}
	if r.Kind == "ports" && (len(r.Pools) > 0 || run != nil || r.Resources != (model.Resources{}) || len(r.Ports) == 0 || r.PortLease != "" || r.Profile != "" || r.StorageLease != "") {
		return empty, model.Err("invalid_request", "a ports lease requires only port claims and no command, profile or parent lease")
	}
	if r.PortLease != "" && (run == nil || len(r.Ports) > 0) {
		return empty, model.Err("invalid_request", "a supervised run can attach one port lease or request its own ports")
	}
	if r.StorageLease != "" && run == nil {
		return empty, model.Err("invalid_request", "only supervised commands can attach storage")
	}
	if r.Profile != "" && !SupportsProfile(r.Profile) {
		return empty, model.Err("unsupported_profile", "supported profiles: rust-build, gradle-service, gradle-build")
	}
	if r.Agent != "" {
		var count int
		if e := m.db.QueryRow(`SELECT COUNT(*) FROM agents WHERE id=?`, r.Agent).Scan(&count); e != nil {
			return empty, e
		}
		if count == 0 {
			return empty, model.Err("invalid_contact", "register the agent contact before using it for attribution")
		}
	}
	if err := m.resolveUsagePolicy(&r, run != nil); err != nil {
		return empty, err
	}
	if r.Key != "" {
		var old []byte
		var savedRun sql.NullString
		e := m.db.QueryRow(`SELECT data,run FROM leases WHERE request_key=?`, r.Key).Scan(&old, &savedRun)
		if e == nil {
			var l model.Lease
			if e = json.Unmarshal(old, &l); e != nil {
				return empty, e
			}
			a, b := r, l.Request
			a.Host = ""
			b.Host = ""
			a.Budget = m.canonicalBudgetSelector(a.Budget)
			b.Budget = m.canonicalBudgetSelector(b.Budget)
			a.PeerCertificate, b.PeerCertificate = "", ""
			a.PeerGrantRevision, b.PeerGrantRevision = 0, 0
			if !reflect.DeepEqual(a, b) {
				return empty, model.Err("idempotency_conflict", "key was already used for a different request")
			}
			var oldRun RunRequest
			if savedRun.Valid {
				if e = json.Unmarshal([]byte(savedRun.String), &oldRun); e != nil {
					return empty, e
				}
			}
			if savedRun.Valid != (run != nil) || (run != nil && (!reflect.DeepEqual(oldRun.Command, run.Command) || oldRun.CWD != run.CWD || !reflect.DeepEqual(oldRun.SSH, run.SSH))) {
				return empty, model.Err("idempotency_conflict", "key was already used for a different command")
			}
			m.attachBudgetEvidence(&l)
			m.attachPoolEvidence(&l)
			return l, nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return empty, e
		}
		var cancelled int
		if e = m.db.QueryRow(`SELECT COUNT(*) FROM cancelled_requests WHERE key=?`, r.Key).Scan(&cancelled); e != nil {
			return empty, e
		}
		if cancelled > 0 {
			return empty, model.Err("request_cancelled", "this request was cancelled before admission")
		}
	}
	if err := m.poolPolicyError(); err != nil {
		return empty, err
	}
	if err := m.budgetPolicyError(); err != nil {
		return empty, err
	}
	if err := m.validateGradleRequest(r, run); err != nil {
		return empty, err
	}
	if !m.requestFitsCeiling(r.Resources) {
		return empty, model.Err("capacity_exceeded", "request exceeds a configured host ceiling or known physical memory capacity")
	}
	if err := m.poolAdmissionAncestry(r.Pools); err != nil {
		return empty, err
	}
	if r.StorageLease != "" {
		if _, e := m.storageForRun(r); e != nil {
			return empty, e
		}
	}
	if r.PortLease != "" {
		if _, e := m.portParent(r, ""); e != nil {
			return empty, e
		}
	}
	r.Budget = m.canonicalBudgetSelector(r.Budget)
	if run != nil {
		copyRun := *run
		copyRun.Request = r
		run = &copyRun
	}
	ls, e := m.list()
	if e != nil {
		return empty, e
	}
	var used model.Resources
	queued := false
	for _, l := range ls {
		if consuming(l.State) {
			used = used.Add(l.Request.Resources)
		}
		if l.State == "queued" {
			queued = true
		}
	}
	diskReserved, e := diskPromises(ls)
	if e != nil {
		return empty, e
	}
	used.Disk = diskReserved
	l := model.Lease{ID: local.ID(), Host: m.host, Owner: m.owner, OwnerUsername: m.ownerUsername, Request: r, Created: time.Now().UTC(), State: "queued", Revision: 1, Capabilities: capabilities}
	binding, e := m.resolveNewBudgetBinding(l, r.Budget)
	if e != nil {
		return empty, e
	}
	l.Budget = &binding
	charges, _, e := m.budgetCharges(ls)
	if e != nil {
		return empty, e
	}
	want, e := m.requestedBudgetAmounts(r)
	if e != nil {
		return empty, e
	}
	budgetBlockers, policies, e := m.budgetAdmissionBlockers(binding, want, charges, false)
	if e != nil {
		return empty, e
	}
	l.BudgetPolicy = policies
	poolUsed, e := poolReservations(ls)
	if e != nil {
		return empty, e
	}
	poolFits, e := m.poolsFit(r.Pools, poolUsed)
	if e != nil {
		return empty, e
	}
	deadlineProblem := m.startDeadlineProblem(l)
	if deadlineProblem != nil {
		l.State = "terminal"
		l.Outcome = "rejected"
		stamp, _ := m.schedulingTime()
		l.Ended = &stamp
		l.QueueInfo = rejectedQueueInfo(stamp, deadlineProblem)
		l.Failure = deadlineProblem.Message
	} else if !queued && m.fitsLiveResources(r.Resources, used) && poolFits && len(budgetBlockers) == 0 {
		m.activate(&l, run != nil)
	} else if !r.Queue {
		return empty, model.Err("insufficient_capacity", "host capacity or policy ceilings are committed, or live memory/disk headroom is insufficient/unavailable; use --wait to queue")
	}
	var rb any
	if run != nil {
		if len(run.Command) == 0 || !filepath.IsAbs(run.CWD) {
			return empty, model.Err("invalid_request", "run needs a command and absolute cwd")
		}
		if r.Profile != "" {
			if e = profileValidators[r.Profile](run.Command, r.Resources.CPU); e != nil {
				return empty, e
			}
		}
		if r.Profile == "rust-build" && r.StorageLease != "" {
			for _, arg := range run.Command[1:] {
				if arg == "--target-dir" || strings.HasPrefix(arg, "--target-dir=") || arg == "--build-dir" || strings.HasPrefix(arg, "--build-dir=") {
					return empty, model.Err("profile_conflict", "attached storage supplies CARGO_TARGET_DIR; remove target-dir overrides")
				}
			}
		}
		b, _ := json.Marshal(run)
		rb = string(b)
	}
	poolTree, bindErr := m.checkedPoolTree()
	if bindErr != nil {
		return empty, bindErr
	}
	poolBinding, bindErr := m.newPoolClaimBinding(poolTree, r.Pools)
	if bindErr != nil {
		return empty, bindErr
	}
	if len(poolBinding.Claims) > 0 {
		l.PoolBinding = &poolBinding
	}
	b, _ := json.Marshal(l)
	tx, e := m.db.Begin()
	if e != nil {
		return empty, e
	}
	defer tx.Rollback()
	var key any
	if r.Key != "" {
		key = r.Key
	}
	if _, e = tx.Exec(`INSERT INTO leases VALUES(?,?,?,?)`, l.ID, key, string(b), rb); e != nil {
		return empty, e
	}
	if e = m.insertSSHOperation(tx, l, run); e != nil {
		return empty, e
	}
	if e = insertPoolClaimBinding(tx, l.ID, poolBinding, "pool.bound"); e != nil {
		return empty, e
	}
	if e = insertBudgetBinding(tx, l.ID, binding, "budget.bound"); e != nil {
		return empty, e
	}
	if e = event(tx, "lease.created", l.ID, l); e != nil {
		return empty, e
	}
	if r.StartDeadline != nil {
		if _, e = tx.Exec(`INSERT INTO metadata(key,value) VALUES('scheduler_clock',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, m.scheduleHighwater.Format(time.RFC3339Nano)); e != nil {
			return empty, e
		}
	}
	if deadlineProblem != nil {
		if e = event(tx, "lease.rejected", l.ID, l); e != nil {
			return empty, e
		}
	}
	if e = tx.Commit(); e != nil {
		return empty, e
	}
	if r.StartDeadline != nil {
		m.scheduleCheckpoint = m.scheduleHighwater
	}
	if l.State == "active" {
		if reason := m.startDeadlineProblem(l); reason != nil {
			return m.rejectUnstarted(l, reason)
		}
		delete(m.startClocks, l.ID)
	}
	if l.State == "provisioning" {
		if len(r.Ports) > 0 {
			l, e = m.provisionPorts(l, run, ls)
		} else if r.Kind == "storage" {
			l, e = m.provisionStorage(l)
		} else {
			l, e = m.launch(l, *run)
		}
	}
	if e == nil && l.State == "queued" {
		if e = m.refreshQueueInfo(); e == nil {
			l, e = m.get(l.ID)
		}
	}
	return l, e
}

func (m *Manager) activate(l *model.Lease, isRun bool) {
	now, _ := m.schedulingTime()
	l.QueueInfo = nil
	end := now.Add(l.Request.TTL)
	l.Started = &now
	l.Expires = &end
	l.State = "active"
	if isRun || l.Request.Kind == "storage" || len(l.Request.Ports) > 0 {
		l.State = "provisioning"
	}
}

func (m *Manager) End(id, outcome string) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.end(id, outcome)
}

// Retain cancellation even when it races ahead of an unacknowledged request.
func (m *Manager) CancelRequest(key string) error {
	if key == "" || len(key) > 128 {
		return model.Err("invalid_request", "a valid request key is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, e := m.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`INSERT OR IGNORE INTO cancelled_requests VALUES(?)`, key); e != nil {
		return e
	}
	if e = event(tx, "request.cancelled", key, nil); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	var id string
	e = m.db.QueryRow(`SELECT id FROM leases WHERE request_key=?`, key).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	_, e = m.end(id, "cancelled")
	return e
}
func (m *Manager) end(id, outcome string) (model.Lease, error) {
	l, e := m.get(id)
	if e != nil {
		return l, e
	}
	if l.State == "terminal" || l.State == "ending" {
		return l, nil
	}
	if l.State == "needs_attention" {
		return l, model.Err("needs_attention", "allocation remains charged pending reconciliation; storage leases support explicit cleanup retry or handoff")
	}
	if l.Request.Kind == "storage" {
		return m.endStorage(l, outcome)
	}
	if l.Request.Kind == "ports" && l.State != "queued" {
		return m.endPortParent(l, outcome)
	}
	if m.gradleObligation(l) {
		if m.jobs[id] == nil {
			return m.reconcileGradle(l, outcome)
		}
		if err := m.endGradleConsumers(l, outcome); err != nil {
			return l, err
		}
	}
	l.Revision++
	l.Outcome = outcome
	if j := m.jobs[id]; j != nil {
		l.State = "ending"
		if e = m.put(l, "lease.ending"); e != nil {
			return l, e
		}
		_ = syscall.Kill(-j.cmd.Process.Pid, syscall.SIGTERM)
		pid := j.cmd.Process.Pid
		m.wg.Go(func() {
			select {
			case <-j.done:
				return
			case <-time.After(2 * time.Second):
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.jobs[id] == j {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		})
		return l, nil
	}
	now := time.Now().UTC()
	l.State = "terminal"
	l.Ended = &now
	if len(l.Ports) > 0 {
		m.finishPorts(&l)
	}
	return l, m.put(l, "lease."+outcome)
}

func (m *Manager) Renew(id string, ttl time.Duration) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.renew(id, ttl)
}
func (m *Manager) renew(id string, ttl time.Duration) (model.Lease, error) {
	l, e := m.get(id)
	if e != nil {
		return l, e
	}
	if err := m.budgetPolicyError(); err != nil {
		return l, err
	}
	if m.draining {
		return l, model.Err("backend_draining", "renewal is paused while backend shutdown drains existing work")
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		return l, model.Err("invalid_request", "invalid renewal ttl")
	}
	if l.State != "active" || l.Expires == nil || !time.Now().Before(*l.Expires) {
		return l, model.Err("lease_inactive", "only an unexpired active lease can be renewed")
	}
	if err := m.gradleRenew(l, ttl); err != nil {
		return l, err
	}
	if l.Request.StorageLease != "" {
		r := l.Request
		r.TTL = ttl
		if _, err := m.storageForRun(r); err != nil {
			return l, err
		}
	}
	if l.Request.PortLease != "" {
		r := l.Request
		r.TTL = ttl
		if _, err := m.portParent(r, l.ID); err != nil {
			return l, err
		}
	}
	ls, e := m.list()
	if e != nil {
		return l, e
	}
	if err := m.diskRenewal(ls, l); err != nil {
		return l, err
	}
	policies, e := m.budgetRenewal(l, ls)
	if e != nil {
		return l, e
	}
	if l.Request.Resources.Memory > 0 {
		var reserved int64
		for _, lease := range ls {
			if consuming(lease.State) {
				reserved += lease.Request.Resources.Memory
			}
		}
		if budget := m.memoryBudget(reserved); budget.Stale || budget.Deficit > 0 || budget.Observation.Available == nil {
			return l, model.Err("renewal_denied", "live memory headroom cannot cover outstanding promises")
		}
	}
	if e = m.poolRenewal(l, ls); e != nil {
		return l, e
	}
	for _, o := range ls {
		if o.State == "queued" {
			return l, model.Err("renewal_denied", "queued work has priority over renewal")
		}
	}
	end := time.Now().UTC().Add(ttl)
	if end.Before(*l.Expires) {
		return l, model.Err("invalid_request", "renewal must extend the current expiry")
	}
	l.Expires = &end
	m.attachBudgetEvidence(&l)
	m.attachPoolEvidence(&l)
	l.BudgetPolicy = policies
	if l.Request.Kind == "storage" && l.Artifact != nil {
		// An explicitly renewed deadline supersedes a prior shortening policy;
		// the earlier retention rationale remains in its durable event.
		l.Artifact.Retention = nil
	}
	l.Revision++
	return l, m.put(l, "lease.renewed")
}

func (m *Manager) Events(after int64) ([]model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows, e := m.db.Query(`SELECT sequence,type,entity,at,data FROM events WHERE sequence>? ORDER BY sequence LIMIT 256`, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Event{}
	for rows.Next() {
		var ev model.Event
		var at string
		var data string
		if e = rows.Scan(&ev.Sequence, &ev.Type, &ev.Entity, &at, &data); e != nil {
			return nil, e
		}
		ev.Data = json.RawMessage(data)
		ev.At, e = time.Parse(time.RFC3339Nano, at)
		if e != nil {
			return nil, e
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (m *Manager) tick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return
	}
	ls, e := m.list()
	if e != nil {
		return
	}
	m.tickGradleWorkloads(ls)
	now := time.Now()
	for _, l := range ls {
		if (l.State == "active" || l.State == "provisioning") && l.Expires != nil && !now.Before(*l.Expires) {
			_, _ = m.end(l.ID, "expired")
		}
	}
	if err := m.reconcileIncidents(); err != nil {
		return
	}
	if err := m.reconcileQueued(); err != nil {
		return
	}
	ls, e = m.list()
	if e != nil {
		return
	}
	var used model.Resources
	for _, l := range ls {
		if consuming(l.State) {
			used = used.Add(l.Request.Resources)
		}
	}
	diskReserved, e := diskPromises(ls)
	if e != nil {
		return
	}
	used.Disk = diskReserved
	for _, l := range ls {
		if l.Request.Kind == "storage" && l.State == "ending" {
			m.scheduleStorage(l.ID)
		}
		if l.Request.Kind == "ports" && l.State == "ending" {
			_, _ = m.finishPortParent(l)
		}
	}
	if m.draining || m.budgetStartupError != nil || m.poolStartupError != nil {
		return
	}
	poolUsed, e := poolReservations(ls)
	if e != nil {
		return
	}
	for _, l := range ls {
		if l.State != "queued" {
			continue
		}
		if l.Request.StorageLease != "" {
			if _, err := m.storageForRun(l.Request); err != nil {
				_, _ = m.end(l.ID, "storage_unavailable")
				continue
			}
		}
		if l.Request.PortLease != "" {
			if _, err := m.portParent(l.Request, l.ID); err != nil {
				_, _ = m.end(l.ID, "ports_unavailable")
				continue
			}
		}
		poolFits, err := m.poolsFit(l.Request.Pools, poolUsed)
		if err != nil {
			return
		}
		budgetFits, policies, err := m.budgetQueuedFit(l)
		if err != nil {
			return
		}
		if !budgetFits || !m.fitsLiveResources(l.Request.Resources, used) || !poolFits {
			break
		}
		if reason := m.startDeadlineProblem(l); reason != nil {
			if _, e = m.rejectUnstarted(l, reason); e != nil {
				return
			}
			continue
		}
		var rb sql.NullString
		if e = m.db.QueryRow(`SELECT run FROM leases WHERE id=?`, l.ID).Scan(&rb); e != nil {
			return
		}
		m.attachBudgetEvidence(&l)
		m.attachPoolEvidence(&l)
		l.BudgetPolicy = policies
		if len(l.Request.Ports) == 0 {
			m.activate(&l, rb.Valid)
			l.Revision++
			if e = m.put(l, "lease.granted"); e != nil {
				return
			}
		}
		if !rb.Valid && l.Request.Kind != "storage" && len(l.Request.Ports) == 0 {
			if reason := m.startDeadlineProblem(l); reason != nil {
				if _, e = m.rejectUnstarted(l, reason); e != nil {
					return
				}
				continue
			}
			delete(m.startClocks, l.ID)
		}
		if rb.Valid {
			var r RunRequest
			if e = json.Unmarshal([]byte(rb.String), &r); e == nil {
				if len(l.Request.Ports) > 0 {
					l, e = m.provisionPorts(l, &r, ls)
				} else {
					l, e = m.launch(l, r)
				}
			}
		} else if l.Request.Kind == "storage" {
			l, e = m.provisionStorage(l)
		} else if len(l.Request.Ports) > 0 {
			l, e = m.provisionPorts(l, nil, ls)
		}
		if e != nil {
			return
		}
		if l.State == "queued" {
			break
		}
		if consuming(l.State) {
			used = used.Add(l.Request.Resources)
			for _, c := range l.Request.Pools {
				poolUsed[c.Pool] += c.Quantity
			}
		}
		if len(l.Ports) > 0 {
			ls = append(ls, l)
		}
	}
}

func (m *Manager) launch(l model.Lease, r RunRequest) (model.Lease, error) {
	if (r.SSH != nil || r.sshRuntime != nil) && (r.SSH == nil || r.sshRuntime == nil) {
		return m.launchFailed(l, model.Err("ssh_replay_denied", "a stored/wire SSH descriptor cannot reconstruct a runtime"))
	}
	if reason := m.startDeadlineProblem(l); reason != nil {
		return m.rejectUnstarted(l, reason)
	}
	if err := m.prepareGradleLaunch(&l, &r); err != nil {
		return m.launchFailed(l, err)
	}
	var f io.Closer = sshNoopCloser{}
	var e error
	if r.sshRuntime == nil {
		if e = os.MkdirAll(filepath.Join(m.home, "logs"), 0700); e != nil {
			return m.launchFailed(l, e)
		}
		f, e = os.OpenFile(filepath.Join(m.home, "logs", l.ID+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			return m.launchFailed(l, e)
		}
	}
	// Do not execute user work until its PID and lease have been persisted.
	// Parent death closes the gate, making the waiting child exit.
	reader, writer, e := os.Pipe()
	if e != nil {
		f.Close()
		return m.launchFailed(l, e)
	}
	defer reader.Close()
	defer writer.Close()
	command := r.Command
	if r.sshRuntime != nil {
		command = []string{"/bin/sh", "-l"}
		if r.sshRuntime.execution.Kind == "exec" {
			command = []string{"/bin/sh", "-c", r.sshRuntime.execution.Command}
		}
	}
	args := append([]string{"-c", `IFS= read -r arm_gate <&3 || exit 125; [ "$arm_gate" = go ] || exit 125; exec 3<&-; exec "$@"`, "arm-launch"}, command...)
	cmd := exec.Command("/bin/sh", args...)
	cmd.ExtraFiles = []*os.File{reader}
	cmd.Dir = r.CWD
	for _, entry := range os.Environ() {
		if r.sshRuntime != nil {
			break
		}
		if strings.HasPrefix(entry, "ARM_POOLS=") || strings.HasPrefix(entry, "ARM_PORTS=") || strings.HasPrefix(entry, "ARM_PORT_LEASE=") || strings.HasPrefix(entry, "ARM_STORAGE_DIR=") || strings.HasPrefix(entry, "ARM_STORAGE_LEASE=") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	if len(l.Request.Pools) > 0 {
		claims, err := json.Marshal(l.Request.Pools)
		if err != nil {
			f.Close()
			return m.launchFailed(l, err)
		}
		cmd.Env = append(cmd.Env, "ARM_POOLS="+string(claims))
	}
	if len(l.Ports) > 0 || l.Request.PortLease != "" {
		if e = m.activatePorts(&l, cmd); e != nil {
			f.Close()
			return m.launchFailed(l, e)
		}
	}
	if l.Request.StorageLease != "" {
		store, e := m.storageForRun(l.Request)
		if e != nil {
			f.Close()
			return m.launchFailed(l, e)
		}
		cmd.Env = append(cmd.Env, "ARM_STORAGE_DIR="+store.ManagedDir, "ARM_STORAGE_LEASE="+store.ID)
		if l.Request.Profile == "rust-build" {
			target := filepath.Join(store.ManagedDir, "target")
			cmd.Env = append(cmd.Env, "CARGO_TARGET_DIR="+target, "CARGO_BUILD_TARGET_DIR="+target, "CARGO_BUILD_BUILD_DIR="+target)
		}
	}
	if l.Request.Profile == "rust-build" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("CARGO_BUILD_JOBS=%d", max(1, int(l.Request.Resources.CPU))))
	}
	applyGradleEnvironment(l, cmd)
	if r.sshRuntime == nil {
		cmd.Stdout = f.(*os.File)
		cmd.Stderr = f.(*os.File)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	} else {
		f, e = r.sshRuntime.prepare(cmd)
		if e != nil {
			return m.launchFailed(l, e)
		}
	}
	if reason := m.startDeadlineProblem(l); reason != nil {
		f.Close()
		return m.rejectUnstarted(l, reason)
	}
	if e = cmd.Start(); e != nil {
		f.Close()
		return m.launchFailed(l, e)
	}
	if r.sshRuntime != nil {
		r.sshRuntime.startCopies()
	}
	j := &job{cmd: cmd, done: make(chan struct{})}
	identity, identityErr := usage.Capture(cmd.Process.Pid)
	if identityErr == nil && identity.PGID == cmd.Process.Pid && identity.UID == m.owner {
		j.identity = &identity
		l.Process = &identity
		l.Capabilities = append(append([]string{}, l.Capabilities...), "supervised-usage-observation")
	} else {
		j.observationError = "native process identity unavailable; usage cannot be attributed"
	}
	if err := m.captureGradleLaunch(&l, j.identity); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		f.Close()
		return m.launchFailed(l, err)
	}
	if (l.Request.UsagePolicy != nil || r.sshRuntime != nil) && j.identity == nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		f.Close()
		return m.launchFailed(l, model.Err("observation_unavailable", "usage policy requires a captured process identity; command was not authorized to start"))
	}
	if l.Request.UsagePolicy != nil {
		l.Capabilities = append(l.Capabilities, "observed-usage-policy")
	}
	m.jobs[l.ID] = j
	l.PID = cmd.Process.Pid
	l.State = "active"
	l.Revision++
	if e = m.put(l, "lease.started"); e != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	} else if reason := m.startDeadlineProblem(l); reason != nil {
		// The shell still waits on our private gate: no requested command ran.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		f.Close()
		delete(m.jobs, l.ID)
		l.PID = 0
		l.Process = nil
		return m.rejectUnstarted(l, reason)
	} else if r.sshRuntime != nil && r.sshRuntime.check() != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		f.Close()
		delete(m.jobs, l.ID)
		l.PID = 0
		l.Process = nil
		return m.launchFailed(l, model.Err("ssh_cancelled", "SSH authority/stream expired before process gate"))
	} else {
		delete(m.startClocks, l.ID)
		_, e = writer.Write([]byte("go\n"))
		if e != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	m.wg.Go(func() {
		err := cmd.Wait()
		sshOutcome := ""
		if r.sshRuntime != nil && r.sshRuntime.ctx.Err() != nil {
			sshOutcome = "interrupted"
			if errors.Is(r.sshRuntime.ctx.Err(), context.DeadlineExceeded) {
				sshOutcome = "expired"
			}
		}
		f.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && !errors.Is(syscall.Kill(-cmd.Process.Pid, 0), syscall.ESRCH) {
			time.Sleep(10 * time.Millisecond)
		}
		groupGone := errors.Is(syscall.Kill(-cmd.Process.Pid, 0), syscall.ESRCH)
		m.mu.Lock()
		defer m.mu.Unlock()
		defer close(j.done)
		delete(m.jobs, l.ID)
		current, ge := m.get(l.ID)
		delete(m.usages, l.ID)
		if (current.Usage == nil || !current.Usage.IdentityVerified || current.Usage.MemberCount == 0) && j.lastUsage != nil {
			summary := *j.lastUsage
			summary.Stale = true
			summary.Detail += "; final summary retains the last identity-verified observation"
			current.Usage = &summary
		}
		if current.Usage != nil {
			current.Usage.Live = false
			current.Usage.Members = nil
		}
		if ge != nil {
			return
		}
		code := 0
		if err != nil {
			code = 1
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			}
		}
		current.ExitCode = &code
		now := time.Now().UTC()
		current.State = "terminal"
		if current.Outcome == "" {
			current.Outcome = "completed"
			if code != 0 {
				current.Outcome = "failed"
			}
			if sshOutcome != "" {
				current.Outcome = sshOutcome
			}
		}
		current.Ended = &now
		if !groupGone {
			current.State = "needs_attention"
			current.Outcome = "descendants_unreconciled"
			current.Ended = nil
		} else if len(current.Ports) > 0 {
			m.finishPorts(&current)
		}
		if err := m.finishGradleJob(&current, groupGone); err != nil {
			current.State, current.Ended = "needs_attention", nil
			current.Failure = "Gradle workload settlement failed: " + err.Error()
		}
		current.Revision++
		_ = m.put(current, "lease."+current.Outcome)
	})
	return l, e
}

func (m *Manager) launchFailed(l model.Lease, err error) (model.Lease, error) {
	now := time.Now().UTC()
	l.State = "terminal"
	l.Outcome = "launch_failed"
	l.Failure = err.Error()
	l.Ended = &now
	if len(l.Ports) > 0 {
		m.finishPorts(&l)
	}
	l.Revision++
	return l, m.put(l, "lease.launch_failed")
}

// Reconcile confirms that an uncertain process group is absent. It never
// sends a signal to a recovered PID, which may have been recycled.
func (m *Manager) Reconcile(id string) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reconcile(id)
}
func (m *Manager) reconcile(id string) (model.Lease, error) {
	l, e := m.get(id)
	if e != nil {
		return l, e
	}
	if l.State == "terminal" {
		return l, nil
	}
	if m.gradleObligation(l) {
		return m.reconcileGradle(l, "")
	}
	if l.Request.Kind == "storage" {
		return l, model.Err("needs_attention", "use storage cleanup to retry or storage handoff to transfer responsibility")
	}
	if l.State == "needs_attention" && len(l.Request.Ports) > 0 {
		return m.reconcilePorts(l)
	}
	if l.State != "needs_attention" || l.PID <= 0 {
		return l, model.Err("needs_attention", "lease has no verifiable recovered process group")
	}
	if !errors.Is(syscall.Kill(-l.PID, 0), syscall.ESRCH) {
		return l, model.Err("needs_attention", "process group still exists or cannot be inspected; no signal was sent")
	}
	now := time.Now().UTC()
	l.State = "terminal"
	l.Outcome = "reconciled"
	l.Ended = &now
	l.Revision++
	return l, m.put(l, "lease.reconciled")
}

func validateCargo(args []string, cpu float64) error {
	if cpu < 1 {
		return model.Err("invalid_request", "rust-build requires at least one CPU")
	}
	for _, arg := range args[1:] {
		if arg == "-j" || strings.HasPrefix(arg, "-j") || arg == "--jobs" || strings.HasPrefix(arg, "--jobs=") || arg == "--config" || strings.HasPrefix(arg, "--config=") {
			return model.Err("profile_conflict", "rust-build supplies the jobs budget; remove explicit jobs or config overrides")
		}
	}
	return nil
}

func (m *Manager) Output(id string, offset int64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, e := m.get(id); e != nil {
		return nil, e
	}
	if offset < 0 {
		return nil, model.Err("invalid_request", "negative log offset")
	}
	f, e := os.Open(filepath.Join(m.home, "logs", id+".log"))
	if os.IsNotExist(e) {
		return []byte{}, nil
	}
	if e != nil {
		return nil, e
	}
	defer f.Close()
	if _, e = f.Seek(offset, 0); e != nil {
		return nil, e
	}
	b := make([]byte, 32*1024)
	n, e := f.Read(b)
	if n > 0 {
		return b[:n], nil
	}
	return []byte{}, nil
}

func (m *Manager) Active() bool {
	s, e := m.Status()
	if e != nil {
		return true
	}
	for _, l := range s.Leases {
		if consuming(l.State) || l.State == "queued" {
			return true
		}
	}
	return false
}
