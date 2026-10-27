package meshops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/mesh"
	"getarm.org/internal/meshssh"
	"getarm.org/internal/model"
	"getarm.org/internal/nodestop"
)

// These are transport-boundary frame controls, not native SSH/closure evidence.
// Real target ledger coverage and actual native tests have separate gates.
func recoveryFrames(t *testing.T) (StopReview, nodestop.Observation, mesh.Identity, mesh.Identity) {
	t.Helper()
	target := nodestop.Target{Host: strings.Repeat("1", 32), Certificate: strings.Repeat("a", 64), SPKI: strings.Repeat("b", 64), Owner: "501", Home: "/private/observer-test", Directory: model.FileIdentity{Device: 7, Inode: 9, Owner: 501}, Generation: strings.Repeat("3", 32), Startup: nodestop.StartupContract}
	b, err := nodestop.NewBoundary(target)
	if err != nil {
		t.Fatal(err)
	}
	actor := nodestop.Actor{Host: strings.Repeat("2", 32), Certificate: strings.Repeat("c", 64), SPKI: strings.Repeat("d", 64), Pair: strings.Repeat("4", 64), GrantRevision: 1}
	p, err := b.Prepare("original-stop", actor, 1, model.ShutdownRequest{}, "frame-only original stop", nodestop.Impact{Complete: true, SHA256: strings.Repeat("5", 64), Obligations: []nodestop.Obligation{}, Blockers: []nodestop.Blocker{}, Peers: []string{}}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	record := nodestop.Record{Schema: nodestop.RecordSchema, Plan: p, Digest: hash, Shutdown: strings.Repeat("6", 32), DesiredRevision: 2, Phase: "drain-ready", Created: p.Observed, Updated: p.Observed}
	obs := nodestop.Observation{Record: record, Desired: nodestop.State{Schema: nodestop.StateSchema, Revision: 2, Running: false, Target: target, Key: p.Key, Digest: hash}, ServerJoined: true, OriginalGenerationAbsent: true}
	review := StopReview{Operation: "stop-arm-node", Key: p.Key, Plan: p, SHA256: hash, HistoryCapacity: 128, CompletionCheck: StopCompletionCheck{Available: true, Detail: "frame-only verified observer"}}
	remote := mesh.Identity{Host: target.Host, Owner: target.Owner, Fingerprint: target.Certificate, SPKI: target.SPKI}
	own := mesh.Identity{Host: actor.Host, Owner: target.Owner, Fingerprint: actor.Certificate, SPKI: actor.SPKI}
	return review, obs, remote, own
}
func readyFrame(target nodestop.Target) StopObserverReady {
	return StopObserverReady{Protocol: StopObserverProtocol, Supported: true, Target: target, Runtime: mesh.RuntimeProof{Host: target.Host, UID: target.Owner, SHA256: strings.Repeat("7", 64), Size: 1234, Version: "devel", Protocol: mesh.LifecycleProtocol, Method: "startup-open-file-snapshot", Captured: time.Now().UTC()}}
}
func TestObserverReadinessRequiresExplicitFreshProtocolAndExactContext(t *testing.T) {
	review, _, _, _ := recoveryFrames(t)
	base := readyFrame(review.Plan.Target)
	if err := base.Validate(review.Plan.Target); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"unsupported", "old-protocol", "wrong-owner", "wrong-generation", "wrong-home", "missing-image"} {
		t.Run(kind, func(t *testing.T) {
			q := base
			switch kind {
			case "unsupported":
				q.Supported = false
			case "old-protocol":
				q.Protocol = "legacy"
			case "wrong-owner":
				q.Runtime.UID = "999"
			case "wrong-generation":
				q.Target.Generation = strings.Repeat("8", 32)
			case "wrong-home":
				q.Target.Home = "/private/other"
			case "missing-image":
				q.Runtime.SHA256 = ""
			}
			if err := q.Validate(review.Plan.Target); err == nil {
				t.Fatal("unverified observer accepted")
			}
		})
	}
	conn := &mesh.LifecycleOperation{RemoteHome: review.Plan.Target.Home, SSHLocator: "observer-fixture.invalid", Binary: "arm", SSHAccount: "native-test-account", SSHHostKey: "native-test-host-key"}
	calls := 0
	c := Coordinator{Context: t.Context(), SSH: func(context.Context, string, string, any, meshssh.Identity, any) (meshssh.Identity, error) {
		calls++
		return meshssh.Identity{}, errors.New("old helper has no probe action")
	}}
	if c.probeStopObserver(conn, review.Plan.Target).Available || calls != 1 {
		t.Fatal("remembered path or equal devel version counted as readiness", calls)
	}
	c.SSH = func(_ context.Context, _, _ string, q any, identity meshssh.Identity, out any) (meshssh.Identity, error) {
		calls++
		request, ok := q.(StopProbeRequest)
		if !ok || request.Action != "node_stop_probe" || request.Target != review.Plan.Target {
			t.Fatal("probe widened", q)
		}
		raw, _ := json.Marshal(base)
		*(out.(*json.RawMessage)) = raw
		return identity, nil
	}
	if !c.probeStopObserver(conn, review.Plan.Target).Available || calls != 2 {
		t.Fatal("fresh exact observer proof refused", calls)
	}
}
func lookupFixture(t *testing.T) (*Service, StopReview, nodestop.Observation, *mesh.Peer, *int, *int) {
	t.Helper()
	review, obs, remote, own := recoveryFrames(t)
	home := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	peer := &mesh.Peer{Identity: remote, Pair: review.Plan.Actor.Pair, Revision: 1, State: "active", Proven: true}
	conn := mesh.LifecycleOperation{Key: "retained-route", Kind: "connect", Digest: strings.Repeat("9", 64), At: time.Now().UTC(), Phase: "connected", LocalHost: own.Host, RemoteHost: remote.Host, LocalIdentity: &own, RemoteIdentity: &remote, Pair: peer.Pair, SSHLocator: "observer-fixture.invalid", SSHAccount: "native-test-account", SSHHostKey: "native-test-host-key", RemoteHome: review.Plan.Target.Home, Binary: "arm", Reason: "retained frame-only native route"}
	if err := mesh.SaveLifecycleOperation(home, conn); err != nil {
		t.Fatal(err)
	}
	q := StartRequest{Key: review.Key, Kind: "stop", Stop: &StopDraft{Host: remote.Host, Reason: review.Plan.Reason, Policy: review.Plan.Policy}}
	digest, _ := mesh.ReviewDigest(*q.Stop)
	guard, err := mesh.ClaimCoordination(home, q.Key, q.Kind, digest, "host:"+remote.Host)
	if err != nil {
		t.Fatal(err)
	}
	rawRequest, _ := json.Marshal(q)
	rawReview, _ := json.Marshal(review)
	hash, _ := mesh.ReviewDigest(review)
	unknown, _ := json.Marshal(StopResult{Operation: "stop-arm-node", Key: q.Key, Status: "unknown", Detail: "frame-only lost reply"})
	if err = guard.Change(func(op *mesh.Coordination) error {
		op.Request = rawRequest
		op.Review = rawReview
		op.ReviewHash = hash
		op.ReviewRevision = op.Revision + 1
		op.Approved = true
		op.Phase = "incomplete"
		op.Result = unknown
		op.Worker = ""
		op.WorkerExpires = time.Time{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rpc, ssh := new(int), new(int)
	service := &Service{Context: t.Context(), Home: home}
	service.New = func(ctx context.Context) *Coordinator {
		return &Coordinator{Context: ctx, Home: home, Inspect: func() (mesh.LifecycleInspection, error) {
			peers := []mesh.Peer{*peer}
			if peer.Identity.Host == "" {
				peers = nil
			}
			return mesh.LifecycleInspection{Running: true, Identity: &own, Config: &mesh.Config{Identity: own, Peers: peers}}, nil
		}, Call: func(context.Context, string, any, any) error {
			*rpc++
			return model.Err("host_unavailable", "listener deliberately offline")
		}, SSH: func(_ context.Context, _, _ string, q any, identity meshssh.Identity, out any) (meshssh.Identity, error) {
			*ssh++
			request, ok := q.(StopInspectRequest)
			if !ok || request.Action != "node_stop_inspect" || request.Key != review.Key || request.Target != review.Plan.Target {
				t.Fatal("lookup performed non-original action", q)
			}
			raw, _ := json.Marshal(obs)
			return identity, json.Unmarshal(raw, out)
		}}
	}
	return service, review, obs, peer, rpc, ssh
}
func TestOfflineStopLookupUsesFixedNativeReadAndCompletionNeverRegresses(t *testing.T) {
	service, review, _, _, rpc, ssh := lookupFixture(t)
	old, err := service.Get(review.Key)
	if err != nil {
		t.Fatal(err)
	}
	done, err := service.LookupStop(review.Key, old.Revision)
	if err != nil || done.Phase != "completed" || *rpc != 1 || *ssh != 1 {
		t.Fatal(done, err, *rpc, *ssh)
	}
	if !done.LookupOnly {
		t.Fatal("completed original key became effect-capable")
	}
	compact := func(raw json.RawMessage) string {
		var out bytes.Buffer
		if err := json.Compact(&out, raw); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	initialCompact := compact(done.Result)
	basePath, err := mesh.LifecycleOperationPath(service.Home, review.Key)
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(filepath.Dir(basePath), "coordinator-"+filepath.Base(basePath))
	receiptBefore, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	infoBefore, err := os.Stat(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	service.New = func(context.Context) *Coordinator {
		t.Fatal("completed key attempted new network/SSH/effect")
		return nil
	}
	again, err := service.LookupStop(review.Key, done.Revision)
	if err != nil || again.Phase != "completed" || again.Revision != done.Revision || compact(again.Result) != initialCompact {
		t.Fatal("original positive completion regressed", again, err)
	}
	comparison, _ := json.Marshal(map[string]string{"raw_initial": string(done.Result), "raw_reread": string(again.Result), "compact_initial": initialCompact, "compact_reread": compact(again.Result)})
	t.Logf("complete JSON result comparison: %s", comparison)
	transient, _ := json.Marshal(StopResult{Operation: "stop-arm-node", Key: review.Key, Status: "unknown", Detail: "later transient disconnection"})
	retained, err := mesh.RecordStopLookup(service.Home, review.Key, done.Revision, transient, errors.New("later unavailable"))
	if err != nil || retained.Phase != "completed" || retained.Revision != done.Revision || compact(retained.Result) != initialCompact {
		t.Fatal("low-level receipt regressed", retained, err)
	}
	receiptAfter, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	infoAfter, err := os.Stat(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(receiptBefore, receiptAfter) || !os.SameFile(infoBefore, infoAfter) || infoBefore.Mode() != infoAfter.Mode() || !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Fatal("passive completed lookups rewrote the saved receipt")
	}
}
func TestUnconfirmedStopLookupRetainsDutyAfterLocalEnrollmentDisconnectOrReplacement(t *testing.T) {
	for _, kind := range []string{"revoked", "unproven", "replaced", "removed"} {
		t.Run(kind, func(t *testing.T) {
			service, review, _, peer, rpc, ssh := lookupFixture(t)
			switch kind {
			case "removed":
				peer.Identity.Host = ""
			case "revoked":
				peer.State = "revoked"
			case "unproven":
				peer.Proven = false
			case "replaced":
				peer.Identity.Fingerprint = strings.Repeat("e", 64)
			}
			before, err := service.Get(review.Key)
			if err != nil {
				t.Fatal(err)
			}
			after, err := service.LookupStop(review.Key, before.Revision)
			if err != nil {
				t.Fatal("lookup should retain a structured unknown result", err)
			}
			if after.Phase != "incomplete" || !after.LookupOnly || after.CanPrepareFresh || *rpc != 0 || *ssh != 0 {
				t.Fatal("changed enrollment gained observer/replay authority", after, *rpc, *ssh)
			}
		})
	}
}
func TestIncompleteRecordPhaseCannotBecomeNativeStoppedResult(t *testing.T) {
	review, obs, _, _ := recoveryFrames(t)
	if !validStopObservation(obs, review.Plan, review.SHA256) {
		t.Fatal("valid frame refused")
	}
	for _, phase := range []string{"accepted", "interrupted"} {
		t.Run(phase, func(t *testing.T) {
			bad := obs
			bad.Record.Phase = phase
			if bad.Record.Validate() != nil {
				t.Fatal("negative is not otherwise valid")
			}
			if validStopObservation(bad, review.Plan, review.SHA256) {
				t.Fatal("incomplete durable phase plus booleans became stopped")
			}
		})
	}
}
