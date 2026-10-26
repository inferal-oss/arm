package manager

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/syncfs"
	"github.com/yrashk/arm/internal/syncreconcile"
	"github.com/yrashk/arm/internal/syncservice"
)

func TestReviewSyncOutgoingStageMustFitLocalDiskPromise(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	if err := os.WriteFile(filepath.Join(root.Path(), "large"), make([]byte, 4<<20), 0600); err != nil {
		t.Fatal(err)
	}
	local, err := root.Scan(t.Context(), nil, nil, syncfs.NamePolicy{}, syncfs.Metadata{}, syncfs.ScanLimits{Entries: syncreconcile.MaxEntries, Bytes: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	base, _, control := sourceSyncIntent(t, m, root)
	control.Resources = model.Resources{CPU: .25, Memory: 2 << 20, Disk: 64 << 20}
	digest, err := control.Digest()
	if err != nil {
		t.Fatal(err)
	}
	remote := syncreconcile.Snapshot{Entries: map[string]syncreconcile.Entry{}, NameKeys: map[string]string{"large": "large"}}
	req := syncreconcile.Request{Binding: base.Binding, Direction: syncreconcile.Push, LocalPolicy: syncreconcile.Additive, RemotePolicy: syncreconcile.Additive, Local: local, Remote: remote, FirstRun: true}
	plan, err := syncreconcile.Reconcile(req)
	if err != nil {
		t.Fatal(err)
	}
	i, rows, err := syncservice.NewIntent(control.Key, control.CommandSHA256, digest, control.Reason, syncreconcile.Local, req, plan, model.Resources{CPU: .25, Memory: 2 << 20, Disk: 2 << 20}, control.Resources, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.ReserveSyncSource(context.Background(), i, rows, root, parent, control)
	if err == nil {
		t.Fatalf("admitted outgoing 4MiB private source stage with only %d local disk bytes: lease=%s", l.Request.Resources.Disk, l.ID)
	}
}
