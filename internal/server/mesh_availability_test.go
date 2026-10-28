package server

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

func awaitMeshAvailability(t *testing.T, observer *meshAvailability, state string) meshAvailabilityState {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		_, peers := observer.Snapshot()
		if len(peers) == 1 && peers[0].State == state {
			return peers[0]
		}
		select {
		case <-deadline.C:
			t.Fatalf("availability did not reach %s: %+v", state, peers)
		case <-tick.C:
		}
	}
}

func joinMobileFixtures(t *testing.T, servers ...*Server) {
	t.Helper()
	t.Cleanup(func() {
		for _, s := range servers {
			if err := s.closeStopMesh(); err != nil {
				t.Error("original mobile fixture mesh did not join", err)
			}
		}
	})
}

func TestMeshAvailabilityAutonomousReadOnlyRecovery(t *testing.T) {
	left, right, l, r := registryPair(t, mesh.Grant{}, mesh.Grant{Permissions: []string{"observe-host"}})
	joinMobileFixtures(t, left, right)
	before := l.Config()
	observer := newMeshAvailability(t.Context(), left, meshStartupAttempt{})
	t.Cleanup(observer.Close)
	fresh := awaitMeshAvailability(t, observer, "reachable")
	if fresh.Resources == nil || fresh.Resources.Host != r.Config().Identity.Host || fresh.Scope == nil || fresh.DeclarationState != "reported" || fresh.Declared == nil || fresh.Declared.Profile != "unspecified" || !reflect.DeepEqual(l.Config(), before) {
		t.Fatal("observation missing or trust changed", fresh)
	}
	if err := right.closeStopMesh(); err != nil {
		t.Fatal(err)
	}
	// Bring the fixture's next due time forward, retaining the actual worker,
	// pinned TLS requests and production failure/backoff path.
	observer.mu.Lock()
	observer.peers[fresh.Host].next = time.Time{}
	observer.mu.Unlock()
	offline := awaitMeshAvailability(t, observer, "offline")
	if offline.Resources != nil || offline.Scope != nil || offline.LastSuccess == nil || !offline.LastSuccess.Equal(*fresh.LastSuccess) || offline.NextAttempt == nil {
		t.Fatal("offline observation claimed current availability", offline)
	}
	if err := right.loadMesh(); err != nil {
		t.Fatal(err)
	}
	rejoined := awaitMeshAvailability(t, observer, "reachable")
	if !rejoined.LastSuccess.After(*fresh.LastSuccess) || !reflect.DeepEqual(l.Config(), before) {
		t.Fatal("rejoin changed trust or reused old observation", rejoined)
	}
	if err := left.closeStopMesh(); err != nil {
		t.Fatal(err)
	}
	if err := left.loadMesh(); err != nil {
		t.Fatal(err)
	}
	_, replaced := observer.Snapshot()
	if len(replaced) != 1 || (replaced[0].Resources != nil || replaced[0].Scope != nil) && (replaced[0].LastSuccess == nil || !replaced[0].LastSuccess.After(*rejoined.LastSuccess)) {
		t.Fatal("replacement local transport inherited old observations", replaced)
	}
	left.meshMu.Lock()
	l = left.mesh.store
	left.meshMu.Unlock()
	if err := l.Revoke(fresh.Host, "fixture revoke"); err != nil {
		t.Fatal(err)
	}
	observer.Invalidate(fresh.Host)
	_, peers := observer.Snapshot()
	if len(peers) != 0 {
		t.Fatal("revoked observations retained", peers)
	}
	observer.Close()
	select {
	case <-observer.done:
	default:
		t.Fatal("original observer not joined")
	}
}

func TestMeshAvailabilityDeniedIsDistinctFromOffline(t *testing.T) {
	left, right, _, _ := registryPair(t, mesh.Grant{}, mesh.Grant{})
	joinMobileFixtures(t, left, right)
	observer := newMeshAvailability(t.Context(), left, meshStartupAttempt{})
	t.Cleanup(observer.Close)
	denied := awaitMeshAvailability(t, observer, "denied")
	if denied.Failure != "forbidden" || denied.Resources != nil || denied.Scope != nil {
		t.Fatal(denied)
	}
}

func TestMeshAvailabilityRestoresOnlyOriginalStartupBind(t *testing.T) {
	s := meshFixture(t)
	joinMobileFixtures(t, s)
	status, err := s.Manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { occupied.Close() })
	address := occupied.Addr().String()
	store, err := mesh.Init(s.home, status.Host, "mobile-listener", address, address)
	if err != nil {
		t.Fatal(err)
	}
	before := store.Config()
	store.Close()
	startupErr := s.loadMesh()
	if !retryableMeshBind(startupErr) {
		t.Fatal("fixture did not produce native startup bind failure", startupErr)
	}
	observer := newMeshAvailability(t.Context(), s, meshStartupAttempt{Approval: before, Err: startupErr})
	t.Cleanup(observer.Close)
	occupied.Close()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		listener, _ := observer.Snapshot()
		if listener.State == "listening" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("original listener did not recover", listener)
		case <-tick.C:
		}
	}
	s.meshMu.Lock()
	after := s.mesh.store.Config()
	s.meshMu.Unlock()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("listener recovery rewrote identity or trust")
	}
	observer.Close()
	if err := s.closeStopMesh(); err != nil {
		t.Fatal(err)
	}
	// Startup recovery is consumed. A later closure cannot resurrect it.
	observer.restoreListener()
	s.meshMu.Lock()
	running := s.mesh != nil
	s.meshMu.Unlock()
	if running {
		t.Fatal("consumed startup duty resurrected a closed listener")
	}
}

func TestMeshAvailabilityBackoffBoundAndFailureClassification(t *testing.T) {
	for _, count := range []uint{0, 1, 2, 10, 1000000} {
		for range 32 {
			delay := recoveryDelay(count)
			if delay < time.Second || delay >= time.Minute {
				t.Fatal("retry frequency bound", count, delay)
			}
		}
	}
	if retryableMeshBind(errors.New("invalid identity")) || retryableMeshBind(context.DeadlineExceeded) || !retryableMeshBind(&net.OpError{Op: "listen", Err: syscall.EADDRNOTAVAIL}) {
		t.Fatal("non-bind error entered listener retry")
	}
	stamp := time.Now()
	age := meshReadAge{ObservationAt: stamp, StaleAfter: time.Second}
	age.advance(2 * time.Second)
	if !age.Stale || age.Age != 2*time.Second || !age.ObservationAt.Equal(stamp) {
		t.Fatal("resource timestamp relabelled", age)
	}
}

func TestMeshAvailabilityFreshnessUsesSuspendInclusiveClock(t *testing.T) {
	wall := time.Now().UTC()
	anchor := leaseclock.Sample{Boot: "fixture-boot", Tick: time.Second.Nanoseconds(), Wall: wall}
	// Wall rollback cannot make the elapsed native observation younger. This
	// controlled sample jump is not evidence of physical host suspension.
	afterWake := leaseclock.Sample{Boot: anchor.Boot, Tick: anchor.Tick + int64(time.Hour), Wall: wall.Add(-time.Hour)}
	elapsed, ok := nodeObservationElapsed(anchor, afterWake)
	if !ok || elapsed != time.Hour {
		t.Fatal("wall time controlled observation freshness", elapsed, ok)
	}
	p := &meshAvailabilityAttempt{}
	p.schedule(anchor, time.Minute)
	if p.due(anchor) || !p.due(afterWake) {
		t.Fatal("retry delay excluded suspend or used wall time")
	}
	for _, sample := range []leaseclock.Sample{{Boot: "replacement-boot", Tick: afterWake.Tick, Wall: wall}, {Boot: anchor.Boot, Tick: 0, Wall: wall}, {}} {
		if _, ok := nodeObservationElapsed(anchor, sample); ok {
			t.Fatal("unknown/regressed boot clock accepted")
		}
	}
}

func TestMeshAvailabilityInvalidPeerAndBoundedErrorProjection(t *testing.T) {
	left, right, l, r := registryPair(t, mesh.Grant{}, mesh.Grant{Permissions: []string{"observe-host"}})
	joinMobileFixtures(t, left, right)
	peer, err := l.Resolve(r.Config().Identity.Host)
	if err != nil {
		t.Fatal(err)
	}
	current := time.Now()
	if !availabilityPeerValidAt(peer, current) {
		t.Fatal("valid observation-only peer rejected")
	}
	for _, name := range []string{"expired", "unproven", "invalid-grant"} {
		t.Run(name, func(t *testing.T) {
			changed, now := peer, current
			if name == "expired" {
				now = peer.Identity.Expires.Add(time.Second)
			}
			if name == "unproven" {
				changed.Proven = false
			}
			if name == "invalid-grant" {
				changed.Grant.Limits.CPU = -1
			}
			out := projectAvailabilityPeer(meshAvailabilityState{State: "reachable", Resources: &meshResourceRead{}, Scope: &model.NodeAccessScope{}}, peer.Identity, changed, now)
			if out.State == "reachable" || out.Resources != nil || out.Scope != nil {
				t.Fatal("invalid current identity retained fresh resources", out)
			}
		})
	}
	for _, test := range []struct{ name, code string }{{"unknown-error-code", "arbitrary_remote_code"}, {"oversized-error-code", strings.Repeat("x", 4<<20)}} {
		t.Run(test.name, func(t *testing.T) {
			state, closed := availabilityFailure(model.Err(test.code, "ignored peer payload"))
			if state != "unavailable" || closed != "remote_error" || len(closed) > 64 {
				t.Fatal("unbounded peer error retained", len(closed))
			}
		})
	}
}

func TestMeshAvailabilityInvalidationJoinsBlockedListenerAttempt(t *testing.T) {
	s := meshFixture(t)
	joinMobileFixtures(t, s)
	status, _ := s.Manager.Status()
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	address := occupied.Addr().String()
	store, err := mesh.Init(s.home, status.Host, "listener-invalidate", address, address)
	if err != nil {
		t.Fatal(err)
	}
	before := store.Config()
	store.Close()
	startupErr := s.loadMesh()
	if !retryableMeshBind(startupErr) {
		t.Fatal(startupErr)
	}
	s.meshAdminMu.Lock()
	locked := true
	defer func() {
		if locked {
			s.meshAdminMu.Unlock()
		}
	}()
	observer := newMeshAvailability(t.Context(), s, meshStartupAttempt{Approval: before, Err: startupErr})
	t.Cleanup(observer.Close)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var attempt chan struct{}
	for attempt == nil {
		observer.mu.Lock()
		attempt = observer.listenerDone
		observer.mu.Unlock()
		if attempt != nil {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("listener did not wait for administration")
		case <-tick.C:
		}
	}
	invalidated := make(chan struct{})
	go func() { observer.Invalidate(""); close(invalidated) }()
	select {
	case <-invalidated:
	case <-time.After(2 * time.Second):
		t.Fatal("invalidation did not cancel and join blocked original listener")
	}
	select {
	case <-attempt:
	default:
		t.Fatal("original blocked listener did not join")
	}
	occupied.Close()
	s.meshAdminMu.Unlock()
	locked = false
	observer.restoreListener()
	s.meshMu.Lock()
	running := s.mesh != nil
	s.meshMu.Unlock()
	if running {
		t.Fatal("invalidated retry recreated listener after returning")
	}
}

func TestMeshAvailabilityStartupFailureDoesNotAdoptNewApproval(t *testing.T) {
	s := meshFixture(t)
	joinMobileFixtures(t, s)
	status, _ := s.Manager.Status()
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	address := occupied.Addr().String()
	store, err := mesh.Init(s.home, status.Host, "old-failed-approval", address, address)
	if err != nil {
		t.Fatal(err)
	}
	before := store.Config()
	store.Close()
	startupErr := s.loadMesh()
	if !retryableMeshBind(startupErr) {
		t.Fatal(startupErr)
	}
	store, err = mesh.OpenRecovery(s.home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Renew("fixture changes approved certificate after failure"); err != nil {
		t.Fatal(err)
	}
	store.Close()
	occupied.Close()
	observer := newMeshAvailability(t.Context(), s, meshStartupAttempt{Approval: before, Err: startupErr})
	t.Cleanup(observer.Close)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		listener, _ := observer.Snapshot()
		if listener.State == "disabled" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("old failure adopted a newer approval", listener)
		case <-tick.C:
		}
	}
	s.meshMu.Lock()
	running := s.mesh != nil
	s.meshMu.Unlock()
	if running {
		t.Fatal("new pin inherited an old failed listener attempt")
	}
}

func TestMeshAvailabilityRevokedGenerationCannotPublish(t *testing.T) {
	left, right, l, r := registryPair(t, mesh.Grant{}, mesh.Grant{Permissions: []string{"observe-host"}})
	joinMobileFixtures(t, left, right)
	peer, err := l.Resolve(r.Config().Identity.Host)
	if err != nil {
		t.Fatal(err)
	}
	left.meshMu.Lock()
	service := left.mesh
	left.meshMu.Unlock()
	observation, err := left.probeMeshAvailability(t.Context(), service, l.Config().Identity.Host, peer)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Revoke(peer.Identity.Host, "publication fence control"); err != nil {
		t.Fatal(err)
	}
	p := &meshAvailabilityAttempt{state: meshAvailabilityState{Host: peer.Identity.Host}}
	m := &meshAvailability{owner: left, ctx: t.Context(), peers: map[string]*meshAvailabilityAttempt{peer.Identity.Host: p}}
	joined := make(chan struct{})
	m.finish(service, peer, p, joined, observation, nil)
	<-joined
	if _, exists := m.peers[peer.Identity.Host]; exists {
		t.Fatal("revoked generation published an observation")
	}
}

func TestMeshAvailabilityCancellationJoinsOriginalProbe(t *testing.T) {
	for _, mode := range []string{"close", "revocation-callback"} {
		t.Run(mode, func(t *testing.T) {
			left, right, l, _ := registryPair(t, mesh.Grant{}, mesh.Grant{Permissions: []string{"observe-host"}})
			joinMobileFixtures(t, left, right)
			if err := right.closeStopMesh(); err != nil {
				t.Fatal(err)
			}
			store, err := mesh.Open(right.home)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			listener, err := net.Listen("tcp", store.Config().Listen)
			if err != nil {
				t.Fatal(err)
			}
			entered, exited, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var connMu sync.Mutex
			connections := map[net.Conn]bool{}
			var once sync.Once
			server := &http.Server{ReadHeaderTimeout: time.Second, ConnState: func(c net.Conn, state http.ConnState) {
				connMu.Lock()
				defer connMu.Unlock()
				if state == http.StateNew {
					connections[c] = false
				}
				if state == http.StateClosed {
					connections[c] = true
				}
			}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Without EOF on the bounded POST body, Go HTTP cannot reliably start
				// background connection reads to notice client cancellation. v1 lacked
				// this drain; the planned no-drain control retains that exact failure.
				if n, err := io.Copy(io.Discard, io.LimitReader(r.Body, 65537)); err != nil || n > 65536 {
					http.Error(w, "invalid fixture body", 400)
					return
				}
				once.Do(func() { close(entered) })
				select {
				case <-r.Context().Done():
					close(exited)
				case <-release:
				}
			})}
			serveDone := make(chan error, 1)
			go func() { serveDone <- server.Serve(tls.NewListener(listener, store.ServerTLS())) }()
			t.Cleanup(func() {
				close(release) // cleanup release never satisfies the cancellation oracle
				server.Close()
				select {
				case err := <-serveDone:
					if !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
						t.Error("original TLS fixture Serve failed", err)
					}
				case <-time.After(5 * time.Second):
					t.Error("original TLS fixture Serve not joined")
				}
				deadline := time.NewTimer(5 * time.Second)
				defer deadline.Stop()
				tick := time.NewTicker(10 * time.Millisecond)
				defer tick.Stop()
				for {
					connMu.Lock()
					complete := true
					for _, closed := range connections {
						complete = complete && closed
					}
					connMu.Unlock()
					if complete {
						break
					}
					select {
					case <-deadline.C:
						t.Error("original TLS fixture connection owners not closed")
						return
					case <-tick.C:
					}
				}
			})
			observer := newMeshAvailability(t.Context(), left, meshStartupAttempt{})
			t.Cleanup(observer.Close)
			if mode == "revocation-callback" {
				left.meshMu.Lock()
				service := left.mesh
				left.meshMu.Unlock()
				l.OnChange(func(host string) { observer.CancelPeer(host); service.revoke(host) })
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("original read did not enter pinned peer")
			}
			if mode == "revocation-callback" {
				left.meshMu.Lock()
				publicationHeld := true
				defer func() {
					if publicationHeld {
						left.meshMu.Unlock()
					}
				}()
				if err := l.Revoke(store.Config().Identity.Host, "original callback cancellation fixture"); err != nil {
					t.Fatal(err)
				}
				observer.mu.Lock()
				retained := len(observer.cancelled)
				var original <-chan struct{}
				for done := range observer.cancelled {
					original = done
				}
				observer.mu.Unlock()
				if retained != 1 {
					t.Fatal("callback did not retain exact original probe join", retained)
				}
				joined := make(chan struct{})
				go func() { observer.JoinCancelled(); close(joined) }()
				select {
				case <-joined:
					t.Fatal("callback join returned before its original publication owner completed")
				case <-time.After(20 * time.Millisecond):
				}
				left.meshMu.Unlock()
				publicationHeld = false
				select {
				case <-joined:
				case <-time.After(5 * time.Second):
					t.Fatal("callback cancellation did not join original owner")
				}
				select {
				case <-original:
				default:
					t.Fatal("callback join omitted original probe receipt")
				}
			}
			observer.Close()
			select {
			case <-observer.done:
			default:
				t.Fatal("observer worker not joined")
			}
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				t.Fatal("original remote read did not observe cancellation")
			}
		})
	}
}
