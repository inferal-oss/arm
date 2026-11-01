package residentservice

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
)

func workerBirthIntentFixtureData(t *testing.T) WorkerBirthIntentData {
	t.Helper()
	b, _ := workerBootstrapFixtureData(t)
	id := func(n uint64) residentrefs.Identity {
		return residentrefs.Identity{Device: 1, Inode: n, UID: b.HomeID.UID}
	}
	imageID := func(n uint64) mesh.ResidentImageInventoryID {
		return mesh.ResidentImageInventoryID{Device: 1, Inode: n, UID: b.HomeID.UID}
	}
	return WorkerBirthIntentData{Schema: 2, Domain: workerBirthIntentDomain, Role: WorkerCoreRole, Home: b.Home, HomeID: b.HomeID, ServiceDebitID: b.DebitID, LaunchKey: b.LaunchKey, ProfileSHA256: b.ProfileSHA256, ContractSHA256: b.ContractSHA256, Build: b.Build, ControlID: b.ChannelID, Fence: residentrefs.FenceIdentities{Home: id(b.HomeID.Inode), Daemon: id(2), Directory: id(3), Ledger: id(4), Launch: id(5), Intent: id(6)}, Image: mesh.ResidentImageInventory{Home: b.Home, DebitID: b.DebitID, Path: filepath.Join(b.Home, "resident-images", "pin-"+b.DebitID, "arm"), SHA256: b.Build.SHA256, Size: b.Build.Size, HomeID: imageID(b.HomeID.Inode), ImagesID: imageID(11), DirectoryID: imageID(12), ImageID: imageID(13)}, SettingsSHA256: strings.Repeat("f", 64), InitialPackageSHA256: b.InitialPackageSHA256, InitialManifestSHA256: b.InitialManifestSHA256, InitialManifestBytes: b.InitialManifestBytes, Source: WorkerPackageSourceData{Path: "/synthetic/package", DirectoryID: NativeID{1, 20, b.HomeID.UID}, ManifestPath: "/synthetic/package/manifest.json", ManifestID: NativeID{1, 21, b.HomeID.UID}}}
}

func TestWorkerBirthIntentCanonicalAndFullFrameBoundsData(t *testing.T) {
	d := workerBirthIntentFixtureData(t)
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(d)
	var again WorkerBirthIntentData
	if decodeOperatorDocument(raw, workerBootstrapMaxWireBytes-workerTransportHeaderBytes, &again) != nil || again.Validate() != nil {
		t.Fatal("canonical intent DATA refused")
	}
	d.Home = "/" + strings.Repeat("h", 3000)
	d.Image.Home = d.Home
	d.Image.Path = filepath.Join(d.Home, "resident-images", "pin-"+d.ServiceDebitID, "arm")
	d.Source.Path = d.Home + "/package"
	d.Source.ManifestPath = filepath.Join(d.Source.Path, "manifest.json")
	if d.Validate() == nil {
		t.Fatal("whole-frame limit omitted")
	}
}

func TestWorkerBirthIntentRejectsRoleLocatorsAndVersionsData(t *testing.T) {
	for _, name := range []string{"schema", "domain", "role", "hash", "control", "fence", "image", "manifest-size", "locator", "manifest-locator", "source-id"} {
		t.Run(name, func(t *testing.T) {
			d := workerBirthIntentFixtureData(t)
			switch name {
			case "schema":
				d.Schema = 1
			case "domain":
				d.Domain = "old"
			case "role":
				d.Role = "--arm-resident-core-v1"
			case "hash":
				d.ContractSHA256 = "missing"
			case "control":
				d.ControlID.UID++
			case "fence":
				d.Fence.Ledger.Inode = 0
			case "image":
				d.Image.SHA256 = strings.Repeat("0", 64)
			case "manifest-size":
				d.InitialManifestBytes = 0
			case "locator":
				d.Source.Path = "relative"
			case "manifest-locator":
				d.Source.ManifestPath = "/other/manifest.json"
			case "source-id":
				d.Source.ManifestID.Inode = 0
			}
			if d.Validate() == nil {
				t.Fatal("invalid intent DATA accepted")
			}
		})
	}
}

func TestWorkerBirthAndServeViewsCannotBeIssuedFromData(t *testing.T) {
	p := unissuedWorkerChildData()
	d := workerBirthIntentFixtureData(t)
	raw, _ := json.Marshal(d)
	if b, err := p.BindOriginalWorkerBirth(&WorkerManagerCharge{}, raw); err == nil || b != nil {
		t.Fatal("zero charge issued birth")
	}
	var zero WorkerBirthBinding
	if view, err := zero.ClaimOriginalServeView(); err == nil || view != nil {
		t.Fatal("zero birth issued view")
	}
	b := &WorkerBirthBinding{child: p, raw: raw, intent: d}
	b.self = b
	p.birth = b
	copy := *b
	if view, err := copy.ClaimOriginalServeView(); err == nil || view != nil {
		t.Fatal("copied birth issued view")
	}
	var view WorkerServeBirthView
	if _, err := view.ValidateOriginal(context.Background()); err == nil {
		t.Fatal("zero serve view accepted")
	}
	assertWorkerNoNativeData(t, p.custody)
}
