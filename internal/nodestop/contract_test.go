package nodestop

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
)

// These are hand-authored contract controls, not native authority/closure proof.
func contractFixture(t *testing.T) (*Boundary, Plan, Current, time.Time) {
	t.Helper()
	target := Target{Host: strings.Repeat("1", 32), Certificate: strings.Repeat("a", 64), SPKI: strings.Repeat("b", 64), Owner: "501", Home: "/private/arm-test", Directory: model.FileIdentity{Device: 7, Inode: 9, Owner: 501}, Generation: strings.Repeat("3", 32), Startup: StartupContract}
	b, err := NewBoundary(target)
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{Host: strings.Repeat("2", 32), Certificate: strings.Repeat("c", 64), SPKI: strings.Repeat("d", 64), Pair: strings.Repeat("4", 64), GrantRevision: 1}
	impact := Impact{Complete: true, SHA256: strings.Repeat("5", 64), Obligations: []Obligation{}, Blockers: []Blocker{}, Peers: []string{actor.Host}}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p, err := b.Prepare("stop-one", actor, 7, model.ShutdownRequest{}, "review this private fixture", impact, now)
	if err != nil {
		t.Fatal(err)
	}
	return b, p, Current{Target: target, Actor: actor, NodeStopGranted: true, DesiredRevision: 7, DesiredRunning: true, Impact: impact}, now
}
func application(t *testing.T, p Plan) Apply {
	t.Helper()
	h, e := p.Digest()
	if e != nil {
		t.Fatal(e)
	}
	return Apply{Key: p.Key, ReviewSHA256: h, Plan: p}
}

func TestGrantGenerationEnrollmentAndDesiredStateFences(t *testing.T) {
	b, p, current, now := contractFixture(t)
	admission, err := b.Admit(application(t, p), current, now)
	if err != nil || !b.Owns(admission) {
		t.Fatal("valid contract frame refused", err)
	}
	for _, name := range []string{"permission", "grant-revision", "pair", "source-certificate", "generation", "home-inode", "desired-revision", "desired-stopped"} {
		t.Run(name, func(t *testing.T) {
			c := current
			switch name {
			case "permission":
				c.NodeStopGranted = false
			case "grant-revision":
				c.Actor.GrantRevision++
			case "pair":
				c.Actor.Pair = strings.Repeat("6", 64)
			case "source-certificate":
				c.Actor.Certificate = strings.Repeat("6", 64)
			case "generation":
				c.Target.Generation = strings.Repeat("6", 32)
			case "home-inode":
				c.Target.Directory.Inode++
			case "desired-revision":
				c.DesiredRevision++
			case "desired-stopped":
				c.DesiredRunning = false
			}
			if got, e := b.Admit(application(t, p), c, now); e == nil || b.Owns(got) {
				t.Fatal("changed authority/frame was admitted")
			}
		})
	}
}
func TestReviewHashKeyAndExpiryCannotBorrowApproval(t *testing.T) {
	b, p, c, now := contractFixture(t)
	for _, name := range []string{"reason", "key", "digest", "expired", "clock-before-review"} {
		t.Run(name, func(t *testing.T) {
			q := application(t, p)
			at := now
			switch name {
			case "reason":
				q.Plan.Reason = "unreviewed replacement purpose"
			case "key":
				q.Key = "other-stop"
			case "digest":
				q.ReviewSHA256 = strings.Repeat("f", 64)
			case "expired":
				at = p.Expires
			case "clock-before-review":
				at = now.Add(-time.Nanosecond)
			}
			if got, e := b.Admit(q, c, at); e == nil || b.Owns(got) {
				t.Fatal("borrowed/expired approval admitted")
			}
		})
	}
}
func TestPublicCensusCannotKeepOpaqueDigestWhileErasingWork(t *testing.T) {
	b, p, c, now := contractFixture(t)
	c.Impact.Obligations = []Obligation{{ID: "owned-lease", Kind: "reservation", State: "active", Revision: 1}}
	c.Impact.Outstanding = 1
	// Current private digest is deliberately unchanged. Comparing only that hash
	// would let the request erase the public obligation roster/default refusal.
	if _, err := b.Admit(application(t, p), c, now); err == nil {
		t.Fatal("unreviewed outstanding work erased behind unchanged opaque digest")
	}
	p.Impact = c.Impact
	if _, err := b.Admit(application(t, p), c, now); err == nil {
		t.Fatal("default stop accepted live obligations")
	}
	p.Policy.Wait = true
	// A changed policy needs its own destination preparation and exact review.
	var err error
	p, err = b.Prepare(p.Key, p.Actor, p.DesiredRevision, p.Policy, p.Reason, p.Impact, now.Add(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Admit(application(t, p), c, now.Add(time.Nanosecond)); err != nil {
		t.Fatal("explicit wait contract refused", err)
	}
}
func TestForceDoesNotEraseUnknownUnjoinedOrIncompleteScope(t *testing.T) {
	b, p, c, now := contractFixture(t)
	p.Policy.Force = true
	for _, kind := range []string{"unknown", "unjoined", "namespace", "unsupported-supervisor"} {
		t.Run(kind, func(t *testing.T) {
			q := p
			q.Impact.Blockers = []Blocker{{Class: kind, ID: "retained", Detail: "unresolved fixture evidence"}}
			at := c
			at.Impact = q.Impact
			if _, err := b.Admit(application(t, q), at, now); err == nil {
				t.Fatal("force bypassed retained blocker")
			}
		})
	}
	p.Impact.Complete = false
	c.Impact = p.Impact
	if _, err := b.Admit(application(t, p), c, now); err == nil {
		t.Fatal("incomplete census interpreted as empty safe scope")
	}
}
func TestCapabilityCannotCrossBoundaryOrBeRestoredFromJSON(t *testing.T) {
	b, p, c, now := contractFixture(t)
	a, err := b.Admit(application(t, p), c, now)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewBoundary(c.Target)
	if err != nil {
		t.Fatal(err)
	}
	if other.Owns(a) || b.Owns(Admission{}) {
		t.Fatal("foreign/empty capability accepted")
	}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var restored Admission
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if b.Owns(restored) {
		t.Fatal("wire data reconstructed an in-process admission")
	}
	copy, err := a.Plan()
	if err != nil {
		t.Fatal(err)
	}
	copy.Impact.Peers[0] = strings.Repeat("f", 32)
	again, err := a.Plan()
	if err != nil || again.Impact.Peers[0] != p.Actor.Host {
		t.Fatal("caller mutated retained capability plan", err)
	}
}
func TestPreparedReviewDetachesCallerCollectionsAndRejectsPartialFrames(t *testing.T) {
	b, p, c, now := contractFixture(t)
	original := p.Impact.Peers[0]
	c.Impact.Peers[0] = strings.Repeat("e", 32)
	if p.Impact.Peers[0] != original {
		t.Fatal("prepared review aliases original census")
	}
	p.Policy = model.ShutdownRequest{Wait: true, Timeout: time.Second}
	if _, err := p.Digest(); err != nil {
		t.Fatal("local wait+timeout semantics lost", err)
	}
	p.Policy.Force = true
	if _, err := p.Digest(); err == nil {
		t.Fatal("force+wait accepted")
	}
	_, err := b.Prepare("invalid\nkey", p.Actor, 7, model.ShutdownRequest{}, "purpose", p.Impact, now)
	if err == nil {
		t.Fatal("control character key accepted")
	}
}
