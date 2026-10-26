package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	api "github.com/inferal-oss/arm/internal/workspaceapi"
	"github.com/inferal-oss/arm/internal/workspacegraph"
)

func TestGraphRefreshCoalescesAndFollowerCancellationDoesNotRestart(t *testing.T) {
	s := &Server{}
	defer s.closeWorkspaceStreams()
	g, e := s.graphState()
	if e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	var calls atomic.Int64
	go func() {
		_, e := g.coalesce(t.Context(), "exact-workspace", func(context.Context) (api.RefreshResult, error) {
			calls.Add(1)
			close(entered)
			<-release
			return api.RefreshResult{Workspace: "exact-workspace"}, nil
		})
		result <- e
	}()
	<-entered
	follower, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e := g.coalesce(follower, "exact-workspace", func(context.Context) (api.RefreshResult, error) {
		t.Error("coalesced follower executed producer")
		return api.RefreshResult{}, nil
	}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	close(release)
	if e := <-result; e != nil || calls.Load() != 1 {
		t.Fatal("refresh replayed after cancelled follower", e, calls.Load())
	}
}
func TestGraphRefreshShutdownCancelsAndJoinsPublication(t *testing.T) {
	s := &Server{}
	g, e := s.graphState()
	if e != nil {
		t.Fatal(e)
	}
	ctx, done, e := s.beginWorkspaceCall(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		defer done()
		_, e := g.coalesce(ctx, "exact", func(ctx context.Context) (api.RefreshResult, error) {
			close(entered)
			<-ctx.Done()
			<-release
			return api.RefreshResult{}, ctx.Err()
		})
		result <- e
	}()
	<-entered
	closed := make(chan struct{})
	go func() { s.closeWorkspaceStreams(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("shutdown returned before producer joined")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join cancelled producer")
	}
	if e := <-result; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	g.publish("exact", []workspacegraph.Observation{{Report: workspacegraph.Report{Source: "source", Sequence: 1}}})
	if len(g.observations("exact")) != 0 {
		t.Fatal("cancelled lifecycle published witnesses")
	}
	if _, e = s.graphState(); e == nil {
		t.Fatal("shutdown reopened graph lifecycle")
	}
}
func TestGraphWitnessSameSequenceNeverRenewsDeadlineOrAliases(t *testing.T) {
	s := &Server{}
	defer s.closeWorkspaceStreams()
	g, e := s.graphState()
	if e != nil {
		t.Fatal(e)
	}
	o := workspacegraph.Observation{Boot: "boot", Deadline: time.Second, Report: workspacegraph.Report{Source: "source", Sequence: 1, Neighbors: []workspacegraph.Neighbor{{Host: "original"}}}}
	g.publish("exact", []workspacegraph.Observation{o})
	o.Deadline = time.Hour
	g.publish("exact", []workspacegraph.Observation{o})
	o.Report.Neighbors[0].Host = "mutated"
	got := g.observations("exact")
	if len(got) != 1 || got[0].Deadline != time.Second || got[0].Report.Neighbors[0].Host != "original" {
		t.Fatal(got)
	}
	got[0].Report.Neighbors[0].Host = "read-alias"
	if g.observations("exact")[0].Report.Neighbors[0].Host != "original" {
		t.Fatal("observation reader mutated live cache")
	}
	old := o
	old.Report.Sequence = 0
	g.publish("exact", []workspacegraph.Observation{old})
	if g.observations("exact")[0].Report.Sequence != 1 {
		t.Fatal("older publication replaced highwater")
	}
}
