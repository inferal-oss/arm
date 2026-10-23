package workspace

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The barrier pauses a real acceptance after it consumed its exact ticket but
// before snapshot validation/Store.mu, without replacing SQL or runtime state.
type blockedRefreshContext struct {
	*queuedContext
	release chan struct{}
}

func newBlockedRefreshContext() *blockedRefreshContext {
	return &blockedRefreshContext{newQueuedContext(), make(chan struct{})}
}
func (c *blockedRefreshContext) AfterFunc(func()) func() bool {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return func() bool { return true }
}

func TestFailedAuthenticatedRefreshPrevalidationWithdrawsPreviousAuthority(t *testing.T) {
	for _, kind := range []string{"structure", "request-binding", "workspace-binding"} {
		t.Run(kind, func(t *testing.T) {
			d, b, root, r := activePair(t)
			t.Cleanup(r.Close)
			before, err := b.s.Read(b.ctx)
			if err != nil {
				t.Fatal(err)
			}
			q, err := r.Begin(b.ctx, root.ID)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := d.s.Issue(d.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			ctx := b.ctx
			switch kind {
			case "structure":
				snapshot.Hash = strings.Repeat("0", 64)
			case "request-binding":
				snapshot.Requester = d.self.Host
				snapshot.Hash = snapshotHash(snapshot)
			case "workspace-binding":
				snapshot.Workspace = strings.Repeat("0", 32)
				snapshot.Hash = snapshotHash(snapshot)
			}
			if err = r.Accept(ctx, d.self, snapshot); err == nil {
				t.Fatal("invalid refresh accepted")
			}
			if _, err = r.Check(b.ctx, root.ID, d.self.Host); code(err) != "membership_unknown" {
				t.Fatal("old authority survived failed authenticated refresh", err)
			}
			after, err := b.s.Read(b.ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("failed acceptance changed durable state", err)
			}
		})
	}
}

func TestUntrustedOrReplayedRefreshCannotConsumeOrWithdraw(t *testing.T) {
	d, b, root, r := activePair(t)
	t.Cleanup(r.Close)
	q, err := r.Begin(b.ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := d.s.Issue(d.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	wrong := b.self
	if err = r.Accept(b.ctx, wrong, snapshot); code(err) != "forbidden" {
		t.Fatal(err)
	}
	if _, err = r.RefreshStart(q); err != nil {
		t.Fatal("untrusted identity consumed legitimate ticket", err)
	}
	if _, err = r.Check(b.ctx, root.ID, d.self.Host); err != nil {
		t.Fatal("untrusted identity withdrew authority", err)
	}
	stale := snapshot
	stale.Nonce = strings.Repeat("0", 64)
	stale.Hash = snapshotHash(stale)
	if err = r.Accept(b.ctx, d.self, stale); code(err) != "stale_refresh" {
		t.Fatal(err)
	}
	if _, err = r.RefreshStart(q); err != nil {
		t.Fatal("stale nonce consumed unrelated ticket", err)
	}
	if err = r.Accept(b.ctx, d.self, snapshot); err != nil {
		t.Fatal(err)
	}
	if err = r.Accept(b.ctx, d.self, snapshot); code(err) != "stale_refresh" {
		t.Fatal("replayed nonce accepted", err)
	}
	if _, err = r.Check(b.ctx, root.ID, d.self.Host); err != nil {
		t.Fatal("replayed nonce withdrew newer authority", err)
	}
}

func TestDelayedFailedRefreshCannotWithdrawNewerPublication(t *testing.T) {
	for _, kind := range []string{"structure", "request-binding", "dependent"} {
		t.Run(kind, func(t *testing.T) {
			d, b, root, r := activePair(t)
			t.Cleanup(r.Close)
			q, err := r.Begin(b.ctx, root.ID)
			if err != nil {
				t.Fatal(err)
			}
			older, err := d.s.Issue(d.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			var dependent func(context.Context, *sql.Tx, DirectoryAcceptance) (DirectoryDependent, error)
			switch kind {
			case "structure":
				older.Hash = strings.Repeat("0", 64)
			case "request-binding":
				older.Requester = d.self.Host
				older.Hash = snapshotHash(older)
			case "dependent":
				dependent = func(context.Context, *sql.Tx, DirectoryAcceptance) (DirectoryDependent, error) {
					return DirectoryDependent{}, errors.New("older graph rejected")
				}
			}
			parent := newBlockedRefreshContext()
			result := make(chan error, 1)
			go func() { result <- r.AcceptChecked(parent, d.self, older, dependent) }()
			publicationWait(t, parent.entered)
			// The old acceptance has consumed its ticket; a newer request can finish
			// while that old authenticated response is still being validated.
			q, err = r.Begin(b.ctx, root.ID)
			if err != nil {
				close(parent.release)
				t.Fatal(err)
			}
			newer, err := d.s.Issue(d.ctx, q)
			if err != nil {
				close(parent.release)
				t.Fatal(err)
			}
			if err = r.Accept(b.ctx, d.self, newer); err != nil {
				close(parent.release)
				t.Fatal(err)
			}
			close(parent.release)
			if err = <-result; err == nil {
				t.Fatal("older invalid acceptance succeeded")
			}
			if _, err = r.Check(b.ctx, root.ID, d.self.Host); err != nil {
				t.Fatal("delayed failure withdrew newer authority", err)
			}
			r.mu.Lock()
			published := r.active[root.ID]
			r.mu.Unlock()
			if published.publication != newer.Nonce || published.hash != newer.Hash {
				t.Fatal("wrong refresh remained active", published)
			}
			records, err := b.s.Read(b.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(records.Observations) != 1 || records.Observations[0].SnapshotHash != newer.Hash {
				t.Fatal("failed old transaction replaced newer SQL observation")
			}
		})
	}
}

func TestRefreshFailureWithdrawalSerializesWithLedgerReader(t *testing.T) {
	d, b, root, r := activePair(t)
	t.Cleanup(r.Close)
	q, err := r.Begin(b.ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := d.s.Issue(d.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Hash = strings.Repeat("0", 64)
	entered, release := make(chan struct{}), make(chan struct{})
	read := make(chan error, 1)
	go func() {
		read <- b.s.readPublished(b.ctx, func(Records) error {
			r.mu.Lock()
			old := r.active[root.ID].publication
			r.mu.Unlock()
			if old == "" {
				return errors.New("reader did not observe prior authority")
			}
			close(entered)
			<-release
			return nil
		})
	}()
	publicationWait(t, entered)
	parent := newQueuedContext()
	result := make(chan error, 1)
	go func() { result <- r.Accept(parent, d.self, snapshot) }()
	publicationWait(t, parent.entered)
	select {
	case err = <-result:
		close(release)
		t.Fatal("failure returned before serialized reader released", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err = <-read; err != nil {
		t.Fatal(err)
	}
	if err = <-result; err == nil {
		t.Fatal("invalid response succeeded")
	}
	if _, err = r.Check(b.ctx, root.ID, d.self.Host); code(err) != "membership_unknown" {
		t.Fatal("failed acceptance retained authority", err)
	}
}

func TestRejectedDependentRefreshWithdrawsBeforeQueuedLedgerReader(t *testing.T) {
	d, b, root, r := activePair(t)
	t.Cleanup(r.Close)
	q, err := r.Begin(b.ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := d.s.Issue(d.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	sentinel := errors.New("authenticated graph rejected")
	go func() {
		result <- r.AcceptChecked(b.ctx, d.self, snapshot, func(context.Context, *sql.Tx, DirectoryAcceptance) (DirectoryDependent, error) {
			close(entered)
			<-release
			return DirectoryDependent{}, sentinel
		})
	}()
	publicationWait(t, entered)
	readerStarted := make(chan struct{})
	read := make(chan error, 1)
	go func() {
		close(readerStarted)
		read <- b.s.readPublished(b.ctx, func(Records) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			if _, ok := r.active[root.ID]; ok {
				return errors.New("queued reader observed rejected old authority")
			}
			return nil
		})
	}()
	publicationWait(t, readerStarted)
	close(release)
	if err = <-result; !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err = <-read; err != nil {
		t.Fatal(err)
	}
}

func TestLocalRefreshRollbackDoesNotExtendPriorPublication(t *testing.T) {
	for _, stage := range []string{"before_validate", "before_commit"} {
		t.Run(stage, func(t *testing.T) {
			d, b, root, r := activePair(t)
			t.Cleanup(r.Close)
			r.mu.Lock()
			prior := r.active[root.ID]
			r.mu.Unlock()
			before, err := b.s.Read(b.ctx)
			if err != nil {
				t.Fatal(err)
			}
			q, err := r.Begin(b.ctx, root.ID)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := d.s.Issue(d.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			sentinel := errors.New("local known rollback")
			b.s.fault = func(at string) error {
				if at == stage {
					return sentinel
				}
				return nil
			}
			err = r.Accept(b.ctx, d.self, snapshot)
			b.s.fault = nil
			if !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			r.mu.Lock()
			after := r.active[root.ID]
			r.mu.Unlock()
			if after != prior {
				t.Fatal("known local rollback changed prior publication or its original deadline", prior, after)
			}
			if _, err = r.Check(b.ctx, root.ID, d.self.Host); err != nil {
				t.Fatal("local rollback removed still-valid prior authority", err)
			}
			records, err := b.s.Read(b.ctx)
			if err != nil || !reflect.DeepEqual(before, records) {
				t.Fatal("local rollback changed persisted observation", err)
			}
		})
	}
}

func TestCloseJoinsRejectedRefreshWithdrawal(t *testing.T) {
	d, b, root, r := activePair(t)
	t.Cleanup(r.Close)
	q, err := r.Begin(b.ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := d.s.Issue(d.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Hash = strings.Repeat("0", 64)
	b.s.mu.Lock()
	parent := newQueuedContext()
	result := make(chan error, 1)
	go func() { result <- r.Accept(parent, d.self, snapshot) }()
	publicationWait(t, parent.entered)
	closed := make(chan struct{})
	go func() { r.Close(); close(closed) }()
	select {
	case <-closed:
		b.s.mu.Unlock()
		t.Fatal("Close failed to join admitted failure cleanup")
	case <-time.After(20 * time.Millisecond):
	}
	b.s.mu.Unlock()
	publicationWait(t, closed)
	if err = <-result; err == nil {
		t.Fatal("invalid response accepted")
	}
	r.mu.Lock()
	remaining := len(r.active)
	r.mu.Unlock()
	if remaining != 0 {
		t.Fatal("closed runtime retained authority")
	}
}
