package database

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/model"
)

// Independently reviewed recursive declaration snapshot. Do not generate/update
// this at runtime from model: a producer field/tag/type change MUST fail proof
// until its persistence semantics and reset obligations are explicitly reviewed.
// Strict raw validation still precedes typed decoding; this extra boundary
// prevents evolving model structs from silently widening that raw schema.
var sshProofTypeShapes = map[string]string{
	"model.Resources":            "CPU float64 `json:\"cpu\"`\nDisk int64 `json:\"disk_bytes\"`\nMemory int64 `json:\"memory_bytes\"`",
	"model.ProcessIdentity":      "Namespace string `json:\"namespace\"`\nPGID int `json:\"pgid\"`\nPID int `json:\"pid\"`\nStart string `json:\"start\"`\nUID string `json:\"uid\"`",
	"model.SSHExecutionRequest":  "Account string `json:\"account\"`\nCWD string `json:\"cwd\"`\nCertificate string `json:\"certificate\"`\nCommandSHA256 string `json:\"command_sha256\"`\nKey string `json:\"key\"`\nKind string `json:\"kind\"`\nPair string `json:\"pair\"`\nPublicKeySHA256 string `json:\"public_key_sha256\"`\nReason string `json:\"reason\"`\nResources model.Resources `json:\"resources\"`\nRevision int `json:\"revision\"`\nSource string `json:\"source\"`\nTTL time.Duration `json:\"ttl_ns\"`\nTarget string `json:\"target\"`\nTerminalSHA256 string `json:\"terminal_sha256\"`",
	"model.SSHOperation":         "AdmittedAt time.Time `json:\"admitted_at\"`\nLease string `json:\"lease\"`\nRequest model.SSHExecutionRequest `json:\"request\"`\nSchema int `json:\"schema\"`",
	"model.BoundPoolClaim":       "Path []string `json:\"path\"`\nPool string `json:\"pool\"`\nQuantity int64 `json:\"quantity\"`",
	"model.PoolClaimBinding":     "Authority string `json:\"authority\"`\nBound time.Time `json:\"bound_at\"`\nClaims []model.BoundPoolClaim `json:\"claims\"`\nOwner string `json:\"owner\"`",
	"model.Error":                "Code string `json:\"code\"`\nMessage string `json:\"message\"`",
	"model.BudgetBinding":        "Authority string `json:\"authority\"`\nBound time.Time `json:\"bound_at\"`\nOwner string `json:\"owner\"`\nPath []string `json:\"path\"`\nSelected string `json:\"selected\"`",
	"model.BudgetLimits":         "CPU *float64 `json:\"cpu,omitempty\"`\nDisk *int64 `json:\"disk_bytes,omitempty\"`\nMemory *int64 `json:\"memory_bytes,omitempty\"`\nPools map[string]int64 `json:\"pools,omitempty\"`",
	"model.BudgetPolicyRevision": "ID string `json:\"id\"`\nLimits model.BudgetLimits `json:\"limits\"`\nRevision int64 `json:\"revision\"`",
	"model.PoolPartition":        "Available int64 `json:\"uncarved_available\"`\nChildBacking int64 `json:\"child_backing\"`\nCommitted int64 `json:\"committed\"`\nConfigured int64 `json:\"configured\"`\nDeficit int64 `json:\"deficit\"`\nDirect int64 `json:\"direct_reserved\"`\nHeld int64 `json:\"held_backing\"`\nPendingReturn int64 `json:\"pending_return\"`",
	"model.QueueBlocker":         "Available *json.Number `json:\"available\"`\nBlockingCount int `json:\"blocking_count,omitempty\"`\nChildCount int `json:\"child_count,omitempty\"`\nChildren []string `json:\"blocking_children,omitempty\"`\nCode string `json:\"code\"`\nDetail string `json:\"detail\"`\nLeases []string `json:\"blocking_leases,omitempty\"`\nObservationAt *time.Time `json:\"observation_at,omitempty\"`\nPartition *model.PoolPartition `json:\"partition,omitempty\"`\nRequested *json.Number `json:\"requested\"`\nResource string `json:\"resource,omitempty\"`\nStale bool `json:\"stale,omitempty\"`\nUnit string `json:\"unit,omitempty\"`",
	"model.QueueEstimate":        "Detail string `json:\"detail\"`\nKind string `json:\"kind\"`\nStart *time.Time `json:\"start_at\"`",
	"model.QueueInfo":            "Blockers []model.QueueBlocker `json:\"blockers\"`\nEstimate model.QueueEstimate `json:\"estimate\"`\nPolicy string `json:\"policy\"`\nPosition int `json:\"position\"`\nRejection *model.Error `json:\"rejection,omitempty\"`\nUpdated time.Time `json:\"updated_at\"`",
	"model.PortClaim":            "Address string `json:\"address\"`\nDualStack bool `json:\"dual_stack,omitempty\"`\nMode string `json:\"mode\"`\nName string `json:\"name\"`\nNamespace string `json:\"namespace,omitempty\"`\nNetwork string `json:\"network\"`\nPort int `json:\"port\"`\nRangeEnd int `json:\"range_end,omitempty\"`",
	"model.PoolClaim":            "Authority string `json:\"authority,omitempty\"`\nHost string `json:\"host,omitempty\"`\nPool string `json:\"pool\"`\nQuantity int64 `json:\"quantity\"`\nScope string `json:\"scope,omitempty\"`",
	"model.UsagePolicy":          "Grace time.Duration `json:\"grace_ns\"`\nMode string `json:\"mode\"`\nTolerance float64 `json:\"tolerance\"`\nWarnings int `json:\"warnings\"`\nWindow time.Duration `json:\"window_ns\"`",
	"model.Request":              "Agent string `json:\"agent,omitempty\"`\nBudget string `json:\"budget,omitempty\"`\nHost string `json:\"host\"`\nKey string `json:\"idempotency_key\"`\nKind string `json:\"kind,omitempty\"`\nOriginKey string `json:\"origin_key,omitempty\"`\nPeer string `json:\"origin_peer,omitempty\"`\nPeerCertificate string `json:\"origin_certificate,omitempty\"`\nPeerGrantRevision int `json:\"origin_grant_revision,omitempty\"`\nPools []model.PoolClaim `json:\"pools,omitempty\"`\nPortLease string `json:\"port_lease,omitempty\"`\nPorts []model.PortClaim `json:\"ports,omitempty\"`\nProfile string `json:\"profile,omitempty\"`\nProject string `json:\"project,omitempty\"`\nQueue bool `json:\"queue\"`\nReason string `json:\"reason\"`\nResources model.Resources `json:\"resources\"`\nServiceLease string `json:\"service_lease,omitempty\"`\nStartDeadline *time.Time `json:\"start_deadline_at,omitempty\"`\nStorageLease string `json:\"storage_lease,omitempty\"`\nTTL time.Duration `json:\"ttl_ns\"`\nUsagePolicy *model.UsagePolicy `json:\"usage_policy,omitempty\"`",
	"model.FileIdentity":         "BirthKnown bool `json:\"birth_known\"`\nBornNanoseconds int64 `json:\"born_nanoseconds\"`\nBornSeconds int64 `json:\"born_seconds\"`\nDevice uint64 `json:\"device\"`\nInode uint64 `json:\"inode\"`\nOwner uint32 `json:\"owner\"`",
	"model.StorageRetention":     "At time.Time `json:\"at\"`\nExpires time.Time `json:\"expires_at\"`\nReason string `json:\"reason\"`",
	"model.Artifact":             "AllocatedBefore *int64 `json:\"allocated_before_cleanup\"`\nContentObserved bool `json:\"content_observed\"`\nDeletedEntries int `json:\"deleted_entries\"`\nIdentity *model.FileIdentity `json:\"identity\"`\nLastError string `json:\"last_error,omitempty\"`\nName string `json:\"name\"`\nNamespaceRemoved bool `json:\"namespace_removed\"`\nPath string `json:\"path\"`\nPhase string `json:\"phase\"`\nPolicy string `json:\"policy\"`\nRetention *model.StorageRetention `json:\"retention,omitempty\"`\nUpdated time.Time `json:\"updated_at\"`",
	"model.PortBinding":          "Detail string `json:\"detail,omitempty\"`\nPortClaim model.PortClaim ``\nSocketID string `json:\"socket_id,omitempty\"`\nState string `json:\"state\"`",
	"model.ProcessUsage":         "AccountedMemoryBytes *uint64 `json:\"accounted_memory_bytes\"`\nCPUSeconds *float64 `json:\"cpu_seconds\"`\nDetail string `json:\"detail,omitempty\"`\nIdentity model.ProcessIdentity `json:\"identity\"`\nMemoryMetric string `json:\"memory_metric\"`\nResidentBytes *uint64 `json:\"resident_bytes\"`",
	"model.WorkloadUsage":        "AccountedMemoryBytes *uint64 `json:\"accounted_memory_bytes\"`\nCPUCores *float64 `json:\"cpu_cores\"`\nCPUQuality string `json:\"cpu_quality\"`\nComplete bool `json:\"complete\"`\nDetail string `json:\"detail\"`\nIdentityVerified bool `json:\"identity_verified\"`\nLive bool `json:\"live\"`\nMemberCount int `json:\"member_count\"`\nMembers []model.ProcessUsage `json:\"members\"`\nMemoryMetric string `json:\"memory_metric\"`\nMemoryQuality string `json:\"memory_quality\"`\nMethod string `json:\"method\"`\nObserved time.Time `json:\"observed_at\"`\nRoot model.ProcessIdentity `json:\"root\"`\nScope string `json:\"scope\"`\nStale bool `json:\"stale\"`\nWindow time.Duration `json:\"window_ns\"`",
	"model.WorkloadBinding":      "Adapter string `json:\"adapter\"`\nBuildDirectory string `json:\"build_directory,omitempty\"`\nCWD string `json:\"cwd\"`\nDetail string `json:\"detail,omitempty\"`\nExecutable string `json:\"executable\"`\nGradleVersion string `json:\"gradle_version,omitempty\"`\nHeapBytes int64 `json:\"heap_bytes\"`\nHome string `json:\"home\"`\nJVMArgs []string `json:\"jvm_args\"`\nJavaHome string `json:\"java_home\"`\nMetaspaceBytes int64 `json:\"metaspace_bytes\"`\nProjectCache string `json:\"project_cache,omitempty\"`\nRole string `json:\"role\"`\nServiceLease string `json:\"service_lease,omitempty\"`\nServiceProcess *model.ProcessIdentity `json:\"service_process,omitempty\"`\nServiceStartMillis int64 `json:\"service_start_millis,omitempty\"`\nState string `json:\"state\"`\nWorkerLimit int `json:\"worker_limit\"`",
	"model.Lease":                "Artifact *model.Artifact `json:\"artifact,omitempty\"`\nBudget *model.BudgetBinding `json:\"budget_binding,omitempty\"`\nBudgetError *model.Error `json:\"budget_error,omitempty\"`\nBudgetPolicy []model.BudgetPolicyRevision `json:\"budget_policy,omitempty\"`\nCapabilities []string `json:\"capabilities\"`\nCreated time.Time `json:\"created_at\"`\nEnded *time.Time `json:\"ended_at,omitempty\"`\nExitCode *int `json:\"exit_code,omitempty\"`\nExpires *time.Time `json:\"expires_at,omitempty\"`\nFailure string `json:\"failure,omitempty\"`\nHost string `json:\"host\"`\nID string `json:\"id\"`\nManagedDir string `json:\"managed_dir,omitempty\"`\nOutcome string `json:\"outcome,omitempty\"`\nOwner string `json:\"owner\"`\nOwnerUsername string `json:\"owner_username,omitempty\"`\nPID int `json:\"pid,omitempty\"`\nPoolBinding *model.PoolClaimBinding `json:\"pool_binding,omitempty\"`\nPoolError *model.Error `json:\"pool_error,omitempty\"`\nPorts []model.PortBinding `json:\"ports,omitempty\"`\nProcess *model.ProcessIdentity `json:\"process_identity,omitempty\"`\nQueueInfo *model.QueueInfo `json:\"queue_info,omitempty\"`\nRequest model.Request `json:\"request\"`\nRevision int `json:\"revision\"`\nStarted *time.Time `json:\"started_at,omitempty\"`\nState string `json:\"state\"`\nUsage *model.WorkloadUsage `json:\"usage,omitempty\"`\nWorkload *model.WorkloadBinding `json:\"workload,omitempty\"`",
	"model.SSHLookup":            "Found bool `json:\"found\"`\nLease *model.Lease `json:\"lease,omitempty\"`\nOperation *model.SSHOperation `json:\"operation,omitempty\"`",
}

func checkSSHProofTypeShapes() error {
	return checkSSHProofTypes(sshProofTypeShapes, reflect.TypeOf(model.Resources{}), reflect.TypeOf(model.ProcessIdentity{}), reflect.TypeOf(model.SSHLookup{}))
}
func checkSSHProofTypes(declared map[string]string, roots ...reflect.Type) error {
	seen := map[reflect.Type]bool{}
	var visit func(reflect.Type) error
	visit = func(t reflect.Type) error {
		if seen[t] {
			return nil
		}
		seen[t] = true
		// These standard-library scalars retain their explicit encoding/json wire
		// semantics. No json.RawMessage/interface/unchecked payload escape exists.
		if t == reflect.TypeOf(time.Time{}) || t == reflect.TypeOf(json.Number("")) {
			return nil
		}
		if t == reflect.TypeOf(json.RawMessage{}) {
			return fmt.Errorf("SSH proof raw payload has no closed independent shape")
		}
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			return visit(t.Elem())
		case reflect.Map:
			if err := visit(t.Key()); err != nil {
				return err
			}
			return visit(t.Elem())
		case reflect.Struct:
			fields := make([]string, 0, t.NumField())
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				fields = append(fields, f.Name+" "+f.Type.String()+" `"+string(f.Tag)+"`")
			}
			sort.Strings(fields)
			expected, ok := declared[t.String()]
			if !ok || expected != strings.Join(fields, "\n") {
				return fmt.Errorf("SSH proof producer type %s changed without independent schema review", t)
			}
			for i := 0; i < t.NumField(); i++ {
				if err := visit(t.Field(i).Type); err != nil {
					return err
				}
			}
		case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		default:
			return fmt.Errorf("SSH proof producer type %s has no independently reviewed shape", t)
		}
		return nil
	}
	for _, t := range roots {
		if err := visit(t); err != nil {
			return err
		}
	}
	return nil
}
