package manager

import (
	"context"
	"math"
	"strings"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/observe"
)

const diskStaleAfter = 3 * time.Second

func (m *Manager) sampleDisk() {
	// A native statfs syscall cannot be cancelled. Bound callers and permit only
	// one in-flight collector; a stuck syscall cannot grow sampler goroutines or
	// keep Close waiting indefinitely. The worker never mutates manager state.
	select {
	case m.diskSampleSlots <- struct{}{}:
	default:
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	type diskSamples struct {
		reading model.DiskAvailability
		pools   map[string]storagePoolReading
	}
	result := make(chan diskSamples, 1)
	paths := map[string]string{}
	for name, p := range m.storagePools {
		paths[name] = p.record.Path
	}
	m.observationMu.RLock()
	collect := m.diskCollector
	m.observationMu.RUnlock()
	if collect == nil {
		collect = observe.DiskAvailability
	}
	observed := time.Now()
	go func() {
		defer func() { <-m.diskSampleSlots }()
		samples := diskSamples{reading: collect(ctx, m.home), pools: map[string]storagePoolReading{}}
		for name, path := range paths {
			samples.pools[name] = storagePoolReading{observation: observe.NativeDiskAvailability(ctx, path), at: observed}
		}
		result <- samples
	}()
	var reading model.DiskAvailability
	select {
	case samples := <-result:
		reading = samples.reading
		m.observationMu.Lock()
		for name, sample := range samples.pools {
			p := m.storagePools[name]
			sample.observation.Scope = "storage-location-filesystem:" + sample.observation.Volume
			sample.observation.Detail = strings.ReplaceAll(sample.observation.Detail, "ARM's state filesystem", "the selected storage filesystem")
			if sample.observation.Volume != p.location.FilesystemID {
				sample.observation.Available = nil
				sample.observation.Detail = "Storage filesystem identity changed; no headroom assumed"
			}
			m.storagePoolReadings[name] = sample
		}
		m.observationMu.Unlock()
	case <-ctx.Done():
		reading = model.DiskAvailability{At: time.Now().UTC(), Scope: "state-filesystem", Method: "statfs:f_bavail", Quality: "unavailable", Detail: "Filesystem availability collection timed out; no free capacity is assumed"}
	}
	m.observationMu.Lock()
	if reading.Available != nil && reading.Volume != "" {
		if m.diskScopeVolume == "" {
			m.diskScopeVolume = reading.Volume
		} else if m.diskScopeVolume != reading.Volume {
			reading.Available = nil
			reading.Quality = "unavailable"
			reading.Detail = "State filesystem identity changed; no headroom is assumed until the authority is restarted and its paths revalidated"
		}
	}
	m.diskAvailability = reading
	m.diskObserved = observed
	m.diskPressureTick, _ = pressureNativeTick()
	m.observationMu.Unlock()
}
func (m *Manager) diskBudget(reserved int64) model.DiskBudget {
	reserved, serviceQuantityKnown := m.standingDiskReserved(reserved)
	m.observationMu.RLock()
	reading, at := m.diskAvailability, m.diskObserved
	m.observationMu.RUnlock()
	r := budgetFromDisk(reading, at, m.capacity.Disk, reserved)
	if !serviceQuantityKnown {
		r.Known = false
		r.Available = 0
		r.Capacity = 0
		r.Deficit = reserved
	}
	return r
}
func budgetFromDisk(reading model.DiskAvailability, at time.Time, ceilingBytes, reserved int64) model.DiskBudget {
	age, wallAge := time.Since(at), time.Since(reading.At)
	r := model.DiskBudget{Observation: reading, Reserved: reserved, Age: max(age, wallAge), StaleAfter: diskStaleAfter, Accounting: "Live nonprivileged state-filesystem headroom minus full outstanding disk promises; shared files, snapshots and actual allocated bytes are not credited as exclusive reclamation"}
	r.Stale = at.IsZero() || reading.At.IsZero() || age < 0 || wallAge < 0 || r.Age > diskStaleAfter
	if ceilingBytes > 0 {
		ceiling := ceilingBytes
		r.Ceiling = &ceiling
	}
	r.Known = !r.Stale && reading.Total != nil && *reading.Total > 0 && reading.Available != nil && *reading.Available >= 0 && *reading.Available <= *reading.Total
	if r.Known {
		r.Capacity = *reading.Available
		if r.Ceiling != nil {
			r.Capacity = min(r.Capacity, *r.Ceiling)
		}
		r.Available = max(0, r.Capacity-reserved)
		r.Deficit = max(0, reserved-r.Capacity)
	}
	return r
}
func (m *Manager) diskRenewal(leases []model.Lease, l model.Lease) error {
	if l.Request.Resources.Disk == 0 {
		return nil
	}
	reserved, err := diskPromises(leases)
	if err != nil {
		return err
	}
	budget := m.diskBudget(reserved)
	if l.Request.StoragePool != "" {
		budget = m.storageBudget(l.Request.StoragePool, leases)
	}
	if !budget.Known || budget.Deficit > 0 {
		return model.Err("renewal_denied", "fresh state-filesystem disk headroom cannot cover outstanding promises")
	}
	return nil
}

func diskPromises(leases []model.Lease) (int64, error) {
	var reserved int64
	for _, lease := range leases {
		switch lease.State {
		case "queued", "terminal":
			continue
		case "active", "provisioning", "ending", "reclaiming", "needs_attention":
		default:
			return 0, model.Err("invalid_accounting", "unknown lifecycle cannot establish disk promises")
		}
		if lease.Request.StoragePool == "" && lease.StorageBinding != nil {
			return 0, model.Err("invalid_accounting", "default disk claim has an unexpected named binding")
		}
		if lease.Request.StoragePool != "" && (lease.StorageBinding == nil || lease.StorageBinding.Name != lease.Request.StoragePool || lease.StorageBinding.Volume == "" || lease.StorageBinding.Backing == "") {
			return 0, model.Err("invalid_accounting", "named storage lease lacks exact backing binding")
		}
		if lease.StorageBinding != nil && !lease.StorageBinding.SharesDefault {
			continue
		}
		n := lease.Request.Resources.Disk
		if n < 0 || n > math.MaxInt64-reserved {
			return 0, model.Err("invalid_accounting", "disk promises are negative or overflow int64")
		}
		reserved += n
	}
	return reserved, nil
}
