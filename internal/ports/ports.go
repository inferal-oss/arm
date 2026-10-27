// Package ports owns bound sockets. Holding and inheriting the same socket is
// distinct from probing a port then closing it for a cooperative workload.
package ports

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/inferal-oss/arm/internal/model"
	"golang.org/x/sys/unix"
)

// Socket owns one file descriptor, which can be passed through exec.Cmd's
// ExtraFiles without releasing the bind. A cooperative claim has no descriptor.
// Callers serialize access to a Socket and remain responsible for the ledger.
type Socket struct {
	Binding model.PortBinding
	File    *os.File
}

var namePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)

// Normalize validates claims and returns a canonical copy for admission and
// idempotency checks. namespace must be the current host's observed identity.
func Normalize(claims []model.PortClaim, namespace string, isRun bool) ([]model.PortClaim, error) {
	if len(claims) == 0 {
		return nil, nil
	}
	if namespace == "" {
		return nil, model.Err("namespace_unavailable", "network namespace identity is unavailable")
	}
	if len(claims) > 16 {
		return nil, model.Err("invalid_request", "at most 16 port claims are supported per request")
	}
	result := append([]model.PortClaim{}, claims...)
	names := map[string]bool{}
	for i := range result {
		c := &result[i]
		if c.Name == "" {
			c.Name = fmt.Sprintf("port%d", i+1)
		}
		if !namePattern.MatchString(c.Name) || names[c.Name] {
			return nil, model.Err("invalid_request", "port names must be unique, start with a letter, and contain up to 32 letters, digits, underscores or hyphens")
		}
		names[c.Name] = true
		if c.Network != "tcp4" && c.Network != "tcp6" && c.Network != "udp4" && c.Network != "udp6" {
			return nil, model.Err("invalid_request", "port network must be tcp4, tcp6, udp4 or udp6")
		}
		addr, err := netip.ParseAddr(c.Address)
		if err != nil || addr.Is4In6() || addr.Zone() != "" || (strings.HasSuffix(c.Network, "4") != addr.Is4()) {
			return nil, model.Err("invalid_request", "bind address must be an unscoped literal IP matching the network family")
		}
		c.Address = addr.String()
		if c.DualStack && (!addr.Is6() || !addr.IsUnspecified()) {
			return nil, model.Err("invalid_request", "dual-stack requires the IPv6 wildcard address ::")
		}
		if c.Port < 0 || c.Port > 65535 || c.RangeEnd < 0 || c.RangeEnd > 65535 || (c.RangeEnd != 0 && (c.Port == 0 || c.RangeEnd < c.Port || c.RangeEnd-c.Port > 255)) {
			return nil, model.Err("invalid_request", "port must be 0..65535; a selection range must contain at most 256 nonzero ports")
		}
		if c.RangeEnd == c.Port {
			c.RangeEnd = 0
		}
		if c.Namespace != "" && c.Namespace != "host" && c.Namespace != namespace {
			return nil, model.Err("namespace_unavailable", "only the daemon's current network namespace is supported")
		}
		c.Namespace = namespace
		if c.Mode == "" {
			c.Mode = "hold"
			if isRun {
				c.Mode = "activation"
			}
		}
		if c.Mode != "hold" && c.Mode != "activation" && c.Mode != "cooperative" {
			return nil, model.Err("invalid_request", "port mode must be hold, activation or cooperative")
		}
		if isRun && c.Mode == "hold" || !isRun && c.Mode == "activation" {
			return nil, model.Err("invalid_request", "hold is for a standalone reservation; activation requires a supervised run")
		}
	}
	return result, nil
}

// Overlap compares selected port claims, including wildcard and dual-stack
// overlap. Call Normalize before using claims as a reservation ledger.
func Overlap(a, b model.PortClaim) bool {
	if a.Namespace != b.Namespace || a.Port == 0 || b.Port == 0 || a.Port != b.Port || len(a.Network) != 4 || len(b.Network) != 4 || a.Network[:3] != b.Network[:3] {
		return false
	}
	x, e1 := netip.ParseAddr(a.Address)
	y, e2 := netip.ParseAddr(b.Address)
	if e1 != nil || e2 != nil {
		return false
	}
	if x.Is4() != y.Is4() {
		return (a.DualStack && x.IsUnspecified()) || (b.DualStack && y.IsUnspecified())
	}
	return x == y || x.IsUnspecified() || y.IsUnspecified()
}

// Open attempts the whole bundle against both the caller's outstanding ledger
// and real kernel binds. Failure closes every socket acquired by this attempt.
// It does not change the ledger or guarantee ownership of cooperative sockets.
func Open(ctx context.Context, claims []model.PortClaim, blocked []model.PortBinding) ([]*Socket, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(claims) == 0 {
		return nil, nil
	}
	namespace, err := Namespace()
	if err != nil {
		return nil, model.Err("namespace_unavailable", err.Error())
	}
	isRun := false
	for _, claim := range claims {
		isRun = isRun || claim.Mode == "activation"
	}
	claims, err = Normalize(claims, namespace, isRun)
	if err != nil {
		return nil, err
	}
	// Keep the caller's ledger unchanged even if append would reuse its backing
	// array. Only the returned sockets become this admission's allocations.
	blocked = append([]model.PortBinding(nil), blocked...)
	sockets := []*Socket{}
	for _, claim := range claims {
		end := claim.RangeEnd
		if end == 0 {
			end = claim.Port
		}
		var selected *Socket
		var last error
		attempts := 0
		for port := claim.Port; port <= end; port++ {
			if err := ctx.Err(); err != nil {
				Close(sockets)
				return nil, err
			}
			candidate := claim
			candidate.Port = port
			candidate.RangeEnd = 0
			conflict := false
			for _, bound := range blocked {
				if Overlap(candidate, bound.PortClaim) {
					conflict = true
					break
				}
			}
			if conflict {
				last = model.Err("port_conflict", "port overlaps an outstanding ARM allocation")
				continue
			}
			s, err := bind(ctx, candidate)
			if err != nil {
				if ctx.Err() != nil {
					Close(sockets)
					return nil, ctx.Err()
				}
				last = err
				if !errors.Is(err, syscall.EADDRINUSE) {
					Close(sockets)
					return nil, model.Err("port_unavailable", err.Error())
				}
				continue
			}
			for _, bound := range blocked {
				if Overlap(s.Binding.PortClaim, bound.PortClaim) {
					conflict = true
					break
				}
			}
			if conflict {
				err = errors.Join(err, s.File.Close())
				last = model.Err("port_conflict", "kernel-selected port overlaps a cooperative reservation")
				if claim.Port == 0 && attempts < 15 {
					attempts++
					port--
					continue
				}
				continue
			}
			selected = s
			break
		}
		if selected == nil {
			Close(sockets)
			return nil, model.Err("port_conflict", fmt.Sprintf("no port available for %s: %v", claim.Name, last))
		}
		if claim.Mode == "cooperative" {
			selected.File.Close()
			selected.File = nil
			selected.Binding.State = "cooperative"
			selected.Binding.SocketID = ""
			selected.Binding.Detail = "Ledger reservation only; the probe socket was closed, so the workload must handle bind failure"
		}
		sockets = append(sockets, selected)
		blocked = append(blocked, selected.Binding)
	}
	return sockets, nil
}

func bind(ctx context.Context, c model.PortClaim) (*Socket, error) {
	lc := net.ListenConfig{Control: func(network, address string, raw syscall.RawConn) error {
		var result error
		err := raw.Control(func(fd uintptr) {
			result = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 0)
			if result == nil && strings.HasSuffix(c.Network, "6") {
				v6only := 1
				if c.DualStack {
					v6only = 0
				}
				result = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, v6only)
			}
		})
		if err != nil {
			return err
		}
		return result
	}}
	address := net.JoinHostPort(c.Address, strconv.Itoa(c.Port))
	var file *os.File
	var actual net.Addr
	if strings.HasPrefix(c.Network, "tcp") {
		listener, err := lc.Listen(ctx, c.Network, address)
		if err != nil {
			return nil, err
		}
		actual = listener.Addr()
		file, err = listener.(*net.TCPListener).File()
		listener.Close()
		if err != nil {
			return nil, err
		}
	} else {
		packet, err := lc.ListenPacket(ctx, c.Network, address)
		if err != nil {
			return nil, err
		}
		actual = packet.LocalAddr()
		file, err = packet.(*net.UDPConn).File()
		packet.Close()
		if err != nil {
			return nil, err
		}
	}
	_, p, _ := net.SplitHostPort(actual.String())
	c.Port, _ = strconv.Atoi(p)
	var st unix.Stat_t
	socketID := ""
	if unix.Fstat(int(file.Fd()), &st) == nil && st.Ino != 0 {
		socketID = strconv.FormatUint(st.Ino, 10)
	}
	return &Socket{Binding: model.PortBinding{PortClaim: c, State: "held", SocketID: socketID}, File: file}, nil
}

func Close(sockets []*Socket) { _ = CloseChecked(sockets) }

// CloseChecked retains original owned-FD close errors for authority teardown.
func CloseChecked(sockets []*Socket) error {
	var err error
	for _, s := range sockets {
		if s.File != nil {
			err = errors.Join(err, s.File.Close())
			s.File = nil
		}
	}
	return err
}

// Probe only observes whether the exact allocation can be bound now. It neither
// establishes ownership of an occupant nor guarantees future availability.
func Probe(ctx context.Context, b model.PortBinding) error {
	if b.Port == 0 || b.RangeEnd != 0 {
		return model.Err("invalid_request", "a probe requires one selected nonzero port")
	}
	c := b.PortClaim
	c.Mode = "hold"
	s, err := Open(ctx, []model.PortClaim{c}, nil)
	if err != nil {
		return err
	}
	Close(s)
	return nil
}
