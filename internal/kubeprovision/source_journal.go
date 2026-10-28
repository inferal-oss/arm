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
