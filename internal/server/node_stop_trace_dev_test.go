//go:build arm_devtrace

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/nodestop"
)

type stopTraceOwner struct {
	home   string
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func (o *stopTraceOwner) wait(t *testing.T, allowError bool) {
	t.Helper()
	select {
	case <-o.done:
		if o.err != nil && !allowError {
			t.Fatal(o.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("real Serve owner did not join; no PID fallback")
	}
}
func traceStart(t *testing.T, home string, capture bool) (*stopTraceOwner, *Server, model.Status) {
	t.Helper()
	if home == "" {
		var err error
		home, err = filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	if capture {
		nodestop.DevBegin(home)
	}
	ctx, cancel := context.WithCancel(t.Context())
	o := &stopTraceOwner{home: home, cancel: cancel, done: make(chan struct{})}
	go func() { o.err = Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 4}); close(o.done) }()
	t.Cleanup(func() { cancel(); o.wait(t, true) })
	if capture {
		t.Cleanup(func() {
			if t.Failed() {
				traceFailurePrefix(t, "startup-"+strings.ReplaceAll(t.Name(), "/", "-"), home)
			}
		})
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var status model.Status
		if err := client.Call(t.Context(), home, "status", struct{}{}, &status); err == nil {
			stopTraceServers.Lock()
			s := stopTraceServers.live[home]
			stopTraceServers.Unlock()
			if !nodestop.DevEnabled(home) || s != nil {
				return o, s, status
			}
		}
		select {
		case <-o.done:
			t.Fatalf("real startup failed: %v", o.err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("real private Serve did not publish")
	return nil, nil, model.Status{}
}
func tracePeer(s *Server, source string) mesh.Peer {
	for _, p := range s.mesh.store.Config().Peers {
		if p.Identity.Host == source {
			return p
		}
	}
	return mesh.Peer{}
}
func traceGrant(s *Server, source string) {
	nodestop.DevEmit(s.home, "grant.observed", tracePeer(s, source))
}
func traceConsent(home string, p nodestop.Plan) nodestop.Apply {
	hash, _ := p.Digest()
	nodestop.DevEmit(home, "operator.reviewed", struct {
		Digest string        `json:"digest"`
		Plan   nodestop.Plan `json:"plan"`
	}{hash, p})
	return nodestop.Apply{Key: p.Key, Plan: p, ReviewSHA256: hash}
}
func tracePlan(t *testing.T, leftHome string, right mesh.Identity, peer mesh.Peer, key string, policy model.ShutdownRequest) (nodestop.Plan, error) {
	t.Helper()
	raw, _ := json.Marshal(stopPlanRequest{Key: key, Reason: "actual finite Go trace", Policy: policy})
	var result nodestop.Prepared
	err := client.Call(t.Context(), leftHome, "mesh_dispatch", struct {
		Host    string
		Request mesh.Request
	}{right.Host, mesh.Request{Operation: "node_stop_plan", Expected: &mesh.ExpectedEnrollment{Pair: peer.Pair, Revision: peer.Revision}, Params: raw}}, &result)
	return result.Plan, err
}
func traceApply(t *testing.T, leftHome string, right mesh.Identity, peer mesh.Peer, q nodestop.Apply) error {
	t.Helper()
	raw, _ := json.Marshal(q)
	var result nodestop.Record
	return client.Call(t.Context(), leftHome, "mesh_dispatch", struct {
		Host    string
		Request mesh.Request
	}{right.Host, mesh.Request{Operation: "node_stop_apply", Key: q.Key, Expected: &mesh.ExpectedEnrollment{Pair: peer.Pair, Revision: peer.Revision}, Params: raw}}, &result)
}
func traceOutcome(home, operation string, err error) {
	out := struct {
		Operation string `json:"operation"`
		Error     string `json:"error"`
		Code      string `json:"code"`
		ErrorType string `json:"error_type"`
	}{Operation: operation}
	if err != nil {
		out.Error = err.Error()
		out.ErrorType = fmt.Sprintf("%T", err)
		var api *model.Error
		if errors.As(err, &api) {
			out.Code = api.Code
		}
	}
	nodestop.DevEmit(home, "driver.outcome", out)
}
func traceRestart(t *testing.T, home string) {
	t.Helper()
	r, needed, err := PrepareNodeRestart(t.Context(), home, "actual explicit local trace restart")
	if err != nil || !needed || r == nil {
		t.Fatal("real restart review missing", needed, err)
	}
	defer r.Close()
	plan, err := r.Plan()
	if err != nil {
		t.Fatal(err)
	}
	nodestop.DevEmit(home, "operator.restart-reviewed", plan.Review)
	if err = r.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
}
func traceFinish(t *testing.T, name, home string) {
	t.Helper()
	nodestop.DevEmit(home, "driver.end", struct{}{})
	facts, err := nodestop.DevEvents()
	if err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("ARM_NODE_STOP_TRACE_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(struct {
		Schema   string             `json:"schema"`
		Scenario string             `json:"scenario"`
		Facts    []nodestop.DevFact `json:"facts"`
	}{"arm.node-stop.actual-go/v1", name, facts}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, name+".json")
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.Write(raw)
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		t.Fatal(err)
	}
	t.Log("actual node-stop raw trace", file)
	// Stop recording before fixture cleanup. Prefix endings are explicit and
	// cleanup cannot silently alter the captured unknown-work state.
	nodestop.DevBegin("")
}

// These are actual native private Serve lifetimes, real mesh enrollment/RPCs,
// SQLite commits, component teardown, marker writes and fixed native inspection.
// Human consent, scheduling faults and clock input are explicitly test inputs;
// authentication and CLI process acceptance have a separate integration suite.
func TestNodeStopActualDevTrace(t *testing.T) {
	scenarios := []string{"two-generation", "two-generation-no-source-observation", "force-owned", "wait-owned", "active-default", "unknown-work", "expired-permit", "clock-fault", "revoked-grant", "changed-grant", "changed-generation", "interrupted-history", "restart-expired", "restart-clock-fault"}
	for _, name := range scenarios {
		t.Run(name, func(t *testing.T) {
			leftOwner, _, leftStatus := traceStart(t, "", false)
			rightOwner, s, rightStatus := traceStart(t, "", true)
			t.Cleanup(func() {
				if t.Failed() {
					traceFailurePrefix(t, name, s.home)
				}
			})
			left := stopEnablePrivateMesh(t, leftOwner.home, leftStatus.Host, "left-enable")
			right := stopEnablePrivateMesh(t, rightOwner.home, rightStatus.Host, "right-enable")
			peer := stopPairPrivate(t, leftOwner.home, rightOwner.home, left, right, strings.Repeat("a", 64))
			traceGrant(s, left.Host)
			s.Manager.DevNodeStopSnapshot("baseline")
			nodestop.DevEmit(s.home, "driver.begin", struct {
				Generation string `json:"generation"`
				Source     string `json:"source"`
			}{s.generation, left.Host})
			if name == "expired-permit" || name == "clock-fault" {
				s.Manager.DevNodeStopClock(10, nil)
				nodestop.DevEmit(s.home, "input.clock-control", struct {
					Tick int64 `json:"tick"`
				}{10})
			}
			policy := model.ShutdownRequest{}
			var lease model.Lease
			if name == "force-owned" || name == "wait-owned" || name == "active-default" {
				var err error
				lease, err = s.Manager.Acquire(model.Request{Key: "trace-owned", Reason: "actual cooperative CPU claim", Resources: model.Resources{CPU: 1}, TTL: time.Minute}, nil)
				if err != nil {
					t.Fatal(err)
				}
				if name == "force-owned" {
					policy.Force = true
				} else if name == "wait-owned" {
					policy.Wait = true
				}
			}
			if name == "unknown-work" {
				var err error
				lease, err = s.Manager.Acquire(model.Request{Key: "trace-port", Kind: "ports", Reason: "actual unresolved cooperative listener", TTL: time.Minute, Ports: []model.PortClaim{{Name: "test", Network: "tcp4", Address: "127.0.0.1", Mode: "cooperative"}}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(lease.Ports[0].Port)))
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				lease, err = s.Manager.End(lease.ID, "revoked")
				if err != nil || lease.State != "needs_attention" {
					t.Fatal("real cooperative ownership loss missing", lease.State, err)
				}
			}
			plan, err := tracePlan(t, leftOwner.home, right, peer, "first", policy)
			traceOutcome(s.home, "prepare", err)
			if err != nil {
				t.Fatal(err)
			}
			q := traceConsent(s.home, plan)
			switch name {
			case "expired-permit":
				s.Manager.DevNodeStopClock(10+int64(nodestop.ReviewTTL), nil)
			case "clock-fault":
				s.Manager.DevNodeStopClock(9, nil)
			case "revoked-grant":
				if err = s.mesh.store.Revoke(left.Host, "actual trace revocation"); err != nil {
					t.Fatal(err)
				}
				traceGrant(s, left.Host)
			case "changed-grant":
				if err = s.mesh.store.Revoke(left.Host, "actual trace grant replacement"); err != nil {
					t.Fatal(err)
				}
				traceGrant(s, left.Host)
				if err = client.Call(t.Context(), leftOwner.home, "mesh_revoke", struct{ Host, Reason string }{right.Host, "actual counterpart replacement"}, nil); err != nil {
					t.Fatal(err)
				}
				peer = stopPairPrivate(t, leftOwner.home, rightOwner.home, left, right, strings.Repeat("b", 64))
				traceGrant(s, left.Host)
			case "changed-generation":
				q.Plan.Target.Generation = strings.Repeat("f", 32)
				q.ReviewSHA256, _ = q.Plan.Digest()
			case "interrupted-history":
				stopTracePause.Lock()
				stopTracePause.home = s.home
				stopTracePause.reached = make(chan struct{})
				stopTracePause.release = make(chan struct{})
				stopTracePause.Unlock()
			}
			if name == "revoked-grant" {
				wireErr := traceApply(t, leftOwner.home, right, peer, q)
				traceOutcome(s.home, "revoked-wire-attempt", wireErr)
				traceRequireCode(t, wireErr, "remote_outcome_unknown")
				// Transport unknown is retained independently. Only the real
				// separate destination grant snapshot proves its guard path.
				raw, _ := json.Marshal(q)
				_, err = s.mesh.store.WithPeerSnapshot(left.Host, func(current mesh.Peer, local mesh.Identity, peers []mesh.PeerImpact) (any, error) {
					return s.mesh.dispatchStop(t.Context(), current, mesh.Request{Operation: "node_stop_apply", Key: q.Key, Host: right.Host, Expected: &mesh.ExpectedEnrollment{Pair: peer.Pair, Revision: peer.Revision}, Params: raw}, local, peers)
				})
			} else {
				err = traceApply(t, leftOwner.home, right, peer, q)
			}
			traceOutcome(s.home, "apply", err)
			refusal := name == "active-default" || name == "unknown-work" || name == "expired-permit" || name == "clock-fault" || name == "revoked-grant" || name == "changed-grant" || name == "changed-generation"
			if refusal {
				codes := map[string]string{"active-default": "active_leases", "unknown-work": "node_stop_blocked", "expired-permit": "node_stop_review_expired", "clock-fault": "node_stop_review_expired", "revoked-grant": "forbidden", "changed-grant": "node_context_changed", "changed-generation": "node_generation_changed"}
				traceRequireCode(t, err, codes[name])
				s.Manager.DevNodeStopSnapshot("refused")
				if name == "expired-permit" {
					_, e := s.Manager.DevRepeatNodeStopPlan(plan)
					traceOutcome(s.home, "repeat-exact-expired-prepare", e)
					traceRequireCode(t, e, "node_stop_review_expired")
					s.Manager.DevNodeStopSnapshot("expired-repeat-refused")
				}
				if name == "clock-fault" {
					s.Manager.DevNodeStopClock(20, nil)
					err = traceApply(t, leftOwner.home, right, peer, q)
					traceOutcome(s.home, "revive-clock", err)
					traceRequireCode(t, err, "node_stop_review_expired")
					s.Manager.DevNodeStopSnapshot("refused-again")
				}
				if _, present, e := s.Manager.NodeStopDesiredState(); e != nil || present {
					t.Fatal("refusal wrote durable stop", present, e)
				}
				traceFinish(t, name, s.home)
				return
			}
			if err != nil {
				t.Fatal("actual stop apply failed before positive history", err)
			}
			if name == "wait-owned" {
				if _, err = s.Manager.End(lease.ID, "released"); err != nil {
					t.Fatal(err)
				}
			}
			if name == "interrupted-history" {
				stopTracePause.Lock()
				reached := stopTracePause.reached
				stopTracePause.Unlock()
				select {
				case <-reached:
				case <-time.After(5 * time.Second):
					t.Fatal("real worker did not reach scheduling cut")
				}
				rightOwner.cancel()
				rightOwner.wait(t, false)
				if _, err = InspectNodeStop(t.Context(), s.home, plan.Target, plan.Key); err == nil {
					t.Fatal("accepted-only history fabricated native completion")
				}
				traceOutcome(s.home, "inspect-interrupted", err)
				traceRestart(t, s.home)
				_, next, _ := traceStart(t, s.home, false)
				// Restart publication remains traceable in the selected home even though
				// capture=false avoids replacing the existing recorder.
				peer = tracePeer(next, left.Host)
				later, e := tracePlan(t, leftOwner.home, right, peer, "second", model.ShutdownRequest{})
				traceOutcome(s.home, "prepare-after-interrupted", e)
				if e != nil {
					t.Fatal("actual second Prepare must reach retained history census", e)
				}
				e = traceApply(t, leftOwner.home, right, peer, traceConsent(s.home, later))
				traceOutcome(s.home, "apply-after-interrupted", e)
				traceRequireCode(t, e, "node_stop_blocked")
				next.Manager.DevNodeStopSnapshot("history-refused")
				traceFinish(t, name, s.home)
				return
			}
			rightOwner.wait(t, false)
			if name != "two-generation-no-source-observation" {
				if _, err = InspectNodeStop(t.Context(), s.home, plan.Target, plan.Key); err != nil {
					t.Fatal("actual original lock/marker/absence observation failed", err)
				}
			}
			err = Serve(t.Context(), s.home, "127.0.0.1:0", model.Resources{CPU: 4})
			traceOutcome(s.home, "ordinary-start-stopped-refused", err)
			if err == nil {
				t.Fatal("ordinary startup ignored durable stopped intent")
			}
			if name == "restart-expired" || name == "restart-clock-fault" {
				r, needed, e := PrepareNodeRestart(t.Context(), s.home, "actual refusal trace restart")
				if e != nil || !needed || r == nil {
					t.Fatal(needed, e)
				}
				defer r.Close()
				rp, e := r.Plan()
				if e != nil {
					t.Fatal(e)
				}
				nodestop.DevEmit(s.home, "operator.restart-reviewed", rp.Review)
				retained := r.elapsed.DevSnapshot()
				if name == "restart-expired" {
					r.elapsed.DevClockControl(retained.First+int64(nodestop.ReviewTTL), nil)
				} else {
					r.elapsed.DevClockControl(retained.Last-1, nil)
				}
				e = r.Apply(t.Context())
				traceOutcome(s.home, "restart-refused", e)
				want := "retained-review-expired"
				if name == "restart-clock-fault" {
					want = "retained-clock-fault"
				}
				kind, matched := r.elapsed.DevFailureIdentity(e)
				if !matched || kind != want || r.attempted {
					t.Fatal("restart did not return the exact retained first Apply failure", kind, matched, r.attempted, e)
				}
				traceFinish(t, name, s.home)
				return
			}
			if name == "two-generation" || name == "two-generation-no-source-observation" {
				traceRestart(t, s.home)
				nextOwner, next, _ := traceStart(t, s.home, false)
				peer = tracePeer(next, left.Host)
				next.Manager.DevNodeStopSnapshot("second-baseline")
				_, e := InspectNodeStop(t.Context(), s.home, plan.Target, plan.Key)
				traceOutcome(s.home, "old-completion-inspection-current-authority-busy", e)
				if e == nil {
					t.Fatal("replacement active authority was reported absent")
				}
				second, e := tracePlan(t, leftOwner.home, right, peer, "second", model.ShutdownRequest{})
				if e != nil {
					t.Fatal(e)
				}
				if e = traceApply(t, leftOwner.home, right, peer, traceConsent(s.home, second)); e != nil {
					t.Fatal(e)
				}
				nextOwner.wait(t, false)
				if _, e = InspectNodeStop(t.Context(), s.home, second.Target, second.Key); e != nil {
					t.Fatal(e)
				}
			}
			traceFinish(t, name, s.home)
		})
	}
}

// A failed fixture retains the exact prefix before fixture cancellation. It
// intentionally lacks driver.end and cannot be exported as a completed trace.
func traceFailurePrefix(t *testing.T, name, home string) {
	if !nodestop.DevEnabled(home) {
		return
	}
	nodestop.DevEmit(home, "driver.failure", struct {
		Failed bool `json:"failed"`
	}{true})
	facts, err := nodestop.DevEvents()
	if err != nil {
		t.Log("capture failure", err)
	}
	dir := os.Getenv("ARM_NODE_STOP_TRACE_DIR")
	if dir == "" {
		t.Log("failure prefix retained in recorder", len(facts), "facts; set ARM_NODE_STOP_TRACE_DIR for a durable artifact")
		return
	}
	raw, e := json.MarshalIndent(struct {
		Schema   string             `json:"schema"`
		Scenario string             `json:"scenario"`
		Facts    []nodestop.DevFact `json:"facts"`
	}{"arm.node-stop.actual-go/v1", name + "-failed", facts}, "", "  ")
	if e != nil {
		t.Log(e)
		return
	}
	f, e := os.OpenFile(filepath.Join(dir, name+".failed.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Log(e)
		return
	}
	_, e = f.Write(raw)
	e = errors.Join(e, f.Sync(), f.Close())
	if e != nil {
		t.Log(e)
	}
	nodestop.DevBegin("")
}

func traceRequireCode(t *testing.T, err error, want string) {
	t.Helper()
	var api *model.Error
	if !errors.As(err, &api) || api.Code != want {
		t.Fatalf("actual intended typed refusal %q required, got %T %v", want, err, err)
	}
}

type stopTraceCountQuery struct{ reads int }

func (q *stopTraceCountQuery) Query(string, ...any) (*sql.Rows, error) {
	q.reads++
	return nil, fmt.Errorf("selected-home observer read control")
}
func (q *stopTraceCountQuery) QueryRow(string, ...any) *sql.Row {
	q.reads++
	panic("unexpected QueryRow in observer guard control")
}
func TestNodeStopTraceUnrelatedHomeInert(t *testing.T) {
	selected, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other := selected + "-unrelated"
	nodestop.DevBegin(selected)
	defer nodestop.DevBegin("")
	sqlBefore, clockBefore := nodestop.DevObserverReadCounts()
	q := &stopTraceCountQuery{}
	if value := traceStopReadRows(other, q); value != nil {
		t.Fatal("unrelated home yielded ledger")
	}
	traceRestartCAS(other, nodestop.State{}, q, nil)
	sqlAfter, clockAfter := nodestop.DevObserverReadCounts()
	if q.reads != 0 || sqlAfter != sqlBefore || clockAfter != clockBefore {
		t.Fatal("unrelated home performed observer-only SQL/elapsed reads", q.reads, sqlAfter-sqlBefore, clockAfter-clockBefore)
	}
	// Positive control proves that these counters/Queryer observe actual reads.
	if value := traceStopReadRows(selected, q); value == nil || q.reads != 1 {
		t.Fatal("selected home did not perform real observer query", q.reads)
	}
	traceRestartCAS(selected, nodestop.State{}, q, nil)
	sqlAfter, clockAfter = nodestop.DevObserverReadCounts()
	if q.reads != 2 || sqlAfter-sqlBefore != 2 || clockAfter-clockBefore != 1 {
		t.Fatal("selected-home read controls inert", q.reads, sqlAfter-sqlBefore, clockAfter-clockBefore)
	}
}
