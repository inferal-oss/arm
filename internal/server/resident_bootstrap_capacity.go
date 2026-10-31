package server

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strings"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/mesh"
	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
)

// CAPACITY DATA ONLY. All maximal numeric values below are local encoded-size
// sentinels, not native observations/holders/identities/grants or a bootstrap to
// send. Only its byte length leaves this function. The original parent must run
// it on actual unused-grant/settings/launch selectors BEFORE debit/image/fence
// namespace effects, then encode/check the actual frame again before one Start.
func p1BootstrapCapacityData(inputs residentservice.P1LaunchInputsData, settings daemonconfig.Settings, launchKey string) (int, error) {
	if len(inputs.Home) == 0 || len(inputs.Home) > 4096 || len(launchKey) == 0 || len(launchKey) > 128 {
		return 0, errResidentChildOriginal
	}
	settingsHash, err := daemonconfig.SnapshotSHA256(settings)
	if err != nil {
		return 0, err
	}
	selector := strings.Repeat("f", 64)
	native := residentrefs.Identity{Device: math.MaxUint64, Inode: math.MaxUint64, UID: math.MaxUint32, GID: math.MaxUint32, Mode: math.MaxUint32}
	imageID := mesh.ResidentImageInventoryID{Device: math.MaxUint64, Inode: math.MaxUint64, UID: math.MaxUint32, GID: math.MaxUint32, Mode: math.MaxUint32}
	bootstrap := p1RootBootstrap{Schema: 2, Service: residentservice.P1BootstrapData{Schema: 2, Home: inputs.Home, Build: inputs.Build, DebitID: selector, LaunchKey: launchKey, ProfileSHA256: inputs.ProfileSHA256, JournalBytes: residentservice.MaxJournalBytes, JournalSHA256: selector, ChannelID: residentservice.NativeID{Device: math.MaxUint64, Inode: math.MaxUint64, UID: math.MaxUint32}},
		Fence: residentbirth.P1FenceData{Home: inputs.Home, Identities: residentrefs.FenceIdentities{Launch: native, Intent: native, Home: native, Daemon: native, Directory: native, Ledger: native}, IntentBytes: 16 << 10, IntentSHA256: selector},
		Image: mesh.ResidentImageInventory{Home: inputs.Home, DebitID: selector, Path: filepath.Join(inputs.Home, "resident-images", "pin-"+selector, "arm"), SHA256: inputs.Build.SHA256, Size: inputs.Build.Size, HomeID: imageID, ImagesID: imageID, DirectoryID: imageID, ImageID: imageID}, Settings: settings, SettingsSHA256: settingsHash}
	for at := range bootstrap.Service.Secret {
		bootstrap.Service.Secret[at] = 255
	}
	intent, err := encodeP1IntentData(bootstrap)
	if err != nil || len(intent) > 16<<10 {
		return 0, errResidentChildOriginal
	}
	// Exact known key (including canonical JSON escaping) and actual unused-grant
	// journal size, before debit/image/fence/Start. Other fields are DATA maxima.
	serviceBytes, err := residentservice.P1RootBoundJournalCapacityData(inputs.GrantJournalBytes, launchKey)
	if err != nil {
		return 0, err
	}
	footerData, err := residentbirth.P1FooterForServiceData(residentservice.P1RootBoundData{Record: residentservice.RootBoundRecord{DebitID: selector, LaunchKey: launchKey, Generation: strings.Repeat("f", 32), CommitmentSHA256: selector}, ProfileSHA256: inputs.ProfileSHA256, ImageSHA256: inputs.Build.SHA256, JournalBytes: serviceBytes, JournalSHA256: selector}, selector, settingsHash)
	if err != nil {
		return 0, err
	}
	footer, err := residentbirth.EncodeP1RootBoundFooterData(footerData)
	if err != nil {
		return 0, err
	}
	launchBytes, err := residentbirth.P1LaunchJournalCapacityData(len(intent), len(footer))
	if err != nil {
		return 0, err
	}
	// The three future signed wire schemas are DATA here, with exact known
	// A/profile/settings/Env/key links and maximal not-yet-known fixed fields.
	handoff := residentprotocol.ContextData{Generation: footerData.Generation, DebitID: selector, LaunchKeySHA256: footerData.LaunchKeySHA256, ProfileSHA256: inputs.ProfileSHA256, ImageSHA256: inputs.Build.SHA256, ImageBytes: inputs.Build.Size, SettingsSHA256: settingsHash, EnvironmentSHA256: inputs.EnvironmentSHA256, IntentSHA256: selector, PreparedCommitmentSHA256: selector, ServiceJournalBytes: serviceBytes, ServiceJournalSHA256: selector, FooterBytes: int64(len(footer)), FooterSHA256: selector, LaunchJournalBytes: int64(launchBytes), LaunchJournalSHA256: selector}
	if _, err = p1HandoffCapacityData(handoff); err != nil {
		return 0, err
	}
	raw, err := json.Marshal(bootstrap)
	if err != nil {
		return 0, err
	}
	frame, err := residentrefs.EncodeFrameData(raw)
	if err != nil {
		return 0, err
	}
	return len(frame), nil
}
