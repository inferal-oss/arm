package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestSyncSourceReviewOriginalCancellationUnblocksRead(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ch := newSourceTestChannel()
	_, hold, err := m.AdmitSyncSource(ctx, i, rows, root, parent, control, func(context.Context) (SyncSourceChannel, error) {
		return SyncSourceChannel{Channel: ch, Requests: make(chan *ssh.Request), CloseTransport: ch.Close, Join: func() error { return nil }}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer hold.JoinTransport()
	entered := make(chan struct{})
	if err := hold.Start(func(context.Context, ssh.Channel, <-chan *ssh.Request) error {
		close(entered)
		_, err := ch.Read(make([]byte, 1))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	<-entered
	cancel()
	select {
	case <-hold.done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("original cancellation left source blocked on channel Read")
	}
}

func TestSyncSourceReviewRechecksOwnerAfterOpen(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	ch, opening, release := newSourceTestChannel(), make(chan struct{}), make(chan struct{})
	_, hold, err := m.AdmitSyncSource(t.Context(), i, rows, root, parent, control, func(context.Context) (SyncSourceChannel, error) {
		close(opening)
		<-release
		return SyncSourceChannel{Channel: ch, Requests: make(chan *ssh.Request), CloseTransport: ch.Close, Join: func() error { return nil }}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer hold.JoinTransport()
	var ran atomic.Bool
	if err := hold.Start(func(context.Context, ssh.Channel, <-chan *ssh.Request) error { ran.Store(true); return nil }); err != nil {
		t.Fatal(err)
	}
	<-opening
	m.mu.Lock()
	l, err := m.get(hold.lease)
	if err == nil {
		l.Request.Reason = "same revision replacement while opening"
		err = m.put(l, "review.replacement")
	}
	m.mu.Unlock()
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_ = hold.Wait(ctx)
	if ran.Load() {
		t.Fatal("replaced admission launched protocol after delayed opener")
	}
}

func TestSyncSourceReviewAdmissionCannotAdoptReplacedRow(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	// Controlled same-transaction replacement of the returned original row.
	// It is not an external CPU observation or native transport acceptance.
	_, err := m.db.Exec(`CREATE TRIGGER review_replace_source AFTER INSERT ON leases WHEN json_extract(NEW.data,'$.request.kind')='execution' BEGIN UPDATE leases SET data=json_set(data,'$.request.reason','replacement predecessor') WHERE id=NEW.id; END`)
	if err != nil {
		t.Fatal(err)
	}
	defer m.db.Exec(`DROP TRIGGER review_replace_source`)
	l, hold, err := m.AdmitSyncSource(t.Context(), i, rows, root, parent, control, func(context.Context) (SyncSourceChannel, error) {
		t.Error("replaced reservation opened transport")
		return SyncSourceChannel{}, errors.New("unreachable")
	})
	if hold != nil {
		defer hold.JoinTransport()
	}
	if err == nil || hold != nil {
		t.Fatal("adopted changed row as original source admission")
	}
	var raw string
	if err := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var retained map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &retained); err != nil {
		t.Fatal(err)
	}
	current, err := m.Inspect(l.ID)
	if err != nil || current.Revision != l.Revision || current.State != "active" || current.Request.Reason != "replacement predecessor" {
		t.Fatal("setup failure overwrote changed successor", current, err)
	}
}

func TestSyncSourceReviewCancellationOwnsOriginalCloserAndJoin(t *testing.T) {
	for _, mode := range []string{"parent", "expiry", "end", "close"} {
		t.Run(mode, func(t *testing.T) {
			m, root, parent := syncManagerFixture(t)
			i, rows, control := sourceSyncIntent(t, m, root)
			if mode == "expiry" {
				control.TTL = 2 * time.Second
				var err error
				i.SSHControlSHA256, err = control.Digest()
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			// Channel.Close deliberately does NOT establish inbound EOF.
			ch := &holdReviewChannel{eof: make(chan struct{})}
			closing, callbackRelease, joining, joinRelease := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var callbackOnce, joinOnce sync.Once
			releaseCallback := func() { callbackOnce.Do(func() { close(callbackRelease) }) }
			releaseJoin := func() { joinOnce.Do(func() { close(joinRelease) }) }
			l, hold, err := m.AdmitSyncSource(ctx, i, rows, root, parent, control, func(context.Context) (SyncSourceChannel, error) {
				return SyncSourceChannel{Channel: ch, Requests: make(chan *ssh.Request), CloseTransport: func() error { close(ch.eof); close(closing); <-callbackRelease; return nil }, Join: func() error { close(joining); <-joinRelease; return nil }}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer hold.JoinTransport()
			defer releaseJoin()
			defer releaseCallback()
			entered := make(chan struct{})
			if err := hold.Start(func(original context.Context, channel ssh.Channel, _ <-chan *ssh.Request) error {
				if original != hold.ctx || channel != ch {
					t.Error("original source tuple replaced")
				}
				close(entered)
				_, err := channel.Read(make([]byte, 1))
				return err
			}); err != nil {
				t.Fatal(err)
			}
			<-entered
			closed := make(chan error, 1)
			switch mode {
			case "parent":
				cancel()
			case "end":
				if _, err := m.End(l.ID, "cancelled"); err != nil {
					t.Fatal(err)
				}
			case "close":
				go func() { closed <- m.Close() }()
			}
			select {
			case <-closing:
			case <-time.After(4 * time.Second):
				t.Fatal("original carrier not closed by cancellation")
			}
			select {
			case <-hold.done:
				t.Fatal("done before original cancellation callback retired")
			default:
			}
			wait, stop := context.WithTimeout(t.Context(), time.Second)
			defer stop()
			err = hold.Wait(wait)
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("original cancellation not reported while callback is pending", err)
			}
			releaseCallback()
			select {
			case <-joining:
			case <-time.After(time.Second):
				t.Fatal("original transport join not reached")
			}
			select {
			case <-hold.done:
				t.Fatal("done before original transport joined")
			default:
			}
			releaseJoin()
			select {
			case <-hold.done:
			case <-time.After(time.Second):
				t.Fatal("original source worker did not join")
			}
			if mode == "close" {
				select {
				case err := <-closed:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("Manager.Close did not join original source")
				}
			} else {
				current, err := m.Inspect(l.ID)
				if err != nil || current.State != "needs_attention" {
					t.Fatal("cancelled source charge lost", current, err)
				}
			}
		})
	}
}

func TestSyncSourceReviewOneShotLaunchKeepsOriginalWitnesses(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	ch := newSourceTestChannel()
	var opened, closed, joined, borrowed atomic.Int32
	_, hold, err := m.AdmitSyncSource(t.Context(), i, rows, root, parent, control, func(context.Context) (SyncSourceChannel, error) {
		opened.Add(1)
		return SyncSourceChannel{Channel: ch, Requests: make(chan *ssh.Request), CloseTransport: func() error { closed.Add(1); return ch.Close() }, Join: func() error { joined.Add(1); return nil }}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer hold.JoinTransport()
	entered, results := make(chan struct{}), make(chan error, 8)
	for range 8 {
		go func() {
			results <- hold.Start(func(context.Context, ssh.Channel, <-chan *ssh.Request) error {
				close(entered)
				_, err := ch.Read(make([]byte, 1))
				return err
			})
		}()
	}
	started := 0
	for range 8 {
		if err := <-results; err == nil {
			started++
		}
	}
	if started != 1 {
		t.Fatal("not one-shot", started)
	}
	<-entered
	m.mu.Lock()
	hold.open = func(context.Context) (SyncSourceChannel, error) { borrowed.Add(1); return SyncSourceChannel{}, nil }
	m.mu.Unlock()
	hold.cancel()
	select {
	case <-hold.done:
	case <-time.After(time.Second):
		t.Fatal("original source did not join")
	}
	if opened.Load() != 1 || closed.Load() != 1 || joined.Load() != 1 || borrowed.Load() != 0 {
		t.Fatal("source swapped or repeated original transport witnesses", opened.Load(), closed.Load(), joined.Load(), borrowed.Load())
	}
	owners, _ := m.cpuOwners()
	for _, owner := range owners {
		if owner.Token == hold.lease {
			t.Fatal("source Go worker fabricated native CPU credit")
		}
	}
}

func TestSyncSourceReviewFailuresRetainOriginalCharges(t *testing.T) {
	for _, mode := range []string{"open", "close", "join", "missing-close", "checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			m, root, parent := syncManagerFixture(t)
			i, rows, control := sourceSyncIntent(t, m, root)
			ch, fault := newSourceTestChannel(), errors.New("controlled original transport fault")
			var runs, joins atomic.Int32
			_, hold, err := m.AdmitSyncSource(t.Context(), i, rows, root, parent, control, func(context.Context) (SyncSourceChannel, error) {
				owned := SyncSourceChannel{Channel: ch, Requests: make(chan *ssh.Request), CloseTransport: func() error {
					_ = ch.Close()
					if mode == "close" {
						return fault
					}
					return nil
				}, Join: func() error {
					joins.Add(1)
					if mode == "join" {
						return fault
					}
					return nil
				}}
				if mode == "missing-close" {
					owned.CloseTransport = nil
				}
				if mode == "open" {
					return owned, fault
				}
				return owned, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer hold.JoinTransport()
			if mode == "checkpoint" {
				if _, err := m.db.Exec(`CREATE TRIGGER review_source_checkpoint BEFORE UPDATE OF data ON leases WHEN NEW.id='` + hold.lease + `' BEGIN SELECT RAISE(ABORT,'source checkpoint refused'); END`); err != nil {
					t.Fatal(err)
				}
				defer m.db.Exec(`DROP TRIGGER review_source_checkpoint`)
			}
			if err := hold.Start(func(context.Context, ssh.Channel, <-chan *ssh.Request) error { runs.Add(1); return nil }); err != nil {
				t.Fatal(err)
			}
			wait, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if err := hold.Wait(wait); err == nil {
				t.Fatal("failed original transport/checkpoint reported success")
			}
			if joins.Load() != 1 {
				t.Fatal("partial/failed original opener was not joined exactly once", joins.Load())
			}
			if (mode == "open" || mode == "missing-close") && runs.Load() != 0 {
				t.Fatal("invalid original transport launched protocol")
			}
			l, err := m.Inspect(hold.lease)
			if err != nil || !consuming(l.State) || l.Request.Resources != i.LocalResources {
				t.Fatal("failed source resource promises were released", l, err)
			}
		})
	}
}

func TestSyncSourceReviewAlreadyClosedCarrierStillJoinsOriginal(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	ch := newSourceTestChannel()
	joining, release := make(chan struct{}), make(chan struct{})
	entered, protocolRelease := make(chan struct{}), make(chan struct{})
	var borrowed atomic.Int32
	var once sync.Once
	finishJoin := func() { once.Do(func() { close(release) }) }
	_, hold, err := m.AdmitSyncSource(t.Context(), i, rows, root, parent, control, func(context.Context) (SyncSourceChannel, error) {
		return SyncSourceChannel{Channel: ch, Requests: make(chan *ssh.Request), CloseTransport: func() error { _ = ch.Close(); return net.ErrClosed }, Join: func() error { close(joining); <-release; return nil }}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer hold.JoinTransport()
	defer finishJoin()
	if err := hold.Start(func(context.Context, ssh.Channel, <-chan *ssh.Request) error {
		close(entered)
		<-protocolRelease
		return ch.Close()
	}); err != nil {
		t.Fatal(err)
	}
	<-entered
	m.mu.Lock()
	hold.open = func(context.Context) (SyncSourceChannel, error) {
		borrowed.Add(1)
		return SyncSourceChannel{}, net.ErrClosed
	}
	m.mu.Unlock()
	close(protocolRelease)
	select {
	case <-joining:
	case <-time.After(time.Second):
		t.Fatal("already-closed original carrier bypassed Join")
	}
	select {
	case <-hold.done:
		t.Fatal("already-closed carrier reported completion before original Join")
	default:
	}
	finishJoin()
	wait, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := hold.Wait(wait); err != nil {
		t.Fatal("idempotent original close invented protocol failure", err)
	}
	if borrowed.Load() != 0 {
		t.Fatal("idempotent close borrowed a successor opener")
	}
	l, err := m.Inspect(hold.lease)
	if err != nil || l.State != "needs_attention" || l.Outcome != "sync_source_complete" || l.Ended != nil {
		t.Fatal("completed source promise was released or misreported", l, err)
	}
}
