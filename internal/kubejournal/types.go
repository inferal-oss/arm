// Package kubejournal holds closed Kubernetes workflow evidence. Its records,
// IDs and prior receipts never create native helper, socket or signal authority.
package kubejournal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"getarm.org/internal/kubeprovision"
	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
)

const Schema = "arm-kubernetes-stages-v1"

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var uidPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,9})$`)
var stageNames = []string{"create", "ship", "start", "local-listener-prepare", "pod-listener-prepare", "local-listener-apply", "pod-listener-apply", "local-pair", "pod-pair", "prove", "local-activate", "pod-activate"}

type SourceScope struct {
	Operation   string `json:"operation"`
	Attempt     string `json:"attempt"`
	Stage       string `json:"stage"`
	BinaryBytes int64  `json:"binary_bytes"`
}

func Scope(s kubesource.Scope) SourceScope {
	return SourceScope{s.Operation, s.Attempt, string(s.Stage), s.B}
}
func (s SourceScope) Native() (kubesource.Scope, error) {
	if len(s.Stage) != 1 {
		return kubesource.Scope{}, fmt.Errorf("invalid source stage")
	}
	v := kubesource.Scope{Operation: s.Operation, Attempt: s.Attempt, Stage: s.Stage[0], B: s.BinaryBytes}
	return v, v.Validate()
}

type Review struct {
	Stage              string          `json:"stage"`
	StageKey           string          `json:"stage_key"`
	InputSHA256        string          `json:"input_sha256"`
	Revision           int64           `json:"revision"`
	SHA256             string          `json:"sha256"`
	Created            time.Time       `json:"created_at"`
	Expires            time.Time       `json:"expires_at"`
	ExpectedGeneration string          `json:"expected_backend_generation"`
	ExpectedPrior      string          `json:"expected_prior_stage"`
	PriorSHA256        string          `json:"prior_result_sha256"`
	SourceProfile      string          `json:"source_profile"`
	SourceResources    model.Resources `json:"source_resources"`
	SourceTTL          time.Duration   `json:"source_ttl_ns"`
	StorageBytes       int64           `json:"storage_bytes"`
	StorageTTL         time.Duration   `json:"storage_ttl_ns"`
	Effects            []string        `json:"effects"`
}
type Stage struct {
	Name               string        `json:"name"`
	Key                string        `json:"key"`
	InputSHA256        string        `json:"input_sha256"`
	State              string        `json:"state"`
	Review             *Review       `json:"review,omitempty"`
	Approved           bool          `json:"approved"`
	ApprovedRevision   int64         `json:"approved_revision"`
	ApprovedSHA256     string        `json:"approved_sha256"`
	ExpectedGeneration string        `json:"expected_backend_generation"`
	ExpectedPrior      string        `json:"expected_prior_stage"`
	Attempted          time.Time     `json:"attempted_at"`
	Confirmed          time.Time     `json:"confirmed_at"`
	Result             *EffectResult `json:"result,omitempty"`
}
type EffectResult struct {
	Pod         *kubeprovision.Pod          `json:"pod,omitempty"`
	Runtime     *kubeprovision.RuntimeProof `json:"runtime,omitempty"`
	Prepared    *PreparedSetup              `json:"prepared,omitempty"`
	Identity    *Identity                   `json:"identity,omitempty"`
	Peer        *Peer                       `json:"peer,omitempty"`
	Proven      bool                        `json:"proven"`
	Activated   bool                        `json:"activated"`
	OwnerClosed bool                        `json:"owner_closed"`
	Unknown     bool                        `json:"unknown"`
}
type SourceAttempt struct {
	Scope    SourceScope         `json:"scope"`
	Stage    string              `json:"stage"`
	Intent   time.Time           `json:"intent_at"`
	Binding  *kubesource.Binding `json:"binding,omitempty"`
	Result   *kubesource.Result  `json:"result,omitempty"`
	Finished time.Time           `json:"finished_at"`
}
type Connector struct {
	ConnectionKey     string    `json:"connection_key"`
	InstallationKey   string    `json:"installation_key"`
	ScopeSHA256       string    `json:"scope_sha256"`
	Phase             string    `json:"phase"`
	PodUID            string    `json:"pod_uid"`
	BackendHost       string    `json:"backend_host"`
	BackendGeneration string    `json:"backend_generation"`
	DestinationUID    string    `json:"destination_uid"`
	MeshIdentity      Identity  `json:"mesh_identity"`
	SSHHostKey        string    `json:"ssh_host_key"`
	SourceIdentity    Identity  `json:"source_identity"`
	Pair              string    `json:"pair"`
	LocalRevision     int       `json:"local_revision"`
	RemoteRevision    int       `json:"remote_revision"`
	Forward           Grant     `json:"source_permissions_on_target"`
	Reverse           Grant     `json:"target_permissions_on_source"`
	ProofDeadline     time.Time `json:"proof_deadline"`
	Revoked           bool      `json:"revoked"`
	ListenerDisabled  bool      `json:"listener_disabled"`
}
type ConnectionDraft struct {
	SourceIdentity Identity                   `json:"source_identity"`
	Forward        Grant                      `json:"source_permissions_on_target"`
	Reverse        Grant                      `json:"target_permissions_on_source"`
	Runtime        kubeprovision.RuntimeProof `json:"runtime"`
	SSHHostKey     string                     `json:"ssh_host_key"`
	DestinationUID string                     `json:"destination_uid"`
}
type Record struct {
	Schema     string             `json:"schema"`
	Kind       string             `json:"kind"`
	Key        string             `json:"key"`
	Owner      string             `json:"owner"`
	SourceHost string             `json:"source_host"`
	Revision   int64              `json:"revision"`
	Created    time.Time          `json:"created_at"`
	Updated    time.Time          `json:"updated_at"`
	Plan       kubeprovision.Plan `json:"plan"`
	Phase      string             `json:"phase"`
	Stages     []Stage            `json:"stages"`
	Sources    []SourceAttempt    `json:"sources"`
	Connection *ConnectionDraft   `json:"connection,omitempty"`
	Connector  *Connector         `json:"connector,omitempty"`
}

func StageKey(key, stage string) string {
	v := sha256.Sum256([]byte(Schema + "\x00" + key + "\x00" + stage))
	return hex.EncodeToString(v[:])
}
func Hash(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	v := sha256.Sum256(raw)
	return hex.EncodeToString(v[:])
}
func reviewHash(r Review) string { r.SHA256 = ""; return Hash(r) }
func KnownStage(name string) bool {
	for _, v := range stageNames {
		if name == v {
			return true
		}
	}
	return false
}
