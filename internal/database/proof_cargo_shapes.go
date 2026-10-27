package database

import (
	"getarm.org/internal/cargoprofile"
	"getarm.org/internal/model"
	"reflect"
)

// Independent fixed declaration snapshot of the reviewed consumed graph. Never
// regenerate at runtime: future producer tag/field/type changes fail closed
// until this proof's persistence semantics receive an explicit review.
var cargoProofTypeShapes = map[string]string{
	"cargoprofile.Audit":              "CWD string `json:\"physical_cwd\"`\nCWDIdentity cargoprofile.DirectoryEvidence `json:\"cwd_identity\"`\nCargoHome string `json:\"cargo_home\"`\nFiles []cargoprofile.FileEvidence `json:\"files\"`\nSettings []cargoprofile.Setting `json:\"regulated_settings\"`",
	"cargoprofile.BirthStep":          "Born *model.FileIdentity `json:\"born,omitempty\"`\nPath string `json:\"path\"`\nStep int `json:\"step\"`",
	"cargoprofile.Command":            "Arguments []string `json:\"arguments\"`\nExecutable string `json:\"executable_selector\"`\nJobs int `json:\"jobs\"`\nManifest string `json:\"manifest_path,omitempty\"`\nOriginal []string `json:\"original\"`\nSubcommand string `json:\"subcommand\"`",
	"cargoprofile.DirectoryEvidence":  "Device uint64 `json:\"device\"`\nInode uint64 `json:\"inode\"`\nMode uint32 `json:\"mode\"`\nOwner uint32 `json:\"owner\"`",
	"cargoprofile.ExecutableEvidence": "Device uint64 `json:\"device\"`\nInode uint64 `json:\"inode\"`\nMode uint32 `json:\"mode\"`\nModified time.Time `json:\"modified_at\"`\nOwner uint32 `json:\"owner\"`\nPath string `json:\"path\"`\nSHA256 string `json:\"sha256\"`\nSize int64 `json:\"size\"`",
	"cargoprofile.FileEvidence":       "Device uint64 `json:\"device,omitempty\"`\nIgnored bool `json:\"ignored\"`\nInode uint64 `json:\"inode,omitempty\"`\nMode uint32 `json:\"mode,omitempty\"`\nOwner uint32 `json:\"owner,omitempty\"`\nPath string `json:\"path\"`\nPresent bool `json:\"present\"`\nSHA256 string `json:\"sha256,omitempty\"`\nSize int64 `json:\"size,omitempty\"`",
	"cargoprofile.HelperFinished":     "At time.Time `json:\"at\"`\nCode int `json:\"code\"`\nCommandStarted bool `json:\"command_started\"`\nFailure string `json:\"failure,omitempty\"`",
	"cargoprofile.HelperRuntime":      "Protocol int `json:\"protocol\"`\nSHA256 string `json:\"sha256\"`\nSize int64 `json:\"size\"`\nVersion string `json:\"version\"`",
	"cargoprofile.HelperStarted":      "At time.Time `json:\"at\"`\nProcess model.ProcessIdentity `json:\"process\"`",
	"cargoprofile.OwnedDirectory":     "Identity model.FileIdentity `json:\"identity\"`\nName string `json:\"name\"`\nPath string `json:\"path\"`",
	"cargoprofile.OwnedPaths":         "AllocationIdentity model.FileIdentity `json:\"allocation_identity\"`\nAllocationPath string `json:\"allocation_path\"`\nBuild cargoprofile.OwnedDirectory `json:\"build\"`\nControl cargoprofile.OwnedDirectory `json:\"control\"`\nRoot cargoprofile.OwnedDirectory `json:\"root\"`\nTarget cargoprofile.OwnedDirectory `json:\"target\"`",
	"cargoprofile.Plan":               "Arguments []string `json:\"effective_arguments\"`\nEnvironment []string `json:\"-\"`\nJobs int `json:\"jobs\"`\nMetadata []string `json:\"metadata_arguments\"`\nPolicy []uint8 `json:\"-\"`\nPolicySHA256 string `json:\"policy_sha256\"`\nRoots cargoprofile.Roots `json:\"roots\"`",
	"cargoprofile.Prepared":           "At time.Time `json:\"prepared_at\"`\nAudit cargoprofile.Audit `json:\"audit\"`\nCommand cargoprofile.Command `json:\"command\"`\nOwned cargoprofile.OwnedPaths `json:\"owned\"`\nPlan cargoprofile.Plan `json:\"plan\"`\nPolicy cargoprofile.FileEvidence `json:\"policy\"`\nSchema int `json:\"schema\"`\nSource cargoprofile.SourceEvidence `json:\"source\"`\nTools cargoprofile.Toolchain `json:\"tools\"`",
	"cargoprofile.Roots":              "Build string `json:\"build\"`\nControl string `json:\"control\"`\nTarget string `json:\"target\"`",
	"cargoprofile.Setting":            "Jobs int64 `json:\"jobs,omitempty\"`\nKey string `json:\"key\"`\nOrigin string `json:\"origin\"`\nPath string `json:\"path,omitempty\"`\nTargetArray bool `json:\"target_array,omitempty\"`\nTargets []string `json:\"targets,omitempty\"`",
	"cargoprofile.SourceEvidence":     "Files []cargoprofile.FileEvidence `json:\"files\"`\nMembers []string `json:\"member_manifests\"`\nWorkspace string `json:\"workspace\"`\nWorkspaceIdentity cargoprofile.DirectoryEvidence `json:\"workspace_identity\"`",
	"cargoprofile.Target":             "Origin string `json:\"origin\"`\nSelector string `json:\"selector\"`",
	"cargoprofile.Toolchain":          "BuiltinTargets []string `json:\"builtin_targets\"`\nCargo cargoprofile.ExecutableEvidence `json:\"cargo\"`\nCargoVersion string `json:\"cargo_version\"`\nCheckedTargets []cargoprofile.Target `json:\"checked_targets\"`\nDetail string `json:\"detail\"`\nHost string `json:\"host\"`\nRustc cargoprofile.ExecutableEvidence `json:\"rustc\"`\nRustcVersion string `json:\"rustc_version\"`\nRustdoc cargoprofile.ExecutableEvidence `json:\"rustdoc\"`\nRustdocVersion string `json:\"rustdoc_version\"`\nRustup *cargoprofile.ExecutableEvidence `json:\"rustup,omitempty\"`\nSchema int `json:\"schema\"`\nSelected cargoprofile.ExecutableEvidence `json:\"selected_executable\"`\nSelection string `json:\"selection_method\"`\nSelector string `json:\"selector\"`\nSysroot string `json:\"sysroot\"`\nSysrootIdentity cargoprofile.DirectoryEvidence `json:\"sysroot_identity\"`\nToolchain string `json:\"toolchain,omitempty\"`",
	"model.CargoBinding":              "Adapter string `json:\"adapter\"`\nAuthorizedAt *time.Time `json:\"authorized_at,omitempty\"`\nBuild string `json:\"build,omitempty\"`\nCargoVersion string `json:\"cargo_version,omitempty\"`\nCommandSHA256 string `json:\"command_sha256\"`\nControl string `json:\"control,omitempty\"`\nDetail string `json:\"detail\"`\nJobs int `json:\"jobs\"`\nPreparedSHA256 string `json:\"prepared_sha256,omitempty\"`\nPreparingAt *time.Time `json:\"preparing_at,omitempty\"`\nRustcVersion string `json:\"rustc_version,omitempty\"`\nRustdocVersion string `json:\"rustdoc_version,omitempty\"`\nSchema int `json:\"schema\"`\nStartedAt *time.Time `json:\"command_started_at,omitempty\"`\nState string `json:\"state\"`\nTarget string `json:\"target,omitempty\"`\nToolchain string `json:\"toolchain,omitempty\"`\nWorkspace string `json:\"workspace,omitempty\"`",
	"model.FileIdentity":              "BirthKnown bool `json:\"birth_known\"`\nBornNanoseconds int64 `json:\"born_nanoseconds\"`\nBornSeconds int64 `json:\"born_seconds\"`\nDevice uint64 `json:\"device\"`\nInode uint64 `json:\"inode\"`\nOwner uint32 `json:\"owner\"`",
	"model.ProcessIdentity":           "Namespace string `json:\"namespace\"`\nPGID int `json:\"pgid\"`\nPID int `json:\"pid\"`\nStart string `json:\"start\"`\nUID string `json:\"uid\"`",
}

func checkCargoProofTypes() error {
	return checkSSHProofTypes(cargoProofTypeShapes, reflect.TypeOf(cargoprofile.Prepared{}), reflect.TypeOf(cargoprofile.HelperRuntime{}), reflect.TypeOf(cargoprofile.HelperStarted{}), reflect.TypeOf(cargoprofile.HelperFinished{}), reflect.TypeOf(cargoprofile.BirthStep{}), reflect.TypeOf(model.CargoBinding{}))
}
