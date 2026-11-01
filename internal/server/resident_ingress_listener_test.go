package server

import (
	"net"
	"testing"
)

type refusedControlListener struct{ calls int }

func (l *refusedControlListener) Accept() (net.Conn, error) {
	l.calls++
	return nil, errResidentServerOriginal
}
func (l *refusedControlListener) Close() error   { l.calls++; return errResidentServerOriginal }
func (l *refusedControlListener) Addr() net.Addr { l.calls++; return nil }

func TestResidentIngressListenerUnissuedRefusesBeforeNativeMethods(t *testing.T) {
	underlying := &refusedControlListener{}
	p := &residentIngressPending{listener: underlying}
	p.self = p
	l := &residentIngressListener{owner: p, original: underlying}
	if conn, err := l.Accept(); conn != nil || err != errResidentServerOriginal {
		t.Fatalf("Accept = %v, %v", conn, err)
	}
	if err := l.Close(); err != errResidentServerOriginal {
		t.Fatalf("Close = %v", err)
	}
	if addr := l.Addr(); addr != nil {
		t.Fatalf("Addr = %v", addr)
	}
	if underlying.calls != 0 || l.closeAttempted || l.closeReturned {
		t.Fatal("unissued listener reached a supplied method")
	}
	for _, slot := range p.slots {
		if slot != nil {
			t.Fatal("unissued listener allocated an accept slot")
		}
	}
}
