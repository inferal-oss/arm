package manager

import (
	"errors"
	"testing"
	"time"

	"getarm.org/internal/model"
)

func TestStoragePoolPerformanceViewRequiresCurrentVolumeConfirmation(t *testing.T) {
	now := time.Now().Add(-time.Minute)
	for _, tc := range []struct {
		name          string
		currentVolume string
		providerErr   error
		at            time.Time
		stale         bool
	}{
		{name: "confirmed", currentVolume: "original", at: now},
		{name: "replacement", currentVolume: "replacement", at: now, stale: true},
		{name: "unavailable", currentVolume: "original", providerErr: errors.New("location unavailable"), at: now, stale: true},
		{name: "provider-root-error", currentVolume: "original", providerErr: errors.New("managed root identity changed"), at: now, stale: true},
		{name: "unknown-volume", at: now, stale: true},
		{name: "aged", currentVolume: "original", at: now.Add(-8 * 24 * time.Hour), stale: true},
		{name: "future", currentVolume: "original", at: now.Add(time.Hour), stale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := &model.StoragePerformance{At: tc.at, Volume: "original"}
			m := &Manager{storagePools: map[string]*storagePool{
				"scratch": {enabled: true, record: model.StoragePoolRecord{Name: "scratch", Volume: "original", Performance: profile}, location: model.StorageLocation{Volume: tc.currentVolume}, providerErr: tc.providerErr},
			}}
			views := m.storagePoolViews(nil)
			if len(views) != 1 || views[0].Performance != profile || views[0].PerformanceStale != tc.stale {
				t.Fatalf("profile freshness: views=%+v want stale=%t", views, tc.stale)
			}
		})
	}
}

func TestStoragePoolReplacementVolumeProfileStaysStaleAcrossRestart(t *testing.T) {
	m, settings, home := poolManager(t)
	p := m.storagePools["scratch"]
	currentVolume := p.location.Volume
	if currentVolume == "" {
		t.Fatal("fixture has no native volume identity")
	}
	// Persist a previous mount's identity/profile, then let startup discover the
	// current native volume at the same configured path. No mount is modified.
	originalVolume := "previous-" + currentVolume
	p.record.Volume = originalVolume
	profile := model.StoragePerformance{At: time.Now().Add(-time.Minute), Volume: originalVolume, Bytes: 8 << 20, BlockBytes: 1 << 20, ReadBytesPerSecond: 1000, WriteBytesPerSecond: 500, RandomReadIOPS: 100, Duration: time.Second, Method: "fixture; no benchmark I/O"}
	if err := m.RecordStoragePerformance("scratch", profile); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		next, err := OpenConfigured(home, settings)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				if err := next.Close(); err != nil {
					t.Error(err)
				}
			}()
			retained := next.storagePools["scratch"]
			if retained.providerErr == nil || retained.record.Volume != originalVolume {
				t.Fatal("replacement did not retain original identity and refuse provider")
			}
			check := func(view model.StoragePoolView) {
				t.Helper()
				if view.Location.Volume != currentVolume || view.Performance == nil || view.Performance.Volume != originalVolume || !view.Performance.At.Equal(profile.At) || !view.PerformanceStale || view.Budget.Known {
					t.Fatalf("replacement metadata mislabeled retained profile: %+v", view)
				}
			}
			views, err := next.StoragePools()
			if err != nil || len(views) != 1 {
				t.Fatalf("pool views: %v %v", views, err)
			}
			check(views[0])
			status, err := next.Status()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, resource := range status.Resources {
				if resource.Storage != nil && resource.Storage.Name == "scratch" {
					check(*resource.Storage)
					found = true
				}
			}
			if !found {
				t.Fatal("missing storage resource projection")
			}
		}()
	}
}
