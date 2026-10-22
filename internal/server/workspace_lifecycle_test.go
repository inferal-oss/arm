package server

import (
	"context"
	"testing"
	"time"
)

func TestWorkspaceNativeWorkflowCloseCancelsAndJoinsTransportInterval(t *testing.T) {
	s := meshFixture(t)
	ctx, done, e := s.beginWorkspaceCall(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	observed := make(chan error, 1)
	go func() { close(entered); <-ctx.Done(); observed <- ctx.Err(); <-release; done() }()
	<-entered
	closed := make(chan struct{})
	go func() { s.closeWorkspaceStreams(); close(closed) }()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("workflow transport context not cancelled")
	}
	select {
	case <-closed:
		t.Fatal("shutdown returned before admitted callback joined")
	default:
	}
	// The manager and its ordinary database remain usable until the transport
	// interval acknowledges cancellation and joins.
	if _, e = s.Manager.Status(); e != nil {
		t.Fatal("database closed before native callback joined", e)
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("joined workflow did not finish close")
	}
	if _, _, e = s.beginWorkspaceCall(context.Background()); e == nil {
		t.Fatal("new workflow admitted after close")
	}
}
