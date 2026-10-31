package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/storage"
	"golang.org/x/sys/unix"
)

// ForwardUnixBound is the private original native socket owner. It is issued
// only after the bound row and event have a positive durable acknowledgement.
// No public route or accept worker is installed by this source cut.
type ForwardUnixBound struct {
	pair                       *ForwardUnixPair
	directory                  *storage.SourceDirectory
	listener                   *net.UnixListener
	socket                     model.FileIdentity
	joined                     <-chan struct{}
	stopWatch                  chan struct{}
	watchDone                  chan struct{}
	stopOnce                   sync.Once
	closeOnce                  sync.Once
	closeErr                   error
	dirCloseOnce               sync.Once
	dirCloseErr                error
	dirCloseAttempted          bool
	nativeMu                   sync.Mutex
	acceptMu                   sync.Mutex // one original accept syscall at a time
	admissionStopped           atomic.Bool
	acceptedMu                 sync.Mutex
	accepted                   map[*net.UnixConn]*forwardUnixAcceptedClose
	beforeAcceptedRegisterTest func() // fixed barrier for close/accept join ordering
	beforeNativeCleanupTest    func() // fixed test seam while cleanup owns nativeMu
	finishOnce                 sync.Once
	finishErr                  error
}

func (b *ForwardUnixBound) closeOriginal() error {
	if b == nil || b.listener == nil {
		return nil
	}
	b.admissionStopped.Store(true)
	b.closeOnce.Do(func() { b.closeErr = b.listener.Close() })
	return b.closeErr
}

func (b *ForwardUnixBound) closeDirectory() error {
	if b == nil || b.directory == nil {
		return nil
	}
	b.dirCloseOnce.Do(func() {
		b.dirCloseAttempted = true
		b.dirCloseErr = b.directory.Close()
	})
	return b.dirCloseErr
}

func (b *ForwardUnixBound) stopWatcher() {
	if b.stopWatch == nil {
		return
	}
	b.stopOnce.Do(func() { close(b.stopWatch) })
	<-b.watchDone
}

func (b *ForwardUnixBound) fault(err error) error {
	if b == nil {
		return err
	}
	b.nativeMu.Lock()
	defer b.nativeMu.Unlock()
	return b.faultLocked(err)
}

// faultLocked serializes retirement with original unlink/terminal work.
func (b *ForwardUnixBound) faultLocked(err error) error {
	if b != nil {
		err = errors.Join(err, b.closeOriginal(), b.closeDirectory())
		if b.pair != nil && b.pair.child != nil {
			reason := "original forward Unix native cleanup uncertain"
			if markErr := b.pair.child.MarkCleanupUncertain(reason); markErr != nil {
				m := b.pair.child.parent.manager
				m.mu.Lock()
				if b.pair.child.parent.unixFault == "" {
					b.pair.child.parent.unixFault = reason
				}
				m.mu.Unlock()
				b.pair.child.cancel()
				b.pair.child.parent.CloseOriginal()
				err = errors.Join(err, markErr)
			}
		}
	}
	return err
}

func (b *ForwardUnixBound) retireOnShutdown() error {
	if b == nil || b.pair == nil {
		return nil
	}
	b.nativeMu.Lock()
	defer b.nativeMu.Unlock()
	m := b.pair.child.parent.manager
	m.mu.Lock()
	terminal := b.pair.data.Phase == "terminal"
	m.mu.Unlock()
	if terminal {
		return nil
	}
	return b.faultLocked(model.Err("forward_unix_cleanup_uncertain", "Manager closed with unfinished original Unix socket"))
}

// transitionLocked checks the complete original source/storage tuple and the
// exact canonical pair row, then records one phase transition and event. The
// caller holds Manager.mu; no native filesystem or socket operation runs here.
func (p *ForwardUnixPair) transitionLocked(ctx context.Context, from string, next forwardUnixPairData, kind string, detach bool) error {
	h, m := p.child.parent, p.child.parent.manager
	if p.child.pair != p || h.unixChild != p.child || m.forwardListeners[h.id] != h || p.data.Phase != from || next.SourceID != p.source || next.StorageID != p.storage || next.DiskBytes != forwardUnixDiskPromise || !validForwardUnixPairPhase(next) {
		return forwardUnixPairUnavailable()
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := readForwardUnixPairRows(ctx, tx)
	if err != nil {
		return err
	}
	var original string
	for _, row := range rows {
		if row.data.SourceID == p.source {
			if !reflect.DeepEqual(row.data, p.data) {
				return forwardUnixPairUnavailable()
			}
			original = row.encoded
		}
	}
	if original == "" {
		return forwardUnixPairUnavailable()
	}
	source, sourceRaw, sourceKey, err := readForwardUnixPairLease(ctx, tx, p.source)
	if err != nil {
		return err
	}
	storageLease, _, storageKey, err := readForwardUnixPairLease(ctx, tx, p.storage)
	if err != nil {
		return err
	}
	if sourceKey != p.data.SourceKey || source.ID != h.id || source.Request.Kind != forwardListenerKind || source.Request.StorageLease != p.storage || source.Request.ForwardSource == nil || !reflect.DeepEqual(*source.Request.ForwardSource, p.data.SourceBinding) || !reflect.DeepEqual(source.Request, h.request) || source.Host != h.result.Host || source.Owner != h.result.Owner || source.Revision != h.result.Revision || source.Revision < p.data.SourceRevision || source.Clock == nil || *source.Clock != p.data.SourceClock || !consuming(source.State) || storageKey != p.data.StorageKey || storageLease.ID != p.storage || storageLease.Request.Kind != "storage" || storageLease.Request.Project != "mesh-forward" || storageLease.Request.Resources != (model.Resources{Disk: forwardUnixDiskPromise}) || storageLease.Request.Reason != "Forward Unix source directory for "+p.source || storageLease.Host != source.Host || storageLease.Owner != source.Owner || storageLease.Clock == nil || *storageLease.Clock != p.data.StorageClock || storageLease.Revision < p.data.StorageRevision || storageLease.Artifact == nil || storageLease.Artifact.Identity == nil || *storageLease.Artifact.Identity != p.data.StorageArtifact || storageLease.Artifact.Name != p.storage || storageLease.Artifact.Path != storageLease.ManagedDir || storageLease.Artifact.Phase != "owned" || storageLease.State != "active" && storageLease.State != "needs_attention" || m.storage == nil || m.storage.Identity() != p.data.StorageRoot {
		return forwardUnixPairUnavailable()
	}
	encoded, err := json.Marshal(next)
	if err != nil || len(encoded) > 4096 {
		return forwardUnixPairUnavailable()
	}
	updated, err := tx.ExecContext(ctx, `UPDATE forward_unix_pairs SET phase=?,data=? WHERE source_id=? AND storage_id=? AND phase=? AND source_key=? AND storage_key=? AND socket_name=? AND disk_bytes=? AND data=?`, next.Phase, string(encoded), p.source, p.storage, from, p.data.SourceKey, p.data.StorageKey, p.data.SocketName, p.data.DiskBytes, original)
	if err != nil {
		return err
	}
	n, err := updated.RowsAffected()
	if err != nil || n != 1 {
		return forwardUnixPairUnavailable()
	}
	var detached model.Lease
	if detach {
		detached = source
		detached.Request.StorageLease = ""
		detached.Revision++
		raw, err := json.Marshal(detached)
		if err != nil {
			return err
		}
		updated, err = tx.ExecContext(ctx, `UPDATE leases SET data=? WHERE id=? AND request_key=? AND data=?`, string(raw), p.source, sourceKey, sourceRaw)
		if err != nil {
			return err
		}
		n, err = updated.RowsAffected()
		if err != nil || n != 1 {
			return forwardUnixPairUnavailable()
		}
	}
	if err = event(tx, kind, p.source, next); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	p.data = next
	if detach {
		h.request, h.result = detached.Request, detached
	}
	return nil
}

func forwardUnixListenerIdentity(listener *net.UnixListener, path string) error {
	raw, err := listener.SyscallConn()
	if err != nil {
		return err
	}
	var inner error
	if err = raw.Control(func(fd uintptr) {
		var st unix.Stat_t
		if inner = unix.Fstat(int(fd), &st); inner != nil {
			return
		}
		flags, e := unix.FcntlInt(fd, unix.F_GETFD, 0)
		if e != nil {
			inner = e
			return
		}
		if flags&unix.FD_CLOEXEC == 0 || st.Mode&unix.S_IFMT != unix.S_IFSOCK {
			inner = fmt.Errorf("original Unix listener FD identity or CLOEXEC unavailable")
			return
		}
		address, e := unix.Getsockname(int(fd))
		if e != nil {
			inner = e
			return
		}
		local, ok := address.(*unix.SockaddrUnix)
		if !ok || local.Name != path {
			inner = fmt.Errorf("original Unix listener is bound to another path")
		}
	}); err != nil {
		return err
	}
	return inner
}

// readyForNativePhase runs after the durable phase ACK and all preceding
// directory IO. Its last read is the source's native elapsed lease clock;
// process-context cancellation is not the entitlement oracle.
func (p *ForwardUnixPair) readyForNativePhase(ctx context.Context, owner *ForwardUnixBound, provider *storage.Provider, workersJoined <-chan struct{}, phase string, constructing bool) error {
	c, h := p.child, p.child.parent
	m := h.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || p.shutdownRequested || p.binding != constructing || p.native != owner || p.data.Phase != phase || phase != "bind_intent" && phase != "bound" || !constructing && phase != "bound" || c.pair != p || c.released || h.unixChild != c || h.settled || h.unixFault != "" || h.ctx.Err() != nil || c.ctx.Err() != nil || m.forwardListeners[h.id] != h || m.storage != provider || provider.Identity() != p.data.StorageRoot {
		return forwardUnixPairUnavailable()
	}
	if _, err := h.remainingLocked(ctx); err != nil {
		return err
	}
	storageLease, err := m.get(p.storage)
	if err != nil || !forwardUnixDefaultRootLease(storageLease) {
		return forwardUnixPairUnavailable()
	}
	for _, joined := range []<-chan struct{}{workersJoined, c.joined, h.joined} {
		select {
		case <-joined:
			return forwardUnixPairUnavailable()
		default:
		}
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := readForwardUnixPairRows(ctx, tx)
	if err != nil {
		return err
	}
	matched := false
	for _, row := range rows {
		if row.data.SourceID == p.source {
			if matched || row.data.StorageID != p.storage || row.data.Phase != phase || !reflect.DeepEqual(row.data, p.data) {
				return forwardUnixPairUnavailable()
			}
			matched = true
		}
	}
	if !matched {
		return forwardUnixPairUnavailable()
	}
	source, _, sourceKey, err := readForwardUnixPairLease(ctx, tx, p.source)
	if err != nil {
		return err
	}
	lease, _, storageKey, err := readForwardUnixPairLease(ctx, tx, p.storage)
	if err != nil {
		return err
	}
	if sourceKey != p.data.SourceKey || source.ID != h.id || source.State != "active" || source.Revision != h.result.Revision || source.PID != 0 || source.Process != nil || source.Request.Kind != forwardListenerKind || source.Request.Key != h.key || source.Request.StorageLease != p.storage || source.Request.ForwardSource == nil || !reflect.DeepEqual(*source.Request.ForwardSource, p.data.SourceBinding) || !reflect.DeepEqual(source.Request, h.request) || source.Clock == nil || *source.Clock != p.data.SourceClock || h.result.Clock == nil || *source.Clock != *h.result.Clock || !reflect.DeepEqual(source.Expires, h.result.Expires) || !reflect.DeepEqual(source.Ports, h.result.Ports) || source.Host != h.result.Host || source.Owner != h.result.Owner || storageKey != p.data.StorageKey || lease.State != "active" || lease.ID != p.storage || lease.Request.Key != p.data.StorageKey || lease.Request.Kind != "storage" || lease.Request.Project != "mesh-forward" || lease.Request.Resources != (model.Resources{Disk: forwardUnixDiskPromise}) || lease.Request.Reason != "Forward Unix source directory for "+p.source || lease.Clock == nil || *lease.Clock != p.data.StorageClock || lease.Revision < p.data.StorageRevision || lease.Artifact == nil || lease.Artifact.Identity == nil || *lease.Artifact.Identity != p.data.StorageArtifact || lease.Artifact.Name != p.storage || lease.Artifact.Path != lease.ManagedDir || lease.Artifact.Phase != "owned" || lease.ManagedDir != provider.Path(p.storage) || lease.Host != source.Host || lease.Owner != source.Owner || lease.Clock.Boot != source.Clock.Boot || lease.Clock.End < source.Clock.End || lease.Clock.End-source.Clock.End < int64(forwardUnixCleanupWindow/2) {
		return forwardUnixPairUnavailable()
	}
	if err = tx.Rollback(); err != nil {
		return forwardUnixPairUnavailable()
	}
	if remaining, err := m.leaseRemaining(lease); err != nil || remaining <= 0 {
		return forwardUnixPairUnavailable()
	}
	if remaining, err := m.leaseRemaining(source); err != nil || remaining <= 0 {
		return forwardUnixPairUnavailable()
	}
	return nil
}

// BindOriginal makes exactly one private native attempt. The supplied channel
// must close only after the caller's actual accept/copy workers have joined.
func (p *ForwardUnixPair) BindOriginal(ctx context.Context, workersJoined <-chan struct{}) (*ForwardUnixBound, error) {
	if p == nil || p.child == nil || p.child.parent == nil || ctx == nil || ctx.Err() != nil || workersJoined == nil {
		return nil, forwardUnixPairUnavailable()
	}
	select {
	case <-workersJoined:
		return nil, forwardUnixPairUnavailable()
	default:
	}
	c, h := p.child, p.child.parent
	for _, joined := range []<-chan struct{}{c.joined, h.joined} {
		select {
		case <-joined:
			return nil, forwardUnixPairUnavailable()
		default:
		}
	}
	m := h.manager
	m.mu.Lock()
	if p.binding || p.aborted || p.native != nil || p.shutdownRequested || c.pair != p || c.released || c.ctx.Err() != nil || p.data.Phase != "planned" || h.unixFault != "" {
		m.mu.Unlock()
		return nil, forwardUnixPairUnavailable()
	}
	if _, err := h.remainingLocked(ctx); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if m.storage == nil {
		m.mu.Unlock()
		return nil, forwardUnixPairUnavailable()
	}
	lease, err := m.get(p.storage)
	if err != nil || !forwardUnixDefaultRootLease(lease) || lease.Artifact == nil || lease.Artifact.Identity == nil || *lease.Artifact.Identity != p.data.StorageArtifact || lease.ManagedDir != m.storage.Path(p.storage) || m.storage.Identity() != p.data.StorageRoot {
		m.mu.Unlock()
		return nil, forwardUnixPairUnavailable()
	}
	p.binding = true
	p.constructorDone = make(chan struct{})
	m.wg.Add(1)
	provider, artifact, path := m.storage, *lease.Artifact, filepath.Join(lease.ManagedDir, p.data.SocketName)
	m.mu.Unlock()
	defer func() { close(p.constructorDone); m.wg.Done() }()
	clearPlanned := func(cause error, dir *storage.SourceDirectory) (*ForwardUnixBound, error) {
		if dir != nil {
			if closeErr := dir.Close(); closeErr != nil {
				cause = errors.Join(cause, closeErr, c.MarkCleanupUncertain("original forward Unix source directory close uncertain"))
				return nil, cause
			}
		}
		m.mu.Lock()
		p.binding = false
		m.mu.Unlock()
		return nil, cause
	}
	// Conservative across Darwin/Linux sockaddr_un and independent of cwd.
	if len(path) > 100 || strings.IndexByte(path, 0) >= 0 {
		return clearPlanned(forwardUnixPairUnavailable(), nil)
	}
	dir, err := provider.HoldSourceDirectory(artifact)
	if err != nil {
		return clearPlanned(err, nil)
	}
	empty, err := dir.ProveEmptyForwardSocket(ctx, p.data.SocketName)
	if err != nil || empty.Identity != p.data.StorageArtifact || empty.Allocated > p.data.DiskBytes {
		return clearPlanned(errors.Join(err, forwardUnixPairUnavailable()), dir)
	}
	owner := &ForwardUnixBound{pair: p, directory: dir, joined: workersJoined}
	m.mu.Lock()
	if _, err = h.remainingLocked(ctx); err == nil && c.pair == p && p.binding && p.data.Phase == "planned" {
		p.native = owner // retain the original directory before the first write
		intent := p.data
		intent.Phase, intent.Revision = "bind_intent", 2
		intent.BindIntent = &forwardUnixBindIntent{Directory: empty.Identity, Allocated: empty.Allocated, At: time.Now().UTC()}
		err = p.transitionLocked(ctx, "planned", intent, "forward.unix_pair_bind_intent", false)
	} else if err == nil {
		err = forwardUnixPairUnavailable()
	}
	m.mu.Unlock()
	if err != nil {
		return nil, owner.fault(errors.Join(err, forwardUnixPairUnavailable()))
	}
	if err = dir.Check(); err != nil || ctx.Err() != nil || c.ctx.Err() != nil {
		return nil, owner.fault(errors.Join(err, ctx.Err(), c.ctx.Err()))
	}
	if p.beforeNativeBindTest != nil {
		p.beforeNativeBindTest()
	}
	if err = p.readyForNativePhase(ctx, owner, provider, workersJoined, "bind_intent", true); err != nil {
		return nil, owner.fault(err)
	}
	if ctx.Err() != nil || c.ctx.Err() != nil {
		return nil, owner.fault(errors.Join(ctx.Err(), c.ctx.Err()))
	}
	for _, joined := range []<-chan struct{}{workersJoined, c.joined, h.joined} {
		select {
		case <-joined:
			return nil, owner.fault(forwardUnixPairUnavailable())
		default:
		}
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, owner.fault(err)
	}
	listener.SetUnlinkOnClose(false)
	owner.listener = listener // first fallible operation after native return follows
	if p.afterNativeBindTest != nil {
		p.afterNativeBindTest()
	}
	if err = forwardUnixListenerIdentity(listener, path); err != nil {
		return nil, owner.fault(err)
	}
	observed, err := dir.ObserveForwardSocket(ctx, p.data.SocketName)
	if err != nil || observed.Directory != p.data.StorageArtifact || observed.Allocated > p.data.DiskBytes {
		return nil, owner.fault(errors.Join(err, forwardUnixPairUnavailable()))
	}
	owner.socket = observed.Socket
	m.mu.Lock()
	if _, err = h.remainingLocked(ctx); err == nil && p.native == owner && p.data.Phase == "bind_intent" {
		bound := p.data
		bound.Phase, bound.Revision = "bound", 3
		bound.Bound = &forwardUnixBoundProof{Socket: observed.Socket, Directory: observed.Directory, Allocated: observed.Allocated, At: time.Now().UTC()}
		err = p.transitionLocked(ctx, "bind_intent", bound, "forward.unix_pair_bound", false)
	} else if err == nil {
		err = forwardUnixPairUnavailable()
	}
	m.mu.Unlock()
	if err != nil {
		return nil, owner.fault(errors.Join(err, forwardUnixPairUnavailable()))
	}
	if p.afterBoundAckTest != nil {
		p.afterBoundAckTest()
	}
	if err = p.readyForNativePhase(ctx, owner, provider, workersJoined, "bound", true); err != nil {
		return nil, owner.fault(err)
	}
	if ctx.Err() != nil || c.ctx.Err() != nil {
		return nil, owner.fault(errors.Join(ctx.Err(), c.ctx.Err()))
	}
	for _, joined := range []<-chan struct{}{workersJoined, c.joined, h.joined} {
		select {
		case <-joined:
			return nil, owner.fault(forwardUnixPairUnavailable())
		default:
		}
	}
	m.mu.Lock()
	if p.native != owner || p.data.Phase != "bound" || !p.binding || p.shutdownRequested || m.closing || c.pair != p || h.unixChild != c {
		m.mu.Unlock()
		return nil, owner.fault(forwardUnixPairUnavailable())
	}
	p.binding = false
	m.mu.Unlock()
	owner.stopWatch, owner.watchDone = make(chan struct{}), make(chan struct{})
	m.wg.Go(func() {
		defer close(owner.watchDone)
		select {
		case <-c.ctx.Done():
			_ = owner.closeOriginal()
		case <-owner.stopWatch:
		}
	})
	return owner, nil
}

// CheckBeforeApplicationBytes is the final local ownership gate for a future
// source worker. It must run after the remote endpoint has positively completed
// pinned TLS and destination-grant admission, and before the worker reads or
// copies any local application byte. It does not certify that remote admission:
// the caller must obtain that proof independently. No public worker calls this
// method yet.
func (b *ForwardUnixBound) CheckBeforeApplicationBytes(ctx context.Context) error {
	if b == nil || b.pair == nil || b.directory == nil || b.listener == nil || ctx == nil || ctx.Err() != nil {
		return forwardUnixPairUnavailable()
	}
	b.nativeMu.Lock()
	defer b.nativeMu.Unlock()
	p := b.pair
	m := p.child.parent.manager
	m.mu.Lock()
	if p.native != b || p.data.Phase != "bound" || p.data.Bound == nil || m.storage == nil {
		m.mu.Unlock()
		return forwardUnixPairUnavailable()
	}
	if _, err := p.child.parent.remainingLocked(ctx); err != nil {
		m.mu.Unlock()
		return err
	}
	provider := m.storage
	lease, err := m.get(p.storage)
	if err != nil || !forwardUnixDefaultRootLease(lease) || lease.Artifact == nil || lease.ManagedDir != provider.Path(p.storage) {
		m.mu.Unlock()
		return forwardUnixPairUnavailable()
	}
	path := filepath.Join(lease.ManagedDir, p.data.SocketName)
	m.mu.Unlock()
	if err := b.directory.Check(); err != nil {
		return err
	}
	observed, err := b.directory.ObserveForwardSocket(ctx, p.data.SocketName)
	if err != nil || observed.Directory != p.data.StorageArtifact || observed.Socket != b.socket || observed.Allocated > p.data.DiskBytes {
		return errors.Join(err, forwardUnixPairUnavailable())
	}
	if err := forwardUnixListenerIdentity(b.listener, path); err != nil {
		return err
	}
	// The native elapsed clock and exact durable rows are sampled last, after
	// the potentially blocking directory and listener observations.
	return p.readyForNativePhase(ctx, b, provider, b.joined, "bound", false)
}

// CloseAfterJoin stops admission, waits for the supplied original workers,
// checks native close/unlink and records a distinct terminal proof. It may be
// called again after a successful terminal ACK to retry ordinary settlement.
func (b *ForwardUnixBound) CloseAfterJoin(ctx context.Context) error {
	if b == nil || b.pair == nil || ctx == nil || ctx.Err() != nil {
		return forwardUnixPairUnavailable()
	}
	b.finishOnce.Do(func() { b.finishErr = b.finishNative(ctx) })
	if b.finishErr != nil {
		return b.finishErr
	}
	return b.settleStorage(ctx)
}

func (b *ForwardUnixBound) finishNative(ctx context.Context) error {
	b.stopWatcher()
	if err := b.closeOriginal(); err != nil {
		return b.fault(err)
	}
	select {
	case <-b.joined:
	case <-ctx.Done():
		return b.fault(ctx.Err())
	}
	// The Manager's accept mutex is an in-flight token covering AcceptUnix,
	// peer verification, registration and rejected-FD close. Stop admission
	// first, then join that token outside nativeMu; a caller-supplied worker
	// join cannot certify an FD still between AcceptUnix and registration.
	b.acceptMu.Lock()
	defer b.acceptMu.Unlock()
	// A live accept may need nativeMu to reject and close its just-returned FD.
	// Waiting for the worker join while holding that mutex would deadlock it.
	b.nativeMu.Lock()
	defer b.nativeMu.Unlock()
	b.acceptedMu.Lock()
	remainingAccepted := len(b.accepted)
	b.acceptedMu.Unlock()
	if remainingAccepted != 0 {
		return b.faultLocked(forwardUnixPairUnavailable())
	}
	m := b.pair.child.parent.manager
	m.mu.Lock()
	sticky := b.pair.child.parent.unixFault
	m.mu.Unlock()
	if sticky != "" {
		return b.faultLocked(model.Err("forward_unix_cleanup_uncertain", sticky))
	}
	if b.beforeNativeCleanupTest != nil {
		b.beforeNativeCleanupTest()
	}
	observed, err := b.directory.ObserveForwardSocket(ctx, b.pair.data.SocketName)
	if err != nil || observed.Socket != b.socket {
		return b.faultLocked(errors.Join(err, forwardUnixPairUnavailable()))
	}
	empty, err := b.directory.UnlinkOriginalForwardSocket(ctx, b.pair.data.SocketName, b.socket)
	if err != nil || empty.Identity != b.pair.data.StorageArtifact || empty.Allocated > b.pair.data.DiskBytes {
		return b.faultLocked(errors.Join(err, forwardUnixPairUnavailable()))
	}
	if err = b.closeDirectory(); err != nil {
		return b.faultLocked(err)
	}
	p, m := b.pair, b.pair.child.parent.manager
	m.mu.Lock()
	if p.native != b || p.data.Phase != "bound" {
		m.mu.Unlock()
		return b.faultLocked(forwardUnixPairUnavailable())
	}
	terminal := p.data
	terminal.Phase, terminal.Revision = "terminal", 4
	terminal.NativeTerminal = &forwardUnixNativeTerminal{Socket: b.socket, Directory: empty.Identity, Allocated: empty.Allocated, At: time.Now().UTC()}
	err = p.transitionLocked(ctx, "bound", terminal, "forward.unix_pair_native_closed", true)
	m.mu.Unlock()
	if err != nil {
		return b.faultLocked(err)
	}
	return nil
}

func (b *ForwardUnixBound) settleStorage(ctx context.Context) error {
	p, m := b.pair, b.pair.child.parent.manager
	m.mu.Lock()
	if p.native != b || p.data.Phase != "terminal" || p.data.NativeTerminal == nil {
		m.mu.Unlock()
		return forwardUnixPairUnavailable()
	}
	_, err := m.end(p.storage, "cancelled")
	m.mu.Unlock()
	if err != nil {
		return err
	}
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		m.mu.Lock()
		lease, readErr := m.get(p.storage)
		if readErr == nil && lease.State == "terminal" && lease.Artifact != nil && lease.Artifact.Phase == "removed" && lease.Artifact.NamespaceRemoved {
			if p.child.pair == p && p.native == b {
				p.child.pair, p.native, p.binding = nil, nil, false
			}
			m.mu.Unlock()
			return nil
		}
		m.mu.Unlock()
		if readErr != nil {
			return readErr
		}
		if lease.State == "needs_attention" {
			// The ordinary cleanup worker has reported a retained obligation.
			// Its completed refusal cannot become success by waiting longer.
			// Keep the original pair registered for explicit reconciliation.
			return model.Err("forward_unix_cleanup_uncertain", "original forward Unix storage requires reconciliation; allocation remains charged")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
