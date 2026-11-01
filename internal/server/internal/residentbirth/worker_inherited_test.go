package residentbirth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/residentservice"
)

func workerFenceIntentFixtureData(t *testing.T) ([]byte, WorkerFenceData) {
	t.Helper()
	home, debit := "/synthetic/home", strings.Repeat("a", 64)
	id := func(n uint64) residentrefs.Identity { return residentrefs.Identity{Device: 1, Inode: n, UID: 1} }
	imageID := func(n uint64) mesh.ResidentImageInventoryID {
		return mesh.ResidentImageInventoryID{Device: 1, Inode: n, UID: 1}
	}
	ids := residentrefs.FenceIdentities{Home: id(1), Daemon: id(2), Directory: id(3), Ledger: id(4), Launch: id(5), Intent: id(6)}
	build := residentservice.Build{SHA256: strings.Repeat("1", 64), Size: 1, GOOS: "linux", GOARCH: "arm64", GoBuild: "fixture", Protocol: 1}
	d := residentservice.WorkerBirthIntentData{
		Schema: 2, Domain: "arm-resident-worker-intent-v1", Role: residentservice.WorkerCoreRole,
		Home: home, HomeID: residentservice.NativeID{Device: 1, Inode: 1, UID: 1}, ServiceDebitID: debit, LaunchKey: "fixture",
		ProfileSHA256: strings.Repeat("b", 64), ContractSHA256: strings.Repeat("c", 64), Build: build,
		ControlID: residentservice.NativeID{Device: 1, Inode: 10, UID: 1}, Fence: ids,
		Image:          mesh.ResidentImageInventory{Home: home, DebitID: debit, Path: filepath.Join(home, "resident-images", "pin-"+debit, "arm"), SHA256: build.SHA256, Size: build.Size, HomeID: imageID(1), ImagesID: imageID(11), DirectoryID: imageID(12), ImageID: imageID(13)},
		SettingsSHA256: strings.Repeat("d", 64), InitialPackageSHA256: strings.Repeat("e", 64), InitialManifestSHA256: strings.Repeat("f", 64), InitialManifestBytes: 1,
		Source: residentservice.WorkerPackageSourceData{Path: "/synthetic/package", DirectoryID: residentservice.NativeID{Device: 1, Inode: 20, UID: 1}, ManifestPath: "/synthetic/package/manifest.json", ManifestID: residentservice.NativeID{Device: 1, Inode: 21, UID: 1}},
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return raw, WorkerFenceData{Schema: 2, Domain: "arm-resident-worker-fence-v1", Home: home, Identities: ids, IntentBytes: len(raw), IntentSHA256: hex.EncodeToString(sum[:])}
}

func TestWorkerFenceCanonicalIntentDataAndRoleRefusals(t *testing.T) {
	raw, expected := workerFenceIntentFixtureData(t)
	if _, err := decodeWorkerFenceIntentData(raw, expected); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unknown", "duplicate", "trailing", "whitespace", "schema", "role", "domain", "hash", "size", "home", "identities", "oversize"} {
		t.Run(name, func(t *testing.T) {
			changed := append([]byte(nil), raw...)
			d := expected
			switch name {
			case "unknown":
				changed = append([]byte(`{"unknown":1,`), raw[1:]...)
			case "duplicate":
				changed = append([]byte(`{"schema":2,`), raw[1:]...)
			case "trailing":
				changed = append(changed, []byte("{}")...)
			case "whitespace":
				changed = append(changed, '\n')
			case "schema":
				d.Schema = 1
			case "role":
				changed = []byte(strings.Replace(string(raw), residentservice.WorkerCoreRole, "--arm-resident-core-v1", 1))
			case "domain":
				d.Domain = "arm-resident-fence-v1"
			case "home":
				d.Home = "/other"
			case "identities":
				d.Identities.Ledger.Inode++
			case "oversize":
				changed = make([]byte, residentrefs.MaxWireBytes-35)
			}
			d.IntentBytes = len(changed)
			sum := sha256.Sum256(changed)
			d.IntentSHA256 = hex.EncodeToString(sum[:])
			if name == "hash" {
				d.IntentSHA256 = strings.Repeat("0", 64)
			}
			if name == "size" {
				d.IntentBytes++
			}
			if _, err := decodeWorkerFenceIntentData(changed, d); err == nil {
				t.Fatal("invalid worker fence DATA accepted")
			}
		})
	}
}

func TestWorkerFenceUnissuedSourcesAndCopiesRefuseBeforeEffects(t *testing.T) {
	var destination *WorkerInheritedFencePending
	if err := NewWorkerInheritedFencePending(&destination, context.Background(), &residentrefs.FenceOriginal{}); err == nil || destination != nil {
		t.Fatal("unissued fence published a view")
	}
	p := &WorkerInheritedFencePending{}
	p.state = &workerInheritedFenceState{self: p, custody: &residentrefs.FenceOriginal{}, rootContext: context.Background()}
	copyPending := *p
	for _, source := range []*WorkerInheritedFencePending{nil, {}, &copyPending, p} {
		if err := source.VerifyOriginal(context.Background(), WorkerFenceData{}); err == nil {
			t.Fatal("unissued fence verified")
		}
		if _, _, err := source.ValidateForOriginalNative(); err == nil {
			t.Fatal("unissued fence supplied native authority")
		}
		if err := source.ValidateForOriginalDatabase(); err == nil {
			t.Fatal("unissued fence supplied database authority")
		}
		if _, err := source.NativeSelectorsOriginal(); err == nil {
			t.Fatal("unissued fence supplied selectors")
		}
		if _, err := source.ValidateOriginalBirth(&residentservice.WorkerServeBirthView{}); err == nil {
			t.Fatal("unissued birth/fence linked")
		}
	}
	if p.state.active != nil || p.state.firstError != nil || p.state.verifyTried || p.state.journal != nil || p.state.before != nil || p.state.after != nil || len(p.state.intent) != 0 {
		t.Fatal("unissued fence reached a native operation")
	}
}
