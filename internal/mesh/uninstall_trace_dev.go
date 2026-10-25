//go:build arm_devtrace

package mesh

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
)

// Dev-only selected-root observer. No env/wire activation; no private Config,
// PEM, endpoint token, SSH credential or lease detail can enter this schema.
type UninstallDevBinding struct {
	Key              string             `json:"key"`
	ReviewSHA256     string             `json:"review_sha256"`
	TransferKey      string             `json:"transfer_key"`
	Root             string             `json:"root"`
	BinarySHA256     string             `json:"binary_sha256"`
	BinaryBytes      int64              `json:"binary_bytes"`
	Owner            string             `json:"owner"`
	StopKey          string             `json:"stop_key"`
	StopSHA256       string             `json:"stop_sha256"`
	Target           nodestop.Target    `json:"target"`
	Actor            nodestop.Actor     `json:"actor"`
	RootIdentity     model.FileIdentity `json:"root_identity"`
	ArtifactIdentity model.FileIdentity `json:"artifact_identity"`
	BinaryIdentity   model.FileIdentity `json:"binary_identity"`
	Recovery         string             `json:"recovery"`
}
type UninstallDevReceipt struct {
	Key              string             `json:"key"`
	ReviewSHA256     string             `json:"review_sha256"`
	Phase            string             `json:"phase"`
	RecoveryIdentity model.FileIdentity `json:"recovery_identity"`
}
type UninstallDevSource struct {
	Key            string `json:"key"`
	Kind           string `json:"kind"`
	Phase          string `json:"phase"`
	Revision       int64  `json:"revision"`
	Approved       bool   `json:"approved"`
	ReviewHash     string `json:"review_hash"`
	RequestHash    string `json:"request_hash"`
	Worker         string `json:"worker"`
	ResultPhase    string `json:"result_phase"`
	StopObserved   bool   `json:"stop_observed"`
	ArtifactAbsent bool   `json:"artifact_absent"`
}
type UninstallDevStop struct {
	RecordSchema             string          `json:"record_schema"`
	RecordDesiredRevision    uint64          `json:"record_desired_revision"`
	RecordPhase              string          `json:"record_phase"`
	RecordKey                string          `json:"record_key"`
	RecordDigest             string          `json:"record_digest"`
	RecordTarget             nodestop.Target `json:"record_target"`
	RecordActor              nodestop.Actor  `json:"record_actor"`
	Desired                  nodestop.State  `json:"desired"`
	ServerJoined             bool            `json:"server_joined"`
	OriginalGenerationAbsent bool            `json:"original_generation_absent"`
	Boundary                 string          `json:"boundary"`
}
type UninstallDevOwnership struct {
	Root     model.FileIdentity `json:"root"`
	Artifact model.FileIdentity `json:"artifact"`
	Binary   model.FileIdentity `json:"binary"`
	Homes    []string           `json:"homes"`
	Removing string             `json:"removing"`
}
type UninstallDevResult struct {
	Receipt        *UninstallDevReceipt `json:"receipt"`
	StopObserved   bool                 `json:"stop_observed"`
	ArtifactAbsent bool                 `json:"artifact_absent"`
}
type UninstallDevPeer struct {
	Host        string   `json:"host"`
	Certificate string   `json:"certificate"`
	SPKI        string   `json:"spki"`
	Pair        string   `json:"pair"`
	Revision    int      `json:"revision"`
	State       string   `json:"state"`
	Proven      bool     `json:"proven"`
	Permissions []string `json:"permissions"`
}
type UninstallDevAuthorization struct {
	ReadOnly        bool               `json:"read_only"`
	Host            string             `json:"host"`
	Owner           string             `json:"owner"`
	Certificate     string             `json:"certificate"`
	SPKI            string             `json:"spki"`
	ListenerEnabled bool               `json:"listener_enabled"`
	Peers           []UninstallDevPeer `json:"peers"`
}
type UninstallDevFact struct {
	Authorization *UninstallDevAuthorization `json:"authorization"`

	Result       *UninstallDevResult `json:"result"`
	RequestedKey string              `json:"requested_key"`

	Allocation    *model.FileIdentity    `json:"allocation"`
	Ownership     *UninstallDevOwnership `json:"ownership"`
	Absent        *bool                  `json:"absent"`
	AttemptSource *UninstallDevSource    `json:"attempt_source"`
	SourceError   string                 `json:"source_error"`

	Schema      string               `json:"schema"`
	Ordinal     int                  `json:"ordinal"`
	Action      string               `json:"action"`
	Binding     UninstallDevBinding  `json:"binding"`
	Attempt     *UninstallDevReceipt `json:"attempt"`
	Stored      *UninstallDevReceipt `json:"stored"`
	StoredError string               `json:"stored_error"`
	Source      *UninstallDevSource  `json:"source"`
	Stop        *UninstallDevStop    `json:"stop"`
	Error       string               `json:"error"`
}
type uninstallDevObserver struct {
	mu      sync.Mutex
	rows    []UninstallDevFact
	bytes   int
	failure error
	hook    func(UninstallDevFact)
}

var uninstallDevObservers sync.Map

// DevObserveUninstall is a test-selected root, never a production control.
// The finisher does not join native work: the test must first join its actual
// original calls/Serve owners. It only removes and reads this recorder.
func DevObserveUninstall(root string) (func() ([]UninstallDevFact, error), error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, fmt.Errorf("canonical private install root required")
	}
	o := &uninstallDevObserver{rows: []UninstallDevFact{}}
	if _, loaded := uninstallDevObservers.LoadOrStore(root, o); loaded {
		return nil, fmt.Errorf("original observer already retained")
	}
	return func() ([]UninstallDevFact, error) {
		uninstallDevObservers.CompareAndDelete(root, o)
		o.mu.Lock()
		defer o.mu.Unlock()
		return append([]UninstallDevFact(nil), o.rows...), o.failure
	}, nil
}

// A cut hook may arrange a real private native failure/cancellation in tests;
// its return value cannot change the product verdict or forge an observation.
func DevUninstallCutHook(root string, hook func(UninstallDevFact)) error {
	v, ok := uninstallDevObservers.Load(root)
	if !ok {
		return fmt.Errorf("original recorder required")
	}
	o := v.(*uninstallDevObserver)
	o.mu.Lock()
	o.hook = hook
	o.mu.Unlock()
	return nil
}
func uninstallDevBinding(p UninstallPlan) (UninstallDevBinding, error) {
	hash, e := p.Digest()
	return UninstallDevBinding{Key: p.Key, ReviewSHA256: hash, TransferKey: p.Transfer.Key, Root: p.Transfer.Root,
		BinarySHA256: p.Transfer.SHA256, BinaryBytes: p.Transfer.Size, Owner: p.Transfer.ExpectedUID,
		StopKey: p.Stop.Plan.Key, StopSHA256: p.Stop.SHA256, Target: p.Stop.Plan.Target, Actor: p.Stop.Plan.Actor,
		RootIdentity: p.Ownership.Root, ArtifactIdentity: p.Ownership.Artifact, BinaryIdentity: p.Ownership.Binary,
		Recovery: p.Recovery}, e
}
func uninstallDevReceipt(r UninstallReceipt) *UninstallDevReceipt {
	return &UninstallDevReceipt{Key: r.Plan.Key, ReviewSHA256: r.SHA256, Phase: r.Phase, RecoveryIdentity: r.RecoveryIdentity}
}
func uninstallDevError(e error) string {
	if e != nil {
		return e.Error()
	}
	return ""
}
func uninstallDevBase(action string, p UninstallPlan, resultErr error) (*uninstallDevObserver, UninstallDevFact) {
	v, ok := uninstallDevObservers.Load(p.Transfer.Root)
	if !ok {
		return nil, UninstallDevFact{}
	}
	o := v.(*uninstallDevObserver)
	binding, e := uninstallDevBinding(p)
	if e != nil {
		o.mu.Lock()
		o.failure = errors.Join(o.failure, e)
		o.mu.Unlock()
		return nil, UninstallDevFact{}
	}
	f := UninstallDevFact{Schema: "arm.uninstall.actual/v1", Action: action, Binding: binding, Error: uninstallDevError(resultErr)}
	// Actual bounded journal read, not an expected phase or a synthesized row.
	stored, e := LookupUninstall(p.Transfer, p.Key)
	if e != nil {
		f.StoredError = e.Error()
	} else {
		f.Stored = uninstallDevReceipt(stored)
	}
	return o, f
}
func traceUninstall(action string, p UninstallPlan, attempt *UninstallReceipt, resultErr error) {
	o, f := uninstallDevBase(action, p, resultErr)
	if o == nil {
		return
	}
	if attempt != nil {
		f.Attempt = uninstallDevReceipt(*attempt)
	}
	o.append(f)
}
func uninstallDevSource(source Coordination) (*UninstallDevSource, error) {
	value := &UninstallDevSource{Key: source.Key, Kind: source.Kind, Phase: source.Phase, Revision: source.Revision,
		Approved: source.Approved, ReviewHash: source.ReviewHash, RequestHash: source.Digest, Worker: source.Worker}
	if len(source.Result) > 0 {
		var r UninstallResult
		if e := Decode(source.Result, &r); e != nil {
			return nil, e
		}
		value.ResultPhase = r.Receipt.Phase
		value.StopObserved = r.StopObserved
		value.ArtifactAbsent = r.ArtifactAbsent
	}
	return value, nil
}
func traceUninstallSource(action string, p UninstallPlan, home string, source Coordination, resultErr error) {
	o, f := uninstallDevBase(action, p, resultErr)
	if o == nil {
		return
	}
	var e error
	f.AttemptSource, e = uninstallDevSource(source)
	if e != nil {
		o.mu.Lock()
		o.failure = errors.Join(o.failure, e)
		o.mu.Unlock()
		return
	}
	stored, present, e := ReadCoordination(home, source.Key)
	if e != nil {
		f.SourceError = e.Error()
	} else if !present {
		f.SourceError = "original source receipt missing"
	} else {
		f.Source, e = uninstallDevSource(stored)
		if e != nil {
			f.SourceError = e.Error()
		}
	}
	o.append(f)
}
func traceUninstallAllocation(p UninstallPlan, r UninstallReceipt, file *os.File) {
	o, f := uninstallDevBase("copy-output-created", p, nil)
	if o == nil {
		return
	}
	f.Attempt = uninstallDevReceipt(r)
	identity, e := installIdentity(file)
	if e != nil {
		f.Error = e.Error()
	} else {
		f.Allocation = &identity
	}
	o.append(f)
}
func traceUninstallOwnership(p UninstallPlan, proof InstallOwnership, resultErr error) {
	o, f := uninstallDevBase("ownership-check-return", p, resultErr)
	if o == nil {
		return
	}
	f.Ownership = &UninstallDevOwnership{Root: proof.Root, Artifact: proof.Artifact, Binary: proof.Binary, Homes: append([]string(nil), proof.Homes...), Removing: proof.Removing}
	o.append(f)
}
func traceUninstallAbsence(p UninstallPlan, absent bool, resultErr error) {
	o, f := uninstallDevBase("absence-check-return", p, resultErr)
	if o == nil {
		return
	}
	f.Absent = &absent
	o.append(f)
}

// DevUninstallNativeScope must be called only from the actual original
// WithInspectedNodeStop callback (enter) and after it returns (leave). This
// labels the separately accepted Stop boundary; it does not simulate a Stop or
// manufacture lock/PID/process facts. Positive tests use real Serve joins.
func DevUninstallNativeScope(action string, p UninstallPlan, observation nodestop.Observation, resultErr error) {
	o, f := uninstallDevBase(action, p, resultErr)
	if o == nil {
		return
	}
	f.Stop = &UninstallDevStop{RecordSchema: observation.Record.Schema, RecordDesiredRevision: observation.Record.DesiredRevision, RecordPhase: observation.Record.Phase, RecordKey: observation.Record.Plan.Key,
		RecordDigest: observation.Record.Digest, RecordTarget: observation.Record.Plan.Target, RecordActor: observation.Record.Plan.Actor,
		Desired: observation.Desired, ServerJoined: observation.ServerJoined,
		OriginalGenerationAbsent: observation.OriginalGenerationAbsent, Boundary: "accepted-original-WithInspectedNodeStop"}
	o.append(f)
}
func (o *uninstallDevObserver) append(f UninstallDevFact) {
	o.mu.Lock()
	if o.failure != nil {
		o.mu.Unlock()
		return
	}
	f.Ordinal = len(o.rows) + 1
	raw, e := json.Marshal(f)
	if e == nil && (len(o.rows) >= 128 || o.bytes+len(raw) > 4<<20) {
		e = fmt.Errorf("uninstall trace bound exceeded")
	}
	if e != nil {
		o.failure = e
		o.mu.Unlock()
		return
	}
	var stored UninstallDevFact
	if e = json.Unmarshal(raw, &stored); e != nil {
		o.failure = e
		o.mu.Unlock()
		return
	}
	o.bytes += len(raw)
	o.rows = append(o.rows, stored)
	hook := o.hook
	o.mu.Unlock()
	if hook != nil {
		var detached UninstallDevFact
		if json.Unmarshal(raw, &detached) == nil {
			hook(detached)
		}
	}
}

var uninstallDevHomeBindings sync.Map

// DevBindUninstall links only an already selected root to the exact original
// Stop key; the actual inspector hook checks this registry before any work.
func DevBindUninstall(p UninstallPlan) error {
	v, ok := uninstallDevObservers.Load(p.Transfer.Root)
	if !ok {
		return fmt.Errorf("selected root recorder required")
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return e
	}
	var detached UninstallPlan
	if e = json.Unmarshal(raw, &detached); e != nil {
		return e
	}
	hash, e := p.Digest()
	if e != nil {
		return e
	}
	key := p.Stop.Plan.Target.Home + "\x00" + p.Stop.Plan.Key
	prior, loaded := uninstallDevHomeBindings.LoadOrStore(key, detached)
	if loaded {
		oldHash, err := prior.(UninstallPlan).Digest()
		if err != nil || oldHash != hash {
			return fmt.Errorf("original Stop binding already retained")
		}
	}
	_ = v
	return nil
}
func DevUninstallObservedBoundary(action, home string, observation nodestop.Observation, resultErr error) {
	key := home + "\x00" + observation.Record.Plan.Key
	v, ok := uninstallDevHomeBindings.Load(key)
	if !ok {
		return
	}
	p := v.(UninstallPlan)
	if _, ok = uninstallDevObservers.Load(p.Transfer.Root); !ok {
		return
	}
	DevUninstallNativeScope(action, p, observation, resultErr)
}

func DevUninstallResultObserved(action string, p UninstallPlan, result UninstallResult, resultErr error) {
	o, f := uninstallDevBase(action, p, resultErr)
	if o == nil {
		return
	}
	f.Result = &UninstallDevResult{Receipt: uninstallDevReceipt(result.Receipt), StopObserved: result.StopObserved, ArtifactAbsent: result.ArtifactAbsent}
	o.append(f)
}
func DevUninstallOtherKey(p UninstallPlan, key string, resultErr error) {
	o, f := uninstallDevBase("other-key-call-return", p, resultErr)
	if o == nil {
		return
	}
	f.RequestedKey = key
	o.append(f)
}

func traceUninstallAuthorization(action string, p UninstallPlan, readOnly bool, c Config, resultErr error) {
	o, f := uninstallDevBase(action, p, resultErr)
	if o == nil {
		return
	}
	a := &UninstallDevAuthorization{ReadOnly: readOnly, Host: c.Identity.Host, Owner: c.Identity.Owner,
		Certificate: c.Identity.Fingerprint, SPKI: c.Identity.SPKI, ListenerEnabled: c.ListenerEnabled(), Peers: []UninstallDevPeer{}}
	for _, peer := range c.Peers {
		a.Peers = append(a.Peers, UninstallDevPeer{Host: peer.Identity.Host, Certificate: peer.Identity.Fingerprint,
			SPKI: peer.Identity.SPKI, Pair: peer.Pair, Revision: peer.Revision, State: peer.State, Proven: peer.Proven,
			Permissions: append([]string(nil), peer.Grant.Permissions...)})
	}
	f.Authorization = a
	o.append(f)
}
