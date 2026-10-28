package server_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/server"
)

func TestConfiguredServerActuallyPublishesNamedPoolsAndExactStartupHash(t *testing.T) {
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	settings := daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0", StoragePools: []model.StoragePoolConfig{{Name: "scratch", Path: base, Tier: "build"}}}
	home := filepath.Join(base, "authority")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.ServeConfigured(ctx, home, settings) }()
	t.Cleanup(func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(10 * time.Second):
			t.Error("configured backend failed to join")
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	var views []model.StoragePoolView
	for {
		e = client.Call(ctx, home, "storage_pools", nil, &views)
		if e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(e)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(views) != 1 || views[0].Name != "scratch" || views[0].Tier != "build" || views[0].Location.Path != base {
		t.Fatal(views)
	}
	var status model.Status
	if e = client.Call(ctx, home, "status", nil, &status); e != nil {
		t.Fatal(e)
	}
	found := false
	for _, r := range status.Resources {
		if r.Storage != nil && r.Storage.Name == "scratch" {
			found = true
		}
	}
	if !found {
		t.Fatal("named pool missing from actual resource views")
	}
	saved, exists, e := mesh.ReadStartupPolicy(home)
	hash, he := daemonconfig.SnapshotSHA256(settings)
	if e != nil || he != nil || !exists || saved.SettingsSHA256 != hash {
		t.Fatal(saved, exists, e, he)
	}
}
