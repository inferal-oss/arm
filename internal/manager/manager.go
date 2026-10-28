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
	"sync/atomic"
	"syscall"
	"time"

	"getarm.org/internal/cargoprofile"
	"getarm.org/internal/childowner"
	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/database"
	"getarm.org/internal/kubesource"
	"getarm.org/internal/leaseclock"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/nodestop"
	"getarm.org/internal/observe"
	"getarm.org/internal/ports"
	"getarm.org/internal/pressure"
	"getarm.org/internal/storage"
	"getarm.org/internal/syncservice"
	"getarm.org/internal/usage"
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
	leaseHost, leaseOwner string
	leaseRequest          model.Request
	nativeDone            chan struct{}
	sshRuntime            *sshRuntime
	controlMu             sync.Mutex
	controlRetired        bool
	controlError          error
	childOwner            *childowner.Owner
	cmd                   *exec.Cmd
	done                  chan struct{}
	identity              *model.ProcessIdentity
	cpuAnchor             string
	cpuExited             atomic.Bool
	observationError      string
	lastUsage             *model.WorkloadUsage
	// Manager.mu protects the one original cancellation/escalation sequence.
	originalCancellationRequested bool
	originalCancellationError     error
}

type Manager struct {
	proxyReported map[string]time.Time

	kubernetesSourceAdmissions map[string]bool
	sourceBeforeAdmission      func()                                  // private disposable causal fixture; runs outside locks
	kubernetesSources          map[string]*KubernetesSourcePreparation // original native source/parent cancellation only

	leaseClockCheckpoint    leaseclock.Sample
	storageExportCancels    map[string]context.CancelFunc
	leaseClockRead          func() (leaseclock.Sample, error)
	leaseClockLast          leaseclock.Sample
	leaseClockError         *model.Error
	sshTransports           map[string]*sshLeaseHold
	nodeStopBoundary        *nodestop.Boundary
	nodeStopUnknown         bool
	nodeStopAccepted        bool
	nodeStopAcceptedTarget  nodestop.Target // current successful admission only, never restored from history
	leaseReviewsUnavailable bool
	leaseReviewError        string
	cargoHelper             CargoHelperSource
	cargoWorkers            map[string]*cargoWorker
	workspaces              *workspaceBoundary
	mu                      sync.Mutex
	// Acquire before mu when both are needed. Held through native CLI Start,
	// never through Wait, so shutdown has a precise no-new-launch boundary.
	pressureLaunchMu   sync.Mutex
	db                 *sql.DB
	databaseSlot       chan struct{}
	home, host, owner  string
	budgetStartupError *model.Error
	poolStartupError   *model.Error
	ownerUsername      string
	capacity           model.Resources
	jobs               map[string]*job
	syncWorkers        map[string]*SyncWorkerHold
	syncSourceWorkers  map[string]*SyncSourceHold
	relayTransits      map[string]*RelayTransit
	forwardListeners   map[string]*ForwardListener
	stop               chan struct{}
	wg                 sync.WaitGroup
	closing            bool
	draining           bool
	shutdownDeadline   time.Time
	observationMu      sync.RWMutex
	observations       map[string]model.ResourceMeasurement
	cpuObserver        cpuObserver
	cpuAvailability    model.CPUAvailability
	cpuObserved        time.Time
	cpuPressureTick    time.Duration
	cpuCredits         []cpuCreditSample
	cpuCreditFrame     observe.CPUCreditFrame
	memoryAvailability model.MemoryAvailability
	memoryObserved     time.Time
	memoryPressureTick time.Duration
	diskAvailability   model.DiskAvailability
	diskObserved       time.Time
	diskPressureTick   time.Duration
	pressureEpoch      string
	pressureState      pressure.State
	pressureLastError  string
	diskScopeVolume    string
	diskSampleSlots    chan struct{}
	diskCollector      func(context.Context, string) model.DiskAvailability

	storageBenchmarkRunner func(context.Context, string, string, int64) (model.StoragePerformance, error)
	storageBenchmarkLeases map[string]context.CancelFunc
	storageBenchmarkBusy   bool
	storagePools           map[string]*storagePool
	storagePoolObserve     func(context.Context, string) (model.StorageLocation, error)
	defaultStorageLocation model.StorageLocation
	storagePoolReadings    map[string]storagePoolReading

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

	// Immutable native-sampling owner snapshot, protected by observationMu.
	cpuWarningOwners map[string]cpuWarningOwner
}

var capabilities = []string{"cooperative-reservation", "process-group-supervision", "durable-events", "host-observation", "custom-resource-pools", "pool-capacity-journal", "local-global-pool-authority", "bulk-lease-actions", "draining-shutdown", "local-owner-policy-budgets", "counted-resource-subpools"}

func Open(home string, capacity model.Resources) (*Manager, error) {
	return openWithLeaseClock(home, capacity, leaseclock.Read)
}

func openWithLeaseClock(home string, capacity model.Resources, clockRead func() (leaseclock.Sample, error)) (*Manager, error) {
	return openWithStartup(home, capacity, clockRead, nil)
}

func openWithStartup(home string, capacity model.Resources, clockRead func() (leaseclock.Sample, error), startup *daemonconfig.Settings) (*Manager, error) {
	if !finiteCPU(capacity.CPU) || capacity.CPU < 0 || capacity.CPU > 65536 {
		return nil, model.Err("invalid_config", "CPU ceiling must be a finite nonnegative logical-core count at most 65536")
	}
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
	if !fresh {
		if e = database.CheckWorkspaceFormatFile(context.Background(), home); e != nil {
			return nil, e
		}
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
		if e = database.RefuseRetiredMountStateAtStartup(context.Background(), home, db); e != nil {
			db.Close()
			return nil, e
		}
	}
	for _, q := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA synchronous=FULL`, `PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
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
	if e = syncservice.VerifyDurableSQLite(context.Background(), db); e != nil {
		db.Close()
		return nil, e
	}
	m := &Manager{kubernetesSourceAdmissions: map[string]bool{}, kubernetesSources: map[string]*KubernetesSourcePreparation{}, relayTransits: map[string]*RelayTransit{}, db: db, home: home, capacity: capacity, jobs: map[string]*job{}, syncWorkers: map[string]*SyncWorkerHold{}, syncSourceWorkers: map[string]*SyncSourceHold{}, stop: make(chan struct{}), storageJobs: map[string]bool{}, portSockets: map[string][]*ports.Socket{}}
	m.leaseClockRead = clockRead
	m.pressureEpoch = local.ID()
	m.cpuObserver = observe.NewCPUSampler()
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
	if startup != nil {
		m.capacity = model.Resources{} // unpublished until its durable decision
		if e = m.applyStartupConfiguration(*startup); e != nil {
			db.Close()
			return nil, e
		}
	}
	if e = m.initSyncRecords(context.Background()); e != nil {
		db.Close()
		return nil, e
	}
	if e = m.initSubpoolRecords(); e != nil && !m.degradePoolStartup(e) {
		db.Close()
		return nil, e
	}
	if e = m.initBudgetRecords(); e != nil && !m.degradeBudgetStartup(e) {
		db.Close()
		return nil, e
	}
	if e = m.initRunRecords(); e != nil {
		db.Close()
		return nil, e
	}
	if e = m.initCargoRecords(); e != nil {
		db.Close()
		return nil, e
	}
	if e = m.initGradleRecords(); e != nil {
		db.Close()
		return nil, e
	}
	if e = m.initLeaseReviews(); e != nil {
		if !errors.Is(e, database.ErrLeaseReviewSchema) {
			db.Close()
			return nil, e
		}
		m.leaseReviewsUnavailable = true // Sticky for this manager; no implicit repair/retry.
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
	if e = m.initStoragePools(startup); e != nil {
		m.closeStoragePools()
		if m.storage != nil {
			m.storage.Close()
		}
		db.Close()
		return nil, e
	}
	opened := false
	defer func() {
		if !opened {
			m.closeStoragePools()
		}
		if !opened && m.storage != nil {
			m.storage.Close()
		}
	}()
	if e = m.initLeaseClock(); e != nil {
		db.Close()
		return nil, e
	}
	// A recovered PID alone never authorizes signalling. Keep uncertain jobs charged.
	leaves, e := m.list()
	if e != nil {
		db.Close()
		return nil, e
	}
	for _, l := range leaves {
		if e = m.validateStorageBinding(l); e != nil {
			db.Close()
			return nil, e
		}
		if l.Usage != nil {
			l.Usage.Live = false
			l.Usage.Stale = true
		}
		var run sql.NullString
		if e = db.QueryRow(`SELECT run FROM leases WHERE id=?`, l.ID).Scan(&run); e != nil {
			db.Close()
			return nil, e
		}

		if (l.Request.Kind == relayTransitKind || l.Request.Kind == syncPreparationKind || l.Request.Kind == forwardListenerKind) && l.State != "terminal" {
			// A recorded in-process allocation is not a positive join witness.
			// In particular, PID0 must never settle a lost transport owner.
			l.State = "needs_attention"
			l.Outcome = "relay_supervision_lost"
			l.Failure = "Original relay transport join was not recorded; resources remain charged pending explicit recovery"
			if l.Request.Kind == syncPreparationKind {
				l.Outcome = "sync_preparation_supervision_lost"
				l.Failure = "Original sync preparation join and scratch cleanup were not recorded; resources remain charged pending explicit recovery"
			}
			if l.Request.Kind == forwardListenerKind {
				l.Outcome = "forward_listener_supervision_lost"
				l.Failure = "Original source listener, connections and port cleanup were not joined; port and resources remain charged"
			}
			l.Revision++
			if e = m.put(l, "relay.recovery_required"); e != nil {
				db.Close()
				return nil, e
			}
			continue
		}
		if syncBound, syncErr := m.syncLeaseBound(l.ID); syncErr != nil {
			db.Close()
			return nil, syncErr
		} else if syncBound && consuming(l.State) {
			if e = m.retainSyncLease(&l, "sync_supervision_lost"); e != nil {
				db.Close()
				return nil, e
			}
			continue
		}
		if run.Valid && consuming(l.State) {
			sshSaved, err := recordedSSHRun(run.String)
			if err != nil {
				db.Close()
				return nil, err
			}
			if sshSaved {
				l.State, l.Outcome, l.Ended = "needs_attention", "ssh_supervision_lost", nil
				l.Failure = "Original SSH transport join was not recorded; saved process descriptors cannot settle its obligations"
				l.Revision++
				if e = m.put(l, "lease.ssh_recovery_required"); e != nil {
					db.Close()
					return nil, e
				}
				continue
			}
		}
		if l.Cargo != nil {
			if e = m.recoverCargo(l); e != nil {
				db.Close()
				return nil, e
			}
			continue
		}
		if l.Request.Profile == "rust-build" && consuming(l.State) {
			l.State = "needs_attention"
			l.Outcome = "cargo_record_missing"
			l.Failure = "Managed Cargo record is missing; no automatic execution or PID0 settlement is permitted"
			l.Revision++
			if e = m.put(l, "cargo.recovery_required"); e != nil {
				db.Close()
				return nil, e
			}
			continue
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
			if _, clockErr := m.leaseRemaining(l); clockErr == nil && !m.gradleObligation(l) && (l.PID == 0 || errors.Is(syscall.Kill(-l.PID, 0), syscall.ESRCH)) {
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
	if e = m.loadHostPressure(); e != nil {
		db.Close()
		return nil, e
	}
	m.sample()
	m.initializeCPU()
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
	if e = m.retainRecoveredLeaseClocks(); e != nil {
		db.Close()
		return nil, e
	}
	if m.leaseClockError == nil {
		if e = m.reconcileLeaseExpiries(); e != nil {
			db.Close()
			return nil, e
		}
	}
	if e = m.initScheduling(); e != nil {
		db.Close()
		return nil, e
	}
	if e = m.initWorkspaces(); e != nil {
		db.Close()
		return nil, e
	}
	opened = true
	return m, nil
}

func (m *Manager) Start() {
	m.startCPUSampling()
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
	m.wg.Go(m.hostPressureLoop)
	m.wg.Go(m.hostPressureDeliveryLoop)
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
	var closeErr error
	var syncOwners []*SyncWorkerHold
	m.pressureLaunchMu.Lock()
	m.mu.Lock()
	m.closing = true
	for _, cancel := range m.storageBenchmarkLeases {
		cancel()
	}
	m.mu.Unlock()
	m.pressureLaunchMu.Unlock()
	// Workspace calls may hold the single database connection while awaiting
	// cancellation. Join them before ordinary job-ending SQL, without holding
	// Manager.mu: final proof gates also inspect manager lifecycle state.
	if m.workspaces != nil {
		m.workspaces.close()
	}
	closeErr = errors.Join(closeErr, m.closeRelayTransits())
	closeErr = errors.Join(closeErr, m.closeForwardListeners())
	m.mu.Lock()
	for id := range m.cargoWorkers {
		_, e := m.end(id, "cancelled")
		closeErr = errors.Join(closeErr, e)
	}
	for id := range m.jobs {
		_, e := m.end(id, "cancelled")
		closeErr = errors.Join(closeErr, e)
	}
	for id, hold := range m.syncWorkers {
		syncOwners = append(syncOwners, hold)
		hold.cancel()
		_, e := m.end(id, "cancelled")
		closeErr = errors.Join(closeErr, e)
	}
	for id := range m.syncSourceWorkers {
		_, e := m.end(id, "cancelled")
		closeErr = errors.Join(closeErr, e)
	}
	if m.nodeStopAccepted || m.nodeStopUnknown {
		ls, e := m.list()
		closeErr = errors.Join(closeErr, e)
		for _, lease := range ls {
			if outstanding(lease) {
				closeErr = errors.Join(closeErr, model.Err("node_stop_unsettled", "an obligation remains charged at Manager.Close"))
				break
			}
		}
		if m.nodeStopUnknown {
			closeErr = errors.Join(closeErr, model.Err("node_stop_outcome_unknown", "stop commit was not reconciled"))
		}
	}
	for _, h := range m.sshTransports {
		h.runtime.cancel()
	}
	m.mu.Unlock()
	close(m.stop)
	m.wg.Wait()
	closeErr = errors.Join(closeErr, m.sshSettlementErrors())
	for _, hold := range syncOwners {
		// This callback belongs to the original admitted runtime even when
		// no runner was started or the lease already needs attention.
		hold.runtime.closeAll()
		<-hold.runtime.closeDone
	}
	if observer, ok := m.cpuObserver.(interface{ Close() error }); ok {
		closeErr = errors.Join(closeErr, observer.Close())
	}
	for _, sockets := range m.portSockets {
		closeErr = errors.Join(closeErr, ports.CloseChecked(sockets))
	}
	if m.storage != nil {
		closeErr = errors.Join(closeErr, m.storage.Close())
	}
	closeErr = errors.Join(closeErr, m.closeStoragePools())
	dbCloseErr := m.db.Close()
	return errors.Join(closeErr, dbCloseErr)
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
	s := model.Status{LeaseClockError: m.leaseClockError, PoolError: m.poolPolicyError(), BudgetError: m.budgetPolicyError(), Host: m.host, Owner: m.owner, OwnerUsername: m.ownerUsername, Draining: m.draining, Capacity: m.capacity, Leases: ls, Capabilities: append(append([]string{}, capabilities...), "observed-usage-policy", "sender-aware-agent-messaging", "workspace-published-agents-poll-v1", "owned-gradle-services"), Infrastructure: append([]model.PortBinding{}, m.infrastructure...)}
	s.CPUWarning = &model.CPUWarningEvidence{State: "unavailable", Detail: "CPU warning lease accounting is unavailable"}
	if e == nil {
		if tick, err := pressureNativeTick(); err == nil {
			_, evidence := m.cpuWarningSample(time.Now().UTC(), tick, ls)
			s.CPUWarning = &evidence
		} else {
			s.CPUWarning = &model.CPUWarningEvidence{State: "unavailable", Detail: "Suspend-inclusive CPU warning clock is unavailable"}
		}
	}
	if m.pressureLastError != "" {
		s.PressureError = &model.Error{Code: "host_pressure_unavailable", Message: m.pressureLastError}
	}
	if !m.leaseReviewsUnavailable {
		s.Capabilities = append(s.Capabilities, "local-lease-reviews-v1")
	} else {
		s.ReviewError = &model.Error{Code: "reviews_unavailable", Message: reviewUnavailableMessage}
	}
	if m.workspaces != nil {
		s.Workspaces = m.workspaces.projection()
	}
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
	cpu := m.cpuBudget(s.Reserved.CPU)
	s.CPU = &cpu
	s.Capacity.CPU = cpu.Capacity
	memory := m.memoryBudget(s.Reserved.Memory)
	s.Memory = &memory
	s.Capacity.Memory = memory.Capacity
	disk := m.diskBudget(s.Reserved.Disk)
	s.Disk = &disk
	s.Capacity.Disk = disk.Capacity
	s.Capabilities = append(s.Capabilities, "live-cpu-admission", "live-memory-admission", "live-disk-admission", "named-storage-pools", "storage-performance-benchmarks")
	for _, kind := range []string{"cpu", "memory", "disk"} {
		total, reserved, unit := s.Capacity.CPU, s.Reserved.CPU, "logical-core-equivalents"
		if kind == "memory" {
			total, reserved, unit = float64(s.Capacity.Memory), float64(s.Reserved.Memory), "bytes"
		}
		if kind == "disk" {
			total, reserved, unit = float64(s.Capacity.Disk), float64(s.Reserved.Disk), "bytes"
		}
		v := model.ResourceView{ID: m.host + "/" + kind, Host: m.host, Pool: m.host + "/" + kind + "-budget", Kind: kind, Unit: unit, BudgetTotal: model.FloatQuantity(total), Reserved: model.FloatQuantity(reserved), Available: model.FloatQuantity(max(0, total-reserved)), Accounting: "cooperative-reservation-budget", Users: []model.ResourceUse{}}
		if kind == "cpu" {
			known := cpu.Known
			v.AvailabilityKnown, v.Stale = &known, cpu.Stale
			v.Available, v.Deficit = model.FloatQuantity(cpu.Available), model.FloatQuantity(cpu.Deficit)
			v.Accounting = cpu.Accounting
		}
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
	m.appendStorageResources(&s, ls)
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
	defer m.traceNodeStop("acquire-return")
	return m.acquire(r, run)
}

func (m *Manager) acquire(r model.Request, run *RunRequest) (model.Lease, error) {
	return m.acquireWithAdmission(r, run, false, nil)
}

// relay is an in-process capability, never a field accepted from a wire
// request. Only AcquireRelayTransit passes it after validating its owner.
func (m *Manager) acquireWithRelay(r model.Request, run *RunRequest, relay bool) (model.Lease, error) {
	return m.acquireWithAdmission(r, run, relay, nil)
}

// A sync admission is an internal typed transaction participant. It cannot be
// supplied by a wire Request or by the ordinary Acquire/AcquireSSH paths.
func (m *Manager) acquireWithAdmission(r model.Request, run *RunRequest, relay bool, sync *syncAdmission) (model.Lease, error) {
	return m.acquireWithSourcePermit(r, run, relay, sync, nil)
}

func (m *Manager) acquireWithSourcePermit(r model.Request, run *RunRequest, relay bool, sync *syncAdmission, source *kubernetesSourcePermit) (model.Lease, error) {
	var empty model.Lease
	if err := validateSourcePermit(m, r, source); err != nil {
		return empty, err
	}
	if sync != nil && (relay || r.Queue || r.StartDeadline != nil) {
		return empty, model.Err("invalid_request", "sync admission must start immediately")
	}

	if run != nil && (run.SSH != nil || run.sshRuntime != nil) && (run.SSH == nil || run.sshRuntime == nil || !reflect.DeepEqual(run.SSH, sshDescriptor(run.sshRuntime.request)) || run.sshRuntime.check() != nil) {
		return empty, model.Err("invalid_request", "a persisted/wire SSH descriptor cannot authorize execution")
	}
	if m.closing || m.draining {
		return empty, model.Err("backend_stopping", "backend is shutting down")
	}
	if err := m.reconcileLeaseExpiries(); err != nil {
		return empty, err
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
	if run != nil && r.Resources.CPU <= 0 {
		return empty, model.Err("invalid_request", "supervised execution requires a positive CPU claim; other resource claims do not authorize execution without CPU headroom")
	}
	if r.Kind != "" && r.Kind != "execution" && r.Kind != "storage" && r.Kind != "ports" && !(relay && (r.Kind == relayTransitKind || r.Kind == syncPreparationKind || r.Kind == forwardListenerKind)) {
		return empty, model.Err("invalid_request", "kind must be execution, storage or ports")
	}
	if r.ForwardSource != nil && (!relay || r.Kind != forwardListenerKind) {
		return empty, model.Err("invalid_request", "forward source binding requires its in-process listener owner")
	}
	if err := m.validateStoragePool(&r); err != nil {
		return empty, err
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
	if r.StorageLease != "" {
		parent, err := m.get(r.StorageLease)
		if err != nil {
			return empty, err
		}
		if kubesource.IsReserved(parent.Request) {
			return empty, model.Err("storage_unavailable", "native source storage cannot be attached by ordinary work")
		}
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
	if err := m.validateCargoRequest(r, run); err != nil {
		return empty, err
	}
	if err := m.validateGradleRequest(r, run); err != nil {
		return empty, err
	}
	if !m.storageRequestFitsCeiling(r) {
		return empty, model.Err("capacity_exceeded", "request exceeds a configured host ceiling or known physical memory capacity")
	}
	if err := m.poolAdmissionAncestry(r.Pools); err != nil {
		return empty, err
	}
	if r.StorageLease != "" && r.Profile != "rust-build" {
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
	if sync != nil {
		ls, e = sync.withoutPreparation(ls)
		if e != nil {
			return empty, e
		}
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
	if r.StoragePool != "" {
		p := m.storagePools[r.StoragePool]
		l.StorageBinding = &model.StoragePoolBinding{Name: r.StoragePool, Volume: p.record.Volume, Backing: p.record.Backing, SharesDefault: p.record.Backing != "" && p.record.Backing == m.defaultStorageLocation.Backing}
	}
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
	} else if !queued && m.fitsStorageRequest(r, used, ls) && poolFits && len(budgetBlockers) == 0 {
		if e = m.activate(&l, run != nil); e != nil {
			return empty, e
		}
	} else if !r.Queue {
		return empty, model.Err("insufficient_capacity", "host capacity or policy ceilings are committed, or live CPU/memory/disk headroom is insufficient/unavailable; use --wait to queue")
	}
	if sync != nil && (deadlineProblem != nil || (run == nil && l.State != "active") || (run != nil && l.State != "provisioning")) {
		return empty, model.Err("insufficient_capacity", "sync reservation requires immediate complete admission")
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
	if r.Profile == "rust-build" {
		projectCargo(&l, cargoNewRecord(l, run))
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
	if source != nil && !source.parent {
		if e = excludeSourceOperationTx(tx, source.scope); e != nil {
			return empty, e
		}
	}
	if _, e = tx.Exec(`INSERT INTO leases VALUES(?,?,?,?)`, l.ID, key, string(b), rb); e != nil {
		return empty, e
	}
	if e = insertCargoRecord(tx, l, run); e != nil {
		return empty, e
	}
	if e = m.insertSSHOperation(tx, l, run); e != nil {
		return empty, e
	}
	if sync != nil {
		if e = sync.reserveTx(tx, l); e != nil {
			return empty, e
		}
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
	e = tx.Commit()
	if e != nil {
		return empty, e
	}
	if sync != nil {
		sync.acknowledgeHandoff()
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
		} else if sync != nil && sync.inline {
			l.State = "active"
			l.Revision++
			e = m.put(l, "lease.started")
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

func (m *Manager) activate(l *model.Lease, isRun bool) error {
	s, err := m.readLeaseClock()
	if err != nil {
		return err
	}
	deadline, err := leaseclock.New(s, l.Request.TTL)
	if err != nil {
		return err
	}
	now, end := s.Wall, deadline.Wall
	l.Clock = deadline
	l.QueueInfo = nil
	l.Started = &now
	l.Expires = &end
	l.State = "active"
	if isRun || l.Request.Kind == "storage" || len(l.Request.Ports) > 0 {
		l.State = "provisioning"
	}
	return nil
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
	defer m.traceNodeStop("end-return")
	if source := m.kubernetesSources[id]; source != nil {
		source.Cancel()
	} // BEFORE any failing ledger read
	if hold := m.syncWorkers[id]; hold != nil {
		hold.cancel()
	}
	if hold := m.syncSourceWorkers[id]; hold != nil {
		hold.cancel()
	}
	l, e := m.get(id)
	if e != nil {
		return l, e
	}
	if cancel, held := m.storageBenchmarkLeases[id]; held {
		cancel()
		if l.State == "active" || l.State == "provisioning" {
			l.State = "ending"
			l.Outcome = outcome
			l.Revision++
			return l, m.put(l, "storage.benchmark_ending")
		}
		return l, nil
	}
	if l.State == "terminal" || l.State == "ending" {
		if l.State == "ending" {
			if h := m.sshTransports[id]; h != nil && l.Host == h.original.Host && l.Owner == h.original.Owner && reflect.DeepEqual(l.Request, h.original.Request) {
				h.runtime.cancel()
			}
		}
		return l, nil
	}
	if l.Request.Kind == relayTransitKind || l.Request.Kind == syncPreparationKind {
		return m.endRelayTransit(l, outcome)
	}
	if l.Request.Kind == forwardListenerKind {
		return m.endForwardListener(l, outcome)
	}
	if worker := m.cargoWorkers[id]; worker != nil && l.RecoveryReason == "lease_clock_uncertain" {
		rec, err := m.getCargo(id)
		if err != nil {
			return l, err
		}
		if rec.Worker != worker.id || l.Host != worker.leaseHost || l.Owner != worker.leaseOwner || !reflect.DeepEqual(l.Request, worker.leaseRequest) {
			return l, model.Err("needs_attention", "original Cargo owner differs; obligations remain charged")
		}
		// Only this explicit ending operation cancels native work after clock
		// uncertainty. Cancellation is not join; the original worker settles it.
		worker.cancel()
		l.State, l.Outcome = "ending", outcome
		l.Revision++
		return l, m.put(l, "lease.ending")
	}
	if l.State == "needs_attention" {
		j := m.jobs[id]
		// An explicit End/Close can still cancel the exact current owner whose
		// clock became uncertain. This never adopts a recovered or replaced row.
		if l.RecoveryReason != "lease_clock_uncertain" || j == nil || j.cmd == nil || j.cmd.Process == nil || l.PID != j.cmd.Process.Pid || l.Host != j.leaseHost || l.Owner != j.leaseOwner || !reflect.DeepEqual(l.Request, j.leaseRequest) || !reflect.DeepEqual(l.Process, j.identity) {
			return l, model.Err("needs_attention", "allocation remains charged pending reconciliation; storage leases support explicit cleanup retry or handoff")
		}
	}
	if hold := m.syncWorkers[id]; hold != nil {
		if err := hold.checkBindingLocked(context.Background()); err != nil {
			return l, err
		}
		err := m.retainSyncLease(&l, outcome)
		return l, err
	}
	if hold := m.syncSourceWorkers[id]; hold != nil {
		if err := hold.checkBindingLocked(context.Background()); err != nil {
			return l, err
		}
		err := m.retainSyncLease(&l, outcome)
		return l, err
	}
	if bound, err := m.syncLeaseBound(l.ID); err != nil {
		return l, err
	} else if bound && m.jobs[id] == nil {
		err = m.retainSyncLease(&l, outcome)
		return l, err
	}
	if l.Request.Kind == "storage" {
		return m.endStorage(l, outcome)
	}
	if l.Request.Kind == "ports" && l.State != "queued" {
		return m.endPortParent(l, outcome)
	}
	if l.Cargo != nil || l.Request.Profile == "rust-build" {
		return m.endCargo(l, outcome)
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
		// Failed persistence still requests the exact original cancellation.
		defer func() { _ = m.requestOriginalJobCancellation(id, j) }()
		l.State = "ending"
		if e = m.put(l, "lease.ending"); e != nil {
			return l, e
		}
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
	if err := m.reconcileLeaseExpiries(); err != nil {
		return model.Lease{}, err
	}
	l, e := m.get(id)
	if e != nil {
		return l, e
	}
	if kubesource.IsReserved(l.Request) {
		return l, model.Err("invalid_request", "source attempts have fixed entitlement and cannot be renewed")
	}
	if l.Request.Kind == relayTransitKind || l.Request.Kind == syncPreparationKind || l.Request.Kind == forwardListenerKind {
		return l, model.Err("invalid_request", "in-process owned work has a fixed reviewed lifetime and cannot be renewed")
	}
	if bound, err := m.syncLeaseBound(id); err != nil {
		return l, err
	} else if bound {
		return l, model.Err("invalid_request", "sync work has a fixed reviewed lifetime and cannot be renewed")
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
	if l.State != "active" || !m.leaseLive(l) {
		return l, model.Err("lease_inactive", "only an unexpired active lease can be renewed")
	}
	if err := m.gradleRenew(l, ttl); err != nil {
		return l, err
	}
	if l.Request.StorageLease != "" {
		r := l.Request
		r.TTL = ttl
		if l.Cargo != nil {
			if l.Cargo.State != "compiling" {
				return l, model.Err("lease_inactive", "Cargo preparation cannot be renewed")
			}
			if _, err := m.cargoStorageParent(r, nil); err != nil {
				return l, err
			}
		} else if _, err := m.storageForRun(r); err != nil {
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
	if l.Request.Resources.CPU > 0 {
		var reserved float64
		for _, lease := range ls {
			if consuming(lease.State) {
				reserved += lease.Request.Resources.CPU
			}
		}
		budget := m.cpuBudget(reserved)
		traceCPURenewal(m, budget)
		if !budget.Known || budget.Deficit > 0 {
			return l, model.Err("renewal_denied", "live CPU headroom cannot cover outstanding promises")
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
	s, err := m.readLeaseClock()
	if err != nil {
		return l, err
	}
	remaining, err := l.Clock.Remaining(s, l.Expires)
	if err != nil || remaining <= 0 {
		return l, model.Err("lease_inactive", "lease expired or elapsed deadline is uncertain")
	}
	if ttl < remaining {
		return l, model.Err("invalid_request", "renewal must extend the current expiry")
	}
	deadline, err := leaseclock.New(s, ttl)
	if err != nil {
		return l, err
	}
	l.Clock = deadline
	end := deadline.Wall
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
	_ = m.expireProxyRuns(time.Now()) // Advisory reporting state; never process authority.
	reviewsReady := false
	// Optional review work runs only after ordinary expiry, incidents, queue
	// reconciliation and admission have had their turn. Always read fresh rows.
	defer func() {
		if !reviewsReady || m.leaseReviewsUnavailable {
			return
		}
		fresh, err := m.list()
		if err == nil {
			err = m.tickLeaseReviews(time.Now().UTC(), fresh)
		}
		if err != nil {
			m.leaseReviewError = "Optional check-in scheduling failed. Inspect stored policy and checkpoint integrity; ordinary lease expiry and admission continue."
		} else {
			m.leaseReviewError = ""
		}
	}()
	ls, e := m.list()
	if e != nil {
		return
	}
	clockErr := m.reconcileLeaseExpiries()
	ls, e = m.list()
	if e != nil {
		return
	}
	m.tickGradleWorkloads(ls)
	if err := m.reconcileIncidents(); err != nil {
		return
	}
	if err := m.reconcileQueued(); err != nil {
		return
	}
	reviewsReady = true
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
	if clockErr != nil || m.draining || m.budgetStartupError != nil || m.poolStartupError != nil {
		return
	}
	poolUsed, e := poolReservations(ls)
	if e != nil {
		return
	}
	for i, l := range ls {
		if l.State != "queued" {
			continue
		}
		var rb sql.NullString
		if e = m.db.QueryRow(`SELECT run FROM leases WHERE id=?`, l.ID).Scan(&rb); e != nil {
			return
		}
		if rb.Valid && l.Request.Resources.CPU <= 0 {
			if _, e = m.rejectUnstarted(l, &model.Error{Code: "invalid_request", Message: "supervised execution requires a positive CPU claim"}); e != nil {
				return
			}
			continue
		}
		if l.Cargo != nil && m.cargoHelper == nil {
			break
		}
		if l.Request.StorageLease != "" {
			if _, err := m.storageParentForProfile(l.Request, nil); err != nil {
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
		if !budgetFits || !m.fitsStorageRequest(l.Request, used, ls) || !poolFits {
			break
		}
		if reason := m.startDeadlineProblem(l); reason != nil {
			if _, e = m.rejectUnstarted(l, reason); e != nil {
				return
			}
			continue
		}
		m.attachBudgetEvidence(&l)
		m.attachPoolEvidence(&l)
		l.BudgetPolicy = policies
		if len(l.Request.Ports) == 0 {
			if e = m.activate(&l, rb.Valid); e != nil {
				return
			}
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
		// Later disk/backing and port checks reread this snapshot. Publish the
		// final grant state before admitting another queued bundle.
		ls[i] = l
		if l.State == "queued" {
			break
		}
		if consuming(l.State) {
			used = used.Add(l.Request.Resources)
			for _, c := range l.Request.Pools {
				poolUsed[c.Pool] += c.Quantity
			}
		}
	}
}

func (m *Manager) launch(l model.Lease, r RunRequest) (model.Lease, error) {
	if r.sshRuntime != nil {
		if err := m.bindSSHTransport(l, r.sshRuntime); err != nil {
			return l, err
		}
	}
	if !m.leaseLive(l) {
		return m.launchFailed(l, model.Err("lease_inactive", "elapsed entitlement unavailable before launch"))
	}
	if l.Request.Resources.CPU <= 0 {
		return m.rejectUnstarted(l, &model.Error{Code: "invalid_request", Message: "supervised execution requires a positive CPU claim"})
	}
	if l.Request.Profile == "rust-build" {
		return m.launchCargo(l, r)
	}
	if err := childowner.Available(); err != nil {
		return m.launchFailed(l, err)
	}
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
	if !m.leaseLive(l) {
		f.Close()
		return m.launchFailed(l, model.Err("lease_inactive", "elapsed entitlement unavailable before process start"))
	}
	if e = cmd.Start(); e != nil {
		f.Close()
		return m.launchFailed(l, e)
	}
	if r.sshRuntime != nil {
		r.sshRuntime.startCopies()
	}
	j := &job{cmd: cmd, done: make(chan struct{}), nativeDone: make(chan struct{}), sshRuntime: r.sshRuntime, leaseHost: l.Host, leaseOwner: l.Owner, leaseRequest: l.Request}
	if r.sshRuntime != nil {
		h := r.sshRuntime.leaseHold
		h.job = j
		h.nativeStarted = true
	}
	currentOwner, ownerErr := childowner.New(cmd)
	if ownerErr != nil {
		_ = reader.Close()
		_ = writer.Close()
		_ = f.Close()
		// An unexpected loss of the native child binding is not permission to
		// block in Wait or discard its original handle. The private gate is closed.
		j.retireControls()
		m.jobs[l.ID] = j
		if r.sshRuntime == nil {
			close(j.done)
		} else {
			close(j.nativeDone)
		}
		l.State = "needs_attention"
		l.Ended = nil
		l.PID = cmd.Process.Pid
		l.Process = j.identity
		l.Revision++
		l.Failure = "Original gated child binding is uncertain: " + ownerErr.Error()
		if r.sshRuntime != nil {
			return m.stageSSHResult(l)
		}
		return l, m.put(l, "lease.supervision_unknown")
	}
	j.childOwner = currentOwner
	identity, identityErr := usage.Capture(cmd.Process.Pid)
	if identityErr == nil && identity.PGID == cmd.Process.Pid && identity.UID == m.owner {
		j.identity = &identity
		// The original process has not been reaped and its launch gate is
		// still closed. A missing native anchor only disables usage credit.
		j.cpuAnchor, _ = usage.CaptureCPUAnchor(identity)
		l.Process = &identity
		l.Capabilities = append(append([]string{}, l.Capabilities...), "supervised-usage-observation")
	} else {
		j.observationError = "native process identity unavailable; usage cannot be attributed"
	}
	if err := m.captureGradleLaunch(&l, j.identity); err != nil {
		current, joined, joinErr := m.joinUnstarted(l, j, reader, writer, f)
		if !joined {
			return current, joinErr
		}
		return m.launchFailed(current, err)
	}
	if (l.Request.UsagePolicy != nil || r.sshRuntime != nil) && j.identity == nil {
		current, joined, joinErr := m.joinUnstarted(l, j, reader, writer, f)
		if !joined {
			return current, joinErr
		}
		return m.launchFailed(current, model.Err("observation_unavailable", "usage policy requires a captured process identity; command was not authorized to start"))
	}
	if l.Request.UsagePolicy != nil {
		l.Capabilities = append(l.Capabilities, "observed-usage-policy")
	}

	m.jobs[l.ID] = j
	l.PID = cmd.Process.Pid
	l.State = "active"
	l.Revision++
	e = m.put(l, "lease.started")
	if e == nil && r.sshRuntime != nil {
		h := r.sshRuntime.leaseHold
		h.expectedPID = l.PID
		if l.Process != nil {
			identity := *l.Process
			h.expectedProcess = &identity
		} else {
			h.expectedProcess = nil
		}
	}
	if e != nil {
		_ = j.signalGroup(syscall.SIGKILL)
	} else if reason := m.startDeadlineProblem(l); reason != nil {
		current, joined, joinErr := m.joinUnstarted(l, j, reader, writer, f)
		if !joined {
			return current, joinErr
		}
		return m.rejectUnstarted(current, reason)
	} else if !m.leaseLive(l) {
		// The requested workload never received the gate. Closing our original
		// endpoints makes the fixed shell exit. Keep its original owner and let the
		// sole existing waiter prove exit; never discard it on a failed Kill/Wait.
		_ = reader.Close()
		_ = writer.Close()
		l.State = "ending"
		l.Outcome = "expired"
		if _, err := m.leaseRemaining(l); err != nil {
			l.Outcome = "lease_clock_uncertain"
		}
		l.Failure = "Elapsed entitlement unavailable before process gate"
		l.Revision++
		e = m.put(l, "lease.ending")

	} else if r.sshRuntime != nil && r.sshRuntime.check() != nil {
		current, joined, joinErr := m.joinUnstarted(l, j, reader, writer, f)
		if !joined {
			return current, joinErr
		}
		return m.launchFailed(current, model.Err("ssh_cancelled", "SSH authority/stream expired before process gate"))

	} else {
		delete(m.startClocks, l.ID)
		_, e = writer.Write([]byte("go\n"))
		if e != nil {
			_ = j.signalGroup(syscall.SIGKILL)
		}
	}
	m.wg.Go(func() {
		waited := j.childOwner.Wait(context.Background())
		err := waited.WaitError
		j.cpuExited.Store(waited.Reaped)
		sshOutcome := ""
		if r.sshRuntime != nil && r.sshRuntime.ctx.Err() != nil {
			sshOutcome = "interrupted"
			if errors.Is(r.sshRuntime.ctx.Err(), context.DeadlineExceeded) {
				sshOutcome = "expired"
			}
		}
		close(j.nativeDone)
		f.Close()
		deadline := time.Now().Add(2 * time.Second)
		for waited.Reaped && time.Now().Before(deadline) && !errors.Is(syscall.Kill(-cmd.Process.Pid, 0), syscall.ESRCH) {
			time.Sleep(10 * time.Millisecond)
		}
		groupGone := waited.Reaped && waited.CleanupError == nil && errors.Is(syscall.Kill(-cmd.Process.Pid, 0), syscall.ESRCH)
		m.mu.Lock()
		defer m.mu.Unlock()
		if r.sshRuntime == nil {
			defer close(j.done)
		}
		if waited.Reaped && r.sshRuntime == nil {
			delete(m.jobs, l.ID)
		}
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
			m.failSSHSettlement(l.ID, ge)
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
		if waited.Reaped {
			current.ExitCode = &code
		} else {
			current.ExitCode = nil
		}
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
			if waited.CleanupError != nil {
				current.Failure = waited.CleanupError.Error()
			}
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
		if bound, bindErr := m.syncLeaseBound(current.ID); bindErr != nil || bound {
			current.State = "needs_attention"
			current.Ended = nil
			current.Failure = "Sync effect and workspace recovery remains charged after worker exit"
			if bindErr != nil {
				current.Failure += ": " + bindErr.Error()
			}
		}
		current.Revision++
		if r.sshRuntime != nil {
			h := r.sshRuntime.leaseHold
			h.nativeReaped = waited.Reaped
			h.nativeSafe = groupGone
			_, _ = m.stageSSHResultFor(l.ID, current)
		} else {
			_ = m.put(current, "lease."+current.Outcome)
		}
	})
	return l, e
}

func (m *Manager) launchFailed(l model.Lease, err error) (model.Lease, error) {
	if bound, bindErr := m.syncLeaseBound(l.ID); bindErr != nil || bound {
		if bindErr != nil {
			m.failSSHSettlement(l.ID, bindErr)
			return l, bindErr
		}
		err := m.retainSyncLease(&l, "sync_launch_failed")
		if m.sshTransports[l.ID] != nil {
			if err != nil {
				m.failSSHSettlement(l.ID, err)
				return l, err
			}
			return m.stageSSHResult(l)
		}
		return l, err
	}
	now := time.Now().UTC()
	l.State = "terminal"
	l.Outcome = "launch_failed"
	l.Failure = err.Error()
	l.Ended = &now
	if len(l.Ports) > 0 {
		m.finishPorts(&l)
	}
	l.Revision++
	if m.sshTransports[l.ID] != nil {
		return m.stageSSHResult(l)
	}
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
	if m.sshTransports[id] != nil {
		return l, model.Err("needs_attention", "current original SSH transport hold must join before any reconciliation")
	}

	if l.State == "terminal" {
		return l, nil
	}
	var savedRun sql.NullString
	if err := m.db.QueryRow(`SELECT run FROM leases WHERE id=?`, id).Scan(&savedRun); err != nil {
		return l, err
	}
	if savedRun.Valid {
		sshSaved, err := recordedSSHRun(savedRun.String)
		if err != nil {
			return l, err
		}
		if sshSaved {
			return l, model.Err("needs_attention", "recovered SSH requires original transport join evidence; a saved PID cannot settle it")
		}
	}
	if bound, err := m.syncLeaseBound(l.ID); err != nil {
		return l, err
	} else if bound {
		return l, model.Err("needs_attention", "sync reservation requires exact effect and native cleanup evidence")
	}
	if l.Request.Kind == relayTransitKind || l.Request.Kind == syncPreparationKind || l.Request.Kind == forwardListenerKind {
		return l, model.Err("needs_attention", "in-process work requires its original join and cleanup evidence; a saved PID is never closure evidence")
	}
	if l.Cargo != nil || l.Request.Profile == "rust-build" {
		return m.reconcileCargo(l)
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
	_, e := cargoprofile.Parse(args, cpu)
	if e != nil {
		return model.Err("profile_conflict", e.Error())
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
