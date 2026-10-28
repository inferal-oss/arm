package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/database"
	"getarm.org/internal/model"
	"getarm.org/internal/observe"
	"getarm.org/internal/storage"
)

type storagePool struct {
	config      model.StoragePoolConfig
	record      model.StoragePoolRecord
	location    model.StorageLocation
	enabled     bool
	provider    *storage.Provider
	providerErr error
}
type storagePoolReading struct {
	observation model.DiskAvailability
	at          time.Time
}

func (m *Manager) initStoragePools(settings *daemonconfig.Settings) error {
	m.storagePools = map[string]*storagePool{}
	m.storagePoolReadings = map[string]storagePoolReading{}
	var raw string
	err := m.db.QueryRow(`SELECT value FROM metadata WHERE key='storage_pools'`).Scan(&raw)
	var saved []model.StoragePoolRecord
	if err == nil {
		if len(raw) > 32768 {
			return fmt.Errorf("storage pool records exceed bound")
		}
		if err = decodePoolRecord([]byte(raw), &saved); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if len(saved) > 8 {
		return fmt.Errorf("too many durable storage pools")
	}
	for _, r := range saved {
		config := daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0", StoragePools: []model.StoragePoolConfig{{Name: r.Name, Path: r.Path}}}
		if config.Validate() != nil || m.storagePools[r.Name] != nil || r.Volume == "" && (r.Root != nil || r.Performance != nil) || r.Root != nil && (r.Root.Owner != uint32(os.Geteuid()) || r.Root.Inode == 0) {
			return fmt.Errorf("invalid storage pool record")
		}
		m.storagePools[r.Name] = &storagePool{record: r, config: model.StoragePoolConfig{Name: r.Name, Path: r.Path}}
	}
	if settings != nil {
		for _, c := range settings.StoragePools {
			p := m.storagePools[c.Name]
			if p == nil {
				p = &storagePool{record: model.StoragePoolRecord{Name: c.Name, Path: c.Path}}
				m.storagePools[c.Name] = p
			}
			if p.record.Path != c.Path {
				return fmt.Errorf("storage pool %s path is immutable; configure a new name", c.Name)
			}
			p.config = c
			p.enabled = true
		}
	}
	if len(m.storagePools) == 0 {
		return nil
	}
	if len(m.storagePools) > 8 {
		return fmt.Errorf("at most eight durable storage locations; retained locations count")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.defaultStorageLocation, err = observe.StorageLocation(ctx, m.home)
	if err != nil {
		return err
	}
	for _, name := range m.storagePoolNames() {
		p := m.storagePools[name]
		location, e := observe.StorageLocation(ctx, p.record.Path)
		if e != nil {
			p.providerErr = e
			p.location = model.StorageLocation{Path: p.record.Path, Volume: p.record.Volume, Backing: p.record.Backing, Detail: "Location unavailable; existing promises retained"}
			continue
		}
		p.location = location
		if p.record.Volume != "" && (p.record.Volume != location.Volume || p.record.Backing != location.Backing) {
			p.providerErr = fmt.Errorf("storage location identity changed; existing promises retained")
			continue
		}
		p.record.Volume, p.record.Backing = location.Volume, location.Backing
		if p.record.Root != nil {
			p.provider, p.providerErr = storage.OpenOnFilesystem(m.storagePoolRoot(p), p.record.Root, location.FilesystemID)
		}
	}
	return m.saveStoragePools()
}
func (m *Manager) storagePoolNames() []string {
	names := make([]string, 0, len(m.storagePools))
	for n := range m.storagePools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
func (m *Manager) saveStoragePools() error {
	records := make([]model.StoragePoolRecord, 0, len(m.storagePools))
	for _, n := range m.storagePoolNames() {
		records = append(records, m.storagePools[n].record)
	}
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	if len(data) > 32768 {
		return fmt.Errorf("storage records exceed bound")
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = database.CheckWorkspaceFormat(context.Background(), tx); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO metadata(key,value) VALUES('workspace_format',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, database.StoragePoolWorkspaceFormat); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO metadata(key,value) VALUES('storage_pools',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(data)); err != nil {
		return err
	}
	return tx.Commit()
}
func (m *Manager) storagePoolRoot(p *storagePool) string {
	path := p.location.Path
	if path == "" {
		path = p.record.Path
	}
	return filepath.Join(path, ".arm-"+m.host+"-"+p.record.Name)
}
func (m *Manager) closeStoragePools() error {
	var err error
	for _, p := range m.storagePools {
		if p.provider != nil {
			err = errors.Join(err, p.provider.Close())
			p.provider = nil
		}
	}
	return err
}
func (m *Manager) providerFor(l model.Lease) *storage.Provider {
	if l.Request.StoragePool == "" || l.Request.StoragePool == "default" {
		return m.storage
	}
	if p := m.storagePools[l.Request.StoragePool]; p != nil && p.providerErr == nil {
		return p.provider
	}
	return nil
}
func (m *Manager) ensurePoolStorage(name string) error {
	if name == "" || name == "default" {
		return m.ensureStorage()
	}
	p := m.storagePools[name]
	if p == nil || !p.enabled {
		return model.Err("storage_unavailable", "storage pool is not configured")
	}
	if p.providerErr != nil {
		return p.providerErr
	}
	if p.provider != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	location, err := m.observePoolLocation(ctx, p.record.Path)
	if err != nil {
		return err
	}
	if location.Volume != p.record.Volume || location.Backing != p.record.Backing || location.Path != p.location.Path {
		return fmt.Errorf("storage pool identity changed")
	}
	provider, err := storage.OpenOnFilesystem(m.storagePoolRoot(p), nil, location.FilesystemID)
	if err != nil {
		return err
	}
	identity := provider.Identity()
	previous := p.record.Root
	p.record.Root = &identity
	if err = m.saveStoragePools(); err != nil {
		p.record.Root = previous
		provider.Close()
		p.providerErr = err
		return err
	}
	p.provider = provider
	return nil
}
func (m *Manager) storageBacking(name string) string {
	if name == "" || name == "default" {
		return m.defaultStorageLocation.Backing
	}
	if p := m.storagePools[name]; p != nil {
		return p.record.Backing
	}
	return ""
}
func (m *Manager) validateStoragePool(r *model.Request) error {
	if r.StoragePool == "default" {
		r.StoragePool = ""
	}
	if r.StoragePool == "" {
		return nil
	}
	p := m.storagePools[r.StoragePool]
	if p == nil || !p.enabled {
		return model.Err("storage_unavailable", "unknown or disabled storage pool")
	}
	if p.record.Volume == "" || p.record.Backing == "" || m.defaultStorageLocation.Backing == "" || p.providerErr != nil {
		return model.Err("storage_unavailable", "storage location identity/provider unavailable; no fallback is allowed")
	}
	if r.Kind != "storage" {
		return model.Err("invalid_request", "--storage-pool is only supported for managed storage leases; attach that lease to execution")
	}
	if p.config.Ceiling > 0 && r.Resources.Disk > p.config.Ceiling {
		return model.Err("capacity_exceeded", "disk request exceeds selected storage pool ceiling")
	}
	return nil
}
func promiseState(l model.Lease) bool { return l.State != "queued" && l.State != "terminal" }
func (m *Manager) storageReservations(ls []model.Lease, name string, shared bool) (int64, error) {
	var total int64
	backing := m.storageBacking(name)
	for _, l := range ls {
		if !promiseState(l) {
			continue
		}
		selected := l.Request.StoragePool
		matches := selected == name || name == "default" && selected == "" || name == "" && selected == "default"
		if shared && backing != "" {
			matches = m.storageBacking(selected) == backing
		}
		if !matches {
			continue
		}
		n := l.Request.Resources.Disk
		if n < 0 || n > math.MaxInt64-total {
			return 0, model.Err("invalid_accounting", "invalid storage promises")
		}
		total += n
	}
	return total, nil
}
func (m *Manager) storageBudget(name string, ls []model.Lease) model.DiskBudget {
	reserved, err := m.storageReservations(ls, name, true)
	if name == "" || name == "default" {
		if err != nil {
			return model.DiskBudget{}
		}
		return m.diskBudget(reserved)
	}
	p := m.storagePools[name]
	if p == nil {
		return model.DiskBudget{}
	}
	m.observationMu.RLock()
	sample := m.storagePoolReadings[name]
	m.observationMu.RUnlock()
	r := budgetFromDisk(sample.observation, sample.at, 0, reserved)
	r.Accounting = "Live location headroom minus full promises across all locations sharing its backing; overlapping pool views must not be summed"
	if err != nil || !p.enabled || p.providerErr != nil || p.record.Backing == "" || m.defaultStorageLocation.Backing == "" {
		r.Known = false
		r.Capacity = 0
		r.Available = 0
		r.Deficit = reserved
	}
	if p.config.Ceiling > 0 {
		ceiling := p.config.Ceiling
		r.Ceiling = &ceiling
		own, e := m.storageReservations(ls, name, false)
		if e != nil {
			r.Known = false
			r.Available = 0
			return r
		}
		r.Available = min(r.Available, max(0, p.config.Ceiling-own))
		r.Deficit = max(r.Deficit, max(0, own-p.config.Ceiling))
	}
	// Other aliases can report stricter headroom (quota/reserve). Never inflate it.
	if r.Known {
		for _, other := range m.storagePoolNames() {
			if other == name || !m.storagePools[other].enabled || m.storageBacking(other) != p.record.Backing {
				continue
			}
			m.observationMu.RLock()
			s := m.storagePoolReadings[other]
			m.observationMu.RUnlock()
			b := budgetFromDisk(s.observation, s.at, 0, reserved)
			if !b.Known {
				r.Known = false
				r.Available = 0
				break
			}
			r.Available = min(r.Available, b.Available)
		}
		if p.record.Backing == m.defaultStorageLocation.Backing {
			b := m.diskBudget(reserved)
			if !b.Known {
				r.Known = false
				r.Available = 0
			} else {
				r.Available = min(r.Available, b.Available)
			}
		}
	}
	return r
}
func (m *Manager) fitsStorageRequest(r model.Request, used model.Resources, ls []model.Lease) bool {
	if r.StoragePool == "" {
		if reserved, err := diskPromises(ls); err == nil {
			used.Disk = reserved
		} else {
			return false
		}
		if r.Resources.Disk > 0 && len(m.storagePools) > 0 {
			b := m.storageBudget("", ls)
			if !b.Known || r.Resources.Disk > b.Available {
				return false
			}
		}
		return m.fitsLiveResources(r.Resources, used)
	}
	resources := r.Resources
	resources.Disk = 0
	used.Disk = 0
	if !m.fitsLiveResources(resources, used) {
		return false
	}
	b := m.storageBudget(r.StoragePool, ls)
	return b.Known && r.Resources.Disk <= b.Available
}
func (m *Manager) StoragePools() ([]model.StoragePoolView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, err := m.list()
	if err != nil {
		return nil, err
	}
	return m.storagePoolViews(ls), nil
}
func (m *Manager) storagePoolViews(ls []model.Lease) []model.StoragePoolView {
	views := []model.StoragePoolView{}
	for _, n := range m.storagePoolNames() {
		p := m.storagePools[n]
		v := model.StoragePoolView{Name: n, Tier: p.config.Tier, Enabled: p.enabled, Location: p.location, Budget: m.storageBudget(n, ls), Performance: p.record.Performance, Detail: "Named host storage; performance is advisory. Shared backing views overlap."}
		if p.providerErr != nil {
			v.Detail = p.providerErr.Error()
		}
		if v.Performance != nil {
			age := time.Since(v.Performance.At)
			// Retained profiles describe the original volume. Current device metadata
			// cannot confirm them while the location/provider identity is unavailable.
			v.PerformanceStale = p.providerErr != nil || p.location.Volume == "" ||
				v.Performance.Volume != p.location.Volume || v.Performance.Volume != p.record.Volume ||
				age < 0 || age > 7*24*time.Hour
		}
		views = append(views, v)
	}
	return views
}
func (m *Manager) appendStorageResources(s *model.Status, ls []model.Lease) {
	for _, v := range m.storagePoolViews(ls) {
		view := v
		known := v.Budget.Known
		r := model.ResourceView{ID: m.host + "/storage/" + v.Name, Host: m.host, Pool: v.Location.Backing, Kind: "storage", Unit: "bytes", BudgetTotal: model.CountQuantity(v.Budget.Capacity), Reserved: model.CountQuantity(v.Budget.Reserved), Available: model.CountQuantity(v.Budget.Available), Deficit: model.CountQuantity(v.Budget.Deficit), Accounting: v.Budget.Accounting, AvailabilityKnown: &known, Stale: v.Budget.Stale, Storage: &view, Users: []model.ResourceUse{}}
		for _, l := range ls {
			if promiseState(l) && l.Request.Resources.Disk > 0 && m.storageBacking(l.Request.StoragePool) == v.Location.Backing && v.Location.Backing != "" {
				r.Users = append(r.Users, model.ResourceUse{Lease: l.ID, Owner: l.Owner, OwnerUsername: l.OwnerUsername, Agent: l.Request.Agent, Project: l.Request.Project, Reason: l.Request.Reason, Quantity: model.CountQuantity(l.Request.Resources.Disk), State: l.State, Expires: l.Expires})
			}
		}
		s.Resources = append(s.Resources, r)
	}
}
func (m *Manager) RecordStoragePerformance(name string, performance model.StoragePerformance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.storagePools[name]
	if p == nil || !p.enabled || performance.Volume != p.record.Volume || performance.At.IsZero() || time.Since(performance.At) > 10*time.Minute || time.Until(performance.At) > time.Second || performance.Bytes < 8<<20 || performance.Bytes > 1<<30 || performance.Duration <= 0 || performance.Duration > 5*time.Minute || performance.Method == "" || len(performance.Method) > 256 || len(performance.Detail) > 2048 {
		return model.Err("invalid_request", "invalid or stale performance observation")
	}
	for _, n := range []float64{performance.ReadBytesPerSecond, performance.WriteBytesPerSecond, performance.RandomReadIOPS} {
		if n <= 0 || math.IsNaN(n) || math.IsInf(n, 0) {
			return model.Err("invalid_request", "invalid performance counters")
		}
	}
	prior := p.record.Performance
	p.record.Performance = &performance
	if err := m.saveStoragePools(); err != nil {
		p.record.Performance = prior
		return err
	}
	return nil
}

func (m *Manager) storageRequestFitsCeiling(r model.Request) bool {
	resources := r.Resources
	if r.StoragePool != "" {
		p := m.storagePools[r.StoragePool]
		if p == nil || p.config.Ceiling > 0 && resources.Disk > p.config.Ceiling {
			return false
		}
		resources.Disk = 0
	}
	return m.requestFitsCeiling(resources)
}

func (m *Manager) validateStorageBinding(l model.Lease) error {
	if l.Request.StoragePool == "" {
		if l.StorageBinding != nil {
			return model.Err("invalid_accounting", "default storage cannot carry a named backing binding")
		}
		return nil
	}
	p := m.storagePools[l.Request.StoragePool]
	b := l.StorageBinding
	if p == nil || b == nil || l.Request.Kind != "storage" || b.Name != l.Request.StoragePool || b.Volume != p.record.Volume || b.Backing == "" || b.Backing != p.record.Backing || m.defaultStorageLocation.Backing != "" && b.SharesDefault != (b.Backing == m.defaultStorageLocation.Backing) {
		return model.Err("invalid_accounting", "named storage lease and durable pool binding differ")
	}
	return nil
}

// Runtime observation boundary; nil uses the native collector. It carries no
// storage/admission authority and is never persisted or recovered from rows.
func (m *Manager) observePoolLocation(ctx context.Context, path string) (model.StorageLocation, error) {
	if m.storagePoolObserve != nil {
		return m.storagePoolObserve(ctx, path)
	}
	return observe.StorageLocation(ctx, path)
}
