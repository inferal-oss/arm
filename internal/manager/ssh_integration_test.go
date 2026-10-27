package manager

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/database"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
	"golang.org/x/crypto/ssh"
)

type sshManagerFixture struct {
	m          *Manager
	client     *ssh.Client
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	q          model.SSHExecutionRequest
	channel    ssh.Channel
	lease      model.Lease
	serverDone <-chan struct{}
	peerReads  *heldSSHPeerReads
}

// A fixed test peer can withhold receipt of the next encrypted SSH packet,
// hence withhold its automatic channel-close response, without closing stdin.
type heldSSHPeerReads struct {
	net.Conn
	hold                  atomic.Bool
	held, release         chan struct{}
	heldOnce, releaseOnce sync.Once
}

func (c *heldSSHPeerReads) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && c.hold.Load() {
		c.heldOnce.Do(func() { close(c.held) })
		<-c.release
	}
	return n, err
}
func (c *heldSSHPeerReads) releaseRead() { c.releaseOnce.Do(func() { close(c.release) }) }
func (c *heldSSHPeerReads) Close() error { c.releaseRead(); return c.Conn.Close() }

func newSSHManagerFixture(t *testing.T, m *Manager, ttl time.Duration) *sshManagerFixture {
	return newSSHManagerFixtureWithAdmission(t, m, ttl, nil)
}

func newSSHManagerFixtureWithAdmission(t *testing.T, m *Manager, ttl time.Duration, admit func(context.Context, model.SSHExecutionRequest, sshservice.Execution, ssh.Channel) (model.Lease, sshservice.Process, error)) *sshManagerFixture {
	return newSSHManagerFixtureWithAdmissionReadHold(t, m, ttl, admit, false)
}

func newSSHManagerFixtureWithAdmissionReadHold(t *testing.T, m *Manager, ttl time.Duration, admit func(context.Context, model.SSHExecutionRequest, sshservice.Execution, ssh.Channel) (model.Lease, sshservice.Process, error), holdPeerReads bool) *sshManagerFixture {
	t.Helper()
	if m == nil {
		m = newManager(t)
	}
	if m.ownerUsername == "" {
		t.Fatal("native account must resolve for private SSH integration")
	}
	_, hostPrivate, _ := ed25519.GenerateKey(rand.Reader)
	host, _ := ssh.NewSignerFromKey(hostPrivate)
	_, userPrivate, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(userPrivate)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	f := &sshManagerFixture{m: m, ctx: ctx, cancel: cancel, q: model.SSHExecutionRequest{Source: strings.Repeat("a", 32), Target: m.host, Pair: strings.Repeat("b", 64), Revision: 1, Certificate: strings.Repeat("c", 64), PublicKeySHA256: sshDigest(signer.PublicKey().Marshal()), Key: local.ID(), Account: m.ownerUsername, Reason: "private execution integration", TTL: ttl, Resources: model.Resources{CPU: .5}, CWD: t.TempDir()}}
	serverDone := make(chan struct{})
	f.serverDone = serverDone
	go func() {
		defer close(serverDone)
		c, err := listener.Accept()
		if err != nil {
			return
		}
		service := sshservice.Server{HostKey: host, Revalidate: func(ctx context.Context) error { return ctx.Err() }, Handshake: time.Second, StartTimeout: time.Second, Execute: func(ctx context.Context, e sshservice.Execution, ch ssh.Channel) (sshservice.Process, error) {
			q := f.q
			q.Kind = e.Kind
			q.CommandSHA256 = sshDigest([]byte(e.Command))
			if e.Terminal != nil {
				raw, _ := json.Marshal(e.Terminal)
				q.TerminalSHA256 = sshDigest(raw)
			}
			var l model.Lease
			var p sshservice.Process
			var err error
			if admit != nil {
				l, p, err = admit(ctx, q, e, ch)
			} else {
				l, p, err = m.AcquireSSH(ctx, q, e, ch, model.Resources{CPU: 1}, time.Minute, time.Now().Add(time.Minute))
			}
			f.mu.Lock()
			f.q = q
			f.channel = ch
			f.lease = l
			f.mu.Unlock()
			return p, err
		}}
		_ = service.Serve(ctx, c, sshservice.Permit{Account: m.ownerUsername, PublicKey: signer.PublicKey()})
	}()
	config := &ssh.ClientConfig{User: m.ownerUsername, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(host.PublicKey()), Timeout: time.Second}
	var client *ssh.Client
	if holdPeerReads {
		var raw net.Conn
		raw, err = net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err == nil {
			f.peerReads = &heldSSHPeerReads{Conn: raw, held: make(chan struct{}), release: make(chan struct{})}
			err = raw.SetDeadline(time.Now().Add(time.Second))
			if err == nil {
				var connection ssh.Conn
				var channels <-chan ssh.NewChannel
				var requests <-chan *ssh.Request
				connection, channels, requests, err = ssh.NewClientConn(f.peerReads, listener.Addr().String(), config)
				if err == nil {
					err = raw.SetDeadline(time.Time{})
					if err == nil {
						client = ssh.NewClient(connection, channels, requests)
					} else {
						_ = connection.Close()
						_ = connection.Wait()
					}
				}
			}
			if err != nil {
				_ = f.peerReads.Close()
			}
		}
	} else {
		client, err = ssh.Dial("tcp", listener.Addr().String(), config)
	}
	if err != nil {
		cancel()
		listener.Close()
		t.Fatal(err)
	}
	f.client = client
	t.Cleanup(func() {
		cancel()
		_ = client.Close()
		_ = listener.Close()
		select {
		case <-serverDone:
		case <-time.After(3 * time.Second):
			t.Error("private SSH server did not stop")
		}
	})
	return f
}
func (f *sshManagerFixture) snapshot() (model.SSHExecutionRequest, ssh.Channel, model.Lease) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.q, f.channel, f.lease
}
func TestSSHManagerActualProcessRedactionAndNoReplay(t *testing.T) {
	f := newSSHManagerFixture(t, nil, time.Minute)
	s, err := f.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	secret := "private-command-sentinel-" + local.ID()
	command := "printf '%s' '" + secret + "';printf error-output >&2;exit 23"
	var stderr bytes.Buffer
	s.Stderr = &stderr
	out, err := s.Output(command)
	exit, ok := err.(*ssh.ExitError)
	if !ok || exit.ExitStatus() != 23 || string(out) != secret || stderr.String() != "error-output" {
		t.Fatal(string(out), stderr.String(), err)
	}
	q, ch, l := f.snapshot()
	if l.ID == "" || l.PID == 0 || l.Process == nil || l.Request.Peer != q.Source {
		t.Fatal("missing fresh supervised lease", l)
	}
	lookup, err := f.m.LookupSSH(q.Source, q.Key)
	if err != nil || !lookup.Found || lookup.Lease.State != "terminal" || lookup.Lease.Outcome != "failed" {
		t.Fatal(lookup, err)
	}
	repeated, p, err := f.m.AcquireSSH(context.Background(), q, sshservice.Execution{Kind: "exec", Command: command}, ch, model.Resources{CPU: 1}, time.Minute, time.Now().Add(time.Minute))
	if err != nil || p != nil || repeated.ID != l.ID {
		t.Fatal("key retry replayed execution", repeated, p, err)
	}
	f.m.mu.Lock()
	rows, err := f.m.db.Query(`SELECT run FROM leases UNION ALL SELECT data FROM events UNION ALL SELECT data FROM ssh_operations`)
	if err != nil {
		f.m.mu.Unlock()
		t.Fatal(err)
	}
	for rows.Next() {
		var data *string
		if err = rows.Scan(&data); err != nil {
			t.Fatal(err)
		}
		if data != nil && strings.Contains(*data, secret) {
			t.Error("raw command/output leaked to durable evidence")
		}
	}
	err = rows.Err()
	rows.Close()
	f.m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(f.m.home, "logs", l.ID+".log")); !os.IsNotExist(err) {
		t.Fatal("SSH unexpectedly created execution log", err)
	}
}
func TestSSHManagerActualPTYResizeAndInput(t *testing.T) {
	f := newSSHManagerFixture(t, nil, time.Minute)
	s, err := f.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.RequestPty("xterm", 24, 80, ssh.TerminalModes{ssh.ECHO: 0}); err != nil {
		t.Fatal(err)
	}
	input, err := s.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start("stty size;read line;stty size;printf '%s' \"$line\""); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(out)
	first, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(first) != "24 80" {
		t.Fatal(first, err)
	}
	if err = s.WindowChange(37, 101); err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(input, "private-input\n"); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(rest), "37 101") || !strings.Contains(string(rest), "private-input") {
		t.Fatal(string(rest), err)
	}
	if err = s.Wait(); err != nil {
		t.Fatal(err)
	}
}
func TestSSHManagerCancellationAndExpiryEndOwnedProcess(t *testing.T) {
	for _, kind := range []string{"cancel", "pty-cancel", "expiry"} {
		t.Run(kind, func(t *testing.T) {
			ttl := time.Minute
			if kind == "expiry" {
				ttl = 150 * time.Millisecond
			}
			f := newSSHManagerFixture(t, nil, ttl)
			s, err := f.client.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if kind == "pty-cancel" {
				if err = s.RequestPty("xterm", 24, 80, ssh.TerminalModes{ssh.ECHO: 0}); err != nil {
					t.Fatal(err)
				}
			}
			out, err := s.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Start("printf ready;exec sleep 30"); err != nil {
				t.Fatal(err)
			}
			if _, err = io.ReadFull(out, make([]byte, 5)); err != nil {
				t.Fatal(err)
			}
			if kind == "cancel" || kind == "pty-cancel" {
				f.cancel()
			}
			_, _, l := f.snapshot()
			eventually(t, func() bool {
				got, err := f.m.Inspect(l.ID)
				return err == nil && (got.State == "terminal" || got.State == "needs_attention")
			})
			got, _ := f.m.Inspect(l.ID)
			if got.State != "terminal" || got.Outcome != map[string]string{"cancel": "interrupted", "pty-cancel": "interrupted", "expiry": "expired"}[kind] {
				t.Fatal(got)
			}
		})
	}
}
func TestSSHManagerWireDescriptorCannotLaunch(t *testing.T) {
	m := newManager(t)
	var r RunRequest
	raw := `{"command":["/bin/sh","-c","touch forbidden"],"cwd":"` + t.TempDir() + `","ssh":{"schema":1,"source":"a","key":"key","kind":"exec","command_sha256":"hash","terminal_sha256":""},"sshRuntime":{"command":"touch forbidden"},"runtime":{"command":"touch forbidden"}}`
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	if r.sshRuntime != nil {
		t.Fatal("wire populated private runtime")
	}
	req := request("wire-ssh")
	if _, err := m.Acquire(req, &r); errorCode(err) != "invalid_request" {
		t.Fatal("wire descriptor accepted", err)
	}
	status, err := m.Status()
	if err != nil || len(status.Leases) != 0 {
		t.Fatal(status, err)
	}
}

func TestSSHManagerAtomicIntentAndProcessGateFaults(t *testing.T) {
	for _, phase := range []string{"ssh.admitted", "lease.started"} {
		t.Run(phase, func(t *testing.T) {
			m := newManager(t)
			m.mu.Lock()
			err := m.ensureSSHRecords()
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.db.Exec(`CREATE TRIGGER fail_ssh_gate BEFORE INSERT ON events WHEN NEW.type='` + phase + `' BEGIN SELECT RAISE(ABORT,'private SSH gate fault'); END`); err != nil {
				t.Fatal(err)
			}
			f := newSSHManagerFixture(t, m, time.Minute)
			marker := filepath.Join(f.q.CWD, "must-not-run")
			s, err := f.client.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.Run("touch '" + marker + "'"); err == nil {
				t.Fatal("ignored admission/gate failure")
			}
			if _, err = os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("process escaped failed intent/PID gate", err)
			}
			m.mu.Lock()
			var leases, ops int
			_ = m.db.QueryRow(`SELECT COUNT(*) FROM leases`).Scan(&leases)
			_ = m.db.QueryRow(`SELECT COUNT(*) FROM ssh_operations`).Scan(&ops)
			m.mu.Unlock()
			if phase == "ssh.admitted" && (leases != 0 || ops != 0) {
				t.Fatal("non-atomic intent/lease rollback", leases, ops)
			}
			if phase == "lease.started" && (leases != 1 || ops != 1) {
				t.Fatal("lost committed prelaunch intent", leases, ops)
			}
		})
	}
}
func TestSSHManagerSettledProofAndCorruptRelationships(t *testing.T) {
	for _, change := range []string{"none", "marker", "missing-intent", "wrong-command-hash", "alias", "null-ttl"} {
		t.Run(change, func(t *testing.T) {
			m, err := Open(filepath.Join(t.TempDir(), "state"), model.Resources{CPU: 1})
			if err != nil {
				t.Fatal(err)
			}
			closed := false
			t.Cleanup(func() {
				if !closed {
					_ = m.Close()
				}
			})
			f := newSSHManagerFixture(t, m, time.Minute)
			s, err := f.client.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Run("exit 0"); err != nil {
				t.Fatal(err)
			}
			s.Close()
			q, _, l := f.snapshot()
			if err = f.m.Close(); err != nil {
				t.Fatal(err)
			}
			closed = true
			home, err := filepath.EvalSymlinks(f.m.home)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "marker":
				_, err = db.Exec(`DELETE FROM metadata WHERE key='ssh_schema'`)
			case "missing-intent":
				_, err = db.Exec(`DELETE FROM ssh_operations`)
			case "wrong-command-hash":
				_, err = db.Exec(`UPDATE ssh_operations SET data=json_set(data,'$.request.command_sha256',?)`, strings.Repeat("e", 64))
			case "alias":
				_, err = db.Exec(`UPDATE ssh_operations SET data=json_set(data,'$.request.Kind','shell')`)
			case "null-ttl":
				_, err = db.Exec(`UPDATE ssh_operations SET data=json_set(data,'$.request.ttl_ns',NULL)`)
			}
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			source, err := database.Offline(context.Background(), home)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			proof, err := database.ProveDatabaseSettled(context.Background(), source, strconv.Itoa(os.Geteuid()))
			if change == "none" {
				if err != nil || !proof.Complete || !proof.Settled || proof.Tables["ssh_operations"] != 1 {
					t.Fatal(q, l, proof, err)
				}
			} else if err == nil {
				t.Fatal("malformed SSH evidence certified settled", change, proof)
			}
		})
	}
}

func TestSSHManagerSignalBackpressureAndPeerBudget(t *testing.T) {
	t.Run("signal", func(t *testing.T) {
		f := newSSHManagerFixture(t, nil, time.Minute)
		s, err := f.client.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		out, err := s.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Start("trap 'printf signaled;exit 0' USR1;printf ready;while :;do sleep .05;done"); err != nil {
			t.Fatal(err)
		}
		if _, err = io.ReadFull(out, make([]byte, 5)); err != nil {
			t.Fatal(err)
		}
		if err = s.Signal(ssh.SIGUSR1); err != nil {
			t.Fatal(err)
		}
		rest, err := io.ReadAll(out)
		if err != nil || string(rest) != "signaled" {
			t.Fatal(string(rest), err)
		}
		if err = s.Wait(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("blocked-output", func(t *testing.T) {
		f := newSSHManagerFixture(t, nil, time.Minute)
		s, err := f.client.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		// Acquire a pipe without consuming it: after the bounded SSH channel window
		// fills, the destination must still process cancellation and settle its job.
		if _, err = s.StdoutPipe(); err != nil {
			t.Fatal(err)
		}
		if err = s.Start("dd if=/dev/zero bs=65536 count=1024 2>/dev/null"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		_, _, l := f.snapshot()
		f.cancel()
		eventually(t, func() bool { got, err := f.m.Inspect(l.ID); return err == nil && got.State == "terminal" })
	})
	t.Run("aggregate-and-owner-budget", func(t *testing.T) {
		m := newManager(t)
		sessions := []*ssh.Session{}
		for i := 0; i < 3; i++ {
			f := newSSHManagerFixture(t, m, time.Minute)
			s, err := f.client.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			sessions = append(sessions, s)
			defer s.Close()
			err = s.Start("exec sleep 30")
			if i < 2 && err != nil {
				t.Fatal(err)
			}
			if i == 2 && err == nil {
				t.Fatal("exceeded aggregate peer CPU ceiling")
			}
		}
		for _, s := range sessions {
			_ = s.Close()
		}
		eventually(t, func() bool { status, err := m.Status(); return err == nil && status.Reserved.CPU == 0 })
		root, err := m.BudgetInspect("self")
		if err != nil {
			t.Fatal(err)
		}
		setPolicy(t, m, root.Budget, model.BudgetLimits{CPU: func() *float64 { v := .25; return &v }()})
		f := newSSHManagerFixture(t, m, time.Minute)
		s, err := f.client.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err = s.Start("true"); err == nil {
			t.Fatal("SSH bypassed owner policy ceiling")
		}
	})
}

func TestSSHManagerRestartNeverReexecutesRedactedRequest(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	m, err := Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = m.Close()
		}
	})
	f := newSSHManagerFixture(t, m, time.Minute)
	marker := filepath.Join(f.q.CWD, "one-execution")
	command := "printf x >> '" + marker + "'"
	s, err := f.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Run(command); err != nil {
		t.Fatal(err)
	}
	s.Close()
	q, ch, l := f.snapshot()
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	reopened, err := Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	found, err := reopened.LookupSSH(q.Source, q.Key)
	if err != nil || !found.Found || found.Lease.ID != l.ID {
		t.Fatal(found, err)
	}
	repeat, process, err := reopened.AcquireSSH(context.Background(), q, sshservice.Execution{Kind: "exec", Command: command}, ch, model.Resources{CPU: 1}, time.Minute, time.Now().Add(time.Minute))
	if err != nil || process != nil || repeat.ID != l.ID {
		t.Fatal(repeat, process, err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil || string(raw) != "x" {
		t.Fatal("restart replayed persisted command", string(raw), err)
	}
	// Losing only an operation row must not treat the retained lease key as a
	// successful new launch, or return a handle for a nonexistent owned job.
	reopened.mu.Lock()
	_, err = reopened.db.Exec(`DELETE FROM ssh_operations`)
	reopened.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.LookupSSH(q.Source, q.Key); errorCode(err) != "ssh_evidence_unavailable" {
		t.Fatal("lost intent looked absent", err)
	}
	if _, _, err = reopened.AcquireSSH(context.Background(), q, sshservice.Execution{Kind: "exec", Command: command}, ch, model.Resources{CPU: 1}, time.Minute, time.Now().Add(time.Minute)); errorCode(err) != "ssh_evidence_unavailable" {
		t.Fatal("lost intent recreated", err)
	}
	raw, err = os.ReadFile(marker)
	if err != nil || string(raw) != "x" {
		t.Fatal("lost intent replayed work", string(raw), err)
	}
}
func TestSSHManagerParentCompletionReapsOrdinaryDescendants(t *testing.T) {
	f := newSSHManagerFixture(t, nil, time.Minute)
	marker := filepath.Join(f.q.CWD, "descendant-must-not-run")
	s, err := f.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Run("(sleep .4;printf unsafe > '" + marker + "') & exit 0"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("owned SSH descendant survived parent completion", err)
	}
}

func TestSSHManagerRecoveredDescriptorCannotLaunch(t *testing.T) {
	m := newManager(t)
	l, err := m.Acquire(request("stored-ssh"), nil)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "must-not-replay")
	var stored RunRequest
	raw, _ := json.Marshal(RunRequest{Command: []string{"/bin/sh", "-c", "touch '" + marker + "'"}, CWD: t.TempDir(), SSH: &model.SSHRunDescriptor{Schema: 1, Key: "stored-key"}})
	if err = json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	got, err := m.launch(l, stored)
	m.mu.Unlock()
	if err != nil || got.State != "terminal" || got.Outcome != "launch_failed" {
		t.Fatal(got, err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("deserialized SSH command launched without runtime", err)
	}
}
