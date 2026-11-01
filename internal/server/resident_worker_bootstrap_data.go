package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
)

var errResidentWorkerChildOriginal = errors.New("original composed worker resident child required; retain original custody")

// Complete canonical private-channel DATA. Neither the frame nor its hashes
// can create an original inherited recipient, grant, root or worker authority.
type workerRootBootstrapData struct {
	Schema         uint32                                  `json:"schema"`
	Domain         string                                  `json:"domain"`
	Service        residentservice.WorkerBootstrapData     `json:"service"`
	Fence          residentbirth.WorkerFenceData           `json:"fence"`
	Image          mesh.ResidentImageInventory             `json:"image"`
	Settings       daemonconfig.Settings                   `json:"settings"`
	SettingsSHA256 string                                  `json:"settings_sha256"`
	Source         residentservice.WorkerPackageSourceData `json:"source"`
}

func encodeWorkerRootIntentData(b workerRootBootstrapData) ([]byte, error) {
	service := b.Service
	i := residentservice.WorkerBirthIntentData{Schema: 2, Domain: "arm-resident-worker-intent-v1", Role: residentservice.WorkerCoreRole, Home: service.Home, HomeID: service.HomeID, ServiceDebitID: service.DebitID, LaunchKey: service.LaunchKey, ProfileSHA256: service.ProfileSHA256, ContractSHA256: service.ContractSHA256, Build: service.Build, ControlID: service.ChannelID, Fence: b.Fence.Identities, Image: b.Image, SettingsSHA256: b.SettingsSHA256, InitialPackageSHA256: service.InitialPackageSHA256, InitialManifestSHA256: service.InitialManifestSHA256, InitialManifestBytes: service.InitialManifestBytes, Source: b.Source}
	if i.Validate() != nil {
		return nil, errResidentWorkerChildOriginal
	}
	raw, err := json.Marshal(i)
	if err != nil || len(raw) == 0 || len(raw) > residentrefs.MaxWireBytes-36 {
		return nil, errResidentWorkerChildOriginal
	}
	return raw, nil
}

func validateWorkerRootBootstrapData(b workerRootBootstrapData) error {
	if b.Schema != 2 || b.Domain != "arm-resident-worker-root-bootstrap-v1" || b.Service.Validate() != nil || b.Fence.Schema != 2 || b.Fence.Domain != "arm-resident-worker-fence-v1" || b.Fence.Home != b.Service.Home || b.Image.Home != b.Service.Home || b.Image.DebitID != b.Service.DebitID || b.Image.Path != filepath.Join(b.Service.Home, "resident-images", "pin-"+b.Service.DebitID, "arm") || b.Image.SHA256 != b.Service.Build.SHA256 || b.Image.Size != b.Service.Build.Size {
		return errResidentWorkerChildOriginal
	}
	settings, err := daemonconfig.SnapshotSHA256(b.Settings)
	if err != nil || settings != b.SettingsSHA256 {
		return errResidentWorkerChildOriginal
	}
	intent, err := encodeWorkerRootIntentData(b)
	if err != nil || len(intent) != b.Fence.IntentBytes {
		return errResidentWorkerChildOriginal
	}
	h := sha256.Sum256(intent)
	if hex.EncodeToString(h[:]) != b.Fence.IntentSHA256 {
		return errResidentWorkerChildOriginal
	}
	return nil
}

func encodeWorkerRootBootstrapPayloadData(b workerRootBootstrapData) ([]byte, error) {
	if validateWorkerRootBootstrapData(b) != nil {
		return nil, errResidentWorkerChildOriginal
	}
	raw, err := json.Marshal(b)
	if err != nil || len(raw) == 0 || len(raw) > residentrefs.MaxWireBytes-36 {
		return nil, errResidentWorkerChildOriginal
	}
	return raw, nil
}

func decodeWorkerRootBootstrapData(raw []byte, value *workerRootBootstrapData) error {
	if value == nil || len(raw) == 0 || len(raw) > residentrefs.MaxWireBytes-36 {
		return errResidentWorkerChildOriginal
	}
	var decoded workerRootBootstrapData
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&decoded) != nil || d.Decode(new(any)) != io.EOF {
		return errResidentWorkerChildOriginal
	}
	canonical, err := encodeWorkerRootBootstrapPayloadData(decoded)
	if err != nil || !bytes.Equal(canonical, raw) {
		return errResidentWorkerChildOriginal
	}
	*value = decoded
	return nil
}

func validateWorkerRootComposedData(b workerRootBootstrapData, actual residentrefs.ComposedIdentities) error {
	if validateWorkerRootBootstrapData(b) != nil || actual.Control.Device != b.Service.ChannelID.Device || actual.Control.Inode != b.Service.ChannelID.Inode || actual.Control.UID != b.Service.ChannelID.UID || actual.Fence != b.Fence.Identities || actual.Service.Home != actual.Fence.Home {
		return errResidentWorkerChildOriginal
	}
	convert := func(id mesh.ResidentImageInventoryID) residentrefs.Identity {
		return residentrefs.Identity{Device: id.Device, Inode: id.Inode, UID: id.UID, GID: id.GID, Mode: id.Mode}
	}
	if actual.Image.Image != convert(b.Image.ImageID) || actual.Image.Images != convert(b.Image.ImagesID) || actual.Image.Directory != convert(b.Image.DirectoryID) || actual.Fence.Home != convert(b.Image.HomeID) {
		return errResidentWorkerChildOriginal
	}
	home := residentservice.NativeID{Device: actual.Service.Home.Device, Inode: actual.Service.Home.Inode, UID: actual.Service.Home.UID}
	if home != b.Service.HomeID {
		return errResidentWorkerChildOriginal
	}
	return nil
}
