package meshsshrelay

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
)

func TestReviewedPolicyCommitCASAndNoFollow(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "mesh"), 0700); err != nil {
		t.Fatal(err)
	}
	local, pin := h("a", 16), h("b", 32)
	before, err := ReadPolicy(home, local, pin)
	if err != nil || before.Schema != 0 {
		t.Fatal(before, err)
	}
	makeReview := func() Review {
		r, err := ReviewPolicy(home, local, pin, "grant", "operator reviewed relay opt-in", func(p *Policy) error {
			p.Relays = append(p.Relays, RelayGrant{Workspace: h("1", 16), Generation: h("2", 16), Local: local, Revision: 1, SSH: true,
				Transit: model.Resources{CPU: .05, Memory: 4 << 20}, BytesPerSecond: 64 << 20, Expires: time.Now().Add(time.Hour)})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	review := makeReview()
	stale := makeReview()
	if err := CommitPolicy(review); err != nil {
		t.Fatal(err)
	}
	if err := CommitPolicy(stale); err == nil {
		t.Fatal("stale grant review overwrote committed policy")
	}
	after, err := ReadPolicy(home, local, pin)
	if err != nil || after.Revision != 2 || len(after.Relays) != 1 || len(after.Events) != 1 {
		t.Fatal(after, err)
	}
	if err := os.Remove(filepath.Join(home, "mesh", policyName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "outside"), filepath.Join(home, "mesh", policyName)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPolicy(home, local, pin); err == nil {
		t.Fatal("symlinked policy accepted")
	}
}

func TestDestinationPolicyRoundTripAndRevocation(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "mesh"), 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	o, _, _ := fixture(now)
	local, pin := o.Target, o.TargetPin
	g := DestinationGrant{Workspace: o.Workspace, Generation: o.Generation, Source: o.Source, Target: o.Target,
		SourceGeneration: o.SourceGeneration, TargetGeneration: o.TargetGeneration,
		DirectoryPin: o.DirectoryPin, DirectorySPKI: o.DirectorySPKI,
		SourcePin: o.SourcePin, SourceSPKI: h("7", 32), TargetPin: o.TargetPin, TargetSPKI: h("8", 32),
		SSHHostKeyPin: o.SSHHostKeyPin, Account: "owner", Owner: "501", Revision: 1, SSH: true,
		MaxTTL: time.Hour, Limits: model.Resources{CPU: 1, Memory: 1024}, Expires: now.Add(time.Hour)}
	review, err := ReviewPolicy(home, local, pin, "grant", "exact original source shell grant", func(p *Policy) error {
		p.Destinations = append(p.Destinations, g)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = CommitPolicy(review); err != nil {
		t.Fatal(err)
	}
	p, err := ReadPolicy(home, local, pin)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Destinations) != 1 || p.Destinations[0] != g {
		t.Fatal("exact destination grant changed during persisted round trip")
	}
	o.Hop = 2
	o.Pair = h("8", 32)
	if _, err = p.Destination(o); err != nil {
		t.Fatal(err)
	}
	revoke, err := ReviewPolicy(home, local, pin, "revoke", "withdraw source shell grant", func(p *Policy) error {
		p.Destinations = []DestinationGrant{}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = CommitPolicy(revoke); err != nil {
		t.Fatal(err)
	}
	p, err = ReadPolicy(home, local, pin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Destination(o); err == nil {
		t.Fatal("revoked destination grant admitted")
	}
}

func TestLegacyUnbudgetedRelayCanBeReviewedButCannotForward(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "mesh"), 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	o, access, _ := fixture(now)
	pin := h("b", 32)
	legacy := RelayGrant{Workspace: o.Workspace, Generation: o.Generation, Local: access.Local,
		Revision: 1, SSH: true, Expires: now.Add(time.Hour)}
	review, err := ReviewPolicy(home, access.Local, pin, "grant", "fixture from prior policy format", func(p *Policy) error {
		p.Relays = append(p.Relays, legacy)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = CommitPolicy(review); err != nil {
		t.Fatal(err)
	}
	policy, err := ReadPolicy(home, access.Local, pin)
	if err != nil || len(policy.Relays) != 1 {
		t.Fatal("legacy policy must remain readable for review", err)
	}
	if _, err = policy.Relay(o); err == nil {
		t.Fatal("legacy grant forwarded without a transit budget")
	}
	replacement := legacy
	replacement.Revision++
	replacement.Transit = model.Resources{CPU: .05, Memory: 4 << 20}
	replacement.BytesPerSecond = 64 << 20
	upgrade, err := ReviewPolicy(home, access.Local, pin, "grant", "review explicit transit budget", func(p *Policy) error {
		p.Relays[0] = replacement
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = CommitPolicy(upgrade); err != nil {
		t.Fatal(err)
	}
	policy, err = ReadPolicy(home, access.Local, pin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = policy.Relay(o); err != nil {
		t.Fatal("reviewed replacement grant did not authorize transit", err)
	}
}
