package server

import (
	"context"
	"strings"
	"testing"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/model"
	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/residentservice"
)

// PURE pending/data controls. Never seal a live descriptor or open native/SQL/
// image/worker state. Cancellation occurs before the first fcntl in the test PID.
func TestOriginalChildPendingOwnsAllThirteenBeforeEffectsAndRejectsCopy(t *testing.T) {
	root := &residentChildPending{}
	err := root.initializeOriginal()
	if err != nil {
		t.Fatal(err)
	}
	state := root.refs.InspectOriginal()
	if state.Registered != 13 || state.DelegatedEntries != 13 || state.MaterializedEntries != 0 || !state.RootCloseAuthorityWithdrawn || state.NativeOutcome != "unknown" {
		t.Fatal(state)
	}
	if root.native != nil || root.manager != nil || root.server != nil || root.runtime != nil {
		t.Fatal("pure constructor performed binding")
	}
	if err = root.initializeOriginal(); err != errResidentChildOriginal {
		t.Fatal("initialized root accepted another initialization", err)
	}
	copied := *root
	if err = copied.initializeOriginal(); err != errResidentChildOriginal {
		t.Fatal("copied root accepted initialization", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = copied.prepareOriginalBindings(ctx); err != errResidentChildOriginal {
		t.Fatal("copied root accepted", err)
	}
	if root.prepareTried {
		t.Fatal("copy changed original root")
	}
	if err = root.prepareOriginalBindings(ctx); err != context.Canceled {
		t.Fatal(err)
	}
	state = root.refs.InspectOriginal()
	if state.MaterializedEntries != 0 || state.Sealed || !state.RootCloseAuthorityWithdrawn || root.bindingsPrepared || root.firstError != context.Canceled {
		t.Fatal(state)
	}
	if root.native != nil || root.manager != nil || root.server != nil || root.bootstrapCall != nil {
		t.Fatal("cancelled root opened original state")
	}
}
func TestP1BootstrapWorstCaseCapacityRefusesOversizeBeforeNamespaceEffects(t *testing.T) {
	inputs := residentservice.P1LaunchInputsData{Home: "/synthetic", Build: residentservice.Build{SHA256: strings.Repeat("a", 64), Size: 1, GOOS: "linux", GOARCH: "amd64", GoBuild: "synthetic-parser-data", Protocol: 1}, ProfileSHA256: strings.Repeat("b", 64), Environment: []string{"GOMAXPROCS=1", "GOMEMLIMIT=1B"}, GrantJournalBytes: 1}
	settings := daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0"}
	size, err := p1BootstrapCapacityData(inputs, settings, "synthetic-data-key")
	if err != nil || size <= 36 || size > residentrefs.MaxWireBytes {
		t.Fatal(size, err)
	}
	inputs.Home = "/" + strings.Repeat("a", 4095)
	if _, err = p1BootstrapCapacityData(inputs, settings, "synthetic-data-key"); err == nil {
		t.Fatal("oversized capacity accepted")
	}
}
