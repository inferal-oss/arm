package meshsshrelay

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/manager"
	"getarm.org/internal/model"
)

func TestAbortWorkerStopJoinsAlreadyRunningCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	entered, release := make(chan struct{}), make(chan struct{})
	w := startAbortWorker(ctx, func() {
		close(entered)
		<-release
	})
	cancel()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("abort callback did not start")
	}
	returned := make(chan bool, 1)
	go func() { returned <- w.StopAndJoin() }()
	select {
	case <-returned:
		t.Fatal("StopAndJoin returned while abort callback was held")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case stopped := <-returned:
		if stopped {
			t.Fatal("already running callback reported stopped")
		}
	case <-time.After(time.Second):
		t.Fatal("StopAndJoin did not finish after callback returned")
	}
}

func relayChargeManager(t *testing.T) *manager.Manager {
	t.Helper()
	home := filepath.Join(t.TempDir(), "manager")
	m, err := manager.Open(home, model.Resources{CPU: 2, Memory: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	m.Start()
	awaitRelayChargeHeadroom(t, m)
	return m
}

func awaitRelayChargeHeadroom(t *testing.T, m *manager.Manager) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, e := m.Status()
		if e != nil {
			t.Fatal(e)
		}
		if status.CPU != nil && status.CPU.Known && !status.CPU.Stale && status.CPU.Available >= .01 && status.Memory != nil && !status.Memory.Stale && status.Memory.Available >= 1<<20 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("test Manager lacked fresh transit CPU/memory headroom: cpu=%+v memory=%+v", status.CPU, status.Memory)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Keep lifecycle fixtures on genuine native capacity. A failed nonqueued
// admission owns no lease, so only transient capacity refusal can be retried.
func acquireRelayChargeWithHeadroom(admission, lifetime context.Context, m *manager.Manager, request model.Request, expires time.Time, joined <-chan struct{}) (*manager.RelayTransit, error) {
	for {
		if err := admission.Err(); err != nil {
			return nil, err
		}
		request.TTL = time.Until(expires)
		hold, err := m.AcquireRelayTransit(lifetime, request, joined)
		var cause *model.Error
		if err == nil || !errors.As(err, &cause) || cause.Code != "insufficient_capacity" {
			return hold, err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-admission.Done():
			timer.Stop()
			return nil, admission.Err()
		case <-timer.C:
		}
	}
}

func TestHeldRelayAbortKeepsTransitResourcesCharged(t *testing.T) {
	m := relayChargeManager(t)
	f := routeTransportFixture(t)
	leaseID := make(chan string, 1)
	f.relay.ReserveTransit = func(_, life context.Context, joined <-chan struct{}, _ Outer, o Open, _ Permit) (TransitReservation, error) {
		hold, e := m.AcquireRelayTransit(life, model.Request{Key: "held-abort:" + o.Route,
			Reason:    "held cancellation callback keeps transit resources charged",
			Resources: model.Resources{CPU: .01, Memory: 1 << 20}, TTL: time.Until(o.Expires)}, joined)
		if e != nil {
			return TransitReservation{}, e
		}
		lease, e := hold.Lease()
		if e != nil {
			return TransitReservation{}, e
		}
		leaseID <- lease.ID
		return TransitReservation{Context: hold.Context(), CheckActive: hold.CheckActive, End: func() { _, _ = m.End(lease.ID, "relay_closed") },
			Finish: func() { _, _ = hold.FinishAfterJoin() }}, nil
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	f.relay.abortHook = func() {
		enteredOnce.Do(func() { close(entered) })
		<-release
	}
	awaitRelayChargeHeadroom(t, m)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, err := f.dial(ctx, f.open)
	if err != nil {
		status, _ := m.Status()
		t.Fatalf("held relay admission: %v; cpu=%+v memory=%+v", err, status.CPU, status.Memory)
	}
	defer conn.Close()
	id := <-leaseID
	f.relay.ClosePeer(f.b.Config().Identity.Host)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("cancellation callback did not enter")
	}
	status, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Reserved.CPU < .01 || status.Reserved.Memory < 1<<20 {
		t.Fatal("transit resources were released while cancellation callback remained active")
	}
	workerDone := make(chan struct{})
	go func() { f.relay.Wait(); close(workerDone) }()
	select {
	case <-workerDone:
		t.Fatal("relay worker returned before cancellation callback joined")
	case <-time.After(20 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("relay worker failed to join after callback returned")
	}
	lease, err := m.Inspect(id)
	if err != nil || lease.State != "terminal" || lease.Outcome != "relay_closed" {
		t.Fatalf("transit charge did not settle after callback join: state=%s outcome=%s error=%v", lease.State, lease.Outcome, err)
	}
}

func TestFailedTransitAdmissionJoinsHeldAbortBeforeChargeSettlement(t *testing.T) {
	m := relayChargeManager(t)
	f := routeTransportFixture(t)
	leaseID := make(chan string, 1)
	admissionError := make(chan error, 1)
	f.relay.ReserveTransit = func(admission, life context.Context, joined <-chan struct{}, _ Outer, o Open, _ Permit) (TransitReservation, error) {
		hold, err := acquireRelayChargeWithHeadroom(admission, life, m, model.Request{Key: "failed-abort:" + o.Route,
			Reason:    "failed admission retains original relay callback charge",
			Resources: model.Resources{CPU: .01, Memory: 1 << 20}}, o.Expires, joined)
		if err != nil {
			admissionError <- err
			return TransitReservation{}, err
		}
		lease, err := hold.Lease()
		if err != nil {
			return TransitReservation{}, err
		}
		leaseID <- lease.ID
		<-admission.Done()
		return TransitReservation{Context: hold.Context(), CheckActive: hold.CheckActive, End: func() { _, _ = m.End(lease.ID, "relay_closed") },
			Finish: func() { _, _ = hold.FinishAfterJoin() }}, admission.Err()
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	f.relay.abortHook = func() {
		enteredOnce.Do(func() { close(entered) })
		<-release
	}
	awaitRelayChargeHeadroom(t, m)
	ctx, cancel := context.WithTimeout(t.Context(), 9*time.Second)
	defer cancel()
	dialDone := make(chan struct{})
	go func() {
		conn, _ := f.dial(ctx, f.open)
		if conn != nil {
			_ = conn.Close()
		}
		close(dialDone)
	}()
	var id string
	select {
	case id = <-leaseID:
	case err := <-admissionError:
		t.Fatal("relay test transport admission failed", err)
	case <-ctx.Done():
		t.Fatal("relay did not acquire the test transport hold within its original context", ctx.Err())
	}
	select {
	case <-entered:
	case <-time.After(7 * time.Second):
		t.Fatal("admission timeout did not enter abort callback")
	}
	status, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Reserved.CPU < .01 || status.Reserved.Memory < 1<<20 {
		t.Fatal("failed admission released resources while its abort callback was held")
	}
	workerDone := make(chan struct{})
	go func() { f.relay.Wait(); close(workerDone) }()
	select {
	case <-workerDone:
		t.Fatal("failed admission published transport join before held callback returned")
	case <-time.After(20 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("failed admission did not join after callback returned")
	}
	select {
	case <-dialDone:
	case <-time.After(time.Second):
		t.Fatal("source dial did not return after failed admission")
	}
	lease, err := m.Inspect(id)
	if err != nil || lease.State != "terminal" || lease.Outcome != "relay_closed" {
		t.Fatalf("failed admission charge did not settle after join: state=%s outcome=%s error=%v", lease.State, lease.Outcome, err)
	}
}
