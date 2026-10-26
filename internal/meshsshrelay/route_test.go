package meshsshrelay

import (
	"strings"
	"testing"
	"time"
)

func h(c string, bytes int) string { return strings.Repeat(c, bytes*2) }

func fixture(now time.Time) (Open, HopAuthority, EndAuthority) {
	a, b, c := h("a", 16), h("b", 16), h("c", 16)
	o := Open{
		Workspace: h("1", 16), Generation: h("2", 16), Route: h("3", 16),
		Source: a, Target: c, SourceGeneration: 2, TargetGeneration: 4, DirectoryRevision: 7,
		DirectoryPin: h("8", 32), DirectorySPKI: h("9", 32),
		SourcePin: h("4", 32), TargetPin: h("5", 32), SSHHostKeyPin: h("6", 32),
		Path: []string{a, b, c}, Hop: 1, Class: ClassSSH, Expires: now.Add(time.Minute),
		Pair: h("7", 32), EnrollmentRevision: 3,
	}
	hop := HopAuthority{Local: b, AuthenticatedPeer: a, Pair: o.Pair, EnrollmentRevision: o.EnrollmentRevision,
		Workspace: o.Workspace, Generation: o.Generation, Source: a, Target: c,
		SourceGeneration: o.SourceGeneration, TargetGeneration: o.TargetGeneration, DirectoryRevision: 8,
		DirectoryPin: o.DirectoryPin, DirectorySPKI: o.DirectorySPKI,
		SourcePin: o.SourcePin, TargetPin: o.TargetPin, RelayEnabled: true, SSHRelayPermit: true,
		ValidUntil: now.Add(30 * time.Second)}
	end := EndAuthority{AuthenticatedSource: a, Local: c, DirectoryPin: o.DirectoryPin, DirectorySPKI: o.DirectorySPKI,
		SourcePin: o.SourcePin, TargetPin: o.TargetPin,
		SSHHostKeyPin: o.SSHHostKeyPin, Workspace: o.Workspace, Generation: o.Generation,
		SourceGeneration: o.SourceGeneration, TargetGeneration: o.TargetGeneration,
		RoutedSSHExec: true, LeaseOwn: true, ServiceEnabled: true, GrantValidUntil: now.Add(time.Minute)}
	return o, hop, end
}

func TestThreeNodeNoDirectRoutePreservesOriginalSource(t *testing.T) {
	now := time.Now().UTC()
	o, first, end := fixture(now)
	if err := o.AuthorizeHop(now, first); err != nil {
		t.Fatal(err)
	}
	digest, err := o.BindingDigest(now)
	if err != nil {
		t.Fatal(err)
	}
	out := NextAuthority{Local: first.Local, DirectPeer: o.Target, Pair: h("8", 32), EnrollmentRevision: 9,
		Workspace: o.Workspace, Generation: o.Generation, SSHRelayPermit: true, ValidUntil: now.Add(time.Minute)}
	if err := o.AuthorizeNext(now, out); err != nil {
		t.Fatal(err)
	}
	next, err := o.Forward(now, out)
	if err != nil {
		t.Fatal(err)
	}
	forwardDigest, err := next.BindingDigest(now)
	if err != nil || forwardDigest != digest {
		t.Fatal("forward changed inner route binding", err)
	}
	if !next.Final() || next.Source != o.Source || next.Target != o.Target || next.SourcePin != o.SourcePin ||
		next.TargetPin != o.TargetPin || next.SSHHostKeyPin != o.SSHHostKeyPin || next.Route != o.Route ||
		next.Expires != o.Expires || next.Class != o.Class {
		t.Fatal("forward changed end-to-end route identity")
	}
	second := first
	second.Local, second.AuthenticatedPeer = o.Target, first.Local
	second.Pair, second.EnrollmentRevision = next.Pair, next.EnrollmentRevision
	second.RelayEnabled = false // final destination need not relay onward
	if err := next.AuthorizeHop(now, second); err != nil {
		t.Fatal(err)
	}
	if err := next.AuthorizeEnd(now, end); err != nil {
		t.Fatal(err)
	}
	if _, err := next.Forward(now, out); err == nil {
		t.Fatal("final hop forwarded")
	}
}

func TestSSHRelayOutgoingNeighborAndBindingChange(t *testing.T) {
	now := time.Now().UTC()
	o, a, _ := fixture(now)
	out := NextAuthority{Local: a.Local, DirectPeer: o.Target, Pair: h("8", 32), EnrollmentRevision: 9,
		Workspace: o.Workspace, Generation: o.Generation, SSHRelayPermit: true, ValidUntil: now.Add(time.Minute)}
	bad := out
	bad.DirectPeer = o.Source
	if err := o.AuthorizeNext(now, bad); err == nil {
		t.Fatal("arbitrary outgoing neighbor admitted")
	}
	bad = out
	bad.SSHRelayPermit = false
	if err := o.AuthorizeNext(now, bad); err == nil {
		t.Fatal("message-only outgoing pair admitted")
	}
	d1, err := o.BindingDigest(now)
	if err != nil {
		t.Fatal(err)
	}
	o.Class = ClassSync
	d2, err := o.BindingDigest(now)
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d2 {
		t.Fatal("class change escaped original endpoint binding")
	}
	o.Class = ClassSSH
	o.DirectoryPin = h("f", 32)
	d3, err := o.BindingDigest(now)
	if err != nil || d3 == d1 {
		t.Fatal("directory pin change escaped original endpoint binding", err)
	}
}

func TestSSHRelayHopDeniesMessageOnlyAndForgedFields(t *testing.T) {
	now := time.Now().UTC()
	o, a, _ := fixture(now)
	check := func(name string, mutate func(*Open, *HopAuthority)) {
		t.Helper()
		q, p := o, a
		q.Path = append([]string(nil), o.Path...)
		mutate(&q, &p)
		if err := q.AuthorizeHop(now, p); err == nil {
			t.Errorf("%s admitted", name)
		}
	}
	check("message-only membership", func(_ *Open, p *HopAuthority) { p.SSHRelayPermit = false })
	check("wrong immediate TLS peer", func(_ *Open, p *HopAuthority) { p.AuthenticatedPeer = o.Target })
	check("wrong pair", func(_ *Open, p *HopAuthority) { p.Pair = h("f", 32) })
	check("stale membership", func(_ *Open, p *HopAuthority) { p.ValidUntil = now })
	check("relay opt out", func(_ *Open, p *HopAuthority) { p.RelayEnabled = false })
	check("source pin swap", func(q *Open, _ *HopAuthority) { q.SourcePin = h("f", 32) })
	check("loop", func(q *Open, _ *HopAuthority) { q.Path[1] = q.Source })
	check("hop reset", func(q *Open, _ *HopAuthority) { q.Hop = 2 })
	check("wrong class", func(q *Open, _ *HopAuthority) { q.Class = "tcp" })
	check("expired", func(q *Open, _ *HopAuthority) { q.Expires = now })
}

func TestSSHRelayEndRequiresOriginalPinnedIdentityAndOwnGrant(t *testing.T) {
	now := time.Now().UTC()
	o, _, end := fixture(now)
	o.Hop = 2
	o.Pair = h("8", 32)
	check := func(name string, mutate func(*EndAuthority)) {
		t.Helper()
		p := end
		mutate(&p)
		if err := o.AuthorizeEnd(now, p); err == nil {
			t.Errorf("%s admitted", name)
		}
	}
	check("relay source substituted", func(p *EndAuthority) { p.AuthenticatedSource = o.Path[1] })
	check("source pin changed", func(p *EndAuthority) { p.SourcePin = h("f", 32) })
	check("host key changed", func(p *EndAuthority) { p.SSHHostKeyPin = h("f", 32) })
	check("no routed execution grant", func(p *EndAuthority) { p.RoutedSSHExec = false })
	check("no lease-own", func(p *EndAuthority) { p.LeaseOwn = false })
	check("service off", func(p *EndAuthority) { p.ServiceEnabled = false })
	check("grant expired", func(p *EndAuthority) { p.GrantValidUntil = now })
}

func TestSSHRelayReplayGuardDeniesReopenAndFullCapacity(t *testing.T) {
	now := time.Now().UTC()
	o, _, _ := fixture(now)
	g, err := NewReplayGate(1)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Admit(now, o); err != nil {
		t.Fatal(err)
	}
	if err = g.Admit(now, o); err == nil {
		t.Fatal("route replay admitted")
	}
	other := o
	other.Route = h("9", 16)
	if err = g.Admit(now, other); err == nil {
		t.Fatal("full replay guard evicted live route")
	}
	other.Expires = now.Add(2 * time.Minute)
	if err = g.Admit(now.Add(time.Minute+time.Nanosecond), other); err != nil {
		t.Fatal(err)
	}
}
