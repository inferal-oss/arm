package server

import (
	"context"
	"testing"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"getarm.org/internal/server/lifetimeview"
)

func TestOriginalResidentServerRetainedBeforeRejectedClaim(t *testing.T) {
	m, err := manager.NewResidentManagerPending(&lifetimeview.ManagerView{}, daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	o, err := newResidentServerPending(&lifetimeview.ServerView{}, m)
	if err != nil {
		t.Fatal(err)
	}
	actual := o.server
	before := o.inspectOriginal()
	if !before.ServerRetained || before.OpenAttempted || before.ClaimRetained || actual.Manager != nil {
		t.Fatal(before)
	}
	first := o.openOriginal(t.Context())
	if first == nil {
		t.Fatal("zero native Server view accepted")
	}
	after := o.inspectOriginal()
	if o.server != actual || !after.ServerRetained || !after.OpenAttempted || after.Opened || after.OperationPending || after.FirstError != first || after.NativeOutcome != "unknown" {
		t.Fatal(after)
	}
	if err := o.startOriginalManager(); err != first {
		t.Fatal("failed original Server launched Manager", err)
	}
	if m.InspectOriginal().StartAttempted || m.InspectOriginal().OpenAttempted {
		t.Fatal("rejected Server invoked Manager effects")
	}
	if err := actual.validateOriginalIngressProof(context.Background(), &ingressProof{original: &residentServerBinding{owner: o, server: actual}}); err == nil {
		t.Fatal("unbound pending Server accepted ingress")
	}
}

func TestOriginalResidentServerCopiedHolderAndMixedIngressRefuse(t *testing.T) {
	if _, err := newResidentServerPending(nil, nil); err == nil {
		t.Fatal("missing original owners accepted")
	}
	o := &residentServerPending{}
	copy := &residentServerPending{self: o}
	if err := copy.openOriginal(t.Context()); err != errResidentServerOriginal {
		t.Fatal(err)
	}
	legacy := &Server{generation: "fixture"}
	if err := legacy.validateOriginalIngressProof(t.Context(), &ingressProof{generation: "fixture", original: &residentServerBinding{}}); err == nil {
		t.Fatal("legacy ingress accepted resident proof")
	}
}
