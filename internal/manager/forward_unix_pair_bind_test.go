package manager

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/leaseclock"
	"getarm.org/internal/local"
)

func newShortForwardUnixPairFixture(t *testing.T) (*Manager, *ForwardListener, *ForwardUnixChild, chan struct{}, chan struct{}) {
	t.Helper()
	var root string
	for i := 0; i < 16 && root == ""; i++ {
		var nonce [1]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			t.Fatal(err)
		}
		candidate := filepath.Join("/private/tmp", hex.EncodeToString(nonce[:]))
		if err := os.Mkdir(candidate, 0700); err == nil {
			root = candidate
		} else if !errors.Is(err, os.ErrExist) {
			t.Fatal(err)
		}
	}
	if root == "" {
		t.Fatal("no short private test directory available")
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return newForwardUnixPairFixtureAt(t, root)
}

func TestForwardUnixOriginalBindAndNativeTerminal(t *testing.T) {
	m, h, c, joined, childJoined := newShortForwardUnixPairFixture(t)
	p, err := c.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.Inspect(p.storage)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lease.ManagedDir, p.data.SocketName)
	if len(path) > 100 {
		t.Fatalf("short fixture socket path has %d bytes: %s", len(path), path)
	}
	workersJoined := make(chan struct{})
	bound, err := p.BindOriginal(context.Background(), workersJoined)
	if err != nil || bound == nil || p.data.Phase != "bound" || p.data.Bound == nil {
		t.Fatalf("original bind did not reach durable bound phase: %+v %v", p.data, err)
	}
	if _, err = os.Lstat(path); err != nil {
		t.Fatalf("original bound socket missing: %v", err)
	}
	close(workersJoined)
	if err = bound.CloseAfterJoin(context.Background()); err != nil {
		t.Fatalf("original native close and settlement failed: %v", err)
	}
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("original socket remained after native terminal: %v", err)
	}
	close(childJoined)
	if err = c.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
	close(joined)
	if _, err = h.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	offline, err := database.Offline(context.Background(), m.home)
	if err != nil {
		t.Fatal(err)
	}
	proof, proofErr := database.ProveDatabaseSettled(context.Background(), offline, m.owner)
	if err = offline.Close(); err != nil {
		t.Fatal(err)
	}
	if proofErr != nil || !proof.Complete || !proof.Settled {
		t.Fatalf("native terminal tombstone did not settle: %+v %v", proof, proofErr)
	}
}

func TestForwardUnixOverlongPathRefusesBeforeIntent(t *testing.T) {
	m, _, c, _, _ := newForwardUnixPairFixtureAt(t, filepath.Join(t.TempDir(), strings.Repeat("a", 40)))
	p, err := c.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.Inspect(p.storage)
	if err != nil {
		t.Fatal(err)
	}
	if len(filepath.Join(lease.ManagedDir, p.data.SocketName)) <= 100 {
		t.Fatal("fixture did not exceed Unix socket path limit")
	}
	if _, err = p.BindOriginal(context.Background(), make(chan struct{})); err == nil || p.data.Phase != "planned" || p.binding {
		t.Fatalf("overlong path advanced into native bind intent: %+v %v", p.data, err)
	}
	var intents int
	if err = m.db.QueryRow(`SELECT count(*) FROM events WHERE type='forward.unix_pair_bind_intent'`).Scan(&intents); err != nil || intents != 0 {
		t.Fatalf("overlong path emitted bind intent: %d %v", intents, err)
	}
	if err = p.AbortBeforeBind(context.Background()); err != nil {
		t.Fatalf("overlong path lost no-bind abort: %v", err)
	}
}

func TestForwardUnixBoundRestartRefusesNoBindRecovery(t *testing.T) {
	m, _, c, joined, childJoined := newShortForwardUnixPairFixture(t)
	p, err := c.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	workersJoined := make(chan struct{})
	if _, err = p.BindOriginal(context.Background(), workersJoined); err != nil {
		t.Fatal(err)
	}
	home := m.home
	close(workersJoined)
	close(childJoined)
	close(joined)
	_ = m.Close() // An unfinished native pair intentionally retains both charges.
	lock, err := local.Lock(home, "daemon.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err = OpenConfiguredWithUnixRecovery(home, forwardUnixPairSettings(), lock); errorCode(err) != "forward_unix_pair_unavailable" {
		t.Fatalf("restart interpreted bound native state as no-bind: %v", err)
	}
}

func TestForwardUnixIntentAckRechecksNativeEntitlementBeforeBind(t *testing.T) {
	for _, kind := range []string{"elapsed", "boot_changed"} {
		t.Run(kind, func(t *testing.T) {
			m, h, c, _, _ := newShortForwardUnixPairFixture(t)
			p, err := c.IssueStoragePair(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			lease, err := m.Inspect(p.storage)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(lease.ManagedDir, p.data.SocketName)
			m.mu.Lock()
			originalRead := m.leaseClockRead
			m.mu.Unlock()
			t.Cleanup(func() { m.mu.Lock(); m.leaseClockRead = originalRead; m.mu.Unlock() })
			p.beforeNativeBindTest = func() {
				if h.Context().Err() != nil {
					t.Error("context expired before the native clock barrier")
				}
				m.mu.Lock()
				sample := m.leaseClockLast
				if kind == "elapsed" {
					sample.Tick = h.result.Clock.End + 1
				} else {
					sample.Boot = "different-boot"
				}
				m.leaseClockRead = func() (leaseclock.Sample, error) { return sample, nil }
				m.mu.Unlock()
			}
			if _, err = p.BindOriginal(context.Background(), make(chan struct{})); err == nil {
				t.Fatal("native bind continued after lease clock lost entitlement")
			}
			if p.data.Phase != "bind_intent" || p.data.Bound != nil || p.native == nil || h.unixFault == "" {
				t.Fatalf("failed prebind check lost intent or original charge: %+v fault=%q", p.data, h.unixFault)
			}
			if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("socket syscall ran after lost native entitlement: %v", err)
			}
			var intents, bounds int
			if err = m.db.QueryRow(`SELECT count(*) FROM events WHERE type='forward.unix_pair_bind_intent'`).Scan(&intents); err != nil {
				t.Fatal(err)
			}
			if err = m.db.QueryRow(`SELECT count(*) FROM events WHERE type='forward.unix_pair_bound'`).Scan(&bounds); err != nil {
				t.Fatal(err)
			}
			if intents != 1 || bounds != 0 {
				t.Fatalf("wrong bind event sequence: intent=%d bound=%d", intents, bounds)
			}
			if err = p.AbortBeforeBind(context.Background()); err == nil {
				t.Fatal("uncertain intent allowed no-bind abort")
			}
		})
	}
}

func TestForwardUnixIntentAckRechecksOriginalTupleBeforeBind(t *testing.T) {
	for _, kind := range []string{"source_revision", "storage_clock", "pair_row", "source_run_text", "source_run_blob", "storage_run_text", "storage_run_blob", "pair_socket_column", "pair_oversized_column"} {
		t.Run(kind, func(t *testing.T) {
			m, h, c, _, _ := newShortForwardUnixPairFixture(t)
			p, err := c.IssueStoragePair(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			lease, err := m.Inspect(p.storage)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(lease.ManagedDir, p.data.SocketName)
			var injectErr error
			p.beforeNativeBindTest = func() {
				switch kind {
				case "source_revision":
					m.mu.Lock()
					h.result.Revision++
					m.mu.Unlock()
				case "storage_clock":
					_, injectErr = m.db.Exec(`UPDATE leases SET data=json_set(data,'$.elapsed_deadline.boot','changed-boot') WHERE id=?`, p.storage)
				case "pair_row":
					_, injectErr = m.db.Exec(`UPDATE forward_unix_pairs SET data=json_set(data,'$.revision',99) WHERE source_id=?`, p.source)
				case "source_run_text":
					_, injectErr = m.db.Exec(`UPDATE leases SET run='{}' WHERE id=?`, p.source)
				case "source_run_blob":
					_, injectErr = m.db.Exec(`UPDATE leases SET run=x'00' WHERE id=?`, p.source)
				case "storage_run_text":
					_, injectErr = m.db.Exec(`UPDATE leases SET run='{}' WHERE id=?`, p.storage)
				case "storage_run_blob":
					_, injectErr = m.db.Exec(`UPDATE leases SET run=x'00' WHERE id=?`, p.storage)
				case "pair_socket_column":
					_, injectErr = m.db.Exec(`UPDATE forward_unix_pairs SET socket_name='f-ffffffffffffffffffffffffffffffff.sock' WHERE source_id=?`, p.source)
				case "pair_oversized_column":
					_, injectErr = m.db.Exec(`UPDATE forward_unix_pairs SET socket_name=? WHERE source_id=?`, strings.Repeat("f", 5000), p.source)
				}
			}
			if _, err = p.BindOriginal(context.Background(), make(chan struct{})); injectErr != nil {
				t.Fatal(injectErr)
			}
			if err == nil || p.data.Phase != "bind_intent" || p.native == nil || !p.native.dirCloseAttempted || h.unixFault == "" {
				t.Fatalf("changed %s tuple reached native bind or lost charge: phase=%q fault=%q error=%v", kind, p.data.Phase, h.unixFault, err)
			}
			if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("changed %s tuple created a socket: %v", kind, err)
			}
		})
	}
}

func TestForwardUnixJoinedChildRefusesBeforeIntent(t *testing.T) {
	m, _, c, _, childJoined := newShortForwardUnixPairFixture(t)
	p, err := c.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	close(childJoined)
	if _, err = p.BindOriginal(context.Background(), make(chan struct{})); err == nil || p.data.Phase != "planned" || p.native != nil {
		t.Fatalf("joined original child entered native bind: %+v %v", p.data, err)
	}
	var intents int
	if err = m.db.QueryRow(`SELECT count(*) FROM events WHERE type='forward.unix_pair_bind_intent'`).Scan(&intents); err != nil || intents != 0 {
		t.Fatalf("joined child emitted bind intent: %d %v", intents, err)
	}
	if err = p.AbortBeforeBind(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestForwardUnixJoinAtIntentAckRefusesBeforeSocket(t *testing.T) {
	for _, kind := range []string{"workers", "child"} {
		t.Run(kind, func(t *testing.T) {
			m, _, c, _, childJoined := newShortForwardUnixPairFixture(t)
			p, err := c.IssueStoragePair(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			lease, err := m.Inspect(p.storage)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(lease.ManagedDir, p.data.SocketName)
			workersJoined := make(chan struct{})
			p.beforeNativeBindTest = func() {
				if kind == "workers" {
					close(workersJoined)
				} else {
					close(childJoined)
				}
			}
			if _, err = p.BindOriginal(context.Background(), workersJoined); err == nil {
				t.Fatal("joined worker reached native bind")
			}
			if p.data.Phase != "bind_intent" || p.native == nil || !p.native.dirCloseAttempted {
				t.Fatalf("joined worker lost intent or original directory close: %+v", p.data)
			}
			if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("joined worker created a socket: %v", err)
			}
		})
	}
}

func TestForwardUnixBoundAckFailureClosesOriginalDirectory(t *testing.T) {
	m, h, c, _, _ := newShortForwardUnixPairFixture(t)
	p, err := c.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.Inspect(p.storage)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lease.ManagedDir, p.data.SocketName)
	if _, err = m.db.Exec(`CREATE TRIGGER test_forward_unix_bound_ack BEFORE UPDATE OF phase ON forward_unix_pairs WHEN NEW.phase='bound' BEGIN SELECT RAISE(FAIL,'bound acknowledgement blocked'); END`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.db.Exec(`DROP TRIGGER test_forward_unix_bound_ack`) }()
	if _, err = p.BindOriginal(context.Background(), make(chan struct{})); err == nil {
		t.Fatal("failed bound acknowledgement returned a capability")
	}
	if p.native == nil || !p.native.dirCloseAttempted || p.native.listener == nil || h.unixFault == "" || p.data.Phase != "bind_intent" {
		t.Fatalf("failed bound acknowledgement dropped original custody or charge: %+v %v", p.data, err)
	}
	if _, statErr := os.Lstat(path); statErr != nil {
		t.Fatalf("failed acknowledgement unexpectedly unlinked the uncertain socket: %v", statErr)
	}
	if _, acceptErr := p.native.listener.AcceptUnix(); acceptErr == nil {
		t.Fatal("original listener remained open after failed acknowledgement")
	}
	if closeErr := p.native.closeDirectory(); closeErr != p.native.dirCloseErr {
		t.Fatalf("original directory close did not retain its first result: %v / %v", closeErr, p.native.dirCloseErr)
	}
}

func TestForwardUnixConstructorFailureClosesOriginalDirectory(t *testing.T) {
	for _, kind := range []string{"bind", "observation"} {
		t.Run(kind, func(t *testing.T) {
			m, h, c, _, _ := newShortForwardUnixPairFixture(t)
			p, err := c.IssueStoragePair(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			lease, err := m.Inspect(p.storage)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(lease.ManagedDir, p.data.SocketName)
			var injectErr error
			if kind == "bind" {
				p.beforeNativeBindTest = func() { injectErr = os.WriteFile(path, []byte("occupied"), 0600) }
			} else {
				p.afterNativeBindTest = func() {
					injectErr = os.Remove(path)
					if injectErr == nil {
						injectErr = os.WriteFile(path, []byte("replaced"), 0600)
					}
				}
			}
			bound, bindErr := p.BindOriginal(context.Background(), make(chan struct{}))
			if injectErr != nil {
				t.Fatal(injectErr)
			}
			if bound != nil || bindErr == nil || p.native == nil || !p.native.dirCloseAttempted || p.data.Phase != "bind_intent" || h.unixFault == "" {
				t.Fatalf("failed %s retained a capability or original directory: bound=%v phase=%q fault=%q error=%v", kind, bound, p.data.Phase, h.unixFault, bindErr)
			}
			if p.native.directory.Check() == nil {
				t.Fatal("original directory FD remained usable after constructor failure")
			}
		})
	}
}

func TestForwardUnixBoundAckRechecksBeforeCapabilityReturn(t *testing.T) {
	for _, kind := range []string{"workers_joined", "child_joined", "elapsed"} {
		t.Run(kind, func(t *testing.T) {
			m, h, c, _, childJoined := newShortForwardUnixPairFixture(t)
			p, err := c.IssueStoragePair(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			workersJoined := make(chan struct{})
			m.mu.Lock()
			originalRead := m.leaseClockRead
			m.mu.Unlock()
			t.Cleanup(func() { m.mu.Lock(); m.leaseClockRead = originalRead; m.mu.Unlock() })
			p.afterBoundAckTest = func() {
				switch kind {
				case "workers_joined":
					close(workersJoined)
				case "child_joined":
					close(childJoined)
				case "elapsed":
					m.mu.Lock()
					sample := m.leaseClockLast
					sample.Tick = h.result.Clock.End + 1
					m.leaseClockRead = func() (leaseclock.Sample, error) { return sample, nil }
					m.mu.Unlock()
				}
			}
			bound, bindErr := p.BindOriginal(context.Background(), workersJoined)
			if bound != nil || bindErr == nil || p.data.Phase != "bound" || p.native == nil || !p.native.dirCloseAttempted || h.unixFault == "" {
				t.Fatalf("post-ACK %s returned a capability or lost charged native state: bound=%v phase=%q fault=%q error=%v", kind, bound, p.data.Phase, h.unixFault, bindErr)
			}
		})
	}
}

func TestForwardUnixManagerCloseRetiresUnfinishedDirectory(t *testing.T) {
	m, _, c, joined, childJoined := newShortForwardUnixPairFixture(t)
	p, err := c.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bound, err := p.BindOriginal(context.Background(), make(chan struct{}))
	if err != nil {
		t.Fatal(err)
	}
	close(childJoined)
	close(joined)
	_ = m.Close()
	if !bound.dirCloseAttempted || p.data.Phase != "bound" {
		t.Fatalf("Manager shutdown lost unfinished original directory: closed=%t phase=%q", bound.dirCloseAttempted, p.data.Phase)
	}
}

func TestForwardUnixConstructorKeepsFirstCloseError(t *testing.T) {
	_, _, c, _, _ := newShortForwardUnixPairFixture(t)
	p, err := c.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p.afterNativeBindTest = func() { _ = p.native.listener.Close() }
	if _, err = p.BindOriginal(context.Background(), make(chan struct{})); err == nil {
		t.Fatal("closed original listener was accepted")
	}
	if p.native == nil || p.native.closeErr == nil || !p.native.dirCloseAttempted {
		t.Fatalf("failed original close lost first error or directory retirement: %v", err)
	}
	first := p.native.closeErr
	if retry := p.native.closeOriginal(); retry != first {
		t.Fatalf("original close result changed on retry: first=%v retry=%v", first, retry)
	}
}

func TestForwardUnixManagerCloseJoinsInFlightConstructor(t *testing.T) {
	m, _, c, joined, childJoined := newShortForwardUnixPairFixture(t)
	p, err := c.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	p.beforeNativeBindTest = func() { close(entered); <-release }
	type result struct {
		bound *ForwardUnixBound
		err   error
	}
	bindDone := make(chan result, 1)
	go func() {
		bound, bindErr := p.BindOriginal(context.Background(), make(chan struct{}))
		bindDone <- result{bound, bindErr}
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("constructor did not reach intent barrier")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- m.Close() }()
	deadline := time.After(10 * time.Second)
	for {
		m.mu.Lock()
		closing := m.closing
		m.mu.Unlock()
		if closing {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Manager.Close did not request shutdown")
		case <-time.After(time.Millisecond):
		}
	}
	close(childJoined)
	close(joined)
	select {
	case <-closeDone:
		t.Fatal("Manager.Close returned before the constructor joined")
	default:
	}
	close(release)
	select {
	case got := <-bindDone:
		if got.bound != nil || got.err == nil {
			t.Fatalf("shutdown returned a positive native owner: %+v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("constructor did not return after shutdown")
	}
	select {
	case <-closeDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Manager.Close did not join the constructor")
	}
	if p.native == nil || !p.native.dirCloseAttempted {
		t.Fatal("shutdown did not retire the original held directory")
	}
}

func TestForwardUnixManagerCloseWaitsForOriginalCleanup(t *testing.T) {
	m, _, c, joined, childJoined := newShortForwardUnixPairFixture(t)
	p, err := c.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	workersJoined := make(chan struct{})
	b, err := p.BindOriginal(context.Background(), workersJoined)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.Inspect(p.storage)
	if err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(lease.ManagedDir, p.data.SocketName)
	close(workersJoined)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	b.beforeNativeCleanupTest = func() { close(entered); <-release }
	finishDone := make(chan error, 1)
	go func() { finishDone <- b.CloseAfterJoin(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("cleanup did not reach original directory barrier")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- m.Close() }()
	deadline := time.After(10 * time.Second)
	for {
		m.mu.Lock()
		closing, shutdownRequested := m.closing, p.shutdownRequested
		m.mu.Unlock()
		if closing && shutdownRequested {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Manager.Close did not reach original owner retirement")
		case <-time.After(time.Millisecond):
		}
	}
	close(childJoined)
	close(joined)
	earlyClose := false
	var finishErr, closeErr error
	select {
	case closeErr = <-closeDone:
		earlyClose = true
	default:
	}
	close(release)
	select {
	case finishErr = <-finishDone:
	case <-time.After(10 * time.Second):
		t.Fatal("original cleanup did not finish")
	}
	if !earlyClose {
		select {
		case closeErr = <-closeDone:
		case <-time.After(10 * time.Second):
			t.Fatal("Manager.Close did not join original cleanup")
		}
	}
	if earlyClose || !b.dirCloseAttempted {
		t.Fatalf("Manager.Close raced original directory retirement: early=%t closed=%t finish=%v close=%v", earlyClose, b.dirCloseAttempted, finishErr, closeErr)
	}
	if _, statErr := os.Lstat(socketPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("original socket path remains after winning cleanup: %v; finish=%v close=%v", statErr, finishErr, closeErr)
	}
	db, err := sql.Open("sqlite", filepath.Join(m.home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var phase, raw string
	if err = db.QueryRow(`SELECT phase,data FROM forward_unix_pairs WHERE source_id=?`, p.source).Scan(&phase, &raw); err != nil {
		t.Fatalf("durable pair row unavailable after winning cleanup: %v; finish=%v close=%v", err, finishErr, closeErr)
	}
	var durable forwardUnixPairData
	if err = json.Unmarshal([]byte(raw), &durable); err != nil || phase != "terminal" || durable.NativeTerminal == nil || durable.NativeTerminal.Socket != b.socket {
		t.Fatalf("winning original cleanup lacked durable native terminal: phase=%q proof=%+v decode=%v finish=%v close=%v", phase, durable.NativeTerminal, err, finishErr, closeErr)
	}
	if finishErr != nil || closeErr != nil {
		t.Logf("ordinary post-terminal settlement/shutdown result: finish=%v close=%v", finishErr, closeErr)
	}
}
