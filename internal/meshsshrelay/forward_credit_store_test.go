package meshsshrelay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/meshforward"
	"github.com/inferal-oss/arm/internal/model"
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
	if err := CommitPolicy(review); err == nil {
		t.Fatal("ambiguous repeated commit reused a prepared nonce")
	}
	policy, err := ReadPolicy(home, grant.Target, grant.TargetPin)
	if err != nil || policy.Schema != 0 {
		t.Fatalf("failed commit published authority: %+v %v", policy, err)
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
