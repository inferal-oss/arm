package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

func sessionEndpoint(t *testing.T, address string) string {
	t.Helper()
	home := t.TempDir()
	if err := local.AtomicJSON(filepath.Join(home, "endpoint.json"), local.Endpoint{URL: strings.Replace(address, "http://", "ws://", 1), Token: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestSessionBoundsUnfinishedWebSocketHandshakeWithoutCancellingParent(t *testing.T) {
	entered := make(chan struct{})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	defer httpServer.Close()
	parent, cancelParent := context.WithCancel(t.Context())
	defer cancelParent()
	started := time.Now()
	session, err := OpenSession(parent, sessionEndpoint(t, httpServer.URL))
	if session != nil {
		session.Close()
		t.Fatal("unfinished handshake created a Session")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("handshake did not reach its own dial deadline", err)
	}
	select {
	case <-entered:
	default:
		t.Fatal("did not reach accepting loopback fixture")
	}
	if elapsed := time.Since(started); elapsed < sessionConnectTimeout/2 || elapsed > sessionConnectTimeout+5*time.Second {
		t.Fatal("handshake timeout outside expected bound", elapsed)
	}
	if parent.Err() != nil {
		t.Fatal("handshake deadline cancelled parent Session lifetime", parent.Err())
	}
}

func TestSessionSurvivesSuccessfulHandshakeDeadlineCleanup(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		for {
			var request model.WireRequest
			if err := wsjson.Read(r.Context(), conn, &request); err != nil {
				return
			}
			if err := wsjson.Write(r.Context(), conn, model.WireResponse{Version: 1, ID: request.ID, Result: map[string]bool{"alive": true}}); err != nil {
				return
			}
		}
	}))
	defer httpServer.Close()
	parent, cancelParent := context.WithCancel(t.Context())
	defer cancelParent()
	session, err := OpenSession(parent, sessionEndpoint(t, httpServer.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	// OpenSession has already cancelled its temporary handshake context. Calls
	// still use the independent parent lifetime, not that cancelled child.
	for range 2 {
		var response struct {
			Alive bool `json:"alive"`
		}
		if err := session.Call(parent, "status", nil, &response); err != nil || !response.Alive {
			t.Fatal("healthy Session inherited cancelled handshake context", err)
		}
	}
	cancelParent()
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not close Session")
	}
}
