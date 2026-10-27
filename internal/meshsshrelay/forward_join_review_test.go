package meshsshrelay

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
)

func TestReviewForwardFinalHoldRetainsOriginalAbortCallback(t *testing.T) {
	for _, failAdmission := range []bool{false, true} {
		name := "active"
		if failAdmission {
			name = "failed-admission"
		}
		t.Run(name, func(t *testing.T) {
			m := relayChargeManager(t)
			f := routeTransportFixture(t)
			f.open.Class, f.open.Service, f.open.SSHHostKeyPin = ClassForward, "web", strings.Repeat("0", 64)
			for _, h := range []*Handler{f.relay, f.destination} {
				h.RoutePath, h.Subprotocol, h.AllowedClass = ForwardPath, ForwardProtocol, ClassForward
			}
			previous := f.destination.Authorize
			f.destination.Authorize = func(ctx context.Context, outer Outer, o Open) (Permit, error) {
				p, err := previous(ctx, outer, o)
				if err != nil {
					return p, err
				}
				p.Inner, err = f.c.RoutedForwardTLS(identity(f.c.Config().Identity), identity(f.b.Config().Identity), true)
				return p, err
			}
			leased := make(chan string, 1)
			settled := make(chan model.Lease, 1)
			f.destination.ReserveFinal = func(_, life context.Context, joined <-chan struct{}, _ Outer, o Open, _ Permit) (TransitReservation, error) {
				hold, err := m.AcquireRelayTransit(life, model.Request{Key: "forward-join:" + o.Route, Reason: "forward callback original join review", Resources: model.Resources{CPU: .01, Memory: 1 << 20}, TTL: time.Until(o.Expires)}, joined)
				if err != nil {
					return TransitReservation{}, err
				}
				lease, err := hold.Lease()
				if err != nil {
					return TransitReservation{}, err
				}
				leased <- lease.ID
				reservation := TransitReservation{Context: hold.Context(), CheckActive: hold.CheckActive, End: func() { _, _ = m.End(lease.ID, "relay_closed") }, Finish: func() { l, _ := hold.FinishAfterJoin(); settled <- l }}
				if failAdmission {
					<-life.Done()
					return reservation, errors.New("retained final admission failure")
				}
				return reservation, nil
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var enterOnce, releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			f.destination.abortHook = func() { enterOnce.Do(func() { close(entered) }); <-release }
			peer, err := f.b.Resolve(f.a.Config().Identity.Host)
			if err != nil {
				t.Fatal(err)
			}
			outer, err := f.b.ClientTLS(peer)
			if err != nil {
				t.Fatal(err)
			}
			var inner *tls.Config
			inner, err = f.b.RoutedForwardTLS(identity(f.b.Config().Identity), identity(f.c.Config().Identity), false)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			conn, err := Dial(ctx, f.relayServer.Listener.Addr().String(), outer, inner, f.open, func(context.Context) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			var id string
			select {
			case id = <-leased:
			case <-time.After(2 * time.Second):
				t.Fatal("final hold not admitted")
			}
			f.destination.ClosePeer(f.a.Config().Identity.Host)
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("original abort callback did not enter")
			}
			select {
			case l := <-settled:
				t.Fatalf("final resource settlement preceded held original callback join: state=%s", l.State)
			case <-time.After(100 * time.Millisecond):
			}
			l, err := m.Inspect(id)
			if err != nil || l.State == "terminal" {
				t.Fatal("final resources no longer charged", l, err)
			}
			releaseOnce.Do(func() { close(release) })
			select {
			case l := <-settled:
				if l.State != "terminal" {
					t.Fatal(l)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("final resources failed to settle after original join")
			}
		})
	}
}
