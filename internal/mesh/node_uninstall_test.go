package mesh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/buildinfo"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/meshssh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
)

// Filesystem ownership controls deliberately do not claim real Serve closure.
func uninstallFixture(t *testing.T) (meshssh.Transfer, nodestop.Prepared, string) {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	bytes := []byte("private fixture image, never executed")
	sum := sha256.Sum256(bytes)
	q := meshssh.Transfer{Root: root, Key: strings.Repeat("1", 64), SHA256: hex.EncodeToString(sum[:]), Size: int64(len(bytes)), ExpectedUID: strconv.Itoa(os.Geteuid()), OS: runtime.GOOS, Arch: runtime.GOARCH, Version: buildinfo.Version, Protocol: LifecycleProtocol, Reason: "original publication"}
	rootFD, unlock, e := provisionLock(q)
	if e != nil {
		t.Fatal(e)
	}
	artifactPath := filepath.Dir(UninstallInstalledPath(q))
	if e = os.Mkdir(artifactPath, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(artifactPath, "arm"), bytes, 0700); e != nil {
		t.Fatal(e)
	}
	artifact, e := directory(artifactPath)
	if e != nil {
		t.Fatal(e)
	}
	proof, e := captureInstallOwnership(rootFD, artifact, q)
	artifact.Close()
	unlock()
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(root, "state")
	if e = os.Mkdir(home, 0700); e != nil {
		t.Fatal(e)
	}
	if e = registerManagedExecutable(UninstallInstalledPath(q), home); e != nil {
		t.Fatal(e)
	}
	fd, e := os.Open(home)
	if e != nil {
		t.Fatal(e)
	}
	homeID, e := installIdentity(fd)
	fd.Close()
	if e != nil {
		t.Fatal(e)
	}
	target := nodestop.Target{Host: strings.Repeat("2", 32), Owner: q.ExpectedUID, Home: home, Directory: homeID, Generation: strings.Repeat("3", 32), Certificate: strings.Repeat("4", 64), SPKI: strings.Repeat("5", 64), Startup: nodestop.StartupContract}
	boundary, e := nodestop.NewBoundary(target)
	if e != nil {
		t.Fatal(e)
	}
	plan, e := boundary.Prepare(UninstallStopKey("remove-one"), nodestop.Actor{Host: strings.Repeat("6", 32), Certificate: strings.Repeat("7", 64), SPKI: strings.Repeat("8", 64), Pair: strings.Repeat("9", 64), GrantRevision: 1}, 1, model.ShutdownRequest{}, "remove private fixture", nodestop.Impact{Complete: true, SHA256: strings.Repeat("a", 64), Obligations: []nodestop.Obligation{}, Blockers: []nodestop.Blocker{}, Peers: []string{}}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	hash, e := plan.Digest()
	if e != nil {
		t.Fatal(e)
	}
	startup := DefaultStartup()
	runtimeProof := RuntimeProof{Host: target.Host, UID: q.ExpectedUID, SHA256: q.SHA256, Size: q.Size, Version: q.Version, Protocol: q.Protocol, Method: "startup-open-file-snapshot", Captured: time.Now()}
	receipt := ProvisionReceipt{Schema: 1, Request: q, Actor: local.CurrentIdentity(), Phase: "started_verified", Binary: UninstallInstalledPath(q), Version: buildinfo.Version, Protocol: LifecycleProtocol, StartHome: home, StartedHost: target.Host, Ownership: proof, Startup: &startup, Runtime: &runtimeProof}
	if e = saveProvision(q, &receipt); e != nil {
		t.Fatal(e)
	}
	return q, nodestop.Prepared{Plan: plan, SHA256: hash}, home
}
func TestManagedUninstallOriginalOwnershipAndRetainedRecovery(t *testing.T) {
	q, stop, home := uninstallFixture(t)
	plan, e := PrepareUninstall(q, "remove-one", stop.Plan.Reason, stop)
	if e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(UninstallInstalledPath(q))
	if e != nil {
		t.Fatal(e)
	}
	r, e := BeginUninstall(t.Context(), plan)
	if e != nil {
		t.Fatal(e)
	}
	if r.Phase != "accepted" {
		t.Fatal(r)
	}
	recovered, e := os.ReadFile(plan.Recovery)
	if e != nil || string(recovered) != string(before) {
		t.Fatal("retained companion differs", e)
	}
	if e = registerManagedExecutable(UninstallInstalledPath(q), home); e != nil {
		t.Fatal("copy admission blocked ordinary startup before positive Stop", e)
	}
	saved, e := os.ReadFile(uninstallReceiptPath(q, plan.Key))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = BeginUninstall(t.Context(), plan); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(uninstallReceiptPath(q, plan.Key))
	if e != nil || string(saved) != string(after) {
		t.Fatal("known key mutated original receipt", e)
	}
	if _, e = LookupUninstall(q, plan.Key); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(UninstallInstalledPath(q)); e != nil {
		t.Fatal("admission removed live image", e)
	}
	changed := plan
	changed.Reason = "changed"
	if _, e = BeginUninstall(t.Context(), changed); e == nil {
		t.Fatal("changed immutable review accepted")
	}
}
func TestManagedUninstallRefusesReplacedSharedReusedAndUnowned(t *testing.T) {
	for _, kind := range []string{"same-bytes-replaced", "hardlink", "other-home", "extra-member", "reused", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			q, stop, home := uninstallFixture(t)
			path := UninstallInstalledPath(q)
			switch kind {
			case "same-bytes-replaced":
				bytes, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.Rename(path, path+"-retained"); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(path, bytes, 0700); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e := os.Link(path, filepath.Join(q.Root, "another-link")); e != nil {
					t.Fatal(e)
				}
			case "other-home":
				if e := registerManagedExecutable(path, home+"-other"); e != nil {
					t.Fatal(e)
				}
			case "extra-member":
				if e := os.WriteFile(filepath.Join(filepath.Dir(path), "not-owned"), []byte("keep"), 0600); e != nil {
					t.Fatal(e)
				}
			case "reused", "legacy":
				r, e := readProvision(q)
				if e != nil {
					t.Fatal(e)
				}
				if kind == "reused" {
					r.ReusedBinary = true
				} else {
					r.Ownership = nil
				}
				if e = saveProvision(q, &r); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := PrepareUninstall(q, "remove-one", stop.Plan.Reason, stop); e == nil {
				t.Fatal("unproven/shared installation eligible")
			}
			if _, e := os.Stat(path); e != nil {
				t.Fatal("refusal removed image", e)
			}
		})
	}
}
func TestManagedUninstallCancellationAndUnknownRecoveryAreNotAdopted(t *testing.T) {
	q, stop, _ := uninstallFixture(t)
	p, e := PrepareUninstall(q, "remove-one", stop.Plan.Reason, stop)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e = BeginUninstall(ctx, p); e == nil {
		t.Fatal("cancelled copy admitted")
	}
	old, e := LookupUninstall(q, p.Key)
	if e != nil || old.Phase != "copy-unknown" {
		t.Fatal("failed copy lacks durable target duty", old, e)
	}
	if known, e := BeginUninstall(t.Context(), p); e != nil || known.Phase != "copy-unknown" {
		t.Fatal("known partial key was not read-only", known, e)
	}
	next := p
	next.Key = "new-uninstall"
	next.Recovery = UninstallRecoveryPath(q, next.Key)
	next.Stop.Plan.Key = UninstallStopKey(next.Key)
	next.Stop.SHA256, _ = next.Stop.Plan.Digest()
	if e = next.Validate(); e != nil {
		t.Fatal("new-key control is not a valid plan", e)
	}
	if _, e = BeginUninstall(t.Context(), next); e == nil {
		t.Fatal("new key allocated another copy despite original duty")
	}
	if _, e = os.Stat(filepath.Dir(next.Recovery)); !os.IsNotExist(e) {
		t.Fatal("refused new key allocated recovery namespace", e)
	}
	if _, e = os.Stat(UninstallInstalledPath(q)); e != nil {
		t.Fatal(e)
	}
}
func TestNodeUninstallGrantIsSeparateAndDeniedByDefault(t *testing.T) {
	if (Grant{}).Has("node-uninstall") || (Grant{Permissions: []string{"node-stop"}}).Has("node-uninstall") {
		t.Fatal("Stop implied uninstall")
	}
	if e := (Grant{Permissions: []string{"node-uninstall", "node-stop"}}).Validate(); e != nil {
		t.Fatal(e)
	}
	q, stop, _ := uninstallFixture(t)
	p, e := PrepareUninstall(q, "remove-one", stop.Plan.Reason, stop)
	if e != nil {
		t.Fatal(e)
	}
	p.Preserved[0] = "omit database"
	if p.Validate() == nil {
		t.Fatal("preservation disclosure substituted")
	}
	raw, _ := json.Marshal(p)
	if len(raw) > 48<<10 {
		t.Fatal("fixture not bounded")
	}
}

// Real native ownership/grant/namespace controls; the Observation input here is
// deliberately synthetic. Actual Serve/join/lock evidence is tested in server.
func uninstallRemovalFixture(t *testing.T) (UninstallPlan, nodestop.Observation, *Store) {
	t.Helper()
	q, prepared, home := uninstallFixture(t)
	dst, e := Init(home, prepared.Plan.Target.Host, "owned-destination", "127.0.0.1:40001", "127.0.0.1:40001")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { dst.Close() })
	src := testStore(t, "6", "owned-source", "127.0.0.1:40002")
	trust(t, dst, src, Grant{Permissions: []string{"node-stop", "node-uninstall"}})
	identity := dst.Config().Identity
	peer, e := dst.Resolve(src.Config().Identity.Host)
	if e != nil {
		t.Fatal(e)
	}
	target := prepared.Plan.Target
	target.Certificate = identity.Fingerprint
	target.SPKI = identity.SPKI
	boundary, e := nodestop.NewBoundary(target)
	if e != nil {
		t.Fatal(e)
	}
	actor := nodestop.Actor{Host: peer.Identity.Host, Certificate: peer.Identity.Fingerprint, SPKI: peer.Identity.SPKI, Pair: peer.Pair, GrantRevision: peer.Revision}
	stop, e := boundary.Prepare(UninstallStopKey("remove-one"), actor, 1, model.ShutdownRequest{}, prepared.Plan.Reason, prepared.Plan.Impact, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	hash, e := stop.Digest()
	if e != nil {
		t.Fatal(e)
	}
	p, e := PrepareUninstall(q, "remove-one", stop.Reason, nodestop.Prepared{Plan: stop, SHA256: hash})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = BeginUninstall(t.Context(), p); e != nil {
		t.Fatal(e)
	}
	record := nodestop.Record{Schema: nodestop.RecordSchema, Plan: stop, Digest: hash, Shutdown: strings.Repeat("a", 32), DesiredRevision: 2, Phase: "drain-ready", Created: time.Now(), Updated: time.Now()}
	if e = record.Validate(); e != nil {
		t.Fatal(e)
	}
	return p, nodestop.Observation{Record: record, Desired: nodestop.State{Schema: nodestop.StateSchema, Revision: 2, Running: false, Target: target, Key: stop.Key, Digest: hash}, ServerJoined: true, OriginalGenerationAbsent: true}, dst
}
func TestManagedUninstallRemovesOnlyOriginalExecutableAndLookupDoesNotRepeat(t *testing.T) {
	p, observation, _ := uninstallRemovalFixture(t)
	sentinel := filepath.Join(p.Stop.Plan.Target.Home, "database-and-history-kept")
	if e := os.WriteFile(sentinel, []byte("retain"), 0600); e != nil {
		t.Fatal(e)
	}
	result, e := RemoveUninstallArtifact(p, observation)
	if e != nil || result.Receipt.Phase != "removed" || !result.ArtifactAbsent || !result.StopObserved {
		t.Fatal(result, e)
	}
	for _, path := range []string{sentinel, p.Recovery, uninstallReceiptPath(p.Transfer, p.Key), filepath.Join(filepath.Dir(UninstallInstalledPath(p.Transfer)), "ownership.json"), filepath.Join(p.Stop.Plan.Target.Home, "mesh", "config.json")} {
		if _, e = os.Stat(path); e != nil {
			t.Fatal("preserved file missing", path, e)
		}
	}
	before, e := os.ReadFile(uninstallReceiptPath(p.Transfer, p.Key))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = RemoveUninstallArtifact(p, observation); e != nil {
		t.Fatal(e)
	}
	r, e := LookupUninstall(p.Transfer, p.Key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ObserveUninstall(r, observation); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(uninstallReceiptPath(p.Transfer, p.Key))
	if e != nil || string(before) != string(after) {
		t.Fatal("known removal or lookup repeated mutation", e)
	}
}
func TestManagedUninstallRefusesCurrentRestartRevocationAndReplacement(t *testing.T) {
	for _, kind := range []string{"restart", "overtaken", "incomplete", "revoked", "replacement"} {
		t.Run(kind, func(t *testing.T) {
			p, observation, dst := uninstallRemovalFixture(t)
			path := UninstallInstalledPath(p.Transfer)
			switch kind {
			case "restart":
				observation.Desired.Running = true
			case "overtaken":
				observation.Desired.Key = "new-stop"
			case "incomplete":
				observation.ServerJoined = false
			case "revoked":
				if e := dst.Revoke(p.Stop.Plan.Actor.Host, "operator revocation"); e != nil {
					t.Fatal(e)
				}
			case "replacement":
				raw, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.Rename(path, path+"-retained"); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(path, raw, 0700); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := RemoveUninstallArtifact(p, observation); e == nil {
				t.Fatal("unsafe removal accepted", kind)
			}
			if _, e := os.Stat(path); e != nil {
				t.Fatal("refusal removed executable", e)
			}
		})
	}
}

func TestManagedUninstallBoundsRosterAndValidatesReferenceSemantics(t *testing.T) {
	for _, kind := range []string{"schema", "phase", "request-root", "runtime", "filename", "roster-bound"} {
		t.Run(kind, func(t *testing.T) {
			q, stop, _ := uninstallFixture(t)
			if kind == "roster-bound" {
				for i := 0; i < 513; i++ {
					if e := os.WriteFile(filepath.Join(q.Root, "unrelated-"+strconv.Itoa(i)), nil, 0600); e != nil {
						t.Fatal(e)
					}
				}
			} else {
				r, e := readProvision(q)
				if e != nil {
					t.Fatal(e)
				}
				r.Request.Key = strings.Repeat("b", 64)
				r.Binary = UninstallInstalledPath(r.Request)
				r.Ownership = nil
				name := "receipt-" + r.Request.Key + ".json"
				switch kind {
				case "schema":
					r.Schema = 0
				case "phase":
					r.Phase = "unexplained"
				case "request-root":
					r.Request.Root = q.Root + "-elsewhere"
				case "runtime":
					r.Runtime.Size++
				case "filename":
					name = "receipt-" + strings.Repeat("c", 64) + ".json"
				}
				raw, e := json.Marshal(r)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(q.Root, name), raw, 0600); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := PrepareUninstall(q, "remove-one", stop.Plan.Reason, stop); e == nil {
				t.Fatal("invalid/unbounded sharing roster was ignored", kind)
			}
			if _, e := os.Stat(UninstallInstalledPath(q)); e != nil {
				t.Fatal("refusal removed original", e)
			}
		})
	}
}
func TestManagedUninstallAdmissionPrecedesFailedAllocationAndCannotBeRekeyed(t *testing.T) {
	q, stop, _ := uninstallFixture(t)
	p, e := PrepareUninstall(q, "remove-one", stop.Plan.Reason, stop)
	if e != nil {
		t.Fatal(e)
	}
	// An unowned pre-existing recovery namespace must stay intact. The failure
	// occurs only after the target has durably recorded its original copy duty.
	dir := filepath.Dir(p.Recovery)
	if e = os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	sentinel := filepath.Join(dir, "unowned")
	if e = os.WriteFile(sentinel, []byte("preserve"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = BeginUninstall(t.Context(), p); e == nil {
		t.Fatal("adopted existing recovery directory")
	}
	r, e := LookupUninstall(q, p.Key)
	if e != nil || r.Phase != "copy-unknown" {
		t.Fatal("allocation failure had no target admission", r, e)
	}
	raw, e := os.ReadFile(sentinel)
	if e != nil || string(raw) != "preserve" {
		t.Fatal("foreign recovery namespace changed", e)
	}
	before, e := os.ReadFile(uninstallReceiptPath(q, p.Key))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = BeginUninstall(t.Context(), p); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(uninstallReceiptPath(q, p.Key))
	if e != nil || string(before) != string(after) {
		t.Fatal("known copy failure was retried", e)
	}
	root, unlock, e := provisionExistingLock(q)
	if e != nil {
		t.Fatal(e)
	}
	first, e := installRootEntries(root, q.Root)
	if e != nil {
		t.Fatal(e)
	}
	second, e := installRootEntries(root, q.Root)
	unlock()
	if e != nil || len(first) != len(second) {
		t.Fatal("directory enumeration reused an exhausted handle", len(first), len(second), e)
	}
	stop.Plan.Key = UninstallStopKey("remove-other")
	stop.SHA256, _ = stop.Plan.Digest()
	if _, e = PrepareUninstall(q, "remove-other", stop.Plan.Reason, stop); e == nil || !strings.Contains(e.Error(), "pending uninstall") {
		t.Fatal("new valid review ignored pending artifact admission", e)
	}
}

func TestManagedUninstallRebindsNamedArtifactAndRejectsAmbiguousGrants(t *testing.T) {
	t.Run("moved-named-child", func(t *testing.T) {
		q, _, _ := uninstallFixture(t)
		r, e := readProvision(q)
		if e != nil {
			t.Fatal(e)
		}
		root, unlock, e := provisionExistingLock(q)
		if e != nil {
			t.Fatal(e)
		}
		defer unlock()
		held, _, e := exclusiveInstall(root, r, r.StartHome)
		if e != nil {
			t.Fatal(e)
		}
		defer held.Close()
		name := filepath.Dir(r.Binary)
		if e = os.Rename(name, name+"-moved"); e != nil {
			t.Fatal(e)
		}
		if e = os.Mkdir(name, 0700); e != nil {
			t.Fatal(e)
		}
		sentinel := filepath.Join(name, "arm")
		if e = os.WriteFile(sentinel, []byte("replacement is not owned"), 0700); e != nil {
			t.Fatal(e)
		}
		if _, e = verifyInstallOwnership(root, held, q, r.Ownership); e == nil || !strings.Contains(e.Error(), "current name") {
			t.Fatal("detached original directory retained namespace authority", e)
		}
		if _, e = uninstallBinaryAbsent(UninstallPlan{Transfer: q, Ownership: *r.Ownership}); e == nil {
			t.Fatal("replacement directory gave positive original absence")
		}
		raw, e := os.ReadFile(sentinel)
		if e != nil || string(raw) != "replacement is not owned" {
			t.Fatal(e)
		}
	})
	t.Run("duplicate-actor-shadow", func(t *testing.T) {
		p, _, dst := uninstallRemovalFixture(t)
		config := dst.Config()
		shadow := config.Peers[0]
		shadow.State = "revoked"
		shadow.Revision++
		config.Peers = append(config.Peers, shadow)
		if e := save(filepath.Join(p.Stop.Plan.Target.Home, "mesh", "config.json"), config); e != nil {
			t.Fatal(e)
		}
		if e := AuthorizeUninstallNative(p); e == nil {
			t.Fatal("duplicate revoked shadow authorized removal")
		}
		if e := AuthorizeUninstallRead(p); e == nil {
			t.Fatal("ambiguous roster authorized read")
		}
	})
	t.Run("own-read-after-mutation-grant-change", func(t *testing.T) {
		p, _, dst := uninstallRemovalFixture(t)
		dst.mu.Lock()
		e := dst.update(func(c *Config) error {
			c.Peers[0].Revision++
			c.Peers[0].Grant = Grant{Permissions: []string{"observe-own"}}
			return nil
		})
		dst.mu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		if e = AuthorizeUninstallRead(p); e != nil {
			t.Fatal("retained own receipt lost read scope", e)
		}
		if e = AuthorizeUninstallNative(p); e == nil {
			t.Fatal("read scope regained uninstall authority")
		}
	})
}
func TestManagedUninstallSourceSettlementIsExactAndMonotone(t *testing.T) {
	// Result settlement control uses the explicitly synthetic observation fixture;
	// actual native inspector authority is exercised separately in server tests.
	p, observation, _ := uninstallRemovalFixture(t)
	completed, e := RemoveUninstallArtifact(p, observation)
	if e != nil {
		t.Fatal(e)
	}
	source, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(source, 0700); e != nil {
		t.Fatal(e)
	}
	request := struct {
		Host         string                `json:"host"`
		ProvisionKey string                `json:"provision_key"`
		Reason       string                `json:"reason"`
		Policy       model.ShutdownRequest `json:"policy"`
	}{p.Stop.Plan.Target.Host, "original-provision", p.Reason, p.Stop.Plan.Policy}
	digest, _ := ReviewDigest(request)
	guard, e := ClaimCoordination(source, p.Key, "uninstall", digest, "host:"+request.Host)
	if e != nil {
		t.Fatal(e)
	}
	review, _ := json.Marshal(p)
	hash, _ := p.Digest()
	rawRequest, _ := json.Marshal(request)
	pending := UninstallResult{Receipt: UninstallReceipt{Schema: UninstallSchema, Plan: p, SHA256: hash, Phase: "unknown"}, Detail: "original admission unknown"}
	pendingRaw, _ := json.Marshal(pending)
	if e = guard.Change(func(op *Coordination) error {
		op.Request = rawRequest
		op.Review = review
		op.ReviewHash = hash
		op.Approved = true
		op.Phase = "incomplete"
		op.Worker = ""
		op.WorkerExpires = time.Time{}
		op.Result = pendingRaw
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	before, _, e := ReadCoordination(source, p.Key)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(completed)
	if _, e = RecordUninstallLookup(source, p.Key, before.Revision-1, raw, nil); e == nil {
		t.Fatal("stale source CAS settled removal")
	}
	saved, e := RecordUninstallLookup(source, p.Key, before.Revision, raw, nil)
	if e != nil || saved.Phase != "completed" || saved.Error != nil {
		t.Fatal(saved, e)
	}
	actual, _, e := ReadCoordination(source, p.Key)
	if e != nil || actual.Phase != "completed" {
		t.Fatal("successful finish not retained", actual, e)
	}
	if _, e = ClaimCoordination(source, "next-operation", "disconnect", strings.Repeat("a", 64), "host:"+request.Host); e != nil {
		t.Fatal("completed uninstall still blocks target", e)
	}
	stable, e := RecordUninstallLookup(source, p.Key, saved.Revision, pendingRaw, os.ErrDeadlineExceeded)
	if e != nil || stable.Phase != "completed" || stable.Revision != saved.Revision {
		t.Fatal("completed original result downgraded", stable, e)
	}
}
