package manager

import (
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sshservice"
	"github.com/yrashk/arm/internal/syncfs"
	"github.com/yrashk/arm/internal/syncreconcile"
	"github.com/yrashk/arm/internal/syncservice"
	"golang.org/x/crypto/ssh"
)

func syncManagerFixture(t *testing.T) (*Manager, *syncfs.Root, *syncfs.Root) {
	t.Helper()
	base := t.TempDir()
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Open(filepath.Join(base, "arm"), model.Resources{CPU: 1, Memory: 1 << 30, Disk: 128 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-m.stop:
			return
		default:
		}
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	cpuReading(m, 8, 8, 0, 8, 0)
	memoryReading(m, 1<<30, 1<<30, 0)
	diskReading(m, 1<<30, 1<<30, 0)
	m.observationMu.Lock()
	m.diskAvailability.Path = m.home
	var fs unix.Statfs_t
	if err := unix.Statfs(m.home, &fs); err != nil {
		m.observationMu.Unlock()
		t.Fatal(err)
	}
	m.diskAvailability.Volume = fmt.Sprintf("fsid:%x", fs.Fsid.Val)
	m.observationMu.Unlock()
	selected := filepath.Join(base, "selected")
	if err := os.Mkdir(selected, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := syncfs.OpenRoot(selected)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	parent, err := syncfs.OpenRoot(m.home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { parent.Close() })
	return m, root, parent
}

func sourceSyncIntent(t *testing.T, m *Manager, root *syncfs.Root) (syncservice.Intent, []syncservice.ManifestRow, model.SSHControlCommitment) {
	t.Helper()
	id, err := root.NativeID()
	if err != nil {
		t.Fatal(err)
	}
	c := fenceControl(m, "sync-key-12345678")
	c.Source, c.Target = m.host, strings.Repeat("a", 32)
	c.CommandSHA256 = sshDigest([]byte(syncWorkerCommand))
	c.Resources = model.Resources{CPU: .25, Memory: 2 << 20, Disk: 2 << 20}
	c.Reason = "reviewed sync source"
	c.CWD = "/tmp"
	digest, err := c.Digest()
	if err != nil {
		t.Fatal(err)
	}
	empty := syncreconcile.Snapshot{Entries: map[string]syncreconcile.Entry{}, NameKeys: map[string]string{}}
	req := syncreconcile.Request{Binding: syncreconcile.Binding{Local: syncreconcile.Endpoint{ID: SyncEndpointID(m.host, m.owner, m.ownerUsername), RootID: id}, Remote: syncreconcile.Endpoint{ID: SyncEndpointID(c.Target, c.Owner, c.Account), RootID: "reviewed-remote-root"}, SelectionID: "all", MetadataID: "content"}, Direction: syncreconcile.Push, LocalPolicy: syncreconcile.Additive, RemotePolicy: syncreconcile.Additive, Local: empty, Remote: empty, FirstRun: true}
	plan, err := syncreconcile.Reconcile(req)
	if err != nil {
		t.Fatal(err)
	}
	i, rows, err := syncservice.NewIntent(c.Key, c.CommandSHA256, digest, c.Reason, syncreconcile.Local, req, plan, model.Resources{CPU: .25, Memory: 2 << 20, Disk: 2 << 20}, c.Resources, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return i, rows, c
}

func TestSyncSourceReservationRetainsChargeAndOriginalKey(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	l, err := m.ReserveSyncSource(context.Background(), i, rows, root, parent, control)
	if err != nil || l.State != "active" {
		t.Fatalf("source reservation: %+v %v", l, err)
	}
	var joined int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM sync_operations o JOIN sync_lease_bindings b ON o.key=b.key AND o.side=b.side JOIN leases l ON l.id=b.lease WHERE o.key=? AND o.side='local' AND b.lease=?`, i.Key, l.ID).Scan(&joined); err != nil || joined != 1 {
		t.Fatalf("atomic source evidence: %d %v", joined, err)
	}
	if err := unix.Mkfifo(filepath.Join(root.Path(), "retry-must-not-scan"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReserveSyncSource(context.Background(), i, rows, root, parent, control); err != syncservice.ErrKeyExists {
		t.Fatalf("original key replay accepted: %v", err)
	}
	if ended, err := m.End(l.ID, "cancelled"); err != nil || ended.State != "needs_attention" {
		t.Fatalf("sync charge released by ordinary End: %+v %v", ended, err)
	}
	if err := m.verifySyncRecords(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(m.home, m.capacity)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := reopened.Inspect(l.ID)
	if err != nil || recovered.State != "needs_attention" || recovered.Request.Resources != i.LocalResources {
		t.Fatalf("source charge lost on restart: %+v %v", recovered, err)
	}
}

func TestSyncSourceFailedReservationRollsBackLease(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	i.ManifestDigest = strings.Repeat("0", 64)
	if _, err := m.ReserveSyncSource(context.Background(), i, rows, root, parent, control); err == nil {
		t.Fatal("changed manifest admitted")
	}
	var leases, operations, bindings int
	if err := m.db.QueryRow(`SELECT (SELECT COUNT(*) FROM leases WHERE coalesce(json_extract(data,'$.request.kind'),'')!='sync-preparation'),(SELECT COUNT(*) FROM sync_operations),(SELECT COUNT(*) FROM sync_lease_bindings)`).Scan(&leases, &operations, &bindings); err != nil || leases != 0 || operations != 0 || bindings != 0 {
		t.Fatalf("failed reservation left charge/evidence: %d %d %d %v", leases, operations, bindings, err)
	}
}

func TestSyncDestinationSSHAdmissionJoinsFenceLeaseAndIntent(t *testing.T) {
	runSyncDestinationAdmission(t, false)
}

func TestSyncDestinationRejectedIntentRollsBackEntireSSHAdmission(t *testing.T) {
	runSyncDestinationAdmission(t, true)
}

func runSyncDestinationAdmission(t *testing.T, corrupt bool) {
	m, root, parent := syncManagerFixture(t)
	var intent syncservice.Intent
	var rows []syncservice.ManifestRow
	f := newSSHManagerFixtureWithAdmission(t, m, time.Minute, func(ctx context.Context, q model.SSHExecutionRequest, e sshservice.Execution, ch ssh.Channel) (model.Lease, sshservice.Process, error) {
		return m.AcquireSyncSSHCommitted(ctx, intent, rows, root, parent, q, e, ch, model.Resources{CPU: 1, Memory: 1 << 30, Disk: 4 << 20}, time.Minute, time.Now().Add(time.Minute), committedControl(m, q))
	})
	f.q.Resources = model.Resources{CPU: .25, Memory: 2 << 20, Disk: 2 << 20}
	f.q.CWD = root.Path()
	f.q.Reason = "reviewed sync destination"
	f.q.Kind = "exec"
	f.q.CommandSHA256 = sshDigest([]byte(syncWorkerCommand))
	control := committedControl(m, f.q)
	digest, err := control.Digest()
	if err != nil {
		t.Fatal(err)
	}
	rootID, err := root.NativeID()
	if err != nil {
		t.Fatal(err)
	}
	empty := syncreconcile.Snapshot{Entries: map[string]syncreconcile.Entry{}, NameKeys: map[string]string{}}
	req := syncreconcile.Request{Binding: syncreconcile.Binding{Local: syncreconcile.Endpoint{ID: SyncEndpointID(f.q.Source, "source-uid", "source-account"), RootID: "reviewed-source-root"}, Remote: syncreconcile.Endpoint{ID: SyncEndpointID(m.host, m.owner, m.ownerUsername), RootID: rootID}, SelectionID: "all", MetadataID: "content"}, Direction: syncreconcile.Push, LocalPolicy: syncreconcile.Additive, RemotePolicy: syncreconcile.Additive, Local: empty, Remote: empty, FirstRun: true}
	plan, err := syncreconcile.Reconcile(req)
	if err != nil {
		t.Fatal(err)
	}
	intent, rows, err = syncservice.NewIntent(f.q.Key, f.q.CommandSHA256, digest, f.q.Reason, syncreconcile.Remote, req, plan, f.q.Resources, f.q.Resources, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if corrupt {
		intent.ManifestDigest = strings.Repeat("0", 64)
	}
	session, err := f.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	// The fixed worker is intentionally not installed yet; the command may
	// fail, but its admission must already be durable and remain charged.
	_, runErr := session.CombinedOutput(syncWorkerCommand)
	if corrupt {
		if runErr == nil {
			t.Fatal("changed manifest started the fixed worker")
		}
		var partial int
		if err := m.db.QueryRow(`SELECT (SELECT COUNT(*) FROM leases WHERE coalesce(json_extract(data,'$.request.kind'),'')!='sync-preparation')+(SELECT COUNT(*) FROM ssh_operations)+(SELECT COUNT(*) FROM ssh_key_fences)+(SELECT COUNT(*) FROM sync_operations)+(SELECT COUNT(*) FROM sync_lease_bindings)`).Scan(&partial); err != nil || partial != 0 {
			t.Fatalf("failed destination reservation left partial admission: %d %v", partial, err)
		}
		return
	}
	q, _, admitted := f.snapshot()
	if admitted.ID == "" {
		t.Fatal("fixed worker was not admitted")
	}
	var joined int
	err = m.db.QueryRow(`SELECT COUNT(*) FROM sync_operations s JOIN sync_lease_bindings b ON s.key=b.key AND s.side=b.side JOIN leases l ON l.id=b.lease JOIN ssh_operations o ON o.lease=l.id JOIN ssh_key_fences f ON f.lease=l.id AND f.principal=o.principal AND f.key=o.key WHERE s.key=? AND s.side='remote' AND o.key=?`, intent.Key, q.Key).Scan(&joined)
	if err != nil || joined != 1 {
		t.Fatalf("destination atomic admission: %d %v", joined, err)
	}
	eventually(t, func() bool { l, e := m.Inspect(admitted.ID); return e == nil && l.State == "needs_attention" })
	if err := m.verifySyncRecords(context.Background(), true); err != nil {
		t.Fatal(err)
	}
}
