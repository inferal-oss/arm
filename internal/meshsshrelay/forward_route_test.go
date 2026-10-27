package meshsshrelay

import (
	"getarm.org/internal/model"
	"strings"
	"testing"
	"time"
)

func TestForwardRouteBindsServiceAndRefusesSSHClassConfusion(t *testing.T) {
	const zero64 = "0000000000000000000000000000000000000000000000000000000000000000"
	source, relay, target := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
	o := Open{Workspace: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Route: strings.Repeat("3", 32), Source: source, Target: target, SourceGeneration: 1, TargetGeneration: 1, DirectoryRevision: 1,
		DirectoryPin: strings.Repeat("4", 64), DirectorySPKI: strings.Repeat("5", 64), SourcePin: strings.Repeat("6", 64), TargetPin: strings.Repeat("7", 64), SSHHostKeyPin: zero64,
		Service: "web", Path: []string{source, relay, target}, Hop: 1, Class: ClassForward, Expires: time.Now().Add(time.Minute), Pair: strings.Repeat("8", 64), EnrollmentRevision: 1}
	if err := o.Validate(time.Now()); err != nil {
		t.Fatal(err)
	}
	digest, err := o.BindingDigest(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	changed := o
	changed.Service = "admin"
	other, err := changed.BindingDigest(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if digest == other {
		t.Fatal("service substitution did not change endpoint binding")
	}
	changed = o
	changed.Class = ClassSSH
	if changed.Validate(time.Now()) == nil {
		t.Fatal("TCP service accepted under SSH class")
	}
	changed = o
	changed.Path = []string{source, relay, source, target}
	if changed.Validate(time.Now()) == nil {
		t.Fatal("forward loop admitted")
	}
	changed = o
	changed.Service = "127.0.0.1:22"
	if changed.Validate(time.Now()) == nil {
		t.Fatal("requester-selected target address admitted")
	}
	grant := RelayGrant{Workspace: o.Workspace, Generation: o.Generation, Local: relay, Revision: 1, SSH: true, Transit: model.Resources{CPU: .01, Memory: 4 << 20}, BytesPerSecond: 1 << 20, Expires: time.Now().Add(time.Hour)}
	if grant.Authorize(time.Now(), o) == nil {
		t.Fatal("SSH relay grant enabled arbitrary TCP forwarding")
	}
	grant.SSH = false
	grant.Transport = true
	if err := grant.Authorize(time.Now(), o); (err == nil) != ForwardReviewEnabled {
		t.Fatalf("generic transport authority did not match development review fence: %v", err)
	}
	// A generic grant must also stay closed on the existing SSH carrier until
	// review; merely disabling the separate TCP URL would leave this widening.
	changed = o
	changed.Class, changed.Service = ClassSSH, ""
	changed.SSHHostKeyPin = strings.Repeat("9", 64)
	if err := grant.Authorize(time.Now(), changed); (err == nil) != ForwardReviewEnabled {
		t.Fatalf("generic SSH transport bypassed development review fence: %v", err)
	}
}
