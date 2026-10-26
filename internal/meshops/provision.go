package meshops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/inferal-oss/arm/internal/companions"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshssh"
	"github.com/inferal-oss/arm/internal/model"
)

// ProvisionTransport replaces only fixed external I/O for deterministic producer
// tests. It is not serialized or configurable by browser/peer/native RPC input.
type ProvisionTransport struct {
	Seed   func(context.Context, string, string, meshssh.CatalogSeedRequest, io.Reader, meshssh.Identity, any) (meshssh.Identity, error)
	Import func(mesh.Artifact) (mesh.Artifact, error)
	Probe  func(context.Context, string, string, string, meshssh.Identity) (meshssh.Probe, meshssh.Identity, error)
	Upload func(context.Context, string, meshssh.Transfer, io.Reader, meshssh.Identity, any) (meshssh.Identity, error)
	Lookup func(context.Context, string, meshssh.Transfer, meshssh.Identity, any) (meshssh.Identity, error)
}

func (a *Coordinator) probeProvision(q ProvisionRequest) (meshssh.Probe, meshssh.Identity, error) {
	fn := meshssh.ProbeHost
	if a.ProvisionTransport != nil && a.ProvisionTransport.Probe != nil {
		fn = a.ProvisionTransport.Probe
	}
	return fn(a.Context, q.Destination, "arm", q.Home, q.SSH)
}
func (a *Coordinator) uploadProvision(q ProvisionRequest, input io.Reader, result any) (meshssh.Identity, error) {
	fn := meshssh.Upload
	if a.ProvisionTransport != nil && a.ProvisionTransport.Upload != nil {
		fn = a.ProvisionTransport.Upload
	}
	return fn(a.Context, q.Destination, q.Transfer, input, q.SSH, result)
}
func (a *Coordinator) lookupProvision(q ProvisionRequest, result any) (meshssh.Identity, error) {
	fn := meshssh.LookupInstalled
	if a.ProvisionTransport != nil && a.ProvisionTransport.Lookup != nil {
		fn = a.ProvisionTransport.Lookup
	}
	return fn(a.Context, q.Destination, q.Transfer, q.SSH, result)
}

type ProvisionRequest struct {
	Seed          *meshssh.CatalogSeed `json:"seed_catalog,omitempty"`
	ConnectionKey string               `json:"connection_key"`
	Destination   string               `json:"ssh_destination"`
	Transfer      meshssh.Transfer     `json:"transfer"`
	SSH           meshssh.Identity     `json:"ssh_identity"`
	Home          string               `json:"remote_home"`
	Startup       mesh.StartupPolicy   `json:"startup"`
}
type ProvisionPlan struct {
	Operation      string           `json:"operation"`
	Key            string           `json:"key"`
	Request        ProvisionRequest `json:"request"`
	ArtifactSource string           `json:"artifact_source"`
	Binary         string           `json:"final_binary"`
	Effect         string           `json:"effect"`
}
type ProvisionResult struct {
	Provisioned   string                `json:"provisioned"`
	Key           string                `json:"key"`
	ConnectionKey string                `json:"connection_key"`
	Receipt       mesh.ProvisionReceipt `json:"receipt"`
	VerifiedAt    time.Time             `json:"verified_at"`
	Reused        bool                  `json:"reused"`
}

func ProvisionKey(connectionKey string) string {
	sum := sha256.Sum256([]byte("arm-node-provision:" + connectionKey))
	return hex.EncodeToString(sum[:])
}
func (q ProvisionRequest) Validate() error {
	if !mesh.ValidKey(q.ConnectionKey) || q.Transfer.Key != ProvisionKey(q.ConnectionKey) || q.Destination == "" || len(q.Destination) > 512 || q.SSH.Account == "" || q.SSH.HostKey == "" || !meshssh.ValidStateHome(q.Home) {
		return fmt.Errorf("invalid linked provisioning identity")
	}
	if e := q.Transfer.Validate(); e != nil {
		return e
	}
	if q.Seed != nil {
		if e := q.Seed.Validate(q.Transfer); e != nil {
			return e
		}
	}
	return q.Startup.Validate()
}

// Provision is native orchestration only. Operator/browser connect cannot
// silently upload through this path; it needs its own separate installation API.
func (a *Coordinator) Provision(q ProvisionRequest, artifact mesh.Artifact, yes, dry bool) (int, error) {
	code, e := a.run(func() (code int, err error) {
		defer func() {
			if a.provisionCleanup != nil {
				if closeErr := a.provisionCleanup(); closeErr != nil {
					code = 1
					err = model.Err("provision_source_cleanup_unknown", fmt.Sprintf("source companion reader cleanup failed: %v; original installation outcome: %v. Retain the original key; target inspection cannot prove source cleanup", closeErr, err))
				}
			}
		}()
		return a.provision(q, artifact, yes, dry)
	})
	if e != nil {
		kind, message := "provision_failed", e.Error()
		var api *model.Error
		if errors.As(e, &api) {
			kind, message = api.Code, api.Message
		}
		if a.Context.Err() != nil {
			kind = "provision_outcome_unknown"
		}
		return code, model.Err(kind, fmt.Sprintf("%s (provision key %s; original connect key %q). Inspect arm mesh operations %s before any recovery", message, q.Transfer.Key, q.ConnectionKey, q.Transfer.Key))
	}
	return code, nil
}
func (a *Coordinator) provision(q ProvisionRequest, artifact mesh.Artifact, yes, dry bool) (int, error) {
	if e := q.Validate(); e != nil {
		return 2, e
	}
	if e := artifact.Validate(); e != nil {
		return 2, e
	}
	if artifact.SHA256 != q.Transfer.SHA256 || artifact.Size != q.Transfer.Size || artifact.OS != q.Transfer.OS || artifact.Arch != q.Transfer.Arch {
		return 2, fmt.Errorf("selected artifact differs from reviewed transfer")
	}
	if q.Seed != nil {
		if a.ProvisionCatalog == nil {
			return 2, model.Err("seed_source_unavailable", "reviewed four-payload source catalog is required before any installation")
		}
		_, digest, err := companions.Canonical(a.ProvisionCatalog.Manifest())
		if err != nil || digest != q.Seed.Digest {
			return 2, model.Err("seed_source_changed", "source companion manifest differs from reviewed seed; nothing installed")
		}
	}
	key := q.Transfer.Key
	binary := q.Transfer.Root + "/artifact-" + q.Transfer.SHA256 + "/arm"
	plan := ProvisionPlan{Operation: "install-and-start-arm-node", Key: key, Request: q, ArtifactSource: "native trusted local catalog / SHA256 " + artifact.SHA256, Binary: binary, Effect: "Copy these verified local bytes into the private source catalog if needed (the original project binary is unchanged). Install only these reviewed bytes at this private content-addressed user path, then start this exact ARM home once with the shown settings (or preserve an already-running authority). No PATH/global executable/root/service changes. No peer trust is granted: a separate fresh TLS identity/endpoint/grant review follows. Interrupted publication/start requires exact lookup; it is never automatically resent."}
	if q.Seed != nil {
		var compressed, stored int64
		for _, payload := range q.Seed.Manifest.Artifacts {
			compressed += payload.CompressedSize
			stored += payload.CompressedSize
		}
		manifest, _, _ := companions.Canonical(q.Seed.Manifest)
		stored += int64(len(manifest))
		plan.Effect += fmt.Sprintf(" Exact catalog destination: %s/mesh-companions/catalog-%s. Catalog transfer: %d bytes; retained files: %d bytes (filesystem allocation may differ).", q.Home, q.Seed.Digest, compressed, stored)
		plan.Effect += " Also copy the FOUR reviewed compressed platform companions to this started node's private mesh-companions configuration for onward provisioning; their exact per-platform sizes/digests are in seed_catalog. This retained operator configuration grants no trust and is not deleted by ledger reset. No payload write occurs before this approval."
	} else {
		plan.Effect += " No onward companion catalog is included in this manual-artifact request."
	}
	if dry || (a.Machine && !yes) {
		if e := a.Print(plan); e != nil {
			return 1, e
		}
		if dry {
			return 0, nil
		}
		return 1, model.Err("confirmation_required", "review installation/start separately; --yes explicitly authorizes this concrete artifact and the later peer review")
	}
	digest, e := mesh.ReviewDigest(q)
	if e != nil {
		return 1, e
	}
	prior, exists, e := mesh.ReadCoordination(a.Home, key)
	if e != nil {
		return 1, e
	}
	if exists {
		if a.guard != nil {
			owned, err := a.guard.Read()
			if err != nil {
				return 1, err
			}
			if owned.Key != key || owned.Kind != "provision" || owned.Digest != digest || owned.Worker == "" || owned.Worker != prior.Worker || len(owned.Targets) != 1 || owned.Targets[0] != "ssh:"+q.Destination {
				return 1, model.Err("idempotency_conflict", "in-process provisioning owner differs from exact admitted request")
			}
			var saved StartRequest
			if err = mesh.Decode(owned.Request, &saved); err != nil || saved.Key != key || saved.Kind != "provision" || saved.Provision == nil {
				return 1, model.Err("idempotency_conflict", "in-process provisioning request is absent or differs")
			}
			savedDigest, err := mesh.ReviewDigest(*saved.Provision)
			if err != nil || savedDigest != digest {
				return 1, model.Err("idempotency_conflict", "in-process provisioning canonical request differs")
			}
		} else {
			if prior.Kind != "provision" || prior.Digest != digest {
				return 1, model.Err("idempotency_conflict", "provision key belongs to another artifact/account/path/startup request")
			}
			var receipt mesh.ProvisionReceipt
			_, e = a.lookupProvision(q, &receipt)
			if e != nil || !provisionSettled(q, receipt) {
				return 1, model.Err("provision_outcome_unknown", fmt.Sprintf("provision key %s: target publication/start is unknown; inspect this exact receipt; no upload/start was resent", key))
			}
			if prior.Phase == "completed" {
				return 0, a.Print(ProvisionResult{binary, key, q.ConnectionKey, receipt, time.Now().UTC(), true})
			}
			if prior.Phase == "incomplete" && prior.ResolvedBy != "" {
				child, ok, readErr := mesh.ReadCoordination(a.Home, prior.ResolvedBy)
				if readErr != nil || !ok || child.ParentKey != key || child.Kind != "provision" || child.Digest != digest || child.Phase != "completed" || !child.Approved || child.Worker != "" || child.Error != nil {
					return 1, model.Err("provision_outcome_unknown", "original installation has no exact durable settled recovery receipt")
				}
				// Preserve both immutable receipts; a healthy resolved exact retry is
				// a fresh read-only target lookup, never a new review or worker.
				return 0, a.Print(ProvisionResult{binary, key, q.ConnectionKey, receipt, time.Now().UTC(), true})
			}
			if prior.Phase != "incomplete" || prior.Worker != "" || prior.ResolvedBy != "" {
				return 1, model.Err("terminal_or_active_key", "provision key is terminal/active; use a fresh explicit reviewed intent")
			}
			a.attemptParent = key
			if e = a.begin(key, "provision", digest, "ssh:"+q.Destination); e != nil {
				return 1, e
			}
			if e = a.recordProvisionRequest(key, q); e != nil {
				return 1, e
			}
			plan.Operation = "verify-existing-provision"
			plan.Effect = "Read-only recovery: verify the exact already-published artifact and durable started receipt. Do not upload, publish, start a backend or retry execution."
			if e = a.Confirm(plan, yes, false); e != nil {
				return 1, e
			}
			if e = a.markApproved(); e != nil {
				return 1, e
			}
			var checked mesh.ProvisionReceipt
			_, e = a.lookupProvision(q, &checked)
			if e != nil || !provisionSettled(q, checked) {
				return 1, model.Err("provision_outcome_unknown", "target provision changed during recovery; no execution retried")
			}
			return 0, a.Print(ProvisionResult{binary, key, q.ConnectionKey, checked, time.Now().UTC(), true})
		}
	}
	if e = local.Prepare(a.Home); e != nil {
		return 1, e
	}
	if e = a.begin(key, "provision", digest, "ssh:"+q.Destination); e != nil {
		return 1, e
	}
	if e = a.recordProvisionRequest(key, q); e != nil {
		return 1, e
	}
	if e = a.Confirm(plan, yes, false); e != nil {
		return 1, e
	}
	if e = a.markApproved(); e != nil {
		return 1, e
	}
	// Import the local catalog only after reviewed install/start approval. In
	// particular machine invocation without --yes and dry-run create no home.
	if a.ProvisionTransport != nil && a.ProvisionTransport.Import != nil {
		imported, importErr := a.ProvisionTransport.Import(artifact)
		if importErr != nil {
			return 1, importErr
		}
		if imported.Validate() != nil || imported.SHA256 != artifact.SHA256 || imported.Size != artifact.Size || imported.OS != artifact.OS || imported.Arch != artifact.Arch || imported.Version != artifact.Version || imported.Protocol != artifact.Protocol {
			return 1, model.Err("artifact_changed", "local source changed since reviewed artifact selection; no remote install attempted")
		}
		artifact = imported
	}
	// No mutation starts from a stale SSH account/platform/state-policy preview.
	p, identity, e := a.probeProvision(q)
	if e != nil || identity != q.SSH || p.UID != q.Transfer.ExpectedUID || p.OS != q.Transfer.OS || p.Arch != q.Transfer.Arch {
		return 1, model.Err("setup_changed", "target platform/account changed since provision review")
	}
	actualStartup, e := ProvisionStartup(p)
	if e != nil || actualStartup != q.Startup {
		return 1, model.Err("setup_changed", "saved/default startup changed since review")
	}
	f, e := mesh.OpenArtifact(a.Home, artifact)
	if e != nil {
		return 1, e
	}
	defer f.Close()
	var receipt mesh.ProvisionReceipt
	_, e = a.uploadProvision(q, io.Reader(f), &receipt)
	if e != nil {
		return 1, model.Err("provision_outcome_unknown", fmt.Sprintf("upload/publication key %s may be incomplete; preserve its stage and inspect exact receipt; no automatic resend", key))
	}
	if receipt.Request != q.Transfer || receipt.Phase != "published_verified" || receipt.Binary != binary {
		return 1, model.Err("provision_outcome_unknown", "publication receipt differs from reviewed exact artifact")
	}
	start := struct {
		Action   string
		Transfer meshssh.Transfer
		Home     string
		Startup  mesh.StartupPolicy
	}{"provision_start", q.Transfer, q.Home, q.Startup}
	_, e = a.ssh(q.Destination, binary, start, q.SSH, &receipt)
	if e != nil || !provisionStarted(q, receipt) {
		return 1, model.Err("provision_outcome_unknown", fmt.Sprintf("start key %s outcome unknown; query exact provision receipt before any recovery; no automatic start retry", key))
	}
	if q.Seed != nil {
		if a.ProvisionCatalog == nil {
			return 1, model.Err("provision_outcome_unknown", "reviewed seed source unavailable; node may be started, no catalog stream resent")
		}
		frames := a.ProvisionCatalog.Frames()
		defer frames.Close()
		seed := meshssh.CatalogSeedRequest{Transfer: q.Transfer, Home: q.Home, Host: receipt.StartedHost, Catalog: *q.Seed}
		fn := meshssh.SeedCatalog
		if a.ProvisionTransport != nil && a.ProvisionTransport.Seed != nil {
			fn = a.ProvisionTransport.Seed
		}
		_, e = fn(a.Context, q.Destination, binary, seed, frames, q.SSH, &receipt)
		if e != nil || !provisionSettled(q, receipt) {
			return 1, model.Err("provision_outcome_unknown", "catalog seed outcome unknown; inspect exact original provision key, never resend frames automatically")
		}
	}
	return 0, a.Print(ProvisionResult{binary, key, q.ConnectionKey, receipt, time.Now().UTC(), false})
}
func provisionSettled(q ProvisionRequest, r mesh.ProvisionReceipt) bool {
	if !provisionStarted(q, r) {
		return false
	}
	if q.Seed == nil {
		return r.Phase == "started_verified" && r.CatalogSeed == nil
	}
	seed := r.CatalogSeed
	if r.Phase != "seeded_verified" || seed == nil || seed.Identity == nil || seed.Request.Validate() != nil || seed.Request.Transfer != q.Transfer || seed.Request.Home != q.Home || seed.Request.Host != r.StartedHost {
		return false
	}
	expectedRequest := meshssh.CatalogSeedRequest{Transfer: q.Transfer, Home: q.Home, Host: r.StartedHost, Catalog: *q.Seed}
	if mesh.ValidateCatalogSeedEvidence(expectedRequest, seed) != nil {
		return false
	}
	expected, _ := mesh.ReviewDigest(*q.Seed)
	actual, _ := mesh.ReviewDigest(seed.Request.Catalog)
	return expected == actual
}
func provisionStarted(q ProvisionRequest, r mesh.ProvisionReceipt) bool {
	return r.Schema == 1 && r.Request == q.Transfer && (r.Phase == "started_verified" || r.Phase == "seeded_verified") && r.StartHome == q.Home && r.Startup != nil && *r.Startup == q.Startup && len(r.StartedHost) == 32 && r.Version == q.Transfer.Version && r.Protocol == q.Transfer.Protocol && r.Runtime != nil && r.Runtime.Host == r.StartedHost && r.Runtime.UID == q.Transfer.ExpectedUID && r.Runtime.SHA256 == q.Transfer.SHA256 && r.Runtime.Size == q.Transfer.Size && r.Runtime.Version == q.Transfer.Version && r.Runtime.Protocol == q.Transfer.Protocol && r.Runtime.Method != "" && !r.Runtime.Captured.IsZero()
}
func (a *Coordinator) recordProvisionRequest(key string, q ProvisionRequest) error {
	raw, e := json.Marshal(StartRequest{Key: key, Kind: "provision", Provision: &q})
	if e != nil {
		return e
	}
	return a.guard.Change(func(op *mesh.Coordination) error {
		if len(op.Request) == 0 {
			op.Request = raw
		}
		return nil
	})
}
func ProvisionStartup(p meshssh.Probe) (mesh.StartupPolicy, error) {
	policy := mesh.StartupPolicy{Schema: 1, Resources: model.Resources{CPU: float64(max(1, p.CPUs-1))}, Listen: "127.0.0.1:0"}
	if len(p.Startup) > 0 && string(p.Startup) != "null" {
		if e := mesh.Decode(p.Startup, &policy); e != nil {
			return policy, e
		}
	}
	policy.RecordedAt = time.Time{}
	return policy, policy.Validate()
}
