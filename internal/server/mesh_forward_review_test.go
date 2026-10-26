package server

import (
	"context"
	"testing"

	"github.com/yrashk/arm/internal/meshsshrelay"
	"github.com/yrashk/arm/internal/workspace"
)

func TestUnreviewedForwardAuthorityUnavailable(t *testing.T) {
	if meshsshrelay.ForwardReviewEnabled {
		t.Skip("review development build explicitly selects the candidate")
	}
	// An empty service would panic if either path proceeded to store/Manager.
	m := &meshService{}
	if _, err := m.startForwardSource(context.Background(), forwardSourceRequest{}); err == nil {
		t.Fatal("source admission reached unapproved authority")
	}
	if _, err := m.routedSSHAdmission(context.Background(), meshsshrelay.Outer{}, meshsshrelay.Open{Class: meshsshrelay.ClassForward}); err == nil {
		t.Fatal("destination/relay admission reached unapproved TCP authority")
	}
}

func TestForwardSourceAuthorityFreezesEndpointMembership(t *testing.T) {
	original := workspace.RouteAccess{Workspace: "workspace", Generation: "generation", OriginGeneration: 1, DestinationGeneration: 1, Local: workspace.Identity{Host: "source"}, Origin: workspace.Identity{Host: "source", Fingerprint: "original-source"}, Destination: workspace.Identity{Host: "target", Fingerprint: "original-target"}, Directory: workspace.Identity{Host: "directory", Fingerprint: "original-directory"}}
	held := sourceAuthority(original)
	for _, change := range []func(*workspace.RouteAccess){
		func(a *workspace.RouteAccess) { a.Generation = "replacement" },
		func(a *workspace.RouteAccess) { a.OriginGeneration++ },
		func(a *workspace.RouteAccess) { a.DestinationGeneration++ },
		func(a *workspace.RouteAccess) { a.Local.Fingerprint = "replacement" },
		func(a *workspace.RouteAccess) { a.Origin.SPKI = "replacement" },
		func(a *workspace.RouteAccess) { a.Destination.Certificate = "replacement" },
		func(a *workspace.RouteAccess) { a.Directory.Fingerprint = "replacement" },
	} {
		next := original
		change(&next)
		if sourceAuthority(next) == held {
			t.Fatal("changed original source listener identity accepted")
		}
	}
}
