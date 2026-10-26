package meshops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/buildinfo"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshssh"
	"github.com/yrashk/arm/internal/model"
)

// Synthetic trusted-catalog/transport controls, not native runtime or SSH proof.
// The real provisioning fixture separately exercises actual executable bytes.
func provisionUIArtifact(t *testing.T, home string) mesh.Artifact {
	t.Helper()
	if e := local.Prepare(home); e != nil {
		t.Fatal(e)
	}
	bytes := []byte("synthetic catalog payload; never executed")
	hash := sha256.Sum256(bytes)
	a := mesh.Artifact{Schema: 1, SHA256: hex.EncodeToString(hash[:]), Size: int64(len(bytes)), OS: "linux", Arch: "arm64", Version: buildinfo.Version, Protocol: mesh.LifecycleProtocol, Imported: time.Now(), Actor: local.CurrentIdentity(), Reason: "native fixture imported trust"}
	dir := filepath.Join(home, "mesh-artifacts")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(a)
	if e := os.WriteFile(filepath.Join(dir, "artifact-"+a.SHA256+".json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "binary-"+a.SHA256), bytes, 0700); e != nil {
		t.Fatal(e)
	}
	return a
}
func provisionUIService(t *testing.T) (*Service, ProvisionDraft, *atomic.Int32, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "source")
	provisionUIArtifact(t, home)
	probes, imports, uploads := &atomic.Int32{}, &atomic.Int32{}, &atomic.Int32{}
	d := ProvisionDraft{ConnectionKey: "ui-connect", Destination: "fixture", Reason: "install fixture for later separate trust review"}
	s := &Service{Home: home, Context: context.Background()}
	s.New = func(ctx context.Context) *Coordinator {
		return &Coordinator{Home: home, Context: ctx, ProvisionTransport: &ProvisionTransport{Probe: func(context.Context, string, string, string, meshssh.Identity) (meshssh.Probe, meshssh.Identity, error) {
			probes.Add(1)
			return meshssh.Probe{OS: "linux", Arch: "arm64", UID: strconv.Itoa(os.Geteuid()), Home: "/fixture/account", StateHome: "/fixture/state", CPUs: 4}, meshssh.Identity{Account: "fixture", HostKey: "SHA256:fixture"}, nil
		}, Import: func(a mesh.Artifact) (mesh.Artifact, error) { imports.Add(1); return a, nil }, Upload: func(context.Context, string, meshssh.Transfer, io.Reader, meshssh.Identity, any) (meshssh.Identity, error) {
			uploads.Add(1)
			t.Error("unapproved or changed target reached upload")
			return meshssh.Identity{}, nil
		}}, SSH: func(context.Context, string, string, any, meshssh.Identity, any) (meshssh.Identity, error) {
			t.Error("test reached remote execution")
			return meshssh.Identity{}, nil
		}}
	}
	t.Cleanup(s.Close)
	return s, d, probes, imports, uploads
}
func TestProvisionUIClosedDraftAndMissingPayloadStayReadOnly(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	d := ProvisionDraft{ConnectionKey: "install-only", Destination: "fixture", Reason: "inspect payload"}
	key := ProvisionKey(d.ConnectionKey)
	for _, bad := range []ProvisionDraft{{ConnectionKey: d.ConnectionKey, Reason: d.Reason}, {ConnectionKey: d.ConnectionKey, Destination: "-F", Reason: d.Reason}, {ConnectionKey: d.ConnectionKey, Destination: "fixture", Connector: "saved", Reason: d.Reason}, {ConnectionKey: d.ConnectionKey, Destination: "fixture", Reason: " "}} {
		if bad.Validate(key) == nil {
			t.Fatal("invalid closed draft accepted", bad)
		}
	}
	if d.Validate("another-key") == nil {
		t.Fatal("unlinked operation key accepted")
	}
	probe := func(context.Context, string, string, string, meshssh.Identity) (meshssh.Probe, meshssh.Identity, error) {
		return meshssh.Probe{OS: "linux", Arch: "arm64", UID: "501", Home: "/fixture/account", StateHome: "/fixture/state", CPUs: 4}, meshssh.Identity{Account: "fixture", HostKey: "SHA256:fixture"}, nil
	}
	if _, _, e := resolveProvisionUI(context.Background(), home, key, d, probe); e == nil || !strings.Contains(e.Error(), "artifact_unavailable") {
		t.Fatal(e)
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("read-only preflight created state", e)
	}
}
func TestProvisionUIReviewDeclineAndKnownKeyNeverReprobeOrUpload(t *testing.T) {
	s, d, probes, imports, uploads := provisionUIService(t)
	key := ProvisionKey(d.ConnectionKey)
	if _, e := s.PrepareProvision(key, d); e != nil {
		t.Fatal(e)
	}
	op := waitPhase(t, s, key, "awaiting_review")
	var plan ProvisionPlan
	if e := mesh.Decode(op.Review, &plan); e != nil || plan.Validate() != nil {
		t.Fatal(plan, e)
	}
	if plan.Request.Transfer.Root != "/fixture/account/.local/share/arm/provisioned" || plan.Request.Home != "/fixture/state" || plan.Request.SSH.Account != "fixture" || probes.Load() != 1 || imports.Load() != 0 || uploads.Load() != 0 {
		t.Fatal("preparation changed authority or performed unapproved work", plan, probes.Load(), imports.Load(), uploads.Load())
	}
	if _, e := s.Decide(Decision{Key: key, ReviewRevision: op.ReviewRevision, ReviewHash: op.ReviewHash, Approve: false}); e != nil {
		t.Fatal(e)
	}
	rejected := waitPhase(t, s, key, "rejected")
	if rejected.Approved || rejected.Error == nil || rejected.Error.Code != "confirmation_declined" {
		t.Fatal(rejected)
	}
	before := probes.Load()
	if known, e := s.PrepareProvision(key, d); e != nil || known.Key != key {
		t.Fatal(known, e)
	}
	if probes.Load() != before || imports.Load() != 0 || uploads.Load() != 0 {
		t.Fatal("known original key reprobed or caused installation")
	}
	d.Reason = "another purpose"
	if _, e := s.PrepareProvision(key, d); e == nil {
		t.Fatal("key reused with another reason")
	}
}
func TestProvisionUIPreclaimedOwnerMustMatchTargetAndCanonicalRequest(t *testing.T) {
	s, d, _, _, _ := provisionUIService(t)
	key := ProvisionKey(d.ConnectionKey)
	c := s.New(context.Background())
	q, material, e := resolveProvisionUI(context.Background(), s.Home, key, d, c.ProvisionTransport.Probe)
	if e != nil {
		t.Fatal(e)
	}
	defer material.Close()
	digest, _ := mesh.ReviewDigest(q)
	g, e := mesh.ClaimCoordination(s.Home, key, "provision", digest, "ssh:another-target")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(StartRequest{Key: key, Kind: "provision", Provision: &q})
	if e = g.Change(func(op *mesh.Coordination) error { op.Request = raw; return nil }); e != nil {
		t.Fatal(e)
	}
	c.guard = g
	c.Print = func(any) error { return nil }
	c.Confirm = func(any, bool, bool) error { t.Error("foreign target reached review"); return nil }
	if _, e = c.Provision(q, material.artifact, true, false); e == nil || !strings.Contains(e.Error(), "idempotency_conflict") {
		t.Fatal(e)
	}
}

func TestProvisionUIApprovalRechecksAccountBeforeRemoteMutation(t *testing.T) {
	s, d, probes, imports, uploads := provisionUIService(t)
	factory := s.New
	s.New = func(ctx context.Context) *Coordinator {
		c := factory(ctx)
		probe := c.ProvisionTransport.Probe
		c.ProvisionTransport.Probe = func(ctx context.Context, a, b, h string, id meshssh.Identity) (meshssh.Probe, meshssh.Identity, error) {
			p, identity, e := probe(ctx, a, b, h, id)
			if probes.Load() > 1 {
				p.UID = "another-account"
			}
			return p, identity, e
		}
		return c
	}
	key := ProvisionKey(d.ConnectionKey)
	if _, e := s.PrepareProvision(key, d); e != nil {
		t.Fatal(e)
	}
	review := waitPhase(t, s, key, "awaiting_review")
	if _, e := s.Decide(Decision{Key: key, ReviewRevision: review.ReviewRevision, ReviewHash: review.ReviewHash, Approve: true}); e != nil {
		t.Fatal(e)
	}
	op := waitPhase(t, s, key, "incomplete")
	if !op.Approved || op.ProvisionConnectionReady || op.Error == nil || op.Error.Code != "setup_changed" || imports.Load() != 1 || uploads.Load() != 0 {
		t.Fatal(op, imports.Load(), uploads.Load())
	}
}
func provisionUICompleted(t *testing.T, changes ...func(*mesh.Coordination)) (string, mesh.Coordination, ProvisionRequest, ProvisionResult) {
	t.Helper()
	s, d, _, _, _ := provisionUIService(t)
	key := ProvisionKey(d.ConnectionKey)
	c := s.New(context.Background())
	q, material, e := resolveProvisionUI(context.Background(), s.Home, key, d, c.ProvisionTransport.Probe)
	if e != nil {
		t.Fatal(e)
	}
	defer material.Close()
	p := ProvisionPlan{Operation: "install-and-start-arm-node", Key: key, Request: q, ArtifactSource: "native trusted local catalog / SHA256 " + q.Transfer.SHA256, Binary: mesh.UninstallInstalledPath(q.Transfer), Effect: "synthetic exact review; not native authority"}
	digest, _ := mesh.ReviewDigest(q)
	g, e := mesh.ClaimCoordination(s.Home, key, "provision", digest, "ssh:"+q.Destination)
	if e != nil {
		t.Fatal(e)
	}
	result := ProvisionResult{Provisioned: p.Binary, Key: key, ConnectionKey: q.ConnectionKey, VerifiedAt: time.Now(), Receipt: mesh.ProvisionReceipt{Schema: 1, Request: q.Transfer, Phase: "started_verified", Binary: p.Binary, Version: q.Transfer.Version, Protocol: q.Transfer.Protocol, StartedHost: strings.Repeat("b", 32), StartHome: q.Home, Startup: &q.Startup, Runtime: &mesh.RuntimeProof{Host: strings.Repeat("b", 32), UID: q.Transfer.ExpectedUID, SHA256: q.Transfer.SHA256, Size: q.Transfer.Size, Version: q.Transfer.Version, Protocol: q.Transfer.Protocol, Method: "synthetic test only", Captured: time.Now()}}}
	request, _ := json.Marshal(StartRequest{Key: key, Kind: "provision", Provision: &q})
	review, _ := json.Marshal(p)
	hash, _ := mesh.ReviewDigest(p)
	rawResult, _ := json.Marshal(result)
	if e = g.Change(func(op *mesh.Coordination) error {
		op.Request = request
		op.Review = review
		op.ReviewHash = hash
		op.ReviewRevision = op.Revision + 1
		op.Result = rawResult
		op.Approved = true
		op.Phase = "completed"
		op.Worker = ""
		op.WorkerExpires = time.Time{}
		for _, change := range changes {
			change(op)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	op, _, e := mesh.ReadCoordination(s.Home, key)
	if e != nil {
		t.Fatal(e)
	}
	return s.Home, op, q, result
}
func TestProvisionConnectionReadyRequiresCompleteBoundSourceEvidence(t *testing.T) {
	home, original, q, result := provisionUICompleted(t)
	if !Project(original).ProvisionConnectionReady {
		t.Fatal("fully bound fixture not ready")
	}
	if _, _, e := ProvisionedConnection(home, original.Key, q.ConnectionKey); e != nil {
		t.Fatal(e)
	}
	if _, _, e := ProvisionedConnection(home, original.Key, "another-key"); e == nil {
		t.Fatal("foreign linked connection accepted")
	}
	for name, change := range map[string]func(*mesh.Coordination){
		"unapproved": func(op *mesh.Coordination) { op.Approved = false }, "incomplete": func(op *mesh.Coordination) { op.Phase = "incomplete" }, "retained-owner": func(op *mesh.Coordination) { op.Worker = "unresolved"; op.WorkerExpires = time.Time{} }, "error": func(op *mesh.Coordination) { op.Error = &model.Error{Code: "unknown", Message: "unresolved"} }, "wrong-target": func(op *mesh.Coordination) { op.Targets = []string{"ssh:elsewhere"} }, "wrong-hash": func(op *mesh.Coordination) { op.ReviewHash = strings.Repeat("0", 64) }, "bad-writer": func(op *mesh.Coordination) { op.Request = []byte(`{"key":"foreign"}`) }, "no-review": func(op *mesh.Coordination) { op.Review = nil }, "wrong-runtime": func(op *mesh.Coordination) {
			r := result
			runtime := *r.Receipt.Runtime
			runtime.SHA256 = strings.Repeat("0", 64)
			r.Receipt.Runtime = &runtime
			op.Result, _ = json.Marshal(r)
		}, "unexpected-seed": func(op *mesh.Coordination) {
			r := result
			r.Receipt.CatalogSeed = &mesh.CatalogSeedReceipt{}
			op.Result, _ = json.Marshal(r)
		}, "wrong-result-key": func(op *mesh.Coordination) { r := result; r.Key = "foreign"; op.Result, _ = json.Marshal(r) },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(original)
			var op mesh.Coordination
			if e := json.Unmarshal(raw, &op); e != nil {
				t.Fatal(e)
			}
			change(&op)
			if Project(op).ProvisionConnectionReady {
				t.Fatal("incomplete evidence projected permission to connect")
			}
		})
	}
}

type provisionUICloser struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
	err     error
}

func (c *provisionUICloser) Close() error {
	c.calls.Add(1)
	close(c.entered)
	<-c.release
	return c.err
}
func TestProvisionSourceReaderClosesBeforeSourceSettlementAndServiceJoin(t *testing.T) {
	s, d, _, _, _ := provisionUIService(t)
	key := ProvisionKey(d.ConnectionKey)
	c := s.New(context.Background())
	q, material, e := resolveProvisionUI(context.Background(), s.Home, key, d, c.ProvisionTransport.Probe)
	if e != nil {
		t.Fatal(e)
	}
	closer := &provisionUICloser{entered: make(chan struct{}), release: make(chan struct{})}
	material.closer = closer
	var active atomic.Int32
	s.Register = func() (func(), error) { active.Add(1); return func() { active.Add(-1) }, nil }
	if _, e = s.Start(StartRequest{Key: key, Kind: "provision", Provision: &q, provisionMaterial: material}); e != nil {
		t.Fatal(e)
	}
	review := waitPhase(t, s, key, "awaiting_review")
	if _, e = s.Decide(Decision{Key: key, ReviewRevision: review.ReviewRevision, ReviewHash: review.ReviewHash, Approve: false}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-closer.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("reader close not attempted")
	}
	current, _, e := mesh.ReadCoordination(s.Home, key)
	if e != nil || current.Worker == "" || active.Load() != 1 {
		t.Fatal("source duty retired before reader joined", current, active.Load(), e)
	}
	joined := make(chan struct{})
	go func() { s.Close(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("Service.Close returned before original reader cleanup")
	case <-time.After(20 * time.Millisecond):
	}
	close(closer.release)
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("service did not join original close")
	}
	if active.Load() != 0 || closer.calls.Load() != 1 {
		t.Fatal("reader not closed exactly once before admission release", active.Load(), closer.calls.Load())
	}
}
func TestProvisionLookupSettlesOnlyExactOriginalEvidenceAndKeepsUnknownOwner(t *testing.T) {
	for _, mode := range []string{"settle", "altered-runtime", "source-changed", "retained-owner", "source-cleanup"} {
		t.Run(mode, func(t *testing.T) {
			home, op, q, result := provisionUICompleted(t, func(op *mesh.Coordination) {
				op.Phase = "incomplete"
				op.Result = nil
				op.Error = &model.Error{Code: "provision_outcome_unknown", Message: "lost target acknowledgement"}
				if mode == "retained-owner" {
					op.Worker = "original-unresolved"
					op.WorkerExpires = time.Time{}
				}
				if mode == "source-cleanup" {
					op.Error.Code = "provision_source_cleanup_unknown"
				}
			})
			calls := 0
			s := &Service{Home: home, Context: context.Background()}
			s.New = func(ctx context.Context) *Coordinator {
				return &Coordinator{Home: home, Context: ctx, ProvisionTransport: &ProvisionTransport{Lookup: func(_ context.Context, _ string, transfer meshssh.Transfer, id meshssh.Identity, out any) (meshssh.Identity, error) {
					calls++
					if transfer != q.Transfer || id != q.SSH {
						t.Fatal("lookup changed original pinned request")
					}
					receipt := result.Receipt
					if mode == "altered-runtime" {
						runtime := *receipt.Runtime
						runtime.SHA256 = strings.Repeat("0", 64)
						receipt.Runtime = &runtime
					}
					if mode == "source-changed" {
						if _, e := mesh.CancelCoordination(home, op.Key, op.Revision); e != nil {
							t.Fatal(e)
						}
					}
					return id, assign(out, receipt)
				}, Upload: func(context.Context, string, meshssh.Transfer, io.Reader, meshssh.Identity, any) (meshssh.Identity, error) {
					t.Fatal("lookup resent upload")
					return meshssh.Identity{}, nil
				}}, SSH: func(context.Context, string, string, any, meshssh.Identity, any) (meshssh.Identity, error) {
					t.Fatal("lookup executed a helper/start")
					return meshssh.Identity{}, nil
				}}
			}
			defer s.Close()
			updated, e := s.LookupProvision(op.Key, op.Revision)
			actual, _, readErr := mesh.ReadCoordination(home, op.Key)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mode == "settle" {
				if e != nil || !updated.ProvisionConnectionReady || actual.Phase != "completed" || actual.Revision != op.Revision+1 || calls != 1 {
					t.Fatal(updated, e, calls)
				}
				before := string(actual.Result)
				again, e := s.LookupProvision(op.Key, actual.Revision)
				if e != nil || !again.ProvisionConnectionReady || string(again.Result) != before {
					t.Fatal("completed source was downgraded or rewritten", again, e)
				}
			} else {
				if e == nil || actual.Phase == "completed" || len(actual.Result) != 0 || actual.Worker != op.Worker {
					t.Fatal("unknown or altered source was falsely completed", actual, e)
				}
				if mode == "source-cleanup" && calls != 0 {
					t.Fatal("target read tried to settle a retained source cleanup duty")
				}
			}
		})
	}
}

func TestProvisionPreflightUsesExistingBoundedInspectionSlots(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	s := &Service{Home: home, Context: context.Background()}
	var active atomic.Int32
	s.Register = func() (func(), error) { active.Add(1); return func() { active.Add(-1) }, nil }
	s.New = func(ctx context.Context) *Coordinator {
		return &Coordinator{Home: home, Context: ctx, ProvisionTransport: &ProvisionTransport{Probe: func(ctx context.Context, _ string, _ string, _ string, _ meshssh.Identity) (meshssh.Probe, meshssh.Identity, error) {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return meshssh.Probe{}, meshssh.Identity{}, ctx.Err()
			}
			return meshssh.Probe{OS: "linux", Arch: "arm64", UID: "501", Home: "/fixture/account", StateHome: "/fixture/state", CPUs: 4}, meshssh.Identity{Account: "fixture", HostKey: "SHA256:fixture"}, nil
		}}}
	}
	results := make(chan error, 2)
	for _, key := range []string{"one", "two"} {
		go func(key string) {
			_, e := s.PrepareProvision(ProvisionKey(key), ProvisionDraft{ConnectionKey: key, Destination: "fixture", Reason: "read-only inspection"})
			results <- e
		}(key)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("two admitted inspections did not start")
		}
	}
	_, e := s.PrepareProvision(ProvisionKey("third"), ProvisionDraft{ConnectionKey: "third", Destination: "fixture", Reason: "third read"})
	if e == nil || !strings.Contains(e.Error(), "busy") {
		t.Fatal("inspection cap exceeded", e)
	}
	close(release)
	for i := 0; i < 2; i++ {
		if e := <-results; e == nil || !strings.Contains(e.Error(), "artifact_unavailable") {
			t.Fatal(e)
		}
	}
	s.Close()
	if active.Load() != 0 {
		t.Fatal("inspection admission intervals not joined", active.Load())
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("read-only bounded inspections created source state", e)
	}
}
func TestProvisionSourceCleanupErrorRetainsKnownResultWithoutConnectionAuthority(t *testing.T) {
	s, d, _, _, _ := provisionUIService(t)
	key := ProvisionKey(d.ConnectionKey)
	factory := s.New
	q, material, e := resolveProvisionUI(context.Background(), s.Home, key, d, factory(context.Background()).ProvisionTransport.Probe)
	if e != nil {
		t.Fatal(e)
	}
	closer := &provisionUICloser{entered: make(chan struct{}), release: make(chan struct{}), err: fmt.Errorf("synthetic reader-close unknown")}
	close(closer.release)
	material.closer = closer
	calls := 0
	s.New = func(ctx context.Context) *Coordinator {
		c := factory(ctx)
		c.ProvisionTransport.Upload = func(_ context.Context, _ string, _ meshssh.Transfer, _ io.Reader, id meshssh.Identity, out any) (meshssh.Identity, error) {
			calls++
			return id, assign(out, mesh.ProvisionReceipt{Schema: 1, Request: q.Transfer, Phase: "published_verified", Binary: mesh.UninstallInstalledPath(q.Transfer)})
		}
		c.SSH = func(_ context.Context, _ string, _ string, _ any, id meshssh.Identity, out any) (meshssh.Identity, error) {
			calls++
			return id, assign(out, mesh.ProvisionReceipt{Schema: 1, Request: q.Transfer, Phase: "started_verified", Binary: mesh.UninstallInstalledPath(q.Transfer), Version: q.Transfer.Version, Protocol: q.Transfer.Protocol, StartedHost: strings.Repeat("b", 32), StartHome: q.Home, Startup: &q.Startup, Runtime: &mesh.RuntimeProof{Host: strings.Repeat("b", 32), UID: q.Transfer.ExpectedUID, SHA256: q.Transfer.SHA256, Size: q.Transfer.Size, Version: q.Transfer.Version, Protocol: q.Transfer.Protocol, Method: "synthetic test only", Captured: time.Now()}})
		}
		return c
	}
	if _, e = s.Start(StartRequest{Key: key, Kind: "provision", Provision: &q, provisionMaterial: material}); e != nil {
		t.Fatal(e)
	}
	review := waitPhase(t, s, key, "awaiting_review")
	if _, e = s.Decide(Decision{Key: key, ReviewRevision: review.ReviewRevision, ReviewHash: review.ReviewHash, Approve: true}); e != nil {
		t.Fatal(e)
	}
	op := waitPhase(t, s, key, "incomplete")
	s.Close()
	if op.Error == nil || op.Error.Code != "provision_source_cleanup_unknown" || op.ProvisionConnectionReady || len(op.Result) == 0 || closer.calls.Load() != 1 || calls != 2 {
		t.Fatal("cleanup failure erased or conferred connection authority", op, calls, closer.calls.Load())
	}
	// A fresh service must retain this source duty; inspecting the target cannot
	// prove that the original reader's Close succeeded.
	next := &Service{Home: s.Home, Context: context.Background(), New: func(context.Context) *Coordinator {
		t.Fatal("retained source cleanup reached target lookup")
		return nil
	}}
	defer next.Close()
	if _, e = next.LookupProvision(key, op.Revision); e == nil || !strings.Contains(e.Error(), "source_owner_unresolved") {
		t.Fatal(e)
	}
}

func TestDashboardProvisionConnectionPinsSourceAndPreservesNativeCLIPath(t *testing.T) {
	for _, mode := range []string{"wrong-binding", "wrong-path", "changed-ssh", "changed-host", "verified-prefix", "legacy-cli"} {
		t.Run(mode, func(t *testing.T) {
			home, op, q, result := provisionUICompleted(t)
			binding, e := ProvisionConnectionBindingFor(home, op.Key, q.ConnectionKey)
			if e != nil {
				t.Fatal(e)
			}
			request := ConnectRequest{Destination: q.Destination, Binary: result.Provisioned, RemoteHome: q.Home, ProvisionKey: op.Key, ProvisionBinding: &binding, Reason: "separate trust review"}
			if mode == "wrong-binding" {
				request.ProvisionBinding.ReviewHash = strings.Repeat("0", 64)
			}
			if mode == "wrong-path" {
				request.Binary = "/another/arm"
			}
			if mode == "legacy-cli" {
				request.ProvisionKey = "legacy-native-original-key"
				request.ProvisionBinding = nil
			}
			calls := 0
			c := &Coordinator{Home: home, Context: context.Background(), Inspect: func() (mesh.LifecycleInspection, error) {
				return mesh.LifecycleInspection{Protocol: mesh.LifecycleProtocol, Version: buildinfo.Version}, nil
			}, Print: func(any) error { return nil }, SSH: func(_ context.Context, _ string, _ string, _ any, expected meshssh.Identity, out any) (meshssh.Identity, error) {
				calls++
				if mode != "legacy-cli" && expected != q.SSH {
					t.Fatal("original SSH account/pin not supplied")
				}
				actual := q.SSH
				host := result.Receipt.StartedHost
				if mode == "changed-ssh" {
					actual.Account = "another-account"
				}
				if mode == "changed-host" {
					host = strings.Repeat("f", 32)
				}
				return actual, assign(out, mesh.LifecycleInspection{Protocol: mesh.LifecycleProtocol, Version: buildinfo.Version, Host: host, Home: q.Home})
			}}
			_, e = c.Connect(q.ConnectionKey, request, true, false)
			if e == nil {
				t.Fatal("synthetic fixture unexpectedly established trust")
			}
			if mode == "wrong-binding" || mode == "wrong-path" {
				if calls != 0 || !strings.Contains(e.Error(), "setup_changed") {
					t.Fatal("changed provenance reached SSH", calls, e)
				}
			} else if mode == "changed-ssh" || mode == "changed-host" {
				if calls != 1 || !strings.Contains(e.Error(), "setup_changed") {
					t.Fatal(calls, e)
				}
			} else if calls != 1 || !strings.Contains(e.Error(), "unsupported_route") {
				t.Fatal("verified prefix/legacy native path changed", calls, e)
			}
		})
	}
}
