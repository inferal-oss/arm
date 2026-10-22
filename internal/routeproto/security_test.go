package routeproto

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestInnerTLSRejectsWrongPinSANExpiryProtocolAndClient(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"untrusted-target", "wrong-target-pin", "wrong-SAN", "expired-target", "TLS12", "wrong-ALPN", "missing-ALPN", "missing-client-cert", "untrusted-client"} {
		t.Run(name, func(t *testing.T) {
			c := policy(f.b, f.c, false, innerProtocol)
			switch name {
			case "untrusted-target":
				c.RootCAs = x509.NewCertPool()
			case "wrong-target-pin":
				other := newIdentity(t, f.c.host, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
				c = policy(f.b, other, false, innerProtocol)
				c.RootCAs.AddCert(f.c.leaf)
			case "wrong-SAN":
				c.ServerName = san(f.a.host)
			case "expired-target":
				c.Time = func() time.Time { return time.Now().Add(2 * time.Hour) }
			case "TLS12":
				c.MinVersion = tls.VersionTLS12
				c.MaxVersion = tls.VersionTLS12
			case "wrong-ALPN":
				c.NextProtos = []string{"unrelated/1"}
			case "missing-ALPN":
				c.NextProtos = nil
			case "missing-client-cert":
				c.Certificates = nil
			case "untrusted-client":
				other := newIdentity(t, "dddddddddddddddddddddddddddddddd", time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
				c.Certificates = []tls.Certificate{other.cert}
			}
			conn, e := f.connect(t, c)
			if e != nil {
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			if e = writeObject(conn, request{"workspace-probe", f.b.host, f.c.host, "message_probe", "must not accept"}); e != nil {
				return
			}
			var result response
			if e = readObject(conn, &result); e == nil {
				t.Fatal("invalid TLS policy reached closed handler", result)
			}
		})
	}
}

func TestRelayRefusesAddressRPCClassAndInvalidPathWithoutDial(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"address-field", "TCP-target", "unix-target", "resource-class", "cycle", "wrong-workspace", "wrong-version"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(f.ctx, time.Second)
			defer cancel()
			ws, e := dialOuter(ctx, f.first.Listener.Addr().String(), policy(f.b, f.a, false, "http/1.1"))
			if e != nil {
				t.Fatal(e)
			}
			defer ws.CloseNow()
			q := map[string]any{"version": 1, "workspace": "workspace-probe", "target": f.c.host, "class": "message", "path": []string{f.c.host}}
			switch name {
			case "address-field":
				q["address"] = "127.0.0.1:22"
			case "TCP-target":
				q["target"] = "127.0.0.1:22"
			case "unix-target":
				q["target"] = "unix:///private/other.sock"
			case "resource-class":
				q["class"] = "run"
			case "cycle":
				q["path"] = []string{f.c.host, f.a.host, f.c.host}
			case "wrong-workspace":
				q["workspace"] = "another"
			case "wrong-version":
				q["version"] = 2
			}
			raw, _ := json.Marshal(q)
			if e = ws.Write(ctx, websocket.MessageBinary, raw); e != nil {
				t.Fatal(e)
			}
			if _, ack, e := ws.Read(ctx); e == nil {
				t.Fatal("unsafe route acknowledged", string(ack))
			}
			if f.dials.Load() != 0 {
				t.Fatal("unsafe open initiated next-hop dial", f.dials.Load())
			}
		})
	}
}

func TestEndToEndCancellationClosesRelayWithoutChangingAuthority(t *testing.T) {
	f := newFixture(t)
	conn, e := f.connect(t, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	f.cancel()
	if e = conn.SetReadDeadline(time.Now().Add(time.Second)); e != nil {
		return
	}
	var b [1]byte
	if n, e := conn.Read(b[:]); e == nil || n != 0 {
		t.Fatal("cancelled TLS stream remained live", n, e)
	}
	select {
	case result := <-f.observed:
		if result.Allowed {
			t.Fatal("cancellation fabricated successful operation", result)
		}
	case <-time.After(30 * time.Millisecond):
	}
}

func TestClosedFinalHandlerRejectsOversizedBodyAndExtraRPCFields(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"body-too-large", "native-rpc-field", "wrong-target", "wrong-workspace"} {
		t.Run(name, func(t *testing.T) {
			conn, e := f.connect(t, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			q := map[string]any{"workspace": "workspace-probe", "source": f.b.host, "target": f.c.host, "operation": "message_probe", "body": ""}
			switch name {
			case "body-too-large":
				q["body"] = strings.Repeat("a", (16<<10)+1)
			case "native-rpc-field":
				q["params"] = map[string]any{"argv": []string{"sh"}}
			case "wrong-target":
				q["target"] = f.a.host
			case "wrong-workspace":
				q["workspace"] = "other"
			}
			if e = writeObject(conn, q); e != nil {
				t.Fatal(e)
			}
			var result response
			if e = readObject(conn, &result); e != nil || result.Allowed || result.Error == "" {
				t.Fatal(result, e)
			}
		})
	}
}

func TestOuterPeerTLSAndOpenDeadlineFailClosed(t *testing.T) {
	f := newFixture(t)
	other := newIdentity(t, "dddddddddddddddddddddddddddddddd", time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	ctx, cancel := context.WithTimeout(f.ctx, time.Second)
	defer cancel()
	if ws, e := dialOuter(ctx, f.first.Listener.Addr().String(), policy(other, f.a, false, "http/1.1")); e == nil {
		defer ws.CloseNow()
		t.Fatal("unpaired outer peer reached relay")
	}
	for _, kind := range []string{"no-control", "oversized-control", "text-control"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(f.ctx, time.Second)
			defer cancel()
			ws, e := dialOuter(ctx, f.first.Listener.Addr().String(), policy(f.b, f.a, false, "http/1.1"))
			if e != nil {
				t.Fatal(e)
			}
			defer ws.CloseNow()
			switch kind {
			case "oversized-control":
				_ = ws.Write(ctx, websocket.MessageBinary, make([]byte, 2048))
			case "text-control":
				_ = ws.Write(ctx, websocket.MessageText, []byte("not a binary admission"))
			}
			if _, ack, e := ws.Read(ctx); e == nil {
				t.Fatal("unsafe admission acknowledged", string(ack))
			}
			if f.dials.Load() != 0 {
				t.Fatal("invalid/expired control opened next hop")
			}
		})
	}
}
