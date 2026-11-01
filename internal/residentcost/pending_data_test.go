package residentcost

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// Arithmetic DATA only. These numbers are neither measurements nor an original
// budget, ledger approval, standing charge or a native funding capability.
func pendingArithmeticFixture(t *testing.T) (PendingSourceContractData, PendingLedgerData) {
	t.Helper()
	c := PendingSourceContractData{Schema: 1, Stage: PendingStage}
	for _, name := range pendingTerms {
		c.Terms = append(c.Terms, SourceTermData{Scope: name, Owner: "synthetic-owner", Derivation: "synthetic arithmetic DATA only", Lifetime: "original-core", Cardinality: 1, PerInstance: BoundsData{Descriptors: 1, IOBytes: 16, RetainedReceiptBytes: 16}, BackingMemoryBytes: 16, RetainedDiskBytes: 8})
	}
	for _, name := range pendingUnknowns {
		c.UnknownCardinalities = append(c.UnknownCardinalities, UnknownCardinalityData{Scope: name, RetainedOriginalAttempts: 1, Derivation: "synthetic DATA count"})
	}
	fingerprint, err := c.FingerprintData()
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	l := PendingLedgerData{Schema: 1, Stage: PendingStage, SourceContractSHA256: fingerprint, BuildSHA256: hash, EnvironmentSHA256: hash, ContractSHA256: hash, InitialPackageSHA256: hash, InitialManifestSHA256: hash, WholePendingResources: ResourcesData{CPU: 1, Memory: 256, Disk: 256}, WholePendingBounds: BoundsData{Tasks: 1, Descriptors: 20, Children: 1, Sockets: 1, NativePending: 1, IOBytes: 512, RetainedReceiptBytes: 512}, IncludedLogicalBounds: BoundsData{Descriptors: 11, IOBytes: 176, RetainedReceiptBytes: 176}, IncludedLogicalMemoryBytes: 176, IncludedLogicalDiskBytes: 88, EnvelopeEvidence: []string{"synthetic-envelope"}, OverlapExplanation: "synthetic overlapping reservations; not measurement truth"}
	for _, name := range pendingUnknowns {
		l.Unknowns = append(l.Unknowns, UnknownAllowanceData{Scope: name, RetainedOriginalAttempts: 1, PerAttemptResources: ResourcesData{CPU: 0.125, Memory: 32, Disk: 16}, PerAttemptBounds: BoundsData{NativePending: 1, IOBytes: 8, RetainedReceiptBytes: 8}, Assumption: "synthetic allowance, not actual funding", Evidence: []string{"synthetic-unknown"}})
	}
	return c, l
}

func TestPendingCostDataAddsUnknownsWithoutDoubleCountingOverlap(t *testing.T) {
	c, l := pendingArithmeticFixture(t)
	got, err := CheckPendingLedgerData(c, l)
	if err != nil {
		t.Fatal(err)
	}
	if got.Resources.Memory != 448 || got.Resources.Disk != 352 || got.Resources.CPU < 1.75 || got.Resources.CPU >= 2 || got.Bounds.NativePending != 7 || got.Bounds.Descriptors != 20 || got.Bounds.IOBytes != 560 || got.LogicalMemoryBytes != 176 || got.LogicalDiskBytes != 88 {
		t.Fatal(got)
	}
	if err = CheckBudgetData(got, ResourcesData{CPU: 2, Memory: 448, Disk: 352}, got.Bounds); err != nil {
		t.Fatal(err)
	}
	for _, resources := range []ResourcesData{{CPU: 1, Memory: 448, Disk: 352}, {CPU: 2, Memory: 447, Disk: 352}, {CPU: 2, Memory: 448, Disk: 351}, {CPU: math.NaN(), Memory: 448, Disk: 352}} {
		if CheckBudgetData(got, resources, got.Bounds) == nil {
			t.Fatal("resource vector ignored", resources)
		}
	}
	short := got.Bounds
	short.NativePending--
	if CheckBudgetData(got, ResourcesData{CPU: 2, Memory: 448, Disk: 352}, short) == nil {
		t.Fatal("count bounds ignored")
	}
}

func TestPendingCostDataRejectsOverflowMissingScopesAndUnfundedUnknowns(t *testing.T) {
	for _, name := range []string{"multiply-overflow", "count-overflow", "memory-overflow", "cpu-overflow", "bad-overlap", "duplicate-scope", "missing-unknown", "zero-unknown", "zero-memory-unknown", "changed-cardinality", "runtime-stage"} {
		c, l := pendingArithmeticFixture(t)
		switch name {
		case "multiply-overflow":
			c.Terms[0].BackingMemoryBytes = math.MaxUint64
			c.Terms[0].Cardinality = 2
		case "count-overflow":
			c.Terms[0].PerInstance.Descriptors = math.MaxUint64
		case "memory-overflow":
			l.WholePendingResources.Memory = math.MaxInt64
			l.IncludedLogicalMemoryBytes = 0
		case "cpu-overflow":
			l.WholePendingResources.CPU = math.MaxFloat64
			l.Unknowns[0].PerAttemptResources.CPU = math.MaxFloat64
		case "bad-overlap":
			l.IncludedLogicalMemoryBytes = 177
		case "duplicate-scope":
			c.Terms[1].Scope = c.Terms[0].Scope
		case "missing-unknown":
			l.Unknowns = l.Unknowns[:len(l.Unknowns)-1]
		case "zero-unknown":
			l.Unknowns[0].PerAttemptResources = ResourcesData{}
			l.Unknowns[0].PerAttemptBounds = BoundsData{}
		case "zero-memory-unknown":
			l.Unknowns[0].PerAttemptResources.Memory = 0
		case "changed-cardinality":
			l.Unknowns[0].RetainedOriginalAttempts = 2
		case "runtime-stage":
			l.Stage = "runtime-active-workers"
		}
		// Rebind valid source shape so overflow reaches checked arithmetic,
		// rather than merely exercising an earlier source-hash mismatch.
		if fingerprint, err := c.FingerprintData(); err == nil {
			l.SourceContractSHA256 = fingerprint
		}
		if _, err := CheckPendingLedgerData(c, l); err == nil {
			t.Fatal("invalid arithmetic DATA accepted", name)
		}
	}
}

func TestPendingLedgerDataCanonicalDecoderRejectsDuplicateAndTrailingFields(t *testing.T) {
	_, l := pendingArithmeticFixture(t)
	raw, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodePendingLedgerData(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{append(append([]byte(nil), raw...), ' '), []byte(`{"Schema":1,"Schema":1}`), []byte(`{"Unknown":1}`), append(append([]byte(nil), raw...), []byte(`{}`)...)} {
		if _, err = DecodePendingLedgerData(bad); err == nil {
			t.Fatal("noncanonical ledger DATA accepted")
		}
	}
}

// Invalid floating values and every resource/count endpoint must refuse at
// the public DATA boundary, including values absent from valid JSON numbers.
func TestPendingCostDataRejectsNonfiniteSignedZeroAndEachShortBound(t *testing.T) {
	c, l := pendingArithmeticFixture(t)
	required, err := CheckPendingLedgerData(c, l)
	if err != nil {
		t.Fatal(err)
	}
	for _, cpu := range []float64{-1, math.Copysign(0, -1), math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, side := range []string{"required", "budget"} {
			need, budget := required, ResourcesData{CPU: 4, Memory: 1024, Disk: 1024}
			if side == "required" {
				need.Resources.CPU = cpu
			} else {
				budget.CPU = cpu
			}
			if CheckBudgetData(need, budget, required.Bounds) == nil {
				t.Fatalf("invalid CPU accepted: %s %v", side, cpu)
			}
		}
	}
	for index := 0; index < 7; index++ {
		values := vectorData(required.Bounds)
		if values[index] == 0 {
			t.Fatalf("fixture does not exercise bound %d", index)
		}
		values[index]--
		if CheckBudgetData(required, ResourcesData{CPU: 4, Memory: 1024, Disk: 1024}, boundsData(values)) == nil {
			t.Fatalf("short bound accepted: %d", index)
		}
	}
	for _, side := range []string{"memory", "disk"} {
		budget := ResourcesData{CPU: 4, Memory: 1024, Disk: 1024}
		if side == "memory" {
			budget.Memory = -1
		} else {
			budget.Disk = -1
		}
		if CheckBudgetData(required, budget, required.Bounds) == nil {
			t.Fatalf("negative %s accepted", side)
		}
	}
}

func TestPendingCostDataRejectsEachOverflowingCountAndReceipt(t *testing.T) {
	for index := 0; index < 7; index++ {
		c, l := pendingArithmeticFixture(t)
		values := vectorData(l.WholePendingBounds)
		values[index] = math.MaxUint64
		l.WholePendingBounds = boundsData(values)
		additional := vectorData(l.Unknowns[0].PerAttemptBounds)
		additional[index] = 1
		l.Unknowns[0].PerAttemptBounds = boundsData(additional)
		if _, err := CheckPendingLedgerData(c, l); err == nil {
			t.Fatalf("overflow accepted: %d", index)
		}
	}
	// An included logical quantity equal to both endpoints is valid; one byte
	// beyond either endpoint cannot silently reduce the required envelope.
	c, l := pendingArithmeticFixture(t)
	l.IncludedLogicalDiskBytes++
	if _, err := CheckPendingLedgerData(c, l); err == nil {
		t.Fatal("excess disk overlap accepted")
	}
}
