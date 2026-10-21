package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/yrashk/arm/internal/model"
)

func sessionFixture(t *testing.T, handle func(context.Context, *websocket.Conn)) *Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer c.CloseNow()
		handle(ctx, c)
	}))
	c, _, err := websocket.Dial(ctx, strings.Replace(httpServer.URL, "http://", "ws://", 1), nil)
	if err != nil {
		cancel()
		httpServer.Close()
		t.Fatal(err)
	}
	s := newSession(ctx, c)
	t.Cleanup(func() { s.Close(); cancel(); httpServer.Close() })
	return s
}

func TestSessionMultiplexesOutOfOrderResponsesEventsAndSlowTelemetry(t *testing.T) {
	s := sessionFixture(t, func(ctx context.Context, c *websocket.Conn) {
		var monitor model.WireRequest
		if err := wsjson.Read(ctx, c, &monitor); err != nil {
			t.Error(err)
			return
		}
		if err := wsjson.Write(ctx, c, model.WireResponse{Version: 1, ID: monitor.ID, Result: model.MonitorPage{Epoch: "test", Cursor: 1}}); err != nil {
			t.Error(err)
			return
		}
		requests := make([]model.WireRequest, sessionRequestLimit)
		for i := range requests {
			if err := wsjson.Read(ctx, c, &requests[i]); err != nil {
				t.Error(err)
				return
			}
		}
		for sequence := int64(1); sequence <= 100; sequence++ {
			if err := wsjson.Write(ctx, c, model.WireResponse{Version: 1, Telemetry: &model.MonitorPage{Epoch: "test", Cursor: sequence}, Subscription: monitor.ID}); err != nil {
				t.Error(err)
				return
			}
		}
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, Event: &model.Event{Sequence: 42, Type: "lease.acquired"}})
		for i := len(requests) - 1; i >= 0; i-- {
			if err := wsjson.Write(ctx, c, model.WireResponse{Version: 1, ID: requests[i].ID, Result: map[string]any{"operation": requests[i].Operation, "capacity": json.Number("9223372036854775807")}}); err != nil {
				t.Error(err)
				return
			}
		}
		<-ctx.Done()
	})
	var initial model.MonitorPage
	if err := s.Call(t.Context(), "monitor_subscribe", model.MonitorQuery{}, &initial); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, operation := range []string{"status", "messages", "resources", "inspect"} {
		wg.Go(func() {
			var result map[string]any
			if err := s.Call(t.Context(), operation, nil, &result); err != nil || result["operation"] != operation || result["capacity"] != json.Number("9223372036854775807") {
				t.Errorf("response was lost/rounded/misrouted: %s %+v %v", operation, result, err)
			}
		})
	}
	wg.Wait()
	select {
	case event := <-s.Events():
		if event.Sequence != 42 {
			t.Fatal(event)
		}
	default:
		t.Fatal("durable event was lost behind telemetry")
	}
	select {
	case page := <-s.Telemetry():
		if page.Cursor != 100 || !page.Gap {
			t.Fatal("slow telemetry did not coalesce with an explicit gap", page)
		}
	default:
		t.Fatal("latest telemetry missing")
	}
}

func TestSessionLimitsInflightRequestsBeforeWriting(t *testing.T) {
	ready, release := make(chan struct{}), make(chan struct{})
	s := sessionFixture(t, func(ctx context.Context, c *websocket.Conn) {
		requests := make([]model.WireRequest, sessionRequestLimit)
		for i := range requests {
			if err := wsjson.Read(ctx, c, &requests[i]); err != nil {
				t.Error(err)
				return
			}
		}
		close(ready)
		select {
		case <-ctx.Done():
			return
		case <-release:
		}
		for _, request := range requests {
			_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, ID: request.ID, Result: true})
		}
		<-ctx.Done()
	})
	var wg sync.WaitGroup
	for range sessionRequestLimit {
		wg.Go(func() {
			if err := s.Call(t.Context(), "status", nil, nil); err != nil {
				t.Error(err)
			}
		})
	}
	<-ready
	err := s.Call(t.Context(), "lease_action", nil, nil)
	var api *model.Error
	if !errors.As(err, &api) || api.Code != "too_many_requests" {
		t.Fatal("unbounded request admitted", err)
	}
	close(release)
	wg.Wait()
}

func TestSessionLostMutationResponseNeverRetries(t *testing.T) {
	var calls atomic.Int32
	s := sessionFixture(t, func(ctx context.Context, c *websocket.Conn) {
		var request model.WireRequest
		if err := wsjson.Read(ctx, c, &request); err != nil {
			t.Error(err)
			return
		}
		calls.Add(1)
		// The remote mutation could already have committed; transport loss is
		// never a definitive rejection and never authorizes an automatic retry.
	})
	err := s.Call(t.Context(), "lease_action", model.LeaseAction{ID: "lease", Revision: 1, Action: "release"}, nil)
	var api *model.Error
	if err == nil || errors.As(err, &api) {
		t.Fatal("lost mutation response was reported as a definitive API answer", err)
	}
	<-s.Done()
	if err = s.Call(t.Context(), "lease_action", nil, nil); err == nil || calls.Load() != 1 {
		t.Fatal("closed session resent a mutation", err, calls.Load())
	}
}

func TestSessionOverflowSignalsResynchronizationAndUnblocksPendingCalls(t *testing.T) {
	s := sessionFixture(t, func(ctx context.Context, c *websocket.Conn) {
		var request model.WireRequest
		if err := wsjson.Read(ctx, c, &request); err != nil {
			t.Error(err)
			return
		}
		for sequence := int64(1); sequence <= 65; sequence++ {
			if err := wsjson.Write(ctx, c, model.WireResponse{Version: 1, Event: &model.Event{Sequence: sequence}}); err != nil {
				return
			}
		}
		<-ctx.Done()
	})
	err := s.Call(t.Context(), "status", nil, nil)
	var api *model.Error
	if err == nil || !strings.Contains(err.Error(), "event buffer filled") || errors.As(err, &api) {
		t.Fatal("event overflow silently dropped durable state or claimed API rejection", err)
	}
	<-s.Done()
}

func TestSessionCancelledRequestDiscardsLateResponseWithoutPoisoningNextCall(t *testing.T) {
	seen, release := make(chan struct{}), make(chan struct{})
	s := sessionFixture(t, func(ctx context.Context, c *websocket.Conn) {
		var first, second model.WireRequest
		if err := wsjson.Read(ctx, c, &first); err != nil {
			t.Error(err)
			return
		}
		close(seen)
		select {
		case <-ctx.Done():
			return
		case <-release:
		}
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, ID: first.ID, Result: "late"})
		if err := wsjson.Read(ctx, c, &second); err != nil {
			t.Error(err)
			return
		}
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, ID: second.ID, Result: "current"})
		<-ctx.Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Call(ctx, "status", nil, nil) }()
	<-seen
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	var result string
	if err := s.Call(t.Context(), "status", nil, &result); err != nil || result != "current" {
		t.Fatal("late response poisoned next request", result, err)
	}
}

func TestSessionCancelledOrdinaryRequestReturnsBeforeWriteGateRelease(t *testing.T) {
	s := sessionFixture(t, func(ctx context.Context, c *websocket.Conn) { <-ctx.Done() })
	s.writeGate <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Call(ctx, "status", nil, nil) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		registered := len(s.pending) > 0
		s.mu.Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			<-s.writeGate
			t.Fatal("ordinary request did not reach held gate")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		<-s.writeGate
		t.Fatal("cancelled ordinary call waited for held write gate")
	}
	<-s.writeGate
	s.mu.Lock()
	pending := len(s.pending)
	s.mu.Unlock()
	if pending != 0 {
		t.Fatal("cancelled prewrite request retained pending state", pending)
	}
}
