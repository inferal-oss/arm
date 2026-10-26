package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
)

// A bounded set of original service owners, not a lifetime history cap. Only
// successful completed receipts may be pruned. Pending/failed owners stay owned.
const maxRetainedMeshClosers = 64

type meshCloseResult struct {
	serveJoined, connectionsAndHandlersJoined               bool
	routedJoined, workspaceJoined, sshRoutedJoined, sshIdle bool
	forwardSourcesJoined                                    bool
	httpClosed, storeClosed, listenerClosed                 bool
	err                                                     error
}

func (r meshCloseResult) complete() bool {
	return r.err == nil && r.httpClosed && r.listenerClosed && r.storeClosed && r.serveJoined && r.connectionsAndHandlersJoined && r.routedJoined && r.workspaceJoined && r.sshRoutedJoined && r.sshIdle && r.forwardSourcesJoined
}

type meshClose struct {
	service *meshService
	serve   <-chan error // captured original Serve owner; never rebound
	ioDone  chan struct{}
	done    chan struct{}
	io      meshCloseResult // immutable after ioDone
	result  meshCloseResult // immutable after done
}

func (m *meshService) connectionState(connection net.Conn, state http.ConnState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state == http.StateNew {
		m.connections[connection] = struct{}{}
	}
	if state == http.StateClosed || state == http.StateHijacked {
		delete(m.connections, connection)
	}
	m.maybeDrainedLocked()
}

func (m *meshService) enterRequest() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sealed {
		return false
	}
	m.requests++
	return true
}
func (m *meshService) leaveRequest() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests--
	m.maybeDrainedLocked()
}
func (m *meshService) sealRequests() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sealed = true
	if m.drainDone == nil {
		m.drainDone = make(chan struct{})
	}
	m.maybeDrainedLocked()
}
func (m *meshService) maybeDrainedLocked() {
	// Go's Server.Serve calls ConnState(StateNew) synchronously before starting
	// each connection goroutine. Observing the original Serve return seals that
	// roster, including accepted TLS handshakes before ServeHTTP. StateHijacked
	// transfers its remaining lifetime to our admitted handler/stream count.
	if m.sealed && m.serveJoined && len(m.connections) == 0 && m.requests == 0 && !m.drainClosed {
		m.drainClosed = true
		close(m.drainDone)
	}
}

func (s *Server) pruneMeshClosersLocked() {
	for service, owner := range s.meshClosers {
		select {
		case <-owner.done:
			if owner.result.complete() {
				delete(s.meshClosers, service)
			}
		default:
		}
	}
}
func (s *Server) canRetainMeshCloser() bool {
	s.meshCloseEvidenceMu.Lock()
	defer s.meshCloseEvidenceMu.Unlock()
	s.pruneMeshClosersLocked()
	return len(s.meshClosers) < maxRetainedMeshClosers
}
func (s *Server) meshCloseBlocker() *nodestop.Blocker {
	s.meshCloseEvidenceMu.Lock()
	defer s.meshCloseEvidenceMu.Unlock()
	s.pruneMeshClosersLocked()
	pending, failed := 0, 0
	for _, owner := range s.meshClosers {
		select {
		case <-owner.done:
			failed++ // successful receipts were pruned above
		default:
			pending++
		}
	}
	if pending+failed == 0 {
		return nil
	}
	class := "unjoined"
	if failed > 0 {
		class = "unknown"
	}
	return &nodestop.Blocker{Class: class, ID: "prior-mesh-close", Detail: fmt.Sprintf("Earlier mesh transport closure: %d original owners still joining, %d failed or missing join receipts. Existing obligations remain tracked; a different clean listener cannot settle these duties.", pending, failed)}
}

// beginMeshClose seals/registers before releasing meshMu. All native close,
// store locks and waits then occur outside meshMu, peer, gate and Manager locks.
func (s *Server) beginMeshClose() (*meshClose, error) {
	s.meshMu.Lock()
	pending := s.meshPending
	s.meshPending = nil
	m := s.mesh
	if m == nil {
		s.meshMu.Unlock()
		if pending != nil {
			pending.close()
		}
		return nil, nil
	}
	if existing := m.closeOwner; existing != nil {
		s.meshMu.Unlock()
		if pending != nil {
			pending.close()
		}
		<-existing.ioDone
		return existing, existing.io.err
	}
	s.meshCloseEvidenceMu.Lock()
	s.pruneMeshClosersLocked()
	if len(s.meshClosers) >= maxRetainedMeshClosers {
		s.meshCloseEvidenceMu.Unlock()
		s.meshMu.Unlock()
		if pending != nil {
			pending.close()
		}
		return nil, model.Err("mesh_close_pending", "Retained mesh closure capacity is full; original owners were preserved")
	}
	owner := &meshClose{service: m, serve: m.serveDone, ioDone: make(chan struct{}), done: make(chan struct{})}
	if s.meshClosers == nil {
		s.meshClosers = make(map[*meshService]*meshClose)
	}
	s.meshClosers[m] = owner
	m.closeOwner = owner
	s.meshClosing = true
	m.sealRequests()
	s.meshCloseEvidenceMu.Unlock()
	s.meshMu.Unlock()
	if pending != nil {
		pending.close()
	}
	if m.routed != nil {
		m.routed.Stop()
	}
	if m.sshRouted != nil {
		m.sshRouted.Stop()
	}
	if m.forwardRouted != nil {
		m.forwardRouted.Stop()
	}
	if m.forwardProxy != nil {
		m.forwardProxy.Stop()
	}
	m.stopForwardSources()
	if m.workspace != nil {
		m.workspace.Stop()
	}
	if m.ssh != nil {
		m.ssh.ClosePeer("")
	}
	result := meshCloseResult{}
	result.forwardSourcesJoined = true
	httpErr := m.http.Close()
	if errors.Is(httpErr, net.ErrClosed) || errors.Is(httpErr, http.ErrServerClosed) {
		httpErr = nil
	}
	result.httpClosed = httpErr == nil
	listenerErr := m.listener.Close()
	if errors.Is(listenerErr, net.ErrClosed) {
		listenerErr = nil
	}
	result.listenerClosed = listenerErr == nil
	if result.listenerClosed {
		s.Manager.RemoveMeshInfrastructure()
	}
	storeErr := m.store.Close()
	result.storeClosed = storeErr == nil
	result.err = errors.Join(httpErr, listenerErr, storeErr)
	owner.io = result
	// IO admission is closed; a new approved listener may open while only the
	// retained old handlers are joining. Their pending census entry remains.
	s.meshMu.Lock()
	if s.mesh == m {
		s.mesh = nil
		s.meshClosing = false
	}
	s.meshMu.Unlock()
	close(owner.ioDone)
	go owner.join()
	return owner, result.err
}

func (owner *meshClose) join() {
	m := owner.service
	result := owner.io
	if owner.serve == nil {
		result.err = errors.Join(result.err, fmt.Errorf("mesh Serve owner has no retained join"))
	} else if err, ok := <-owner.serve; !ok {
		result.err = errors.Join(result.err, fmt.Errorf("mesh Serve owner closed without a result"))
	} else {
		result.serveJoined = true
		if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
			result.err = errors.Join(result.err, err)
		}
		m.mu.Lock()
		m.serveJoined = true
		m.maybeDrainedLocked()
		m.mu.Unlock()
		<-m.drainDone
		result.connectionsAndHandlersJoined = true
	}
	if m.routed != nil {
		m.routed.Wait()
	}
	result.routedJoined = true
	if m.sshRouted != nil {
		m.sshRouted.Wait()
	}
	if m.forwardRouted != nil {
		m.forwardRouted.Wait()
	}
	result.sshRoutedJoined = true
	if m.workspace != nil {
		m.workspace.Wait()
	}
	result.workspaceJoined = true
	result.sshIdle = m.ssh == nil || m.ssh.Active() == 0
	if !result.sshIdle {
		result.err = errors.Join(result.err, fmt.Errorf("original SSH handler still owns sessions"))
	}
	if result.err == nil && !result.complete() {
		result.err = fmt.Errorf("original mesh close receipt is incomplete")
	}
	owner.result = result
	close(owner.done)
}
