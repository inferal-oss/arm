package meshops

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"getarm.org/internal/buildinfo"
	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/meshssh"
)

func assign(dst, value any) error {
	if dst == nil {
		return nil
	}
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	return json.Unmarshal(raw, dst)
}
func serviceFixture(t *testing.T) (*Service, StartRequest, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "source")
	remoteHome := filepath.Join(t.TempDir(), "target")
	for _, p := range []string{home, remoteHome} {
		if e := local.Prepare(p); e != nil {
			t.Fatal(e)
		}
	}
	ownStore, e := mesh.Init(home, strings.Repeat("a", 32), "source", "127.0.0.1:32101", "127.0.0.1:32101")
	if e != nil {
		t.Fatal(e)
	}
	own := ownStore.Config()
	ownStore.Close()
	remoteStore, e := mesh.Init(remoteHome, strings.Repeat("b", 32), "target", "127.0.0.1:32102", "127.0.0.1:32102")
	if e != nil {
		t.Fatal(e)
	}
	remote := remoteStore.Config()
	remoteStore.Close()
	ownInspect := mesh.LifecycleInspection{Protocol: 1, Version: buildinfo.Version, Home: home, Running: true, Host: own.Identity.Host, Identity: &own.Identity, Config: &own, Startup: mesh.DefaultStartup()}
	targetInspect := mesh.LifecycleInspection{Protocol: 1, Version: buildinfo.Version, Home: remoteHome, Running: true, Host: remote.Identity.Host, Identity: &remote.Identity, Config: &remote, Startup: mesh.DefaultStartup(), Route: &mesh.SSHRoute{ClientIP: "127.0.0.1", ServerIP: "127.0.0.1"}}
	applied, aborted := &atomic.Int32{}, &atomic.Int32{}
	s := &Service{Home: home, Context: context.Background()}
	s.New = func(ctx context.Context) *Coordinator {
		return &Coordinator{Context: ctx, Home: home, Inspect: func() (mesh.LifecycleInspection, error) { return ownInspect, nil }, Ensure: func(mesh.StartupPolicy) (mesh.LifecycleInspection, error) { return ownInspect, nil }, Call: func(ctx context.Context, op string, q, out any) error {
			switch op {
			case "mesh_setup_prepare":
				r := q.(mesh.PrepareRequest)
				return assign(out, mesh.PreparedSetup{Key: r.Key, Token: "private-local-token", Identity: own.Identity, Listen: "127.0.0.1:32101", Expires: time.Now().Add(2 * time.Minute)})
			case "mesh_setup_apply":
				applied.Add(1)
				return assign(out, map[string]bool{"enabled": true})
			case "mesh_setup_abort":
				aborted.Add(1)
				return assign(out, map[string]bool{"aborted": true})
			case "mesh_pair":
				var pair Bootstrap
				if e := assign(&pair, q); e != nil {
					return e
				}
				own.Peers = []mesh.Peer{{Identity: remote.Identity, Pair: pair.Pair, Grant: pair.Grant, State: "pending", Revision: 1}}
				return assign(out, own.Peers[0])
			case "mesh_prove":
				return nil
			case "mesh_activate":
				own.Peers[0].State = "active"
				return nil
			case "mesh_dispatch":
				return assign(out, remote.Identity)
			case "mesh_peers":
				return assign(out, []mesh.Peer{{Identity: remote.Identity, Revision: 3}})
			}
			return fmt.Errorf("unexpected native operation %s", op)
		}, SSH: func(ctx context.Context, destination, binary string, q any, expected meshssh.Identity, out any) (meshssh.Identity, error) {
			identity := meshssh.Identity{Account: "fixture", HostKey: "SHA256:fixture"}
			switch r := q.(type) {
			case Helper:
				switch r.Action {
				case "lifecycle_inspect", "lifecycle_ensure":
					return identity, assign(out, targetInspect)
				case "lifecycle_prepare":
					return identity, assign(out, mesh.PreparedSetup{Key: r.Prepare.Key, Token: "private-remote-token", Identity: remote.Identity, Listen: "127.0.0.1:32102", Expires: time.Now().Add(2 * time.Minute)})
				case "lifecycle_apply":
					applied.Add(1)
					return identity, assign(out, map[string]bool{"enabled": true})
				case "lifecycle_abort":
					aborted.Add(1)
					return identity, assign(out, map[string]bool{"aborted": true})
				}
			case Bootstrap:
				switch r.Action {
				case "pair":
					remote.Peers = []mesh.Peer{{Identity: own.Identity, Pair: r.Pair, Grant: r.Grant, State: "pending", Revision: 1}}
					return identity, assign(out, remote.Peers[0])
				case "activate":
					remote.Peers[0].State = "active"
					return identity, assign(out, map[string]bool{"active": true})
				case "peers":
					return identity, assign(out, []mesh.Peer{{Identity: own.Identity, Revision: 3}})
				}
			}
			return identity, fmt.Errorf("unexpected helper %T", q)
		}}
	}
	q := StartRequest{Key: "workflow", Kind: "connect", Connect: &ConnectRequest{Destination: "fixture", Binary: "arm", Reason: "test reviewed workflow", Forward: mesh.Grant{Permissions: []string{"observe-host"}, MaxTTL: time.Hour}, Reverse: mesh.Grant{Permissions: []string{}, MaxTTL: time.Hour}}}
	t.Cleanup(s.Close)
	return s, q, applied, aborted
}
func waitPhase(t *testing.T, s *Service, key, phase string) Operation {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		op, e := s.Get(key)
		if e == nil && op.Phase == phase {
			return op
		}
		time.Sleep(5 * time.Millisecond)
	}
	op, e := s.Get(key)
	t.Fatalf("wanted %s: %+v %v", phase, op, e)
	return op
}
func TestServicePrepareReviewAndConfirmAreDurableAndDefaultNo(t *testing.T) {
	s, q, applied, _ := serviceFixture(t)
	if _, e := s.Start(q); e != nil {
		t.Fatal(e)
	}
	review := waitPhase(t, s, q.Key, "awaiting_review")
	if applied.Load() != 0 {
		t.Fatal("listener enabled before confirmation")
	}
	raw, _ := json.Marshal(review)
	if strings.Contains(string(raw), "private-local-token") || strings.Contains(string(raw), "private-remote-token") || strings.Contains(string(raw), "\"worker\"") {
		t.Fatal("private reservation/worker leaked")
	}
	until := time.Now().Add(200 * time.Millisecond)
	for {
		unlock, e := mesh.LockLifecycle(s.Home)
		if e == nil {
			unlock()
			break
		}
		if !time.Now().Before(until) {
			t.Fatal("lock held while awaiting human", e)
		}
		time.Sleep(2 * time.Millisecond)
	}
	duplicate, e := s.Start(q)
	if e != nil || duplicate.Key != q.Key {
		t.Fatal("exact start retry failed", e)
	}
	other := q
	other.Key = "other"
	if _, e = s.Start(other); e == nil {
		t.Fatal("concurrent target accepted")
	}
	if _, e = s.Decide(Decision{Key: q.Key, ReviewRevision: review.ReviewRevision, ReviewHash: "different", Approve: true}); e == nil {
		t.Fatal("changed review approved")
	}
	if _, e = s.Decide(Decision{Key: q.Key, ReviewRevision: review.ReviewRevision, ReviewHash: review.ReviewHash, Approve: true}); e != nil {
		t.Fatal(e)
	}
	done := waitPhase(t, s, q.Key, "completed")
	if !done.Approved || applied.Load() != 2 || len(done.Result) == 0 {
		t.Fatal(done, applied.Load())
	}
}
func TestServiceDeclineAndShutdownAbortWithoutTrust(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprint(shutdown), func(t *testing.T) {
			s, q, applied, aborted := serviceFixture(t)
			if _, e := s.Start(q); e != nil {
				t.Fatal(e)
			}
			review := waitPhase(t, s, q.Key, "awaiting_review")
			if shutdown {
				s.Close()
			} else {
				if _, e := s.Decide(Decision{Key: q.Key, ReviewRevision: review.ReviewRevision, ReviewHash: review.ReviewHash, Approve: false}); e != nil {
					t.Fatal(e)
				}
			}
			done := waitPhase(t, s, q.Key, "rejected")
			if applied.Load() != 0 || done.Approved || aborted.Load() != 2 {
				t.Fatal("decline enabled trust or failed exact abort", applied.Load(), aborted.Load(), done)
			}
			if _, e := s.Decide(Decision{Key: q.Key, ReviewRevision: review.ReviewRevision, ReviewHash: review.ReviewHash, Approve: true}); e == nil {
				t.Fatal("old declined review resurrected")
			}
		})
	}
}

func TestCompletedRetryPreservesReceiptAndRestoresOnlyExactEnrollment(t *testing.T) {
	s, q, applied, _ := serviceFixture(t)
	if _, e := s.Start(q); e != nil {
		t.Fatal(e)
	}
	review := waitPhase(t, s, q.Key, "awaiting_review")
	if _, e := s.Decide(Decision{Key: q.Key, ReviewRevision: review.ReviewRevision, ReviewHash: review.ReviewHash, Approve: true}); e != nil {
		t.Fatal(e)
	}
	waitPhase(t, s, q.Key, "completed")
	original, _, _ := mesh.ReadCoordination(s.Home, q.Key)
	before, _ := json.Marshal(original)
	c := s.New(context.Background())
	c.Print = func(any) error { return nil }
	c.Confirm = func(any, bool, bool) error { t.Fatal("healthy retry requested new trust review"); return nil }
	if _, e := c.Connect(q.Key, *q.Connect, true, false); e != nil {
		t.Fatal(e)
	}
	after, _, _ := mesh.ReadCoordination(s.Home, q.Key)
	encoded, _ := json.Marshal(after)
	if string(before) != string(encoded) {
		t.Fatal("healthy retry changed immutable receipt")
	}
	if applied.Load() != 2 {
		t.Fatal("healthy retry applied listener/trust")
	}
	// Explicit restore starts stopped ordinary backends, never prepare/pair/activate.
	c = s.New(context.Background())
	oldInspect, oldEnsure, oldSSH := c.Inspect, c.Ensure, c.SSH
	var localRunning, remoteRunning atomic.Bool
	c.Inspect = func() (mesh.LifecycleInspection, error) {
		v, e := oldInspect()
		v.Running = localRunning.Load()
		return v, e
	}
	c.Ensure = func(p mesh.StartupPolicy) (mesh.LifecycleInspection, error) {
		localRunning.Store(true)
		return oldEnsure(p)
	}
	c.SSH = func(ctx context.Context, destination, binary string, payload any, expected meshssh.Identity, out any) (meshssh.Identity, error) {
		identity, e := oldSSH(ctx, destination, binary, payload, expected, out)
		if e == nil {
			if h, ok := payload.(Helper); ok && (h.Action == "lifecycle_inspect" || h.Action == "lifecycle_ensure") {
				if h.Action == "lifecycle_ensure" {
					remoteRunning.Store(true)
				}
				out.(*mesh.LifecycleInspection).Running = remoteRunning.Load()
			}
		}
		return identity, e
	}
	c.Print = func(any) error { return nil }
	c.Confirm = func(any, bool, bool) error { return nil }
	if _, e := c.Connect(q.Key, *q.Connect, true, false); e != nil {
		t.Fatal(e)
	}
	if !localRunning.Load() || !remoteRunning.Load() || applied.Load() != 2 {
		t.Fatal("restore changed trust or missed ordinary backend start")
	}
	after, _, _ = mesh.ReadCoordination(s.Home, q.Key)
	encoded, _ = json.Marshal(after)
	if string(before) != string(encoded) {
		t.Fatal("restore overwrote original receipt")
	}
	all, e := mesh.ListCoordinations(s.Home)
	if e != nil || len(all) != 2 {
		t.Fatal("bounded separate attempt missing", len(all), e)
	}
	found := false
	for _, op := range all {
		if op.ParentKey == q.Key {
			found = true
			if op.Phase != "completed" || len(op.Review) == 0 {
				t.Fatal("restore attempt evidence missing")
			}
		}
	}
	if !found {
		t.Fatal("restore parent link missing")
	}
	for _, op := range all {
		if op.ParentKey != "" {
			c := s.New(context.Background())
			c.Print = func(any) error { return nil }
			c.Confirm = func(any, bool, bool) error { t.Fatal("child receipt offered new enrollment"); return nil }
			if _, e := c.Connect(op.Key, *q.Connect, true, false); e == nil {
				t.Fatal("CLI child receipt accepted as original intent")
			}
		}
	}
	// A changed enrollment cannot be repaired under this completed intent key.
	c = s.New(context.Background())
	inspect := c.Inspect
	c.Inspect = func() (mesh.LifecycleInspection, error) {
		v, e := inspect()
		var cfg mesh.Config
		_ = assign(&cfg, v.Config)
		cfg.Peers[0].State = "revoked"
		v.Config = &cfg
		return v, e
	}
	c.Print = func(any) error { return nil }
	c.Confirm = func(any, bool, bool) error { t.Fatal("old key offered new trust"); return nil }
	if _, e := c.Connect(q.Key, *q.Connect, true, false); e == nil {
		t.Fatal("completed key re-enrolled revoked trust")
	}
}
func TestDeclinedKeyCannotBecomeApprovedOrLoseProvenance(t *testing.T) {
	s, q, applied, _ := serviceFixture(t)
	if _, e := s.Start(q); e != nil {
		t.Fatal(e)
	}
	review := waitPhase(t, s, q.Key, "awaiting_review")
	if _, e := s.Decide(Decision{Key: q.Key, ReviewRevision: review.ReviewRevision, ReviewHash: review.ReviewHash, Approve: false}); e != nil {
		t.Fatal(e)
	}
	waitPhase(t, s, q.Key, "rejected")
	before, _, _ := mesh.ReadCoordination(s.Home, q.Key)
	raw, _ := json.Marshal(before)
	for i := 0; i < 2; i++ {
		c := s.New(context.Background())
		c.Print = func(any) error { return nil }
		c.Confirm = func(any, bool, bool) error { t.Fatal("terminal declined key reused"); return nil }
		if _, e := c.Connect(q.Key, *q.Connect, true, false); e == nil {
			t.Fatal("declined key became new intent")
		}
		if _, e := s.Start(q); e != nil {
			t.Fatal("read-only terminal service retry", e)
		}
	}
	after, _, _ := mesh.ReadCoordination(s.Home, q.Key)
	encoded, _ := json.Marshal(after)
	if string(raw) != string(encoded) || applied.Load() != 0 {
		t.Fatal("declined receipt/provenance changed")
	}
}

func TestOperatorDraftRejectsWhitespaceReasonsWithoutRewriting(t *testing.T) {
	s, q, _, _ := serviceFixture(t)
	q.Connect.Reason = " \t\n "
	if _, e := s.Start(q); e == nil {
		t.Fatal("blank connect reason accepted")
	}
	q = StartRequest{Key: "blank-disconnect", Kind: "disconnect", Disconnect: &DisconnectDraft{Host: strings.Repeat("a", 32), Pair: strings.Repeat("b", 64), Reason: " \t "}}
	if _, e := s.Start(q); e == nil {
		t.Fatal("blank disconnect reason accepted")
	}
	if all, e := s.List(); e != nil || len(all) != 0 {
		t.Fatal("invalid audit reason wrote intent", all, e)
	}
}
