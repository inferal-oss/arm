package mesh

import (
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// PreparedListener owns a bound TCP socket which does not accept connections
// until its exact address is approved and Activate is called. No close/rebind
// window separates the concrete preview endpoint from the actual listener.
type PreparedListener struct {
	mu      sync.Mutex
	file    *os.File
	address string
}

func PrepareListener(ip string) (*PreparedListener, error) {
	address := net.ParseIP(ip)
	if address == nil || address.IsUnspecified() || address.IsMulticast() {
		return nil, fmt.Errorf("mesh preparation requires one non-wildcard literal interface IP")
	}
	family := unix.AF_INET6
	if address.To4() != nil {
		family = unix.AF_INET
	}
	syscall.ForkLock.RLock()
	fd, err := unix.Socket(family, unix.SOCK_STREAM, unix.IPPROTO_TCP)
	if err != nil {
		syscall.ForkLock.RUnlock()
		return nil, err
	}
	unix.CloseOnExec(fd)
	syscall.ForkLock.RUnlock()
	file := os.NewFile(uintptr(fd), "arm-mesh-pending")
	fail := func(err error) (*PreparedListener, error) { file.Close(); return nil, err }
	var socketAddress unix.Sockaddr
	if family == unix.AF_INET {
		var bytes [4]byte
		copy(bytes[:], address.To4())
		socketAddress = &unix.SockaddrInet4{Addr: bytes}
	} else {
		if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, 1); err != nil {
			return fail(err)
		}
		var bytes [16]byte
		copy(bytes[:], address.To16())
		socketAddress = &unix.SockaddrInet6{Addr: bytes}
	}
	if err := unix.Bind(fd, socketAddress); err != nil {
		return fail(err)
	}
	bound, err := unix.Getsockname(fd)
	if err != nil {
		return fail(err)
	}
	port := 0
	switch bound := bound.(type) {
	case *unix.SockaddrInet4:
		port = bound.Port
	case *unix.SockaddrInet6:
		port = bound.Port
	}
	if port <= 0 {
		return fail(fmt.Errorf("OS did not return a concrete mesh port"))
	}
	return &PreparedListener{file: file, address: net.JoinHostPort(address.String(), fmt.Sprint(port))}, nil
}

func (p *PreparedListener) Address() string { return p.address }

func (p *PreparedListener) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.file == nil {
		return nil
	}
	err := p.file.Close()
	p.file = nil
	return err
}

func (p *PreparedListener) Activate() (net.Listener, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.file == nil {
		return nil, fmt.Errorf("prepared mesh socket is closed or already activated")
	}
	if err := unix.Listen(int(p.file.Fd()), 128); err != nil {
		return nil, err
	}
	listener, err := net.FileListener(p.file)
	if err != nil {
		p.file.Close()
		p.file = nil
		return nil, err
	}
	p.file.Close()
	p.file = nil
	return listener, nil
}
