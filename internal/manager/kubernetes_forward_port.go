package manager

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
	"getarm.org/internal/ports"
	"golang.org/x/sys/unix"
)

// This original socket has no descriptor/locator constructor and no exported
// loan API. The future fixed f helper must register every inherited loan before
// exposure; this unused constructor alone cannot authorize application streams.
type kubernetesForwardSocket struct {
	file        *os.File
	closing     atomic.Bool
	once        sync.Once
	err         error
	closeNative func(*os.File) error
}

func (s *kubernetesForwardSocket) closeOriginal() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() { s.closing.Store(true); s.err = s.closeNative(s.file) })
	return s.err
}
func (s *kubernetesForwardSocket) available() bool {
	return s != nil && s.file != nil && !s.closing.Load()
}

type kubernetesForwardSocketOps struct {
	open        func() (int, error)
	closeOnExec func(int) error
	close       func(*os.File) error
}

func openKubernetesForwardSocket(name string) (*kubernetesForwardSocket, error) {
	return openKubernetesForwardSocketWith(name, kubernetesForwardSocketOps{
		open: func() (int, error) { return unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0) },
		closeOnExec: func(fd int) error {
			_, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
			return err
		},
		close: func(f *os.File) error { return f.Close() },
	})
}

// Fork exclusion covers creation through checked CLOEXEC, including retirement
// of an unmarked original on failure. No Manager lock or worker join belongs
// inside this section. Return that retired owner too, so its first close error
// remains part of subsequent resource-settlement evidence.
func openKubernetesForwardSocketWith(name string, ops kubernetesForwardSocketOps) (*kubernetesForwardSocket, error) {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	fd, err := ops.open()
	if err != nil {
		return nil, err
	}
	socket := &kubernetesForwardSocket{file: os.NewFile(uintptr(fd), name), closeNative: ops.close}
	if err = ops.closeOnExec(fd); err != nil {
		return socket, errors.Join(err, socket.closeOriginal())
	}
	return socket, nil
}

func (k *KubernetesSourcePreparation) prepareForwardPort(budget string) error {
	m := k.manager
	m.mu.Lock()
	if k.ctx.Err() != nil || m.closing || m.draining {
		m.mu.Unlock()
		return model.Err("lease_inactive", "source cancelled before port admission")
	}
	request := k.scope.PortRequest(m.host, budget, m.portNamespace)
	var err error
	k.port, err = m.acquireWithSourcePermit(request, nil, false, nil, &kubernetesSourcePermit{manager: m, scope: k.scope, parent: true, port: true, request: request})
	k.portID = k.port.ID
	if k.port.ID != "" {
		m.kubernetesSources[k.port.ID] = k
	}
	state := k.port.State
	m.mu.Unlock()
	if err != nil {
		_ = k.transit.MarkCleanupUncertain("forward port admission acknowledgment unresolved")
		return err
	}
	if state != "provisioning" {
		return model.Err("resource_unavailable", "original forward port is not pending native bind")
	}
	namespace, err := ports.Namespace()
	if err != nil || namespace != request.Ports[0].Namespace {
		return model.Err("namespace_unavailable", "original forward network namespace changed")
	}
	if err = k.checkForwardAdmission(); err != nil {
		return err
	}
	// Own the original immediately after socket(), before every fallible step.
	socket, err := openKubernetesForwardSocket("kubernetes-forward-original")
	m.mu.Lock()
	k.socket = socket
	m.mu.Unlock()
	if err != nil {
		return err
	}
	fd := int(socket.file.Fd())
	if err = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 0); err != nil {
		return err
	}
	if m.sourceBeforePortBind != nil {
		m.sourceBeforePortBind()
	}
	if err = k.checkForwardAdmission(); err != nil {
		return err
	}
	if err = k.ctx.Err(); err != nil {
		return err
	}
	if err = unix.Bind(fd, &unix.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		return err
	}
	if err = k.checkForwardAdmission(); err != nil {
		return err
	}
	if err = k.ctx.Err(); err != nil {
		return err
	}
	if err = unix.Listen(fd, 8); err != nil {
		return err
	}
	address, err := unix.Getsockname(fd)
	if err != nil {
		return err
	}
	selected, ok := address.(*unix.SockaddrInet4)
	if !ok || selected.Port < 1 || selected.Port > 65535 || selected.Addr != [4]byte{127, 0, 0, 1} {
		return fmt.Errorf("original forward bind differs")
	}
	binding := model.PortBinding{PortClaim: request.Ports[0], State: "held", Detail: "Original Kubernetes forward socket retained by its native source owner"}
	binding.Port = selected.Port
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Ino != 0 {
		binding.SocketID = strconv.FormatUint(st.Ino, 10)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	actual, err := m.get(k.port.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, k.port) || k.ctx.Err() != nil || m.closing || m.draining || !m.leaseLive(actual) {
		return model.Err("lease_inactive", "forward port changed before binding acknowledgment")
	}
	updated := actual
	updated.Ports = []model.PortBinding{binding}
	updated.State = "active"
	updated.Revision++
	if err = m.put(updated, "kubernetes.forward_port_bound"); err != nil {
		return err
	}
	k.port = updated
	return nil
}

// Only the constructor uses this partial-tuple checker. No file or stream is
// exposed before the full source/storage/port attachment is acknowledged.
func (k *KubernetesSourcePreparation) checkForwardAdmission() error {
	select {
	case <-k.callerJoined:
		return model.Err("lease_inactive", "original forward caller already joined")
	default:
	}
	m := k.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if k.ctx.Err() != nil || m.closing || m.draining {
		return model.Err("lease_inactive", "original forward construction stopped")
	}
	parent, err := m.get(k.parent.ID)
	if err != nil {
		return err
	}
	port, err := m.get(k.port.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(parent, k.parent) || !reflect.DeepEqual(port, k.port) || parent.State != "active" || parent.Artifact == nil || parent.Artifact.Phase != "owned" || port.State != "provisioning" || !m.leaseLive(parent) || !m.leaseLive(port) {
		return model.Err("lease_inactive", "original forward parents changed")
	}
	select {
	case <-k.callerJoined:
		return model.Err("lease_inactive", "original forward caller joined before native bind")
	default:
	}
	if err = k.transit.checkActiveLocked(k.ctx); err != nil {
		return err
	}
	return k.ctx.Err()
}
func (k *KubernetesSourcePreparation) checkForwardPortLocked(source model.Lease, remaining time.Duration) error {
	select {
	case <-k.callerJoined:
		return model.Err("lease_inactive", "original forward caller already joined")
	default:
	}
	m := k.manager
	port, err := m.get(k.port.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(port, k.port) || port.State != "active" || !m.leaseLive(port) || !m.leaseCovers(port, remaining) || !k.socket.available() {
		return model.Err("lease_inactive", "original forward socket/parent unavailable")
	}
	if err = kubesource.ValidatePortPair(source, port); err != nil {
		return err
	}
	return k.transit.checkActiveLocked(k.ctx)
}

// End cancels the captured original before this SQL read. Generic port cleanup
// cannot close/probe it or infer positive closure while its caller still runs.
func (k *KubernetesSourcePreparation) endForwardPortLocked(outcome string) (model.Lease, error) {
	m := k.manager
	l, err := m.get(k.port.ID)
	if err != nil {
		return l, err
	}
	if l.State == "terminal" || l.State == "ending" {
		return l, nil
	}
	if l.Host != m.host || l.Owner != m.owner || !reflect.DeepEqual(l.Request, k.port.Request) {
		return l, model.Err("lease_inactive", "original forward port tuple changed")
	}
	l.State = "ending"
	l.Outcome = outcome
	l.Revision++
	if err = m.put(l, "kubernetes.forward_port_ending"); err != nil {
		return l, err
	}
	k.port = l
	return l, nil
}

// Native first close is unconditional and outside bookkeeping locks. A failed
// source helper/file join or native close retains the port and source promise.
func (k *KubernetesSourcePreparation) finishForwardPort(helpersClosed bool) (closed, settled bool, state string, resultErr error) {
	closeErr := k.socket.closeOriginal()
	if !helpersClosed {
		closeErr = errors.Join(closeErr, fmt.Errorf("original forward helper/file closure unresolved"))
	}
	closed = closeErr == nil
	m := k.manager
	m.mu.Lock()
	expected := k.port
	m.mu.Unlock()
	available := false
	probeClosed := true
	var probeErr error
	if closed && len(expected.Ports) == 1 {
		available, probeClosed, probeErr = probeOriginalForwardPort(expected.Ports[0])
	} else if closed && len(expected.Ports) == 0 {
		available = true
	}
	closed = closed && probeClosed
	m.mu.Lock()
	defer m.mu.Unlock()
	l, err := m.get(k.portID)
	if err != nil {
		return closed, false, "needs_attention", errors.Join(closeErr, probeErr, err)
	}
	if !reflect.DeepEqual(l, k.port) {
		return closed, false, l.State, errors.Join(closeErr, probeErr, fmt.Errorf("forward parent changed before original retirement"))
	}
	if closeErr != nil || probeErr != nil || !available {
		l.State = "needs_attention"
		l.Ended = nil
		l.Failure = "Original forward cleanup or availability observation unresolved"
		l.Revision++
		for n := range l.Ports {
			l.Ports[n].State = "unresolved"
		}
		err = m.put(l, "kubernetes.forward_port_uncertain")
		if err == nil {
			k.port = l
		}
		return closed, false, l.State, errors.Join(closeErr, probeErr, err)
	}
	for n := range l.Ports {
		l.Ports[n].State = "observed_available"
		l.Ports[n].SocketID = ""
		l.Ports[n].Detail = "Original socket closed positively; separate exact bind probe succeeded; no future reservation"
	}
	l.State = "terminal"
	l.Outcome = "released"
	l.Revision++
	now := time.Now().UTC()
	l.Ended = &now
	if err = m.put(l, "kubernetes.forward_port_closed"); err != nil {
		return closed, false, "needs_attention", err
	}
	k.port = l
	return closed, true, l.State, nil
}

// The observation cannot substitute for original closure. Every probe's new
// descriptor has its own first-close result, outside Manager.mu and without
// current SQL/entitlement being required to close it.
func probeOriginalForwardPort(binding model.PortBinding) (available, closed bool, resultErr error) {
	if binding.Network != "tcp4" || binding.Address != "127.0.0.1" || binding.Port < 1 || binding.Port > 65535 {
		return false, true, fmt.Errorf("invalid original forward probe binding")
	}
	socket, err := openKubernetesForwardSocket("kubernetes-forward-cleanup-probe")
	if err != nil {
		return false, socket.closeOriginal() == nil, err
	}
	fd := int(socket.file.Fd())
	defer func() {
		err := socket.closeOriginal()
		closed = err == nil
		if err != nil {
			available = false
		}
		resultErr = errors.Join(resultErr, err)
	}()
	if err = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 0); err != nil {
		return false, false, err
	}
	if err = unix.Bind(fd, &unix.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}, Port: binding.Port}); err != nil {
		return false, false, err
	}
	return true, false, nil
}
