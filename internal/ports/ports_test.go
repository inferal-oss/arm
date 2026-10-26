package ports

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func claim(network, address string, port int) model.PortClaim {
	return model.PortClaim{Network: network, Address: address, Port: port}
}

func open(t *testing.T, claims ...model.PortClaim) []*Socket {
	t.Helper()
	s, err := Open(t.Context(), claims, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Close(s) })
	return s
}

func errorCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *model.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestNormalizeRejectsUnsupportedClaims(t *testing.T) {
	for name, c := range map[string]model.PortClaim{
		"hostname":            claim("tcp4", "localhost", 0),
		"mapped":              claim("tcp6", "::ffff:127.0.0.1", 0),
		"family":              claim("udp4", "::1", 0),
		"zone":                claim("tcp6", "fe80::1%lo0", 0),
		"protocol":            claim("tcp", "127.0.0.1", 0),
		"negative":            claim("tcp4", "127.0.0.1", -1),
		"too large":           claim("tcp4", "127.0.0.1", 65536),
		"remote namespace":    {Network: "tcp4", Address: "127.0.0.1", Namespace: "other"},
		"large range":         {Network: "tcp4", Address: "127.0.0.1", Port: 1000, RangeEnd: 1256},
		"zero range":          {Network: "tcp4", Address: "127.0.0.1", RangeEnd: 100},
		"reversed range":      {Network: "tcp4", Address: "127.0.0.1", Port: 100, RangeEnd: 99},
		"dual-stack specific": {Network: "tcp6", Address: "::1", DualStack: true},
		"invalid mode":        {Network: "tcp4", Address: "127.0.0.1", Mode: "reserve-and-rebind"},
		"invalid name":        {Name: "../sock", Network: "tcp4", Address: "127.0.0.1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Normalize([]model.PortClaim{c}, "current", false); err == nil {
				t.Fatal("accepted unsupported claim")
			}
		})
	}
	c := claim("tcp6", "0:0:0:0:0:0:0:1", 8080)
	c.Namespace, c.RangeEnd = "host", 8080
	r, err := Normalize([]model.PortClaim{c}, "current", true)
	if err != nil || r[0].Name != "port1" || r[0].Address != "::1" || r[0].Mode != "activation" || r[0].Namespace != "current" || r[0].RangeEnd != 0 || c.Address == r[0].Address {
		t.Fatalf("normalization or input isolation: %+v %v", r, err)
	}
	for _, isRun := range []bool{false, true} {
		bad := claim("tcp4", "127.0.0.1", 0)
		bad.Mode = "activation"
		if isRun {
			bad.Mode = "hold"
		}
		if _, err := Normalize([]model.PortClaim{bad}, "current", isRun); err == nil {
			t.Fatal("accepted mode for wrong lifecycle")
		}
	}
	if _, err := Normalize([]model.PortClaim{c}, "", false); err == nil {
		t.Fatal("invented namespace")
	}
	if _, err := Normalize([]model.PortClaim{c, c}, "current", false); err != nil {
		t.Fatal(err)
	} // generated names differ
	c.Name = "same"
	if _, err := Normalize([]model.PortClaim{c, c}, "current", false); err == nil {
		t.Fatal("accepted duplicate names")
	}
	if _, err := Normalize(make([]model.PortClaim, 17), "current", false); err == nil {
		t.Fatal("accepted unbounded claims")
	}
}

func TestNamespaceAndLedgerOverlap(t *testing.T) {
	ns, err := Namespace()
	if err != nil || ns == "" {
		t.Fatalf("namespace: %q %v", ns, err)
	}
	a := claim("tcp4", "0.0.0.0", 18080)
	a.Namespace = ns
	b := a
	b.Address = "127.0.0.1"
	if !Overlap(a, b) {
		t.Fatal("wildcard failed to overlap specific bind")
	}
	b.Namespace = ns + "other"
	if Overlap(a, b) {
		t.Fatal("different namespace conflicts")
	}
	b = a
	b.Network = "udp4"
	if Overlap(a, b) {
		t.Fatal("TCP and UDP confused")
	}
	b = a
	b.Network, b.Address = "tcp6", "::"
	if Overlap(a, b) {
		t.Fatal("v6only conflicts with IPv4")
	}
	b.DualStack = true
	if !Overlap(a, b) {
		t.Fatal("dual-stack wildcard failed to overlap IPv4")
	}
	b = a
	b.Network = ""
	if Overlap(a, b) {
		t.Fatal("invalid persisted input overlaps")
	}
}

func TestRollbackAndCooperativeLedgerConflict(t *testing.T) {
	held := open(t, claim("tcp4", "127.0.0.1", 0))[0]
	probe := claim("udp4", "127.0.0.1", 0)
	probe.Mode = "cooperative"
	cooperative := open(t, probe)[0]
	if cooperative.File != nil || cooperative.Binding.State != "cooperative" || cooperative.Binding.Detail == "" {
		t.Fatalf("cooperative mode overstated guarantee: %+v", cooperative)
	}
	if err := Probe(t.Context(), cooperative.Binding); err != nil {
		t.Fatal("cooperative socket remained held:", err)
	}
	// The first actual bind must be rolled back when the second claim conflicts.
	first := cooperative.Binding.PortClaim
	first.Mode = "hold"
	first.Name = "first"
	second := held.Binding.PortClaim
	second.Name = "second"
	_, err := Open(t.Context(), []model.PortClaim{first, second}, nil)
	errorCode(t, err, "port_conflict")
	if err := Probe(t.Context(), cooperative.Binding); err != nil {
		t.Fatal("failed bundle leaked first socket:", err)
	}
	blocked := make([]model.PortBinding, 1, 8)
	blocked[0] = cooperative.Binding
	backing := blocked[:cap(blocked)]
	_, err = Open(t.Context(), []model.PortClaim{first}, blocked)
	errorCode(t, err, "port_conflict")
	// A successful request must not append its internal claims into caller memory.
	s, err := Open(t.Context(), []model.PortClaim{claim("tcp4", "127.0.0.1", 0)}, blocked)
	if err != nil {
		t.Fatal(err)
	}
	Close(s)
	if backing[1] != (model.PortBinding{}) {
		t.Fatal("mutated caller's ledger backing array")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Open(ctx, []model.PortClaim{first}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation:", err)
	}
	if err := Probe(t.Context(), model.PortBinding{PortClaim: probe}); err == nil {
		t.Fatal("probe selected an unrelated ephemeral port")
	}
}

func TestKernelFamiliesWildcardsAndRange(t *testing.T) {
	tcp := open(t, claim("tcp4", "0.0.0.0", 0))[0]
	port := tcp.Binding.Port
	_, err := Open(t.Context(), []model.PortClaim{claim("tcp4", "127.0.0.1", port)}, nil)
	errorCode(t, err, "port_conflict")
	udp := open(t, claim("udp4", "127.0.0.1", port))[0]
	if udp.Binding.Port != port {
		t.Fatal("protocol-specific port selection changed")
	}
	// The exact first candidate is occupied in the kernel, independently of ARM's ledger.
	if port < 65535 {
		c := claim("tcp4", "127.0.0.1", port)
		c.RangeEnd = min(65535, port+255)
		s := open(t, c)[0]
		if s.Binding.Port <= port || s.Binding.Port > c.RangeEnd || s.Binding.RangeEnd != 0 {
			t.Fatalf("unresolved range: %+v", s.Binding)
		}
	}
	// A kernel-selected IPv6-only port need not be free in IPv4, including
	// sockets held earlier in this fixture. Distinguish such external/family
	// collisions from a broken IPV6_V6ONLY setting before choosing a new port.
	paired := false
	for attempt := 0; attempt < 16; attempt++ {
		v6, err := Open(t.Context(), []model.PortClaim{claim("tcp6", "::", 0)}, nil)
		if err != nil {
			t.Skipf("IPv6 unavailable: %v", err)
		}
		c := claim("tcp4", "0.0.0.0", v6[0].Binding.Port)
		v4, err := Open(t.Context(), []model.PortClaim{c}, nil)
		Close(v6)
		if err == nil {
			Close(v4)
			paired = true
			break
		}
		// If removing our v6-only socket makes IPv4 available, its settings
		// caused the conflict; retrying would hide an implementation defect.
		probe, probeErr := Open(t.Context(), []model.PortClaim{c}, nil)
		if probeErr == nil {
			Close(probe)
			t.Fatalf("IPv6-only socket blocked an otherwise free IPv4 binding: %v", err)
		}
		errorCode(t, err, "port_conflict")
		errorCode(t, probeErr, "port_conflict")
	}
	if !paired {
		t.Fatal("could not select a port free in both independent families")
	}
	dual := claim("tcp6", "::", 0)
	dual.DualStack = true
	d := open(t, dual)[0]
	_, err = Open(t.Context(), []model.PortClaim{claim("tcp4", "127.0.0.1", d.Binding.Port)}, nil)
	errorCode(t, err, "port_conflict")
}

func TestInheritListeningSocketWithoutRebind(t *testing.T) {
	claims := []model.PortClaim{claim("tcp4", "127.0.0.1", 0), claim("udp4", "127.0.0.1", 0)}
	for i := range claims {
		claims[i].Mode = "activation"
	}
	s := open(t, claims...)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSocketChild$")
	cmd.Env = append(os.Environ(), "ARM_SOCKET_TEST_CHILD=1")
	cmd.ExtraFiles = []*os.File{s[0].File, s[1].File}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			cancel()
			_ = cmd.Wait()
		}
	}()
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("child initialization: %q %v %s", line, err, stderr.String())
	}
	for _, sock := range s {
		if err := Probe(ctx, sock.Binding); err == nil {
			t.Fatal("socket was free during handoff")
		}
	}
	dialer := net.Dialer{Timeout: time.Second}
	tcp, err := dialer.DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(s[0].Binding.Port)))
	if err != nil {
		t.Fatal(err)
	}
	tcp.SetDeadline(time.Now().Add(3 * time.Second))
	data := make([]byte, 3)
	if _, err := io.ReadFull(tcp, data); err != nil || string(data) != "tcp" {
		tcp.Close()
		t.Fatalf("inherited TCP: %q %v", data, err)
	}
	tcp.Close() // client initiates FIN, avoiding a server-side TIME_WAIT for the final probe
	udp, err := dialer.DialContext(ctx, "udp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(s[1].Binding.Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	udp.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := udp.Write([]byte("udp")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(udp, data); err != nil || string(data) != "udp" {
		t.Fatalf("inherited UDP: %q %v", data, err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatalf("child failed: %v %s", err, stderr.String())
	}
	for _, sock := range s {
		if err := Probe(ctx, sock.Binding); err == nil {
			t.Fatal("child exit released daemon's copy")
		}
	}
	Close(s)
	for _, sock := range s {
		if err := Probe(ctx, sock.Binding); err != nil {
			t.Fatal("socket stayed occupied after all copies closed:", err)
		}
	}
}

func TestSocketChild(t *testing.T) {
	if os.Getenv("ARM_SOCKET_TEST_CHILD") != "1" {
		t.Skip("subprocess fixture")
	}
	tcpFile, udpFile := os.NewFile(3, "tcp"), os.NewFile(4, "udp")
	listener, err := net.FileListener(tcpFile)
	tcpFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	packet, err := net.FilePacketConn(udpFile)
	udpFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	fmt.Println("ready")
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("tcp")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, conn); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	packet.SetDeadline(time.Now().Add(3 * time.Second))
	data := make([]byte, 16)
	n, addr, err := packet.ReadFrom(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := packet.WriteTo(data[:n], addr); err != nil {
		t.Fatal(err)
	}
}

func TestStaleNamespaceCannotProbeLocalSocket(t *testing.T) {
	s := open(t, claim("tcp4", "127.0.0.1", 0))[0]
	b := s.Binding
	b.Namespace += ":stale"
	errorCode(t, Probe(t.Context(), b), "namespace_unavailable")
	if !strings.Contains(b.Namespace, ":stale") {
		t.Fatal("input mutated")
	}
}

func TestConcurrentBindsHaveOneWinner(t *testing.T) {
	s := open(t, claim("tcp4", "127.0.0.1", 0))
	c := s[0].Binding.PortClaim
	Close(s)
	type result struct {
		sockets []*Socket
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 8)
	for range 8 {
		go func() {
			<-start
			s, err := Open(t.Context(), []model.PortClaim{c}, nil)
			results <- result{s, err}
		}()
	}
	close(start)
	winners := [][]*Socket{}
	defer func() {
		for _, s := range winners {
			Close(s)
		}
	}()
	for range 8 {
		r := <-results
		if r.err == nil {
			winners = append(winners, r.sockets)
		} else {
			errorCode(t, r.err, "port_conflict")
		}
	}
	if len(winners) != 1 {
		t.Fatalf("concurrent winners: %d", len(winners))
	}
}
