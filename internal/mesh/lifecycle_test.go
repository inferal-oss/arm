package mesh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLifecycleInspectionIsReadOnlyAndDefaultsAreExplicit(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	t.Setenv("SSH_CONNECTION", "127.0.0.1 43210 127.0.0.1 22")
	inspection, err := InspectLifecycle(home, "test")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Identity != nil || inspection.Running || inspection.Route == nil || inspection.Startup.Resources.Memory != 0 || !strings.Contains(inspection.StartupSource, "no prior") {
		t.Fatal(inspection)
	}
	if _, err = os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("inspection created state", err)
	}
	for _, raw := range []string{"127.0.0.1 ssh 127.0.0.1 22", "name 1234 127.0.0.1 22", "127.0.0.1 1 127.0.0.1 65536"} {
		if _, err := ParseSSHRoute(raw); err == nil {
			t.Fatal("accepted", raw)
		}
	}
}
func TestListenerStateAndExactDisconnectCannotRevokeReenrollment(t *testing.T) {
	a := testStore(t, "a", "alpha", "127.0.0.1:40001")
	b := testStore(t, "b", "beta", "127.0.0.1:40002")
	trust(t, a, b, Grant{Permissions: []string{"observe-host"}})
	peer, _ := a.Resolve("beta")
	if err := a.RevokeExact("disconnect1", peer.Identity.Host, peer.Pair, peer.Identity.Fingerprint, peer.Revision, "operator disconnect"); err != nil {
		t.Fatal(err)
	}
	if err := a.RevokeExact("disconnect1", peer.Identity.Host, peer.Pair, peer.Identity.Fingerprint, peer.Revision, "operator disconnect"); err != nil {
		t.Fatal("exact retry", err)
	}
	if disabled, err := a.DisableUnsharedListener(); err != nil || disabled {
		t.Fatal("manual listener disabled", err)
	}
	fresh, err := a.PairReviewed(peer.Identity, peer.Grant, strings.Repeat("34", 32), "re-pair", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.RevokeExact("old-new-key", peer.Identity.Host, peer.Pair, peer.Identity.Fingerprint, peer.Revision, "old delayed disconnect"); err == nil {
		t.Fatal("old CAS revoked new enrollment")
	}
	if err = a.RevokeExact("disconnect1", peer.Identity.Host, peer.Pair, peer.Identity.Fingerprint, peer.Revision, "operator disconnect"); err != nil {
		t.Fatal(err)
	}
	actual, _ := a.Resolve("beta")
	if actual.Pair != fresh.Pair || actual.State != "pending" {
		t.Fatal("old replay changed current trust", actual)
	}
	if _, err = a.PairReviewed(peer.Identity, peer.Grant, strings.Repeat("56", 32), "stale review", true, peer.Revision); err == nil {
		t.Fatal("stale reviewed revision accepted")
	}
}
func TestPreparedIdentityDoesNotEnableListenerAndStartupPersistsCeiling(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	s, err := InitPrepared(home, strings.Repeat("a", 32), "alpha", "127.0.0.1:41000", "127.0.0.1:41000")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Config().ListenerEnabled() || !s.Config().Managed {
		t.Fatal("prepared identity became desired listener")
	}
	p := DefaultStartup()
	p.Resources.Memory = 123456
	if err = SaveStartupPolicy(home, p); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectLifecycle(home, "test")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Host != s.Config().Identity.Host || inspection.Startup.Resources.Memory != 123456 {
		t.Fatal(inspection)
	}
	unlock, err := LockLifecycle(home)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err = LockLifecycle(home); err == nil {
		t.Fatal("concurrent lifecycle lock accepted")
	}
}
func TestStartupPolicyPreservesAutoAndPositiveDiskCeilings(t *testing.T) {
	for _, disk := range []int64{0, 1 << 30} {
		p := DefaultStartup()
		p.Resources.Disk = disk
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	p := DefaultStartup()
	p.Resources.Disk = -1
	if err := p.Validate(); err == nil {
		t.Fatal("negative disk accepted")
	}
}
func TestLifecycleJournalPreservesIntentActorAndPhaseHistory(t *testing.T) {
	s := testStore(t, "a", "alpha", "127.0.0.1:40001")
	op := LifecycleOperation{Key: "operation", Kind: "connect", Digest: "digest", At: time.Now().UTC(), Phase: "prepared", Reason: "test"}
	if err := SaveLifecycleOperation(s.home, op); err != nil {
		t.Fatal(err)
	}
	op.Phase = "reviewed"
	if err := SaveLifecycleOperation(s.home, op); err != nil {
		t.Fatal(err)
	}
	actual, exists, err := ReadLifecycleOperation(s.home, op.Key)
	if err != nil || !exists || len(actual.Transitions) != 2 || actual.Actor.UID == "" {
		t.Fatal(actual, err)
	}
	op.Digest = "changed"
	if err := SaveLifecycleOperation(s.home, op); err == nil {
		t.Fatal("changed keyed intent overwrote history")
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "other"), filepath.Join(s.home, "daemon.log")); err != nil {
		t.Fatal(err)
	}
	if log, err := OpenLifecycleLog(s.home); err == nil {
		log.Close()
		t.Fatal("symlink log followed")
	}
}
func TestLifecycleResolutionKeepsStableIDsAndLocalCollisionGuards(t *testing.T) {
	config := Config{Identity: Identity{Host: "local-id", Name: "local-name"}, Peers: []Peer{{Identity: Identity{Host: "first-id", Name: "same"}}, {Identity: Identity{Host: "second-id", Name: "first-id"}}, {Identity: Identity{Host: "third-id", Name: "same"}}, {Identity: Identity{Host: "fourth-id", Name: "local-name"}}}}
	if peer, err := ResolveConfig(config, "first-id"); err != nil || peer.Identity.Host != "first-id" {
		t.Fatal("stable ID ambiguous with another name", peer, err)
	}
	for _, name := range []string{"same", "local-name", "local-id"} {
		if _, err := ResolveConfig(config, name); err == nil {
			t.Fatal("ambiguous/local name accepted", name)
		}
	}
}
