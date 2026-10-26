package manager

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sshservice"
	"github.com/yrashk/arm/internal/syncservice"
	"golang.org/x/crypto/ssh"
)

// This channel deliberately distinguishes a local close request from inbound
// EOF, as an SSH channel does when its peer has not closed the connection.
type holdReviewChannel struct{ eof chan struct{} }

func (c *holdReviewChannel) Read([]byte) (int, error)                     { <-c.eof; return 0, io.EOF }
func (*holdReviewChannel) Write(p []byte) (int, error)                    { return len(p), nil }
func (*holdReviewChannel) Close() error                                   { return nil }
func (*holdReviewChannel) CloseWrite() error                              { return nil }
func (*holdReviewChannel) SendRequest(string, bool, []byte) (bool, error) { return false, nil }
func (c *holdReviewChannel) Stderr() io.ReadWriter                        { return c }

// The source reservation fixture supplies real charged rows for private hold
// mechanics only. Actual destination admission/SSH lifecycle is exercised
// separately by TestSyncDestinationInlineWorkerOriginalLifecycle.
func reviewSyncHold(t *testing.T) (*Manager, *SyncWorkerHold) {
	t.Helper()
	m, root, parent := syncManagerFixture(t)
	intent, rows, control := sourceSyncIntent(t, m, root)
	l, err := m.ReserveSyncSource(context.Background(), intent, rows, root, parent, control)
	if err != nil {
		t.Fatal(err)
	}
	channel := &holdReviewChannel{eof: make(chan struct{})}
	t.Cleanup(func() { close(channel.eof) })
	runtime, err := newSSHRuntime(context.Background(), model.SSHExecutionRequest{TTL: time.Minute}, sshservice.Execution{}, channel, nil, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var volume string
	if err = m.db.QueryRow(`SELECT volume FROM sync_lease_bindings WHERE lease=?`, l.ID).Scan(&volume); err != nil {
		t.Fatal(err)
	}
	h := newSyncWorkerHold(m, l, &syncAdmission{intent: intent, root: root, parent: parent, volume: volume}, runtime)
	m.mu.Lock()
	m.syncWorkers[l.ID] = h
	m.mu.Unlock()
	t.Cleanup(h.JoinTransport)
	return m, h
}

func TestSyncWorkerReviewRejectsReplacedLeaseAndBinding(t *testing.T) {
	for _, change := range []string{"peer", "cpu", "owner", "revision", "volume", "binding-key", "binding-side", "binding-disk"} {
		t.Run(change, func(t *testing.T) {
			m, h := reviewSyncHold(t)
			if err := h.CheckActive(context.Background()); err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			l, err := m.get(h.lease)
			if err != nil {
				m.mu.Unlock()
				t.Fatal(err)
			}
			switch change {
			case "peer":
				l.Request.Peer = "changed-source"
			case "cpu":
				l.Request.Resources.CPU += .1
			case "owner":
				l.Owner = "changed-owner"
			case "revision":
				l.Revision++
			case "volume":
				_, err = m.db.Exec(`UPDATE sync_lease_bindings SET volume='changed' WHERE lease=?`, h.lease)
			case "binding-key":
				_, err = m.db.Exec(`UPDATE sync_lease_bindings SET key='another-key' WHERE lease=?`, h.lease)
			case "binding-side":
				_, err = m.db.Exec(`UPDATE sync_lease_bindings SET side='remote' WHERE lease=?`, h.lease)
			case "binding-disk":
				_, err = m.db.Exec(`UPDATE sync_lease_bindings SET disk_bytes=disk_bytes+1 WHERE lease=?`, h.lease)
			}
			if change == "peer" || change == "cpu" || change == "owner" || change == "revision" {
				err = m.put(l, "review.replacement")
			}
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err = h.CheckActive(context.Background()); err == nil {
				t.Error("replaced authority accepted by active check")
			}
			if err = h.Start(func(context.Context, ssh.Channel) error { return nil }); err == nil {
				t.Error("replaced authority launched a worker")
			}
		})
	}
}

func TestSyncWorkerReviewWaitCancellationPrecedesOriginalJoin(t *testing.T) {
	m, h := reviewSyncHold(t)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	if err := h.Start(func(ctx context.Context, _ ssh.Channel) error {
		close(entered)
		<-ctx.Done()
		<-release
		return ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := m.End(h.lease, "cancelled"); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := h.Wait(wait); !errors.Is(err, context.Canceled) {
		t.Errorf("original cancellation hidden until worker join: %v", err)
	}
	select {
	case <-h.done:
		t.Error("unjoined worker reported complete")
	default:
	}
}

func TestSyncWorkerReviewEndCancelsAttentionOwner(t *testing.T) {
	m, h := reviewSyncHold(t)
	m.mu.Lock()
	l, err := m.get(h.lease)
	if err == nil {
		err = m.retainSyncLease(&l, "unknown")
	}
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = m.End(h.lease, "cancelled")
	if h.runtime.ctx.Err() == nil {
		t.Error("End left original uncertain worker uncancelled")
	}
}

var _ ssh.Channel = (*holdReviewChannel)(nil)
var _ syncservice.DecodeAdmission = (*SyncWorkerHold)(nil)

func TestSyncWorkerReviewCheckpointFailureIsNotSuccess(t *testing.T) {
	m, h := reviewSyncHold(t)
	_, err := m.db.Exec(`CREATE TRIGGER review_refuse_sync_checkpoint BEFORE UPDATE OF data ON leases WHEN NEW.id='` + h.lease + `' BEGIN SELECT RAISE(ABORT,'retained checkpoint refused'); END`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.db.Exec(`DROP TRIGGER review_refuse_sync_checkpoint`) })
	if err = h.Start(func(context.Context, ssh.Channel) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	exit, err := h.Wait(ctx)
	if err == nil && exit.Code == 0 {
		t.Error("failed retained checkpoint reported successful worker")
	}
}

func TestSyncWorkerReviewOneShotOriginalBindings(t *testing.T) {
	m, h := reviewSyncHold(t)
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{})
	results := make(chan error, 8)
	for range 8 {
		go func() {
			results <- h.Start(func(ctx context.Context, ch ssh.Channel) error {
				if ctx != h.runtime.ctx || ch != h.runtime.channel {
					t.Error("worker received replacement context or channel")
				}
				close(entered)
				<-release
				return nil
			})
		}()
	}
	successes := 0
	for range 8 {
		if err := <-results; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("one-shot launches=%d", successes)
	}
	<-entered
	owners, _ := m.cpuOwners()
	for _, owner := range owners {
		if owner.Token == h.lease {
			t.Error("inline worker invented native CPU owner credit")
		}
	}
	m.mu.Lock()
	credit := m.currentCPUCredit([]cpuCreditSample{{id: h.lease, cores: .25}}, .25, .25)
	m.mu.Unlock()
	if credit != 0 {
		t.Error("inline worker invented measured CPU credit")
	}
	if _, err := m.Renew(h.lease, time.Hour); err == nil {
		t.Error("fixed original worker lifetime renewed")
	}
}

func TestSyncWorkerReviewJoinRetiresOriginalCallback(t *testing.T) {
	_, h := reviewSyncHold(t)
	if !h.runtime.stopClose() {
		t.Fatal("original live callback already ran")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	h.runtime.stopClose = context.AfterFunc(h.runtime.ctx, func() { close(entered); <-release; close(h.runtime.closeDone) })
	joined := make(chan struct{})
	go func() { h.JoinTransport(); close(joined) }()
	<-entered
	select {
	case <-joined:
		t.Error("transport join returned before original callback retired")
	default:
	}
	close(release)
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("transport join did not finish after original callback")
	}
}

func TestSyncWorkerReviewCompletionPreservesChangedSuccessor(t *testing.T) {
	m, h := reviewSyncHold(t)
	entered, release := make(chan struct{}), make(chan struct{})
	if err := h.Start(func(context.Context, ssh.Channel) error { close(entered); <-release; return nil }); err != nil {
		t.Fatal(err)
	}
	<-entered
	m.mu.Lock()
	l, err := m.get(h.lease)
	if err == nil {
		l.Request.Peer = "successor-source"
		err = m.put(l, "review.replacement")
	}
	m.mu.Unlock()
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	<-h.done
	exit, err := h.Wait(context.Background())
	if err == nil && exit.Code == 0 {
		t.Error("changed predecessor reported success")
	}
	if _, err = m.End(h.lease, "cancelled"); err == nil {
		t.Error("End accepted changed successor")
	}
	current, err := m.Inspect(h.lease)
	if err != nil || current.State != l.State || current.Revision != l.Revision || current.Request.Peer != "successor-source" {
		t.Fatalf("changed successor overwritten: %+v %v", current, err)
	}
}

func TestSyncWorkerReviewForceStopRetainsPromise(t *testing.T) {
	m, h := reviewSyncHold(t)
	entered := make(chan struct{})
	if err := h.Start(func(ctx context.Context, _ ssh.Channel) error { close(entered); <-ctx.Done(); return ctx.Err() }); err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := m.BeginShutdown(model.ShutdownRequest{Force: true}); err != nil {
		t.Fatal(err)
	}
	report, err := m.AdvanceShutdown()
	if err != nil {
		t.Fatal(err)
	}
	if report.Phase != "blocked" || len(report.Remaining) != 1 || report.Remaining[0].ID != h.lease {
		t.Fatalf("force stop hid charged inline owner: %+v", report)
	}
	<-h.done
	l, err := m.Inspect(h.lease)
	if err != nil || l.State != "needs_attention" || l.Ended != nil {
		t.Fatalf("force stop released uncertain promise: %+v %v", l, err)
	}
}
