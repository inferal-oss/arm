package syncworker

import (
	"context"
	"errors"
	"fmt"

	"github.com/inferal-oss/arm/internal/manager"
	"github.com/inferal-oss/arm/internal/syncfs"
	"github.com/inferal-oss/arm/internal/syncreconcile"
	"github.com/inferal-oss/arm/internal/syncservice"
	"golang.org/x/crypto/ssh"
)

// RunSource consumes the one-use source hold. The original authenticated SSH
// opener is bound at source admission; this entry point cannot substitute a
// channel or lifetime. The first frame precedes exec acknowledgement because
// destination admission reads it before accepting the fixed command.
func RunSource(hold *manager.SyncSourceHold) error {
	if hold == nil {
		return errors.New("original source sync hold required")
	}
	return hold.Start(func(ctx context.Context, ch ssh.Channel, requests <-chan *ssh.Request) error {
		closeOriginal, err := hold.OriginalTransportCloser(ctx, ch)
		if err != nil {
			return err
		}
		if err := hold.CheckActive(ctx); err != nil {
			return err
		}
		root, parent := hold.Root(), hold.WorkParent()
		if root == nil || parent == nil {
			return errors.New("source sync roots unavailable")
		}
		names, err := syncfs.DetectNamePolicy(root, parent)
		if err != nil {
			return err
		}
		ledger := hold.Ledger()
		// Bound stored control before its first decode, including the
		// preparation sent before the exec acknowledgement.
		lookup, err := (&endpoint{Side: syncreconcile.Local, Ledger: ledger, Hold: hold}).boundedLookup(ctx)
		if err != nil || lookup.State != "reserved" || lookup.Intent.Side != syncreconcile.Local {
			return syncservice.ErrUnknown
		}
		if err := hold.CheckIntent(ctx, lookup.Intent); err != nil {
			return err
		}
		writeDone := make(chan error, 1)
		go func() {
			writeDone <- syncservice.WritePreparation(ch, syncservice.Preparation{Version: syncservice.Schema, Intent: lookup.Intent, Manifest: lookup.Manifest})
		}()
		accepted, requestErr := ch.SendRequest("exec", true, ssh.Marshal(struct{ Command string }{FixedCommand}))
		if requestErr != nil || !accepted {
			_ = closeOriginal()
		}
		writeErr := <-writeDone
		if err := errors.Join(requestErr, writeErr); err != nil {
			return err
		}
		if !accepted {
			return errors.New("destination refused fixed sync worker")
		}
		waitExit := func() error {
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case req, ok := <-requests:
					if !ok {
						return errors.New("sync SSH exit status missing")
					}
					if req.Type != "exit-status" {
						continue
					}
					var status struct{ Status uint32 }
					if ssh.Unmarshal(req.Payload, &status) != nil || status.Status != 0 {
						return fmt.Errorf("destination sync worker exit %d", status.Status)
					}
					return nil
				}
			}
		}
		session, err := newOwnedSingleStreamSession(ch, waitExit, closeOriginal)
		if err != nil {
			return err
		}
		e := &endpoint{Side: syncreconcile.Local, Root: root, WorkParent: parent, Names: names, Metadata: syncfs.Metadata{}, Ledger: ledger, Hold: hold, Session: session, PreparedSent: true}
		return runEndpoint(ctx, e)
	})
}
