package manager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/observe"
)

// BenchmarkStoragePool is opt-in. It uses ordinary local admission, retains the
// original leases until native I/O and scratch cleanup return, and never probes
// unrelated paths or changes a running workload's placement.
func (m *Manager) BenchmarkStoragePool(ctx context.Context, name string, size int64, reason, agent string) (result model.StorageBenchmarkResult, err error) {
	if strings.TrimSpace(reason) == "" || len(reason) > 4096 || size < 8<<20 || size > 1<<30 || size%(1<<20) != 0 {
		return result, model.Err("invalid_request", "benchmark requires reason and whole-MiB size between 8MiB and 1GiB")
	}
	m.mu.Lock()
	if m.closing || m.storageBenchmarkBusy {
		m.mu.Unlock()
		return result, model.Err("operation_busy", "another benchmark or shutdown is in progress")
	}
	p := m.storagePools[name]
	if p == nil || !p.enabled {
		m.mu.Unlock()
		return result, model.Err("storage_unavailable", "select a configured storage pool")
	}
	volume := p.record.Volume
	m.storageBenchmarkBusy = true
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.storageBenchmarkBusy = false; m.mu.Unlock(); m.wg.Done() }()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	ttl := 2 * time.Minute
	store, err := m.Acquire(model.Request{Kind: "storage", StoragePool: name, Reason: reason, Agent: agent, Key: local.ID(), Resources: model.Resources{Disk: size + 1<<20}, TTL: ttl}, nil)
	if err != nil {
		return result, err
	}
	if store.State != "active" {
		return result, fmt.Errorf("benchmark storage is not active: %s", store.State)
	}
	result.StorageLease = store.ID
	result.CleanupState = "pending"

	// Runtime holds prevent expiry, cancellation and shutdown from returning
	// resources or cleaning scratch before the original native worker joins.
	m.mu.Lock()
	if m.storageBenchmarkLeases == nil {
		m.storageBenchmarkLeases = map[string]context.CancelFunc{}
	}
	current, checkErr := m.get(store.ID)
	if checkErr != nil || current.State != "active" || !m.leaseLive(current) {
		m.mu.Unlock()
		m.StorageCleanup(store.ID)
		return result, model.Err("storage_unavailable", "benchmark storage ended before worker admission")
	}
	m.storageBenchmarkLeases[store.ID] = cancel
	m.storageJobs[store.ID] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.storageBenchmarkLeases, store.ID)
		delete(m.storageJobs, store.ID)
		lease, e := m.get(store.ID)
		if e == nil && lease.State == "ending" {
			m.scheduleStorage(store.ID)
		}
		m.mu.Unlock()
		if e == nil && lease.State != "ending" {
			lease, e = m.StorageCleanup(store.ID)
		}
		if e == nil {
			result.CleanupState = lease.State
		}
		err = errors.Join(err, e)
	}()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	cpu, err := m.Acquire(model.Request{Reason: reason, Agent: agent, Key: local.ID(), Resources: model.Resources{CPU: 1, Memory: 8 << 20}, TTL: ttl}, nil)
	if err != nil {
		return result, err
	}
	m.mu.Lock()
	current, checkErr = m.get(cpu.ID)
	if checkErr != nil || current.State != "active" || !m.leaseLive(current) {
		m.mu.Unlock()
		return result, model.Err("insufficient_capacity", "benchmark CPU ended before worker admission")
	}
	m.storageBenchmarkLeases[cpu.ID] = cancel
	result.ExecutionLease = cpu.ID
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.storageBenchmarkLeases, cpu.ID)
		lease, e := m.get(cpu.ID)
		if e == nil && lease.State == "ending" {
			stamp := time.Now().UTC()
			lease.State = "terminal"
			lease.Ended = &stamp
			lease.Revision++
			e = m.put(lease, "storage.benchmark_joined")
		}
		m.mu.Unlock()
		if e == nil && lease.State != "terminal" {
			_, e = m.End(cpu.ID, "released")
		}
		err = errors.Join(err, e)
	}()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	runner := m.storageBenchmarkRunner
	if runner == nil {
		runner = func(ctx context.Context, path, volume string, size int64) (model.StoragePerformance, error) {
			return observe.BenchmarkStorageOwned(ctx, path, volume, size, *store.Artifact.Identity)
		}
	}
	// Persist knowledge of possible writes BEFORE native I/O. Even after the
	// scratch name disappears, clones/snapshots can retain its physical blocks.
	m.mu.Lock()
	intent, intentErr := m.get(store.ID)
	if intentErr == nil && intent.State == "active" && m.leaseLive(intent) && intent.Artifact != nil {
		intent.Artifact.ContentObserved = true
		intent.Revision++
		intentErr = m.put(intent, "storage.benchmark_write_intent")
	} else if intentErr == nil {
		intentErr = model.Err("storage_unavailable", "benchmark storage ended before write intent")
	}
	m.mu.Unlock()
	if intentErr != nil {
		return result, intentErr
	}
	metrics, runErr := runner(ctx, store.ManagedDir, volume, size)
	result.StoragePerformance = metrics
	err = runErr
	if err != nil {
		return result, err
	}
	if err = m.RecordStoragePerformance(name, result.StoragePerformance); err != nil {
		return result, err
	}
	return result, nil
}
