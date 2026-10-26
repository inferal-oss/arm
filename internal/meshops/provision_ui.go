package meshops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/inferal-oss/arm/internal/buildinfo"
	"github.com/inferal-oss/arm/internal/companions"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshssh"
	"github.com/inferal-oss/arm/internal/model"
)

// ProvisionDraft is the whole dashboard installation input. Paths, startup,
// payload selection and native SSH options are deliberately not configurable.
type ProvisionDraft struct {
	ConnectionKey string `json:"connection_key"`
	Destination   string `json:"ssh_destination,omitempty"`
	Connector     string `json:"connector,omitempty"`
	Reason        string `json:"reason"`
}

func (d ProvisionDraft) Validate(key string) error {
	if !mesh.ValidKey(d.ConnectionKey) || key != ProvisionKey(d.ConnectionKey) || strings.TrimSpace(d.Reason) == "" || len(d.Reason) > 4096 || (d.Destination == "") == (d.Connector == "") {
		return model.Err("invalid_request", "exact linked provisioning key, destination or reviewed connector, and purpose required")
	}
	if d.Connector != "" {
		if len(d.Connector) > 64 || !mesh.ValidKey(d.Connector) {
			return model.Err("invalid_request", "bounded reviewed connector ID required")
		}
		return nil
	}
	return (mesh.Connector{ID: "operator", Destination: d.Destination, Binary: "arm", Reason: d.Reason}).Validate()
}
func resolveProvisionLocation(home string, d ProvisionDraft) (string, string, string, error) {
	if d.Connector == "" {
		return d.Destination, "arm", "", nil
	}
	all, e := mesh.ReadConnectors(home)
	if e != nil {
		return "", "", "", e
	}
	for _, c := range all {
		if c.ID == d.Connector {
			return c.Destination, c.Binary, c.Home, nil
		}
	}
	return "", "", "", model.Err("not_found", "reviewed native connector not found")
}

type provisionMaterial struct {
	artifact  mesh.Artifact
	catalog   *companions.Catalog
	closer    io.Closer
	closeOnce sync.Once
	closeErr  error
}

func (m *provisionMaterial) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		if m.closer != nil {
			m.closeErr = m.closer.Close()
		}
	})
	return m.closeErr
}

// selectProvisionMaterial opens only an embedded/seeded catalog or an already
// imported private artifact. It does not import files or follow sibling binaries.
func selectProvisionMaterial(home string, p meshssh.Probe, reason string) (*provisionMaterial, error) {
	out := &provisionMaterial{}
	source, e := companions.Embedded(buildinfo.Version, mesh.LifecycleProtocol)
	if e != nil {
		return nil, e
	}
	if source == nil {
		stored, err := mesh.OpenCompanionCatalog(home, buildinfo.Version, mesh.LifecycleProtocol)
		if err != nil {
			return nil, err
		}
		if stored != nil {
			source = stored.Catalog
			out.closer = stored
		}
	}
	if source != nil {
		out.catalog = source
		payload, ok := source.Lookup(p.OS, p.Arch)
		if !ok {
			out.Close()
			return nil, model.Err("artifact_unavailable", "trusted companion catalog lacks verified destination platform")
		}
		manifest := source.Manifest()
		out.artifact = mesh.Artifact{Schema: 1, SHA256: payload.SHA256, Size: payload.Size, OS: payload.OS, Arch: payload.Arch, Version: manifest.Version, Protocol: manifest.Protocol, Imported: time.Now().UTC(), Actor: local.CurrentIdentity(), Reason: reason}
		return out, nil
	}
	all, e := mesh.Artifacts(home)
	if e != nil {
		return nil, e
	}
	for _, artifact := range all {
		if artifact.OS == p.OS && artifact.Arch == p.Arch {
			if out.artifact.SHA256 != "" {
				return nil, model.Err("artifact_ambiguous", "multiple trusted artifacts match the target; install a reviewed carrier/catalog for dashboard provisioning")
			}
			out.artifact = artifact
		}
	}
	if out.artifact.SHA256 == "" {
		return nil, model.Err("artifact_unavailable", fmt.Sprintf("no trusted %s/%s payload; use a carrier build or import a trusted payload through the native CLI", p.OS, p.Arch))
	}
	return out, nil
}

func resolveProvisionUI(ctx context.Context, home, key string, d ProvisionDraft, probe func(context.Context, string, string, string, meshssh.Identity) (meshssh.Probe, meshssh.Identity, error)) (ProvisionRequest, *provisionMaterial, error) {
	var q ProvisionRequest
	if e := d.Validate(key); e != nil {
		return q, nil, e
	}
	destination, binary, state, e := resolveProvisionLocation(home, d)
	if e != nil {
		return q, nil, e
	}
	p, identity, e := probe(ctx, destination, binary, state, meshssh.Identity{})
	if e != nil {
		return q, nil, e
	}
	if p.Installed != "" {
		return q, nil, model.Err("already_installed", "ARM is already available on this account; use a separate existing-node connection review")
	}
	material, e := selectProvisionMaterial(home, p, d.Reason)
	if e != nil {
		return q, nil, e
	}
	startup, e := ProvisionStartup(p)
	if e != nil {
		material.Close()
		return q, nil, e
	}
	a := material.artifact
	q = ProvisionRequest{ConnectionKey: d.ConnectionKey, Destination: destination, SSH: identity, Home: p.StateHome, Startup: startup, Transfer: meshssh.Transfer{Root: path.Join(p.Home, ".local", "share", "arm", "provisioned"), Key: key, SHA256: a.SHA256, Size: a.Size, ExpectedUID: p.UID, OS: p.OS, Arch: p.Arch, Version: a.Version, Protocol: a.Protocol, Reason: d.Reason}}
	if material.catalog != nil {
		manifest := material.catalog.Manifest()
		_, digest, err := companions.Canonical(manifest)
		if err != nil {
			material.Close()
			return q, nil, err
		}
		q.Seed = &meshssh.CatalogSeed{Schema: 1, Digest: digest, Manifest: manifest}
	}
	if e = q.Validate(); e != nil {
		material.Close()
		return q, nil, e
	}
	return q, material, nil
}

func (m *provisionMaterial) matches(q ProvisionRequest) error {
	if m == nil || m.artifact.Validate() != nil || m.artifact.SHA256 != q.Transfer.SHA256 || m.artifact.Size != q.Transfer.Size || m.artifact.OS != q.Transfer.OS || m.artifact.Arch != q.Transfer.Arch || m.artifact.Version != q.Transfer.Version || m.artifact.Protocol != q.Transfer.Protocol {
		return model.Err("artifact_changed", "native selected payload differs from exact transfer")
	}
	if (m.catalog == nil) != (q.Seed == nil) {
		return model.Err("artifact_changed", "native catalog selection differs from reviewed seed")
	}
	if m.catalog != nil {
		_, digest, e := companions.Canonical(m.catalog.Manifest())
		if e != nil || digest != q.Seed.Digest {
			return model.Err("artifact_changed", "native catalog manifest differs")
		}
	}
	return nil
}
func (m *provisionMaterial) configure(c *Coordinator) {
	c.provisionCleanup = m.Close
	if m.catalog == nil {
		return
	}
	c.ProvisionCatalog = m.catalog
	var transport ProvisionTransport
	if c.ProvisionTransport != nil {
		transport = *c.ProvisionTransport
	}
	transport.Import = func(expected mesh.Artifact) (mesh.Artifact, error) {
		stream, _, e := m.catalog.Open(expected.OS, expected.Arch)
		if e != nil {
			return expected, e
		}
		defer stream.Close()
		return mesh.ImportCompanion(c.Home, expected, stream)
	}
	c.ProvisionTransport = &transport
}

func storedProvisionRequest(op mesh.Coordination) (ProvisionRequest, error) {
	var q ProvisionRequest
	var saved StartRequest
	if op.Kind != "provision" || op.ParentKey != "" || op.ResolvedBy != "" || op.Handoff != nil || mesh.Decode(op.Request, &saved) != nil || saved.Key != op.Key || saved.Kind != "provision" || saved.Provision == nil || saved.Connect != nil || saved.Reconnect != nil || saved.Disconnect != nil || saved.Stop != nil || saved.Uninstall != nil {
		return q, model.Err("idempotency_conflict", "exact original provisioning writer envelope required")
	}
	q = *saved.Provision
	digest, e := mesh.ReviewDigest(q)
	if e != nil || q.Validate() != nil || q.Transfer.Key != op.Key || digest != op.Digest || len(op.Targets) != 1 || op.Targets[0] != "ssh:"+q.Destination {
		return q, model.Err("idempotency_conflict", "original provisioning request differs from admission")
	}
	return q, nil
}

// PrepareProvision performs bounded read-only native resolution, then transfers
// the selected catalog's lifetime to the existing reviewed worker. No install or
// catalog import occurs before its independent installation decision.
func (s *Service) PrepareProvision(key string, d ProvisionDraft) (Operation, error) {
	if e := d.Validate(key); e != nil {
		return Operation{}, e
	}
	release := func() {}
	if s.Register != nil {
		var e error
		release, e = s.Register()
		if e != nil {
			return Operation{}, e
		}
	}
	defer release()
	s.mu.Lock()
	if s.closing || s.Context.Err() != nil {
		s.mu.Unlock()
		return Operation{}, model.Err("backend_stopping", "operator coordinator is closing")
	}
	destination, _, state, e := resolveProvisionLocation(s.Home, d)
	if e != nil {
		s.mu.Unlock()
		return Operation{}, e
	}
	if prior, exists, e := mesh.ReadCoordination(s.Home, key); e != nil {
		s.mu.Unlock()
		return Operation{}, e
	} else if exists {
		q, e := storedProvisionRequest(prior)
		s.mu.Unlock()
		if e != nil {
			return Operation{}, e
		}
		if q.ConnectionKey != d.ConnectionKey || q.Destination != destination || q.Transfer.Reason != d.Reason || state != "" && q.Home != state {
			return Operation{}, model.Err("idempotency_conflict", "original provisioning key belongs to another destination or purpose")
		}
		return Project(prior), nil
	}
	if s.inspections >= 2 {
		s.mu.Unlock()
		return Operation{}, model.Err("busy", "two bounded native inspections are already finishing")
	}
	ctx, cancel := context.WithTimeout(s.Context, 25*time.Second)
	c := s.New(ctx)
	if c == nil {
		s.mu.Unlock()
		cancel()
		return Operation{}, fmt.Errorf("native provisioning inspector unavailable")
	}
	s.inspections++
	s.wg.Add(1)
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); s.inspections--; s.mu.Unlock(); s.wg.Done() }()
	probe := meshssh.ProbeHost
	if c.ProvisionTransport != nil && c.ProvisionTransport.Probe != nil {
		probe = c.ProvisionTransport.Probe
	}
	q, material, e := resolveProvisionUI(ctx, s.Home, key, d, probe)
	if e != nil {
		return Operation{}, e
	}
	return s.Start(StartRequest{Key: key, Kind: "provision", Provision: &q, provisionMaterial: material})
}

func provisionReviewBound(op mesh.Coordination) (ProvisionRequest, error) {
	q, e := storedProvisionRequest(op)
	if e != nil {
		return q, e
	}
	var p ProvisionPlan
	if e = mesh.Decode(op.Review, &p); e != nil {
		return q, e
	}
	if e = p.Validate(); e != nil {
		return q, e
	}
	digest, e := mesh.ReviewDigest(p)
	requestDigest, re := mesh.ReviewDigest(p.Request)
	if e != nil || re != nil || digest != op.ReviewHash || p.Operation != "install-and-start-arm-node" || p.Key != op.Key || requestDigest != op.Digest || p.Binary != mesh.UninstallInstalledPath(q.Transfer) || p.ArtifactSource != "native trusted local catalog / SHA256 "+q.Transfer.SHA256 || p.Effect == "" || op.ReviewRevision < 1 || op.ReviewRevision > op.Revision {
		return q, model.Err("review_changed", "exact original provisioning review differs")
	}
	return q, nil
}
func validateProvisionResult(q ProvisionRequest, r ProvisionResult) error {
	if r.Key != q.Transfer.Key || r.ConnectionKey != q.ConnectionKey || r.Provisioned != mesh.UninstallInstalledPath(q.Transfer) || r.Receipt.Binary != r.Provisioned || r.VerifiedAt.IsZero() || !provisionSettled(q, r.Receipt) {
		return model.Err("provision_outcome_unknown", "original publication/start/catalog receipt is not completely verified")
	}
	return nil
}
func completedProvision(op mesh.Coordination) (ProvisionRequest, ProvisionResult, error) {
	q, e := provisionReviewBound(op)
	var r ProvisionResult
	if e != nil {
		return q, r, e
	}
	if !op.Approved || op.Phase != "completed" || op.Worker != "" || op.Error != nil {
		return q, r, model.Err("provision_outcome_unknown", "approved completed provisioning with no retained source worker required")
	}
	if e = mesh.Decode(op.Result, &r); e != nil {
		return q, r, e
	}
	return q, r, validateProvisionResult(q, r)
}

func (s *Service) LookupProvision(key string, revision int64) (Operation, error) {
	release := func() {}
	if s.Register != nil {
		var e error
		release, e = s.Register()
		if e != nil {
			return Operation{}, e
		}
	}
	defer release()
	s.mu.Lock()
	if s.closing || s.Context.Err() != nil {
		s.mu.Unlock()
		return Operation{}, model.Err("backend_stopping", "operator coordinator is closing")
	}
	if len(s.workers) >= 2 || s.workers[key] != nil {
		s.mu.Unlock()
		return Operation{}, model.Err("worker_active", "original provisioning worker is still active")
	}
	op, exists, e := mesh.ReadCoordination(s.Home, key)
	if e != nil {
		s.mu.Unlock()
		return Operation{}, e
	}
	if !exists || op.Kind != "provision" || op.Revision != revision || !op.Approved || op.Worker != "" && time.Now().Before(op.WorkerExpires) {
		s.mu.Unlock()
		return Operation{}, model.Err("review_changed", "exact inactive approved original provisioning receipt required")
	}
	if op.Error != nil && op.Error.Code == "provision_source_cleanup_unknown" {
		s.mu.Unlock()
		return Project(op), model.Err("source_owner_unresolved", "target inspection cannot settle the original source companion cleanup duty")
	}
	q, e := provisionReviewBound(op)
	if e != nil {
		s.mu.Unlock()
		return Operation{}, e
	}
	if op.Phase == "completed" {
		if _, _, e = completedProvision(op); e != nil {
			s.mu.Unlock()
			return Project(op), e
		}
	}
	ctx, cancel := context.WithTimeout(s.Context, 30*time.Second)
	c := s.New(ctx)
	if c == nil {
		s.mu.Unlock()
		cancel()
		return Operation{}, fmt.Errorf("native provisioning inspector unavailable")
	}
	if s.workers == nil {
		s.workers = map[string]context.CancelFunc{}
	}
	s.workers[key] = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); delete(s.workers, key); s.mu.Unlock(); s.wg.Done() }()
	var receipt mesh.ProvisionReceipt
	_, e = c.lookupProvision(q, &receipt)
	if e != nil {
		return Project(op), model.Err("provision_outcome_unknown", "original target receipt could not be verified; no upload, start or catalog stream was resent")
	}
	r := ProvisionResult{Provisioned: mesh.UninstallInstalledPath(q.Transfer), Key: key, ConnectionKey: q.ConnectionKey, Receipt: receipt, VerifiedAt: time.Now().UTC(), Reused: true}
	if e = validateProvisionResult(q, r); e != nil {
		return Project(op), e
	}
	raw, e := json.Marshal(r)
	if e != nil {
		return Project(op), e
	}
	updated, e := mesh.RecordProvisionLookup(s.Home, key, revision, raw, func(current mesh.Coordination, value json.RawMessage) error {
		actual, err := provisionReviewBound(current)
		if err != nil {
			return err
		}
		want, err := mesh.ReviewDigest(q)
		got, other := mesh.ReviewDigest(actual)
		if err != nil || other != nil || want != got || current.ReviewHash != op.ReviewHash || current.ReviewRevision != op.ReviewRevision {
			return model.Err("review_changed", "original provisioning scope changed during lookup")
		}
		var result ProvisionResult
		if err = mesh.Decode(value, &result); err != nil {
			return err
		}
		return validateProvisionResult(actual, result)
	})
	return Project(updated), e
}

// ProvisionedConnection returns only completely settled original provenance.
// It grants no trust; callers still execute the existing fresh connection review.
func ProvisionedConnection(home, provisionKey, connectionKey string) (ProvisionRequest, ProvisionResult, error) {
	var q ProvisionRequest
	var r ProvisionResult
	op, exists, e := mesh.ReadCoordination(home, provisionKey)
	if e != nil {
		return q, r, e
	}
	if !exists {
		return q, r, model.Err("not_found", "original provisioning receipt not found")
	}
	q, r, e = completedProvision(op)
	if e != nil {
		return q, r, e
	}
	if q.ConnectionKey != connectionKey {
		return q, r, model.Err("idempotency_conflict", "connection must use its exact original linked key")
	}
	return q, r, nil
}

// Validate permits only the fixed installation review shape produced by ARM.
// It does not grant installation or network authority.
func (p ProvisionPlan) Validate() error {
	if e := p.Request.Validate(); e != nil {
		return e
	}
	if p.Operation != "install-and-start-arm-node" || p.Key != p.Request.Transfer.Key || p.Binary != mesh.UninstallInstalledPath(p.Request.Transfer) || p.ArtifactSource != "native trusted local catalog / SHA256 "+p.Request.Transfer.SHA256 || p.Effect == "" || len(p.Effect) > 16384 {
		return model.Err("review_changed", "unsupported exact installation plan")
	}
	return nil
}

// ProvisionConnectionBinding is produced by the closed dashboard resolver. The
// native CLI's existing ProvisionKey-only recovery path is unchanged.
type ProvisionConnectionBinding struct {
	Key         string `json:"provision_key"`
	RequestHash string `json:"request_sha256"`
	ReviewHash  string `json:"review_sha256"`
	StartedHost string `json:"started_host"`
}

func ProvisionConnectionBindingFor(home, key, connectionKey string) (ProvisionConnectionBinding, error) {
	var binding ProvisionConnectionBinding
	op, exists, e := mesh.ReadCoordination(home, key)
	if e != nil {
		return binding, e
	}
	if !exists {
		return binding, model.Err("not_found", "original provisioning receipt absent")
	}
	q, r, e := completedProvision(op)
	if e != nil {
		return binding, e
	}
	if q.ConnectionKey != connectionKey {
		return binding, model.Err("idempotency_conflict", "original linked connection key differs")
	}
	return ProvisionConnectionBinding{Key: key, RequestHash: op.Digest, ReviewHash: op.ReviewHash, StartedHost: r.Receipt.StartedHost}, nil
}
func (b ProvisionConnectionBinding) matches(q ProvisionRequest, r ProvisionResult) error {
	digest, e := mesh.ReviewDigest(q)
	if e != nil || b.Key != q.Transfer.Key || b.RequestHash != digest || b.StartedHost != r.Receipt.StartedHost {
		return model.Err("setup_changed", "retained dashboard provisioning binding differs")
	}
	return nil
}

func ValidateProvisionResultForPlan(p ProvisionPlan, r ProvisionResult) error {
	if e := p.Validate(); e != nil {
		return e
	}
	return validateProvisionResult(p.Request, r)
}
