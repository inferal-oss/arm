package manager

import (
	"path/filepath"
	"testing"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/model"
)

// Admission must account for every earlier grant in the same scheduling pass,
// even when disk availability is recomputed from the lease snapshot.
func TestStorageQueueSameTickDiskAccounting(t *testing.T) {
	for _, name := range []string{"default-headroom", "default-ceiling", "named-ceiling", "named-aliases", "default-alias"} {
		t.Run(name, func(t *testing.T) {
			var m *Manager
			var ready func()
			requests := []model.Request{diskRequest("first", 600), diskRequest("second", 600)}
			if name == "default-headroom" || name == "default-ceiling" {
				m = newManager(t)
				m.capacity.Disk = 0
				available := int64(1000)
				if name == "default-ceiling" {
					m.capacity.Disk = 1000
					available = 10000
				}
				diskReading(m, 10000, 0, 0)
				ready = func() { diskReading(m, 10000, available, 0) }
			} else {
				base, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				settings := daemonconfig.Settings{Resources: model.Resources{CPU: 2}, Listen: "127.0.0.1:0", StoragePools: []model.StoragePoolConfig{
					{Name: "scratch", Path: base}, {Name: "alias", Path: base},
				}}
				m, err = OpenConfigured(filepath.Join(base, "state"), settings)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := m.Close(); err != nil {
						t.Error(err)
					}
				})
				cpuReading(m, 64, 64, 0, 64, 0)
				backing := m.storagePools["scratch"].record.Backing
				m.defaultStorageLocation.Backing = "queue-independent-default"
				for _, pool := range []string{"scratch", "alias"} {
					poolReading(m, pool, backing, 0)
				}
				requests[0] = namedStorage("first", 600)
				requests[1] = namedStorage("second", 600)
				available := int64(1000)
				switch name {
				case "named-ceiling":
					m.storagePools["scratch"].config.Ceiling = 1000
					available = 10000
				case "named-aliases":
					requests[1].StoragePool = "alias"
				case "default-alias":
					m.defaultStorageLocation.Backing = backing
					diskReading(m, 10000, 0, 0)
					requests[1] = diskRequest("second", 600)
				}
				ready = func() {
					for _, pool := range []string{"scratch", "alias"} {
						poolReading(m, pool, backing, available)
					}
					diskReading(m, 10000, available, 0)
				}
			}
			assertQueuedDiskGrantsSerialize(t, m, requests, ready)
		})
	}
}

func assertQueuedDiskGrantsSerialize(t *testing.T, m *Manager, requests []model.Request, ready func()) {
	t.Helper()
	leases := make([]model.Lease, len(requests))
	for i, request := range requests {
		request.Queue = true
		lease, err := m.Acquire(request, nil)
		if err != nil || lease.State != "queued" {
			t.Fatalf("queue %d: state=%s error=%v", i, lease.State, err)
		}
		leases[i] = lease
	}
	ready()
	check := func(want ...string) {
		t.Helper()
		for i, state := range want {
			lease, err := m.Inspect(leases[i].ID)
			if err != nil || lease.State != state {
				t.Fatalf("lease %d: state=%s want=%s error=%v", i, lease.State, state, err)
			}
			leases[i] = lease
		}
	}
	release := func(lease model.Lease) {
		t.Helper()
		var err error
		if lease.Request.Kind == "storage" {
			_, err = m.StorageHandoff(lease.ID)
		} else {
			_, err = m.End(lease.ID, "released")
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	m.tick()
	check("active", "queued")
	ready()
	m.tick()
	check("active", "queued")
	release(leases[0])
	ready()
	m.tick()
	check("terminal", "active")
	release(leases[1])
}
