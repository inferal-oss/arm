package residentservice

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// SYNTHETIC parser/hash/copy DATA only: no native issuer, Plan/Apply, OriginalDebit,
// child, ManagerCharge, actual command Start or resource measurement is invoked.
func executionEnvironmentData() *ExecutionEnvironment {
	return &ExecutionEnvironment{Schema: 1, Entries: []string{"GOMAXPROCS=1", "GOMEMLIMIT=1B"}}
}
func executionProfileData(t *testing.T) (MeasuredProfile, EvidenceBundle) {
	t.Helper()
	profile, evidence := evidenceData(t)
	profile.Schema = 2
	profile.RuntimeEnvironment = executionEnvironmentData()
	evidence.Schema = 2
	evidence.Fixture = "resident-root-standing-p1-v2"
	evidence.RuntimeEnvironment = profile.RuntimeEnvironment.Clone()
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	profile.EvidenceSHA256 = digest(raw)
	return profile, evidence
}
func TestExecutionEnvironmentLegacyCanonicalBytesAndHashUnchanged(t *testing.T) {
	profile := dataRecords(t)[0].Grant.Intent.Profile
	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != legacyProfileDataCanonical || digest(raw) != legacyProfileDataSHA256 {
		t.Fatal("schema1 canonical bytes/hash changed")
	}
	profile.RuntimeEnvironment = executionEnvironmentData()
	if err = profile.Validate(); err == nil {
		t.Fatal("schema1 environment accepted")
	}
	if err = cloneMeasuredProfile(dataRecords(t)[0].Grant.Intent.Profile).RequireP1Environment(); err == nil {
		t.Fatal("schema1 promoted into P1 execution")
	}
}
func TestExecutionEnvironmentStrictSchema2DataAndRefusals(t *testing.T) {
	profile, _ := executionProfileData(t)
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*MeasuredProfile)
	}{
		{"missing", func(p *MeasuredProfile) { p.RuntimeEnvironment = nil }},
		{"wrong-env-schema", func(p *MeasuredProfile) { p.RuntimeEnvironment.Schema = 2 }},
		{"duplicate", func(p *MeasuredProfile) {
			p.RuntimeEnvironment.Entries = append(p.RuntimeEnvironment.Entries, "GOMAXPROCS=1")
		}},
		{"reordered", func(p *MeasuredProfile) {
			p.RuntimeEnvironment.Entries[0], p.RuntimeEnvironment.Entries[1] = p.RuntimeEnvironment.Entries[1], p.RuntimeEnvironment.Entries[0]
		}},
		{"loader-injection", func(p *MeasuredProfile) {
			p.RuntimeEnvironment.Entries = append(p.RuntimeEnvironment.Entries, "LD_PRELOAD=/synthetic/library")
		}},
		{"debug-injection", func(p *MeasuredProfile) {
			p.RuntimeEnvironment.Entries = append([]string{"GODEBUG=synthetic"}, p.RuntimeEnvironment.Entries...)
		}},
		{"empty", func(p *MeasuredProfile) { p.RuntimeEnvironment.Entries[0] = "GOMAXPROCS=" }},
		{"zero", func(p *MeasuredProfile) { p.RuntimeEnvironment.Entries[0] = "GOMAXPROCS=0" }},
		{"noncanonical-number", func(p *MeasuredProfile) { p.RuntimeEnvironment.Entries[0] = "GOMAXPROCS=01" }},
		{"heap-off", func(p *MeasuredProfile) { p.RuntimeEnvironment.Entries[1] = "GOMEMLIMIT=off" }},
		{"overflow", func(p *MeasuredProfile) { p.RuntimeEnvironment.Entries[1] = "GOMEMLIMIT=8EiB" }},
		{"heap-above-claim", func(p *MeasuredProfile) { p.RuntimeEnvironment.Entries[1] = "GOMEMLIMIT=2B" }},
		{"nul", func(p *MeasuredProfile) { p.RuntimeEnvironment.Entries[1] = "GOMEMLIMIT=1B\x00" }},
		{"oversize", func(p *MeasuredProfile) {
			p.RuntimeEnvironment.Entries = append(p.RuntimeEnvironment.Entries, "PATH=/"+strings.Repeat("a", MaxExecutionEnvironmentBytes))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			copy := cloneMeasuredProfile(profile)
			c.mutate(&copy)
			if err := copy.Validate(); err == nil {
				t.Fatal("invalid schema2 data accepted")
			}
		})
	}
}
func TestExecutionEnvironmentEvidenceVersionAndExactValueBinding(t *testing.T) {
	profile, evidence := executionProfileData(t)
	if err := evidence.Validate(profile); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"missing", "mismatch", "legacy", "fixture"} {
		t.Run(name, func(t *testing.T) {
			copy := evidence
			copy.RuntimeEnvironment = evidence.RuntimeEnvironment.Clone()
			switch name {
			case "missing":
				copy.RuntimeEnvironment = nil
			case "mismatch":
				copy.RuntimeEnvironment.Entries[0] = "GOMAXPROCS=2"
			case "legacy":
				copy.Schema = 1
			case "fixture":
				copy.Fixture = "resident-root-standing-v1"
			}
			if err := copy.Validate(profile); err == nil {
				t.Fatal("cross-version or mismatched evidence accepted")
			}
		})
	}
}
func TestExecutionEnvironmentProfileIntentDebitHashSensitivityAndCopyOwnership(t *testing.T) {
	profile, _ := executionProfileData(t)
	intent := dataRecords(t)[0].Grant.Intent
	intent.Profile = cloneMeasuredProfile(profile)
	update := func(i *OperatorIntent) {
		raw, err := json.Marshal(i.Profile)
		if err != nil {
			t.Fatal(err)
		}
		i.Inputs.Profile.SHA256 = digest(raw)
		i.Inputs.Profile.Size = int64(len(raw))
		i.Inputs.Evidence.SHA256 = i.Profile.EvidenceSHA256
	}
	update(&intent)
	first, err := intent.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	firstProfile, err := intent.Profile.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	changed := intent
	changed.Profile = cloneMeasuredProfile(intent.Profile)
	changed.Profile.RuntimeEnvironment.Entries[0] = "GOMAXPROCS=2"
	update(&changed)
	second, err := changed.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	secondProfile, err := changed.Profile.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || firstProfile == secondProfile {
		t.Fatal("environment did not bind hashes")
	}
	// Strict serialized-ledger validation only: no grant is applied or passed
	// to any issuer. Recompute the changed intent's plan hash so a stale debit
	// commitment is the sole broken link, then repair that exact link as control.
	records := dataRecords(t)
	records[0].Grant.Intent = intent
	records[0].Grant.PlanSHA = first
	records[1].Debit.ProfileSHA256 = firstProfile
	if err := ValidateLedger(records); err != nil {
		t.Fatal("schema2 DATA control", err)
	}
	records[0].Grant.Intent = changed
	records[0].Grant.PlanSHA = second
	if err := ValidateLedger(records[:1]); err != nil {
		t.Fatal("changed grant DATA invalid", err)
	}
	if err := ValidateLedger(records); err == nil {
		t.Fatal("stale environment/profile debit accepted")
	}
	records[1].Debit.ProfileSHA256 = secondProfile
	if err := ValidateLedger(records); err != nil {
		t.Fatal("updated debit DATA control", err)
	}
	plan := &OperatorPlan{SHA256: first, Intent: intent}
	record := grantForPlan(plan) // serialized DATA, never applied
	plan.Intent.Profile.RuntimeEnvironment.Entries[0] = "GOMAXPROCS=3"
	if record.Intent.Profile.RuntimeEnvironment.Entries[0] != "GOMAXPROCS=1" {
		t.Fatal("grant DATA aliases plan environment")
	}
	if profile.RuntimeEnvironment.Entries[0] != "GOMAXPROCS=1" {
		t.Fatal("profile clone aliases caller")
	}
}
func TestExecutionEnvironmentActualProcessAndCommandMismatchBeforeEffects(t *testing.T) {
	expected := executionEnvironmentData()
	if err := expected.MatchesCommandData(&exec.Cmd{}); err == nil {
		t.Fatal("nil Env would inherit")
	}
	if err := expected.MatchesCommandData(&exec.Cmd{Env: []string{"GOMAXPROCS=2", "GOMEMLIMIT=1B"}}); err == nil {
		t.Fatal("changed command environment accepted")
	}
	command := &exec.Cmd{Env: append([]string(nil), expected.Entries...)}
	if err := expected.MatchesCommandData(command); err != nil {
		t.Fatal(err)
	}
	// Real test process has the pinned test runner environment, not this synthetic
	// two-key profile. Never print its values or invoke a process/namespace effect.
	if err := expected.MatchesActual(os.Environ()); err == nil {
		t.Fatal("different actual startup environment accepted")
	}
	if err := expected.MatchesActual([]string{"GOMEMLIMIT=1B", "GOMAXPROCS=1"}); err != nil {
		t.Fatal("equivalent actual environment refused")
	}
	if err := expected.MatchesActual([]string{"GOMAXPROCS=1", "GOMEMLIMIT=1B", "GOMAXPROCS=1"}); err == nil {
		t.Fatal("duplicate actual key accepted")
	}
	if command.Process != nil {
		t.Fatal("DATA check started a command")
	}
}

const legacyProfileDataCanonical = `{"schema":1,"build":{"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":1,"goos":"linux","goarch":"amd64","go_build":"synthetic-parser-data","protocol":1},"resources":{"cpu":1,"memory":1,"disk":1},"bounds":{"Tasks":1,"Descriptors":1,"Children":1,"Sockets":1,"NativePending":1,"IOBytes":1,"RetainedReceiptBytes":1},"evidence_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`
const legacyProfileDataSHA256 = "d760f0057f089d365045f1a09fc1e68a8fc21f6fe7d653b7ef914833609e03d6"
