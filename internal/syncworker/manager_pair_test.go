package syncworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/manager"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
	"github.com/inferal-oss/arm/internal/syncfs"
	"github.com/inferal-oss/arm/internal/syncreconcile"
	"github.com/inferal-oss/arm/internal/syncservice"
	"golang.org/x/crypto/ssh"
)

func TestManagerOwnedSyncPairOverAuthenticatedSSH(t *testing.T) {
	runManagerOwnedSyncPair(t, syncreconcile.Both, syncreconcile.Additive, false)
}

func TestNativeManagerSyncWorkerDirections(t *testing.T) {
	for _, tc := range []struct {
		name      string
		direction syncreconcile.Direction
		policy    syncreconcile.Policy
	}{
		{"push-additive", syncreconcile.Push, syncreconcile.Additive},
		{"push-full", syncreconcile.Push, syncreconcile.Full},
		{"pull-additive", syncreconcile.Pull, syncreconcile.Additive},
	} {
		t.Run(tc.name, func(t *testing.T) { runManagerOwnedSyncPair(t, tc.direction, tc.policy, false) })
	}
}

func TestNativeManagerSyncWorkerCancellation(t *testing.T) {
	runManagerOwnedSyncPair(t, syncreconcile.Both, syncreconcile.Additive, true)
}

func runManagerOwnedSyncPair(t *testing.T, direction syncreconcile.Direction, policy syncreconcile.Policy, cancelBeforeExec bool) {
	t.Helper()
	p := setupPair(t)
	write(t, p.local, "from-source", "source content to remote")
	write(t, p.remote, "from-destination", "destination content to source")
	base := filepath.Dir(p.local.Path())
	resources := model.Resources{CPU: .1, Memory: 32 << 20, Disk: 16 << 20}
	capacity := model.Resources{CPU: 4, Memory: 4 << 30, Disk: 1 << 30}
	source, err := manager.Open(filepath.Join(base, "source-arm"), capacity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	destination, err := manager.Open(filepath.Join(base, "destination-arm"), capacity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := destination.Close(); err != nil {
			t.Error(err)
		}
	})
	sourceState, err := source.Status()
	if err != nil {
		t.Fatal(err)
	}
	destinationState, err := destination.Status()
	if err != nil {
		t.Fatal(err)
	}
	sourceWork, err := syncfs.OpenRoot(filepath.Join(base, "source-arm"))
	if err != nil {
		t.Fatal(err)
	}
	defer sourceWork.Close()
	destinationWork, err := syncfs.OpenRoot(filepath.Join(base, "destination-arm"))
	if err != nil {
		t.Fatal(err)
	}
	defer destinationWork.Close()
	hostKey, userKey := signer(t), signer(t)
	publicDigest := sha256.Sum256(userKey.PublicKey().Marshal())
	hostDigest := sha256.Sum256(hostKey.PublicKey().Marshal())
	key := "manager-pair-" + strings.Repeat("a", 20)
	control := model.SSHControlCommitment{Schema: 1, Source: sourceState.Host, Target: destinationState.Host,
		Pair: strings.Repeat("b", 64), Revision: 1, Certificate: strings.Repeat("c", 64), Owner: destinationState.Owner,
		Account: destinationState.OwnerUsername, HostKeySHA256: hex.EncodeToString(hostDigest[:]), Key: key,
		PublicKeySHA256: hex.EncodeToString(publicDigest[:]), Reason: "reviewed Manager sync pair", TTL: time.Minute,
		Resources: resources, CWD: p.remote.Path(), Kind: "exec", CommandSHA256: FixedCommandSHA256()}
	controlDigest, err := control.Digest()
	if err != nil {
		t.Fatal(err)
	}
	q := model.SSHExecutionRequest{Source: control.Source, Target: control.Target, Pair: control.Pair, Revision: control.Revision,
		Certificate: control.Certificate, Account: control.Account, Key: key, PublicKeySHA256: control.PublicKeySHA256,
		Reason: control.Reason, TTL: control.TTL, Resources: resources, CWD: control.CWD, Kind: "exec", CommandSHA256: control.CommandSHA256}
	req := requestPair(t, p, direction, policy, nil, nil)
	req.Binding.Local.ID = manager.SyncEndpointID(sourceState.Host, sourceState.Owner, sourceState.OwnerUsername)
	req.Binding.Remote.ID = manager.SyncEndpointID(destinationState.Host, destinationState.Owner, destinationState.OwnerUsername)
	req.Binding.MetadataID = "content"
	plan, err := syncreconcile.Reconcile(req)
	if err != nil {
		t.Fatal(err)
	}
	intent, rows, err := syncservice.NewIntent(key, control.CommandSHA256, controlDigest, control.Reason, syncreconcile.Local, req, plan, resources, resources, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	// Native observers may start with unknown interval headroom. Observe only;
	// the single original admission below remains the authority decision.
	if err := waitManagerPairReadiness(t.Context(), source, resources); err != nil {
		t.Fatalf("source native readiness unavailable: %v", err)
	}
	t.Logf("source native readiness: %s", managerPairBudget(source))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	execEntered, transportJoined := make(chan struct{}), make(chan struct{})
	var originalSource *manager.SyncSourceHold
	var originalDestination *manager.SyncWorkerHold
	server := sshservice.Server{HostKey: hostKey, Handshake: 5 * time.Second, StartTimeout: 5 * time.Second,
		Revalidate: func(context.Context) error { return nil },
		Execute: func(ctx context.Context, execution sshservice.Execution, ch ssh.Channel) (sshservice.Process, error) {
			if cancelBeforeExec {
				close(execEntered)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			if err := waitManagerPairReadiness(ctx, destination, resources); err != nil {
				return nil, fmt.Errorf("destination native readiness unavailable: %w", err)
			}
			t.Logf("destination native readiness: %s", managerPairBudget(destination))
			_, hold, err := destination.AdmitSyncWorkerFromFrame(ctx, p.remote, destinationWork, q, execution, ch, capacity, time.Minute, time.Now().Add(time.Minute), control)
			if err != nil {
				t.Logf("original destination admission refused: %v; native budgets=%s", err, managerPairBudget(destination))
				return nil, err
			}
			originalDestination = hold
			if err := RunDestination(hold); err != nil {
				hold.JoinTransport()
				return nil, err
			}
			return hold, nil
		}}
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			serverDone <- e
			return
		}
		serverDone <- server.Serve(ctx, conn, sshservice.Permit{Account: destinationState.OwnerUsername, PublicKey: userKey.PublicKey()})
	}()
	opened := 0
	lease, hold, err := source.AdmitSyncSource(ctx, intent, rows, p.local, sourceWork, control, func(ctx context.Context) (manager.SyncSourceChannel, error) {
		opened++
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
		if err != nil {
			return manager.SyncSourceChannel{}, err
		}
		sshConn, channels, requests, err := ssh.NewClientConn(conn, "arm-sync-pair", &ssh.ClientConfig{User: destinationState.OwnerUsername, Auth: []ssh.AuthMethod{ssh.PublicKeys(userKey)}, HostKeyCallback: ssh.FixedHostKey(hostKey.PublicKey()), Timeout: 5 * time.Second})
		if err != nil {
			_ = conn.Close()
			return manager.SyncSourceChannel{}, err
		}
		client := ssh.NewClient(sshConn, channels, requests)
		ch, reqs, err := client.OpenChannel("session", nil)
		if err != nil {
			_ = client.Close()
			return manager.SyncSourceChannel{}, err
		}
		return manager.SyncSourceChannel{Channel: ch, Requests: reqs, CloseTransport: client.Close, Join: func() error {
			defer close(transportJoined)
			clientErr := client.Wait()
			e := <-serverDone
			// Check both durable protocol outcomes independently, only after
			// the actual original client and server transport workers joined.
			complete := false
			if originalSource != nil && originalDestination != nil {
				sl, dl := originalSource.Ledger(), originalDestination.Ledger()
				s, se := sl.Lookup(ctx)
				d, de := dl.Lookup(ctx)
				sb, sbe := syncservice.LoadBaseline(ctx, sl.DB, req.Binding, syncreconcile.Local)
				db, dbe := syncservice.LoadBaseline(ctx, dl.DB, req.Binding, syncreconcile.Remote)
				complete = se == nil && de == nil && sbe == nil && dbe == nil && s.State == "confirmed" && d.State == "confirmed" &&
					s.LocalReceipt != nil && s.RemoteReceipt != nil && d.LocalReceipt != nil && d.RemoteReceipt != nil &&
					sb != nil && db != nil && reflect.DeepEqual(sb, db)
			}
			return managerPairTeardown(ctx, clientErr, e, complete)
		}}, nil
	})
	if err != nil || lease.State != "active" || opened != 0 {
		t.Fatalf("source admission before dial: state=%s error=%v opened=%d native budgets=%s", lease.State, err, opened, managerPairBudget(source))
	}
	originalSource = hold
	if err := RunSource(hold); err != nil {
		t.Fatal(err)
	}
	if cancelBeforeExec {
		select {
		case <-execEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("original destination exec not reached")
		}
		if _, err := source.End(lease.ID, "cancelled"); err != nil {
			t.Fatal(err)
		}
		wait, stop := context.WithTimeout(t.Context(), 5*time.Second)
		defer stop()
		if err := hold.Wait(wait); !errors.Is(err, context.Canceled) {
			t.Fatal("original cancellation hidden", err)
		}
		hold.JoinTransport()
		select {
		case <-transportJoined:
		default:
			t.Fatal("source returned before original transport joined")
		}
		retained, err := source.Inspect(lease.ID)
		if err != nil || retained.State != "needs_attention" || retained.Ended != nil || retained.Request.Resources != resources {
			t.Fatal("cancelled native promise released or replaced", retained, err)
		}
		assertManagerPairFiles(t, p.local.Path(), map[string]string{"from-source": "source content to remote"})
		assertManagerPairFiles(t, p.remote.Path(), map[string]string{"from-destination": "destination content to source"})
		state, err := destination.Status()
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range state.Leases {
			if l.Request.OriginKey == key {
				t.Fatal("pre-exec cancellation admitted destination effects", l)
			}
		}
		return
	}
	if err := hold.Wait(ctx); err != nil {
		t.Fatalf("Manager-owned source protocol failed: %v", err)
	}
	hold.JoinTransport()
	if opened != 1 {
		t.Fatal("Manager-owned original source opener count", opened)
	}
	if direction == syncreconcile.Both && (contents(t, p.remote, "from-source") != "source content to remote" || contents(t, p.local, "from-destination") != "destination content to source") {
		t.Fatal("Manager-owned bidirectional sync did not converge", opened)
	}
	wantLocal := map[string]string{"from-source": "source content to remote"}
	wantRemote := map[string]string{"from-destination": "destination content to source"}
	if direction == syncreconcile.Push || direction == syncreconcile.Both {
		wantRemote["from-source"] = "source content to remote"
		if policy == syncreconcile.Full {
			delete(wantRemote, "from-destination")
		}
	}
	if direction == syncreconcile.Pull || direction == syncreconcile.Both {
		wantLocal["from-destination"] = "destination content to source"
	}
	assertManagerPairFiles(t, p.local.Path(), wantLocal)
	assertManagerPairFiles(t, p.remote.Path(), wantRemote)
	if baseline, err := syncservice.LoadBaseline(ctx, hold.Ledger().DB, req.Binding, syncreconcile.Local); err != nil || baseline == nil {
		t.Fatalf("source baseline absent: %v", err)
	}
	retained, err := source.Inspect(lease.ID)
	if err != nil || retained.State != "needs_attention" || retained.Outcome != "sync_source_complete" {
		t.Fatalf("source promise not retained: %+v %v", retained, err)
	}
	destinationStatus, err := destination.Status()
	if err != nil {
		t.Fatal(err)
	}
	var destinationPromises int
	for _, candidate := range destinationStatus.Leases {
		if candidate.Request.OriginKey == key {
			destinationPromises++
			if candidate.State != "needs_attention" || candidate.Outcome != "sync_worker_complete" {
				t.Fatalf("destination promise not retained after receipt: %+v", candidate)
			}
		}
	}
	if destinationPromises != 1 {
		t.Fatalf("expected one exact destination promise, found %d", destinationPromises)
	}
	pairRecovery, err := syncservice.InspectPair(ctx, hold.Ledger(), originalDestination.Ledger())
	if err != nil || !pairRecovery.JointlyConfirmed || pairRecovery.NeedsAttention {
		t.Fatalf("completed authenticated pair recovery differs: %+v %v", pairRecovery, err)
	}
	for _, endpoint := range []struct {
		manager *manager.Manager
		side    syncreconcile.Side
	}{{source, syncreconcile.Local}, {destination, syncreconcile.Remote}} {
		view, err := endpoint.manager.InspectSyncKey(ctx, key, endpoint.side)
		if err != nil || view.OperationState != "confirmed" || view.Lease.State != "needs_attention" ||
			view.Lease.Ended != nil || view.Lease.Request.Resources != resources || view.LocalReceipt == nil || view.RemoteReceipt == nil {
			t.Fatalf("confirmed receipt changed charged endpoint recovery: %+v %v", view, err)
		}
	}
}

func assertManagerPairFiles(t *testing.T, root string, want map[string]string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != len(want) {
		t.Fatal("native selected file set changed", root, len(entries), len(want), err)
	}
	for name, body := range want {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(got) != body {
			t.Fatal("native selected contents differ", name, string(got), err)
		}
	}
}

// Expected connection teardown may be idempotent only after authenticated
// exit/dual receipts/baseline confirmation AND both original transport joins.
// A genuine parent cancellation or incomplete protocol remains a failure.
func managerPairTeardown(ctx context.Context, clientErr, serverErr error, complete bool) error {
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), clientErr, serverErr)
	}
	if !complete {
		return errors.Join(errors.New("original authenticated pair completion is unproven"), clientErr, serverErr)
	}
	if errors.Is(clientErr, io.EOF) || errors.Is(clientErr, net.ErrClosed) {
		clientErr = nil
	}
	if errors.Is(serverErr, io.EOF) || errors.Is(serverErr, context.Canceled) {
		serverErr = nil
	}
	return errors.Join(clientErr, serverErr)
}

func TestManagerPairTeardownRequiresCompleteJoinedProtocol(t *testing.T) {
	for _, tc := range []struct {
		name                                      string
		client, server                            error
		complete, cancelled, expired, wantFailure bool
	}{
		{"completed", net.ErrClosed, context.Canceled, true, false, false, false},
		{"lost-exit-or-receipt", net.ErrClosed, context.Canceled, false, false, false, true},
		{"missing-confirmation", nil, nil, false, false, false, true},
		{"parent-cancelled", net.ErrClosed, context.Canceled, true, true, false, true},
		{"parent-expired", net.ErrClosed, context.Canceled, true, false, true, true},
		{"unknown-client-failure", errors.New("unexpected client transport failure"), context.Canceled, true, false, false, true},
		{"unknown-server-failure", io.EOF, errors.New("unexpected server transport failure"), true, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			if tc.expired {
				var stop context.CancelFunc
				ctx, stop = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer stop()
			}
			if err := managerPairTeardown(ctx, tc.client, tc.server, tc.complete); (err != nil) != tc.wantFailure {
				t.Fatal("teardown lost protocol/cancellation authority", err)
			}
		})
	}
}

// This is a fixture readiness observation, never a probe Acquire, operation
// retry, observer override, or promise that the subsequent admission must fit.
func waitManagerPairReadiness(parent context.Context, m *manager.Manager, need model.Resources) error {
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		s, err := m.Status()
		if err != nil {
			return err
		}
		if s.CPU != nil && s.CPU.Known && !s.CPU.Stale && s.CPU.Available >= need.CPU &&
			s.Memory != nil && !s.Memory.Stale && s.Memory.Observation.Total != nil && s.Memory.Observation.Available != nil && s.Memory.Available >= need.Memory &&
			s.Disk != nil && s.Disk.Known && !s.Disk.Stale && s.Disk.Available >= need.Disk {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("fresh native headroom did not become ready: %w; budgets=%s", ctx.Err(), managerPairBudget(m))
		case <-ticker.C:
		}
	}
}

// Omit host, owner, paths and native scope handles from technical receipts.
func managerPairBudget(m *manager.Manager) string {
	s, err := m.Status()
	if err != nil {
		return "status unavailable"
	}
	budget := map[string]any{}
	if c := s.CPU; c != nil {
		budget["cpu"] = map[string]any{"known": c.Known, "stale": c.Stale, "available": c.Available, "reserved": c.Reserved, "capacity": c.Capacity, "interval_ns": c.Observation.Interval, "age_ns": c.Age}
	}
	if b := s.Memory; b != nil {
		budget["memory"] = map[string]any{"known": b.Observation.Total != nil && b.Observation.Available != nil, "stale": b.Stale, "available": b.Available, "reserved": b.Reserved, "capacity": b.Capacity, "age_ns": b.Age}
	}
	if b := s.Disk; b != nil {
		budget["disk"] = map[string]any{"known": b.Known, "stale": b.Stale, "available": b.Available, "reserved": b.Reserved, "capacity": b.Capacity, "age_ns": b.Age}
	}
	raw, _ := json.Marshal(budget)
	return string(raw)
}
