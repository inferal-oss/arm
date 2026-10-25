//go:build arm_devtrace

package server

import (
	"context"
	"sync"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
)

var stopTraceServers = struct {
	sync.Mutex
	live map[string]*Server
}{live: map[string]*Server{}}

func traceStopPublished(s *Server) {
	if s == nil || !nodestop.DevEnabled(s.home) {
		return
	}
	stopTraceServers.Lock()
	stopTraceServers.live[s.home] = s
	stopTraceServers.Unlock()
	nodestop.DevEmit(s.home, "serve.published", struct {
		Generation string `json:"generation"`
	}{s.generation})
	s.Manager.DevNodeStopSnapshot("published")
}
func traceStopServeEnd(s *Server, err error) {
	if s == nil {
		return
	}
	stopTraceServers.Lock()
	delete(stopTraceServers.live, s.home)
	stopTraceServers.Unlock()
	if !nodestop.DevEnabled(s.home) {
		return
	}
	out := struct {
		Generation string `json:"generation"`
		Error      string `json:"error"`
	}{Generation: s.generation}
	if err != nil {
		out.Error = err.Error()
	}
	nodestop.DevEmit(s.home, "serve.return", out)
}
func traceStopReadRows(home string, q nodestop.Queryer) any {
	if !nodestop.DevEnabled(home) {
		return nil
	}
	return nodestop.DevReadLedger(q)
}
func traceStopObserved(home string, observation NodeStopObservation, ledger any) {
	nodestop.DevEmit(home, "native.observed", struct {
		Observation NodeStopObservation `json:"observation"`
		Ledger      any                 `json:"ledger"`
	}{observation, ledger})
}
func traceRestartReview(r *NodeRestartReview, kind string, err error) {
	if r == nil || !nodestop.DevEnabled(r.home) {
		return
	}
	// Called while r.mu is held (or before the new object is published). The
	// elapsed snapshot reads only ticks retained by actual Check calls.
	out := struct {
		Expected             nodestop.State      `json:"expected"`
		Review               nodestop.Restart    `json:"review"`
		Elapsed              nodestop.DevElapsed `json:"elapsed"`
		Attempted            bool                `json:"attempted"`
		Closed               bool                `json:"closed"`
		Error                string              `json:"error"`
		FailureKind          string              `json:"failure_kind"`
		FailureMatchesReturn bool                `json:"failure_matches_return"`
	}{Expected: r.expected, Review: r.review, Elapsed: r.elapsed.DevSnapshot(), Attempted: r.attempted, Closed: r.closed}
	if err != nil {
		out.Error = err.Error()
		out.FailureKind, out.FailureMatchesReturn = r.elapsed.DevFailureIdentity(err)
	}
	nodestop.DevEmit(r.home, "restart."+kind, out)
}
func traceRestartCAS(home string, state nodestop.State, q nodestop.Queryer, elapsed *nodestop.ElapsedReview) {
	if !nodestop.DevEnabled(home) {
		return
	}
	nodestop.DevEmit(home, "restart.cas", struct {
		State   nodestop.State      `json:"state"`
		Ledger  nodestop.DevLedger  `json:"ledger"`
		Elapsed nodestop.DevElapsed `json:"elapsed"`
	}{state, nodestop.DevReadLedger(q), elapsed.DevSnapshot()})
}

var stopTracePause = struct {
	sync.Mutex
	home    string
	reached chan struct{}
	release chan struct{}
}{}

// A development scheduling control, executed by the real worker outside every
// manager/grant/gate lock. Cancelling Serve while paused exercises a real
// interrupted lifetime; this never emits synthetic successful closure stages.
func traceStopWorkerPause(s *Server, ctx context.Context) bool {
	stopTracePause.Lock()
	if stopTracePause.home != s.home {
		stopTracePause.Unlock()
		return false
	}
	reached, release := stopTracePause.reached, stopTracePause.release
	stopTracePause.home = ""
	stopTracePause.Unlock()
	close(reached)
	nodestop.DevEmit(s.home, "worker.paused", struct{}{})
	select {
	case <-ctx.Done():
		return true
	case <-release:
		return false
	}
}

func traceStopTeardown(s *Server, err error) {
	if s == nil || !nodestop.DevEnabled(s.home) {
		return
	}
	out := struct {
		Generation string `json:"generation"`
		Error      string `json:"error"`
	}{Generation: s.generation}
	if err != nil {
		out.Error = err.Error()
	}
	nodestop.DevEmit(s.home, "serve.teardown", out)
}
func traceStopMarker(home string, r nodestop.Record, c nodestop.Closed, lock model.FileIdentity, hash string) {
	nodestop.DevEmit(home, "marker.matched", struct {
		Record nodestop.Record    `json:"record"`
		Closed nodestop.Closed    `json:"closed"`
		Lock   model.FileIdentity `json:"lock"`
		SHA256 string             `json:"sha256"`
	}{r, c, lock, hash})
}
func traceStopGrant(home string, peer mesh.Peer, local mesh.Identity) {
	nodestop.DevEmit(home, "grant.authenticated", struct {
		Peer  mesh.Peer     `json:"peer"`
		Local mesh.Identity `json:"local"`
	}{peer, local})
}

func traceStopStartupRefused(home string, state nodestop.State) {
	if nodestop.DevEnabled(home) {
		nodestop.DevEmit(home, "startup.stopped-refused", state)
	}
}
