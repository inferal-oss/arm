package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
)

// Deliberately synthetic DATA only; no producer, source, FD, grant or native
// claim is created by this grammar fixture or the canonical encoders below.
func workerRootBootstrapFixtureData(t *testing.T) workerRootBootstrapData {
	t.Helper()
	digest := strings.Repeat("a", 64)
	id := func(inode uint64) residentrefs.Identity {
		return residentrefs.Identity{Device: 1, Inode: inode, UID: 1, GID: 1, Mode: 0700}
	}
	imageID := func(inode uint64) mesh.ResidentImageInventoryID {
		return mesh.ResidentImageInventoryID{Device: 1, Inode: inode, UID: 1, GID: 1, Mode: 0700}
	}
	settings := daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0"}
	settingsSHA, err := daemonconfig.SnapshotSHA256(settings)
	if err != nil {
		t.Fatal(err)
	}
	b := workerRootBootstrapData{Schema: 2, Domain: "arm-resident-worker-root-bootstrap-v1", Settings: settings, SettingsSHA256: settingsSHA,
		Service: residentservice.WorkerBootstrapData{Schema: 2, Domain: "arm-resident-worker-bootstrap-v1", Role: residentservice.WorkerCoreRole, ChannelID: residentservice.NativeID{Device: 1, Inode: 2, UID: 1}, Home: "/synthetic", HomeID: residentservice.NativeID{Device: 1, Inode: 1, UID: 1}, Build: residentservice.Build{SHA256: digest, Size: 1, GOOS: "linux", GOARCH: "amd64", GoBuild: "synthetic-go", Protocol: 1}, DebitID: digest, LaunchKey: "synthetic-launch", ProfileSHA256: digest, ContractSHA256: digest, InitialPackageSHA256: digest, InitialManifestSHA256: digest, InitialManifestBytes: 2, JournalBytes: 178, JournalSHA256: digest, Secret: [32]byte{1}},
		Fence:   residentbirth.WorkerFenceData{Schema: 2, Domain: "arm-resident-worker-fence-v1", Home: "/synthetic", Identities: residentrefs.FenceIdentities{Home: id(1), Daemon: id(3), Directory: id(4), Ledger: id(5), Launch: id(6), Intent: id(7)}},
		Image:   mesh.ResidentImageInventory{Home: "/synthetic", DebitID: digest, Path: "/synthetic/resident-images/pin-" + digest + "/arm", SHA256: digest, Size: 1, HomeID: imageID(1), ImagesID: imageID(8), DirectoryID: imageID(9), ImageID: imageID(10)},
		Source:  residentservice.WorkerPackageSourceData{Path: "/synthetic/package", ManifestPath: "/synthetic/package/manifest.json", DirectoryID: residentservice.NativeID{Device: 1, Inode: 11, UID: 1}, ManifestID: residentservice.NativeID{Device: 1, Inode: 12, UID: 1}}}
	intent, err := encodeWorkerRootIntentData(b)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(intent)
	b.Fence.IntentBytes = len(intent)
	b.Fence.IntentSHA256 = hex.EncodeToString(h[:])
	return b
}

func TestWorkerRootBootstrapCanonicalOuterFrameAndRoleSeparationData(t *testing.T) {
	b := workerRootBootstrapFixtureData(t)
	raw, err := encodeWorkerRootBootstrapPayloadData(b)
	if err != nil {
		t.Fatal(err)
	}
	var parsed workerRootBootstrapData
	if err = decodeWorkerRootBootstrapData(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	again, err := encodeWorkerRootBootstrapPayloadData(parsed)
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatal("noncanonical full bootstrap", err)
	}
	for _, bad := range [][]byte{append(append([]byte(nil), raw...), ' '), bytes.Replace(raw, []byte(`"schema":2`), []byte(`"schema":2,"schema":2`), 1), []byte(`{"schema":2}`)} {
		if err = decodeWorkerRootBootstrapData(bad, &parsed); err == nil {
			t.Fatal("unknown/duplicate/noncanonical DATA accepted")
		}
	}
	b.Service.Role = ResidentCoreRole
	if _, err = encodeWorkerRootBootstrapPayloadData(b); err == nil {
		t.Fatal("legacy role accepted")
	}
	b = workerRootBootstrapFixtureData(t)
	b.Source.ManifestPath = "/synthetic/other.json"
	if _, err = encodeWorkerRootBootstrapPayloadData(b); err == nil {
		t.Fatal("changed source locator accepted")
	}
	if ResidentWorkerCoreRole != residentservice.WorkerCoreRole {
		t.Fatal("private role mismatch")
	}
}

func TestWorkerPrivateRoleAndRootRefuseBeforeProcessCensus(t *testing.T) {
	for _, args := range [][]string{nil, {}, {ResidentCoreRole}, {ResidentWorkerCoreRole, "extra"}, {" " + ResidentWorkerCoreRole}} {
		if originalResidentWorkerCoreArgs(args) || RunOriginalResidentWorkerCore(args) != 1 {
			t.Fatal("invalid role entered private startup")
		}
	}
	if !originalResidentWorkerCoreArgs([]string{ResidentWorkerCoreRole}) {
		t.Fatal("exact role rejected")
	}
	root := &residentWorkerChildPending{}
	if root.initializeOriginal() == nil || root.refs != nil || root.self != nil || root.mu != nil {
		t.Fatal("unissued entry claimed raw13")
	}
	root.self = root
	if root.prepareOriginalBindings(context.Background()) == nil || root.refs != nil || root.prepareTried {
		t.Fatal("unissued root entered effects")
	}
	if processResidentWorkerCoreEntry.call != nil || processResidentWorkerCoreEntry.root != nil {
		t.Fatal("pure refusal claimed original entry")
	}
}

func TestWorkerRootPreparedAndProtocolFlagsCannotCreateAuthority(t *testing.T) {
	for _, root := range []*residentWorkerChildPending{nil, {}, {self: &residentWorkerChildPending{}}} {
		if root.validateOriginalPreparedBindings() == nil || root.commitOriginalRootBound() == nil || root.exchangeRootProtocolOriginal() == nil {
			t.Fatal("unissued root accepted prepared/proof flags")
		}
	}
	if _, err := originalWorkerRootProtocolContextData(nil, nil); err == nil {
		t.Fatal("DATA issued original protocol context")
	}
	called := false
	phase := &residentWorkerProtocolPhase{index: 1, ioAttempted: true, cancel: func() { called = true }}
	if phase.finishIOOriginal(nil) == nil || called {
		t.Fatal("unissued phase canceled original lifetime")
	}
}

func TestWorkerCurrentServerRequiresOriginalManagerAndRootContext(t *testing.T) {
	root := context.Background()
	m, err := manager.NewResidentWorkerManagerPending(&lifetimeview.WorkerManagerView{}, daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0"}, &serviceview.WorkerManagerCharge{})
	if err != nil {
		t.Fatal(err)
	}
	o, err := newResidentWorkerServerPending(&lifetimeview.WorkerServerView{}, m, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = o.validateCurrentOriginal(root, &lifetimeview.WorkerServerView{}, &serviceview.WorkerCurrentChallengeView{}, &serviceview.WorkerManagerCharge{}); err == nil {
		t.Fatal("unissued current Server accepted")
	}
	if _, err = o.currentOriginalData(nil, nil, nil); err == nil {
		t.Fatal("prepared flags supplied current Server")
	}
	if o.currentTried || o.currentValidation != nil || o.active != nil || o.claim != nil || o.server.Manager != nil || o.started {
		t.Fatal("refusal entered native/Manager work")
	}
}
