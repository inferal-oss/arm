package manager

import (
	"testing"
)

func TestStoragePoolReviewQueuedGrantsShareOneBackingSnapshot(t *testing.T) {
	m, _, _ := poolManager(t)
	m.defaultStorageLocation.Backing = "independent-default-review"
	backing := m.storagePools["scratch"].record.Backing
	poolReading(m, "scratch", backing, 0)
	ids := []string{}
	for _, key := range []string{"queued-one", "queued-two"} {
		request := namedStorage(key, 600)
		request.Queue = true
		lease, err := m.Acquire(request, nil)
		if err != nil || lease.State != "queued" {
			t.Fatalf("queue seed: %v %s", err, lease.State)
		}
		ids = append(ids, lease.ID)
	}
	poolReading(m, "scratch", backing, 1000)
	m.tick()
	held := int64(0)
	states := []string{}
	for _, id := range ids {
		lease, err := m.Inspect(id)
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, lease.State)
		if lease.State != "queued" && lease.State != "terminal" {
			held += lease.Request.Resources.Disk
		}
	}
	if held > 1000 {
		t.Fatalf("queued grants overcommitted shared backing: held=%d capacity=1000 states=%v", held, states)
	}
	for _, id := range ids {
		lease, _ := m.Inspect(id)
		if lease.State == "active" {
			_, _ = m.StorageHandoff(id)
		} else if lease.State == "queued" {
			_, _ = m.End(id, "cancelled")
		}
	}
}
