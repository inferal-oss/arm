package syncworker

import (
	"context"
	"errors"

	"getarm.org/internal/manager"
	"getarm.org/internal/sshservice"
	"getarm.org/internal/syncfs"
	"getarm.org/internal/syncreconcile"
	"golang.org/x/crypto/ssh"
)

// RunDestination executes the fixed worker on the exact in-process Manager
// hold minted after destination SSH and sync admission. The selected roots,
// ledger, resource promise and channel remain tied to that original hold.
// It consumes the one-shot launch; Wait/JoinTransport own actual completion.
func RunDestination(hold *manager.SyncWorkerHold) error {
	if hold == nil {
		return errors.New("admitted destination worker and SSH channel required")
	}
	return hold.Start(func(ctx context.Context, stream ssh.Channel) error {
		closeOriginal, err := sshservice.OriginalTransportCloser(ctx, stream)
		if err != nil {
			return err
		}
		if err := hold.CheckActive(ctx); err != nil {
			return err
		}
		prepared := hold.Preparation()
		if prepared == nil {
			return errors.New("destination worker requires charged pre-exec preparation")
		}
		root, parent := hold.Root(), hold.WorkParent()
		if root == nil || parent == nil {
			return errors.New("destination roots unavailable")
		}
		names, err := syncfs.DetectNamePolicy(root, parent)
		if err != nil {
			return err
		}
		session, err := newOwnedSingleStreamSession(stream, func() error { return nil }, closeOriginal)
		if err != nil {
			return err
		}
		e := &endpoint{Side: syncreconcile.Remote, Root: root, WorkParent: parent, Names: names, Metadata: syncfs.Metadata{}, Ledger: hold.Ledger(), Hold: hold, Session: session, Prepared: prepared}
		return runEndpoint(ctx, e)
	})
}
