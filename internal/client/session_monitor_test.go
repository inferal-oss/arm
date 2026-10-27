package client

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func readSessionRequest(t *testing.T, ctx context.Context, c *websocket.Conn) model.WireRequest {
	t.Helper()
	var request model.WireRequest
	if err := wsjson.Read(ctx, c, &request); err != nil {
		t.Error(err)
	}
	return request
}

func acknowledgeMonitor(t *testing.T, ctx context.Context, c *websocket.Conn, request model.WireRequest) {
	t.Helper()
	if err := wsjson.Write(ctx, c, model.WireResponse{Version: 1, ID: request.ID, Result: model.MonitorPage{Epoch: "test", Cursor: 1}}); err != nil {
		t.Error(err)
	}
}

func TestSessionRejectedReplacementPreservesExistingMonitorAndUnsubscribeClearsIt(t *testing.T) {
	s := sessionFixture(t, func(ctx context.Context, c *websocket.Conn) {
		first := readSessionRequest(t, ctx, c)
		acknowledgeMonitor(t, ctx, c, first)
		replacement := readSessionRequest(t, ctx, c)
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, ID: replacement.ID, Error: &model.Error{Code: "invalid_request", Message: "rejected replacement"}})
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, Subscription: first.ID, Telemetry: &model.MonitorPage{Epoch: "test", Cursor: 2}})
		unsubscribe := readSessionRequest(t, ctx, c)
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, ID: unsubscribe.ID, Result: map[string]bool{"subscribed": false}})
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, Subscription: first.ID, Telemetry: &model.MonitorPage{Epoch: "test", Cursor: 3}})
		barrier := readSessionRequest(t, ctx, c)
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, ID: barrier.ID, Result: true})
		<-ctx.Done()
	})
	if err := s.Call(t.Context(), "monitor_subscribe", model.MonitorQuery{}, nil); err != nil {
		t.Fatal(err)
	}
	var api *model.Error
	if err := s.Call(t.Context(), "monitor_subscribe", model.MonitorQuery{After: -1}, nil); !errors.As(err, &api) || api.Code != "invalid_request" {
		t.Fatal("rejected subscription lost API result", err)
	}
	select {
	case page := <-s.Telemetry():
		if page.Cursor != 2 {
			t.Fatal("existing subscription stopped after rejection", page)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("existing telemetry was lost after rejected replacement")
	}
	if err := s.Call(t.Context(), "monitor_unsubscribe", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Call(t.Context(), "status", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case page := <-s.Telemetry():
		t.Fatal("telemetry survived successful unsubscribe", page)
	default:
	}
}

func TestSessionSerializesMonitorControlsWithoutBlockingOrdinaryRequests(t *testing.T) {
	firstSeen, release := make(chan struct{}), make(chan struct{})
	s := sessionFixture(t, func(ctx context.Context, c *websocket.Conn) {
		first := readSessionRequest(t, ctx, c)
		close(firstSeen)
		normal := readSessionRequest(t, ctx, c)
		if normal.Operation != "status" {
			t.Error("concurrent monitor control overtook acknowledgement", normal.Operation)
		}
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, ID: normal.ID, Result: true})
		select {
		case <-ctx.Done():
			return
		case <-release:
		}
		acknowledgeMonitor(t, ctx, c, first)
		second := readSessionRequest(t, ctx, c)
		if second.Operation != "monitor_subscribe" {
			t.Error("replacement request missing", second.Operation)
		}
		acknowledgeMonitor(t, ctx, c, second)
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, Subscription: first.ID, Telemetry: &model.MonitorPage{Epoch: "test", Cursor: 91}})
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, Subscription: second.ID, Telemetry: &model.MonitorPage{Epoch: "test", Cursor: 92}})
		barrier := readSessionRequest(t, ctx, c)
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, ID: barrier.ID, Result: true})
		<-ctx.Done()
	})
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- s.Call(t.Context(), "monitor_subscribe", model.MonitorQuery{}, nil) }()
	<-firstSeen
	queuedContext, cancelQueued := context.WithCancel(t.Context())
	cancelQueued()
	if err := s.Call(queuedContext, "monitor_subscribe", model.MonitorQuery{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled queued control did not leave the gate without writing", err)
	}
	go func() { secondDone <- s.Call(t.Context(), "monitor_subscribe", model.MonitorQuery{}, nil) }()
	if err := s.Call(t.Context(), "status", nil, nil); err != nil {
		t.Fatal("normal request blocked behind monitor control", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if err := s.Call(t.Context(), "status", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case page := <-s.Telemetry():
		if page.Cursor != 92 {
			t.Fatal("subscription identity did not follow acknowledged wire order", page)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("final acknowledged subscription was ignored")
	}
}

func TestSessionPrewriteMonitorCancellationPreservesExistingStream(t *testing.T) {
	s := sessionFixture(t, func(ctx context.Context, c *websocket.Conn) {
		first := readSessionRequest(t, ctx, c)
		acknowledgeMonitor(t, ctx, c, first)
		normal := readSessionRequest(t, ctx, c)
		if normal.Operation != "status" {
			t.Error("cancelled monitor control reached the wire", normal.Operation)
		}
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, Subscription: first.ID, Telemetry: &model.MonitorPage{Epoch: "test", Cursor: 7}})
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, ID: normal.ID, Result: true})
		<-ctx.Done()
	})
	if err := s.Call(t.Context(), "monitor_subscribe", model.MonitorQuery{}, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	s.writeGate <- struct{}{}
	go func() { done <- s.Call(ctx, "monitor_subscribe", model.MonitorQuery{}, nil) }()
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
			t.Fatal("replacement request was not registered")
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
		t.Fatal("cancelled monitor call waited for held write gate")
	}
	<-s.writeGate
	if err := s.Call(t.Context(), "status", nil, nil); err != nil {
		t.Fatal("prewrite cancellation killed the session", err)
	}
	select {
	case page := <-s.Telemetry():
		if page.Cursor != 7 {
			t.Fatal(page)
		}
	default:
		t.Fatal("prewrite cancellation changed existing telemetry identity")
	}
}

func TestSessionSentMonitorCancellationClosesAmbiguousSession(t *testing.T) {
	seen, release := make(chan struct{}), make(chan struct{})
	s := sessionFixture(t, func(ctx context.Context, c *websocket.Conn) {
		first := readSessionRequest(t, ctx, c)
		acknowledgeMonitor(t, ctx, c, first)
		replacement := readSessionRequest(t, ctx, c)
		close(seen)
		select {
		case <-ctx.Done():
			return
		case <-release:
		}
		// A late success cannot leave a live client silently following the wrong ID.
		_ = wsjson.Write(ctx, c, model.WireResponse{Version: 1, ID: replacement.ID, Result: model.MonitorPage{Epoch: "test"}})
	})
	if err := s.Call(t.Context(), "monitor_subscribe", model.MonitorQuery{}, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Call(ctx, "monitor_subscribe", model.MonitorQuery{}, nil) }()
	<-seen
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-s.Done():
		if !strings.Contains(s.Err().Error(), "monitor control outcome unknown") {
			t.Fatal(s.Err())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ambiguous monitor replacement left a live wrong subscription")
	}
}
