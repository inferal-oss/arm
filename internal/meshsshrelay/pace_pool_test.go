package meshsshrelay

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
)

func TestTransitRateSharedAcrossConnectionsAndDirections(t *testing.T) {
	g := RelayGrant{Workspace: strings.Repeat("a", 32), Generation: strings.Repeat("b", 32), Local: strings.Repeat("c", 32), Revision: 1, SSH: true, Transit: model.Resources{CPU: .01, Memory: 1 << 20}, BytesPerSecond: 64 << 10, Expires: time.Now().Add(time.Minute)}
	var pool PacerPool
	first, err := pool.ForGrant(g)
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.ForGrant(g)
	if err != nil || first != second {
		t.Fatalf("connections received different rate budgets: %v", err)
	}
	if err = first.wait(t.Context(), 64<<10); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err = second.wait(ctx, 32<<10); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second connection bypassed shared budget: %v", err)
	}
	g.Revision++
	next, err := pool.ForGrant(g)
	if err != nil || next == first {
		t.Fatal("different exact review did not receive its own budget", err)
	}
}
