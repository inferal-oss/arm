package cli

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/server"
)

func TestEnsureMeshBackendPreservesHealthyConfiguredBudgets(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	configured := model.Resources{CPU: 2, Memory: 64 << 20, Disk: 128 << 20}
	go func() { done <- server.Serve(ctx, home, "127.0.0.1:0", configured) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("isolated backend did not stop")
		}
	}()
	a := app{home: home, ctx: ctx}
	deadline := time.Now().Add(5 * time.Second)
	for {
		inspection, err := a.inspectMeshLifecycle()
		if err == nil && inspection.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("isolated backend not ready", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	before, exists, err := mesh.ReadStartupPolicy(home)
	if err != nil || !exists {
		t.Fatal(err)
	}
	actual, err := a.ensureMeshBackend(mesh.DefaultStartup())
	if err != nil || !actual.Running || actual.Startup.Resources != configured {
		t.Fatal(actual, err)
	}
	after, _, err := mesh.ReadStartupPolicy(home)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("healthy startup policy was rewritten", before, after, err)
	}
}
