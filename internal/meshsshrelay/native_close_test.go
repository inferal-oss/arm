package meshsshrelay

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"testing"
)

type closeFaultConn struct {
	net.Conn
	mu     sync.Mutex
	closes int
	fault  error
}

type applicationCloseConn struct {
	net.Conn
	fault error
}

func (c *applicationCloseConn) Close() error {
	_ = c.Conn.Close()
	return c.fault
}

func (c *closeFaultConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes++
	_ = c.Conn.Close()
	if c.closes == 1 {
		return c.fault
	}
	return nil
}

func TestNativeOriginalCloseKeepsFirstRawFailure(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	fault := errors.New("injected first raw close failure")
	raw := &closeFaultConn{Conn: left, fault: fault}
	socket := &socketAbort{}
	socket.set(raw)
	_, cancel := context.WithCancel(t.Context())
	carrier := newBounded(right, cancel, func() error { socket.close(); return nil })
	carrier.native = socket
	if err := carrier.Close(); err != nil {
		t.Fatal(err)
	}
	if err := carrier.Close(); err != nil {
		t.Fatal(err)
	}
	if err := carrier.NativeCloseProof(); !errors.Is(err, fault) {
		t.Fatalf("first raw failure lost: %v", err)
	}
	raw.mu.Lock()
	count := raw.closes
	raw.mu.Unlock()
	if count != 1 {
		t.Fatalf("raw socket closed %d times", count)
	}
}

func TestNativeOriginalCloseProofUnavailableIsUncertain(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	_, cancel := context.WithCancel(t.Context())
	carrier := newBounded(left, cancel, nil)
	applicationErr, nativeErr := closeCarrierOriginal(carrier)
	if applicationErr != nil {
		t.Fatal(applicationErr)
	}
	var uncertain *NativeCloseUncertain
	if !errors.As(nativeErr, &uncertain) {
		t.Fatalf("missing native uncertainty: %v", nativeErr)
	}
}

func TestTLSCloseReportsOriginalRawFailureSeparately(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	fault := errors.New("injected original TCP close failure")
	raw := &closeFaultConn{Conn: left, fault: fault}
	socket := &socketAbort{}
	socket.set(raw)
	_, cancel := context.WithCancel(t.Context())
	carrier := newBounded(right, cancel, func() error { socket.close(); return nil })
	carrier.native = socket
	inner := tls.Client(carrier, &tls.Config{})
	_, nativeErr := CloseOriginalTLS(inner)
	var uncertain *NativeCloseUncertain
	if !errors.As(nativeErr, &uncertain) || !errors.Is(nativeErr, fault) {
		t.Fatalf("TLS close lost first raw socket failure: %v", nativeErr)
	}
	_, secondNativeErr := CloseOriginalTLS(inner)
	if !errors.Is(secondNativeErr, fault) {
		t.Fatalf("second TLS close masked original failure: %v", secondNativeErr)
	}
}

func TestImplicitTLSCloseIsFirstRawCloseProof(t *testing.T) {
	for _, injected := range []bool{false, true} {
		t.Run(map[bool]string{false: "known_close", true: "native_failure"}[injected], func(t *testing.T) {
			left, right := net.Pipe()
			defer right.Close()
			var fault error
			if injected {
				fault = errors.New("implicit HTTP/TLS first raw close failed")
			}
			raw := &closeFaultConn{Conn: left, fault: fault}
			socket := &socketAbort{}
			owned := socket.set(raw)
			inner := tls.Client(owned, &tls.Config{})
			_ = inner.Close() // TLS/HTTP owns the first Close, outside socketAbort.
			if err := socket.closeOriginal(); !errors.Is(err, fault) || (err == nil) != (fault == nil) {
				t.Fatalf("implicit first close proof changed: %v", err)
			}
			raw.mu.Lock()
			count := raw.closes
			raw.mu.Unlock()
			if count != 1 {
				t.Fatalf("second native close hid first result: %d", count)
			}
		})
	}
}

func TestTLSApplicationCloseErrorWithKnownRawClose(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	socket := &socketAbort{}
	socket.set(left)
	_, cancel := context.WithCancel(t.Context())
	fault := errors.New("injected TLS application close error")
	carrier := newBounded(&applicationCloseConn{Conn: right, fault: fault}, cancel, func() error { socket.close(); return nil })
	carrier.native = socket
	inner := tls.Client(carrier, &tls.Config{})
	applicationErr, nativeErr := CloseOriginalTLS(inner)
	if !errors.Is(applicationErr, fault) || nativeErr != nil {
		t.Fatalf("known raw close was classified as native debt: application=%v native=%v", applicationErr, nativeErr)
	}
}
