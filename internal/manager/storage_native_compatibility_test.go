package manager

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
)

func TestProviderPairC2NamedStorageNamespaceRetainsFloors(t *testing.T) {
	for _, variant := range []string{"absent", "format1", "format3", "format2", "format-null", "format-blob", "format-alias", "pools", "pools-null", "pools-alias", "pools-unicode-alias", "oversized-unrelated", "oversized-and-alias"} {
		t.Run(variant, func(t *testing.T) {
			f := c2Fixture(t, true)
			l := c2Lease(t, f, strings.Repeat("7", 32), "active", model.Resources{CPU: .125, Disk: 9}, false)
			switch variant {
			case "format1":
				c2Exec(t, f, `INSERT INTO metadata VALUES('workspace_format','1')`)
			case "format3":
				c2Exec(t, f, `INSERT INTO metadata VALUES('workspace_format','3')`)
			case "format2":
				c2Exec(t, f, `INSERT INTO metadata VALUES('workspace_format','2')`)
			case "format-null":
				c2Exec(t, f, `INSERT INTO metadata VALUES('workspace_format',NULL)`)
			case "format-blob":
				c2Exec(t, f, `INSERT INTO metadata VALUES('workspace_format',CAST('1' AS BLOB))`)
			case "format-alias":
				c2Exec(t, f, `INSERT INTO metadata VALUES('Workspace_Format','1')`)
			case "pools":
				c2Exec(t, f, `INSERT INTO metadata VALUES('storage_pools','[]')`)
			case "pools-null":
				c2Exec(t, f, `INSERT INTO metadata VALUES('storage_pools',NULL)`)
			case "pools-alias":
				c2Exec(t, f, `INSERT INTO metadata VALUES('Storage_Pools','[]')`)
			case "pools-unicode-alias":
				c2Exec(t, f, `INSERT INTO metadata VALUES('ſtorage_pools','[]')`)
			case "oversized-unrelated", "oversized-and-alias":
				// An unrelated long key neither becomes a truncated canonical key
				// nor hides another, unsupported alias in the same snapshot.
				c2Exec(t, f, `INSERT INTO metadata VALUES(?, '1')`, "workspace_format"+strings.Repeat("ſ", 64<<10))
				if variant == "oversized-and-alias" {
					c2Exec(t, f, `INSERT INTO metadata VALUES('ſtorage_pools','[]')`)
				}
			}
			p, err := f.m.observeProviderPairInventory(t.Context())
			valid := variant == "absent" || variant == "format1" || variant == "oversized-unrelated"
			if (err == nil) != valid || (p.state == "covered") != valid || p.physical.cpuMillis != 1125 || p.physical.diskBytes != (64<<20)+9 || p.pairs != 1 || p.ordinaryRows != 3 {
				t.Fatal("namespace refusal erased duties or granted coverage", p, err)
			}
			var raw string
			if err := f.m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&raw); err != nil || raw != string(c1JSON(t, l)) {
				t.Fatal("observation changed ordinary obligation", err)
			}
		})
	}
}

func TestProviderPairC2StorageFieldsStayClosed(t *testing.T) {
	for _, place := range []string{"lease", "request", "run"} {
		for _, variant := range []string{"absent", "empty", "null", "alias"} {
			t.Run(place+"/"+variant, func(t *testing.T) {
				f := c2Fixture(t, false)
				l := c2Lease(t, f, strings.Repeat("8", 32), "active", model.Resources{CPU: .125, Disk: 9}, place == "run")
				var raw string
				column := "data"
				if place == "run" {
					column = "run"
				}
				if err := f.m.db.QueryRow(`SELECT `+column+` FROM leases WHERE id=?`, l.ID).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var obj map[string]json.RawMessage
				if err := json.Unmarshal([]byte(raw), &obj); err != nil {
					t.Fatal(err)
				}
				target := obj
				key, empty := "storage_pool", `""`
				if place == "lease" {
					key, empty = "storage_binding", `{}`
				} else if place == "request" {
					target = map[string]json.RawMessage{}
					if err := json.Unmarshal(obj["request"], &target); err != nil {
						t.Fatal(err)
					}
				}
				if variant != "absent" {
					if variant == "alias" {
						key = strings.ToUpper(key)
					}
					value := empty
					if variant == "null" {
						value = "null"
					}
					target[key] = json.RawMessage(value)
				}
				if place == "request" {
					obj["request"] = c1JSON(t, target)
				}
				changed := string(c1JSON(t, obj))
				c2Exec(t, f, `UPDATE leases SET `+column+`=? WHERE id=?`, changed, l.ID)
				p, err := f.m.observeProviderPairInventory(t.Context())
				if (err == nil) != (variant == "absent") || (p.state == "covered") != (variant == "absent") || p.physical.cpuMillis != 125 || p.physical.diskBytes != 9 {
					t.Fatal("new model fields widened closed census or erased floor", p, err)
				}
			})
		}
	}
}

// This runner is deliberately held after observing cancellation: no completion
// or capacity return can be inferred from cancel alone.
func TestStorageBenchmarkOriginalBindingAndJoin(t *testing.T) {
	for _, variant := range []string{"expired-cpu", "expired-storage", "request", "deadline", "storage-binding", "unreadable", "close-unreadable"} {
		t.Run(variant, func(t *testing.T) {
			m, _, _ := poolManager(t)
			m.capacity.Memory = 64 << 20
			entered, cancelled, join := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			var runnerContext context.Context
			m.storageBenchmarkRunner = func(ctx context.Context, _, _ string, _ int64) (model.StoragePerformance, error) {
				runnerContext = ctx
				close(entered)
				<-ctx.Done()
				close(cancelled)
				<-join
				return model.StoragePerformance{}, ctx.Err()
			}
			done := make(chan error, 1)
			go func() {
				_, err := m.BenchmarkStoragePool(t.Context(), "scratch", 8<<20, "binding and original join", "")
				done <- err
			}()
			t.Cleanup(func() {
				m.mu.Lock()
				for id := range m.storageBenchmarkLeases {
					m.cancelRegisteredStorageBenchmark(id)
				}
				m.mu.Unlock()
				once.Do(func() { close(join) })
			})
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("runner not admitted")
			}
			m.mu.Lock()
			var l model.Lease
			for _, h := range m.storageBenchmarkLeases {
				if (h.lease.Request.Kind == "storage") == (variant == "expired-storage" || variant == "storage-binding") {
					l = h.lease
					break
				}
			}
			// Capture the acknowledged write-intent body, not the pre-intent revision.
			original, err := m.get(l.ID)
			if err != nil {
				m.mu.Unlock()
				t.Fatal(err)
			}
			raw, err := json.Marshal(original)
			if err != nil {
				m.mu.Unlock()
				t.Fatal(err)
			}
			var changed model.Lease
			if err := json.Unmarshal(raw, &changed); err != nil {
				m.mu.Unlock()
				t.Fatal(err)
			}
			switch variant {
			case "request":
				changed.Request.Key += "-replacement"
			case "deadline":
				stamp := changed.Expires.Add(time.Second)
				changed.Expires = &stamp
			case "storage-binding":
				changed.StorageBinding.Backing += "-replacement"
			}
			body, err := json.Marshal(changed)
			if err != nil {
				m.mu.Unlock()
				t.Fatal(err)
			}
			if variant == "unreadable" || variant == "close-unreadable" {
				body = []byte(`{`)
			}
			if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(body), l.ID); err != nil {
				m.mu.Unlock()
				t.Fatal(err)
			}
			validExpiry := variant == "expired-cpu" || variant == "expired-storage"
			if variant != "unreadable" && variant != "close-unreadable" {
				sample, err := m.readLeaseClock()
				if err != nil {
					m.mu.Unlock()
					t.Fatal(err)
				}
				sample.Tick += int64(3 * time.Minute)
				sample.Wall = sample.Wall.Add(3 * time.Minute)
				m.leaseClockRead = func() (leaseclock.Sample, error) { return sample, nil }
				rows, err := m.checkpointExpiryLeases()
				if err != nil {
					m.mu.Unlock()
					t.Fatal(err)
				}
				a := &automaticExpiryEnding{roster: &automaticExpiryRoster{rows: keyedRetainedRows(rows), sample: sample, validated: map[string]bool{}}}
				ending, err := m.endAutomaticExpiry(l.ID, "expired", a)
				if (err == nil) != validExpiry || (validExpiry && ending.State != "ending") {
					m.mu.Unlock()
					t.Fatal("expiry did not respect original binding", ending.State, err)
				}
			}
			var closeDone chan error
			if !validExpiry {
				if runnerContext.Err() != nil {
					m.mu.Unlock()
					t.Fatal("changed/unreadable row cancelled original automatically")
				}
				if variant == "close-unreadable" {
					closeDone = make(chan error, 1)
					m.mu.Unlock()
					go func() { closeDone <- m.Close() }()
					select {
					case <-cancelled:
					case <-time.After(5 * time.Second):
						t.Fatal("Close did not withdraw original before reading broken SQL")
					}
					select {
					case <-closeDone:
						t.Fatal("Close returned before original runner joined")
					default:
					}
					m.mu.Lock()
				} else if _, err := m.end(l.ID, "cancelled"); err == nil {
					m.mu.Unlock()
					t.Fatal("explicit ending mutated changed row")
				}
				if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), l.ID); err != nil {
					m.mu.Unlock()
					t.Fatal(err)
				}
			}
			if len(m.storageBenchmarkLeases) != 2 || !m.storageJobs[findBenchmarkStorageID(m)] {
				m.mu.Unlock()
				t.Fatal("original holds released before join")
			}
			m.mu.Unlock()
			select {
			case <-cancelled:
			case <-time.After(5 * time.Second):
				t.Fatal("original context not cancelled")
			}
			select {
			case <-done:
				t.Fatal("benchmark returned before original join")
			default:
			}
			once.Do(func() { close(join) })
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancelled benchmark succeeded")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("original benchmark did not join")
			}
			if closeDone != nil {
				select {
				case err := <-closeDone:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("Close did not finish after original join")
				}
			}
		})
	}
}

func findBenchmarkStorageID(m *Manager) string {
	for id, h := range m.storageBenchmarkLeases {
		if h.lease.Request.Kind == "storage" {
			return id
		}
	}
	return ""
}

func TestRetainedStorageBindingRejectsReplacement(t *testing.T) {
	m, _, _ := poolManager(t)
	l, err := m.Acquire(namedStorage("binding-original", 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	h := &storageExportCancellation{lease: l, cancel: cancel, operation: "binding-export"}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.storageExportCancels = map[string]*storageExportCancellation{l.ID: h}
	m.storageJobs[exportJobKey(l.ID)], m.storageJobs[exportOperationJobKey(h.operation)] = true, true
	defer func() {
		delete(m.storageExportCancels, l.ID)
		delete(m.storageJobs, exportJobKey(l.ID))
		delete(m.storageJobs, exportOperationJobKey(h.operation))
	}()
	changed := l
	binding := *l.StorageBinding
	binding.Backing += "-replacement"
	changed.StorageBinding = &binding
	if retainedStorageParentBinding(changed, l) || retainedLeaseBinding(l.ID, changed, l) || m.cancelBoundRetained(checkpointExpiryLease{id: l.ID, lease: changed}) == nil || ctx.Err() != nil {
		t.Fatal("replacement storage binding acquired original cancellation")
	}
	stamp := l.Expires.Add(time.Minute)
	changed.Expires = &stamp
	m.refreshRetainedDeadline(changed)
	if !reflect.DeepEqual(h.lease.Expires, l.Expires) {
		t.Fatal("replacement storage binding refreshed original deadline")
	}
	valid := l
	valid.Expires = &stamp
	m.refreshRetainedDeadline(valid)
	if !reflect.DeepEqual(h.lease.Expires, valid.Expires) || m.cancelBoundRetained(checkpointExpiryLease{id: l.ID, lease: valid}) != nil || ctx.Err() != context.Canceled {
		t.Fatal("same original could not renew and cancel")
	}
	legacy := l
	legacy.StorageBinding = nil
	if !retainedLeaseBinding(l.ID, legacy, legacy) || retainedLeaseBinding(l.ID, l, legacy) {
		t.Fatal("nil legacy binding semantics changed")
	}
}
