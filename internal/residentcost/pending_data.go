// Package residentcost checks bounded cost DATA. It never issues an original
// grant, native claim, funded view, readiness or resource-release authority.
package residentcost

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

const PendingStage = "pending-original-root-proof-v1"
const MaxLedgerBytes = 64 << 10

var ErrInvalid = errors.New("invalid, unsupported or unquantified pending startup cost data")

var pendingTerms = [...]string{
	"original-custody-and-entry", "bounded-funding-input", "service-birth-and-rootbound",
	"inherited-image-and-fence", "root-four-phase-protocol", "worker-native-issuer",
	"official-one-connection-sql", "database-wal-and-publication",
	"manager-initialization-and-observers", "same-server-auth-and-status", "partial-unknown-retention",
}
var pendingUnknowns = [...]string{
	"go-runtime-gc-stack-allocator-mapped-image", "official-connector-connect-opener-driver-rows-sqlite-native",
	"native-reference-lock-file-poller-timer-and-validation-tails", "manager-helper-observer-and-server-status-retention",
	"retained-partial-abnormal-startup-operations", "pending-database-wal-shm-and-publication-growth",
}

func PendingTermsData() []string         { return append([]string(nil), pendingTerms[:]...) }
func PendingUnknownScopesData() []string { return append([]string(nil), pendingUnknowns[:]...) }

type BoundsData struct {
	Tasks, Descriptors, Children, Sockets, NativePending uint64
	IOBytes, RetainedReceiptBytes                        uint64
}
type ResourcesData struct {
	CPU          float64
	Memory, Disk int64
}
type SourceTermData struct {
	Scope, Owner, Derivation, Lifetime    string
	Cardinality                           uint64
	PerInstance                           BoundsData
	BackingMemoryBytes, RetainedDiskBytes uint64
}
type UnknownCardinalityData struct {
	Scope                    string
	RetainedOriginalAttempts uint64
	Derivation               string
}
type PendingSourceContractData struct {
	Schema               uint32
	Stage                string
	Terms                []SourceTermData
	UnknownCardinalities []UnknownCardinalityData
}
type UnknownAllowanceData struct {
	Scope                    string
	RetainedOriginalAttempts uint64
	PerAttemptResources      ResourcesData
	PerAttemptBounds         BoundsData
	Assumption               string
	Evidence                 []string
}
type PendingLedgerData struct {
	Schema                                                            uint32
	Stage                                                             string
	BuildSHA256, EnvironmentSHA256, ContractSHA256                    string
	InitialPackageSHA256, InitialManifestSHA256, SourceContractSHA256 string
	WholePendingResources                                             ResourcesData
	WholePendingBounds                                                BoundsData
	IncludedLogicalBounds                                             BoundsData
	IncludedLogicalMemoryBytes, IncludedLogicalDiskBytes              uint64
	EnvelopeEvidence                                                  []string
	OverlapExplanation                                                string
	Unknowns                                                          []UnknownAllowanceData
}
type PendingTotalsData struct {
	Resources                            ResourcesData
	Bounds                               BoundsData
	LogicalBounds                        BoundsData
	LogicalMemoryBytes, LogicalDiskBytes uint64
}

func textData(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}
func digestData(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func referencesData(values []string) bool {
	if len(values) == 0 || len(values) > 32 {
		return false
	}
	prior := ""
	for _, v := range values {
		if !textData(v, 128) || v <= prior || strings.ContainsAny(v, "/\\") {
			return false
		}
		prior = v
	}
	return true
}
func addData(a, b uint64) (uint64, error) {
	if a > math.MaxUint64-b {
		return 0, ErrInvalid
	}
	return a + b, nil
}
func multiplyData(a, b uint64) (uint64, error) {
	if b != 0 && a > math.MaxUint64/b {
		return 0, ErrInvalid
	}
	return a * b, nil
}
func vectorData(v BoundsData) [7]uint64 {
	return [7]uint64{v.Tasks, v.Descriptors, v.Children, v.Sockets, v.NativePending, v.IOBytes, v.RetainedReceiptBytes}
}
func boundsData(v [7]uint64) BoundsData { return BoundsData{v[0], v[1], v[2], v[3], v[4], v[5], v[6]} }
func addBoundsData(a, b BoundsData) (BoundsData, error) {
	x, y := vectorData(a), vectorData(b)
	for i := range x {
		v, err := addData(x[i], y[i])
		if err != nil {
			return BoundsData{}, err
		}
		x[i] = v
	}
	return boundsData(x), nil
}
func multiplyBoundsData(a BoundsData, count uint64) (BoundsData, error) {
	x := vectorData(a)
	for i := range x {
		v, err := multiplyData(x[i], count)
		if err != nil {
			return BoundsData{}, err
		}
		x[i] = v
	}
	return boundsData(x), nil
}
func resourceShapeData(r ResourcesData) bool {
	return !math.IsNaN(r.CPU) && !math.IsInf(r.CPU, 0) && r.CPU >= 0 && !(r.CPU == 0 && math.Signbit(r.CPU)) && r.Memory >= 0 && r.Disk >= 0
}
func addResourcesData(a, b ResourcesData) (ResourcesData, error) {
	if !resourceShapeData(a) || !resourceShapeData(b) || a.Memory > math.MaxInt64-b.Memory || a.Disk > math.MaxInt64-b.Disk {
		return ResourcesData{}, ErrInvalid
	}
	cpu := a.CPU + b.CPU
	if a.CPU > 0 && b.CPU > 0 {
		cpu = math.Nextafter(cpu, math.Inf(1))
	}
	r := ResourcesData{CPU: cpu, Memory: a.Memory + b.Memory, Disk: a.Disk + b.Disk}
	if !resourceShapeData(r) {
		return ResourcesData{}, ErrInvalid
	}
	return r, nil
}
func multiplyResourcesData(r ResourcesData, count uint64) (ResourcesData, error) {
	if !resourceShapeData(r) || count == 0 || count > math.MaxUint32 {
		return ResourcesData{}, ErrInvalid
	}
	memory, err := multiplyData(uint64(r.Memory), count)
	if err != nil || memory > math.MaxInt64 {
		return ResourcesData{}, ErrInvalid
	}
	disk, err := multiplyData(uint64(r.Disk), count)
	if err != nil || disk > math.MaxInt64 {
		return ResourcesData{}, ErrInvalid
	}
	cpu := r.CPU * float64(count)
	if r.CPU > 0 && count > 1 {
		cpu = math.Nextafter(cpu, math.Inf(1))
	}
	v := ResourcesData{CPU: cpu, Memory: int64(memory), Disk: int64(disk)}
	if !resourceShapeData(v) {
		return ResourcesData{}, ErrInvalid
	}
	return v, nil
}

// Source descriptions are DATA, even when their fingerprint is valid. The real
// original issuer separately binds the compiled source description and evidence
// to its actual child, current binary, standing charge and completed gate.
func (c PendingSourceContractData) FingerprintData() (string, error) {
	if c.Schema != 1 || c.Stage != PendingStage || len(c.Terms) != len(pendingTerms) || len(c.UnknownCardinalities) != len(pendingUnknowns) {
		return "", ErrInvalid
	}
	for i, t := range c.Terms {
		if (t.PerInstance == (BoundsData{}) && t.BackingMemoryBytes == 0 && t.RetainedDiskBytes == 0) || t.Scope != pendingTerms[i] || !textData(t.Owner, 128) || !textData(t.Derivation, 1024) || t.Cardinality == 0 || t.Cardinality > math.MaxUint32 || (t.Lifetime != "original-core" && t.Lifetime != "one-startup-attempt") {
			return "", ErrInvalid
		}
	}
	for i, u := range c.UnknownCardinalities {
		if u.Scope != pendingUnknowns[i] || u.RetainedOriginalAttempts == 0 || u.RetainedOriginalAttempts > math.MaxUint32 || !textData(u.Derivation, 1024) {
			return "", ErrInvalid
		}
	}
	raw, err := json.Marshal(c)
	if err != nil || len(raw) > MaxLedgerBytes {
		return "", ErrInvalid
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}

// Whole-process envelope and application reservations overlap only by the
// explicit included quantities. Remaining logical reservations and each named
// additional allowance are checked and added exactly once. Arithmetic success
// neither verifies measurement truth nor creates funding or native authority.
func CheckPendingLedgerData(c PendingSourceContractData, l PendingLedgerData) (PendingTotalsData, error) {
	var result PendingTotalsData
	fingerprint, err := c.FingerprintData()
	if err != nil {
		return result, err
	}
	if l.Schema != 1 || l.Stage != PendingStage || l.SourceContractSHA256 != fingerprint || !resourceShapeData(l.WholePendingResources) || l.WholePendingResources.CPU <= 0 || l.WholePendingResources.Memory <= 0 || l.WholePendingResources.Disk <= 0 || !referencesData(l.EnvelopeEvidence) || !textData(l.OverlapExplanation, 1024) || len(l.Unknowns) != len(pendingUnknowns) {
		return result, ErrInvalid
	}
	for _, h := range []string{l.BuildSHA256, l.EnvironmentSHA256, l.ContractSHA256, l.InitialPackageSHA256, l.InitialManifestSHA256} {
		if !digestData(h) {
			return result, ErrInvalid
		}
	}
	for _, term := range c.Terms {
		b, e := multiplyBoundsData(term.PerInstance, term.Cardinality)
		if e != nil {
			return result, e
		}
		result.LogicalBounds, e = addBoundsData(result.LogicalBounds, b)
		if e != nil {
			return result, e
		}
		memory, e := multiplyData(term.BackingMemoryBytes, term.Cardinality)
		if e != nil {
			return result, e
		}
		result.LogicalMemoryBytes, e = addData(result.LogicalMemoryBytes, memory)
		if e != nil {
			return result, e
		}
		disk, e := multiplyData(term.RetainedDiskBytes, term.Cardinality)
		if e != nil {
			return result, e
		}
		result.LogicalDiskBytes, e = addData(result.LogicalDiskBytes, disk)
		if e != nil {
			return result, e
		}
	}
	if l.IncludedLogicalMemoryBytes > result.LogicalMemoryBytes || l.IncludedLogicalMemoryBytes > uint64(l.WholePendingResources.Memory) || l.IncludedLogicalDiskBytes > result.LogicalDiskBytes || l.IncludedLogicalDiskBytes > uint64(l.WholePendingResources.Disk) {
		return result, ErrInvalid
	}
	extraMemory := result.LogicalMemoryBytes - l.IncludedLogicalMemoryBytes
	extraDisk := result.LogicalDiskBytes - l.IncludedLogicalDiskBytes
	if extraMemory > math.MaxInt64 || extraDisk > math.MaxInt64 {
		return result, ErrInvalid
	}
	result.Resources, err = addResourcesData(l.WholePendingResources, ResourcesData{Memory: int64(extraMemory), Disk: int64(extraDisk)})
	if err != nil {
		return result, err
	}
	logical, observed, included := vectorData(result.LogicalBounds), vectorData(l.WholePendingBounds), vectorData(l.IncludedLogicalBounds)
	for i := range logical {
		if included[i] > logical[i] || included[i] > observed[i] {
			return result, ErrInvalid
		}
		observed[i], err = addData(observed[i], logical[i]-included[i])
		if err != nil {
			return result, err
		}
	}
	result.Bounds = boundsData(observed)
	for i, u := range l.Unknowns {
		if u.Scope != pendingUnknowns[i] || u.RetainedOriginalAttempts != c.UnknownCardinalities[i].RetainedOriginalAttempts || !textData(u.Assumption, 1024) || !referencesData(u.Evidence) || !resourceShapeData(u.PerAttemptResources) {
			return result, ErrInvalid
		}
		if u.PerAttemptResources == (ResourcesData{}) {
			return result, ErrInvalid
		}
		// The fixed scope states which unquantified resources cannot silently
		// become zero. Numbers still require actual reviewed evidence.
		if i < 5 && (u.PerAttemptResources.Memory <= 0 || u.PerAttemptResources.CPU <= 0) {
			return result, ErrInvalid
		}
		if (i == 1 || i == 4 || i == 5) && u.PerAttemptResources.Disk <= 0 {
			return result, ErrInvalid
		}
		r, e := multiplyResourcesData(u.PerAttemptResources, u.RetainedOriginalAttempts)
		if e != nil {
			return result, e
		}
		result.Resources, e = addResourcesData(result.Resources, r)
		if e != nil {
			return result, e
		}
		b, e := multiplyBoundsData(u.PerAttemptBounds, u.RetainedOriginalAttempts)
		if e != nil {
			return result, e
		}
		result.Bounds, e = addBoundsData(result.Bounds, b)
		if e != nil {
			return result, e
		}
	}
	counts := vectorData(result.Bounds)
	for i := 0; i < 5; i++ {
		if counts[i] > math.MaxUint32 {
			return result, ErrInvalid
		}
	}
	return result, nil
}

// The actual original funding owner must obtain budget from its same genuine
// standing charge. A caller-supplied resource vector remains DATA, not a grant.
func CheckBudgetData(required PendingTotalsData, resources ResourcesData, bounds BoundsData) error {
	if !resourceShapeData(required.Resources) || !resourceShapeData(resources) || required.Resources.CPU > resources.CPU || required.Resources.Memory > resources.Memory || required.Resources.Disk > resources.Disk {
		return ErrInvalid
	}
	x, y := vectorData(required.Bounds), vectorData(bounds)
	for i := range x {
		if x[i] > y[i] {
			return ErrInvalid
		}
	}
	return nil
}

func DecodePendingLedgerData(raw []byte) (PendingLedgerData, error) {
	var value PendingLedgerData
	if PreflightLedgerJSONData(raw) != nil {
		return value, ErrInvalid
	}
	if len(raw) == 0 || len(raw) > MaxLedgerBytes {
		return value, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&value) != nil || d.Decode(new(any)) != io.EOF {
		return PendingLedgerData{}, ErrInvalid
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(raw, canonical) {
		return PendingLedgerData{}, ErrInvalid
	}
	return value, nil
}
