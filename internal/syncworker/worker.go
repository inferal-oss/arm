// Package syncworker contains the fixed, admitted sync execution state machine.
// The runner is exposed only through a concrete Manager-minted destination
// hold after exact admission; public server dispatch remains closed.
package syncworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net"
	"slices"
	"strings"
	"sync"

	"github.com/inferal-oss/arm/internal/rsyncengine"
	"github.com/inferal-oss/arm/internal/syncfs"
	"github.com/inferal-oss/arm/internal/syncreconcile"
	"github.com/inferal-oss/arm/internal/syncservice"
)

const FixedCommand = syncservice.FixedWorkerCommand

func FixedCommandSHA256() string {
	sum := sha256.Sum256([]byte(FixedCommand))
	return hex.EncodeToString(sum[:])
}

// admittedHold is supplied only by a Manager-owned worker adapter. Positive
// values in Intent do not implement this interface or authorize filesystem IO.
type admittedHold interface {
	syncservice.DecodeAdmission
	CheckIntent(context.Context, syncservice.Intent) error
	ChargeVolume(context.Context, string, int64) error
	DiskBytes() int64
}

type transferHandle interface {
	io.ReadWriteCloser
	// Abort closes only the original session carrier bound to this handle.
	Abort() error
	Join() error
}

// admittedSession is a one-use, authenticated SSH control/transfer owner.
// OpenTransfer binds a deterministic batch ID; Join waits for actual transport
// workers after Close, including error and cancellation paths.
type admittedSession interface {
	Control() io.ReadWriteCloser
	OpenTransfer(context.Context, batch) (transferHandle, error)
	Close() error
	Abort() error
	Join() error
}

type endpoint struct {
	Side         syncreconcile.Side
	Root         *syncfs.Root
	WorkParent   *syncfs.Root
	Names        syncfs.NamePolicy
	Metadata     syncfs.Metadata
	Ledger       *syncservice.Ledger
	Hold         admittedHold
	Session      admittedSession
	Prepared     *syncservice.Preparation
	PreparedSent bool
	Receipt      *syncservice.SideReceipt
	Workspace    string
	NativeCount  int
}

type batch struct {
	Source   syncreconcile.Side `json:"source"`
	Receiver syncreconcile.Side `json:"receiver"`
}

func batches(plan syncreconcile.Plan) []batch {
	var out []batch
	actions := plan.Actions()
	for _, receiver := range []syncreconcile.Side{syncreconcile.Local, syncreconcile.Remote} {
		for _, source := range []syncreconcile.Side{syncreconcile.Local, syncreconcile.Remote} {
			for _, a := range actions {
				if a.Side == receiver && a.Source == source && a.After != nil {
					out = append(out, batch{Source: source, Receiver: receiver})
					break
				}
			}
		}
	}
	return out
}

// batchWriteLimit is derived from the reviewed plan, never the peer's rsync
// file list or protocol statistics. Every reconstructed file byte, including
// a byte copied from an existing basis file, consumes this allowance.
func batchWriteLimit(plan syncreconcile.Plan, b batch) (int64, error) {
	var total int64
	for _, a := range plan.Actions() {
		if a.Source == b.Source && a.Side == b.Receiver && a.After != nil && a.After.Kind == syncreconcile.File {
			if err := add(&total, a.After.Size); err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}

func add(total *int64, v int64) error {
	if v < 0 || *total > math.MaxInt64-v {
		return errors.New("sync disk demand overflow")
	}
	*total += v
	return nil
}

// forecast charges the source manifest and a conservative receiver peak:
// retained preimage, basis copy, completed After, and rsync temporary After.
// Metadata/inode and control overhead are additional. Manager must map this
// demand to the actual selected filesystem and hold it for the worker life.
func forecast(intent syncservice.Intent, side syncreconcile.Side) (int64, error) {
	var total int64 = 1 << 20 // workspace, journal and control floor
	for _, a := range intent.Actions {
		if err := add(&total, 16<<10); err != nil {
			return 0, err
		}
		if a.After != nil && a.Source == side {
			if err := add(&total, a.After.Size); err != nil {
				return 0, err
			}
		}
		if a.Side == side {
			if a.Before != nil {
				for range 2 {
					if err := add(&total, a.Before.Size); err != nil {
						return 0, err
					}
				}
			}
			if a.After != nil {
				for range 2 {
					if err := add(&total, a.After.Size); err != nil {
						return 0, err
					}
				}
			}
		}
	}
	if total > 1<<40 {
		return 0, errors.New("sync operation exceeds native stage limit")
	}
	return total, nil
}

func (e *endpoint) check(ctx context.Context, intent syncservice.Intent, plan syncreconcile.Plan) error {
	if e == nil || e.Root == nil || e.WorkParent == nil || e.Ledger == nil || e.Hold == nil || e.Session == nil || e.Side != intent.Side || e.Ledger.Key != intent.Key || e.Ledger.Side != e.Side {
		return errors.New("incomplete admitted sync worker")
	}
	if intent.WorkerCommandSHA256 != FixedCommandSHA256() {
		return errors.New("sync worker command differs from fixed entry")
	}
	if err := e.Hold.CheckActive(ctx); err != nil {
		return err
	}
	if err := e.Hold.CheckIntent(ctx, intent); err != nil {
		return err
	}
	if err := syncfs.VerifyPlanRoot(plan, e.Side, e.Root); err != nil {
		return err
	}
	if err := e.WorkParent.Check(); err != nil {
		return err
	}
	return nil
}

func (e *endpoint) workspace(ctx context.Context, plan syncreconcile.Plan, bytes int64, role string) (*syncfs.Workspace, error) {
	w, err := syncfs.CreateWorkspace(e.Root, e.WorkParent, syncfs.Reservation{Key: e.Ledger.Key, PlanDigest: plan.Digest(), Generation: plan.Generation(), Side: e.Side, DiskBytes: bytes})
	if err != nil {
		return w, err
	}
	if err := e.Ledger.RecordWorkspace(ctx, role, e.Root, e.WorkParent, w); err != nil {
		return w, err
	}
	return w, nil
}

func (e *endpoint) materialize(ctx context.Context, plan syncreconcile.Plan, b batch, bytes int64) (*syncfs.Workspace, []syncfs.DirectoryMetadata, error) {
	w, err := e.workspace(ctx, plan, bytes, "source-"+batchID(b))
	if err != nil {
		return w, nil, err
	}
	dirs, _, err := syncfs.BuildSourceManifest(ctx, e.Root, w.Stage, plan, b.Source, b.Receiver, e.Names, e.Metadata, bytes)
	return w, dirs, err
}

func ownActions(plan syncreconcile.Plan, side syncreconcile.Side) int {
	n := 0
	for _, a := range plan.Actions() {
		if a.Side == side {
			n++
		}
	}
	return n
}

func (e *endpoint) publish(ctx context.Context, plan syncreconcile.Plan, work *syncfs.Workspace, dirs []syncfs.DirectoryMetadata) error {
	if err := e.Hold.CheckActive(ctx); err != nil {
		return err
	}
	if err := syncfs.FinalizeStageDirectories(ctx, e.Root, work.Stage, plan, e.Side, e.Metadata, dirs); err != nil {
		return err
	}
	proposal, _ := plan.ProposedBaseline()
	if err := e.Root.Check(); err != nil {
		return err
	}
	publisher, err := syncfs.NewBoundPublisher(e.Root, work, plan, e.Names, e.Metadata, proposal.Binding.Exclusions, e.Ledger)
	if err != nil {
		return err
	}
	receipts, err := publisher.ApplyAll(ctx)
	if err != nil {
		return err
	}
	if len(receipts) != ownActions(plan, e.Side) {
		return syncservice.ErrUnknown
	}
	for _, r := range receipts {
		if r.State != "applied" {
			return syncservice.ErrUnknown
		}
	}
	if err := e.Hold.CheckActive(ctx); err != nil {
		return err
	}
	if err := e.Root.Check(); err != nil {
		return err
	}
	e.NativeCount = len(receipts)
	e.Workspace = work.Path()
	return nil
}

// runLocalBatch uses the same protocol-27 engine for same-endpoint sources,
// without treating an unreviewed local file as a receiver stage authority.
func runLocalBatch(ctx context.Context, source, target string, metadata syncfs.Metadata, writeLimit int64) error {
	if writeLimit < 0 {
		return errors.New("negative local receiver write limit")
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	errorsFromReceiver := make(chan error, 1)
	go func() {
		_, err := rsyncengine.Run(ctx, b, rsyncengine.ServerReceiver, target, rsyncengine.Metadata{Symlinks: true, Permissions: metadata.Permissions, ModTimes: metadata.ModTimes, ReceiverWriteLimit: &writeLimit})
		errorsFromReceiver <- err
	}()
	_, senderErr := rsyncengine.Run(ctx, a, rsyncengine.ClientSender, source, rsyncengine.Metadata{Symlinks: true, Permissions: metadata.Permissions, ModTimes: metadata.ModTimes})
	if senderErr != nil {
		_ = a.Close()
		_ = b.Close()
	}
	receiverErr := <-errorsFromReceiver
	return errors.Join(senderErr, receiverErr)
}

// The engine closes its stream only for error/cancellation. A successful
// batch retains the control carrier, while an error must abort that original
// carrier immediately so both protocol workers can finish before their join.
type abortingTransferStream struct {
	transferHandle
	once sync.Once
	err  error
}

func (s *abortingTransferStream) Close() error {
	s.once.Do(func() { s.err = s.transferHandle.Abort() })
	return s.err
}

func runTransfer(ctx context.Context, handle transferHandle, role rsyncengine.Role, root string, metadata syncfs.Metadata, writeLimit *int64) error {
	if handle == nil {
		return errors.New("missing admitted transfer")
	}
	stream := &abortingTransferStream{transferHandle: handle}
	var err error
	if (role == rsyncengine.ServerReceiver || role == rsyncengine.ClientReceiver) && (writeLimit == nil || *writeLimit < 0) {
		err = errors.New("fixed worker receiver requires an explicit nonnegative write limit")
	} else {
		_, err = rsyncengine.Run(ctx, stream, role, root, rsyncengine.Metadata{Symlinks: true, Permissions: metadata.Permissions, ModTimes: metadata.ModTimes, ReceiverWriteLimit: writeLimit})
	}
	if err != nil {
		err = errors.Join(err, stream.Close())
	}
	closeErr := handle.Close()
	joinErr := handle.Join()
	return errors.Join(err, closeErr, joinErr)
}

func batchID(b batch) string { return string(b.Source) + "-to-" + string(b.Receiver) }

func sortedDirs(in []syncfs.DirectoryMetadata) []syncfs.DirectoryMetadata {
	out := slices.Clone(in)
	slices.SortFunc(out, func(a, b syncfs.DirectoryMetadata) int {
		return strings.Compare(a.Path, b.Path)
	})
	return out
}
