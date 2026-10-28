package server

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

func TestNodeCacheNativeClockSuspendAndFailures(t *testing.T) {
	wall := time.Now().UTC()
	anchor := leaseclock.Sample{Boot: "cache-boot", Tick: int64(time.Second), Wall: wall}
	// Exercise cache time under an actually valid enrolled identity. An
	// incomplete synthetic identity is independently unavailable regardless
	// of native elapsed time and cannot represent the fresh controls.
	store, err := mesh.Init(filepath.Join(t.TempDir(), "state"), strings.Repeat("a", 32), "cache-clock", "127.0.0.1:40001", "127.0.0.1:40001")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error("original cache fixture store did not close", err)
		}
	})
	identity := store.Config().Identity
	if _, err := mesh.ValidateIdentity(identity); err != nil {
		t.Fatal("cache fixture identity unavailable", err)
	}
	peer := mesh.Peer{Identity: identity, Pair: "pair", Revision: 2, State: "active"}
	total := int64(100)
	s := &Server{nodeCache: map[string]cachedNodeRead{peer.Identity.Host: {Fingerprint: identity.Fingerprint, Pair: "pair", Revision: 2, MetadataAt: wall, ScopeAt: wall, metadataClock: anchor, scopeClock: anchor, Scope: &model.NodeAccessScope{Grant: model.NodeGrant{Permissions: []string{"observe-host"}}}, Metadata: &model.NodeMetadata{Host: peer.Identity.Host, ReportedAt: wall, Inventory: model.NodeInventory{Memory: model.NodeInventorySize{Total: &total, ObservedAt: &wall, Known: true, StaleAfter: 3 * time.Second}}}}}}
	for _, test := range []struct {
		name  string
		now   leaseclock.Sample
		stale bool
	}{
		{"fresh", anchor, false},
		{"wall-rollback-only", leaseclock.Sample{Boot: anchor.Boot, Tick: anchor.Tick, Wall: wall.Add(-time.Hour)}, false},
		{"long-suspend-wall-rollback", leaseclock.Sample{Boot: anchor.Boot, Tick: anchor.Tick + int64(time.Hour), Wall: wall.Add(-time.Hour)}, true},
		{"tick-regression", leaseclock.Sample{Boot: anchor.Boot, Tick: 0, Wall: wall}, true},
		{"changed-boot", leaseclock.Sample{Boot: "replacement", Tick: anchor.Tick, Wall: wall}, true},
		{"read-unavailable", leaseclock.Sample{}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := peerNodeEntry(peer)
			s.applyNodeCacheAt(&entry, peer, test.now)
			if entry.MetadataFreshness.Stale != test.stale || entry.ScopeFreshness.Stale != test.stale {
				t.Fatal("wrong native freshness", entry)
			}
			if test.stale && entry.Health != "stale" {
				t.Fatal("stale native observation health differs", entry)
			}
			if test.stale && (entry.Metadata.Inventory.Memory.Known || !entry.Metadata.Inventory.Memory.Stale) {
				t.Fatal("stale native inventory exposed as known", entry)
			}
			if !entry.Metadata.ReportedAt.Equal(wall) || !entry.Metadata.Inventory.Memory.ObservedAt.Equal(wall) || !entry.MetadataFreshness.Received.Equal(wall) {
				t.Fatal("source timestamps were relabelled")
			}
			if test.name == "long-suspend-wall-rollback" && entry.MetadataFreshness.Age != time.Hour {
				t.Fatal("suspend excluded from cache age", entry.MetadataFreshness)
			}
		})
	}
	t.Run("invalid-identity-remains-unavailable", func(t *testing.T) {
		invalid := peer
		invalid.Identity.Certificate = ""
		entry := peerNodeEntry(invalid)
		s.applyNodeCacheAt(&entry, invalid, anchor)
		if entry.Health != "unavailable" || !entry.MetadataFreshness.Stale || !entry.ScopeFreshness.Stale {
			t.Fatal("invalid identity gained fresh observations", entry)
		}
	})
}

func TestNodeCacheObservationClockBoundsRead(t *testing.T) {
	wall := time.Now().UTC()
	anchor := leaseclock.Sample{Boot: "cache-boot", Tick: int64(time.Second), Wall: wall}
	for _, name := range []string{"valid", "start-read-failure", "end-read-failure", "regression", "boot-change", "elapsed-timeout"} {
		t.Run(name, func(t *testing.T) {
			reads, calls := 0, 0
			read := func() (leaseclock.Sample, error) {
				reads++
				if name == "start-read-failure" || name == "end-read-failure" && reads == 2 {
					return leaseclock.Sample{}, errors.New("fixture clock failure")
				}
				now := anchor
				if reads == 2 {
					now.Tick += int64(2 * time.Second)
					now.Wall = wall.Add(-time.Hour)
					if name == "regression" {
						now.Tick = 0
					}
					if name == "boot-change" {
						now.Boot = "replacement"
					}
					if name == "elapsed-timeout" {
						now.Tick = anchor.Tick + int64(time.Hour)
					}
				}
				return now, nil
			}
			raw, received, start, err := readNodeObservation(read, func() (json.RawMessage, error) { calls++; return json.RawMessage(`{}`), nil })
			if name == "valid" {
				if err != nil || string(raw) != "{}" || start.Tick != anchor.Tick || !received.Equal(wall.Add(-time.Hour)) {
					t.Fatal("native bounded read failed", err)
				}
			} else if err == nil {
				t.Fatal("uncertain clock refreshed observation")
			}
			if name == "start-read-failure" && calls != 0 || calls > 1 {
				t.Fatal("clock failure caused dispatch/replay", calls)
			}
		})
	}
}
