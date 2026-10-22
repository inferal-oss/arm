package workspaceroute

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestPendingMessageControlsCannotConsumeReservedLookupSlot(t *testing.T) {
	f := routeTransportFixture(t)
	peer, e := f.b.Resolve(f.a.Config().Identity.Host)
	if e != nil {
		t.Fatal(e)
	}
	config, e := f.b.ClientTLS(peer)
	if e != nil {
		t.Fatal(e)
	}
	transport := &http.Transport{Proxy: nil, DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != peer.Identity.Address {
			return nil, fmt.Errorf("test exact target changed")
		}
		raw, e := (&net.Dialer{}).DialContext(ctx, "tcp", address)
		if e != nil {
			return nil, e
		}
		conn := tls.Client(raw, config)
		if e = conn.HandshakeContext(ctx); e != nil {
			raw.Close()
			return nil, e
		}
		return conn, nil
	}}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	sockets := []*websocket.Conn{}
	for i := 0; i < 6; i++ {
		ws, _, e := websocket.Dial(ctx, "wss://"+peer.Identity.Address+Path, &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, Subprotocols: []string{Protocol}, HTTPHeader: http.Header{"Arm-Workspace-Class": []string{ClassMessage}}})
		if e != nil {
			t.Fatal("pending message reservation", i, e)
		}
		sockets = append(sockets, ws)
	}
	defer func() {
		for _, ws := range sockets {
			_ = ws.CloseNow()
		}
	}()
	ordinary := f.open
	ordinary.Route = strings.Repeat("4", 32)
	if conn, e := f.dial(ctx, ordinary); e == nil {
		conn.Close()
		t.Fatal("message peer pending bound exceeded")
	}
	lookup := f.open
	lookup.Class = ClassLookup
	lookup.Route = strings.Repeat("5", 32)
	conn, e := f.dial(ctx, lookup)
	if e != nil {
		t.Fatal("ordinary pending messages starved reserved lookup", e)
	}
	conn.Close()
	// A control frame cannot switch into the reserved lookup class after the
	// ordinary class header claimed its independently counted reservation.
	changed := f.open
	changed.Class = ClassLookup
	changed.Route = strings.Repeat("6", 32)
	raw, _ := json.Marshal(changed)
	if e = sockets[0].Write(ctx, websocket.MessageBinary, raw); e != nil {
		t.Fatal(e)
	}
	if _, _, e = sockets[0].Read(ctx); e == nil {
		t.Fatal("control switched declared class to consume reserved lookup authority")
	}
}
func TestDataRateCannotSpendReservedControlBudgetsOrReplayAcrossClasses(t *testing.T) {
	b := admissionBudget{}
	expires := time.Now().Add(10 * time.Second)
	rejected := false
	for i := 0; i < 80; i++ {
		if !b.admit("same-peer", fmt.Sprintf("data-%d", i), expires, ClassMessage) {
			rejected = true
			break
		}
	}
	if !rejected {
		t.Fatal("data peer rate unbounded")
	}
	if !b.admit("same-peer", "read-outcome", expires, ClassLookup) || !b.admit("same-peer", "refresh-directory", expires, ClassDirectory) {
		t.Fatal("data rate spent reserved control budgets")
	}
	if b.admit("same-peer", "read-outcome", expires, ClassMessage) {
		t.Fatal("route replay changed class to bypass recent ID evidence")
	}
}
