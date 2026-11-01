package residentservice

import (
	"context"
	"encoding/json"
	"path/filepath"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
)

const workerBirthIntentDomain = "arm-resident-worker-intent-v1"

// Locator commitments are DATA. Native package/read admission and current-path
// validation remain required before any package can be opened or activated.
type WorkerPackageSourceData struct {
	Path         string   `json:"path"`
	DirectoryID  NativeID `json:"directory_id"`
	ManifestPath string   `json:"manifest_path"`
	ManifestID   NativeID `json:"manifest_id"`
}

type WorkerBirthIntentData struct {
	Schema                uint32                       `json:"schema"`
	Domain                string                       `json:"domain"`
	Role                  string                       `json:"role"`
	Home                  string                       `json:"home"`
	HomeID                NativeID                     `json:"home_id"`
	ServiceDebitID        string                       `json:"service_debit_id"`
	LaunchKey             string                       `json:"launch_key"`
	ProfileSHA256         string                       `json:"profile_sha256"`
	ContractSHA256        string                       `json:"contract_sha256"`
	Build                 Build                        `json:"build"`
	ControlID             NativeID                     `json:"control_id"`
	Fence                 residentrefs.FenceIdentities `json:"fence"`
	Image                 mesh.ResidentImageInventory  `json:"image"`
	SettingsSHA256        string                       `json:"settings_sha256"`
	InitialPackageSHA256  string                       `json:"initial_package_sha256"`
	InitialManifestSHA256 string                       `json:"initial_manifest_sha256"`
	InitialManifestBytes  uint32                       `json:"initial_manifest_bytes"`
	Source                WorkerPackageSourceData      `json:"source"`
}

func workerImageIdentityData(id mesh.ResidentImageInventoryID) residentrefs.Identity {
	return residentrefs.Identity{Device: id.Device, Inode: id.Inode, UID: id.UID, GID: id.GID, Mode: id.Mode}
}

func (d WorkerBirthIntentData) Validate() error {
	if d.Schema != 2 || d.Domain != workerBirthIntentDomain || d.Role != WorkerCoreRole || !validInputPath(d.Home) || d.HomeID.Inode == 0 || !boundedText(d.LaunchKey, 128) || !validWorkerBuildData(d.Build) || d.ControlID.Inode == 0 || d.ControlID.UID != d.HomeID.UID {
		return ErrInvalid
	}
	for _, h := range []string{d.ServiceDebitID, d.ProfileSHA256, d.ContractSHA256, d.SettingsSHA256, d.InitialPackageSHA256, d.InitialManifestSHA256} {
		if !validDigest(h) {
			return ErrInvalid
		}
	}
	if d.InitialManifestBytes == 0 || d.InitialManifestBytes > workerManifestBytes || sharedID(d.Fence.Home) != d.HomeID {
		return ErrInvalid
	}
	for _, id := range []residentrefs.Identity{d.Fence.Home, d.Fence.Daemon, d.Fence.Directory, d.Fence.Ledger, d.Fence.Launch, d.Fence.Intent} {
		if id.Inode == 0 || id.UID != d.HomeID.UID {
			return ErrInvalid
		}
	}
	if d.Image.Home != d.Home || d.Image.DebitID != d.ServiceDebitID || d.Image.Path != filepath.Join(d.Home, "resident-images", "pin-"+d.ServiceDebitID, "arm") || d.Image.SHA256 != d.Build.SHA256 || d.Image.Size != d.Build.Size || sharedID(workerImageIdentityData(d.Image.HomeID)) != d.HomeID {
		return ErrInvalid
	}
	for _, id := range []mesh.ResidentImageInventoryID{d.Image.HomeID, d.Image.ImagesID, d.Image.DirectoryID, d.Image.ImageID} {
		if id.Inode == 0 || id.UID != d.HomeID.UID {
			return ErrInvalid
		}
	}
	if !validInputPath(d.Source.Path) || !validInputPath(d.Source.ManifestPath) || d.Source.ManifestPath != filepath.Join(d.Source.Path, "manifest.json") || d.Source.DirectoryID.Inode == 0 || d.Source.ManifestID.Inode == 0 || d.Source.DirectoryID.UID != d.HomeID.UID || d.Source.ManifestID.UID != d.HomeID.UID {
		return ErrInvalid
	}
	raw, err := json.Marshal(d)
	if err != nil || len(raw) > workerBootstrapMaxWireBytes-workerTransportHeaderBytes {
		return ErrInvalid
	}
	return nil
}

type WorkerBirthBinding struct {
	self         *WorkerBirthBinding
	child        *WorkerChildPending
	charge       *WorkerManagerCharge
	raw          []byte
	intent       WorkerBirthIntentData
	intentSHA256 string
	complete     bool
	serveTried   bool
	serve        *WorkerServeBirthView
}

// An actual native serve constructor may claim this once-only nonclosing view.
// The view is not a native fence or Manager claim and has no public factory.
type WorkerServeBirthView struct {
	self  *WorkerServeBirthView
	birth *WorkerBirthBinding
}

type WorkerBirthSelectorsData struct {
	Home                  string   `json:"home"`
	HomeID                NativeID `json:"home_id"`
	DebitID               string   `json:"debit_id"`
	ProfileSHA256         string   `json:"profile_sha256"`
	ContractSHA256        string   `json:"contract_sha256"`
	IntentSHA256          string   `json:"intent_sha256"`
	SettingsSHA256        string   `json:"settings_sha256"`
	ImageSHA256           string   `json:"image_sha256"`
	InitialPackageSHA256  string   `json:"initial_package_sha256"`
	InitialManifestSHA256 string   `json:"initial_manifest_sha256"`
}

func (p *WorkerChildPending) BindOriginalWorkerBirth(charge *WorkerManagerCharge, intent []byte) (birth *WorkerBirthBinding, resultErr error) {
	if charge == nil || charge.self != charge || p == nil || charge.child != p {
		return nil, ErrInvalid
	}
	if len(intent) == 0 || len(intent) > workerBootstrapMaxWireBytes-workerTransportHeaderBytes {
		return nil, ErrInvalid
	}
	op, err := p.beginOriginal("worker-child-birth")
	if err != nil {
		return nil, err
	}
	normal := false
	defer p.custody.raw.finish(op, &normal, &resultErr)
	birth = &WorkerBirthBinding{child: p, charge: charge}
	birth.self = birth
	p.custody.raw.mu.Lock()
	p.birth = birth
	p.custody.raw.mu.Unlock()
	birth.raw = append([]byte(nil), intent...)
	resultErr = p.bindBirthBodyOriginal(birth)
	if resultErr == nil {
		p.custody.raw.mu.Lock()
		birth.complete = true
		p.custody.raw.mu.Unlock()
	}
	normal = true
	return birth, resultErr
}

func (p *WorkerChildPending) bindBirthBodyOriginal(b *WorkerBirthBinding) error {
	w := p.custody
	if b.charge != p.charge || b.charge.owner != w || decodeOperatorDocument(b.raw, workerBootstrapMaxWireBytes-workerTransportHeaderBytes, &b.intent) != nil || b.intent.Validate() != nil {
		return ErrInvalid
	}
	if err := p.currentBodyOriginal(p.rootContext, true); err != nil {
		return err
	}
	d := b.intent
	g, record := w.grant, w.debit.record
	if d.Home != w.path || d.HomeID != g.Intent.HomeID || d.ServiceDebitID != record.ID || d.LaunchKey != record.LaunchKey || d.ProfileSHA256 != record.ProfileSHA256 || d.ContractSHA256 != record.ContractSHA256 || d.Build != g.Intent.Profile.Core.Build || d.ControlID != p.bootstrap.ChannelID || d.InitialPackageSHA256 != record.InitialPackageSHA256 || d.InitialManifestSHA256 != record.InitialManifestSHA256 || d.InitialManifestBytes != g.Intent.Profile.Contract.InitialManifestBytes {
		return ErrInvalid
	}
	ids := p.runtimeProof.Identities
	if workerImageIdentityData(d.Image.ImageID) != ids.Image || workerImageIdentityData(d.Image.ImagesID) != ids.Images || workerImageIdentityData(d.Image.DirectoryID) != ids.Directory {
		return ErrInvalid
	}
	w.raw.mu.Lock()
	b.intentSHA256 = digest(b.raw)
	w.raw.mu.Unlock()
	return nil
}

func (b *WorkerBirthBinding) originalData() (*WorkerChildPending, *workerCustody, error) {
	if b == nil || b.self != b || b.child == nil || b.child.self != b.child || b.child.custody == nil || b.child.custody.raw == nil {
		return nil, nil, ErrInvalid
	}
	p := b.child
	w := p.custody
	w.raw.mu.Lock()
	valid := p.birth == b && b.complete && b.charge != nil && b.charge.self == b.charge && p.charge == b.charge && b.charge.child == p && b.charge.owner == w && p.runtimeBound && p.currentShapeLocked() && validDigest(b.intentSHA256) && digest(b.raw) == b.intentSHA256
	w.raw.mu.Unlock()
	if !valid {
		return nil, nil, ErrInvalid
	}
	return p, w, nil
}

func (b *WorkerBirthBinding) selectorsData() WorkerBirthSelectorsData {
	d := b.intent
	return WorkerBirthSelectorsData{Home: d.Home, HomeID: d.HomeID, DebitID: d.ServiceDebitID, ProfileSHA256: d.ProfileSHA256, ContractSHA256: d.ContractSHA256, IntentSHA256: b.intentSHA256, SettingsSHA256: d.SettingsSHA256, ImageSHA256: d.Build.SHA256, InitialPackageSHA256: d.InitialPackageSHA256, InitialManifestSHA256: d.InitialManifestSHA256}
}

func (b *WorkerBirthBinding) ClaimOriginalServeView() (view *WorkerServeBirthView, resultErr error) {
	p, w, err := b.originalData()
	if err != nil {
		return nil, err
	}
	op, err := p.beginOriginal("worker-birth-current")
	if err != nil {
		return nil, err
	}
	normal := false
	defer w.raw.finish(op, &normal, &resultErr)
	w.raw.mu.Lock()
	if b.serveTried {
		w.raw.mu.Unlock()
		resultErr = ErrInvalid
		normal = true
		return nil, resultErr
	}
	b.serveTried = true
	w.raw.mu.Unlock()
	resultErr = p.currentBodyOriginal(p.rootContext, true)
	if resultErr == nil {
		view = &WorkerServeBirthView{birth: b}
		view.self = view
		w.raw.mu.Lock()
		b.serve = view
		w.raw.mu.Unlock()
	}
	normal = true
	return view, resultErr
}

func (v *WorkerServeBirthView) ValidateOriginal(ctx context.Context) (selectors WorkerBirthSelectorsData, resultErr error) {
	if v == nil || v.self != v || v.birth == nil {
		return selectors, ErrInvalid
	}
	p, w, err := v.birth.originalData()
	if err != nil {
		return selectors, err
	}
	w.raw.mu.Lock()
	valid := v.birth.serve == v && v.birth.serveTried
	w.raw.mu.Unlock()
	if !valid || !sameWorkerContextData(ctx, p.rootContext) {
		return selectors, ErrInvalid
	}
	op, err := p.beginOriginal("worker-birth-current")
	if err != nil {
		return selectors, err
	}
	normal := false
	defer w.raw.finish(op, &normal, &resultErr)
	resultErr = p.currentBodyOriginal(ctx, true)
	if resultErr == nil {
		w.raw.mu.Lock()
		selectors = v.birth.selectorsData()
		w.raw.mu.Unlock()
	}
	normal = true
	return selectors, resultErr
}

// Require the actual worker fence recipient of this same original process
// census, then compare its current native identities with the committed intent.
func (v *WorkerServeBirthView) ValidateOriginalFence(ctx context.Context, fence *residentrefs.FenceOriginal) (selectors WorkerBirthSelectorsData, resultErr error) {
	if v == nil || v.self != v || v.birth == nil || fence == nil {
		return selectors, ErrInvalid
	}
	p, w, err := v.birth.originalData()
	if err != nil {
		return selectors, err
	}
	w.raw.mu.Lock()
	valid := v.birth.serve == v && v.birth.serveTried
	w.raw.mu.Unlock()
	if !valid || !sameWorkerContextData(ctx, p.rootContext) {
		return selectors, ErrInvalid
	}
	op, err := p.beginOriginal("worker-birth-current")
	if err != nil {
		return selectors, err
	}
	normal := false
	defer w.raw.finish(op, &normal, &resultErr)
	resultErr = p.currentBodyOriginal(ctx, true)
	if resultErr == nil {
		resultErr = fence.RequireOriginalWorkerCore()
	}
	if resultErr == nil {
		resultErr = p.source.ValidateOriginalFenceLink(fence)
	}
	if resultErr == nil {
		var ids residentrefs.FenceIdentities
		ids, resultErr = fence.ValidateOriginal(ctx)
		if resultErr == nil && ids != v.birth.intent.Fence {
			resultErr = ErrInvalid
		}
	}
	if resultErr == nil {
		w.raw.mu.Lock()
		selectors = v.birth.selectorsData()
		w.raw.mu.Unlock()
	}
	normal = true
	return selectors, resultErr
}
