package meshops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/buildinfo"
	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/meshssh"
)

type reconnectFixture struct {
	home, target                          string
	source, destination                   *mesh.Store
	draft                                 ReconnectDraft
	prior                                 mesh.LifecycleOperation
	ensures, applies, sshCalls, pairCalls int
	inspectRemote                         func(*mesh.LifecycleInspection)
	confirm                               func(any) error
	lastPlan                              any
}

func newReconnectFixture(t *testing.T, initiallyActive ...bool) *reconnectFixture {
	t.Helper()
	f := &reconnectFixture{home: filepath.Join(t.TempDir(), "source"), target: filepath.Join(t.TempDir(), "target")}
	for _, h := range []string{f.home, f.target} {
		if e := local.Prepare(h); e != nil {
			t.Fatal(e)
		}
	}
	var e error
	f.source, e = mesh.Init(f.home, strings.Repeat("a", 32), "source", "127.0.0.1:32101", "127.0.0.1:32101")
	if e != nil {
		t.Fatal(e)
	}
	f.destination, e = mesh.Init(f.target, strings.Repeat("b", 32), "target", "127.0.0.1:32102", "127.0.0.1:32102")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.source.Close(); f.destination.Close() })
	oldGrant := mesh.Grant{Permissions: []string{"observe-host", "message"}, OperatorInbox: true, MaxTTL: time.Hour}
	pair := strings.Repeat("7", 64)
	left, right := f.source.Config().Identity, f.destination.Config().Identity
	for _, item := range []struct {
		s     *mesh.Store
		other mesh.Identity
	}{{f.source, right}, {f.destination, left}} {
		if _, e = item.s.Pair(item.other, oldGrant, pair, "old connection"); e != nil {
			t.Fatal(e)
		}
		if e = item.s.Prove(item.other.Host, pair); e != nil {
			t.Fatal(e)
		}
		if e = item.s.Activate(item.other.Host, pair); e != nil {
			t.Fatal(e)
		}
	}
	lp, _ := f.source.Resolve(right.Host)
	rp, _ := f.destination.Resolve(left.Host)
	f.prior = mesh.LifecycleOperation{Key: "previous-connect", Kind: "connect", Digest: strings.Repeat("d", 64), At: time.Now().UTC(), Phase: "connected", LocalHost: left.Host, RemoteHost: right.Host, LocalIdentity: &left, RemoteIdentity: &right, Pair: pair, LocalRevision: lp.Revision, RemoteRevision: rp.Revision, SSHLocator: "fixture", SSHAccount: "fixture-user", SSHHostKey: "SHA256:fixture", RemoteHome: f.target, Binary: "arm", Reason: "original reviewed connection"}
	if e = mesh.SaveLifecycleOperation(f.home, f.prior); e != nil {
		t.Fatal(e)
	}
	if len(initiallyActive) == 0 || !initiallyActive[0] {
		for _, item := range []struct {
			s *mesh.Store
			p mesh.Peer
		}{{f.source, lp}, {f.destination, rp}} {
			if e = item.s.RevokeExact("old-disconnect", item.p.Identity.Host, item.p.Pair, item.p.Identity.Fingerprint, item.p.Revision, "old disconnect"); e != nil {
				t.Fatal(e)
			}
		}
	}
	f.draft = ReconnectDraft{Host: right.Host, Reason: "new explicitly selected capacity", Forward: mesh.Grant{Permissions: []string{"observe-host"}, MaxTTL: time.Hour}, Reverse: mesh.Grant{Permissions: []string{}, MaxTTL: time.Hour}}
	return f
}
func (f *reconnectFixture) inspection(s *mesh.Store, home string) mesh.LifecycleInspection {
	c := s.Config()
	v := mesh.LifecycleInspection{Protocol: mesh.LifecycleProtocol, Version: buildinfo.Version, Home: home, Owner: local.CurrentIdentity(), Running: true, Host: c.Identity.Host, Identity: &c.Identity, Config: &c, Startup: mesh.DefaultStartup(), Route: &mesh.SSHRoute{ClientIP: "127.0.0.1", ServerIP: "127.0.0.1"}}
	if home == f.target && f.inspectRemote != nil {
		f.inspectRemote(&v)
	}
	return v
}
func (f *reconnectFixture) coordinator() *Coordinator {
	return &Coordinator{Context: context.Background(), Home: f.home, Machine: false, Inspect: func() (mesh.LifecycleInspection, error) { return f.inspection(f.source, f.home), nil }, Ensure: func(mesh.StartupPolicy) (mesh.LifecycleInspection, error) {
		f.ensures++
		return f.inspection(f.source, f.home), nil
	}, Print: func(any) error { return nil }, Confirm: func(v any, _, _ bool) error {
		f.lastPlan = v
		if f.confirm != nil {
			return f.confirm(v)
		}
		return nil
	}, Call: func(_ context.Context, op string, q, out any) error {
		switch op {
		case "mesh_setup_prepare":
			r := q.(mesh.PrepareRequest)
			i := f.source.Config().Identity
			return assign(out, mesh.PreparedSetup{Key: r.Key, Token: "source-token", Identity: i, Listen: i.Address, Existing: true, Expires: time.Now().Add(time.Minute)})
		case "mesh_setup_apply":
			f.applies++
			return assign(out, map[string]bool{"enabled": true})
		case "mesh_pair":
			var b Bootstrap
			if e := assign(&b, q); e != nil {
				return e
			}
			f.pairCalls++
			p, e := f.source.PairReviewed(b.Identity, b.Grant, b.Pair, b.Reason, b.ReplaceRevoked, *b.ExpectedRevision)
			if e != nil {
				return e
			}
			return assign(out, p)
		case "mesh_prove":
			var r map[string]string
			if e := assign(&r, q); e != nil {
				return e
			}
			if e := f.destination.Prove(f.source.Config().Identity.Host, r["Pair"]); e != nil {
				return e
			}
			return f.source.Prove(r["Host"], r["Pair"])
		case "mesh_activate":
			var r map[string]string
			if e := assign(&r, q); e != nil {
				return e
			}
			return f.source.Activate(r["Host"], r["Pair"])
		case "mesh_peers":
			return assign(out, f.source.Config().Peers)
		case "mesh_setup_abort":
			return nil
		default:
			return fmt.Errorf("unexpected local operation %s", op)
		}
	}, SSH: func(_ context.Context, destination, binary string, q any, expected meshssh.Identity, out any) (meshssh.Identity, error) {
		f.sshCalls++
		id := meshssh.Identity{Account: "fixture-user", HostKey: "SHA256:fixture"}
		if expected != (meshssh.Identity{}) && expected != id {
			return id, fmt.Errorf("SSH identity changed")
		}
		if destination != "fixture" && destination != "replacement-locator" {
			return id, fmt.Errorf("unknown fixture locator")
		}
		switch r := q.(type) {
		case Helper:
			switch r.Action {
			case "lifecycle_inspect":
				return id, assign(out, f.inspection(f.destination, f.target))
			case "lifecycle_ensure":
				f.ensures++
				return id, assign(out, f.inspection(f.destination, f.target))
			case "lifecycle_prepare":
				i := f.destination.Config().Identity
				return id, assign(out, mesh.PreparedSetup{Key: r.Prepare.Key, Token: "target-token", Identity: i, Listen: i.Address, Existing: true, Expires: time.Now().Add(time.Minute)})
			case "lifecycle_apply":
				f.applies++
				return id, assign(out, map[string]bool{"enabled": true})
			case "lifecycle_abort":
				return id, nil
			}
		case Bootstrap:
			switch r.Action {
			case "pair":
				f.pairCalls++
				p, e := f.destination.PairReviewed(r.Identity, r.Grant, r.Pair, r.Reason, r.ReplaceRevoked, *r.ExpectedRevision)
				if e != nil {
					return id, e
				}
				return id, assign(out, p)
			case "activate":
				return id, f.destination.Activate(r.Host, r.Pair)
			case "peers":
				return id, assign(out, f.destination.Config().Peers)
			}
		}
		return id, fmt.Errorf("unexpected SSH request %T", q)
	}}
}
func TestReconnectNewEnrollmentFreshGrantsAndDelayedOldCleanup(t *testing.T) {
	f := newReconnectFixture(t)
	before, _ := os.ReadFile(filepath.Join(f.home, "mesh", "config.json"))
	r, e := ResolveReconnect(f.home, "new-reconnect", f.draft)
	if e != nil {
		t.Fatal(e)
	}
	if r.Binding.Selected.Destination != "fixture" || r.Binding.Connection == nil {
		t.Fatal(r)
	}
	a := f.coordinator()
	if code, e := a.Reconnect("new-reconnect", r, true, false); code != 0 || e != nil {
		t.Fatal(code, e)
	}
	p, _ := f.source.Resolve(f.draft.Host)
	if p.State != "active" || p.Pair == f.prior.Pair || !reflect.DeepEqual(p.Grant, f.draft.Reverse) {
		t.Fatal("old grants/enrollment inherited", p)
	}
	q, _ := f.destination.Resolve(f.prior.LocalHost)
	if q.State != "active" || !reflect.DeepEqual(q.Grant, f.draft.Forward) {
		t.Fatal(q)
	}
	if e = f.source.RevokeExact("old-disconnect", p.Identity.Host, f.prior.Pair, p.Identity.Fingerprint, f.prior.LocalRevision, "old disconnect"); e != nil {
		t.Fatal(e)
	}
	actual, _ := f.source.Resolve(p.Identity.Host)
	if actual.Pair != p.Pair || actual.State != "active" {
		t.Fatal("old cleanup revoked new enrollment", actual)
	}
	after, _ := os.ReadFile(filepath.Join(f.home, "mesh", "config.json"))
	if string(before) == string(after) {
		t.Fatal("new enrollment was not persisted")
	}
	op, ok, e := mesh.ReadCoordination(f.home, "new-reconnect")
	if e != nil || !ok || op.Kind != "reconnect" || op.Phase != "completed" {
		t.Fatal(op, e)
	}
	calls := f.sshCalls
	ensures := f.ensures
	// A terminal key is local lookup, even with a changed current registry.
	if e = os.Rename(filepath.Join(f.home, "mesh", "config.json"), filepath.Join(f.home, "mesh", "config.saved")); e != nil {
		t.Fatal(e)
	}
	saved, e := ResolveReconnect(f.home, "new-reconnect", f.draft)
	if e != nil {
		t.Fatal(e)
	}
	if code, e := a.Reconnect("new-reconnect", saved, true, false); code != 0 || e != nil {
		t.Fatal(code, e)
	}
	if f.sshCalls != calls || f.ensures != ensures {
		t.Fatal("known reconnect key redispatched")
	}
	changed := f.draft
	changed.Reason = "different intent"
	if _, e = ResolveReconnect(f.home, "new-reconnect", changed); e == nil {
		t.Fatal("old key accepted another draft")
	}
}
func TestReconnectMismatchBeforeAnyStartup(t *testing.T) {
	for _, kind := range []string{"host", "pin", "owner", "home", "active-destination"} {
		t.Run(kind, func(t *testing.T) {
			f := newReconnectFixture(t)
			r, e := ResolveReconnect(f.home, "mismatch", f.draft)
			if e != nil {
				t.Fatal(e)
			}
			f.inspectRemote = func(i *mesh.LifecycleInspection) {
				switch kind {
				case "host":
					i.Host = strings.Repeat("c", 32)
				case "pin":
					i.Identity.Fingerprint = strings.Repeat("f", 64)
				case "owner":
					i.Owner.UID = "99999"
				case "home":
					i.Home += "-different"
				case "active-destination":
					i.Config.Peers[0].State = "active"
				}
			}
			if code, e := f.coordinator().Reconnect("mismatch", r, true, false); code == 0 || e == nil {
				t.Fatal("changed authority accepted")
			}
			if f.ensures != 0 || f.applies != 0 {
				t.Fatal("mismatch caused startup/mutation", f.ensures, f.applies)
			}
			if _, ok, e := mesh.ReadCoordination(f.home, "mismatch"); e != nil || ok {
				t.Fatal("preflight mismatch created intent", ok, e)
			}
		})
	}
}
func TestReconnectBindingRecheckedAfterReview(t *testing.T) {
	f := newReconnectFixture(t)
	r, e := ResolveReconnect(f.home, "review-race", f.draft)
	if e != nil {
		t.Fatal(e)
	}
	f.confirm = func(any) error {
		_, e := f.source.PairReviewed(f.destination.Config().Identity, f.draft.Forward, strings.Repeat("8", 64), "another reviewed operator", true)
		return e
	}
	if code, e := f.coordinator().Reconnect("review-race", r, true, false); code == 0 || e == nil {
		t.Fatal("changed enrollment applied")
	}
	if f.applies != 0 {
		t.Fatal("mutated listener/trust after stale review")
	}
}
func TestReconnectResolverAmbiguityLocatorAndDryReadOnly(t *testing.T) {
	f := newReconnectFixture(t)
	r, e := ResolveReconnect(f.home, "dry", f.draft)
	if e != nil {
		t.Fatal(e)
	}
	before, _ := json.Marshal(f.source.Config())
	a := f.coordinator()
	if code, e := a.Reconnect("dry", r, false, true); code != 0 || e != nil {
		t.Fatal(code, e)
	}
	after, _ := json.Marshal(f.source.Config())
	if string(before) != string(after) || f.ensures != 0 || f.applies != 0 {
		t.Fatal("dry run mutated")
	}
	if _, ok, _ := mesh.ReadCoordination(f.home, "dry"); ok {
		t.Fatal("dry run recorded intent")
	}
	other := f.prior
	other.Key = "duplicate-connection"
	if e = mesh.SaveLifecycleOperation(f.home, other); e != nil {
		t.Fatal(e)
	}
	if _, e = ResolveReconnect(f.home, "ambiguous", f.draft); e == nil {
		t.Fatal("ambiguous old enrollment guessed")
	}
}

func TestReconnectLostPairAcknowledgementRequiresExplicitRecovery(t *testing.T) {
	f := newReconnectFixture(t)
	r, e := ResolveReconnect(f.home, "lost-pair", f.draft)
	if e != nil {
		t.Fatal(e)
	}
	a := f.coordinator()
	original := a.SSH
	lost := false
	a.SSH = func(ctx context.Context, d, b string, q any, expected meshssh.Identity, out any) (meshssh.Identity, error) {
		id, e := original(ctx, d, b, q, expected, out)
		if p, ok := q.(Bootstrap); ok && p.Action == "pair" && e == nil && !lost {
			lost = true
			return id, fmt.Errorf("actual pair committed but fixture reply lost")
		}
		return id, e
	}
	if code, e := a.Reconnect("lost-pair", r, true, false); code == 0 || e == nil {
		t.Fatal("lost pair incorrectly completed")
	}
	op, ok, e := mesh.ReadCoordination(f.home, "lost-pair")
	if e != nil || !ok || op.Phase != "incomplete" || !op.Approved || op.Worker != "" {
		t.Fatal(op, e)
	}
	remote, _ := f.destination.Resolve(f.prior.LocalHost)
	if remote.Pair == f.prior.Pair || remote.State != "pending" {
		t.Fatal("actual committed destination pair missing", remote)
	}
	calls := f.sshCalls
	if code, e := f.coordinator().Reconnect("lost-pair", r, true, false); code != 0 || e != nil {
		t.Fatal(code, e)
	}
	if f.sshCalls != calls {
		t.Fatal("ordinary retry redispatched")
	}
	if code, e := f.coordinator().ResumeReconnect("lost-pair", r, true, false); code != 0 || e != nil {
		t.Fatal("explicit original recovery failed", code, e)
	}
	final, _, e := mesh.ReadCoordination(f.home, "lost-pair")
	if e != nil || final.ResolvedBy == "" {
		t.Fatal("original unknown not linked to exact child", final, e)
	}
	peer, _ := f.source.Resolve(f.draft.Host)
	if peer.Pair != remote.Pair || peer.State != "active" {
		t.Fatal("recovery changed enrollment instead of joining original", peer)
	}
}

func TestReconnectExplicitLocatorMissingMetadataAndSSHIdentity(t *testing.T) {
	t.Run("replacement locator, same identity", func(t *testing.T) {
		f := newReconnectFixture(t)
		d := f.draft
		d.Destination = "replacement-locator"
		r, e := ResolveReconnect(f.home, "replacement", d)
		if e != nil {
			t.Fatal(e)
		}
		if code, e := f.coordinator().Reconnect("replacement", r, true, false); code != 0 || e != nil {
			t.Fatal(code, e)
		}
	})
	t.Run("missing locator", func(t *testing.T) {
		f := newReconnectFixture(t)
		path, e := mesh.LifecycleOperationPath(f.home, f.prior.Key)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.Remove(path); e != nil {
			t.Fatal(e)
		}
		if _, e = ResolveReconnect(f.home, "missing", f.draft); e == nil || !strings.Contains(e.Error(), "ssh_locator_required") {
			t.Fatal(e)
		}
	})
	t.Run("changed fresh SSH account", func(t *testing.T) {
		f := newReconnectFixture(t)
		r, e := ResolveReconnect(f.home, "ssh-changed", f.draft)
		if e != nil {
			t.Fatal(e)
		}
		a := f.coordinator()
		previous := a.SSH
		a.SSH = func(ctx context.Context, d, b string, q any, expected meshssh.Identity, out any) (meshssh.Identity, error) {
			id, e := previous(ctx, d, b, q, expected, out)
			id.Account = "different-account"
			return id, e
		}
		if code, e := a.Reconnect("ssh-changed", r, true, false); code == 0 || e == nil {
			t.Fatal("changed SSH account inherited old identity")
		}
		if f.ensures != 0 || f.applies != 0 {
			t.Fatal("SSH mismatch caused mutation")
		}
	})
}

func TestReconnectRetainedDisconnectRequiresExplicitReviewedHandoff(t *testing.T) {
	f := newReconnectFixture(t)
	g, e := mesh.ClaimCoordination(f.home, "lost-disconnect", "disconnect", strings.Repeat("d", 64), "host:"+f.draft.Host)
	if e != nil {
		t.Fatal(e)
	}
	// Retain the native destination outcome separately from workflow exclusion.
	prior := mesh.LifecycleOperation{Key: "lost-disconnect", Kind: "disconnect", Digest: strings.Repeat("d", 64), At: time.Now().UTC(), Phase: "remote-pending", LocalHost: f.prior.LocalHost, RemoteHost: f.draft.Host, Pair: f.prior.Pair, Reason: "retain lost destination acknowledgement"}
	if e = g.SaveLifecycle(prior); e != nil {
		t.Fatal(e)
	}
	if e = g.Change(func(op *mesh.Coordination) error {
		op.Phase = "incomplete"
		op.Worker = ""
		op.WorkerExpires = time.Time{}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	before, _, e := mesh.ReadLifecycleOperation(f.home, "lost-disconnect")
	if e != nil {
		t.Fatal(e)
	}
	_, e = ResolveReconnect(f.home, "new-enrollment", f.draft)
	if e == nil || !strings.Contains(e.Error(), "lost-disconnect") || !strings.Contains(e.Error(), "incomplete") || !strings.Contains(e.Error(), "operations KEY") {
		t.Fatal("unactionable unresolved target refusal", e)
	}
	if f.ensures != 0 || f.sshCalls != 0 {
		t.Fatal("blocked reconnect performed native work")
	}
	op, ok, e := mesh.ReadCoordination(f.home, "lost-disconnect")
	if e != nil || !ok {
		t.Fatal(e)
	}
	hash, e := mesh.CoordinationRecoveryHash(op)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = mesh.HandoffCoordination(f.home, op.Key, op.Revision, hash, "retain original obligations while separately reviewing new enrollment"); e != nil {
		t.Fatal(e)
	}
	after, _, e := mesh.ReadLifecycleOperation(f.home, "lost-disconnect")
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("handoff erased native outcome", e)
	}
	r, e := ResolveReconnect(f.home, "new-enrollment", f.draft)
	if e != nil {
		t.Fatal(e)
	}
	if code, e := f.coordinator().Reconnect("new-enrollment", r, true, false); e != nil || code != 0 {
		t.Fatal(code, e)
	}
	current, e := f.source.Resolve(f.draft.Host)
	if e != nil {
		t.Fatal(e)
	}
	if current.Pair == f.prior.Pair || current.State != "active" {
		t.Fatal("fresh enrollment missing")
	}
	if e = f.source.RevokeExact("lost-disconnect", f.draft.Host, f.prior.Pair, f.prior.RemoteIdentity.Fingerprint, f.prior.LocalRevision, "late old cleanup"); e == nil {
		t.Fatal("old generation cleanup unexpectedly matched replacement")
	}
	latest, e := f.source.Resolve(f.draft.Host)
	if e != nil || latest.State != "active" || latest.Pair != current.Pair {
		t.Fatal("old cleanup revoked replacement", e)
	}
}

func TestReconnectExplicitAddressSyntaxBeforeStateOrNativeWork(t *testing.T) {
	f := newReconnectFixture(t)
	for _, address := range []string{"not-an-ip", "127.0.0.1:9000", "[::1]", "fe80::1%en0", strings.Repeat("1", 65), " 127.0.0.1"} {
		for _, side := range []string{"local", "remote"} {
			t.Run(side+"/"+address, func(t *testing.T) {
				d := f.draft
				if side == "local" {
					d.LocalIP = address
				} else {
					d.RemoteIP = address
				}
				if _, e := ResolveReconnect(f.home, "invalid-address", d); e == nil {
					t.Fatal("malformed explicit address accepted")
				}
				if _, exists, e := mesh.ReadCoordination(f.home, "invalid-address"); e != nil || exists {
					t.Fatal("malformed draft created coordinator", e)
				}
			})
		}
	}
	for _, address := range []string{"", "127.0.0.1", "::1", "2001:db8::1234", "::ffff:192.0.2.1"} {
		d := f.draft
		d.LocalIP = address
		d.RemoteIP = address
		if _, e := ResolveReconnect(f.home, "valid-address", d); e != nil {
			t.Fatal("valid address syntax refused", address, e)
		}
	}
	if f.sshCalls != 0 || f.ensures != 0 || f.applies != 0 {
		t.Fatal("pure preflight performed native work")
	}
}
