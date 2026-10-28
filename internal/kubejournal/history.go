package kubejournal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"getarm.org/internal/kubeprovision"
	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
)

// HistorySchema is an unused successor format. No existing protected writer,
// Manager entrypoint, dispatcher or proof reader accepts it. A saved decision
// is evidence only; it cannot recreate a live worker's original capability.
const HistorySchema = "arm-kubernetes-stages-v2"
const MaxStageReviews = 8
const MaxStageObservations = 16
const MaxSourceHistory = 128

const (
	EffectRole       = "effect"
	VerificationRole = "verification"
	CarrierRole      = "carrier"
)

type SourceStep struct {
	Role         string          `json:"role"`
	Profile      string          `json:"profile"`
	Purposes     []string        `json:"purposes"`
	Actions      []string        `json:"actions"`
	RequestCount uint8           `json:"request_count"`
	Resources    model.Resources `json:"resources"`
	SourceTTL    time.Duration   `json:"source_ttl_ns"`
	StorageBytes int64           `json:"storage_bytes"`
	StorageTTL   time.Duration   `json:"storage_ttl_ns"`
	PortCount    uint8           `json:"port_count"`
	PortTTL      time.Duration   `json:"port_ttl_ns"`
}

type StageEvidenceRef struct {
	Observed          time.Time `json:"observed_at"`
	Stage             string    `json:"stage"`
	Count             uint8     `json:"observation_count"`
	PrefixSHA256      string    `json:"prefix_sha256"`
	ObservationSHA256 string    `json:"observation_sha256"`
}

type StageReview struct {
	Role               string                `json:"role"`
	Stage              string                `json:"stage"`
	StageKey           string                `json:"stage_key"`
	InputSHA256        string                `json:"input_sha256"`
	Revision           int64                 `json:"revision"`
	SHA256             string                `json:"sha256"`
	Created            time.Time             `json:"created_at"`
	Expires            time.Time             `json:"expires_at"`
	ExpectedGeneration string                `json:"expected_backend_generation"`
	PriorSHA256        string                `json:"prior_result_sha256"`
	PriorObservation   uint8                 `json:"prior_observation_count"`
	Steps              []SourceStep          `json:"source_sequence"`
	ProofInputs        []StageEvidenceRef    `json:"proof_inputs"`
	Installation       *InstallationEvidence `json:"installation,omitempty"`
	Effects            []string              `json:"effects"`
	Decision           string                `json:"decision"`
	Decided            time.Time             `json:"decided_at"`
	DecisionRevision   int64                 `json:"decision_revision"`
}

type HistoryEffect struct {
	Revision     int64     `json:"revision"`
	ReviewSHA256 string    `json:"review_sha256"`
	Attempted    time.Time `json:"attempted_at"`
}

// Observations append rather than replace uncertain or successful effects.
// Verification-only observations can settle an original attempt, never mint
// another effect attempt or erase the original decision and outcome.
type StageObservation struct {
	Revision     int64        `json:"revision"`
	ReviewSHA256 string       `json:"review_sha256"`
	Role         string       `json:"role"`
	Observed     time.Time    `json:"observed_at"`
	Result       EffectResult `json:"result"`
}
type HistoryStage struct {
	Name          string             `json:"name"`
	Key           string             `json:"key"`
	InputSHA256   string             `json:"input_sha256"`
	ExpectedPrior string             `json:"expected_prior_stage"`
	Reviews       []StageReview      `json:"reviews"`
	Effect        *HistoryEffect     `json:"effect,omitempty"`
	Observations  []StageObservation `json:"observations"`
}
type HistorySource struct {
	IntentRevision   int64               `json:"intent_revision"`
	BoundRevision    int64               `json:"bound_revision"`
	FinishedRevision int64               `json:"finished_revision"`
	Stage            string              `json:"stage"`
	ReviewSHA256     string              `json:"review_sha256"`
	Step             uint8               `json:"step"`
	Scope            SourceScope         `json:"scope"`
	Intent           time.Time           `json:"intent_at"`
	Binding          *kubesource.Binding `json:"binding,omitempty"`
	Result           *kubesource.Result  `json:"result,omitempty"`
	Finished         time.Time           `json:"finished_at"`
}
type InstallationEvidence struct {
	Key         string           `json:"key"`
	PlanSHA256  string           `json:"plan_sha256"`
	SourceHost  string           `json:"source_host"`
	Owner       string           `json:"owner"`
	Observation StageEvidenceRef `json:"observation"`
}

type History struct {
	Schema       string                `json:"schema"`
	Kind         string                `json:"kind"`
	Key          string                `json:"key"`
	Owner        string                `json:"owner"`
	SourceHost   string                `json:"source_host"`
	Revision     int64                 `json:"revision"`
	Created      time.Time             `json:"created_at"`
	Updated      time.Time             `json:"updated_at"`
	Plan         kubeprovision.Plan    `json:"plan"`
	Connection   *ConnectionDraft      `json:"connection,omitempty"`
	Installation *InstallationEvidence `json:"installation,omitempty"`
	Phase        string                `json:"phase"`
	Stages       []HistoryStage        `json:"stages"`
	Sources      []HistorySource       `json:"sources"`
}

func historyHash(value any) string {
	return Hash(struct {
		Domain string `json:"domain"`
		Value  any    `json:"value"`
	}{HistorySchema, value})
}
func (r History) InputHash(stage string) string {
	return historyHash(struct {
		Kind, Key, Stage, Plan string
		Connection             *ConnectionDraft
	}{r.Kind, r.Key, stage, r.Plan.SHA256, r.Connection})
}
func historyStageKey(key, stage string) string {
	return historyHash(struct{ Key, Stage string }{key, stage})
}
func stageReviewHash(review StageReview) string {
	review.SHA256, review.Decision, review.Decided, review.DecisionRevision = "", "pending", time.Time{}, 0
	return historyHash(review)
}

func sourceSequence(stage, role string, size int64) ([]SourceStep, error) {
	var profiles []byte
	if role == EffectRole {
		profiles = []byte{profile(stage)}
		if stage == "create" {
			profiles = []byte{kubesource.Bootstrap}
		}
		if stage == "start" {
			profiles = append(profiles, kubesource.Inspect)
		}
	} else if role == VerificationRole {
		if stage == "ship" || stage == "start" {
			profiles = []byte{kubesource.Inspect}
		} else if _, ok := verificationAction(stage); ok {
			profiles = []byte{kubesource.Bootstrap}
		} else {
			return nil, fmt.Errorf("unsupported closed read-only stage action")
		}
	} else if role == CarrierRole && stage == "prove" {
		profiles = []byte{kubesource.Forward}
	} else {
		return nil, fmt.Errorf("unsupported closed stage review role")
	}
	steps := make([]SourceStep, 0, len(profiles))
	for n, p := range profiles {
		s := kubesource.Scope{Stage: p, B: size}
		stepRole := role
		if role == EffectRole && stage == "start" && n == 1 {
			stepRole = VerificationRole
		}
		if p == kubesource.Forward {
			stepRole = CarrierRole
		}
		step := SourceStep{Role: stepRole, Profile: string(p), Purposes: s.Purposes(), Actions: []string{}, Resources: model.Resources{CPU: kubesource.SourceCPU, Memory: kubesource.SourceMemory}, SourceTTL: kubesource.SourceTTL, StorageBytes: s.DiskBytes(), StorageTTL: kubesource.ParentTTL}
		for _, purpose := range step.Purposes {
			if purpose != "payload" {
				step.Actions = append(step.Actions, purpose)
				step.RequestCount++
			}
		}
		if p == kubesource.Bootstrap {
			step.Actions = []string{stage}
			if stage == "create" {
				step.Actions = []string{"create_original_pod"}
			}
			if role == VerificationRole {
				action, _ := verificationAction(stage)
				step.Actions = []string{action}
			}
		}
		if p == kubesource.Forward {
			step.PortCount = 1
			step.PortTTL = kubesource.ParentTTL
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func NewHistory(kind, key, owner, host string, plan kubeprovision.Plan, connection *ConnectionDraft, now time.Time) (History, error) {
	if kind != "kubernetes_install" || connection != nil {
		return History{}, fmt.Errorf("connect requires an exact current installation observation")
	}
	return newHistory(kind, key, owner, host, plan, nil, nil, now)
}

func NewConnectionHistory(key string, installation History, connection ConnectionDraft, now time.Time) (History, error) {
	if installation.Validate() != nil || installation.Kind != "kubernetes_install" || installation.Phase != "completed" || installation.SourceUnresolved() || now.Before(installation.Updated) {
		return History{}, fmt.Errorf("current completed installation evidence required")
	}
	ref, err := installation.CaptureEvidence("start")
	if err != nil {
		return History{}, err
	}
	n, _ := installation.stage("start")
	latest := installation.Stages[n].Observations[len(installation.Stages[n].Observations)-1]
	if !same(latest.Result.Runtime, &connection.Runtime) {
		return History{}, fmt.Errorf("connection runtime differs from current installation observation")
	}
	input := &InstallationEvidence{Key: installation.Key, PlanSHA256: installation.Plan.SHA256, SourceHost: installation.SourceHost, Owner: installation.Owner, Observation: ref}
	return newHistory("kubernetes_connect", key, installation.Owner, installation.SourceHost, installation.Plan, &connection, input, now)
}

func newHistory(kind, key, owner, host string, plan kubeprovision.Plan, connection *ConnectionDraft, installation *InstallationEvidence, now time.Time) (History, error) {
	// Reuse V1's immutable plan/owner/directional-grant validation, not its
	// incompatible single-profile stage lifecycle or approval format.
	base, err := New(kind, key, owner, host, plan, connection, now)
	if err != nil {
		return History{}, err
	}
	r := History{Schema: HistorySchema, Kind: kind, Key: key, Owner: owner, SourceHost: host, Revision: 1, Created: now.UTC(), Updated: now.UTC(), Plan: base.Plan, Connection: base.Connection, Installation: installation, Phase: "pending", Stages: []HistoryStage{}, Sources: []HistorySource{}}
	prior := ""
	for _, stage := range stageRoster(kind) {
		r.Stages = append(r.Stages, HistoryStage{Name: stage, Key: historyStageKey(key, stage), InputSHA256: r.InputHash(stage), ExpectedPrior: prior, Reviews: []StageReview{}, Observations: []StageObservation{}})
		prior = stage
	}
	return r.Snapshot()
}

func (r *History) UnmarshalJSON(raw []byte) error {
	type plain History
	var next plain
	if err := Decode(raw, &next); err != nil {
		return err
	}
	value := History(next)
	if err := value.Validate(); err != nil {
		return err
	}
	*r = value
	return nil
}
func (r History) Snapshot() (History, error) {
	raw, err := json.Marshal(r)
	var next History
	if err == nil {
		err = Decode(raw, &next)
	}
	if err == nil {
		canonical, encodeErr := json.Marshal(next)
		if encodeErr != nil || !bytes.Equal(raw, canonical) {
			err = fmt.Errorf("V2 history canonical round trip changed")
		}
	}
	return next, err
}
