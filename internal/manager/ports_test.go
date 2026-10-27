package manager

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/ports"
)

func portRequest(key string) model.Request {
	r := request(key)
	r.Kind = "ports"
	r.Resources = model.Resources{}
	r.Ports = []model.PortClaim{{Name: "server", Network: "tcp4", Address: "127.0.0.1"}}
	return r
}
func TestPortAdmissionQueueAndAtomicBudgets(t *testing.T) {
	m := newManager(t)
	m.Start()
	external, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	r := portRequest("blocked")
	r.Kind = ""
	r.Resources.CPU = 1
	r.Ports[0].Port = external.Addr().(*net.TCPAddr).Port
	l, err := m.Acquire(r, nil)
	if err == nil || l.State != "terminal" || l.Outcome != "admission_failed" {
		t.Fatalf("external listener was bypassed: %+v %v", l, err)
	}
	s, _ := m.Status()
	if s.Reserved.CPU != 0 {
		t.Fatal("failed port claim retained CPU")
	}
	r.Key = "queued"
	r.Queue = true
	l, err = m.Acquire(r, nil)
	if err != nil || l.State != "queued" || l.Started != nil || len(l.Ports) != 0 {
		t.Fatalf("partial queued grant: %+v %v", l, err)
	}
	s, _ = m.Status()
	if s.Reserved.CPU != 0 {
		t.Fatal("queued ports retained CPU")
	}
	external.Close()
	eventually(t, func() bool { l, _ = m.Inspect(l.ID); return l.State == "active" })
	if l.Ports[0].Port != r.Ports[0].Port {
		t.Fatal("granted wrong port")
	}
	same, err := m.Acquire(r, nil)
	if err != nil || same.ID != l.ID {
		t.Fatalf("port idempotency: %+v %v", same, err)
	}
	r.Ports[0].Address = "0.0.0.0"
	if _, err = m.Acquire(r, nil); err == nil {
		t.Fatal("different socket reused key")
	}
	s, _ = m.Status()
	if s.Reserved.CPU != 1 || len(s.Resources) != 4 || s.Resources[3].Users[0].Reason != r.Reason || s.Resources[3].Port.State != "held" {
		t.Fatalf("missing who/why port budget: %+v", s)
	}
	l, err = m.End(l.ID, "released")
	if err != nil || l.State != "terminal" {
		t.Fatalf("release: %+v %v", l, err)
	}
}
func TestCooperativeOccupantSurvivesRevocationAndStaysCharged(t *testing.T) {
	m := newManager(t)
	r := portRequest("cooperative")
	r.Ports[0].Mode = "cooperative"
	l, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(l.Ports[0].Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	l, err = m.End(l.ID, "revoked")
	if err != nil || l.State != "needs_attention" || l.Ports[0].State != "unresolved" {
		t.Fatalf("occupied port was declared reclaimed: %+v %v", l, err)
	}
	if _, err = m.Reconcile(l.ID); err == nil {
		t.Fatal("reconciled occupied port")
	}
	conn, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal("unrelated listener was terminated:", err)
	}
	conn.Close()
	listener.Close()
	l, err = m.Reconcile(l.ID)
	if err != nil || l.State != "terminal" || l.Outcome != "revoked" {
		t.Fatalf("reconcile: %+v %v", l, err)
	}
}
func TestPortRestartNeverRestoresHeldState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	m, err := Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.Acquire(portRequest("restart"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(l.Ports[0].Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	m, err = Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	l, _ = m.Inspect(l.ID)
	if l.State != "needs_attention" || l.Ports[0].State != "unknown" {
		t.Fatalf("persisted socket pretended to survive: %+v", l)
	}
	if _, err = m.Reconcile(l.ID); err == nil {
		t.Fatal("restart freed occupied socket")
	}
	listener.Close()
	l, err = m.Reconcile(l.ID)
	if err != nil || l.State != "terminal" {
		t.Fatalf("restart reconcile: %+v %v", l, err)
	}
}

func TestPortExpiryClosesHeldDescriptors(t *testing.T) {
	m := newManager(t)
	m.Start()
	r := portRequest("expire-held")
	r.TTL = 50 * time.Millisecond
	l, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if l.Started == nil || l.Expires == nil || l.Expires.Sub(*l.Started) != r.TTL {
		t.Fatalf("port TTL did not start at grant: %+v", l)
	}
	eventually(t, func() bool { l, _ = m.Inspect(l.ID); return l.State == "terminal" })
	if l.Outcome != "expired" || l.Ports[0].State != "observed_available" {
		t.Fatalf("expired held port not reconciled: %+v", l)
	}
	if err := ports.Probe(t.Context(), l.Ports[0]); err != nil {
		t.Fatal("expired port still held", err)
	}
	if _, err := m.Renew(l.ID, time.Minute); err == nil {
		t.Fatal("revived expired port")
	}
}
func TestAttachedPortLeaseRetainsSocketAndRevokesOnlyItsConsumer(t *testing.T) {
	m := newManager(t)
	m.Start()
	parent, err := m.Acquire(portRequest("parent"), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := request("attached")
	r.TTL = 10 * time.Second
	r.PortLease = parent.ID
	cwd := t.TempDir()
	run := RunRequest{Request: r, CWD: cwd, Command: []string{"/bin/sh", "-c", `printf '%s' "$ARM_PORTS" > activation.json; exec sleep 30`}}
	l, err := m.Acquire(r, &run)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { _, e := os.Stat(filepath.Join(cwd, "activation.json")); return e == nil })
	data, err := os.ReadFile(filepath.Join(cwd, "activation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var activated []model.ActivatedPort
	if json.Unmarshal(data, &activated) != nil || len(activated) != 1 || activated[0].FD == nil || *activated[0].FD != 4 || activated[0].Port != parent.Ports[0].Port {
		t.Fatalf("activation contract: %s", data)
	}
	status, _ := m.Status()
	consumerShown := false
	for _, row := range status.Resources {
		if row.Kind == "port" {
			if row.Reserved != "1" {
				t.Fatal("consumer double-charged port")
			}
			for _, u := range row.Users {
				if u.Lease == l.ID && u.Relationship == "consumer" && u.Quantity == "0" {
					consumerShown = true
				}
			}
		}
	}
	if !consumerShown {
		t.Fatal("resource view omitted the actual consumer and its purpose")
	}
	r.Key = "second"
	run.Request = r
	if _, err = m.Acquire(r, &run); err == nil {
		t.Fatal("shared a port lease without an explicit sharing contract")
	}
	if _, err = m.Renew(l.ID, 2*time.Minute); err == nil {
		t.Fatal("child renewal outlasted parent")
	}
	if _, err = m.End(parent.ID, "released"); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { p, _ := m.Inspect(parent.ID); return p.State == "terminal" })
	l, _ = m.Inspect(l.ID)
	if l.State != "terminal" || l.Outcome != "revoked" {
		t.Fatalf("attached consumer not revoked: %+v", l)
	}
}
func TestRunReceivesTCPAndUDPDescriptors(t *testing.T) {
	m := newManager(t)
	m.Start()
	r := request("activation")
	r.Ports = portRequest("").Ports
	r.Ports = append(r.Ports, model.PortClaim{Name: "datagram", Network: "udp4", Address: "127.0.0.1"})
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := RunRequest{Request: r, CWD: t.TempDir(), Command: []string{exe, "-test.run=^TestPortCommandFixture$"}}
	l, err := m.Acquire(r, &run)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		_, e := os.Stat(filepath.Join(run.CWD, "ready"))
		if e != nil {
			current, _ := m.Inspect(l.ID)
			if current.State == "terminal" {
				output, _ := m.Output(l.ID, 0)
				t.Fatalf("fixture exited before ready: %+v %s", current, output)
			}
		}
		return e == nil
	})
	for _, b := range l.Ports {
		if err := ports.Probe(t.Context(), b); err == nil {
			t.Fatal("lost reservation during launch")
		}
	}
	tcp, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(l.Ports[0].Port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tcp.SetDeadline(time.Now().Add(2 * time.Second))
	payload := make([]byte, 3)
	if _, err = io.ReadFull(tcp, payload); err != nil || string(payload) != "arm" {
		tcp.Close()
		t.Fatalf("inherited listener: %q %v", payload, err)
	}
	tcp.Close()
	udp, err := net.DialTimeout("udp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(l.Ports[1].Port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	udp.SetDeadline(time.Now().Add(2 * time.Second))
	udp.Write([]byte("arm"))
	if _, err = io.ReadFull(udp, payload); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { l, _ = m.Inspect(l.ID); return l.State == "terminal" })
	if l.ExitCode == nil || *l.ExitCode != 0 {
		output, _ := m.Output(l.ID, 0)
		t.Fatalf("fixture failed: %+v %s", l, output)
	}
}
func TestPortCommandFixture(t *testing.T) {
	raw := os.Getenv("ARM_PORTS")
	if raw == "" {
		t.Skip("supervised child fixture")
	}
	var p []model.ActivatedPort
	if json.Unmarshal([]byte(raw), &p) != nil || len(p) != 2 || p[0].FD == nil || p[1].FD == nil {
		t.Fatal("missing activation metadata")
	}
	tcpFile := os.NewFile(uintptr(*p[0].FD), "tcp")
	tcp, err := net.FileListener(tcpFile)
	tcpFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	udpFile := os.NewFile(uintptr(*p[1].FD), "udp")
	udp, err := net.FilePacketConn(udpFile)
	udpFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	if err = os.WriteFile("ready", []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	conn, err := tcp.Accept()
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	conn.Write([]byte("arm"))
	io.Copy(io.Discard, conn)
	conn.Close()
	udp.SetDeadline(time.Now().Add(3 * time.Second))
	data := make([]byte, 32)
	n, addr, err := udp.ReadFrom(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = udp.WriteTo(data[:n], addr); err != nil {
		t.Fatal(err)
	}
}

func TestEscapedInheritedSocketStaysCharged(t *testing.T) {
	m := newManager(t)
	m.Start()
	r := request("escaped")
	r.Ports = portRequest("").Ports
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := RunRequest{Request: r, CWD: t.TempDir(), Command: []string{exe, "-test.run=^TestPortEscapeFixture$"}}
	l, err := m.Acquire(r, &run)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { l, _ = m.Inspect(l.ID); return l.State == "needs_attention" || l.State == "terminal" })
	if l.State != "needs_attention" || l.Ports[0].State != "unresolved" {
		output, _ := m.Output(l.ID, 0)
		t.Fatalf("escaped socket was declared free: %+v %s", l, output)
	}
	if _, err = m.Reconcile(l.ID); err == nil {
		t.Fatal("reconciled live escaped socket")
	}
	// The fixture exits by itself; ARM never receives authority to signal it.
	eventually(t, func() bool { l, err = m.Reconcile(l.ID); return err == nil && l.State == "terminal" })
}
func TestPortEscapeFixture(t *testing.T) {
	var p []model.ActivatedPort
	if json.Unmarshal([]byte(os.Getenv("ARM_PORTS")), &p) != nil || len(p) != 1 {
		t.Skip("escape parent fixture")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestEscapedPortSleeper$")
	cmd.Env = append(os.Environ(), "ARM_ESCAPE_FIXTURE=1")
	file := os.NewFile(uintptr(*p[0].FD), "inherited")
	defer file.Close()
	cmd.ExtraFiles = []*os.File{file}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Process.Release(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = os.Stat("escape-ready"); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("escaped fixture did not start")
}
func TestEscapedPortSleeper(t *testing.T) {
	if os.Getenv("ARM_ESCAPE_FIXTURE") != "1" {
		t.Skip("escaped child fixture")
	}
	file := os.NewFile(3, "inherited")
	defer file.Close()
	if _, err := file.Stat(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("escape-ready", []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
}
