package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/model"
)

func poolManager(t *testing.T) (*Manager, daemonconfig.Settings, string) {
	t.Helper()
	base := t.TempDir()
	base, e := filepath.EvalSymlinks(base)
	if e != nil {
		t.Fatal(e)
	}
	settings := daemonconfig.Settings{Resources: model.Resources{CPU: 2}, Listen: "127.0.0.1:0", StoragePools: []model.StoragePoolConfig{{Name: "scratch", Path: base, Tier: "build"}}}
	home := filepath.Join(base, "state")
	m, e := OpenConfigured(home, settings)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if !m.closing {
			m.Close()
		}
	})
	cpuReading(m, 64, 64, 0, 64, 0)
	return m, settings, home
}
func poolReading(m *Manager, name, backing string, available int64) {
	p := m.storagePools[name]
	p.record.Backing = backing
	p.providerErr = nil
	total := int64(10000)
	at := time.Now()
	m.observationMu.Lock()
	m.storagePoolReadings[name] = storagePoolReading{observation: model.DiskAvailability{At: at, Volume: p.location.FilesystemID, Total: &total, Available: &available}, at: at}
	m.observationMu.Unlock()
}
func namedStorage(key string, n int64) model.Request {
	return model.Request{Kind: "storage", StoragePool: "scratch", Resources: model.Resources{Disk: n}, Reason: "pool fixture", Key: key, TTL: time.Minute}
}
func TestStoragePoolPlacementSharedAliasesAndCeiling(t *testing.T) {
	m, _, _ := poolManager(t)
	m.capacity.Disk = 20
	m.defaultStorageLocation.Backing = "independent-state-fixture"
	poolReading(m, "scratch", m.storagePools["scratch"].record.Backing, 1000)
	s, e := m.Acquire(namedStorage("named", 500), nil)
	if e != nil || s.State != "active" {
		t.Fatal(s, e)
	}
	if !strings.HasPrefix(s.ManagedDir, m.storagePools["scratch"].record.Path+string(os.PathSeparator)+".arm-") || s.StorageBinding == nil || s.StorageBinding.SharesDefault {
		t.Fatal(s)
	}
	legacy, e := m.Acquire(diskRequest("default", 20), nil)
	if e != nil || legacy.State != "active" {
		t.Fatal(legacy, e)
	}
	status, e := m.Status()
	if e != nil || status.Reserved.Disk != 20 || len(status.Resources) != 4 {
		t.Fatal(status.Reserved, e)
	}
	m.storagePools["scratch"].config.Ceiling = 600
	if _, e = m.Acquire(namedStorage("over-pool", 601), nil); errorCode(e) != "capacity_exceeded" {
		t.Fatal(e)
	}
	if _, e = m.Acquire(namedStorage("no-space", 101), nil); errorCode(e) != "insufficient_capacity" {
		t.Fatal(e)
	}
	if _, e = m.StorageHandoff(s.ID); e != nil {
		t.Fatal(e)
	}
}
func TestStoragePoolSharedBackingCannotDoubleSpend(t *testing.T) {
	m, _, _ := poolManager(t)
	backing := m.defaultStorageLocation.Backing
	if backing == "" {
		backing = "shared-fixture"
		m.defaultStorageLocation.Backing = backing
	}
	diskReading(m, 10000, 1000, 0)
	poolReading(m, "scratch", backing, 1000)
	s, e := m.Acquire(namedStorage("shared", 700), nil)
	if e != nil {
		t.Fatal(e)
	}
	if !s.StorageBinding.SharesDefault {
		t.Fatal(s.StorageBinding)
	}
	if _, e = m.Acquire(diskRequest("overlap", 301), nil); errorCode(e) != "insufficient_capacity" {
		t.Fatal("default double-spent shared backing", e)
	}
	if _, e = m.Acquire(namedStorage("overlap2", 301), nil); errorCode(e) != "insufficient_capacity" {
		t.Fatal("alias double-spent shared backing", e)
	}
	views, e := m.StoragePools()
	if e != nil || len(views) != 1 || views[0].Budget.Available != 300 || views[0].Budget.Reserved != 700 {
		t.Fatal(views, e)
	}
	m.storagePools["scratch"].config.Ceiling = 800
	views, _ = m.StoragePools()
	if views[0].Budget.Available != 100 {
		t.Fatal(views)
	}
	m.StorageHandoff(s.ID)
}
func TestStoragePoolStaleAndMissingMountRefuseWithoutFallback(t *testing.T) {
	m, _, _ := poolManager(t)
	poolReading(m, "scratch", "separate", 1000)
	m.observationMu.Lock()
	s := m.storagePoolReadings["scratch"]
	s.at = s.at.Add(-time.Hour)
	m.storagePoolReadings["scratch"] = s
	m.observationMu.Unlock()
	if _, e := m.Acquire(namedStorage("stale", 10), nil); errorCode(e) != "insufficient_capacity" {
		t.Fatal(e)
	}
	poolReading(m, "scratch", "separate", 1000)
	m.storagePools["scratch"].enabled = false
	if _, e := m.Acquire(namedStorage("disabled", 10), nil); errorCode(e) != "storage_unavailable" {
		t.Fatal(e)
	}
	r := diskRequest("wrong-kind", 10)
	r.StoragePool = "scratch"
	m.storagePools["scratch"].enabled = true
	if _, e := m.Acquire(r, nil); errorCode(e) != "invalid_request" {
		t.Fatal(e)
	}
}
func TestStoragePoolRestartRetainsProviderAndDisabledObligations(t *testing.T) {
	m, settings, home := poolManager(t)
	s, e := m.Acquire(namedStorage("restart", 1024), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(s.ManagedDir, "keep"), []byte("owned result"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	settings.StoragePools = nil
	next, e := OpenConfigured(home, settings)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	views, e := next.StoragePools()
	if e != nil || len(views) != 1 || views[0].Enabled || views[0].Budget.Reserved != 1024 {
		t.Fatal(views, e)
	}
	if _, e = next.Acquire(namedStorage("disabled-restart", 1), nil); errorCode(e) != "storage_unavailable" {
		t.Fatal(e)
	}
	if _, e = next.StorageHandoff(s.ID); e != nil {
		t.Fatal(e)
	}
	if data, e := os.ReadFile(filepath.Join(s.ManagedDir, "keep")); e != nil || string(data) != "owned result" {
		t.Fatal(string(data), e)
	}
}
func TestStoragePoolBenchmarkCancelledAndInvalidRequestsDoNotWrite(t *testing.T) {
	m, _, _ := poolManager(t)
	before, _ := os.ReadDir(m.storagePools["scratch"].record.Path)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e := m.BenchmarkStoragePool(ctx, "scratch", 8<<20, "cancelled fixture", ""); e != context.Canceled {
		t.Fatal(e)
	}
	if _, e := m.BenchmarkStoragePool(t.Context(), "scratch", 1, "invalid fixture", ""); errorCode(e) != "invalid_request" {
		t.Fatal(e)
	}
	after, _ := os.ReadDir(m.storagePools["scratch"].record.Path)
	if len(before) != len(after) {
		t.Fatal("cancelled benchmark wrote files")
	}
}
func TestStoragePoolBenchmarkRuntimeHoldRetainsCharges(t *testing.T) {
	m := newManager(t)
	m.storageBenchmarkLeases = map[string]context.CancelFunc{}
	l, e := m.Acquire(diskRequest("held-benchmark", 10), nil)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.storageBenchmarkLeases[l.ID] = cancel
	ending, e := m.End(l.ID, "cancelled")
	if e != nil || ending.State != "ending" || ctx.Err() != context.Canceled {
		t.Fatal(ending, e)
	}
	status, e := m.Status()
	if e != nil || status.Disk.Reserved != 10 {
		t.Fatal(status.Disk, e)
	}
	delete(m.storageBenchmarkLeases, l.ID)
}

func TestStoragePoolBenchmarkCancellationJoinsBeforeCapacityReturns(t *testing.T) {
	m, _, _ := poolManager(t)
	m.capacity.Memory = 64 << 20
	entered, joined := make(chan struct{}), make(chan struct{})
	m.storageBenchmarkRunner = func(ctx context.Context, path, volume string, size int64) (model.StoragePerformance, error) {
		close(entered)
		<-ctx.Done()
		<-joined
		return model.StoragePerformance{}, ctx.Err()
	}
	result := make(chan error, 1)
	go func() {
		_, e := m.BenchmarkStoragePool(t.Context(), "scratch", 8<<20, "controlled cancellation fixture", "")
		result <- e
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("benchmark never admitted")
	}
	// Read the exact admitted CPU owner; its lifetime remains held after End.
	m.mu.Lock()
	all, e := m.list()
	m.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	cpuID := ""
	for _, l := range all {
		if l.Request.Resources.CPU > 0 {
			cpuID = l.ID
			break
		}
	}
	if cpuID == "" {
		t.Fatal("missing CPU claim")
	}
	ending, e := m.End(cpuID, "cancelled")
	if e != nil || ending.State != "ending" {
		t.Fatal(ending, e)
	}
	status, e := m.Status()
	if e != nil || status.Reserved.CPU != 1 {
		t.Fatal("capacity returned before join", status.Reserved, e)
	}
	select {
	case <-result:
		t.Fatal("returned before original runner joined")
	default:
	}
	close(joined)
	if e = <-result; e == nil {
		t.Fatal("cancelled benchmark succeeded")
	}
	status, e = m.Status()
	if e != nil || status.Reserved.CPU != 0 {
		t.Fatal(status.Reserved, e)
	}
}

func TestStoragePoolsStayOutsideInitialPeerClaims(t *testing.T) {
	m, _, _ := poolManager(t)
	r := namedStorage("remote-selector", 1)
	if _, e := m.AcquirePeer("peer", r, nil, model.Resources{CPU: 1, Disk: 1024}, time.Hour); errorCode(e) != "unsupported_remote_claim" {
		t.Fatal(e)
	}
}

func TestStoragePoolMissingLocationRemainsReadableAcrossRestart(t *testing.T) {
	m, settings, home := poolManager(t)
	if e := m.Close(); e != nil {
		t.Fatal(e)
	}
	settings.StoragePools = append(settings.StoragePools, model.StoragePoolConfig{Name: "missing", Path: filepath.Join(filepath.Dir(home), "absent")})
	for i := 0; i < 2; i++ {
		next, e := OpenConfigured(home, settings)
		if e != nil {
			t.Fatal(e)
		}
		views, e := next.StoragePools()
		if e != nil || len(views) != 2 {
			t.Fatal(views, e)
		}
		r := namedStorage("missing", 1)
		r.StoragePool = "missing"
		if _, e = next.Acquire(r, nil); errorCode(e) != "storage_unavailable" {
			t.Fatal(e)
		}
		if e = next.Close(); e != nil {
			t.Fatal(e)
		}
	}
}
func TestStoragePoolCorruptDurableIdentityRefusesRestart(t *testing.T) {
	for _, raw := range []string{`[] {}`, `[{"name":"scratch","name":"other","path":"/fixture","volume":"v","backing_pool":"b"}]`, `[{"name":"../escape","path":"/fixture","volume":"v","backing_pool":"b"}]`} {
		t.Run(raw, func(t *testing.T) {
			m, settings, home := poolManager(t)
			if _, e := m.db.Exec(`UPDATE metadata SET value=? WHERE key='storage_pools'`, raw); e != nil {
				t.Fatal(e)
			}
			if e := m.Close(); e != nil {
				t.Fatal(e)
			}
			if next, e := OpenConfigured(home, settings); e == nil {
				next.Close()
				t.Fatal("corrupt location record accepted")
			}
		})
	}
}

func TestStoragePoolDurableFormatFenceCannotBeRemovedOrDowngraded(t *testing.T) {
	for _, mutation := range []string{`DELETE FROM metadata WHERE key='workspace_format'`, `UPDATE metadata SET value='1' WHERE key='workspace_format'`, `DELETE FROM metadata WHERE key='storage_pools'`} {
		t.Run(mutation, func(t *testing.T) {
			m, settings, home := poolManager(t)
			var format string
			if e := m.db.QueryRow(`SELECT value FROM metadata WHERE key='workspace_format'`).Scan(&format); e != nil || format != "3" {
				t.Fatal(format, e)
			}
			if _, e := m.db.Exec(mutation); e != nil {
				t.Fatal(e)
			}
			if e := m.Close(); e != nil {
				t.Fatal(e)
			}
			if next, e := OpenConfigured(home, settings); errorCode(e) != "workspace_format_unsupported" {
				if next != nil {
					next.Close()
				}
				t.Fatal(e)
			}
		})
	}
}

func TestStoragePoolUnknownDefaultBackingCannotManufactureIndependence(t *testing.T) {
	m, _, _ := poolManager(t)
	m.defaultStorageLocation.Backing = ""
	if _, e := m.Acquire(namedStorage("unknown-default", 1), nil); errorCode(e) != "storage_unavailable" {
		t.Fatal(e)
	}
	views, e := m.StoragePools()
	if e != nil || views[0].Budget.Known || views[0].Budget.Available != 0 {
		t.Fatal(views, e)
	}
}

func TestStoragePoolForgedDefaultOverlapBindingRefusesRestart(t *testing.T) {
	m, settings, home := poolManager(t)
	l, e := m.Acquire(namedStorage("overlap-binding", 1), nil)
	if e != nil {
		t.Fatal(e)
	}
	if !l.StorageBinding.SharesDefault {
		t.Fatal("fixture does not share its state backing")
	}
	if _, e = m.db.Exec(`UPDATE leases SET data=json_set(data,'$.storage_binding.shares_default',json('false')) WHERE id=?`, l.ID); e != nil {
		t.Fatal(e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	if next, e := OpenConfigured(home, settings); errorCode(e) != "invalid_accounting" {
		if next != nil {
			next.Close()
		}
		t.Fatal("forged default overlap accepted", e)
	}
}

func TestStoragePoolBenchmarkWriteIntentSurvivesEmptyNamespaceCleanup(t *testing.T) {
	m, _, _ := poolManager(t)
	m.capacity.Memory = 64 << 20
	m.storageBenchmarkRunner = func(ctx context.Context, path, volume string, size int64) (model.StoragePerformance, error) {
		m.mu.Lock()
		leases, e := m.list()
		m.mu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		recorded := false
		for _, l := range leases {
			if l.ManagedDir == path && l.Artifact != nil {
				recorded = l.Artifact.ContentObserved
			}
		}
		if !recorded {
			t.Fatal("native worker reached before durable write evidence")
		}
		return model.StoragePerformance{At: time.Now().UTC(), Volume: volume, Bytes: size, BlockBytes: 1 << 20, ReadBytesPerSecond: 1, WriteBytesPerSecond: 1, RandomReadIOPS: 1, RandomReadLatencyNS: 1, Duration: time.Millisecond, Method: "controlled-no-native-write"}, nil
	}
	result, e := m.BenchmarkStoragePool(t.Context(), "scratch", 8<<20, "owned scratch intent fixture", "")
	if e != nil {
		t.Fatal(e)
	}
	if result.StorageLease == "" || result.ExecutionLease == "" {
		t.Fatal("benchmark omitted cleanup owners", result)
	}
	storageEventually(t, func() bool { l, e := m.Inspect(result.StorageLease); return e == nil && l.State == "needs_attention" })
	l, e := m.Inspect(result.StorageLease)
	if e != nil || l.Artifact == nil || !l.Artifact.ContentObserved || l.State == "terminal" {
		t.Fatal("empty namespace erased known write evidence", l, e)
	}
	// Explicit operator handoff settles logical responsibility, not physical credit.
	if _, e = m.StorageHandoff(l.ID); e != nil {
		t.Fatal(e)
	}
}
