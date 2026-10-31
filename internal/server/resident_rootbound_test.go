package server

import (
	"strings"
	"testing"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/model"
	"getarm.org/internal/server/internal/residentservice"
)

func TestRootBoundRequiresActualCompletedPreparedRootBeforeEffects(t *testing.T) {
	r := &residentChildPending{}
	r.self = r
	for _, candidate := range []*residentChildPending{nil, {}, {self: r}, r} {
		if err := candidate.commitOriginalRootBound(); err != errResidentChildOriginal {
			t.Fatal(err)
		}
	}
	if r.rootBoundTried || r.rootBound != nil || r.active != nil || r.service != nil || r.fence != nil || processResidentCoreEntry.call != nil {
		t.Fatal("DATA entered original RootBound/native phase")
	}
}
func TestRootBoundPreflightDataRejectsBeforeDebitCapacityOverflow(t *testing.T) {
	inputs := residentservice.P1LaunchInputsData{Home: "/synthetic", Build: residentservice.Build{SHA256: strings.Repeat("a", 64), Size: 1, GOOS: "linux", GOARCH: "amd64", GoBuild: "synthetic-data", Protocol: 1}, ProfileSHA256: strings.Repeat("b", 64), EnvironmentSHA256: strings.Repeat("c", 64), GrantJournalBytes: 1}
	key := strings.Repeat("\x01", 128)
	settings := daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0"}
	if _, err := p1BootstrapCapacityData(inputs, settings, key); err != nil {
		t.Fatal(err)
	}
	inputs.GrantJournalBytes = residentservice.MaxJournalBytes
	if _, err := p1BootstrapCapacityData(inputs, settings, key); err == nil {
		t.Fatal("service3 journal capacity overflow accepted")
	}
	inputs.GrantJournalBytes = 1
	if _, err := p1BootstrapCapacityData(inputs, settings, key+"x"); err == nil {
		t.Fatal("key overflow accepted before effects")
	}
}
