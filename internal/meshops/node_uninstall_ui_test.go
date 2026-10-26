package meshops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/buildinfo"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshssh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
)

// Synthetic contract/source-row controls; these do not assert native Stop or
// ownership. Actual native authority is covered by the backend/CLI fixtures.
func uninstallUIRecord(t *testing.T, change func(*mesh.Coordination)) (string, mesh.Coordination, mesh.UninstallPlan) {
	t.Helper()
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(home, 0700); e != nil {
		t.Fatal(e)
	}
	owner := strconv.Itoa(os.Geteuid())
	id := model.FileIdentity{Device: 1, Inode: 2, Owner: uint32(os.Geteuid()), BornSeconds: 1, BirthKnown: true}
	target := nodestop.Target{Host: strings.Repeat("b", 32), Certificate: strings.Repeat("c", 64), SPKI: strings.Repeat("d", 64), Owner: owner, Home: "/private/fixture-state", Directory: id, Generation: strings.Repeat("e", 32), Startup: nodestop.StartupContract}
	boundary, e := nodestop.NewBoundary(target)
	if e != nil {
		t.Fatal(e)
	}
	key := "ui-uninstall"
	actor := nodestop.Actor{Host: strings.Repeat("a", 32), Certificate: strings.Repeat("f", 64), SPKI: strings.Repeat("1", 64), Pair: strings.Repeat("2", 64), GrantRevision: 1}
	stop, e := boundary.Prepare(mesh.UninstallStopKey(key), actor, 1, model.ShutdownRequest{}, "Retire fixture", nodestop.Impact{Complete: true, SHA256: strings.Repeat("3", 64), Obligations: []nodestop.Obligation{}, Blockers: []nodestop.Blocker{}, Peers: []string{}}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	stopHash, _ := stop.Digest()
	transfer := meshssh.Transfer{Root: "/private/fixture-install", Key: strings.Repeat("4", 64), SHA256: strings.Repeat("5", 64), Size: 1, ExpectedUID: owner, OS: runtime.GOOS, Arch: runtime.GOARCH, Version: buildinfo.Version, Protocol: mesh.LifecycleProtocol, Reason: "original fixture publication"}
	plan := mesh.UninstallPlan{Schema: mesh.UninstallSchema, Key: key, Reason: stop.Reason, Transfer: transfer, Ownership: mesh.InstallOwnership{Schema: 1, Transfer: transfer, Root: id, Artifact: id, Binary: id, Homes: []string{target.Home}}, Stop: nodestop.Prepared{Plan: stop, SHA256: stopHash}, Recovery: mesh.UninstallRecoveryPath(transfer, key), Preserved: []string{"database", "node identity and grants", "configuration and startup policy", "lease and operation history", "backups and retained artifacts", "companion catalog configuration", "recovery helper and uninstall receipt"}}
	hash, e := plan.Digest()
	if e != nil {
		t.Fatal(e)
	}
	request := UninstallDraft{Host: target.Host, ProvisionKey: transfer.Key, Reason: plan.Reason, Policy: stop.Policy}
	digest, _ := mesh.ReviewDigest(request)
	guard, e := mesh.ClaimCoordination(home, key, "uninstall", digest, "host:"+target.Host)
	if e != nil {
		t.Fatal(e)
	}
	rawRequest, _ := json.Marshal(request)
	rawReview, _ := json.Marshal(plan)
	rawResult, _ := json.Marshal(mesh.UninstallResult{Receipt: mesh.UninstallReceipt{Schema: mesh.UninstallSchema, Plan: plan, SHA256: hash, Phase: "stop-admitted", Updated: time.Now(), RecoveryIdentity: id}, Detail: "original admission"})
	if e = guard.Change(func(op *mesh.Coordination) error {
		op.Request = rawRequest
		op.Review = rawReview
		op.ReviewHash = hash
		op.Approved = true
		op.Phase = "incomplete"
		op.Worker = ""
		op.WorkerExpires = time.Time{}
		op.Result = rawResult
		if change != nil {
			change(op)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	op, _, e := mesh.ReadCoordination(home, key)
	if e != nil {
		t.Fatal(e)
	}
	return home, op, plan
}
func TestUninstallServiceRequiresExactApprovedInactiveFinish(t *testing.T) {
	for _, kind := range []string{"approval", "revision", "fingerprint", "source-owner", "service-owner"} {
		t.Run(kind, func(t *testing.T) {
			home, op, _ := uninstallUIRecord(t, func(op *mesh.Coordination) {
				if kind == "approval" {
					op.Approved = false
				}
				if kind == "source-owner" {
					op.Worker = "original-worker"
					op.WorkerExpires = time.Now().Add(time.Minute)
				}
			})
			service := &Service{Home: home, Context: t.Context(), New: func(context.Context) *Coordinator { t.Fatal("rejected request reached native factory"); return nil }}
			revision, hash := op.Revision, op.ReviewHash
			if kind == "revision" {
				revision--
			}
			if kind == "fingerprint" {
				hash = strings.Repeat("f", 64)
			}
			if kind == "service-owner" {
				service.workers = map[string]context.CancelFunc{op.Key: func() {}}
			}
			_, e := service.LookupUninstall(op.Key, revision, true, hash)
			var api *model.Error
			if !errors.As(e, &api) || (api.Code != "review_changed" && api.Code != "worker_active") {
				t.Fatal(kind, e)
			}
		})
	}
}
func TestUninstallServiceCompletedLookupNeverRepeatsOrDowngrades(t *testing.T) {
	home, op, _ := uninstallUIRecord(t, func(op *mesh.Coordination) {
		var r mesh.UninstallResult
		if e := mesh.Decode(op.Result, &r); e != nil {
			t.Fatal(e)
		}
		r.Receipt.Phase = "removed"
		r.StopObserved = true
		r.ArtifactAbsent = true
		op.Result, _ = json.Marshal(r)
		op.Phase = "completed"
	})
	before := append([]byte(nil), op.Result...)
	nativeCalls := 0
	service := &Service{Home: home, Context: t.Context(), New: func(ctx context.Context) *Coordinator {
		return &Coordinator{Home: home, Context: ctx, Inspect: func() (mesh.LifecycleInspection, error) {
			nativeCalls++
			t.Fatal("completed original lookup attempted native work")
			return mesh.LifecycleInspection{}, nil
		}}
	}}
	for _, finish := range []bool{false, true} {
		next, e := service.LookupUninstall(op.Key, op.Revision, finish, op.ReviewHash)
		if e != nil || next.Phase != "completed" || next.Revision != op.Revision {
			t.Fatal(next, e)
		}
	}
	after, _, e := mesh.ReadCoordination(home, op.Key)
	if e != nil || string(after.Result) != string(before) || nativeCalls != 0 {
		t.Fatal("cached completion changed", e)
	}
}
func TestUninstallStartAcceptsOnlyOneExactBrowserDraft(t *testing.T) {
	draft := UninstallDraft{Host: strings.Repeat("b", 32), ProvisionKey: "original-provision", Reason: "Retire node"}
	q := StartRequest{Key: "ui-uninstall", Kind: "uninstall", Uninstall: &draft}
	canonical, target, e := validateStart(q)
	if e != nil || target != "host:"+draft.Host || canonical.(UninstallDraft) != draft {
		t.Fatal(canonical, target, e)
	}
	q.Connect = &ConnectRequest{}
	if _, _, e = validateStart(q); e == nil {
		t.Fatal("mixed uninstall accepted")
	}
	q.Connect = nil
	q.Kind = "connect"
	if _, _, e = validateStart(q); e == nil {
		t.Fatal("uninstall draft lent another kind")
	}
}

func TestUninstallServiceDeclineNeverCopiesStopsOrRemoves(t *testing.T) {
	_, _, plan := uninstallUIRecord(t, nil)
	source, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(source, 0700); e != nil {
		t.Fatal(e)
	}
	connectionKey := "ui-source-connect"
	key := ProvisionKey(connectionKey)
	plan.Transfer.Key = key
	plan.Transfer.Root = filepath.Join(source, "target-install-never-created")
	plan.Ownership.Transfer = plan.Transfer
	provision := ProvisionRequest{ConnectionKey: connectionKey, Destination: "fixture.example", Transfer: plan.Transfer, SSH: meshssh.Identity{Account: "fixture", HostKey: "SHA256:fixture"}, Home: plan.Stop.Plan.Target.Home, Startup: mesh.DefaultStartup()}
	digest, _ := mesh.ReviewDigest(provision)
	writer := &Coordinator{Home: source, Context: t.Context()}
	if e = writer.begin(key, "provision", digest, "ssh:"+provision.Destination); e != nil {
		t.Fatal(e)
	}
	defer writer.heartbeatStop()
	if e = writer.recordProvisionRequest(key, provision); e != nil {
		t.Fatal(e)
	}
	if e = writer.guard.Change(func(op *mesh.Coordination) error {
		op.Approved = true
		op.Phase = "completed"
		op.Worker = ""
		op.WorkerExpires = time.Time{}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	target := mesh.Identity{Host: plan.Stop.Plan.Target.Host, Owner: plan.Stop.Plan.Target.Owner, Fingerprint: plan.Stop.Plan.Target.Certificate, SPKI: plan.Stop.Plan.Target.SPKI}
	own := mesh.Identity{Host: plan.Stop.Plan.Actor.Host, Fingerprint: plan.Stop.Plan.Actor.Certificate, SPKI: plan.Stop.Plan.Actor.SPKI}
	peer := mesh.Peer{Identity: target, Pair: plan.Stop.Plan.Actor.Pair, Revision: plan.Stop.Plan.Actor.GrantRevision, State: "active", Proven: true}
	if e = mesh.SaveLifecycleOperation(source, mesh.LifecycleOperation{Key: connectionKey, Kind: "connect", Reason: "fixture connection", Digest: strings.Repeat("a", 64), At: time.Now(), Phase: "completed", RemoteHost: target.Host, RemoteIdentity: &target, Pair: peer.Pair, SSHLocator: provision.Destination, SSHAccount: provision.SSH.Account, SSHHostKey: provision.SSH.HostKey, RemoteHome: provision.Home, Binary: mesh.UninstallInstalledPath(provision.Transfer)}); e != nil {
		t.Fatal(e)
	}
	var applies atomic.Int32
	service := &Service{Home: source, Context: t.Context()}
	service.New = func(ctx context.Context) *Coordinator {
		return &Coordinator{Home: source, Context: ctx, Inspect: func() (mesh.LifecycleInspection, error) {
			return mesh.LifecycleInspection{Running: true, Identity: &own, Config: &mesh.Config{Identity: own, Peers: []mesh.Peer{peer}}}, nil
		}, Call: func(ctx context.Context, operation string, params, result any) error {
			var request struct {
				Host    string
				Request mesh.Request
			}
			if operation != "mesh_dispatch" {
				return fmt.Errorf("unexpected %s", operation)
			}
			if e := assign(&request, params); e != nil {
				return e
			}
			switch request.Request.Operation {
			case "access_scope":
				return assign(result, model.NodeAccessScope{Source: own.Host, Target: target.Host, Pair: peer.Pair, Revision: peer.Revision})
			case "node_uninstall_plan":
				var q mesh.UninstallPrepare
				if e := mesh.Decode(request.Request.Params, &q); e != nil {
					return e
				}
				p := plan
				p.Key = q.Key
				p.Reason = q.Reason
				p.Recovery = mesh.UninstallRecoveryPath(p.Transfer, p.Key)
				p.Stop.Plan.Key = mesh.UninstallStopKey(p.Key)
				p.Stop.Plan.Reason = p.Reason
				p.Stop.SHA256, _ = p.Stop.Plan.Digest()
				return assign(result, p)
			case "node_uninstall_apply":
				applies.Add(1)
				return fmt.Errorf("decline reached mutation")
			}
			return fmt.Errorf("unexpected %s", request.Request.Operation)
		}, SSH: func(context.Context, string, string, any, meshssh.Identity, any) (meshssh.Identity, error) {
			applies.Add(1)
			return meshssh.Identity{}, fmt.Errorf("decline reached native helper")
		}}
	}
	defer service.Close()
	draft := UninstallDraft{Host: target.Host, ProvisionKey: key, Reason: "Keep original installation"}
	if _, e = service.Start(StartRequest{Key: "ui-decline", Kind: "uninstall", Uninstall: &draft}); e != nil {
		t.Fatal(e)
	}
	review := waitPhase(t, service, "ui-decline", "awaiting_review")
	if review.Approved || applies.Load() != 0 {
		t.Fatal("preparation admitted uninstall")
	}
	if _, e = service.Decide(Decision{Key: review.Key, ReviewRevision: review.ReviewRevision, ReviewHash: review.ReviewHash, Approve: false}); e != nil {
		t.Fatal(e)
	}
	rejected := waitPhase(t, service, "ui-decline", "rejected")
	if rejected.Approved || rejected.Error == nil || rejected.Error.Code != "confirmation_declined" || applies.Load() != 0 {
		t.Fatal("decline was not a confirmed no-op", rejected)
	}
	if _, e = os.Stat(plan.Transfer.Root); !os.IsNotExist(e) {
		t.Fatal("decline created target namespace", e)
	}
}
