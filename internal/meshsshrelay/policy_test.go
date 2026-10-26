package meshsshrelay

import (
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
)

func TestDestinationGrantOriginalSourceNotRelayAndStableKeyNamespace(t *testing.T) {
	now := time.Now().UTC()
	o, _, _ := fixture(now)
	o.Hop = 2
	o.Pair = h("8", 32)
	g := DestinationGrant{Workspace: o.Workspace, Generation: o.Generation, Source: o.Source, Target: o.Target,
		SourceGeneration: o.SourceGeneration, TargetGeneration: o.TargetGeneration,
		DirectoryPin: o.DirectoryPin, DirectorySPKI: o.DirectorySPKI,
		SourcePin: o.SourcePin, SourceSPKI: h("7", 32), TargetPin: o.TargetPin, TargetSPKI: h("8", 32),
		SSHHostKeyPin: o.SSHHostKeyPin, Account: "owner", Owner: "501", Revision: 4, SSH: true,
		MaxTTL: time.Hour, Limits: model.Resources{CPU: 2, Memory: 1 << 30}, Expires: now.Add(time.Hour)}
	resources := model.Resources{CPU: 1, Memory: 1 << 20}
	authorize := func(q Open, p DestinationGrant) error {
		return p.Authorize(now, q, o.SourcePin, g.SourceSPKI, o.TargetPin, g.TargetSPKI, o.SSHHostKeyPin, "owner", "501", time.Minute, resources)
	}
	if err := authorize(o, g); err != nil {
		t.Fatal(err)
	}
	virtual, err := g.VirtualPair()
	if err != nil {
		t.Fatal(err)
	}
	q := o
	q.Route = h("f", 16)
	q.Path = []string{o.Source, h("d", 16), o.Target}
	if err := authorize(q, g); err != nil {
		t.Fatal("alternate path lost exact destination grant", err)
	}
	other, err := g.VirtualPair()
	if err != nil || other != virtual {
		t.Fatal("alternate path changed operation key namespace", err)
	}
	relay := g
	relay.Source = o.Path[1]
	relay.SourcePin = h("a", 32)
	if err := authorize(o, relay); err == nil {
		t.Fatal("relay's own grant authorized original source")
	}
	noGrant := g
	noGrant.SSH = false
	if err := authorize(o, noGrant); err == nil {
		t.Fatal("message membership authorized SSH")
	}
	changed := g
	changed.Revision++
	newPair, err := changed.VirtualPair()
	if err != nil || newPair == virtual {
		t.Fatal("grant revision did not fence old operation namespace", err)
	}
	changedDirectory := g
	changedDirectory.DirectoryPin = h("f", 32)
	newPair, err = changedDirectory.VirtualPair()
	if err != nil || newPair == virtual || authorize(o, changedDirectory) == nil {
		t.Fatal("directory pin change retained original SSH authority", err)
	}
	tooLarge := resources
	tooLarge.CPU = 3
	if err := g.Authorize(now, o, o.SourcePin, g.SourceSPKI, o.TargetPin, g.TargetSPKI, o.SSHHostKeyPin, "owner", "501", time.Minute, tooLarge); err == nil {
		t.Fatal("resource ceiling ignored")
	}
}

func TestTransitGrantSeparateFromMessageRelay(t *testing.T) {
	now := time.Now().UTC()
	o, a, _ := fixture(now)
	g := RelayGrant{Workspace: o.Workspace, Generation: o.Generation, Local: a.Local, Revision: 1, SSH: true, Expires: now.Add(time.Minute)}
	if err := g.Authorize(now, o); err != nil {
		t.Fatal(err)
	}
	g.SSH = false
	if err := g.Authorize(now, o); err == nil {
		t.Fatal("SSH relay permitted without opt-in")
	}
	g.Sync = true
	o.Class = ClassSync
	if err := g.Authorize(now, o); err != nil {
		t.Fatal(err)
	}
}
