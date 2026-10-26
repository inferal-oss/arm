package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type sourceTestChannel struct {
	once   sync.Once
	closed chan struct{}
	mu     sync.Mutex
	output bytes.Buffer
}

func newSourceTestChannel() *sourceTestChannel {
	return &sourceTestChannel{closed: make(chan struct{})}
}
func (c *sourceTestChannel) Read([]byte) (int, error) {
	<-c.closed
	return 0, io.EOF
}
func (c *sourceTestChannel) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closed:
		return 0, io.ErrClosedPipe
	default:
		return c.output.Write(p)
	}
}
func (c *sourceTestChannel) Close() error                                 { c.once.Do(func() { close(c.closed) }); return nil }
func (*sourceTestChannel) CloseWrite() error                              { return nil }
func (*sourceTestChannel) SendRequest(string, bool, []byte) (bool, error) { return true, nil }
func (*sourceTestChannel) Stderr() io.ReadWriter                          { return &bytes.Buffer{} }

func TestSyncSourceHoldOpensAfterReservationAndJoinsOriginal(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	ch := newSourceTestChannel()
	var leaseID string
	var opened, joined int
	l, hold, err := m.AdmitSyncSource(t.Context(), i, rows, root, parent, control, func(ctx context.Context) (SyncSourceChannel, error) {
		opened++
		prior, e := m.Inspect(leaseID)
		if e != nil || prior.State != "active" || prior.Request.OriginKey != i.Key {
			return SyncSourceChannel{}, errors.New("original source reservation was not charged before SSH open")
		}
		requests := make(chan *ssh.Request)
		close(requests)
		return SyncSourceChannel{Channel: ch, Requests: requests, CloseTransport: ch.Close, Join: func() error { joined++; return nil }}, nil
	})
	if err != nil || l.State != "active" || hold == nil || opened != 0 {
		t.Fatalf("source admission/open order: %+v %v opened=%d", l, err, opened)
	}
	leaseID = l.ID
	if err := hold.Start(func(ctx context.Context, original ssh.Channel, _ <-chan *ssh.Request) error {
		if original != ch {
			return errors.New("source received another channel")
		}
		if err := hold.CheckIntent(ctx, i); err != nil {
			return err
		}
		if err := hold.ChargeVolume(ctx, i.Binding.Local.RootID, 1<<20); err != nil {
			return err
		}
		_, err := original.Write([]byte("source-worker"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := hold.Start(func(context.Context, ssh.Channel, <-chan *ssh.Request) error {
		t.Error("duplicate source worker ran")
		return nil
	}); err == nil {
		t.Fatal("second source worker accepted")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := hold.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	hold.JoinTransport()
	if opened != 1 || joined != 1 || ch.output.String() != "source-worker" {
		t.Fatalf("original transport not joined once: opened=%d joined=%d output=%q", opened, joined, ch.output.String())
	}
	retained, err := m.Inspect(l.ID)
	if err != nil || retained.State != "needs_attention" {
		t.Fatalf("source promise lost: %+v %v", retained, err)
	}
	if err := m.verifySyncRecords(t.Context(), true); err != nil {
		t.Fatal(err)
	}
}

func TestSyncSourceHoldEndCancelsBlockedOriginal(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	ch := newSourceTestChannel()
	entered := make(chan struct{})
	joined := make(chan struct{})
	l, hold, err := m.AdmitSyncSource(t.Context(), i, rows, root, parent, control, func(context.Context) (SyncSourceChannel, error) {
		requests := make(chan *ssh.Request)
		close(requests)
		return SyncSourceChannel{Channel: ch, Requests: requests, CloseTransport: ch.Close, Join: func() error { close(joined); return nil }}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := hold.Start(func(_ context.Context, original ssh.Channel, _ <-chan *ssh.Request) error {
		close(entered)
		_, err := original.Read(make([]byte, 1))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := m.End(l.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := hold.Wait(ctx); err == nil {
		t.Fatal("cancelled source worker reported success")
	}
	hold.JoinTransport()
	select {
	case <-joined:
	case <-ctx.Done():
		t.Fatal("original source transport did not join")
	}
	retained, err := m.Inspect(l.ID)
	if err != nil || retained.State != "needs_attention" {
		t.Fatalf("cancelled source promise lost: %+v %v", retained, err)
	}
}

func TestSyncSourceHoldRejectsSameRevisionLeaseReplacement(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	l, hold, err := m.AdmitSyncSource(t.Context(), i, rows, root, parent, control, func(context.Context) (SyncSourceChannel, error) {
		t.Error("replaced lease opened an SSH channel")
		return SyncSourceChannel{}, errors.New("unreachable")
	})
	if err != nil {
		t.Fatal(err)
	}
	replacement := l
	replacement.Request.Reason = "different same-revision lease row"
	changed, err := json.Marshal(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(changed), l.ID); err != nil {
		t.Fatal(err)
	}
	if err := hold.CheckActive(t.Context()); err == nil {
		t.Fatal("same-revision lease replacement retained worker authority")
	}
	if err := hold.Start(func(context.Context, ssh.Channel, <-chan *ssh.Request) error { return nil }); err == nil {
		t.Fatal("same-revision lease replacement started worker")
	}
	if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(hold.leaseRecord), l.ID); err != nil {
		t.Fatal(err)
	}
	hold.JoinTransport()
	retained, err := m.Inspect(l.ID)
	if err != nil || retained.State != "needs_attention" || retained.Outcome != "sync_source_transport_closed" {
		t.Fatalf("original source charge not retained: %+v %v", retained, err)
	}
}

var _ ssh.Channel = (*sourceTestChannel)(nil)
