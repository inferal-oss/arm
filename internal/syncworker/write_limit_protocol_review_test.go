package syncworker

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/syncreconcile"
	"getarm.org/internal/syncservice"
)

type capMutatingSession struct {
	admittedSession
	ledger  *syncservice.Ledger
	mutated bool
}

func (s *capMutatingSession) OpenTransfer(ctx context.Context, b batch) (transferHandle, error) {
	rows, err := s.ledger.Workspaces(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.Role == "source-"+batchID(b) {
			if err := os.WriteFile(filepath.Join(r.Path, "stage", "file"), []byte(strings.Repeat("overrun", 4096)), 0600); err != nil {
				return nil, err
			}
			s.mutated = true
			return s.admittedSession.OpenTransfer(ctx, b)
		}
	}
	return nil, syncservice.ErrUnknown
}

// This pair uses controlled admission holds and real original duplex sessions.
// It models a peer changing its private sender stage after reviewed materialization.
func TestStagingCapReviewOverrunAbortsOriginalPairBeforePublication(t *testing.T) {
	p := setupPair(t)
	write(t, p.local, "file", "old")
	write(t, p.remote, "keep", "retained")
	req := requestPair(t, p, syncreconcile.Push, syncreconcile.Additive, nil, nil)
	plan, err := syncreconcile.Reconcile(req)
	if err != nil {
		t.Fatal(err)
	}
	resources := model.Resources{CPU: 1, Memory: 64 << 20, Disk: 64 << 20}
	local, rows, err := syncservice.NewIntent("staging-overrun-review-key", FixedCommandSHA256(), hash("reviewed SSH control"), "private staging overrun review", syncreconcile.Local, req, plan, resources, resources, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	remote, other, err := syncservice.NewIntent(local.Key, local.WorkerCommandSHA256, local.SSHControlSHA256, local.Reason, syncreconcile.Remote, req, plan, resources, resources, local.Created)
	if err != nil {
		t.Fatal(err)
	}
	reserveTest(t, p.localDB, local, rows)
	reserveTest(t, p.remoteDB, remote, other)
	a, b := net.Pipe()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	localJoins, remoteJoins := 0, 0
	ls, err := newSingleStreamSession(a, func() error { localJoins++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	rs, err := newSingleStreamSession(b, func() error { remoteJoins++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	ll := &syncservice.Ledger{DB: p.localDB, Key: local.Key, Side: syncreconcile.Local}
	rl := &syncservice.Ledger{DB: p.remoteDB, Key: remote.Key, Side: syncreconcile.Remote}
	mutator := &capMutatingSession{admittedSession: ls, ledger: ll}
	lh, rh := &testHold{intent: local, root: req.Binding.Local.RootID, disk: resources.Disk}, &testHold{intent: remote, root: req.Binding.Remote.RootID, disk: resources.Disk}
	le := &endpoint{Side: syncreconcile.Local, Root: p.local, WorkParent: p.localWork, Names: p.names, Metadata: p.metadata, Ledger: ll, Hold: lh, Session: mutator}
	re := &endpoint{Side: syncreconcile.Remote, Root: p.remote, WorkParent: p.remoteWork, Names: p.names, Metadata: p.metadata, Ledger: rl, Hold: rh, Session: rs}
	localDone, remoteDone := make(chan error, 1), make(chan error, 1)
	go func() { localDone <- runEndpoint(ctx, le) }()
	go func() { remoteDone <- runEndpoint(ctx, re) }()
	localJoined, remoteJoined := false, false
	defer func() {
		cancel()
		ls.Abort()
		rs.Abort()
		if !localJoined {
			<-localDone
		}
		if !remoteJoined {
			<-remoteDone
		}
	}()
	var lerr, rerr error
	timedOut := false
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for !localJoined || !remoteJoined {
		select {
		case lerr = <-localDone:
			localJoined = true
		case rerr = <-remoteDone:
			remoteJoined = true
		case <-timer.C:
			timedOut = true
			cancel()
			if !localJoined {
				lerr = <-localDone
				localJoined = true
			}
			if !remoteJoined {
				rerr = <-remoteDone
				remoteJoined = true
			}
		}
	}
	if !mutator.mutated || !lh.charged || !rh.charged || !ls.joined || !rs.joined || localJoins != 1 || remoteJoins != 1 {
		t.Fatal("overrun lost original stage/hold/carrier witnesses", localJoins, remoteJoins)
	}
	if contents(t, p.local, "file") != "old" || contents(t, p.remote, "keep") != "retained" {
		t.Fatal("overrun changed selected input")
	}
	if _, err := os.Stat(filepath.Join(p.remote.Path(), "file")); !os.IsNotExist(err) {
		t.Fatal("overrun published into selected receiver root", err)
	}
	for _, e := range []*endpoint{le, re} {
		lookup, err := e.Ledger.Lookup(t.Context())
		if err != nil || lookup.State != "reserved" || len(lookup.Effects) != 0 || lookup.LocalReceipt != nil || lookup.RemoteReceipt != nil {
			t.Fatal("overrun advanced native effects/receipts", lookup.State, err)
		}
		locators, err := e.Ledger.Workspaces(t.Context())
		if err != nil || len(locators) == 0 {
			t.Fatal("overrun lost retained physical workspace obligation", err)
		}
	}
	for _, db := range []struct {
		ledger *syncservice.Ledger
		side   syncreconcile.Side
	}{{ll, syncreconcile.Local}, {rl, syncreconcile.Remote}} {
		if _, err := syncservice.LoadBaseline(t.Context(), db.ledger.DB, req.Binding, db.side); err == nil {
			t.Fatal("overrun confirmed a baseline")
		}
	}
	if timedOut {
		t.Fatal("cap overrun waited for parent cancellation before original joins", lerr, rerr)
	}
	if ctx.Err() != nil || rerr == nil || !strings.Contains(rerr.Error(), "receiver write limit exceeded") || lerr == nil {
		t.Fatal("overrun did not promptly preserve its cap error", lerr, rerr, ctx.Err())
	}
}
