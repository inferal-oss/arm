package meshsshrelay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/meshforward"
	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

func creditFixture(t *testing.T, total int64) (string, meshforward.Grant) {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "mesh"), 0700); err != nil {
		t.Fatal(err)
	}
	g := meshforward.Grant{
		Workspace: h("1", 16), Generation: h("2", 16), Source: h("3", 16), Target: h("4", 16),
		SourceGeneration: 1, TargetGeneration: 1, DirectoryPin: h("5", 32), DirectorySPKI: h("6", 32),
		SourcePin: h("7", 32), SourceSPKI: h("8", 32), TargetPin: h("9", 32), TargetSPKI: h("a", 32),
		Account: "owner", Endpoint: meshforward.Endpoint{Name: "service", Network: "tcp", Address: "127.0.0.1:8080"},
		Revision: 1, MaxConnections: 2, BytesPerConnection: 1 << 20, BytesPerSecond: 1 << 20,
		TotalBytes: total, CreditNonce: h("b", 16),
		Transit: model.Resources{CPU: .01, Memory: 4 << 20}, MaxTTL: time.Minute, Expires: time.Now().UTC().Add(time.Hour),
	}
	return home, g
}

func commitCreditGrant(t *testing.T, home string, g meshforward.Grant) {
	t.Helper()
	review, err := ReviewPolicy(home, g.Target, g.TargetPin, "grant", "reviewed exact TCP credit", func(p *Policy) error {
		p.Forwards = append(p.Forwards, g)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := CommitPolicy(review); err != nil {
		t.Fatal(err)
	}
}

func TestForwardCreditConcurrentSequentialAndRestart(t *testing.T) {
	home, grant := creditFixture(t, 64<<10)
	commitCreditGrant(t, home, grant)
	ctx := t.Context()
	var wg sync.WaitGroup
	errors := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			granted, err := ReserveForwardBytes(ctx, home, grant.Target, grant.TargetPin, grant, 4<<10)
			if err == nil && granted != 4<<10 {
				err = fmt.Errorf("short concurrent reservation: %d", granted)
			}
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	// A fresh call has no in-memory proxy state, but still observes the spent
	// durable record. Neither another connection nor a daemon restart refills it.
	if _, err := ReserveForwardBytes(ctx, home, grant.Target, grant.TargetPin, grant, 1); err == nil {
		t.Fatal("aggregate credit reset after sequential reservations")
	}
	if err := CheckForwardCredit(ctx, home, grant.Target, grant.TargetPin, grant); err == nil {
		t.Fatal("exhausted durable credit passed pre-READY check")
	}
	d, err := policyDirectory(home)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ledger, exists, err := readForwardCreditAt(d)
	if err != nil || !exists || len(ledger.Entries) != 1 || ledger.Entries[0].Spent != grant.TotalBytes {
		t.Fatalf("durable spent total differs: %+v %v", ledger, err)
	}
}

func TestForwardCreditMissingCorruptAndLegacyDeny(t *testing.T) {
	home, grant := creditFixture(t, 1024)
	legacy := grant
	legacy.TotalBytes = 0
	legacy.CreditNonce = ""
	commitCreditGrant(t, home, legacy)
	policy, err := ReadPolicy(home, grant.Target, grant.TargetPin)
	if err != nil || len(policy.Forwards) != 1 || policy.Forwards[0] != legacy {
		t.Fatalf("legacy policy must remain inspectable: %+v %v", policy, err)
	}
	if legacy.ValidateCredit() == nil {
		t.Fatal("legacy grant invented an aggregate credit")
	}
	review, err := ReviewPolicy(home, grant.Target, grant.TargetPin, "grant", "review explicit TCP total", func(p *Policy) error {
		p.Forwards[0] = grant
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := CommitPolicy(review); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "mesh", forwardCreditName)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 1); err == nil {
		t.Fatal("missing preexisting ledger reset credit")
	}
	if err := os.WriteFile(path, []byte(`{"schema":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 1); err == nil {
		t.Fatal("corrupt ledger admitted a write")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "outside-credit.json")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 1); err == nil {
		t.Fatal("symlinked ledger admitted a write")
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "outside" {
		t.Fatalf("symlink target changed: %q %v", raw, err)
	}
}

func TestForwardCreditPreparationBeforePolicyCommitCannotGrantOrReset(t *testing.T) {
	home, grant := creditFixture(t, 1024)
	review, err := ReviewPolicy(home, grant.Target, grant.TargetPin, "grant", "review first credit", func(p *Policy) error {
		p.Forwards = append(p.Forwards, grant)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := policyDirectory(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = prepareForwardCreditsAt(d, review.Before, review.After); err != nil {
		t.Fatal(err)
	}
	d.Close() // Simulate process loss before the policy rename.
	if _, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 1); err == nil {
		t.Fatal("prepared ledger without policy publication granted credit")
	}
	if err := CommitPolicy(review); err != nil {
		t.Fatalf("exact original CAS review could not publish its prepared credit: %v", err)
	}
	policy, err := ReadPolicy(home, grant.Target, grant.TargetPin)
	if err != nil || policy.Schema != 1 || len(policy.Forwards) != 1 {
		t.Fatalf("exact review did not publish its authority: %+v %v", policy, err)
	}
	if n, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 1); err != nil || n != 1 {
		t.Fatalf("exact review lost prepared credit: %d %v", n, err)
	}
}

func TestForwardCreditOrphanedPreparationRetainsHistory(t *testing.T) {
	home, grant := creditFixture(t, 1024)
	review, err := ReviewPolicy(home, grant.Target, grant.TargetPin, "grant", "review first credit", func(p *Policy) error {
		p.Forwards = append(p.Forwards, grant)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := policyDirectory(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = prepareForwardCreditsAt(d, review.Before, review.After); err != nil {
		t.Fatal(err)
	}
	d.Close() // The original review is lost before policy publication.
	next := grant
	next.CreditNonce = strings.Repeat("c", 32)
	recovery, err := ReviewPolicy(home, grant.Target, grant.TargetPin, "grant", "new review after uncertain preparation", func(p *Policy) error {
		p.Forwards = append(p.Forwards, next)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if recovery.After.CreditLedgerID != review.After.CreditLedgerID {
		t.Fatal("recovery review discarded durable credit history")
	}
	if err := CommitPolicy(recovery); err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 1); err == nil {
		t.Fatal("unpublished original nonce acquired authority")
	}
	if n, err := ReserveForwardBytes(t.Context(), home, next.Target, next.TargetPin, next, 1); err != nil || n != 1 {
		t.Fatalf("new review could not use retained ledger: %d %v", n, err)
	}
}

func TestForwardCreditReplacementKeepsOldHistory(t *testing.T) {
	home, grant := creditFixture(t, 1024)
	commitCreditGrant(t, home, grant)
	if n, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 768); err != nil || n != 768 {
		t.Fatalf("initial credit reservation: %d %v", n, err)
	}
	next := grant
	next.Revision++
	next.CreditNonce = strings.Repeat("c", 32)
	next.TotalBytes = 2048
	review, err := ReviewPolicy(home, grant.Target, grant.TargetPin, "grant", "review replacement credit", func(p *Policy) error {
		p.Forwards[0] = next
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := CommitPolicy(review); err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 1); err == nil {
		t.Fatal("replaced grant spent old credit")
	}
	if n, err := ReserveForwardBytes(t.Context(), home, next.Target, next.TargetPin, next, 2048); err != nil || n != 2048 {
		t.Fatalf("replacement credit reservation: %d %v", n, err)
	}
	d, err := policyDirectory(home)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ledger, exists, err := readForwardCreditAt(d)
	if err != nil || !exists || len(ledger.Entries) != 2 || ledger.Entries[0].Spent != 768 || ledger.Entries[1].Spent != 2048 {
		t.Fatalf("replacement reset old ledger history: %+v %v", ledger, err)
	}
	revoke, err := ReviewPolicy(home, grant.Target, grant.TargetPin, "revoke", "withdraw replacement TCP grant", func(p *Policy) error {
		p.Forwards = nil
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := CommitPolicy(revoke); err != nil {
		t.Fatal(err)
	}
	reuse, err := ReviewPolicy(home, grant.Target, grant.TargetPin, "grant", "attempt to reuse old grant epoch", func(p *Policy) error {
		p.Forwards = append(p.Forwards, grant)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := CommitPolicy(reuse); err == nil {
		t.Fatal("revocation let an old credit nonce reset")
	}
}

func TestForwardCreditSmallRemainderIsExact(t *testing.T) {
	home, grant := creditFixture(t, 1000)
	commitCreditGrant(t, home, grant)
	if n, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 900); err != nil || n != 900 {
		t.Fatalf("first reservation: %d %v", n, err)
	}
	if n, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 200); err != nil || n != 100 {
		t.Fatalf("remaining credit not prepaid exactly: %d %v", n, err)
	}
	if _, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 1); err == nil {
		t.Fatal("aggregate credit exceeded after final partial reservation")
	}
}

func TestForwardCreditProcessRestart(t *testing.T) {
	if os.Getenv("ARM_FORWARD_CREDIT_TEST_CHILD") == "1" {
		var grant meshforward.Grant
		if err := json.Unmarshal([]byte(os.Getenv("ARM_FORWARD_CREDIT_TEST_GRANT")), &grant); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("ARM_FORWARD_CREDIT_TEST_HOME")
		n, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 700)
		if err != nil || n != 700 {
			t.Fatalf("replacement process reservation: %d %v", n, err)
		}
		return
	}
	home, grant := creditFixture(t, 1000)
	commitCreditGrant(t, home, grant)
	if n, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 100); err != nil || n != 100 {
		t.Fatalf("initial process reservation: %d %v", n, err)
	}
	raw, err := json.Marshal(grant)
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestForwardCreditProcessRestart$")
	child.Env = append(os.Environ(), "ARM_FORWARD_CREDIT_TEST_CHILD=1", "ARM_FORWARD_CREDIT_TEST_HOME="+home, "ARM_FORWARD_CREDIT_TEST_GRANT="+string(raw))
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("replacement process did not use durable credit: %v: %s", err, output)
	}
	if n, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 300); err != nil || n != 200 {
		t.Fatalf("original process ignored replacement spend: %d %v", n, err)
	}
}

func TestForwardCreditCatalogFullDeniesNewGrant(t *testing.T) {
	home, grant := creditFixture(t, 1000)
	commitCreditGrant(t, home, grant)
	d, err := policyDirectory(home)
	if err != nil {
		t.Fatal(err)
	}
	ledger, exists, err := readForwardCreditAt(d)
	if err != nil || !exists {
		t.Fatalf("initial ledger missing: %v", err)
	}
	for i := len(ledger.Entries); i < maxForwardCreditEntries; i++ {
		ledger.Entries = append(ledger.Entries, forwardCreditEntry{
			Nonce: fmt.Sprintf("%032x", i+1), Digest: strings.Repeat("d", 64), ReviewSHA256: strings.Repeat("e", 64), Total: 1,
		})
	}
	if err := writeForwardCreditAt(d, ledger); err != nil {
		t.Fatal(err)
	}
	d.Close()
	next := grant
	next.Revision++
	next.CreditNonce = strings.Repeat("e", 32)
	review, err := ReviewPolicy(home, grant.Target, grant.TargetPin, "grant", "review replacement after full credit history", func(p *Policy) error {
		p.Forwards[0] = next
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := CommitPolicy(review); err == nil {
		t.Fatal("full historical credit catalog reset for new grant")
	}
	policy, err := ReadPolicy(home, grant.Target, grant.TargetPin)
	if err != nil || len(policy.Forwards) != 1 || policy.Forwards[0].CreditNonce != grant.CreditNonce {
		t.Fatalf("catalog failure published replacement authority: %+v %v", policy, err)
	}
}

func TestForwardCreditHeldPolicyLockCancellation(t *testing.T) {
	if os.Getenv("ARM_FORWARD_CREDIT_LOCK_CHILD") == "1" {
		d, err := policyDirectory(os.Getenv("ARM_FORWARD_CREDIT_LOCK_HOME"))
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		fd, err := unix.Openat(int(d.Fd()), policyLockName, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer unix.Close(fd)
		if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(os.Stdout, "locked")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	home, grant := creditFixture(t, 1024)
	commitCreditGrant(t, home, grant)
	childCtx, childCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer childCancel()
	child := exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestForwardCreditHeldPolicyLockCancellation$")
	child.Env = append(os.Environ(), "ARM_FORWARD_CREDIT_LOCK_CHILD=1", "ARM_FORWARD_CREDIT_LOCK_HOME="+home)
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = child.Wait() })
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "locked" {
		t.Fatalf("separate process did not hold policy lock: %v %s", scanner.Err(), stderr.String())
	}
	checkCtx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := CheckForwardCredit(checkCtx, home, grant.Target, grant.TargetPin, grant); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("held lock ignored credit-check cancellation: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("credit-check cancellation waited on another process lock")
	}
	stream, client := net.Pipe()
	service, application := net.Pipe()
	defer client.Close()
	defer application.Close()
	p := &meshforward.Proxy{
		Dial: func(context.Context, string, string) (net.Conn, error) { return service, nil },
		CheckCredit: func(ctx context.Context, g meshforward.Grant) error {
			return CheckForwardCredit(ctx, home, grant.Target, grant.TargetPin, g)
		},
		Credit: func(_ context.Context, _ meshforward.Grant, n int) (int, error) { return n, nil },
	}
	q := meshforward.Request{Version: 1, Workspace: grant.Workspace, Generation: grant.Generation,
		Source: grant.Source, Target: grant.Target, SourcePin: grant.SourcePin, TargetPin: grant.TargetPin,
		Service: grant.Endpoint.Name, GrantRevision: grant.Revision, ConnectionID: h("d", 16),
		TTL: time.Second, RouteBindingHash: h("e", 32)}
	who := meshforward.Identity{SourcePin: grant.SourcePin, SourceSPKI: grant.SourceSPKI,
		TargetPin: grant.TargetPin, TargetSPKI: grant.TargetSPKI, Account: grant.Account}
	ready := make(chan struct{})
	done := make(chan error, 1)
	openCtx, openCancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer openCancel()
	go func() {
		done <- p.Open(openCtx, q, who, stream, func(context.Context) (meshforward.Grant, error) { return grant, nil },
			func() error { close(ready); return nil })
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("pre-READY lock wait did not cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pre-READY proxy kept waiting on another process lock")
	}
	select {
	case <-ready:
		t.Fatal("proxy sent READY without acquiring current credit authority")
	default:
	}
}

func TestForwardCreditAmbiguousDirectorySyncBurnsWithoutDelivery(t *testing.T) {
	home, grant := creditFixture(t, 1000)
	commitCreditGrant(t, home, grant)
	fault := errors.New("injected directory sync failure after ledger rename")
	n, err := reserveForwardBytesWithWrite(t.Context(), home, grant.Target, grant.TargetPin, grant, 100,
		func(d *os.File, ledger forwardCreditLedger) error {
			return writeForwardCreditAtWithFault(d, ledger, func() error { return fault })
		})
	if n != 0 || !errors.Is(err, fault) {
		t.Fatalf("ambiguous commit permitted a write: %d %v", n, err)
	}
	d, err := policyDirectory(home)
	if err != nil {
		t.Fatal(err)
	}
	ledger, exists, err := readForwardCreditAt(d)
	d.Close()
	if err != nil || !exists || len(ledger.Entries) != 1 || ledger.Entries[0].Spent != 100 {
		t.Fatalf("ambiguous rename discarded spent history: %+v %v", ledger, err)
	}
	if n, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 1000); err != nil || n != 900 {
		t.Fatalf("later reservation reset ambiguous credit: %d %v", n, err)
	}
}

func TestForwardCreditUnavailableNeverDialsServiceOrSendsReady(t *testing.T) {
	for _, state := range []string{"missing", "exhausted"} {
		t.Run(state, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			home, grant := creditFixture(t, 1024)
			grant.Endpoint.Address = listener.Addr().String()
			commitCreditGrant(t, home, grant)
			if state == "missing" {
				if err := os.Remove(filepath.Join(home, "mesh", forwardCreditName)); err != nil {
					t.Fatal(err)
				}
			} else {
				if n, err := ReserveForwardBytes(t.Context(), home, grant.Target, grant.TargetPin, grant, 1024); err != nil || n != 1024 {
					t.Fatalf("exhaustion setup: %d %v", n, err)
				}
			}
			stream, client := net.Pipe()
			defer client.Close()
			var dialCalls int
			ready := false
			proxy := &meshforward.Proxy{
				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					dialCalls++
					return (&net.Dialer{}).DialContext(ctx, network, address)
				},
				CheckCredit: func(ctx context.Context, g meshforward.Grant) error {
					return CheckForwardCredit(ctx, home, grant.Target, grant.TargetPin, g)
				},
				Credit: func(ctx context.Context, g meshforward.Grant, n int) (int, error) {
					return ReserveForwardBytes(ctx, home, grant.Target, grant.TargetPin, g, n)
				},
			}
			q := meshforward.Request{Version: 1, Workspace: grant.Workspace, Generation: grant.Generation,
				Source: grant.Source, Target: grant.Target, SourcePin: grant.SourcePin, TargetPin: grant.TargetPin,
				Service: grant.Endpoint.Name, GrantRevision: grant.Revision, ConnectionID: h("d", 16),
				TTL: time.Second, RouteBindingHash: h("e", 32)}
			who := meshforward.Identity{SourcePin: grant.SourcePin, SourceSPKI: grant.SourceSPKI,
				TargetPin: grant.TargetPin, TargetSPKI: grant.TargetSPKI, Account: grant.Account}
			if err := proxy.Open(t.Context(), q, who, stream, func(context.Context) (meshforward.Grant, error) { return grant, nil },
				func() error { ready = true; return nil }); err == nil {
				t.Fatal("unavailable credit admitted source")
			}
			if dialCalls != 0 || ready || proxy.Active() != 0 {
				t.Fatalf("unavailable credit touched endpoint: dials=%d ready=%v active=%d", dialCalls, ready, proxy.Active())
			}
			tcp := listener.(*net.TCPListener)
			_ = tcp.SetDeadline(time.Now().Add(100 * time.Millisecond))
			if conn, err := tcp.Accept(); err == nil {
				conn.Close()
				t.Fatal("unavailable credit opened destination service socket")
			}
		})
	}
}
