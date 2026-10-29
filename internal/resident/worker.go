package resident

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// ClientRequest is the deliberately narrow prototype API. Caller principal,
// resident/worker identity and private capability cannot be supplied by HTTP.
type ClientRequest struct {
	Operation string          `json:"operation"`
	Key       string          `json:"key,omitempty"`
	Params    json.RawMessage `json:"params"`
}

// ServeWorker requires explicit inherited channels from a trusted launcher. It
// opens no state directory/ledger, starts no children and has no Manager handle.
// It is not wired into the public command or ordinary backend startup.
func ServeWorker(ctx context.Context, private net.Conn, listener net.Listener, version string) error {
	defer private.Close()
	defer listener.Close()
	host, _, err := net.SplitHostPort(listener.Addr().String())
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return failure("listener_invalid")
	}
	_ = private.SetDeadline(time.Now().Add(5 * time.Second))
	var boot bootstrap
	if err := readFrame(private, 4096, &boot); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	digest, err := ExecutableSHA256(exe)
	if err != nil {
		return err
	}
	if boot.Protocol != Protocol || boot.Worker.Version != version || boot.Worker.SHA256 != digest || len(boot.Capability) != 64 || len(boot.ClientToken) != 64 {
		return failure("worker_incompatible")
	}
	if err := writeFrame(private, 4096, hello{Protocol: Protocol, Resident: boot.Resident, Worker: boot.Worker, Capability: boot.Capability}); err != nil {
		return err
	}
	var welcome struct {
		Protocol int `json:"protocol"`
	}
	if err := readFrame(private, 4096, &welcome); err != nil {
		return err
	}
	if welcome.Protocol != Protocol {
		return failure("worker_incompatible")
	}
	_ = private.SetDeadline(time.Time{})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var sequence uint64
	var sequenceMu sync.Mutex
	var lifecycleMu sync.Mutex
	var handlers sync.WaitGroup
	closing := false
	slot := make(chan struct{}, 1)
	h := &http.Server{ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 8 * time.Second, IdleTimeout: time.Second, MaxHeaderBytes: 8192}
	h.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lifecycleMu.Lock()
		if closing {
			lifecycleMu.Unlock()
			http.Error(w, "worker closing", http.StatusServiceUnavailable)
			return
		}
		handlers.Add(1)
		lifecycleMu.Unlock()
		defer handlers.Done()
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "POST" || r.URL.Path != "/call" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+boot.ClientToken)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		select {
		case slot <- struct{}{}:
			defer func() { <-slot }()
		default:
			http.Error(w, "busy", http.StatusTooManyRequests)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxRequest+1))
		if err != nil || len(body) > MaxRequest {
			http.Error(w, "request limit", http.StatusRequestEntityTooLarge)
			return
		}
		var q ClientRequest
		if decode(body, &q) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		// One in-flight request bounds memory and serializes private frames.
		sequenceMu.Lock()
		defer sequenceMu.Unlock()
		sequence++
		request := Request{Protocol: Protocol, Resident: boot.Resident, Worker: boot.Worker, Capability: boot.Capability, ID: sequence, Operation: q.Operation, Key: q.Key, Params: q.Params}
		_ = private.SetDeadline(time.Now().Add(6 * time.Second))
		var reply Reply
		if err = writeFrame(private, MaxRequest, request); err == nil {
			err = readFrame(private, MaxResponse, &reply)
		}
		if err != nil {
			cancel()
			http.Error(w, "resident response unavailable; outcome may be unknown", http.StatusBadGateway)
			return
		}
		_ = private.SetDeadline(time.Time{})
		if reply.Protocol != Protocol || reply.Resident != boot.Resident || reply.Worker != boot.Worker || reply.ID != request.ID || reply.Key != request.Key {
			cancel()
			http.Error(w, "resident binding changed", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	})
	joined := make(chan struct{})
	go func() { defer close(joined); <-ctx.Done(); _ = listener.Close(); _ = h.Close(); _ = private.Close() }()
	bounded := &boundedListener{Listener: listener, slots: make(chan struct{}, 4), done: ctx.Done()}
	err = h.Serve(bounded)
	lifecycleMu.Lock()
	closing = true
	lifecycleMu.Unlock()
	cancel()
	<-joined
	handlers.Wait()
	if errors.Is(err, net.ErrClosed) || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type boundedListener struct {
	net.Listener
	slots chan struct{}
	done  <-chan struct{}
}

func (l *boundedListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &boundedConn{Conn: c, release: func() { <-l.slots }}, nil
}

type boundedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *boundedConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
