package residentservice

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"path/filepath"
	"strings"
	"time"
)

// These are bounded document/protocol limits, not resource reservations or
// assertions that a measured maximum is an enforced future upper bound.
const (
	MaxProfileBytes          = MaxPayloadBytes
	MaxEvidenceBytes         = 1 << 20
	MaxEvidenceArtifacts     = 32
	MaxEvidenceArtifactBytes = 64 << 10
	MaxEvidenceRawBytes      = 512 << 10
)

type OperatorFileInput struct {
	Path   string   `json:"path"`
	ID     NativeID `json:"native_id"`
	Size   int64    `json:"size"`
	SHA256 string   `json:"sha256"`
}
type OperatorInputs struct {
	Profile  OperatorFileInput `json:"profile"`
	Evidence OperatorFileInput `json:"evidence"`
}

func validInputPath(path string) bool {
	return boundedText(path, 4096) && filepath.IsAbs(path) && filepath.Clean(path) == path && path != "/" && len(strings.Split(strings.TrimPrefix(path, "/"), "/")) <= 64
}
func (i OperatorInputs) Validate() error {
	for _, item := range []struct {
		f     OperatorFileInput
		limit int64
	}{{i.Profile, MaxProfileBytes}, {i.Evidence, MaxEvidenceBytes}} {
		if !validInputPath(item.f.Path) || item.f.ID.Inode == 0 || item.f.Size <= 0 || item.f.Size > item.limit || !validDigest(item.f.SHA256) {
			return ErrInvalid
		}
	}
	return nil
}

type MeasurementArtifact struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Raw    []byte `json:"raw"` // bounded actual retained artifact bytes, private input only
}
type MeasurementCoverage struct {
	Workload  string   `json:"workload"`
	Method    string   `json:"method"`
	Artifacts []string `json:"artifacts"`
}
type MeasurementCostScope struct {
	Scope     string   `json:"scope"`
	Method    string   `json:"method"`
	Artifacts []string `json:"artifacts"`
}
type EvidenceBundle struct {
	Schema              uint32                 `json:"schema"`
	Build               Build                  `json:"build"`
	Fixture             string                 `json:"fixture"`
	FixtureSourceSHA256 string                 `json:"fixture_source_sha256"`
	Started             time.Time              `json:"started"`
	Finished            time.Time              `json:"finished"`
	ObservedResources   Resources              `json:"observed_resources"`
	ObservedBounds      Bounds                 `json:"observed_bounds"`
	Coverage            []MeasurementCoverage  `json:"coverage"`
	CostScopes          []MeasurementCostScope `json:"cost_scopes"`
	Unknowns            []string               `json:"unknowns"`
	Artifacts           []MeasurementArtifact  `json:"artifacts"`
	RuntimeEnvironment  *ExecutionEnvironment  `json:"runtime_environment,omitempty"`
}

var requiredStandingWorkloads = [...]string{
	"birth-image-fence", "native-authority", "database-schema-wal-retention",
	"manager-helpers", "stable-ingress", "controller-worker-replacement",
	"supervised-run", "cli-proxy-reporting", "partial-initialization",
}

var requiredStandingCostScopes = [...]string{
	"runtime-native-and-opaque", "retained-partial-unknown-native-work",
	"image-fence-launch-overhead", "database-pool-sqlite-wal",
	"buffers-logs-receipts-retention", "resident-ingress-manager-controller",
}

// Strict canonical documents reject unknown/duplicate keys, trailing objects,
// alternative encodings and silent field loss by exact typed re-encoding.
// Input tooling must emit the canonical JSON; no unknown field is discarded.
func decodeOperatorDocument(raw []byte, limit int, result any) error {
	if len(raw) == 0 || len(raw) > limit {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(result); err != nil {
		return ErrInvalid
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return ErrInvalid
	}
	encoded, err := json.Marshal(result)
	if err != nil || !bytes.Equal(encoded, raw) {
		return ErrInvalid
	}
	return nil
}

func (e EvidenceBundle) Validate(profile MeasuredProfile) error {
	if (e.Schema != 1 && e.Schema != 2) || e.Schema != profile.Schema || e.Build != profile.Build || !validDigest(e.FixtureSourceSHA256) || e.Started.IsZero() || !e.Finished.After(e.Started) || e.Finished.Sub(e.Started) > 24*time.Hour {
		return ErrInvalid
	}
	if e.Schema == 1 {
		if e.Fixture != "resident-root-standing-v1" || e.RuntimeEnvironment != nil || profile.RuntimeEnvironment != nil {
			return ErrInvalid
		}
	} else {
		if e.Fixture != "resident-root-standing-p1-v2" || e.RuntimeEnvironment == nil || profile.RuntimeEnvironment == nil || !e.RuntimeEnvironment.Equal(profile.RuntimeEnvironment) {
			return ErrInvalid
		}
		if _, err := e.RuntimeEnvironment.Validate(); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(e)
	if err != nil || len(encoded) > MaxEvidenceBytes {
		return ErrInvalid
	}
	r := e.ObservedResources
	if math.IsNaN(r.CPU) || math.IsInf(r.CPU, 0) || r.CPU <= 0 || r.Memory <= 0 || r.Disk <= 0 || r.CPU > profile.Resources.CPU || r.Memory > profile.Resources.Memory || r.Disk > profile.Resources.Disk {
		return ErrInvalid
	}
	b, cap := e.ObservedBounds, profile.Bounds
	if b.Tasks == 0 || b.Descriptors == 0 || b.Children == 0 || b.Sockets == 0 || b.NativePending == 0 || b.IOBytes == 0 || b.RetainedReceiptBytes == 0 || b.Tasks > cap.Tasks || b.Descriptors > cap.Descriptors || b.Children > cap.Children || b.Sockets > cap.Sockets || b.NativePending > cap.NativePending || b.IOBytes > cap.IOBytes || b.RetainedReceiptBytes > cap.RetainedReceiptBytes {
		return ErrInvalid
	}
	if len(e.Artifacts) == 0 || len(e.Artifacts) > MaxEvidenceArtifacts || len(e.Unknowns) == 0 || len(e.Unknowns) > 16 || len(e.Coverage) != len(requiredStandingWorkloads) || len(e.CostScopes) != len(requiredStandingCostScopes) {
		return ErrInvalid
	}
	for _, unknown := range e.Unknowns {
		if !boundedText(unknown, 1024) {
			return ErrInvalid
		}
	}
	artifacts := make(map[string]bool, len(e.Artifacts))
	var total int64
	previous := ""
	for _, a := range e.Artifacts {
		if !boundedText(a.Name, 128) || a.Name <= previous || strings.ContainsAny(a.Name, "/\\") || (a.Kind != "native-observation" && a.Kind != "owner-high-watermark" && a.Kind != "fixture-transcript") || a.Size <= 0 || a.Size > MaxEvidenceArtifactBytes || int64(len(a.Raw)) != a.Size || !validDigest(a.SHA256) || digest(a.Raw) != a.SHA256 || total > MaxEvidenceRawBytes-a.Size {
			return ErrInvalid
		}
		total += a.Size
		artifacts[a.Name] = false
		previous = a.Name
	}
	for i, coverage := range e.Coverage {
		if coverage.Workload != requiredStandingWorkloads[i] || !boundedText(coverage.Method, 1024) || len(coverage.Artifacts) == 0 || len(coverage.Artifacts) > MaxEvidenceArtifacts {
			return ErrInvalid
		}
		previous = ""
		for _, name := range coverage.Artifacts {
			if _, exists := artifacts[name]; !exists || name <= previous {
				return ErrInvalid
			}
			artifacts[name] = true
			previous = name
		}
	}
	for i, scope := range e.CostScopes {
		if scope.Scope != requiredStandingCostScopes[i] || !boundedText(scope.Method, 1024) || len(scope.Artifacts) == 0 || len(scope.Artifacts) > MaxEvidenceArtifacts {
			return ErrInvalid
		}
		previous = ""
		for _, name := range scope.Artifacts {
			if _, exists := artifacts[name]; !exists || name <= previous {
				return ErrInvalid
			}
			artifacts[name] = true
			previous = name
		}
	}
	for _, used := range artifacts {
		if !used {
			return ErrInvalid
		}
	}
	return nil
}

// Artifact hashes/shape/coverage fields are DATA checks. They do not establish
// measurement truth, physical enforcement, future maxima, native closure or
// operator approval. The exact plan must display these limits before apply.
var standingMeasurementLimits = [...]string{
	"Coverage and measurement assertions are supplied evidence for explicit operator review; document and hash validation does not independently establish their truth.",
	"Observed maxima do not prove future upper bounds. CPU, memory and disk claims are cooperative promises, not kernel-enforced limits.",
	"Opaque runtime, driver, native and partial operations remain unknown and fully funded; no native closure or release credit is inferred.",
	"The standing debit has no automatic expiry, restart adoption, closure or release in this activation cut.",
}
