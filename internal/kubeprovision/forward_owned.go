package kubeprovision

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// ForwardSession owns the listener, upgraded stream and every accepted local
// connection. Done closes only after all accept and copy workers have exited.
// A timeout from Close retains this handle for later inspection and Close.
type ForwardSession struct {
	LocalPort uint16
	cancel    context.CancelFunc
	done      chan struct{}
	once      sync.Once
	mu        sync.Mutex
	err       error
	active    map[net.Conn]struct{}
	slots     chan struct{}
	requestID atomic.Uint64
	handlers  sync.WaitGroup
}

// Each active local connection can own two SPDY streams plus three copy
// workers. A slot is held until those workers and streams have closed.
const MaxForwardConnections = 8

func (f *ForwardSession) Done() <-chan struct{} { return f.done }
func (f *ForwardSession) Err() error            { f.mu.Lock(); defer f.mu.Unlock(); return f.err }
func (f *ForwardSession) fail(err error) {
	f.mu.Lock()
	if f.err == nil {
		f.err = err
	}
	f.mu.Unlock()
	f.cancel()
}
func (f *ForwardSession) Close() error {
	f.once.Do(f.cancel)
	select {
	case <-f.done:
		return f.Err()
	case <-time.After(5 * time.Second):
		return fmt.Errorf("%w: owned port forward did not join", ErrUnknown)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func contextTransportConfig(base *rest.Config, ctx context.Context) *rest.Config {
	copyConfig := rest.CopyConfig(base)
	prior := copyConfig.WrapTransport
	copyConfig.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		if prior != nil {
			rt = prior(rt)
		}
		return roundTripperFunc(func(request *http.Request) (*http.Response, error) { return rt.RoundTrip(request.WithContext(ctx)) })
	}
	return copyConfig
}

func (f *ForwardSession) join() error {
	select {
	case <-f.done:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("%w: owned port forward did not join", ErrUnknown)
	}
}

func (c *ClientGo) Forward(ctx context.Context, pod Pod, remotePort uint16) (*ForwardSession, error) {
	if pod.UID == "" || pod.Name == "" || remotePort == 0 {
		return nil, ErrIdentity
	}
	current, err := c.GetPod(ctx, pod.Namespace, pod.Name)
	if err != nil {
		return nil, err
	}
	if current.UID != pod.UID {
		return nil, ErrIdentity
	}
	runCtx, cancel := context.WithCancel(ctx)
	restClient, ok := c.client.CoreV1().RESTClient().(*rest.RESTClient)
	if !ok || restClient == nil || c.config == nil {
		cancel()
		return nil, errors.New("native Kubernetes forwarding transport unavailable")
	}
	request := restClient.Post().Resource("pods").Namespace(pod.Namespace).Name(pod.Name).SubResource("portforward")
	config := contextTransportConfig(c.config, runCtx)
	websocket, err := portforward.NewSPDYOverWebsocketDialer(request.URL(), config)
	if err != nil {
		cancel()
		return nil, err
	}
	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		cancel()
		return nil, err
	}
	legacy := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", request.URL())
	dialer := portforward.NewFallbackDialer(websocket, legacy, func(error) bool { return true })
	session := &ForwardSession{cancel: cancel, done: make(chan struct{}), active: map[net.Conn]struct{}{}, slots: make(chan struct{}, MaxForwardConnections)}
	ready := make(chan struct{})
	go session.run(runCtx, dialer, remotePort, ready)
	select {
	case <-ctx.Done():
		cancel()
		if err := session.join(); err != nil {
			return session, err
		}
		return nil, ctx.Err()
	case <-session.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// A transport dial deadline can fire before the context timer's
		// goroutine publishes Err. Preserve the caller's elapsed deadline
		// regardless of which completion channel wins this select.
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("port forward ended before readiness: %w", session.Err())
	case <-ready:
	}
	current, err = c.GetPod(ctx, pod.Namespace, pod.Name)
	if err != nil || current.UID != pod.UID {
		cancel()
		if joinErr := session.join(); joinErr != nil {
			return session, joinErr
		}
		return nil, ErrIdentity
	}
	select {
	case <-session.done:
		return nil, fmt.Errorf("port forward ended after readiness: %w", session.Err())
	default:
	}
	return session, nil
}

func (f *ForwardSession) run(ctx context.Context, dialer httpstream.Dialer, remotePort uint16, ready chan<- struct{}) {
	defer close(f.done)
	err := f.serve(ctx, dialer, remotePort, ready)
	f.mu.Lock()
	if f.err == nil {
		f.err = err
	}
	f.mu.Unlock()
}

func (f *ForwardSession) serve(ctx context.Context, dialer httpstream.Dialer, remotePort uint16, ready chan<- struct{}) error {
	streamConn, protocol, err := dialer.Dial(portforward.PortForwardProtocolV1Name)
	if err != nil {
		return fmt.Errorf("port forward upgrade: %w", err)
	}
	defer streamConn.Close()
	if protocol != portforward.PortForwardProtocolV1Name {
		return errors.New("Kubernetes port forward protocol changed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if port <= 0 || port > 65535 {
		return errors.New("invalid localhost forwarding port")
	}
	f.LocalPort = uint16(port)
	acceptDone := make(chan error, 1)
	go f.accept(ctx, listener, streamConn, remotePort, acceptDone)
	close(ready)
	var acceptErr error
	gotAccept := false
	select {
	case <-ctx.Done():
	case <-streamConn.CloseChan():
	case acceptErr = <-acceptDone:
		gotAccept = true
	}
	listener.Close()
	streamConn.Close()
	if !gotAccept {
		acceptErr = <-acceptDone
	}
	f.closeActive()
	f.handlers.Wait()
	if err := f.Err(); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	if acceptErr != nil && !errors.Is(acceptErr, net.ErrClosed) {
		return acceptErr
	}
	return errors.New("Kubernetes port forward stream closed")
}

func (f *ForwardSession) accept(ctx context.Context, listener net.Listener, streamConn httpstream.Connection, remotePort uint16, done chan<- error) {
	for {
		client, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		if ctx.Err() != nil {
			client.Close()
			continue
		}
		select {
		case f.slots <- struct{}{}:
		default:
			// Refuse before creating any SPDY stream or copy worker.
			client.Close()
			continue
		}
		if ctx.Err() != nil {
			<-f.slots
			client.Close()
			continue
		}
		f.mu.Lock()
		f.active[client] = struct{}{}
		f.handlers.Add(1)
		f.mu.Unlock()
		go f.handle(ctx, streamConn, client, remotePort)
	}
}

func (f *ForwardSession) closeActive() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for client := range f.active {
		client.Close()
	}
}

func (f *ForwardSession) handle(ctx context.Context, streamConn httpstream.Connection, client net.Conn, remotePort uint16) {
	defer func() { <-f.slots; f.handlers.Done() }()
	defer func() { client.Close(); f.mu.Lock(); delete(f.active, client); f.mu.Unlock() }()
	id := strconv.FormatUint(f.requestID.Add(1), 10)
	headers := http.Header{}
	headers.Set(corev1.StreamType, corev1.StreamTypeError)
	headers.Set(corev1.PortHeader, strconv.Itoa(int(remotePort)))
	headers.Set(corev1.PortForwardRequestIDHeader, id)
	errorStream, err := streamConn.CreateStream(headers)
	if err != nil {
		return
	}
	defer streamConn.RemoveStreams(errorStream)
	defer errorStream.Reset()
	headers.Set(corev1.StreamType, corev1.StreamTypeData)
	dataStream, err := streamConn.CreateStream(headers)
	if err != nil {
		return
	}
	defer streamConn.RemoveStreams(dataStream)
	defer dataStream.Reset()
	type errorResult struct {
		message []byte
		err     error
	}
	errorDone := make(chan errorResult, 1)
	localDone := make(chan error, 1)
	remoteDone := make(chan error, 1)
	var copies sync.WaitGroup
	copies.Add(3)
	go func() {
		defer copies.Done()
		message, err := io.ReadAll(io.LimitReader(errorStream, 4097))
		errorDone <- errorResult{message, err}
	}()
	go func() {
		defer copies.Done()
		_, err := io.Copy(dataStream, client)
		dataStream.Close()
		localDone <- err
	}()
	go func() { defer copies.Done(); _, err := io.Copy(client, dataStream); remoteDone <- err }()
	waitingLocal := localDone
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-streamConn.CloseChan():
			break loop
		case <-remoteDone:
			break loop
		case err := <-waitingLocal:
			if err != nil {
				break loop
			}
			waitingLocal = nil
		case result := <-errorDone:
			if len(result.message) > 4096 {
				f.fail(errors.New("Kubernetes port forward error stream exceeded bound"))
				break loop
			}
			if len(result.message) > 0 {
				f.fail(errors.New("Kubernetes port forward remote stream failed"))
				break loop
			}
			errorDone = nil
		}
	}
	client.Close()
	dataStream.Reset()
	errorStream.Reset()
	copies.Wait()
}
