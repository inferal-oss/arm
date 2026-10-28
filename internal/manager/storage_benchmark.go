package manager

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/observe"
)

// A process-local original, never reconstructed from a current ledger row.
// Its deadline changes only after an acknowledged ordinary renewal.
type storageBenchmarkHold struct {
	lease  model.Lease
	cancel context.CancelFunc
}

func storageBenchmarkIdentity(id string, l model.Lease, h *storageBenchmarkHold) bool {
	if h == nil || h.cancel == nil || !retainedLeaseBinding(id, l, h.lease) || l.ManagedDir != h.lease.ManagedDir {
		return false
	}
	if l.Artifact == nil || h.lease.Artifact == nil {
		return l.Artifact == nil && h.lease.Artifact == nil
	}
	return reflect.DeepEqual(l.Artifact.Identity, h.lease.Artifact.Identity)
}

func storageBenchmarkBinding(id string, l model.Lease, h *storageBenchmarkHold) bool {
	return storageBenchmarkIdentity(id, l, h) && retainedDeadlineBinding(l, h.lease)
}

// Explicit withdrawal addresses the registered original even if SQL is unreadable.
// It grants neither settlement nor authority over a replacement row.
func (m *Manager) cancelRegisteredStorageBenchmark(id string) {
	if h := m.storageBenchmarkLeases[id]; h != nil && h.cancel != nil && h.lease.ID == id && h.lease.Host == m.host && h.lease.Owner == m.owner {
		h.cancel()
	}
}

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
		m.storageBenchmarkLeases = map[string]*storageBenchmarkHold{}
	}
	storeHold := &storageBenchmarkHold{lease: store, cancel: cancel}
	current, checkErr := m.get(store.ID)
	if checkErr != nil || current.State != "active" || !storageBenchmarkBinding(store.ID, current, storeHold) || !m.leaseLive(current) {
		if checkErr == nil && storageBenchmarkBinding(store.ID, current, storeHold) {
			_, _ = m.storageCleanup(store.ID)
		}
		m.mu.Unlock()
		return result, model.Err("storage_unavailable", "benchmark storage ended before worker admission")
	}
	storeHold.lease = current
	m.storageBenchmarkLeases[store.ID] = storeHold
	m.storageJobs[store.ID] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.storageBenchmarkLeases, store.ID)
		lease, e := m.get(store.ID)
		if e == nil && !storageBenchmarkBinding(store.ID, lease, storeHold) {
			e = model.Err("needs_attention", "joined benchmark storage binding differs; obligations remain charged")
		}
		if e == nil {
			delete(m.storageJobs, store.ID)
		}
		if e == nil && lease.State == "ending" {
			m.scheduleStorage(store.ID)
		}
		if e == nil && lease.State != "ending" {
			lease, e = m.storageCleanup(store.ID)
		}
		m.mu.Unlock()
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
	if checkErr != nil || current.State != "active" || !storageBenchmarkBinding(cpu.ID, current, &storageBenchmarkHold{lease: cpu, cancel: cancel}) || !m.leaseLive(current) {
		m.mu.Unlock()
		return result, model.Err("insufficient_capacity", "benchmark CPU ended before worker admission")
	}
	cpuHold := &storageBenchmarkHold{lease: current, cancel: cancel}
	m.storageBenchmarkLeases[cpu.ID] = cpuHold
	result.ExecutionLease = cpu.ID
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.storageBenchmarkLeases, cpu.ID)
		lease, e := m.get(cpu.ID)
		if e == nil && !storageBenchmarkBinding(cpu.ID, lease, cpuHold) {
			e = model.Err("needs_attention", "joined benchmark CPU binding differs; obligations remain charged")
		}
		if e == nil && lease.State == "ending" {
			stamp := time.Now().UTC()
			lease.State = "terminal"
			lease.Ended = &stamp
			lease.Revision++
			e = m.put(lease, "storage.benchmark_joined")
		}
		if e == nil && lease.State != "terminal" {
			_, e = m.end(cpu.ID, "released")
		}
		m.mu.Unlock()
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
	if intentErr == nil && intent.State == "active" && storageBenchmarkBinding(store.ID, intent, storeHold) && m.leaseLive(intent) && intent.Artifact != nil {
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
