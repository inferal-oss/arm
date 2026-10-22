package mesh

import (
	"net"
	"testing"
	"time"
)

func TestPreparedListenerDoesNotListenBeforeActivation(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1"} {
		t.Run(ip, func(t *testing.T) {
			pending, err := PrepareListener(ip)
			if err != nil {
				t.Fatal(err)
			}
			defer pending.Close()
			if c, err := net.DialTimeout("tcp", pending.Address(), time.Second); err == nil {
				c.Close()
				t.Fatal("pending socket accepted a TCP connection")
			}
			if competing, err := net.Listen("tcp", pending.Address()); err == nil {
				competing.Close()
				t.Fatal("pending socket did not reserve exact endpoint")
			}
			listener, err := pending.Activate()
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			if listener.Addr().String() != pending.Address() {
				t.Fatal("activation changed endpoint", listener.Addr(), pending.Address())
			}
			if _, err := pending.Activate(); err == nil {
				t.Fatal("activated reservation twice")
			}
			c, err := net.DialTimeout("tcp", pending.Address(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			c.Close()
			if err := pending.Close(); err != nil {
				t.Fatal(err)
			}
			c, err = net.DialTimeout("tcp", listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal("closing transferred reservation closed active listener", err)
			}
			c.Close()
		})
	}
}

func TestPreparedListenerCloseReleasesReservation(t *testing.T) {
	pending, err := PrepareListener("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	address := pending.Address()
	if err := pending.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("closed preparation retained port", err)
	}
	listener.Close()
	if _, err := pending.Activate(); err == nil {
		t.Fatal("closed reservation activated")
	}
	for _, ip := range []string{"0.0.0.0", "::", "224.0.0.1", "hostname", "127.0.0.1:0"} {
		if p, err := PrepareListener(ip); err == nil {
			p.Close()
			t.Fatal("unsafe address accepted", ip)
		}
	}
}
