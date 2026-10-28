package kubeprovision

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"time"
)

// Legacy readers accept a closed phase set, but ignore unknown JSON fields.
// This on-disk phase makes those readers refuse BEFORE any helper/effect. Only
// this guarded reader projects the retained target phase for SDK observation.
const sourceExecPhase Phase = "source_exec_pending"

var ErrSourceOwnerUnavailable = errors.New("original Kubernetes source owner is active or unresolved")

type sourceDiskRecord struct {
	Record
	TargetPhase Phase  `json:"source_target_phase,omitempty"`
	SourceOwner string `json:"source_owner,omitempty"`
}

type sourceExecGuard struct {
	journal               FileJournal
	key, owner, plan, pod string
	revision              uint64
	mu                    sync.Mutex
	finished              bool
}

func (j FileJournal) beginSourceExec(ctx context.Context, key, plan, pod string) (*sourceExecGuard, error) {
	return j.beginSourceOwner(ctx, key, plan, pod, 0, false)
}

// Preparation binds the target snapshot before any factory allocation or
// caller read. A concurrent target attempt or even a same-value revision
// change must be inspected again, not discovered only after reading payload.
func (j FileJournal) beginSourcePreparation(ctx context.Context, key, plan, pod string, revision uint64) (*sourceExecGuard, error) {
	if revision == 0 {
		return nil, ErrJournalConflict
	}
	return j.beginSourceOwner(ctx, key, plan, pod, revision, true)
}

func (j FileJournal) beginSourceOwner(ctx context.Context, key, plan, pod string, revision uint64, preparation bool) (*sourceExecGuard, error) {
	var result *sourceExecGuard
	err := j.withLock(ctx, func(dirfd int) error {
		d, ok, err := j.readDiskLocked(dirfd, key)
		if err != nil {
			return err
		}
		if !ok || d.Phase != PhaseCreated || d.Pod == nil || d.Pod.UID != pod || d.Plan.SHA256 != plan {
			return ErrIdentity
		}
		if d.SourceCleanupUnknown {
			return ErrSourceOwnerUnavailable
		}
		if preparation && (d.Revision != revision || d.BinaryPhase != "") {
			return ErrJournalConflict
		}
		var nonce [32]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		d.SourceOwner = hex.EncodeToString(nonce[:])
		d.TargetPhase = d.Phase
		d.SourceCleanupUnknown = true
		if err := j.writeDiskLocked(dirfd, d); err != nil {
			return err
		}
		result = &sourceExecGuard{journal: j, key: key, owner: d.SourceOwner, plan: plan, pod: pod, revision: d.Revision}
		return nil
	})
	return result, err
}

func (g *sourceExecGuard) currentLocked(ctx context.Context) error {
	if g.finished || ctx == nil || ctx.Err() != nil {
		return ErrSourceOwnerUnavailable
	}
	return g.journal.withLock(ctx, func(dirfd int) error {
		d, ok, err := g.journal.readDiskLocked(dirfd, g.key)
		if err != nil {
			return err
		}
		if !ok || !d.SourceCleanupUnknown || d.SourceOwner != g.owner || d.Plan.SHA256 != g.plan || d.Pod == nil || d.Pod.UID != g.pod || d.Revision != g.revision {
			return ErrSourceOwnerUnavailable
		}
		return nil
	})
}

func (g *sourceExecGuard) current(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.currentLocked(ctx)
}

// advance is available only through this captured original, never public
// Update. The current nonce is checked first; while g.mu is held this owner
// cannot finish/rotate. Update's exact revision CAS and its private on-disk
// envelope preserve that nonce across the target change. A refused/unknown
// write never advances the cached revision or permits later positive finish.
func (g *sourceExecGuard) advance(ctx context.Context, journal Journal, prior Record, change func(*Record) error) (Record, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if prior.Plan.Review.Key != g.key || prior.Plan.SHA256 != g.plan || prior.Pod == nil || prior.Pod.UID != g.pod || prior.Revision != g.revision {
		return prior, ErrSourceOwnerUnavailable
	}
	if err := g.currentLocked(ctx); err != nil {
		return prior, err
	}
	next, err := journal.Update(ctx, g.key, g.revision, func(next *Record) error {
		if !next.SourceCleanupUnknown || next.Plan.SHA256 != g.plan || next.Pod == nil || next.Pod.UID != g.pod || next.Revision != g.revision {
			return ErrSourceOwnerUnavailable
		}
		return change(next)
	})
	if err == nil {
		g.revision = next.Revision
	}
	return next, err
}

// Source ownership has its own nonce/CAS; target Revision is unchanged. That
// keeps the provider's existing remote-effect CAS intact. No public Update can
// alter this source bit. Unknown closure has no adoption/recovery constructor.
func (g *sourceExecGuard) finish(unknown bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.finished {
		return ErrHelperCleanupUnknown
	}
	g.finished = true
	if unknown {
		return ErrHelperCleanupUnknown
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return g.journal.withLock(ctx, func(dirfd int) error {
		d, ok, err := g.journal.readDiskLocked(dirfd, g.key)
		if err != nil {
			return err
		}
		if !ok || !d.SourceCleanupUnknown || d.SourceOwner != g.owner || d.Plan.SHA256 != g.plan || d.Pod == nil || d.Pod.UID != g.pod || d.Revision != g.revision {
			return ErrHelperCleanupUnknown
		}
		d.SourceCleanupUnknown = false
		d.SourceOwner = ""
		d.TargetPhase = ""
		return g.journal.writeDiskLocked(dirfd, d)
	})
}

type sourceExecJournal interface {
	beginSourceExec(context.Context, string, string, string) (*sourceExecGuard, error)
}

type sourcePreparationJournal interface {
	beginSourcePreparation(context.Context, string, string, string, uint64) (*sourceExecGuard, error)
}

// Retain only fixed source-ownership classes through the remote-output fence.
// Credential/API/script errors continue to collapse into the generic outcome.
func unknownExecResult(err error) error {
	if errors.Is(err, ErrHelperCleanupUnknown) {
		return errors.Join(ErrUnknown, ErrHelperCleanupUnknown)
	}
	if errors.Is(err, ErrSourceOwnerUnavailable) {
		return errors.Join(ErrUnknown, ErrSourceOwnerUnavailable)
	}
	return ErrUnknown
}

func unknownExecRecord(r Record, err error) (Record, error) {
	if errors.Is(err, ErrHelperCleanupUnknown) || errors.Is(err, ErrSourceOwnerUnavailable) {
		r.SourceCleanupUnknown = true
	}
	return r, unknownExecResult(err)
}

// execWithSource records an original source owner before a fixed stream.
// Transports must join source work before returning, or return the typed
// cleanup error. Production uses OwnedExec; raw ClientGo streaming is refused.
func (p Provider) execWithSource(ctx context.Context, key string, transport ExecAPI, pod Pod, args []string, input io.Reader, out, stderr io.Writer) (err error) {
	return p.execWithSourceCleanup(ctx, key, transport, pod, args, input, out, stderr, nil)
}

func (p Provider) execWithSourceCleanup(ctx context.Context, key string, transport ExecAPI, pod Pod, args []string, input io.Reader, out, stderr io.Writer, cleanup func() error) (err error) {
	if _, raw := transport.(*ClientGo); raw {
		return errors.New("owned Kubernetes exec transport required")
	}
	if original, ok := transport.(*managedSourceOperation); ok {
		if original.guard == nil || original.guard.key != key || original.guard.pod != pod.UID {
			return ErrSourceOwnerUnavailable
		}
		// Preparation owns this same guard through the final payload close.
		// Do not mint a nested owner or retire it between its fixed calls.
		defer func() {
			if cleanup != nil && cleanup() != nil {
				err = ErrHelperCleanupUnknown
			}
		}()
		return original.Exec(ctx, pod, args, input, out, stderr)
	}
	j, ok := p.Journal.(sourceExecJournal)
	if !ok {
		return errors.New("durable original Kubernetes source ownership required")
	}
	r, exists, err := p.Journal.Read(ctx, key)
	if err != nil {
		return err
	}
	if !exists {
		return ErrUnknown
	}
	g, err := j.beginSourceExec(ctx, key, r.Plan.SHA256, pod.UID)
	if err != nil {
		return err
	}
	if managed, ok := transport.(*ManagedSourceExec); ok {
		transport = &managedSourceOperation{source: managed, guard: g}
	}
	returned := false
	defer func() {
		if cleanup != nil {
			if closeErr := cleanup(); closeErr != nil {
				err = ErrHelperCleanupUnknown
			}
		}
		if closeErr := g.finish(!returned || errors.Is(err, ErrHelperCleanupUnknown)); closeErr != nil {
			err = ErrHelperCleanupUnknown
		}
	}()
	err = transport.Exec(ctx, pod, args, input, out, stderr)
	returned = true
	return err
}

// Provider stage commits may not revise target evidence underneath another
// original helper. The check belongs inside the journal's locked CAS callback:
// an early observation alone cannot exclude a new source owner.
func (p Provider) updateSourceStage(ctx context.Context, key string, prior Record, change func(*Record) error) (Record, error) {
	updated, err := p.Journal.Update(ctx, key, prior.Revision, func(next *Record) error {
		if next.SourceCleanupUnknown {
			return ErrSourceOwnerUnavailable
		}
		return change(next)
	})
	if errors.Is(err, ErrSourceOwnerUnavailable) {
		prior.SourceCleanupUnknown = true
		return prior, err
	}
	return updated, err
}
