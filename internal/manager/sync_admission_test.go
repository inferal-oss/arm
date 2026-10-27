package manager

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/sshservice"
	"getarm.org/internal/syncfs"
	"getarm.org/internal/syncreconcile"
	"getarm.org/internal/syncservice"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
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
	recovery, err := reopened.InspectSyncKey(t.Context(), i.Key, syncreconcile.Local)
	if err != nil || recovery.Lease.ID != l.ID || recovery.Lease.State != "needs_attention" || recovery.OperationState != "reserved" {
		t.Fatalf("restarted source recovery changed original charge or intent: %+v %v", recovery, err)
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
	runSyncDestinationAdmission(t, false, false, false)
}

func TestSyncDestinationRejectedIntentRollsBackEntireSSHAdmission(t *testing.T) {
	runSyncDestinationAdmission(t, true, false, false)
}

func TestSyncDestinationInlineWorkerUsesCommittedHold(t *testing.T) {
	runSyncDestinationAdmission(t, false, true, false)
}

func TestSyncDestinationInlineWorkerRejectsChangedIntentBeforeDispatch(t *testing.T) {
	runSyncDestinationAdmission(t, true, true, false)
}

func TestSyncDestinationFrameDecodedUnderChargedPreparation(t *testing.T) {
	runSyncDestinationAdmission(t, false, true, true)
}

func TestSyncDestinationChangedFrameRollsBackChargedPreparation(t *testing.T) {
	runSyncDestinationAdmission(t, true, true, true)
}

func TestSyncDestinationInlineWorkerOriginalLifecycle(t *testing.T) {
	for _, stop := range []string{"end", "expiry", "close"} {
		t.Run(stop, func(t *testing.T) { runSyncDestinationAdmissionLifecycle(t, false, true, stop, false) })
	}
}

func runSyncDestinationAdmission(t *testing.T, corrupt, inline, fromFrame bool) {
	runSyncDestinationAdmissionLifecycle(t, corrupt, inline, "", fromFrame)
}

func runSyncDestinationAdmissionLifecycle(t *testing.T, corrupt, inline bool, stop string, fromFrame bool) {
	m, root, parent := syncManagerFixture(t)
	var intent syncservice.Intent
	var rows []syncservice.ManifestRow
	entered, workerJoined := make(chan struct{}), make(chan struct{})
	stopped := make(chan error, 1)
	f := newSSHManagerFixtureWithAdmission(t, m, time.Minute, func(ctx context.Context, q model.SSHExecutionRequest, e sshservice.Execution, ch ssh.Channel) (model.Lease, sshservice.Process, error) {
		if inline {
			var l model.Lease
			var hold *SyncWorkerHold
			var err error
			if fromFrame {
				l, hold, err = m.AdmitSyncWorkerFromFrame(ctx, root, parent, q, e, ch, model.Resources{CPU: 1, Memory: 1 << 30, Disk: 4 << 20}, time.Minute, time.Now().Add(time.Minute), committedControl(m, q))
			} else {
				l, hold, err = m.AdmitSyncWorker(ctx, intent, rows, root, parent, q, e, ch, model.Resources{CPU: 1, Memory: 1 << 30, Disk: 4 << 20}, time.Minute, time.Now().Add(time.Minute), committedControl(m, q))
			}
			if err != nil {
				return l, nil, err
			}
			if fromFrame && (hold.Preparation() == nil || hold.Preparation().Intent.Side != syncreconcile.Local) {
				t.Error("charged preparation frame not retained for the worker")
			}
			if err := hold.CheckIntent(ctx, intent); err != nil {
				hold.JoinTransport()
				return l, nil, err
			}
			changed := intent
			changed.PlanDigest = strings.Repeat("0", 64)
			if err := hold.CheckIntent(ctx, changed); err == nil {
				t.Error("inline hold accepted another plan")
			}
			if err := hold.Start(func(_ context.Context, original ssh.Channel) error {
				if original != ch {
					t.Error("worker received another SSH channel")
				}
				if stop != "" {
					defer close(workerJoined)
					close(entered)
					_, readErr := original.Read(make([]byte, 1))
					return readErr
				}
				_, writeErr := original.Write([]byte("inline-worker"))
				return writeErr
			}); err != nil {
				hold.JoinTransport()
				return l, nil, err
			}
			if stop != "" {
				go func() {
					<-entered
					switch stop {
					case "end":
						_, err := m.End(l.ID, "cancelled")
						stopped <- err
					case "close":
						stopped <- m.Close()
					case "expiry":
						<-hold.runtime.ctx.Done()
						stopped <- nil
					}
				}()
			}
			if err := hold.Start(func(context.Context, ssh.Channel) error { t.Error("duplicate inline worker ran"); return nil }); err == nil {
				t.Error("duplicate inline worker accepted")
			}
			return l, hold, nil
		}
		return m.AcquireSyncSSHCommitted(ctx, intent, rows, root, parent, q, e, ch, model.Resources{CPU: 1, Memory: 1 << 30, Disk: 4 << 20}, time.Minute, time.Now().Add(time.Minute), committedControl(m, q))
	})
	f.q.Resources = model.Resources{CPU: .25, Memory: 2 << 20, Disk: 2 << 20}
	if stop == "expiry" {
		f.q.TTL = 2 * time.Second
	}
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
	validIntent := intent
	if corrupt {
		intent.ManifestDigest = strings.Repeat("0", 64)
	}
	var output []byte
	var runErr error
	if fromFrame {
		ch, reqs, openErr := f.client.OpenChannel("session", nil)
		if openErr != nil {
			t.Fatal(openErr)
		}
		wire := validIntent
		wire.Side = syncreconcile.Local
		var encoded bytes.Buffer
		if writeErr := syncservice.WritePreparation(&encoded, syncservice.Preparation{Version: syncservice.Schema, Intent: wire, Manifest: rows}); writeErr != nil {
			t.Fatal(writeErr)
		}
		if corrupt {
			raw := encoded.Bytes()
			at := bytes.Index(raw, []byte(wire.ManifestDigest))
			if at < 0 {
				t.Fatal("manifest digest missing from encoded frame")
			}
			if raw[at] == '0' {
				raw[at] = '1'
			} else {
				raw[at] = '0'
			}
		}
		wrote := make(chan error, 1)
		go func() {
			_, writeErr := io.Copy(ch, &encoded)
			wrote <- writeErr
		}()
		accepted, sendErr := ch.SendRequest("exec", true, ssh.Marshal(struct{ Command string }{syncWorkerCommand}))
		if !corrupt && (sendErr != nil || !accepted) {
			t.Fatalf("preparation-frame exec denied: %v", sendErr)
		}
		if corrupt && accepted {
			t.Fatal("changed preparation frame accepted")
		}
		if writeErr := <-wrote; writeErr != nil && !corrupt {
			t.Fatal(writeErr)
		}
		if !corrupt {
			output, runErr = io.ReadAll(ch)
		} else {
			runErr = fmt.Errorf("changed preparation denied: %v", sendErr)
		}
		if !corrupt && runErr == nil {
			for req := range reqs {
				if req.Type == "exit-status" {
					var status struct{ Status uint32 }
					if ssh.Unmarshal(req.Payload, &status) != nil || status.Status != 0 {
						runErr = fmt.Errorf("sync worker exit %d", status.Status)
					}
					break
				}
			}
		}
		_ = ch.Close()
	} else {
		session, openErr := f.client.NewSession()
		if openErr != nil {
			t.Fatal(openErr)
		}
		// The fixed worker is intentionally not installed yet; the command may
		// fail, but its admission must already be durable and remain charged.
		output, runErr = session.CombinedOutput(syncWorkerCommand)
	}
	if corrupt {
		if runErr == nil {
			t.Fatal("changed manifest started the fixed worker")
		}
		var partial int
		query := `SELECT (SELECT COUNT(*) FROM leases WHERE coalesce(json_extract(data,'$.request.kind'),'')!='sync-preparation')+(SELECT COUNT(*) FROM sync_operations)+(SELECT COUNT(*) FROM sync_lease_bindings)`
		if !fromFrame {
			query = `SELECT (SELECT COUNT(*) FROM leases WHERE coalesce(json_extract(data,'$.request.kind'),'')!='sync-preparation')+(SELECT COUNT(*) FROM ssh_operations)+(SELECT COUNT(*) FROM ssh_key_fences)+(SELECT COUNT(*) FROM sync_operations)+(SELECT COUNT(*) FROM sync_lease_bindings)`
		}
		if err := m.db.QueryRow(query).Scan(&partial); err != nil || partial != 0 {
			t.Fatalf("failed destination reservation left partial admission: %d %v", partial, err)
		}
		return
	}
	if stop != "" {
		if runErr == nil {
			t.Error("cancelled inline worker reported success")
		}
		select {
		case err := <-stopped:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("original lifecycle did not finish")
		}
		select {
		case <-workerJoined:
		case <-time.After(time.Second):
			t.Fatal("original worker read did not join")
		}
		select {
		case <-f.serverDone:
		case <-time.After(time.Second):
			t.Fatal("original SSH transport did not join")
		}
		checkManager := m
		if stop == "close" {
			reopened, err := Open(m.home, m.capacity)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			checkManager = reopened
		}
		_, _, admitted := f.snapshot()
		retained, err := checkManager.Inspect(admitted.ID)
		if err != nil || retained.State != "needs_attention" || retained.Request.Resources != f.q.Resources {
			t.Fatalf("cancel/restart lost promise: %+v %v", retained, err)
		}
		if err := checkManager.verifySyncRecords(context.Background(), true); err != nil {
			t.Fatal(err)
		}
		recovery, err := checkManager.InspectSyncKey(t.Context(), intent.Key, syncreconcile.Remote)
		if err != nil || recovery.Lease.ID != admitted.ID || recovery.Lease.State != "needs_attention" || recovery.OperationState != "reserved" {
			t.Fatalf("cancelled/restarted destination recovery changed charge or intent: %+v %v", recovery, err)
		}
		return
	}
	if inline && (runErr != nil || string(output) != "inline-worker") {
		t.Fatalf("in-process worker did not own SSH output: %q %v", output, runErr)
	}
	q, _, admitted := f.snapshot()
	if admitted.ID == "" {
		t.Fatal("fixed worker was not admitted")
	}
	if inline && m.jobs[admitted.ID] != nil {
		t.Fatal("inline sync worker launched an external shell job")
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
	recovery, err := m.InspectSyncKey(t.Context(), intent.Key, syncreconcile.Remote)
	if err != nil || recovery.Lease.ID != admitted.ID || recovery.Lease.State != "needs_attention" || recovery.OperationState != "reserved" || recovery.Lease.Request.Resources != q.Resources {
		t.Fatalf("retained destination recovery changed charge or operation state: %+v %v", recovery, err)
	}
}
