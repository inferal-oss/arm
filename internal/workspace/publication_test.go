package workspace

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"
)

// queuedContext signals that a Runtime call has registered cancellation, before
// it waits for Store.mu. It does not manufacture membership or clock evidence.
type queuedContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
	done    chan struct{}
}

func newQueuedContext() *queuedContext {
	return &queuedContext{Context: context.Background(), entered: make(chan struct{}), done: make(chan struct{})}
}
func (c *queuedContext) Done() <-chan struct{} { return c.done }
func (c *queuedContext) AfterFunc(func()) func() bool {
	c.once.Do(func() { close(c.entered) })
	return func() bool { return true }
}
func publicationWait(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal("publication barrier timed out")
	}
}

func TestRefreshWaitingPreservesStillValidActiveMembership(t *testing.T) {
	d, root, b, c, _, br, _ := routingFixtures(t)
	q, e := br.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	b.s.mu.Lock()
	parent := newQueuedContext()
	result := make(chan error, 1)
	go func() { result <- br.Accept(parent, d.self, snapshot) }()
	publicationWait(t, parent.entered)
	br.mu.Lock()
	old, ok := br.active[root.ID]
	br.mu.Unlock()
	b.s.mu.Unlock()
	if e = <-result; e != nil {
		t.Fatal(e)
	}
	if !ok || old.hash == "" {
		t.Fatal("queued refresh removed still-valid membership before obtaining the ledger")
	}
	if _, e = br.RouteAccess(b.ctx, root.ID, b.self.Host, c.self.Host, RouteMessage); e != nil {
		t.Fatal(e)
	}
}

func TestRefreshPublishesCacheWithLedgerBeforeWaitingRoute(t *testing.T) {
	d, root, b, c, _, br, _ := routingFixtures(t)
	q, e := br.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	committed, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	b.s.fault = func(stage string) error {
		if stage == "after_commit" {
			once.Do(func() { close(committed); <-release })
		}
		return nil
	}
	defer func() { b.s.mu.Lock(); b.s.fault = nil; b.s.mu.Unlock() }()
	accepted := make(chan error, 1)
	go func() { accepted <- br.Accept(b.ctx, d.self, snapshot) }()
	publicationWait(t, committed)
	parent := newQueuedContext()
	routed := make(chan error, 1)
	go func() { _, e := br.RouteAccess(parent, root.ID, b.self.Host, c.self.Host, RouteMessage); routed <- e }()
	publicationWait(t, parent.entered)
	close(release)
	if e = <-accepted; e != nil {
		t.Fatal(e)
	}
	if e = <-routed; e != nil {
		t.Fatal("route mixed pre-refresh cache with committed replacement ledger:", e)
	}
}

func TestRefreshLostAcknowledgementDoesNotActivateOrDestroyLaterAcceptance(t *testing.T) {
	d, root, b, c, _, br, _ := routingFixtures(t)
	q, e := br.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	b.s.fault = func(stage string) error {
		if stage == "after_commit" {
			return errors.New("lost reply")
		}
		return nil
	}
	e = br.Accept(b.ctx, d.self, snapshot)
	b.s.fault = nil
	if code(e) != "workspace_outcome_unknown" {
		t.Fatal(e)
	}
	if _, e = br.RouteAccess(b.ctx, root.ID, b.self.Host, c.self.Host, RouteMessage); e == nil {
		t.Fatal("lost-ACK refresh activated authority")
	}
	q, e = br.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e = d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	if e = br.Accept(b.ctx, d.self, snapshot); e != nil {
		t.Fatal(e)
	}
	if _, e = br.Check(b.ctx, root.ID, c.self.Host); e != nil {
		t.Fatal("fresh later acceptance lost:", e)
	}
}

func TestRefreshCloseJoinsCommittedButUnpublishedObservation(t *testing.T) {
	d, root, b, c, _, br, _ := routingFixtures(t)
	q, e := br.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	committed, release := make(chan struct{}), make(chan struct{})
	b.s.fault = func(stage string) error {
		if stage == "after_commit" {
			close(committed)
			<-release
		}
		return nil
	}
	result := make(chan error, 1)
	go func() { result <- br.Accept(b.ctx, d.self, snapshot) }()
	publicationWait(t, committed)
	closed := make(chan struct{})
	go func() { br.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close skipped admitted refresh publication")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	publicationWait(t, closed)
	if e = <-result; code(e) != "backend_stopping" {
		t.Fatal(e)
	}
	b.s.fault = nil
	if _, e = br.RouteAccess(b.ctx, root.ID, b.self.Host, c.self.Host, RouteMessage); code(e) != "backend_stopping" {
		t.Fatal(e)
	}
	records, e := b.s.Read(b.ctx)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, o := range records.Observations {
		found = found || o.SnapshotHash == snapshot.Hash
	}
	if !found {
		t.Fatal("committed display-only observation lost")
	}
	br.mu.Lock()
	defer br.mu.Unlock()
	if len(br.active) != 0 {
		t.Fatal("late publication reactivated closed runtime")
	}
}

func TestFinalizeWaitsForSerializedLatestRefresh(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	b, join, admission := joinedCandidate(t, d, root)
	completeCandidate(t, d, root, b, admission)
	br := NewRuntime(b.s)
	defer br.Close()
	refresh := func() Snapshot {
		t.Helper()
		q, e := br.Begin(b.ctx, root.ID)
		if e != nil {
			t.Fatal(e)
		}
		s, e := d.s.Issue(d.ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	if e := br.Accept(b.ctx, d.self, refresh()); e != nil {
		t.Fatal(e)
	}
	snapshot := refresh()
	join, _, e := b.s.Lookup(b.ctx, root.ID, join.Key)
	if e != nil {
		t.Fatal(e)
	}
	committed, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	b.s.fault = func(stage string) error {
		if stage == "after_commit" {
			once.Do(func() { close(committed); <-release })
		}
		return nil
	}
	accepted := make(chan error, 1)
	go func() { accepted <- br.Accept(b.ctx, d.self, snapshot) }()
	publicationWait(t, committed)
	parent := newQueuedContext()
	finalized := make(chan error, 1)
	go func() { _, e := br.Finalize(parent, root.ID, join.Key, join.Revision); finalized <- e }()
	publicationWait(t, parent.entered)
	close(release)
	if e := <-accepted; e != nil {
		t.Fatal(e)
	}
	if e := <-finalized; e != nil {
		t.Fatal("finalize mixed older cache with refreshed SQL:", e)
	}
	b.s.fault = nil
}

func TestRejectedRefreshKeepsPriorCurrentMembership(t *testing.T) {
	d, root, b, c, _, br, _ := routingFixtures(t)
	q, e := br.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	b.s.fault = func(stage string) error {
		if stage == "before_commit" {
			return errors.New("rollback before commit")
		}
		return nil
	}
	e = br.Accept(b.ctx, d.self, snapshot)
	b.s.fault = nil
	if e == nil {
		t.Fatal("fault did not abort refresh")
	}
	if _, e = br.Check(b.ctx, root.ID, c.self.Host); e != nil {
		t.Fatal("rolled-back refresh removed still-current membership", e)
	}
}

func TestDirectoryIssueWitnessLostAcknowledgementStaysDisplayOnly(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	q := RefreshRequest{Source: rootPrincipal(root), Nonce: hash("directory-issue")}
	issue := func() (Snapshot, error) {
		return d.s.IssueChecked(d.ctx, q, func(_ context.Context, _ *sql.Tx, a DirectoryIssue) (DirectoryDependent, error) {
			return DirectoryDependent{Hash: hash(q.Nonce), Issued: a.Snapshot.Issued, ValidUntil: a.Snapshot.ValidUntil}, nil
		})
	}
	if _, e := issue(); e != nil {
		t.Fatal(e)
	}
	d.s.mu.Lock()
	first := d.s.directoryGraphs[root.ID]
	d.s.mu.Unlock()
	if first.hash == "" {
		t.Fatal("successful issue lacked live witness")
	}
	q.Nonce = hash("lost-directory-issue")
	d.s.fault = func(stage string) error {
		if stage == "after_commit" {
			return errors.New("lost directory ACK")
		}
		return nil
	}
	_, e := issue()
	d.s.fault = nil
	if code(e) != "workspace_outcome_unknown" {
		t.Fatal(e)
	}
	d.s.mu.Lock()
	lost := d.s.directoryGraphs[root.ID]
	d.s.mu.Unlock()
	if lost.hash != "" {
		t.Fatal("unacknowledged issue activated an observation")
	}
	if _, e = issue(); e != nil {
		t.Fatal(e)
	}
	d.s.mu.Lock()
	latest := d.s.directoryGraphs[root.ID]
	d.s.mu.Unlock()
	if latest.hash == "" || latest.hash == first.hash || latest.count <= first.count {
		t.Fatal("new successful issue did not publish exact latest witness")
	}
}
