package manager

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"

	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

func forwardCoreOwner(t *testing.T) (*Manager, *KubernetesSourcePreparation, func()) {
	t.Helper()
	m := sourceCoreManager(t)
	joined := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(joined) }) }
	owner, err := m.PrepareKubernetesForwardSource(t.Context(), sourceCoreScope(kubesource.Forward), joined)
	if err != nil {
		finish()
		t.Fatal(err)
	}
	t.Cleanup(func() { finish(); _, _ = owner.FinishAfterJoin() })
	return m, owner, finish
}
func TestKubernetesForwardOriginalTripleCloseAndCachedBinding(t *testing.T) {
	m, owner, finish := forwardCoreOwner(t)
	binding, err := owner.Binding()
	if err != nil || binding.PortLeaseID == "" {
		t.Fatal(binding, err)
	}
	flags, err := unix.FcntlInt(owner.socket.file.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("original socket can cross exec", flags, err)
	}
	m.mu.Lock()
	source, _ := m.get(binding.SourceLeaseID)
	disk, _ := m.get(binding.StorageLeaseID)
	port, _ := m.get(binding.PortLeaseID)
	m.mu.Unlock()
	if kubesource.ValidatePair(source, disk) != nil || kubesource.ValidatePortPair(source, port) != nil || source.Request.Resources.Disk != 0 || len(m.portSockets) != 0 {
		t.Fatal("not the exact separate original triple", source, port)
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port.Ports[0].Port))
	if ln, err := net.Listen("tcp4", address); err == nil {
		ln.Close()
		t.Fatal("admitted original port was not retained")
	}
	if _, err = m.End(binding.PortLeaseID, "released"); err != nil {
		t.Fatal(err)
	}
	if owner.Context().Err() == nil {
		t.Fatal("port End failed to cancel original source")
	}
	select {
	case <-owner.privateJoined:
		t.Fatal("End substituted for caller join")
	default:
	}
	if ln, err := net.Listen("tcp4", address); err == nil {
		ln.Close()
		t.Fatal("End released the unjoined original socket")
	}
	finish()
	result, err := owner.FinishAfterJoin()
	if err != nil || !result.SourceClosed || !result.SourceSettled || !result.PortClosed || !result.PortSettled || result.PortState != "terminal" {
		t.Fatal(result, err)
	}
	after, err := owner.Binding()
	if err != nil || after != binding {
		t.Fatal("cached binding changed after close", after, err)
	}
	if ln, err := net.Listen("tcp4", address); err != nil {
		t.Fatal("closed original port still held", err)
	} else {
		ln.Close()
	}
}
func TestKubernetesForwardCancellationAtFinalBindCutCreatesNoBinding(t *testing.T) {
	m := sourceCoreManager(t)
	var original *KubernetesSourcePreparation
	var hookErr error
	m.sourceBeforePortBind = func() {
		m.mu.Lock()
		for _, k := range m.kubernetesSources {
			if k.scope.Stage == kubesource.Forward {
				original = k
				break
			}
		}
		m.mu.Unlock()
		if original == nil {
			hookErr = errors.New("source owner absent before final bind cut")
			return
		}
		_, hookErr = m.End(original.portID, "released")
	}
	joined := make(chan struct{})
	owner, err := m.PrepareKubernetesForwardSource(t.Context(), sourceCoreScope(kubesource.Forward), joined)
	close(joined)
	if owner != nil {
		_, _ = owner.FinishAfterJoin()
	}
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	if err == nil || owner != nil || original == nil {
		t.Fatal(owner, err)
	}
	m.mu.Lock()
	port, readErr := m.get(original.portID)
	source, _ := m.get(original.transit.id)
	m.mu.Unlock()
	if readErr != nil || len(port.Ports) != 0 || port.State != "terminal" || source.State != "terminal" || original.socket.available() {
		t.Fatal("cancelled final cut exposed native binding or retained live socket", port, source, readErr)
	}
}
func TestKubernetesForwardFirstCloseFailureRetainsSourceAndPort(t *testing.T) {
	m, owner, finish := forwardCoreOwner(t)
	owner.socket.closeNative = func(f *os.File) error { return errors.Join(f.Close(), errors.New("fixture first-close uncertainty")) }
	finish()
	result, err := owner.FinishAfterJoin()
	if err == nil || result.SourceClosed || result.SourceSettled || result.PortClosed || result.PortSettled {
		t.Fatal("uncertain original close settled", result, err)
	}
	m.mu.Lock()
	source, _ := m.get(result.SourceLeaseID)
	port, _ := m.get(result.PortLeaseID)
	m.mu.Unlock()
	if source.State != "needs_attention" || port.State != "needs_attention" || source.Request.Resources.CPU != kubesource.SourceCPU || source.Request.Resources.Memory != kubesource.SourceMemory {
		t.Fatal("original promises not retained", source, port)
	}
	joined := make(chan struct{})
	next, err := m.PrepareKubernetesSource(t.Context(), sourceCoreScope(kubesource.Inspect), joined)
	close(joined)
	if next != nil {
		_, _ = next.FinishAfterJoin()
	}
	if err == nil || next != nil {
		t.Fatal("unknown f allowed concurrent inspection", next, err)
	}
}

func TestKubernetesForwardEveryOriginalEndRetainsSocketUntilJoin(t *testing.T) {
	for _, role := range []string{"source", "storage", "port"} {
		t.Run(role, func(t *testing.T) {
			m, owner, finish := forwardCoreOwner(t)
			b, err := owner.Binding()
			if err != nil {
				t.Fatal(err)
			}
			id := b.SourceLeaseID
			if role == "storage" {
				id = b.StorageLeaseID
			}
			if role == "port" {
				id = b.PortLeaseID
			}
			m.mu.Lock()
			port, _ := m.get(b.PortLeaseID)
			m.mu.Unlock()
			address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port.Ports[0].Port))
			if _, err = m.End(id, "released"); err != nil {
				t.Fatal(err)
			}
			if owner.Context().Err() == nil {
				t.Fatal("original End failed to withdraw admission", role)
			}
			if ln, err := net.Listen("tcp4", address); err == nil {
				ln.Close()
				t.Fatal("End freed original before join", role)
			}
			finish()
			r, err := owner.FinishAfterJoin()
			if err != nil || !r.SourceClosed || !r.SourceSettled || !r.PortClosed || !r.PortSettled {
				t.Fatal(role, r, err)
			}
		})
	}
}
func TestKubernetesForwardManagerCloseWaitsForOriginalCaller(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "state"), model.Resources{CPU: 1, Memory: 512 << 20, Disk: 512 << 20})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 64, 64, 0, 64, 0)
	var closeOnce sync.Once
	var closeErr error
	closeManager := func() error { closeOnce.Do(func() { closeErr = m.Close() }); return closeErr }
	t.Cleanup(func() { _ = closeManager() })
	joined := make(chan struct{})
	var joinOnce sync.Once
	finish := func() { joinOnce.Do(func() { close(joined) }) }
	owner, err := m.PrepareKubernetesForwardSource(t.Context(), sourceCoreScope(kubesource.Forward), joined)
	if err != nil {
		finish()
		t.Fatal(err)
	}
	t.Cleanup(func() { finish(); _, _ = owner.FinishAfterJoin() })
	closed := make(chan error, 1)
	go func() { closed <- closeManager() }()
	select {
	case <-owner.Context().Done():
	case <-t.Context().Done():
		t.Fatal("Manager Close did not cancel source")
	}
	select {
	case err := <-closed:
		t.Fatal("Manager Close returned before original caller", err)
	default:
	}
	if !owner.socket.available() {
		t.Fatal("Manager Close preemptively closed original socket")
	}
	finish()
	r, err := owner.FinishAfterJoin()
	if err != nil || !r.SourceClosed || !r.SourceSettled || !r.PortClosed || !r.PortSettled {
		t.Fatal(r, err)
	}
	if err = <-closed; err != nil {
		t.Fatal(err)
	}
}
func TestKubernetesForwardSQLFailureStillClosesOriginalAndRetainsSettlement(t *testing.T) {
	m, owner, finish := forwardCoreOwner(t)
	binding, err := owner.Binding()
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	port, _ := m.get(binding.PortLeaseID)
	m.mu.Unlock()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port.Ports[0].Port))
	if err = m.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = m.End(binding.PortLeaseID, "released"); err == nil || owner.Context().Err() == nil {
		t.Fatal("failed SQL suppressed original cancellation", err)
	}
	finish()
	r, err := owner.FinishAfterJoin()
	if err == nil || !r.SourceClosed || !r.PortClosed || r.SourceSettled || r.PortSettled {
		t.Fatal("closure and failed settlement conflated", r, err)
	}
	cached, err := owner.Binding()
	if err != nil || cached != binding {
		t.Fatal("binding read depended on failed SQL", cached, err)
	}
	if ln, err := net.Listen("tcp4", address); err != nil {
		t.Fatal("failed SQL prevented actual original close", err)
	} else {
		ln.Close()
	}
}

func TestKubernetesForwardJoinedCallerAtBindCutCreatesNoBinding(t *testing.T) {
	m := sourceCoreManager(t)
	joined := make(chan struct{})
	var original *KubernetesSourcePreparation
	m.sourceBeforePortBind = func() {
		m.mu.Lock()
		for _, k := range m.kubernetesSources {
			original = k
			break
		}
		m.mu.Unlock()
		close(joined)
	}
	owner, err := m.PrepareKubernetesForwardSource(t.Context(), sourceCoreScope(kubesource.Forward), joined)
	if owner != nil {
		_, _ = owner.FinishAfterJoin()
	}
	if err == nil || owner != nil || original == nil {
		t.Fatal(owner, err)
	}
	m.mu.Lock()
	port, readErr := m.get(original.portID)
	m.mu.Unlock()
	if readErr != nil || len(port.Ports) != 0 || port.State != "terminal" || original.socket.available() {
		t.Fatal("joined original caller allowed bind", port, readErr)
	}
}

func TestKubernetesForwardRequestDebtSurvivesPositivePortAndCPUClosure(t *testing.T) {
	m, owner, finish := forwardCoreOwner(t)
	if payload, err := owner.Files().NewPayload(t.Context()); err == nil || payload != nil {
		t.Fatal("forward profile exposed payload")
	}
	request, err := owner.files.create(t.Context(), "mesh_forward")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = request.Write([]byte("one bounded original forward request")); err != nil {
		t.Fatal(err)
	}
	if err = request.Close(); err != nil {
		t.Fatal(err)
	}
	if again, err := owner.files.create(t.Context(), "mesh_forward"); err == nil || again != nil {
		t.Fatal("forward request purpose repeated")
	}
	finish()
	r, err := owner.FinishAfterJoin()
	if err != nil || !r.SourceClosed || !r.SourceSettled || !r.PortClosed || !r.PortSettled || !r.StorageRetained || r.PromisedDisk != 131072 || (r.StorageState != "ending" && r.StorageState != "needs_attention") {
		t.Fatal("positive native port/CPU closure erased request storage debt", r, err)
	}
	m.mu.Lock()
	parent, readErr := m.get(r.StorageLeaseID)
	m.mu.Unlock()
	if readErr != nil || !consuming(parent.State) || parent.Request.Resources.Disk != 131072 || parent.Artifact == nil || !parent.Artifact.ContentObserved || parent.Artifact.AllocatedBefore == nil {
		t.Fatal("original paired storage promise/history missing", parent, readErr)
	}
}

func TestKubernetesForwardSocketForkExclusionAndFirstClose(t *testing.T) {
	for _, mode := range []string{"normal", "cloexec-refused", "close-uncertain"} {
		t.Run(mode, func(t *testing.T) {
			flagErr := errors.New("fixture CLOEXEC refusal")
			closeErr := errors.New("fixture original close uncertainty")
			fd, closes := -1, 0
			excluded := true
			checkExclusion := func() {
				// Go's fork/exec writer must not acquire the lock while the
				// descriptor exists without verified CLOEXEC, including failure
				// retirement. An omitted critical section makes this observable.
				if syscall.ForkLock.TryLock() {
					excluded = false
					syscall.ForkLock.Unlock()
				}
			}
			socket, err := openKubernetesForwardSocketWith("forward-exec-fixture", kubernetesForwardSocketOps{
				open: func() (int, error) {
					checkExclusion()
					var err error
					fd, err = unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
					return fd, err
				},
				closeOnExec: func(fd int) error {
					checkExclusion()
					if mode != "normal" {
						return flagErr
					}
					_, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
					return err
				},
				close: func(f *os.File) error {
					if mode != "normal" {
						checkExclusion()
					}
					closes++
					actual := f.Close()
					if mode == "close-uncertain" {
						return errors.Join(actual, closeErr)
					}
					return actual
				},
			})
			if socket != nil {
				t.Cleanup(func() { _ = socket.closeOriginal() })
			}
			if socket == nil || !excluded {
				t.Fatal("creation or unmarked retirement escaped fork exclusion", socket, err)
			}
			if mode == "normal" {
				flags, flagReadErr := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
				if err != nil || flagReadErr != nil || flags&unix.FD_CLOEXEC == 0 || closes != 0 || !socket.available() {
					t.Fatal("normal original CLOEXEC/lifecycle differs", flags, closes, err, flagReadErr)
				}
			} else if !errors.Is(err, flagErr) || closes != 1 || socket.available() {
				t.Fatal("unmarked original not retired before return", closes, err)
			}
			first := socket.closeOriginal()
			second := socket.closeOriginal()
			if closes != 1 || first != second || (mode == "close-uncertain") != errors.Is(first, closeErr) {
				t.Fatal("first close was retried or its error lost", closes, first, second)
			}
			if mode == "close-uncertain" && !errors.Is(err, closeErr) {
				t.Fatal("constructor lost original close uncertainty", err)
			}
		})
	}
}
